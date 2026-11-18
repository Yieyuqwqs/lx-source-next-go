// Package reqlog 记录访问日志。
//
// 只依赖 slog，不依赖任何日志实现细节；
// 凭据脱敏由 logx 的 Handler 统一完成，这里不做重复处理。
package reqlog

import (
	"log/slog"
	"net/http"
	"time"

	"github.com/lxsrcs/lx-source-next/pkg/httpx"
	"github.com/lxsrcs/lx-source-next/pkg/middleware"
)

// New 构造访问日志中间件。logger 为 nil 时直接透传。
func New(logger *slog.Logger) middleware.Middleware {
	if logger == nil {
		return func(next http.Handler) http.Handler { return next }
	}
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			start := time.Now()
			sw := &statusWriter{ResponseWriter: w, status: http.StatusOK}
			next.ServeHTTP(sw, r)
			logger.Info("请求完成",
				"method", r.Method,
				"path", r.URL.Path,
				"status", sw.status,
				"bytes", sw.written,
				"client", httpx.ClientAddrString(r.Context()),
				"cost", time.Since(start).String(),
			)
		})
	}
}

// statusWriter 记录状态码与写出字节数。
type statusWriter struct {
	http.ResponseWriter
	status  int
	written int
	wrote   bool
}

func (s *statusWriter) WriteHeader(code int) {
	if !s.wrote {
		s.status = code
		s.wrote = true
	}
	s.ResponseWriter.WriteHeader(code)
}

func (s *statusWriter) Write(b []byte) (int, error) {
	s.wrote = true
	n, err := s.ResponseWriter.Write(b)
	s.written += n
	return n, err
}

// Unwrap 让 http.ResponseController 能拿到底层 writer。
func (s *statusWriter) Unwrap() http.ResponseWriter { return s.ResponseWriter }
