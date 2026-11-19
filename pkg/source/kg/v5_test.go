package kg

import (
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
	if got := q.Get("signature"); got != "36675ae6573a6778f9fe3e7fb2b7153" {
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

	if _, _, err := parseV5URL([]byte(`{"status":2,"error":"no privilege"}`)); err == nil {
		t.Error("status 非 1 时必须报错")
	}
	if !strings.Contains(err.Error(), "no privilege") {
		t.Errorf("错误应带上游原因: %v", err)
	}
}
