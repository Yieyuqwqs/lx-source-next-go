// Package apikey 校验接口密钥。
//
// 规则从注入的 provider 读取，因此热更后立即生效，
// 中间件本身不持有任何配置状态。
package apikey

import (
	"crypto/subtle"
	"log/slog"
	"net/http"

	"github.com/lxsrcs/lx-source-next/pkg/apierr"
	"github.com/lxsrcs/lx-source-next/pkg/httpx"
	"github.com/lxsrcs/lx-source-next/pkg/middleware"
)

// Rules 是鉴权规则。
type Rules struct {
	// Enabled 是否启用密钥校验。
	Enabled bool
	// Key 是期望的密钥。
	Key string
}

// HeaderName 是密钥请求头。
const HeaderName = "X-Request-Key"

// QueryName 是密钥查询参数，用于无法设置请求头的场景。
const QueryName = "key"

// New 构造鉴权中间件。rules 每次请求都会被调用。
func New(rules func() Rules, logger *slog.Logger) middleware.Middleware {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			rule := rules()
			if !rule.Enabled {
				next.ServeHTTP(w, r)
				return
			}
			got := extract(r)
			if !equal(got, rule.Key) {
				if logger != nil {
					logger.Warn("接口密钥校验失败",
						"client", httpx.ClientAddrString(r.Context()),
						"path", r.URL.Path,
					)
				}
				httpx.Fail(w, apierr.New(apierr.CodeUnauthorized, "接口密钥无效"))
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}

func extract(r *http.Request) string {
	if v := r.Header.Get(HeaderName); v != "" {
		return v
	}
	return r.URL.Query().Get(QueryName)
}

// equal 用常数时间比较，避免按字节比较泄露前缀信息。
func equal(a, b string) bool {
	if len(a) != len(b) {
		return false
	}
	return subtle.ConstantTimeCompare([]byte(a), []byte(b)) == 1
}
