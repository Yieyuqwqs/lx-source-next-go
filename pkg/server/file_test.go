package server

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/lxsrcs/lx-source-next/pkg/cache"
	"github.com/lxsrcs/lx-source-next/pkg/config"
)

const fileTOML = `
[Data]
Mode = 1
Type = 0
TTL = 3600
`

var sampleBody = []byte("0123456789abcdefghijklmnopqrstuvwxyz")

// newFileApp 准备一份已缓存的文件并挂上文件路由。
func newFileApp(t *testing.T) (*App, *cache.File, string) {
	t.Helper()
	d, err := config.Parse([]byte(fileTOML))
	if err != nil {
		t.Fatalf("解析配置: %v", err)
	}
	holder := config.NewHolder(&config.Snapshot{Dyn: d, LoadedAt: time.Now(), Version: 1})

	fc, err := cache.NewFile(t.TempDir())
	if err != nil {
		t.Fatalf("NewFile: %v", err)
	}
	key := "wy/1999253939/320k.mp3"
	if err := fc.Set(context.Background(), key, sampleBody, 0); err != nil {
		t.Fatalf("Set: %v", err)
	}
	app := New(Deps{Config: holder, Files: fc, Options: Options{}})
	return app, fc, key
}

func svcFile(a *App, key string, hdr map[string]string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodGet, "/api/v1/file/"+key, nil)
	for k, v := range hdr {
		req.Header.Set(k, v)
	}
	rec := httptest.NewRecorder()
	a.Handler().ServeHTTP(rec, req)
	return rec
}

func TestFileFullResponse(t *testing.T) {
	app, _, key := newFileApp(t)
	rec := svcFile(app, key, nil)

	if rec.Code != http.StatusOK {
		t.Fatalf("状态码 = %d, want 200", rec.Code)
	}
	if got := rec.Body.String(); got != string(sampleBody) {
		t.Errorf("正文长度 = %d, want %d", len(got), len(sampleBody))
	}
	h := rec.Header()
	if ct := h.Get("Content-Type"); ct != "audio/mpeg" {
		t.Errorf("Content-Type = %q, want audio/mpeg", ct)
	}
	if et := h.Get("ETag"); et == "" || !strings.HasPrefix(et, `"`) {
		t.Errorf("ETag = %q，应为带引号的强校验器", et)
	}
	if lm := h.Get("Last-Modified"); lm == "" {
		t.Error("缺少 Last-Modified")
	}
	if ar := h.Get("Accept-Ranges"); ar != "bytes" {
		t.Errorf("Accept-Ranges = %q, want bytes", ar)
	}
	if cc := h.Get("Cache-Control"); cc != "public, max-age=3600" {
		t.Errorf("Cache-Control = %q", cc)
	}
	if cl := h.Get("Content-Length"); cl != "36" {
		t.Errorf("Content-Length = %q, want 36", cl)
	}
}

func TestFileRangeResponse(t *testing.T) {
	app, _, key := newFileApp(t)
	rec := svcFile(app, key, map[string]string{"Range": "bytes=0-9"})

	if rec.Code != http.StatusPartialContent {
		t.Fatalf("状态码 = %d, want 206", rec.Code)
	}
	if got := rec.Body.String(); got != "0123456789" {
		t.Errorf("分片内容 = %q", got)
	}
	if cr := rec.Header().Get("Content-Range"); cr != "bytes 0-9/36" {
		t.Errorf("Content-Range = %q, want bytes 0-9/36", cr)
	}
	if cl := rec.Header().Get("Content-Length"); cl != "10" {
		t.Errorf("Content-Length = %q, want 10", cl)
	}
}

func TestFileTailRange(t *testing.T) {
	app, _, key := newFileApp(t)
	rec := svcFile(app, key, map[string]string{"Range": "bytes=-5"})
	if rec.Code != http.StatusPartialContent {
		t.Fatalf("状态码 = %d, want 206", rec.Code)
	}
	if got := rec.Body.String(); got != "vwxyz" {
		t.Errorf("尾部分片 = %q, want vwxyz", got)
	}
}

func TestFileConditionalETag(t *testing.T) {
	app, _, key := newFileApp(t)
	first := svcFile(app, key, nil)
	et := first.Header().Get("ETag")

	rec := svcFile(app, key, map[string]string{"If-None-Match": et})
	if rec.Code != http.StatusNotModified {
		t.Errorf("If-None-Match 命中应 304，实际 %d", rec.Code)
	}
	if rec.Body.Len() != 0 {
		t.Error("304 不应带正文")
	}
}

func TestFileConditionalTime(t *testing.T) {
	app, _, key := newFileApp(t)
	first := svcFile(app, key, nil)
	lm := first.Header().Get("Last-Modified")

	rec := svcFile(app, key, map[string]string{"If-Modified-Since": lm})
	if rec.Code != http.StatusNotModified {
		t.Errorf("If-Modified-Since 命中应 304，实际 %d", rec.Code)
	}
}

func TestFileIfRange(t *testing.T) {
	app, _, key := newFileApp(t)
	et := svcFile(app, key, nil).Header().Get("ETag")

	// If-Range 匹配 → 正常返回分片
	rec := svcFile(app, key, map[string]string{"Range": "bytes=0-3", "If-Range": et})
	if rec.Code != http.StatusPartialContent {
		t.Errorf("If-Range 匹配应 206，实际 %d", rec.Code)
	}
	// If-Range 不匹配 → 忽略 Range，返回完整内容
	rec = svcFile(app, key, map[string]string{"Range": "bytes=0-3", "If-Range": `"stale"`})
	if rec.Code != http.StatusOK {
		t.Errorf("If-Range 不匹配应返回 200 全文，实际 %d", rec.Code)
	}
}

func TestFileNotFound(t *testing.T) {
	app, _, _ := newFileApp(t)
	rec := svcFile(app, "wy/404/none.mp3", nil)
	if rec.Code != http.StatusNotFound {
		t.Errorf("不存在的文件应 404，实际 %d", rec.Code)
	}
}

func TestFilePathTraversalBlocked(t *testing.T) {
	app, _, _ := newFileApp(t)
	// 这些路径要么被 ServeMux 规范化，要么被 buildFileKey 拦下，
	// 无论哪条路，都不能返回 200 或读到缓存目录之外的东西。
	cases := []string{
		"wy/%2e%2e/320k.mp3",
		"wy/..%2f..%2fetc/passwd",
		"..%2f..%2fetc/passwd/x/y",
		"wy/1/..",
		"wy/1/.hidden",
		"wy/1/a%00b.mp3",
		"wy/1/a%5cb.mp3",
	}
	for _, c := range cases {
		req := httptest.NewRequest(http.MethodGet, "/api/v1/file/"+c, nil)
		rec := httptest.NewRecorder()
		app.Handler().ServeHTTP(rec, req)
		if rec.Code == http.StatusOK || rec.Code == http.StatusPartialContent {
			t.Errorf("路径 %q 不应成功，实际 %d", c, rec.Code)
		}
		if strings.Contains(rec.Body.String(), "root:") {
			t.Errorf("路径 %q 疑似读到了系统文件", c)
		}
	}
}

func TestFileWithoutDriver(t *testing.T) {
	d, _ := config.Parse([]byte(fileTOML))
	holder := config.NewHolder(&config.Snapshot{Dyn: d, LoadedAt: time.Now(), Version: 1})
	app := New(Deps{Config: holder, Options: Options{}})
	rec := svcFile(app, "wy/1/320k.mp3", nil)
	if rec.Code != http.StatusServiceUnavailable {
		t.Errorf("未注入文件驱动应 503，实际 %d", rec.Code)
	}
}

func TestBuildFileKey(t *testing.T) {
	if k, err := buildFileKey("wy", "123", "320k.mp3"); err != nil || k != "wy/123/320k.mp3" {
		t.Errorf("合法键 = %q, %v", k, err)
	}
	bad := [][]string{
		{"wy", "123"},
		{"", "123", "320k.mp3"},
		{"wy", "..", "320k.mp3"},
		{"wy", "123", ".."},
		{"wy", "123", ".meta"},
		{"wy/../x", "123", "a.mp3"},
		{"wy", "1 2", "a.mp3"},
	}
	for _, parts := range bad {
		if _, err := buildFileKey(parts...); err == nil {
			t.Errorf("应拒绝 %v", parts)
		}
	}
}

func TestContentTypeMapping(t *testing.T) {
	cases := map[string]string{
		"a.mp3":  "audio/mpeg",
		"a.flac": "audio/flac",
		"a.lrc":  "text/plain; charset=utf-8",
		"a.jpg":  "image/jpeg",
		"a.zzz":  "application/octet-stream",
	}
	for name, want := range cases {
		if got := contentType(name); got != want {
			t.Errorf("contentType(%q) = %q, want %q", name, got, want)
		}
	}
}

func TestETagChangesWithContent(t *testing.T) {
	a := etag(100, time.Unix(1000, 0))
	b := etag(101, time.Unix(1000, 0))
	c := etag(100, time.Unix(1001, 0))
	if a == b || a == c {
		t.Error("大小或时间变化时 ETag 必须变化")
	}
	if a != etag(100, time.Unix(1000, 0)) {
		t.Error("相同输入应得到相同 ETag")
	}
}
