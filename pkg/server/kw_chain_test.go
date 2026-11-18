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

// newKwApp 用 httptest 伪造上游，装配完整的 App。
func newKwApp(t *testing.T, key []byte, calls *atomic.Int64) (*App, *cache.File) {
	t.Helper()

	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		// 对端真的解密我们的 q 参数，验证密文合法
		plain, err := kw.DesDecrypt(r.URL.Query().Get("q"), key)
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

	src := kw.New(kw.Config{BaseURL: upstream.URL, Key: key}, nil)
	app := New(Deps{
		Config:  holder,
		Cache:   reader,
		Files:   fc,
		Sources: source.Registry{"kw": src},
		Options: Options{},
	})
	return app, fc
}

// TestKwFullChain 串起「路由 → 鉴权跳过 → 缓存 → kw Source → 解析 → 回填」。
func TestKwFullChain(t *testing.T) {
	key := []byte("12345678")
	var calls atomic.Int64
	app, fc := newKwApp(t, key, &calls)

	// 第一次：缓存未命中，打到上游
	rec := get(t, app, "/api/v0/url/kw/123/320k")
	if rec.Code != http.StatusOK {
		t.Fatalf("状态码 = %d，响应 %s", rec.Code, rec.Body.String())
	}
	m := decode(t, rec)
	data, _ := m["data"].(string)
	if !strings.Contains(data, "kw.invalid/123/320kmp3.mp3") {
		t.Errorf("data = %q", data)
	}
	ext, _ := m["ext"].(map[string]any)
	if ext["qual"] != "320k" {
		t.Errorf("ext.qual = %v", ext["qual"])
	}
	if calls.Load() != 1 {
		t.Errorf("上游调用 %d 次，want 1", calls.Load())
	}

	// 第二次：命中缓存，不再打扰上游
	if rec := get(t, app, "/api/v0/url/kw/123/320k"); rec.Code != http.StatusOK {
		t.Fatalf("第二次状态码 = %d", rec.Code)
	}
	if calls.Load() != 1 {
		t.Errorf("第二次应命中缓存，上游调用 %d 次", calls.Load())
	}

	// 缓存确实落盘在内部命名空间
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

// TestKwJmpChain 验证 jmp 直接把 302 Location 指向上游直链。
func TestKwJmpChain(t *testing.T) {
	var calls atomic.Int64
	app, _ := newKwApp(t, []byte("12345678"), &calls)

	rec := get(t, app, "/api/v0/url/kw/999/flac?jmp")
	if rec.Code != http.StatusFound {
		t.Fatalf("状态码 = %d，want 302", rec.Code)
	}
	loc := rec.Header().Get("Location")
	if !strings.Contains(loc, "kw.invalid/999/2000kflac.mp3") {
		t.Errorf("Location = %q", loc)
	}
}

// TestKwQualityFallbackChain 验证 master 在 kw 上回落到 flac24bit。
func TestKwQualityFallbackChain(t *testing.T) {
	var calls atomic.Int64
	app, _ := newKwApp(t, []byte("12345678"), &calls)

	rec := get(t, app, "/api/v0/url/kw/1/master")
	if rec.Code != http.StatusOK {
		t.Fatalf("状态码 = %d", rec.Code)
	}
	ext, _ := decode(t, rec)["ext"].(map[string]any)
	if ext["qual"] != "flac24bit" {
		t.Errorf("回落音质 = %v，want flac24bit", ext["qual"])
	}
}

// TestKwNotConfiguredChain 验证未配置密钥时错误码穿过整条链路。
func TestKwNotConfiguredChain(t *testing.T) {
	var calls atomic.Int64
	app, _ := newKwApp(t, nil, &calls) // 无密钥

	rec := get(t, app, "/api/v0/url/kw/1/320k")
	if rec.Code != http.StatusServiceUnavailable {
		t.Errorf("未配置应 503，实际 %d", rec.Code)
	}
	if calls.Load() != 0 {
		t.Error("未配置时不应发起上游请求")
	}
	m := decode(t, rec)
	if msg, _ := m["msg"].(string); !strings.Contains(msg, "未配置") {
		t.Errorf("msg = %q", msg)
	}
}
