package server

import (
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/lxsrcs/lx-source-next/pkg/config"
	"github.com/lxsrcs/lx-source-next/pkg/source"
	"github.com/lxsrcs/lx-source-next/pkg/source/mock"
)

// holderFor 解析一段动态配置并包成快照，供只关心配置的用例使用。
func holderFor(t *testing.T, raw string) *config.Holder {
	t.Helper()
	d, err := config.Parse([]byte(raw))
	if err != nil {
		t.Fatalf("解析配置: %v", err)
	}
	return config.NewHolder(&config.Snapshot{Dyn: d, LoadedAt: time.Now(), Version: 1})
}

// TestIndexPage 非 ApiOnly 时根路径给自检页。
func TestIndexPage(t *testing.T) {
	a := app(t, "[Apis]\nWebLog = false\nApiOnly = false\n", Options{}, nil)
	rec := do(a, http.MethodGet, "/", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("状态码 = %d, want 200", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), "/api/v0/ping") {
		t.Errorf("自检页应列出接口")
	}
}

// TestIndexPageApiOnly ApiOnly 打开时根路径按 404 处理。
func TestIndexPageApiOnly(t *testing.T) {
	a := app(t, "[Apis]\nWebLog = false\nApiOnly = true\n", Options{}, nil)
	rec := do(a, http.MethodGet, "/", nil)
	if rec.Code != http.StatusNotFound {
		t.Errorf("状态码 = %d, want 404", rec.Code)
	}
}

// TestCorsPreflight 开启跨域后预检直接 204，并带允许头。
func TestCorsPreflight(t *testing.T) {
	a := app(t, "[Apis]\nWebLog = false\n", Options{EnableCors: true}, nil)
	rec := do(a, http.MethodOptions, "/api/v0/ping", nil)
	if rec.Code != http.StatusNoContent {
		t.Fatalf("状态码 = %d, want 204", rec.Code)
	}
	if got := rec.Header().Get("Access-Control-Allow-Origin"); got != "*" {
		t.Errorf("Allow-Origin = %q", got)
	}
	if !strings.Contains(rec.Header().Get("Access-Control-Allow-Headers"), "X-Request-Key") {
		t.Errorf("Allow-Headers = %q", rec.Header().Get("Access-Control-Allow-Headers"))
	}
}

// TestSourceDisabled 配置里停用的来源要如实拒绝，而不是回落到别的源。
func TestSourceDisabled(t *testing.T) {
	a := New(Deps{
		Config:         holderFor(t, "[Apis]\nWebLog = false\n"),
		Sources:        source.Registry{"mock": mock.New()},
		SourcesEnabled: func(string) bool { return false },
	})
	rec := do(a, http.MethodGet, "/api/v0/url/mock/123/128k", nil)
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("状态码 = %d, want 503", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), "停用") {
		t.Errorf("错误应说明已停用: %s", rec.Body.String())
	}
}

// TestErrorAudioRedirect 配了错误音频时，取链失败回 302 而不是 JSON 错误。
func TestErrorAudioRedirect(t *testing.T) {
	a := New(Deps{
		Config:  holderFor(t, "[Apis]\nWebLog = false\n[Errs]\nExpi = 120\nErrMp3 = 'https://example.com/err.mp3'\n"),
		Sources: source.Registry{"mock": mock.New()},
	})
	rec := do(a, http.MethodGet, "/api/v0/url/mock/notfound/128k", nil)
	if rec.Code != http.StatusFound {
		t.Fatalf("状态码 = %d, want 302（错误音频）", rec.Code)
	}
	if loc := rec.Header().Get("Location"); loc != "https://example.com/err.mp3" {
		t.Errorf("Location = %q", loc)
	}
	if cc := rec.Header().Get("Cache-Control"); !strings.Contains(cc, "120") {
		t.Errorf("错误音频应按 Errs.Expi 缓存, Cache-Control = %q", cc)
	}
}
