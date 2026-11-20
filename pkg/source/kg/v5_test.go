package kg

import (
	"context"
	"errors"
	"net/url"
	"strings"
	"testing"
)

// TestV5Signature 用固定 clienttime 校验签名链路（黄金值由实测实现算出）。
func TestV5Signature(t *testing.T) {
	raw, err := v5Query("abc", "flac", 1700000000)
	if err != nil {
		t.Fatalf("v5Query: %v", err)
	}
	q, err := url.ParseQuery(raw)
	if err != nil {
		t.Fatalf("解析 query: %v", err)
	}
	if got := q.Get("key"); got != "689828745cab3e437bbe973cb1cb2e5f" {
		t.Errorf("key = %q", got)
	}
	if got := q.Get("signature"); got != "b36675ae6573a6778f9fe3e7fb2b7153" {
		t.Errorf("signature = %q", got)
	}
	if got := q.Get("quality"); got != "flac" {
		t.Errorf("quality = %q", got)
	}
	if got := q.Get("clienttime"); got != "1700000000" {
		t.Errorf("clienttime = %q", got)
	}
}

// TestV5Fallback 降档只降不升。
func TestV5Fallback(t *testing.T) {
	cases := []struct {
		qual string
		want string
	}{
		{"flac24bit", "high,flac,320,128"},
		{"flac", "flac,320,128"},
		{"320k", "320,128"},
		{"128k", "128"},
		{"看不懂", "128"},
	}
	for _, c := range cases {
		got := strings.Join(v5Fallback(c.qual), ",")
		if got != c.want {
			t.Errorf("v5Fallback(%q) = %q, want %q", c.qual, got, c.want)
		}
	}
}

// TestParseV5URL 覆盖字符串、数组、失败三种响应。
func TestParseV5URL(t *testing.T) {
	link, st, err := parseV5URL([]byte(`{"status":1,"url":"http://a/1.mp3"}`))
	if err != nil || link != "http://a/1.mp3" || st != 1 {
		t.Errorf("字符串形态: link=%q st=%d err=%v", link, st, err)
	}

	link, _, err = parseV5URL([]byte(`{"status":1,"url":["http://a/1.mp3","http://a/2.flac"]}`))
	if err != nil || link != "http://a/2.flac" {
		t.Errorf("数组形态应取最后一个: link=%q err=%v", link, err)
	}

	if _, _, err = parseV5URL([]byte(`{"status":2,"error":"no privilege"}`)); err == nil {
		t.Error("status 非 1 时必须报错")
	} else if !strings.Contains(err.Error(), "no privilege") {
		t.Errorf("错误应带上游原因: %v", err)
	}
}

// TestV5VerifyRequired 上游要人工验证时要能识别成「请求级」失败。
//
// 2026-11-21 实测：匿名请求开始回 status=2 +「本次请求需要验证」，
// 这时换档位重试没有意义，必须立刻上冒。
func TestV5VerifyRequired(t *testing.T) {
	for _, body := range []string{
		`{"status":2,"error":"本次请求需要验证"}`,
		`{"status":2}`,
	} {
		_, _, err := parseV5URL([]byte(body))
		if !errors.Is(err, ErrVerifyRequired) {
			t.Errorf("%s 应判定为 ErrVerifyRequired，实际 %v", body, err)
		}
	}

	// 普通的「无权限」不能被误判成验证门槛，否则会放弃降档
	if _, _, err := parseV5URL([]byte(`{"status":2,"error":"no privilege"}`)); errors.Is(err, ErrVerifyRequired) {
		t.Error("无权限不应判定为验证门槛")
	}
	if _, _, err := parseV5URL([]byte(`{"status":1,"url":""}`)); err == nil {
		t.Error("status=1 但没有 url 应报错")
	}
}

// TestParseV5NeedsPurchase 新版报文用 priv_status/fail_process 表达「要买」。
func TestParseV5NeedsPurchase(t *testing.T) {
	body := `{"priv_status":0,"auth_through":[],"fail_process":["pkg","buy"],"url":""}`
	_, _, err := parseV5URL([]byte(body))
	if !errors.Is(err, ErrNeedsPurchase) {
		t.Fatalf("应判定 ErrNeedsPurchase，实际 %v", err)
	}
	if !strings.Contains(err.Error(), "pkg,buy") {
		t.Errorf("错误里应带上 fail_process: %v", err)
	}
}

// TestURLVerifyRequiredStopsFallback 一次验证失败就打住，别空跑四趟。
func TestURLVerifyRequiredStopsFallback(t *testing.T) {
	d := &fakeDoer{body: `{"status":2,"error":"本次请求需要验证"}`}
	c := New(Config{}, d)

	_, err := c.URL(context.Background(), "B3A52A7A958BF0AED0EBFBA2E9A818B7", "flac24bit")
	if err == nil {
		t.Fatal("应报错")
	}
	if !strings.Contains(err.Error(), "人工验证") {
		t.Errorf("错误信息应说明要人工验证: %v", err)
	}
	if len(d.reqs) != 1 {
		t.Errorf("应只请求一次，实际 %d 次", len(d.reqs))
	}
}
