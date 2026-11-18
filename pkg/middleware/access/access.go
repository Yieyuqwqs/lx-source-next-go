// Package access 基于 IP 黑白名单做访问控制。
//
// 它只是 pkg/ipguard 的 HTTP 适配层：把「配置」编译成「守卫」并缓存，
// 把判定结果写进 context 供后续中间件使用。
// 名单来源由注入的 provider 提供，热更时自动重新编译。
package access

import (
	"fmt"
	"log/slog"
	"net/http"
	"sync"

	"github.com/lxsrcs/lx-source-next/pkg/apierr"
	"github.com/lxsrcs/lx-source-next/pkg/httpx"
	"github.com/lxsrcs/lx-source-next/pkg/ipguard"
	"github.com/lxsrcs/lx-source-next/pkg/middleware"
)

// Config 是访问控制的原始配置。
type Config struct {
	Enabled bool
	Strict  bool
	White   []string
	Black   []string
}

// key 返回配置指纹，用于判断是否需要重新编译。
func (c Config) key() string {
	return fmt.Sprintf("%v|%v|%v|%v", c.Enabled, c.Strict, c.White, c.Black)
}

// policy 是编译后的策略。
type policy struct {
	enabled bool
	strict  bool
	guard   *ipguard.Guard
}

// compiler 缓存编译结果，只在配置指纹变化时重编译。
type compiler struct {
	mu      sync.Mutex
	lastKey string
	last    *policy
	lastErr error
}

func (c *compiler) get(cfg Config) (*policy, error) {
	k := cfg.key()
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.last != nil && c.lastKey == k {
		return c.last, nil
	}
	g, err := ipguard.New(ipguard.Config{
		Allow:  cfg.White,
		Deny:   cfg.Black,
		Strict: cfg.Strict,
	})
	if err != nil {
		c.lastKey = k
		c.lastErr = fmt.Errorf("access: 编译名单失败: %w", err)
		c.last = nil
		return nil, c.lastErr
	}
	c.lastKey = k
	c.lastErr = nil
	c.last = &policy{enabled: cfg.Enabled, strict: cfg.Strict, guard: g}
	return c.last, nil
}

// New 构造访问控制中间件。
func New(cfg func() Config, logger *slog.Logger) middleware.Middleware {
	c := &compiler{}
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			pol, err := c.get(cfg())
			if err != nil {
				if logger != nil {
					logger.Error("访问控制配置非法", "err", err)
				}
				httpx.Fail(w, apierr.Wrap(apierr.CodeInternal, "访问控制配置非法", err))
				return
			}
			if !pol.enabled {
				next.ServeHTTP(w, r)
				return
			}
			addr := httpx.ClientAddr(r.Context())
			d := pol.guard.Check(addr)
			if !d.Allow {
				if logger != nil {
					logger.Warn("拒绝访问", "client", addr.String(), "path", r.URL.Path)
				}
				httpx.Fail(w, apierr.New(apierr.CodeForbidden, "来源被拒绝"))
				return
			}
			// 白名单来源在后续中间件里可跳过限流
			next.ServeHTTP(w, r.WithContext(httpx.WithTrusted(r.Context(), d.Trusted)))
		})
	}
}
