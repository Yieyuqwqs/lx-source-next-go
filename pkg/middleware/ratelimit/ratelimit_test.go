package ratelimit

import (
	"net/http"
	"net/http/httptest"
	"net/netip"
	"testing"
	"time"

	"github.com/lxsrcs/lx-source-next/pkg/httpx"
	"github.com/lxsrcs/lx-source-next/pkg/middleware"
)

func run(t *testing.T, opt Options, clientIP string) int {
	t.Helper()
	h := middleware.Chain{
		func(next http.Handler) http.Handler {
			return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				ctx := httpx.WithClientAddr(r.Context(), netip.MustParseAddr(clientIP))
				next.ServeHTTP(w, r.WithContext(ctx))
			})
		},
		New(opt),
	}.Then(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	}))
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/x", nil))
	return rec.Code
}

func TestDisabled(t *testing.T) {
	opt := Options{Rules: func() Rules { return Rules{Enabled: false, Single: 1} }}
	for i := 0; i < 5; i++ {
		if code := run(t, opt, "1.2.3.4"); code != http.StatusNoContent {
			t.Fatalf("未启用时第 %d 次应放行，实际 %d", i+1, code)
		}
	}
}

func TestSingleLimit(t *testing.T) {
	opt := Options{
		Rules:   func() Rules { return Rules{Enabled: true, Single: 2, Window: time.Minute} },
		Limiter: NewMemory(),
	}
	if code := run(t, opt, "1.2.3.4"); code != http.StatusNoContent {
		t.Fatalf("第 1 次应放行")
	}
	if code := run(t, opt, "1.2.3.4"); code != http.StatusNoContent {
		t.Fatalf("第 2 次应放行")
	}
	if code := run(t, opt, "1.2.3.4"); code != http.StatusTooManyRequests {
		t.Fatalf("第 3 次应被限流，实际 %d", code)
	}
	// 另一个来源不受影响
	if code := run(t, opt, "5.6.7.8"); code != http.StatusNoContent {
		t.Fatalf("其他来源应放行，实际 %d", code)
	}
}

func TestGlobalLimit(t *testing.T) {
	opt := Options{
		Rules:   func() Rules { return Rules{Enabled: true, Global: 1, Window: time.Minute} },
		Limiter: NewMemory(),
	}
	if code := run(t, opt, "1.1.1.1"); code != http.StatusNoContent {
		t.Fatal("全局第 1 次应放行")
	}
	if code := run(t, opt, "2.2.2.2"); code != http.StatusTooManyRequests {
		t.Fatal("全局第 2 次应被限流")
	}
}

func TestSkip(t *testing.T) {
	opt := Options{
		Rules:   func() Rules { return Rules{Enabled: true, Single: 1, Window: time.Minute} },
		Limiter: NewMemory(),
		Skip:    func(r *http.Request) bool { return httpx.Trusted(r.Context()) },
	}
	// 不带 trusted 时被限流
	if code := run(t, opt, "1.1.1.1"); code != http.StatusNoContent {
		t.Fatal("第 1 次应放行")
	}
	if code := run(t, opt, "1.1.1.1"); code != http.StatusTooManyRequests {
		t.Fatal("第 2 次应被限流")
	}
}

func TestRulesHotSwap(t *testing.T) {
	single := 1
	opt := Options{
		Rules:   func() Rules { return Rules{Enabled: true, Single: single, Window: time.Minute} },
		Limiter: NewMemory(),
	}
	run(t, opt, "9.9.9.9")
	if code := run(t, opt, "9.9.9.9"); code != http.StatusTooManyRequests {
		t.Fatal("应被限流")
	}
	// 热更提高上限后，同一窗口内继续放行
	single = 10
	if code := run(t, opt, "9.9.9.9"); code != http.StatusNoContent {
		t.Errorf("提高上限后应放行，实际 %d", code)
	}
}

// countingLimiter 证明 Limiter 接口是可替换的。
type countingLimiter struct{ calls int }

func (c *countingLimiter) Allow(key string, limit int, window time.Duration) (bool, time.Duration) {
	c.calls++
	return true, 0
}

func TestCustomLimiterIsPluggable(t *testing.T) {
	lim := &countingLimiter{}
	opt := Options{
		Rules:   func() Rules { return Rules{Enabled: true, Global: 5, Single: 5, Window: time.Minute} },
		Limiter: lim,
	}
	run(t, opt, "1.1.1.1")
	if lim.calls != 2 {
		t.Errorf("自定义 Limiter 调用次数 = %d, want 2（全局 + 单来源）", lim.calls)
	}
}

func TestMemoryWindowReset(t *testing.T) {
	m := NewMemory()
	window := 30 * time.Millisecond
	if ok, _ := m.Allow("k", 1, window); !ok {
		t.Fatal("第一次应放行")
	}
	if ok, retry := m.Allow("k", 1, window); ok || retry <= 0 {
		t.Fatal("同窗口内第二次应拒绝并给出重试时长")
	}
	time.Sleep(window + 10*time.Millisecond)
	if ok, _ := m.Allow("k", 1, window); !ok {
		t.Fatal("窗口过期后应重新放行")
	}
}

func TestMemoryUnlimited(t *testing.T) {
	m := NewMemory()
	for i := 0; i < 10; i++ {
		if ok, _ := m.Allow("k", 0, time.Minute); !ok {
			t.Fatal("limit<=0 应视为不限制")
		}
	}
}
