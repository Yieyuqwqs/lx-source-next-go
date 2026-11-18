package access

import (
	"net/http"
	"net/http/httptest"
	"net/netip"
	"testing"

	"github.com/lxsrcs/lx-source-next/pkg/httpx"
	"github.com/lxsrcs/lx-source-next/pkg/middleware"
)

func run(t *testing.T, cfg func() Config, clientIP string) (int, bool) {
	t.Helper()
	trusted := false
	h := middleware.Chain{
		func(next http.Handler) http.Handler {
			return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				ctx := r.Context()
				if clientIP != "" {
					ctx = httpx.WithClientAddr(ctx, netip.MustParseAddr(clientIP))
				}
				next.ServeHTTP(w, r.WithContext(ctx))
			})
		},
		New(cfg, nil),
	}.Then(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		trusted = httpx.Trusted(r.Context())
		w.WriteHeader(http.StatusNoContent)
	}))
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/x", nil))
	return rec.Code, trusted
}

func TestDisabled(t *testing.T) {
	code, trusted := run(t, func() Config { return Config{Enabled: false} }, "10.0.0.1")
	if code != http.StatusNoContent || trusted {
		t.Errorf("未启用应直接放行，code=%d trusted=%v", code, trusted)
	}
}

func TestWhitelistTrusted(t *testing.T) {
	cfg := func() Config {
		return Config{Enabled: true, White: []string{"127.0.0.1", "192.168.0.0/16"}}
	}
	code, trusted := run(t, cfg, "192.168.5.6")
	if code != http.StatusNoContent || !trusted {
		t.Errorf("白名单应放行且标记 trusted，code=%d trusted=%v", code, trusted)
	}
}

func TestBlacklistRejected(t *testing.T) {
	cfg := func() Config { return Config{Enabled: true, Black: []string{"10.0.0.0/8"}} }
	code, _ := run(t, cfg, "10.1.1.1")
	if code != http.StatusForbidden {
		t.Errorf("黑名单应返回 403，实际 %d", code)
	}
}

func TestStrictMode(t *testing.T) {
	cfg := func() Config {
		return Config{Enabled: true, Strict: true, White: []string{"127.0.0.1"}, Black: []string{"10.0.0.0/8"}}
	}
	// 严格模式下未过白的一律拒绝
	if code, _ := run(t, cfg, "8.8.8.8"); code != http.StatusForbidden {
		t.Errorf("严格模式未过白应 403，实际 %d", code)
	}
	// 白名单照常
	if code, trusted := run(t, cfg, "127.0.0.1"); code != http.StatusNoContent || !trusted {
		t.Errorf("严格模式白名单应放行，code=%d", code)
	}
}

func TestInvalidConfig(t *testing.T) {
	cfg := func() Config { return Config{Enabled: true, White: []string{"不是IP"}} }
	if code, _ := run(t, cfg, "1.1.1.1"); code != http.StatusInternalServerError {
		t.Errorf("配置非法应返回 500，实际 %d", code)
	}
}

func TestRecompilesOnChange(t *testing.T) {
	white := "192.168.0.0/16"
	cfg := func() Config { return Config{Enabled: true, White: []string{white}} }

	if code, _ := run(t, cfg, "192.168.1.1"); code != http.StatusNoContent {
		t.Fatalf("初始配置应放行")
	}
	white = "10.0.0.0/8" // 热更白名单
	if code, _ := run(t, cfg, "192.168.1.1"); code != http.StatusNoContent {
		// 非严格模式下非白名单普通 IP 也放行，这里主要验证不 panic 且不误判
		t.Logf("非严格模式，普通 IP 依然放行: %d", code)
	}
	if code, trusted := run(t, cfg, "10.9.9.9"); code != http.StatusNoContent || !trusted {
		t.Errorf("热更后的白名单应生效，code=%d trusted=%v", code, trusted)
	}
}
