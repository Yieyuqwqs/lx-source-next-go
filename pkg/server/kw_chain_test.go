package server

import (
	"encoding/base64"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/lxsrcs/lx-source-next/pkg/cache"
	cachepolicy "github.com/lxsrcs/lx-source-next/pkg/cache/policy"
	"github.com/lxsrcs/lx-source-next/pkg/config"
	"github.com/lxsrcs/lx-source-next/pkg/source"
	"github.com/lxsrcs/lx-source-next/pkg/source/kw"
)

// kwLyricPayloadB64 与 pkg/source/kw 单测用的是同一份歌词响应样本。
//
// 明文："[ti:test]\n[ar:test]\n[00:01.00]hello\n[00:02.50]world\n"
const kwLyricPayloadB64 = "VFA9Y29udGVudA0KbHJjeD0xDQoNCnic88xw9Q3LMPD2CDQwSapK9vQIC/RzdIqM9HMPNQwOC0uLcMtwisjONnEJzHZzLHerCsgJSw41tEwOM3QK8w0ycfRwCnVKKre1BQA8lBaa"

// newKwLyricUpstream 起一个假的 mlyric 上游；noLyric 时回 TP=none。
//
// 存在的意义是让 pkg/server 的用例永远不碰真网络。
func newKwLyricUpstream(t *testing.T, calls *atomic.Int64, noLyric bool) string {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		if r.URL.Path != "/mobi.s" {
			http.Error(w, "路径不对: "+r.URL.Path, http.StatusNotFound)
			return
		}
		if r.URL.Query().Get("type") != "lyric" {
			http.Error(w, "type 不对", http.StatusBadRequest)
			return
		}
		if noLyric {
			fmt.Fprint(w, "TP=none\n")
			return
		}
		payload, err := base64.StdEncoding.DecodeString(kwLyricPayloadB64)
		if err != nil {
			http.Error(w, "样本损坏", http.StatusInternalServerError)
			return
		}
		w.Write(payload)
	}))
	t.Cleanup(srv.Close)
	return srv.URL
}

const kwChainTOML = `
[Data]
Mode = 1
Type = 0
TTL = 3600
`

var kwTestKey = []byte("12345678")

// newKwMobiApp 伪造一个 **能解密 q 参数** 的 mobi 上游，装配完整 App。
func newKwMobiApp(t *testing.T, calls *atomic.Int64) (*App, *cache.File) {
	t.Helper()

	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		plain, err := kw.DesDecrypt(r.URL.Query().Get("q"), kwTestKey)
		if err != nil {
			http.Error(w, "q 解密失败: "+err.Error(), http.StatusBadRequest)
			return
		}
		v, err := url.ParseQuery(string(plain))
		if err != nil {
			http.Error(w, "q 明文非法", http.StatusBadRequest)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprintf(w, `{"code":200,"data":{"url":"https://kw.invalid/%s/%s.mp3","bitrate":"%s","duration":248}}`,
			v.Get("rid"), v.Get("br"), v.Get("br"))
	}))
	t.Cleanup(upstream.Close)

	cfg := kw.Config{Strategy: kw.StrategyMobi, BaseURL: upstream.URL, Key: kwTestKey}
	// 歌词另有宿主，用例里也指到本地假上游，避免测试依赖真网络
	cfg.LyricBaseURL = newKwLyricUpstream(t, new(atomic.Int64), false)
	return buildKwApp(t, cfg, calls)
}

// newKwAntiApp 伪造一个 antiserver 风格的上游（返回裸链接）。
func newKwAntiApp(t *testing.T, calls *atomic.Int64) (*App, *cache.File) {
	t.Helper()

	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		q := r.URL.Query()
		if q.Get("type") != "convert_url" {
			http.Error(w, "type 不对", http.StatusBadRequest)
			return
		}
		rid := strings.TrimPrefix(q.Get("rid"), "MUSIC_")
		fmt.Fprintf(w, "https://kw.invalid/anti/%s.mp3\n", rid)
	}))
	t.Cleanup(upstream.Close)

	cfg := kw.Config{Strategy: kw.StrategyAnti, BaseURL: upstream.URL}
	cfg.LyricBaseURL = newKwLyricUpstream(t, new(atomic.Int64), false)
	return buildKwApp(t, cfg, calls)
}

func buildKwApp(t *testing.T, cfg kw.Config, _ *atomic.Int64) (*App, *cache.File) {
	t.Helper()

	d, err := config.Parse([]byte(kwChainTOML))
	if err != nil {
		t.Fatalf("解析配置: %v", err)
	}
	holder := config.NewHolder(&config.Snapshot{Dyn: d, LoadedAt: time.Now(), Version: 1})

	fc, err := cache.NewFile(t.TempDir())
	if err != nil {
		t.Fatalf("NewFile: %v", err)
	}
	pol := cachepolicy.NewDynamic(func() cachepolicy.Config {
		dd := holder.Dynamic()
		return cachepolicy.Config{Mode: dd.Data.Mode, TTL: time.Duration(dd.Data.TTL) * time.Second}
	})
	reader := cache.NewReader(fc, pol, cache.ReaderOptions{})

	return New(Deps{
		Config:  holder,
		Cache:   reader,
		Files:   fc,
		Sources: source.Registry{"kw": kw.New(cfg, nil)},
		Options: Options{},
	}), fc
}

// TestKwFullChainMobi 串起「路由 → 缓存 → kw Source(mobi) → 解密校验 → 解析 → 回填」。
func TestKwFullChainMobi(t *testing.T) {
	var calls atomic.Int64
	app, fc := newKwMobiApp(t, &calls)

	rec := get(t, app, "/api/v0/url/kw/123/320k")
	if rec.Code != http.StatusOK {
		t.Fatalf("状态码 = %d，响应 %s", rec.Code, rec.Body.String())
	}
	m := decode(t, rec)
	if data, _ := m["data"].(string); !strings.Contains(data, "kw.invalid/123/320kmp3.mp3") {
		t.Errorf("data = %q", data)
	}
	if ext, _ := m["ext"].(map[string]any); ext["qual"] != "320k" {
		t.Errorf("ext.qual = %v", ext["qual"])
	}
	if calls.Load() != 1 {
		t.Errorf("上游调用 %d 次，want 1", calls.Load())
	}

	// 第二次命中缓存
	if rec := get(t, app, "/api/v0/url/kw/123/320k"); rec.Code != http.StatusOK {
		t.Fatalf("第二次状态码 = %d", rec.Code)
	}
	if calls.Load() != 1 {
		t.Errorf("第二次应命中缓存，上游调用 %d 次", calls.Load())
	}

	// 缓存落在内部命名空间
	p, err := fc.Path("_meta/kw/123-320k.url")
	if err != nil {
		t.Fatalf("Path: %v", err)
	}
	b, err := os.ReadFile(p)
	if err != nil {
		t.Fatalf("读缓存文件: %v", err)
	}
	if !strings.Contains(string(b), "kw.invalid/123/320kmp3.mp3") {
		t.Errorf("缓存内容 = %s", b)
	}
}

// TestKwFullChainAnti 覆盖免密钥策略：默认配置即可工作。
func TestKwFullChainAnti(t *testing.T) {
	var calls atomic.Int64
	app, _ := newKwAntiApp(t, &calls)

	rec := get(t, app, "/api/v0/url/kw/228908/320k")
	if rec.Code != http.StatusOK {
		t.Fatalf("状态码 = %d，响应 %s", rec.Code, rec.Body.String())
	}
	if data, _ := decode(t, rec)["data"].(string); !strings.Contains(data, "kw.invalid/anti/228908.mp3") {
		t.Errorf("data = %q", data)
	}
	// 再来一次命中缓存
	if rec := get(t, app, "/api/v0/url/kw/228908/320k"); rec.Code != http.StatusOK {
		t.Fatalf("第二次状态码 = %d", rec.Code)
	}
	if calls.Load() != 1 {
		t.Errorf("上游调用 %d 次，want 1", calls.Load())
	}
}

// TestKwJmpChain 验证 jmp 直接把 302 Location 指向上游直链。
func TestKwJmpChain(t *testing.T) {
	var calls atomic.Int64
	app, _ := newKwMobiApp(t, &calls)

	rec := get(t, app, "/api/v0/url/kw/999/flac?jmp")
	if rec.Code != http.StatusFound {
		t.Fatalf("状态码 = %d，want 302", rec.Code)
	}
	if loc := rec.Header().Get("Location"); !strings.Contains(loc, "kw.invalid/999/2000kflac.mp3") {
		t.Errorf("Location = %q", loc)
	}
}

// TestKwQualityFallbackChain 验证 master 在 kw 上回落到 flac24bit。
func TestKwQualityFallbackChain(t *testing.T) {
	var calls atomic.Int64
	app, _ := newKwMobiApp(t, &calls)

	rec := get(t, app, "/api/v0/url/kw/1/master")
	if rec.Code != http.StatusOK {
		t.Fatalf("状态码 = %d", rec.Code)
	}
	if ext, _ := decode(t, rec)["ext"].(map[string]any); ext["qual"] != "flac24bit" {
		t.Errorf("回落音质 = %v，want flac24bit", ext["qual"])
	}
}

// TestKwBadStrategyChain 验证策略非法时 503 且不发请求。
//
// 注意：无密钥不再等于「未配置」——新语义下会推断成免密钥的 anti 策略。
// 真正不可用的情形是策略名非法。
func TestKwBadStrategyChain(t *testing.T) {
	var calls atomic.Int64
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		http.Error(w, "不该被调用", http.StatusTeapot)
	}))
	defer upstream.Close()

	app, _ := buildKwApp(t, kw.Config{Strategy: "bogus", BaseURL: upstream.URL}, &calls)

	rec := get(t, app, "/api/v0/url/kw/1/320k")
	if rec.Code != http.StatusServiceUnavailable {
		t.Errorf("策略非法应 503，实际 %d", rec.Code)
	}
	if calls.Load() != 0 {
		t.Error("策略非法时不应发起上游请求")
	}
	if msg, _ := decode(t, rec)["msg"].(string); !strings.Contains(msg, "策略") {
		t.Errorf("msg = %q", msg)
	}
}

// TestKwLyricChain 串起「路由 → 缓存 → kw Source → mlyric 解析」。
//
// 歌词与取链策略解耦：anti 策略下也能拿到，所以这里就用 anti 装配。
func TestKwLyricChain(t *testing.T) {
	var calls atomic.Int64
	lyricURL := newKwLyricUpstream(t, &calls, false)
	app, _ := buildKwApp(t, kw.Config{Strategy: kw.StrategyAnti, LyricBaseURL: lyricURL}, new(atomic.Int64))

	rec := get(t, app, "/api/v0/lrc/kw/228908")
	if rec.Code != http.StatusOK {
		t.Fatalf("状态码 = %d，响应 %s", rec.Code, rec.Body.String())
	}
	data, _ := decode(t, rec)["data"].(map[string]any)
	lyric, _ := data["lyric"].(string)
	if !strings.Contains(lyric, "[00:01.00]hello") {
		t.Errorf("lyric = %q", lyric)
	}
	if calls.Load() != 1 {
		t.Errorf("上游调用 %d 次，want 1", calls.Load())
	}

	// 第二次应当命中缓存，不再打上游
	if rec := get(t, app, "/api/v0/lrc/kw/228908"); rec.Code != http.StatusOK {
		t.Fatalf("第二次状态码 = %d", rec.Code)
	}
	if calls.Load() != 1 {
		t.Errorf("第二次应命中缓存，上游调用 %d 次", calls.Load())
	}
}

// TestKwLyricNoLyricChain 上游明确「没有歌词」时返回 503，而不是空歌词 200。
func TestKwLyricNoLyricChain(t *testing.T) {
	var calls atomic.Int64
	lyricURL := newKwLyricUpstream(t, &calls, true)
	app, _ := buildKwApp(t, kw.Config{Strategy: kw.StrategyAnti, LyricBaseURL: lyricURL}, new(atomic.Int64))

	rec := get(t, app, "/api/v0/lrc/kw/228908")
	if rec.Code != http.StatusServiceUnavailable {
		t.Errorf("没有歌词应 503，实际 %d", rec.Code)
	}
}

// TestKwAntiQualityDedupe 验证 anti 策略下不同音质请求只占一份缓存、只打一次上游。
func TestKwAntiQualityDedupe(t *testing.T) {
	var calls atomic.Int64
	app, fc := newKwAntiApp(t, &calls)

	for _, q := range []string{"128k", "320k", "flac", "flac24bit"} {
		if rec := get(t, app, "/api/v0/url/kw/228908/"+q); rec.Code != http.StatusOK {
			t.Fatalf("音质 %s 状态码 = %d", q, rec.Code)
		}
	}
	// 四次请求，但 anti 只有一档，缓存键归一后只应打上游一次
	if calls.Load() != 1 {
		t.Errorf("上游调用 %d 次，want 1（音质应被归一化）", calls.Load())
	}
	// 缓存里只应有一个条目
	p, err := fc.Path("_meta/kw/228908-128k.url")
	if err != nil {
		t.Fatalf("Path: %v", err)
	}
	if _, err := os.Stat(p); err != nil {
		t.Errorf("归一化后的缓存键不存在: %v", err)
	}
	if _, err := os.Stat(p + ".meta"); err != nil {
		t.Errorf("缓存元信息不存在: %v", err)
	}
	// 上报的音质必须是保守值，不得谎报无损
	rec := get(t, app, "/api/v0/url/kw/228908/flac24bit")
	if ext, _ := decode(t, rec)["ext"].(map[string]any); ext["qual"] != "128k" {
		t.Errorf("qual = %v，不应谎报无损", ext["qual"])
	}
}
