package server

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/lxsrcs/lx-source-next/pkg/cache"
	cachepolicy "github.com/lxsrcs/lx-source-next/pkg/cache/policy"
	"github.com/lxsrcs/lx-source-next/pkg/config"
	"github.com/lxsrcs/lx-source-next/pkg/source"
	"github.com/lxsrcs/lx-source-next/pkg/source/mock"
)

const v0TOML = `
[Auth.ApiPass]
Enable = false
Value = ''

[Data]
Mode = 1
Type = 0
TTL = 3600
`

// countingSource 记录上游调用次数，用于验证缓存确实生效。
type countingSource struct {
	source.Source
	urlCalls atomic.Int64
}

func (c *countingSource) URL(ctx context.Context, id, q string) (source.LinkInfo, error) {
	c.urlCalls.Add(1)
	return c.Source.URL(ctx, id, q)
}

func newV0App(t *testing.T, src source.Source, raw ...string) *App {
	t.Helper()
	cfgRaw := v0TOML
	if len(raw) > 0 {
		cfgRaw = raw[0]
	}
	d, err := config.Parse([]byte(cfgRaw))
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

	reg := source.Registry{"mock": src}
	return New(Deps{Config: holder, Cache: reader, Files: fc, Sources: reg, Options: Options{}})
}

func get(t *testing.T, a *App, path string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, path, nil)
	rec := httptest.NewRecorder()
	a.Handler().ServeHTTP(rec, req)
	return rec
}

func decode(t *testing.T, rec *httptest.ResponseRecorder) map[string]any {
	t.Helper()
	var m map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &m); err != nil {
		t.Fatalf("响应不是 JSON: %v (%s)", err, rec.Body.String())
	}
	return m
}

func TestV0URLAndAliases(t *testing.T) {
	a := newV0App(t, mock.New())

	for _, path := range []string{
		"/api/v0/url/mock/123/320k",
		"/api/v0/link/mock/123/320k",
	} {
		rec := get(t, a, path)
		if rec.Code != http.StatusOK {
			t.Fatalf("%s 状态码 = %d", path, rec.Code)
		}
		m := decode(t, rec)
		if m["code"].(float64) != 0 {
			t.Errorf("%s code = %v", path, m["code"])
		}
		data, _ := m["data"].(string)
		if !strings.Contains(data, "https://mock.invalid/audio/123/320k.mp3") {
			t.Errorf("%s data = %q", path, data)
		}
		ext, _ := m["ext"].(map[string]any)
		if ext["qual"] != "320k" {
			t.Errorf("%s ext.qual = %v", path, ext["qual"])
		}
		if expi, ok := ext["expi"].(float64); !ok || expi <= 0 {
			t.Errorf("%s ext.expi = %v", path, ext["expi"])
		}
	}
}

func TestV0QualityFallback(t *testing.T) {
	a := newV0App(t, mock.New())
	// mock 只支持 128k/320k/flac，请求 master 应回落到 flac
	rec := get(t, a, "/api/v0/url/mock/123/master")
	if rec.Code != http.StatusOK {
		t.Fatalf("状态码 = %d", rec.Code)
	}
	ext, _ := decode(t, rec)["ext"].(map[string]any)
	if ext["qual"] != "flac" {
		t.Errorf("回落音质 = %v, want flac", ext["qual"])
	}
}

func TestV0EmptyQualityWithTrailingSlash(t *testing.T) {
	a := newV0App(t, mock.New())
	// 文档要求：音质留空时保留尾部斜杠
	rec := get(t, a, "/api/v0/url/mock/123/")
	if rec.Code != http.StatusOK {
		t.Fatalf("尾部斜杠状态码 = %d", rec.Code)
	}
	if data, _ := decode(t, rec)["data"].(string); data == "" {
		t.Error("应返回链接")
	}
}

func TestV0UnknownMethod(t *testing.T) {
	a := newV0App(t, mock.New())
	rec := get(t, a, "/api/v0/nope/mock/123/320k")
	if rec.Code != http.StatusBadRequest {
		t.Errorf("未知方法应 400，实际 %d", rec.Code)
	}
}

func TestV0UnknownSource(t *testing.T) {
	a := newV0App(t, mock.New())
	rec := get(t, a, "/api/v0/url/xx/123/320k")
	if rec.Code != http.StatusNotFound {
		t.Errorf("未知来源应 404，实际 %d", rec.Code)
	}
}

func TestV0JmpRedirects(t *testing.T) {
	a := newV0App(t, mock.New())
	rec := get(t, a, "/api/v0/url/mock/123/320k?jmp")
	if rec.Code != http.StatusFound {
		t.Fatalf("jmp 应 302，实际 %d", rec.Code)
	}
	if loc := rec.Header().Get("Location"); !strings.HasPrefix(loc, "https://mock.invalid/audio/123/") {
		t.Errorf("Location = %q", loc)
	}
	if cc := rec.Header().Get("Cache-Control"); cc != "no-store" {
		t.Errorf("302 应带 no-store，实际 %q", cc)
	}
}

func TestV0JmpOnMVAndPic(t *testing.T) {
	a := newV0App(t, mock.New())
	for _, p := range []string{"/api/v0/mv/mock/5397146/720p?jmp", "/api/v0/pic/mock/123/m?jmp"} {
		rec := get(t, a, p)
		if rec.Code != http.StatusFound {
			t.Errorf("%s 应 302，实际 %d", p, rec.Code)
		}
		if rec.Header().Get("Location") == "" {
			t.Errorf("%s 缺少 Location", p)
		}
	}
}

func TestV0JmpReturnsJSONOnError(t *testing.T) {
	a := newV0App(t, mock.New())
	// 文档：jmp 出错时仍以 JSON 返回
	rec := get(t, a, "/api/v0/url/mock/notfound/320k?jmp")
	if rec.Code != http.StatusNotFound {
		t.Fatalf("应 404，实际 %d", rec.Code)
	}
	if ct := rec.Header().Get("Content-Type"); !strings.Contains(ct, "application/json") {
		t.Errorf("出错时应返回 JSON，Content-Type = %q", ct)
	}
	m := decode(t, rec)
	if m["code"].(float64) != float64(7) {
		t.Errorf("错误码 = %v, want 7", m["code"])
	}
}

func TestV0Lyric(t *testing.T) {
	a := newV0App(t, mock.New())
	rec := get(t, a, "/api/v0/lrc/mock/123")
	if rec.Code != http.StatusOK {
		t.Fatalf("状态码 = %d", rec.Code)
	}
	data, _ := decode(t, rec)["data"].(map[string]any)
	lyric, _ := data["lyric"].(string)
	if !strings.Contains(lyric, "[00:00.00]") {
		t.Errorf("lyric = %q", lyric)
	}
	if lx, _ := data["lxlyric"].(string); !strings.Contains(lx, "<0,400>") {
		t.Errorf("lxlyric = %q", lx)
	}
}

func TestV0LyricJmpPlainText(t *testing.T) {
	a := newV0App(t, mock.New())
	rec := get(t, a, "/api/v0/lyric/mock/123?jmp")
	if rec.Code != http.StatusOK {
		t.Fatalf("状态码 = %d", rec.Code)
	}
	if ct := rec.Header().Get("Content-Type"); !strings.Contains(ct, "text/plain") {
		t.Errorf("Content-Type = %q", ct)
	}
	if !strings.Contains(rec.Body.String(), "[00:00.00]") {
		t.Errorf("正文 = %q", rec.Body.String())
	}
}

func TestV0MovieAliases(t *testing.T) {
	a := newV0App(t, mock.New())
	for _, p := range []string{"/api/v0/mov/mock/5397146/720p", "/api/v0/mv/mock/5397146/720p"} {
		rec := get(t, a, p)
		if rec.Code != http.StatusOK {
			t.Fatalf("%s 状态码 = %d", p, rec.Code)
		}
		data, _ := decode(t, rec)["data"].(map[string]any)
		if !strings.Contains(data["link"].(string), "/video/") {
			t.Errorf("%s link = %v", p, data["link"])
		}
	}
}

func TestV0MusicAndVideo(t *testing.T) {
	a := newV0App(t, mock.New())

	rec := get(t, a, "/api/v0/mid/mock/1912598037")
	if rec.Code != http.StatusOK {
		t.Fatalf("mid 状态码 = %d", rec.Code)
	}
	data, _ := decode(t, rec)["data"].(map[string]any)
	if data["name"] != "Mock Song 1912598037" {
		t.Errorf("mid name = %v", data["name"])
	}
	if files, _ := data["file"].([]any); len(files) != 3 {
		t.Errorf("mid file 数量 = %d", len(files))
	}

	rec = get(t, a, "/api/v0/vid/mock/90001")
	if rec.Code != http.StatusOK {
		t.Fatalf("vid 状态码 = %d", rec.Code)
	}
	if v := decode(t, rec)["data"].(map[string]any); v["id"] != "90001" {
		t.Errorf("vid id = %v", v["id"])
	}
}

func TestV0Pic(t *testing.T) {
	a := newV0App(t, mock.New())
	rec := get(t, a, "/api/v0/pic/mock/123/m")
	if rec.Code != http.StatusOK {
		t.Fatalf("状态码 = %d", rec.Code)
	}
	if s, _ := decode(t, rec)["data"].(string); !strings.Contains(s, "/pic/123-m.jpg") {
		t.Errorf("pic = %q", s)
	}
}

func TestV0CacheHitsUpstreamOnce(t *testing.T) {
	cs := &countingSource{Source: mock.New()}
	a := newV0App(t, cs)

	for i := 0; i < 5; i++ {
		if rec := get(t, a, "/api/v0/url/mock/123/320k"); rec.Code != http.StatusOK {
			t.Fatalf("第 %d 次状态码 = %d", i+1, rec.Code)
		}
	}
	if got := cs.urlCalls.Load(); got != 1 {
		t.Errorf("应该是缓存命中，上游调用 %d 次", got)
	}
}

func TestV0RequiresApiKey(t *testing.T) {
	d, err := config.Parse([]byte("[Auth.ApiPass]\nEnable = true\nValue = 'secret'\n"))
	if err != nil {
		t.Fatal(err)
	}
	holder := config.NewHolder(&config.Snapshot{Dyn: d, LoadedAt: time.Now(), Version: 1})
	a := New(Deps{
		Config:  holder,
		Sources: source.Registry{"mock": mock.New()},
		Options: Options{EnableApiKey: true},
	})

	if rec := get(t, a, "/api/v0/url/mock/123/320k"); rec.Code != http.StatusUnauthorized {
		t.Errorf("缺少密钥应 401，实际 %d", rec.Code)
	}

	req := httptest.NewRequest(http.MethodGet, "/api/v0/url/mock/123/320k", nil)
	req.Header.Set("X-Request-Key", "secret")
	rec := httptest.NewRecorder()
	a.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Errorf("带正确密钥应 200，实际 %d", rec.Code)
	}
}

func TestV0NoSources(t *testing.T) {
	d, _ := config.Parse([]byte(v0TOML))
	holder := config.NewHolder(&config.Snapshot{Dyn: d, LoadedAt: time.Now(), Version: 1})
	a := New(Deps{Config: holder, Options: Options{}})
	rec := get(t, a, "/api/v0/url/mock/123/320k")
	if rec.Code != http.StatusServiceUnavailable {
		t.Errorf("无来源时应 503，实际 %d", rec.Code)
	}
}

func TestFileRouteRejectsInternalKeys(t *testing.T) {
	a := newV0App(t, mock.New())
	rec := get(t, a, "/api/v1/file/_meta/mock/123-320k.url")
	if rec.Code != http.StatusNotFound {
		t.Errorf("内部键不应通过文件路由，实际 %d", rec.Code)
	}
}
