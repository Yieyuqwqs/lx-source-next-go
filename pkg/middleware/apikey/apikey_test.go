package apikey

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/lxsrcs/lx-source-next/pkg/middleware"
)

func run(t *testing.T, rules func() Rules, req *http.Request) int {
	t.Helper()
	h := middleware.Chain{New(rules, nil)}.Then(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	}))
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec.Code
}

func TestDisabled(t *testing.T) {
	code := run(t, func() Rules { return Rules{Enabled: false} }, httptest.NewRequest(http.MethodGet, "/x", nil))
	if code != http.StatusNoContent {
		t.Errorf("未启用时应放行，状态码 %d", code)
	}
}

func TestEnabled(t *testing.T) {
	rules := func() Rules { return Rules{Enabled: true, Key: "s3cret"} }

	req := httptest.NewRequest(http.MethodGet, "/x", nil)
	req.Header.Set(HeaderName, "s3cret")
	if code := run(t, rules, req); code != http.StatusNoContent {
		t.Errorf("请求头密钥正确应放行，状态码 %d", code)
	}

	req = httptest.NewRequest(http.MethodGet, "/x?key=s3cret", nil)
	if code := run(t, rules, req); code != http.StatusNoContent {
		t.Errorf("查询参数密钥正确应放行，状态码 %d", code)
	}

	req = httptest.NewRequest(http.MethodGet, "/x", nil)
	req.Header.Set(HeaderName, "wrong")
	if code := run(t, rules, req); code != http.StatusUnauthorized {
		t.Errorf("密钥错误应返回 401，实际 %d", code)
	}

	req = httptest.NewRequest(http.MethodGet, "/x", nil)
	if code := run(t, rules, req); code != http.StatusUnauthorized {
		t.Errorf("缺少密钥应返回 401，实际 %d", code)
	}
}

func TestRulesHotSwap(t *testing.T) {
	enabled := false
	rules := func() Rules { return Rules{Enabled: enabled, Key: "k"} }
	req := func() *http.Request { return httptest.NewRequest(http.MethodGet, "/x", nil) }

	if code := run(t, rules, req()); code != http.StatusNoContent {
		t.Errorf("未启用应放行")
	}
	enabled = true
	if code := run(t, rules, req()); code != http.StatusUnauthorized {
		t.Errorf("热更后应开始校验，实际 %d", code)
	}
}
