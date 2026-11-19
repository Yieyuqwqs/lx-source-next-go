// Package apierr 统一接口错误码。
//
// v0 的历史约定是「一个整数码同时决定 HTTP 状态」，两套规范混用过一段时间。
// 这里把它收敛成单一类型：一处定义 Code，HTTP 状态由查表得出，不再散落在各 handler。
package apierr

import (
	"errors"
	"fmt"
	"net/http"
)

// Code 是接口状态码。数值与线上历史枚举保持一致。
type Code int

const (
	// CodeOK 成功。
	CodeOK Code = 0
	// CodeForbidden 无权限。
	CodeForbidden Code = 1
	// CodeUnavailable 服务不可用；v0 约定此时可返回错误音频占位。
	CodeUnavailable Code = 2
	// CodeUnauthorized 密钥缺失或错误。
	CodeUnauthorized Code = 3
	// CodeInternal 内部错误。
	CodeInternal Code = 4
	// CodeRateLimited 触发限流。
	CodeRateLimited Code = 5
	// CodeBadRequest 参数错误。
	CodeBadRequest Code = 6
	// CodeNotFound 资源不存在。
	//
	// 历史枚举里没有这一项，属于兼容性扩展：
	// 老客户端只会看到 code!=0 并按 msg 处理，不会因为多一个码而错乱。
	CodeNotFound Code = 7
)

// HTTPStatus 返回该代码对应的 HTTP 状态码。
func (c Code) HTTPStatus() int {
	switch c {
	case CodeOK:
		return http.StatusOK
	case CodeForbidden:
		return http.StatusForbidden
	case CodeUnavailable:
		return http.StatusServiceUnavailable
	case CodeUnauthorized:
		return http.StatusUnauthorized
	case CodeInternal:
		return http.StatusInternalServerError
	case CodeRateLimited:
		return http.StatusTooManyRequests
	case CodeBadRequest:
		return http.StatusBadRequest
	case CodeNotFound:
		return http.StatusNotFound
	}
	// 未知码按服务器错误处理：回 200 会让客户端把错误当成成功
	return http.StatusInternalServerError
}

// String 便于日志输出。
func (c Code) String() string {
	switch c {
	case CodeOK:
		return "ok"
	case CodeForbidden:
		return "forbidden"
	case CodeUnavailable:
		return "unavailable"
	case CodeUnauthorized:
		return "unauthorized"
	case CodeInternal:
		return "internal"
	case CodeRateLimited:
		return "rate_limited"
	case CodeBadRequest:
		return "bad_request"
	case CodeNotFound:
		return "not_found"
	}
	return fmt.Sprintf("code(%d)", int(c))
}

// Error 是带状态码的接口错误。
type Error struct {
	Code Code
	Msg  string
	Err  error
}

// New 构造一个错误。
func New(code Code, msg string) *Error {
	return &Error{Code: code, Msg: msg}
}

// Newf 构造一个带格式化的错误。
func Newf(code Code, format string, args ...any) *Error {
	return &Error{Code: code, Msg: fmt.Sprintf(format, args...)}
}

// Wrap 包装底层错误，保留 Code 与可读说明。
func Wrap(code Code, msg string, err error) *Error {
	return &Error{Code: code, Msg: msg, Err: err}
}

// Error 实现 error。
func (e *Error) Error() string {
	if e == nil {
		return ""
	}
	if e.Err != nil {
		if e.Msg == "" {
			return e.Err.Error()
		}
		return e.Msg + ": " + e.Err.Error()
	}
	return e.Msg
}

// Unwrap 支持 errors.Is / errors.As 穿透到根因。
func (e *Error) Unwrap() error { return e.Err }

// HTTPStatus 返回该错误应使用的 HTTP 状态码。
func (e *Error) HTTPStatus() int {
	if e == nil {
		return http.StatusOK
	}
	return e.Code.HTTPStatus()
}

// From 从错误链里取出 *Error。
func From(err error) (*Error, bool) {
	var e *Error
	if errors.As(err, &e) {
		return e, true
	}
	return nil, false
}

// CodeOf 返回错误对应的状态码；非 *Error 一律视为内部错误。
func CodeOf(err error) Code {
	if err == nil {
		return CodeOK
	}
	if e, ok := From(err); ok {
		return e.Code
	}
	return CodeInternal
}
