// Package cors 给接口加跨域响应头，供网页端直接调用。
package cors

import (
	"net/http"

	"github.com/lxsrcs/lx-source-next/pkg/middleware"
)

// AllowHeaders 是允许的请求头（接口密钥与常见类型）。
const allowHeaders = "Content-Type, X-Request-Key"

// New 构造跨域中间件。
//
// 只用通配来源：本服务是「自建 + 自带密钥」的形态，不做 Cookie 凭据跨域，
// 因此不需要 Allow-Credentials，也不需要来源白名单。
func New() middleware.Middleware {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			h := w.Header()
			h.Set("Access-Control-Allow-Origin", "*")
			h.Set("Access-Control-Allow-Methods", "GET, POST, OPTIONS")
			h.Set("Access-Control-Allow-Headers", allowHeaders)
			h.Set("Access-Control-Max-Age", "3600")

			// 预检直接放行，不进入业务链
			if r.Method == http.MethodOptions {
				w.WriteHeader(http.StatusNoContent)
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}
