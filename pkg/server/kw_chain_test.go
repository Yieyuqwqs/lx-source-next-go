package server

import (
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

// TestKwAntiNoLyricChain 验证 anti 策略下歌词返回 503 而不是乱猜。
func TestKwAntiNoLyricChain(t *testing.T) {
	var calls atomic.Int64
	app, _ := newKwAntiApp(t, &calls)

	rec := get(t, app, "/api/v0/lrc/kw/228908")
	if rec.Code != http.StatusServiceUnavailable {
		t.Errorf("anti 策略不提供歌词，应 503，实际 %d", rec.Code)
	}
}
