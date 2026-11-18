// Package recoverer 把 panic 变成 500 响应，避免单个请求打挂进程。
package recoverer

import (
	"fmt"
	"log/slog"
	"net/http"
	"runtime/debug"

	"github.com/lxsrcs/lx-source-next/pkg/httpx"
	"github.com/lxsrcs/lx-source-next/pkg/middleware"
)

// New 构造 panic 恢复中间件。logger 可为 nil。
func New(logger *slog.Logger) middleware.Middleware {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			defer func() {
				rec := recover()
				if rec == nil {
					return
				}
				if logger != nil {
					logger.Error("请求 panic",
						"path", r.URL.Path,
						"panic", fmt.Sprint(rec),
						"stack", string(debug.Stack()),
					)
				}
				httpx.Fail(w, fmt.Errorf("内部错误: %v", rec))
			}()
			next.ServeHTTP(w, r)
		})
	}
}
