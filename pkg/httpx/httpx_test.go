package httpx

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/lxsrcs/lx-source-next/pkg/apierr"
)

func TestFailIncludesRootCause(t *testing.T) {
	root := errors.New("dial tcp: lookup antiserver.kuwo.cn: no such host")
	err := apierr.Wrap(apierr.CodeUnavailable, "kw: 请求上游失败", root)

	rec := httptest.NewRecorder()
	Fail(rec, err)

	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("状态码 = %d", rec.Code)
	}
	var env Envelope
	if e := json.Unmarshal(rec.Body.Bytes(), &env); e != nil {
		t.Fatalf("响应不是 JSON: %v", e)
	}
	if !strings.Contains(env.Msg, "请求上游失败") {
		t.Errorf("缺少外层描述: %q", env.Msg)
	}
	if !strings.Contains(env.Msg, "no such host") {
		t.Errorf("根因未透出，排查会瞎: %q", env.Msg)
	}
}

func TestFailWithoutCause(t *testing.T) {
	rec := httptest.NewRecorder()
	Fail(rec, apierr.New(apierr.CodeBadRequest, "参数错误"))
	if !strings.Contains(rec.Body.String(), "参数错误") {
		t.Errorf("body = %s", rec.Body.String())
	}
	if strings.Contains(rec.Body.String(), ": ") {
		t.Errorf("无根因时不应拼接冒号: %s", rec.Body.String())
	}
}
