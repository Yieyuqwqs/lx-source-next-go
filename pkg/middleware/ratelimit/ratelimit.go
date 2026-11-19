// Package ratelimit 提供可替换实现的速率限制中间件。
//
// Limiter 是接口，内存窗口计数只是默认实现；
// 需要跨实例共享时换成 Redis 实现即可，中间件一行不用改。
package ratelimit

import (
	"log/slog"
	"net/http"
	"sync"
	"time"

	"github.com/lxsrcs/lx-source-next/pkg/apierr"
	"github.com/lxsrcs/lx-source-next/pkg/httpx"
	"github.com/lxsrcs/lx-source-next/pkg/middleware"
)

// Rules 是限流规则。
type Rules struct {
	// Enabled 是否启用。
	Enabled bool
	// Global 全局每窗口上限，<=0 表示不限。
	Global int
	// Single 单来源每窗口上限，<=0 表示不限。
	Single int
	// Window 窗口长度。
	Window time.Duration
}

// Limiter 是可替换的限流实现。
type Limiter interface {
	// Allow 判断 key 在 limit/window 下是否放行；不放行时返回建议重试时长。
	Allow(key string, limit int, window time.Duration) (bool, time.Duration)
}

// Options 是中间件构造参数。
type Options struct {
	// Rules 每次请求调用，保证热更即时生效。
	Rules func() Rules
	// Limiter 为空时使用内置内存实现。
	Limiter Limiter
	// Skip 返回 true 的请求不参与限流（例如白名单来源）。
	Skip func(*http.Request) bool
	// Logger 可为空。
	Logger *slog.Logger
}

// New 构造限流中间件。
func New(opt Options) middleware.Middleware {
	lim := opt.Limiter
	if lim == nil {
		lim = NewMemory()
	}
	logger := opt.Logger
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			rule := opt.Rules()
			if !rule.Enabled {
				next.ServeHTTP(w, r)
				return
			}
			if opt.Skip != nil && opt.Skip(r) {
				next.ServeHTTP(w, r)
				return
			}
			key := httpx.ClientAddrString(r.Context())
			if key == "" {
				// 拿不到来源就不限流，避免误伤全部流量
				next.ServeHTTP(w, r)
				return
			}
			if rule.Global > 0 {
				if ok, retry := lim.Allow("global", rule.Global, rule.Window); !ok {
					reject(w, logger, r, "全局", retry)
					return
				}
			}
			if rule.Single > 0 {
				if ok, retry := lim.Allow(key, rule.Single, rule.Window); !ok {
					reject(w, logger, r, "单来源", retry)
					return
				}
			}
			next.ServeHTTP(w, r)
		})
	}
}

func reject(w http.ResponseWriter, logger *slog.Logger, r *http.Request, scope string, retry time.Duration) {
	if logger != nil {
		logger.Warn("触发限流", "scope", scope, "client", httpx.ClientAddrString(r.Context()), "path", r.URL.Path)
	}
	if retry > 0 {
		w.Header().Set("Retry-After", formatSeconds(retry))
	}
	httpx.Fail(w, apierr.New(apierr.CodeRateLimited, scope+"请求过于频繁"))
}

func formatSeconds(d time.Duration) string {
	s := int(d / time.Second)
	if d%time.Second != 0 {
		s++
	}
	if s < 1 {
		s = 1
	}
	return itoa(s)
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var buf [20]byte
	i := len(buf)
	for n > 0 {
		i--
		buf[i] = byte('0' + n%10)
		n /= 10
	}
	return string(buf[i:])
}

// Memory 是内置的窗口计数限流器，并发安全。
//
// 采用「固定窗口 + 惰性清扫」：不做后台 goroutine，
// 计数条目在窗口过期后的访问或超过阈值时被回收。
type Memory struct {
	mu      sync.Mutex
	entries map[string]*entry
	maxIdle int
}

type entry struct {
	count int
	reset time.Time
}

// NewMemory 创建内存限流器。
func NewMemory() *Memory {
	return &Memory{entries: make(map[string]*entry), maxIdle: 4096}
}

// Allow 实现 Limiter。
func (m *Memory) Allow(key string, limit int, window time.Duration) (bool, time.Duration) {
	if limit <= 0 {
		return true, 0
	}
	if window <= 0 {
		window = time.Second
	}
	now := time.Now()

	m.mu.Lock()
	defer m.mu.Unlock()

	e, ok := m.entries[key]
	if !ok {
		e = &entry{reset: now.Add(window)}
		m.entries[key] = e
	} else if now.After(e.reset) {
		e.count = 0
		e.reset = now.Add(window)
	}
	e.count++
	allowed := e.count <= limit
	retry := time.Duration(0)
	if !allowed {
		retry = e.reset.Sub(now)
	}
	if len(m.entries) > m.maxIdle {
		m.sweep(now)
		// 全是活跃条目时也不能无限增长：淘汰任意一条，保证内存有上界
		for len(m.entries) > m.maxIdle {
			for k := range m.entries {
				delete(m.entries, k)
				break
			}
		}
	}
	return allowed, retry
}

// sweep 清掉已过期的条目。调用方需持有锁。
func (m *Memory) sweep(now time.Time) {
	for k, e := range m.entries {
		if now.After(e.reset) {
			delete(m.entries, k)
		}
	}
}

// Size 返回当前跟踪的键数量，便于观测。
func (m *Memory) Size() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return len(m.entries)
}
