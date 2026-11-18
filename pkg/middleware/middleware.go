// Package middleware 提供可组合的 HTTP 中间件骨架。
//
// 设计要点：
//   - 中间件就是标准的 func(http.Handler) http.Handler，不绑定任何框架；
//   - 每个中间件只依赖自己需要的窄接口/函数，不认识 config 包；
//   - 需要「当前策略」的中间件接收一个 provider 函数，
//     由组合根在启动时把它接到配置快照上，于是热更自动生效；
//   - 装配顺序完全由组合根决定，想加就加、想去就去。
package middleware

import "net/http"

// Middleware 是标准 HTTP 中间件。
type Middleware func(http.Handler) http.Handler

// Chain 是按顺序组合的中间件链。
type Chain []Middleware

// Then 把链套在最终 handler 外。先加入的中间件在最外层。
func (c Chain) Then(h http.Handler) http.Handler {
	for i := len(c) - 1; i >= 0; i-- {
		if c[i] == nil {
			continue
		}
		h = c[i](h)
	}
	return h
}

// Append 返回追加中间件后的新链，不修改原链。
func (c Chain) Append(ms ...Middleware) Chain {
	out := make(Chain, 0, len(c)+len(ms))
	out = append(out, c...)
	out = append(out, ms...)
	return out
}

// Len 返回链长度（跳过 nil）。
func (c Chain) Len() int {
	n := 0
	for _, m := range c {
		if m != nil {
			n++
		}
	}
	return n
}
