package apierr

import (
	"errors"
	"fmt"
	"net/http"
	"testing"
)

func TestCodeHTTPStatus(t *testing.T) {
	cases := []struct {
		code Code
		want int
	}{
		{CodeOK, http.StatusOK},
		{CodeForbidden, http.StatusForbidden},
		{CodeUnavailable, http.StatusServiceUnavailable},
		{CodeUnauthorized, http.StatusUnauthorized},
		{CodeInternal, http.StatusInternalServerError},
		{CodeRateLimited, http.StatusTooManyRequests},
		{CodeBadRequest, http.StatusBadRequest},
	}
	for _, c := range cases {
		if got := c.code.HTTPStatus(); got != c.want {
			t.Errorf("%v.HTTPStatus() = %d, want %d", c.code, got, c.want)
		}
	}
	// 未知码按服务器错误处理：回 200 会让客户端把错误当成成功
	if got := Code(99).HTTPStatus(); got != http.StatusInternalServerError {
		t.Errorf("未知码 HTTPStatus = %d, want 500", got)
	}
}

func TestNewAndWrap(t *testing.T) {
	e := New(CodeBadRequest, "参数错误")
	if e.Code != CodeBadRequest || e.Msg != "参数错误" {
		t.Errorf("New 结果不对: %+v", e)
	}
	if e.Error() != "参数错误" {
		t.Errorf("Error() = %q", e.Error())
	}
	if e.HTTPStatus() != http.StatusBadRequest {
		t.Errorf("HTTPStatus = %d", e.HTTPStatus())
	}

	root := errors.New("连接被重置")
	w := Wrap(CodeUnavailable, "上游失败", root)
	if !errors.Is(w, root) {
		t.Error("Wrap 后 errors.Is 应能找到根因")
	}
	if got, want := w.Error(), "上游失败: 连接被重置"; got != want {
		t.Errorf("Error() = %q, want %q", got, want)
	}

	nf := Newf(CodeRateLimited, "超过 %d 次", 10)
	if nf.Msg != "超过 10 次" {
		t.Errorf("Newf Msg = %q", nf.Msg)
	}
}

func TestFromAndCodeOf(t *testing.T) {
	inner := New(CodeUnauthorized, "密钥错误")
	outer := fmt.Errorf("处理请求: %w", inner)

	got, ok := From(outer)
	if !ok || got != inner {
		t.Fatalf("From 未能取出原错误")
	}
	if CodeOf(outer) != CodeUnauthorized {
		t.Errorf("CodeOf = %v, want unauthorized", CodeOf(outer))
	}
	if CodeOf(outer).HTTPStatus() != http.StatusUnauthorized {
		t.Errorf("HTTPStatus = %d", CodeOf(outer).HTTPStatus())
	}

	if CodeOf(nil) != CodeOK {
		t.Error("nil 应视为成功码")
	}
	if CodeOf(errors.New("普通错误")) != CodeInternal {
		t.Error("非 *Error 应视为内部错误")
	}
	if _, ok := From(errors.New("普通错误")); ok {
		t.Error("普通错误不应被识别为 *Error")
	}
}

func TestNilSafe(t *testing.T) {
	var e *Error
	if e.HTTPStatus() != http.StatusOK {
		t.Errorf("nil.HTTPStatus = %d", e.HTTPStatus())
	}
	if e.Error() != "" {
		t.Errorf("nil.Error = %q", e.Error())
	}
}
