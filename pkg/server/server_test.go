package server

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/lxsrcs/lx-source-next/pkg/config"
	"github.com/lxsrcs/lx-source-next/pkg/logx"
)

const baseTOML = `
[Auth.BanList]
Enable = %v
Strict = false
White = ['127.0.0.1']
Black = %s

[Auth.RateLimit]
Enable = %v
Block = 60
Globle = 0
Single = 2

[Auth.ApiPass]
Enable = %v
Value = 'the-key'

[Apis]
WebLog = %v
`

func app(t *testing.T, raw string, opt Options, hub *logx.Hub) *App {
	t.Helper()
	d, err := config.Parse([]byte(raw))
	if err != nil {
		t.Fatalf("解析配置: %v", err)
	}
	holder := config.NewHolder(&config.Snapshot{Dyn: d, LoadedAt: time.Now(), Version: 1})
	return New(Deps{Config: holder, LogHub: hub, Options: opt})
}

func do(a *App, method, path string, hdr map[string]string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(method, path, nil)
	for k, v := range hdr {
		req.Header.Set(k, v)
	}
	rec := httptest.NewRecorder()
	a.Handler().ServeHTTP(rec, req)
	return rec
}

func TestPingWithoutMiddleware(t *testing.T) {
	a := app(t, "[Apis]\nWebLog = false\n", Options{}, nil)
	rec := do(a, http.MethodGet, "/api/v0/ping", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("状态码 = %d, want 200", rec.Code)
	}
	var env struct {
		Code int `json:"code"`
		Data any `json:"data"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &env); err != nil {
		t.Fatalf("响应不是 JSON: %v", err)
	}
	if env.Code != 0 || env.Data != "pong" {
		t.Errorf("响应体 = %+v", env)
	}
}

func TestApiKeyGate(t *testing.T) {
	raw := "[Apis]\nWebLog = false\n[Auth.ApiPass]\nEnable = true\nValue = 'the-key'\n"
	opt := Options{EnableApiKey: true}
	a := app(t, raw, opt, nil)

	if rec := do(a, http.MethodGet, "/api/v0/ping", nil); rec.Code != http.StatusUnauthorized {
		t.Errorf("缺少密钥应 401，实际 %d", rec.Code)
	}
	if rec := do(a, http.MethodGet, "/api/v0/ping", map[string]string{"X-Request-Key": "nope"}); rec.Code != http.StatusUnauthorized {
		t.Errorf("错误密钥应 401，实际 %d", rec.Code)
	}
	if rec := do(a, http.MethodGet, "/api/v0/ping", map[string]string{"X-Request-Key": "the-key"}); rec.Code != http.StatusOK {
		t.Errorf("正确密钥应 200，实际 %d", rec.Code)
	}
	// 用同一份配置但不装密钥中间件 → 说明中间件是可拔插的
	bare := app(t, raw, Options{}, nil)
	if rec := do(bare, http.MethodGet, "/api/v0/ping", nil); rec.Code != http.StatusOK {
		t.Errorf("未装配鉴权时应放行，实际 %d", rec.Code)
	}
}

func TestAccessGate(t *testing.T) {
	raw := "[Apis]\nWebLog = false\n[Auth.BanList]\nEnable = true\nBlack = ['192.0.2.0/24']\n"
	a := app(t, raw, Options{EnableAccess: true}, nil)

	// httptest 默认 RemoteAddr 是 192.0.2.1:1234，落在黑名单网段
	if rec := do(a, http.MethodGet, "/api/v0/ping", nil); rec.Code != http.StatusForbidden {
		t.Errorf("黑名单应 403，实际 %d", rec.Code)
	}
	bare := app(t, raw, Options{}, nil)
	if rec := do(bare, http.MethodGet, "/api/v0/ping", nil); rec.Code != http.StatusOK {
		t.Errorf("未装配访问控制时应放行，实际 %d", rec.Code)
	}
}

func TestRateLimitGate(t *testing.T) {
	raw := "[Apis]\nWebLog = false\n[Auth.RateLimit]\nEnable = true\nBlock = 60\nGloble = 0\nSingle = 2\n"
	a := app(t, raw, Options{EnableRateLimit: true}, nil)

	if rec := do(a, http.MethodGet, "/api/v0/ping", nil); rec.Code != http.StatusOK {
		t.Fatalf("第 1 次应放行，实际 %d", rec.Code)
	}
	if rec := do(a, http.MethodGet, "/api/v0/ping", nil); rec.Code != http.StatusOK {
		t.Fatalf("第 2 次应放行，实际 %d", rec.Code)
	}
	rec := do(a, http.MethodGet, "/api/v0/ping", nil)
	if rec.Code != http.StatusTooManyRequests {
		t.Errorf("第 3 次应 429，实际 %d", rec.Code)
	}
	if rec.Header().Get("Retry-After") == "" {
		t.Error("被限流时应给出 Retry-After")
	}
}

func TestRateLimitAllowsRepeatedWhenEngineOff(t *testing.T) {
	raw := "[Apis]\nWebLog = false\n[Auth.RateLimit]\nEnable = true\nBlock = 60\nSingle = 2\n"
	a := app(t, raw, Options{}, nil) // 不装限流中间件
	for i := 0; i < 10; i++ {
		if rec := do(a, http.MethodGet, "/api/v0/ping", nil); rec.Code != http.StatusOK {
			t.Fatalf("第 %d 次本轮应放行，实际 %d", i+1, rec.Code)
		}
	}
}

func TestSSERequiresWebLog(t *testing.T) {
	a := app(t, "[Apis]\nWebLog = false\n", Options{}, logx.NewHub(8))
	if rec := do(a, http.MethodGet, "/api/v1/test/sse", nil); rec.Code != http.StatusForbidden {
		t.Errorf("未开启 WebLog 时 SSE 应 403，实际 %d", rec.Code)
	}
	a = app(t, "[Apis]\nWebLog = true\n", Options{}, nil)
	if rec := do(a, http.MethodGet, "/api/v1/test/sse", nil); rec.Code != http.StatusServiceUnavailable {
		t.Errorf("未注入 Hub 时 SSE 应 503，实际 %d", rec.Code)
	}
}

func TestStatus(t *testing.T) {
	a := app(t, "[Apis]\nWebLog = false\n", Options{}, logx.NewHub(8))
	rec := do(a, http.MethodGet, "/api/v1/status", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("状态码 = %d", rec.Code)
	}
	var env struct {
		Data map[string]any `json:"data"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &env); err != nil {
		t.Fatalf("响应不是 JSON: %v", err)
	}
	if env.Data["configVersion"] == nil {
		t.Error("status 应包含 configVersion")
	}
}

func TestPanicRecovered(t *testing.T) {
	// 直接验证 recoverer 已装配：构造一个会 panic 的 handler 走同一条链
	a := app(t, "[Apis]\nWebLog = false\n", Options{}, nil)
	h := a.chain().Then(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		panic("boom")
	}))
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/x", nil))
	if rec.Code != http.StatusInternalServerError {
		t.Errorf("panic 应转成 500，实际 %d", rec.Code)
	}
}
