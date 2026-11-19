// Package httpx 是传输层的最小公共件：统一响应信封、错误落盘、客户端地址解析。
//
// 这一层不含业务语义，也不认识配置；
// 它只把「内部错误」翻译成「HTTP 响应」，供中间件与 handler 复用。
package httpx

import (
	"context"
	"encoding/json"
	"net/http"
	"net/netip"
	"strings"

	"github.com/lxsrcs/lx-source-next/pkg/apierr"
)

// Envelope 是对外统一响应结构。字段名保持与既有客户端兼容。
type Envelope struct {
	Code int    `json:"code"`
	Msg  string `json:"msg"`
	Data any    `json:"data"`
	Ext  any    `json:"ext,omitempty"`
}

// WriteJSON 写出 JSON 响应。
func WriteJSON(w http.ResponseWriter, status int, body any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	// 响应已发出，序列化失败也无处可报，忽略即可。
	_ = json.NewEncoder(w).Encode(body)
}

// OK 写出成功响应。
func OK(w http.ResponseWriter, data any) {
	WriteJSON(w, http.StatusOK, Envelope{Code: 0, Msg: "", Data: data})
}

// Fail 把错误写成统一响应：状态码与业务码都由 apierr 决定。
func Fail(w http.ResponseWriter, err error) {
	code := apierr.CodeOf(err)
	msg := ""
	if e, ok := apierr.From(err); ok {
		msg = e.Msg
		// 把根因拼上：这是自建服务，能看到 "dial tcp: lookup ... no such host"
		// 远比只看到 "请求上游失败" 有用。
		if e.Err != nil {
			if msg == "" {
				msg = e.Err.Error()
			} else {
				msg = msg + ": " + e.Err.Error()
			}
		}
	} else if err != nil {
		msg = err.Error()
	}
	WriteJSON(w, code.HTTPStatus(), Envelope{Code: int(code), Msg: msg})
}

type ctxKey int

const clientAddrKey ctxKey = iota

// WithClientAddr 把解析好的客户端地址放进 context。
func WithClientAddr(ctx context.Context, addr netip.Addr) context.Context {
	return context.WithValue(ctx, clientAddrKey, addr)
}

// ClientAddr 取出客户端地址；取不到时返回零值。
func ClientAddr(ctx context.Context) netip.Addr {
	if v, ok := ctx.Value(clientAddrKey).(netip.Addr); ok {
		return v
	}
	return netip.Addr{}
}

// ClientAddrString 取出客户端地址的字符串形式，缺失时返回空串。
func ClientAddrString(ctx context.Context) string {
	a := ClientAddr(ctx)
	if !a.IsValid() {
		return ""
	}
	return a.String()
}

// IPResolver 解析客户端地址。
//
// 是否信任转发头是一个部署决策，因此作为构造参数注入，
// 而不是靠全局开关去猜。
type IPResolver struct {
	// TrustProxy 为 true 时优先读取 Headers 里的转发地址。
	TrustProxy bool
	// Headers 是转发头，按顺序尝试；为空时用 X-Forwarded-For / X-Real-IP。
	Headers []string
}

var defaultHeaders = []string{"X-Forwarded-For", "X-Real-IP"}

// Resolve 从请求里解析客户端地址。
func (r IPResolver) Resolve(req *http.Request) netip.Addr {
	if !r.TrustProxy {
		addr, _ := parseAddrPort(req.RemoteAddr)
		return addr
	}
	headers := r.Headers
	if len(headers) == 0 {
		headers = defaultHeaders
	}
	for _, h := range headers {
		v := req.Header.Get(h)
		if v == "" {
			continue
		}
		// X-Forwarded-For 是逗号分隔链，取最左（最初客户端）
		first, _, _ := strings.Cut(v, ",")
		if addr, err := netip.ParseAddr(strings.TrimSpace(first)); err == nil {
			return addr.Unmap()
		}
	}
	addr, _ := parseAddrPort(req.RemoteAddr)
	return addr
}

func parseAddrPort(s string) (netip.Addr, bool) {
	if s == "" {
		return netip.Addr{}, false
	}
	if ap, err := netip.ParseAddrPort(s); err == nil {
		return ap.Addr().Unmap(), true
	}
	if a, err := netip.ParseAddr(s); err == nil {
		return a.Unmap(), true
	}
	return netip.Addr{}, false
}

const trustedKey ctxKey = 1

// WithTrusted 标记该请求来自白名单（可跳过速率限制）。
func WithTrusted(ctx context.Context, v bool) context.Context {
	return context.WithValue(ctx, trustedKey, v)
}

// Trusted 返回该请求是否被标记为白名单来源。
func Trusted(ctx context.Context) bool {
	v, _ := ctx.Value(trustedKey).(bool)
	return v
}
