package wy

import (
	"context"
	"strings"
	"testing"
)

// TestCookieHeaderAnonymous 匿名时只带环境标识。
func TestCookieHeaderAnonymous(t *testing.T) {
	c := New(Config{}, nil)
	if got := c.cookieHeader(); got != "os=pc" {
		t.Errorf("匿名 cookie = %q, want os=pc", got)
	}
}

// TestCookieHeaderWithCredential 配了登录态要拼上，且已有 os 时不重复。
func TestCookieHeaderWithCredential(t *testing.T) {
	c := New(Config{Cookie: "MUSIC_U=abc"}, nil)
	got := c.cookieHeader()
	if !strings.Contains(got, "MUSIC_U=abc") || !strings.Contains(got, "os=pc") {
		t.Errorf("cookie = %q，应同时含登录态与环境标识", got)
	}

	c2 := New(Config{Cookie: "os=pc; MUSIC_U=abc"}, nil)
	if got := c2.cookieHeader(); got != "os=pc; MUSIC_U=abc" {
		t.Errorf("已有 os 时不该重复拼接，得到 %q", got)
	}
}

// TestURLUsesConfiguredCookie 取链请求必须真的带上登录态。
//
// 匿名时官方接口只回 url:null，所以这条配置是「能不能取到地址」的关键。
func TestURLUsesConfiguredCookie(t *testing.T) {
	body := `{"data":[{"id":1,"url":"http://m8.music.126.net/a.mp3","br":320000,"code":200}]}`
	d := &fakeDoer{body: body}
	c := New(Config{EapiBase: "https://wy.invalid", Cookie: "MUSIC_U=abc"}, d)

	if _, err := c.URL(context.Background(), "1", "320k"); err != nil {
		t.Fatalf("URL: %v", err)
	}
	if len(d.reqs) == 0 {
		t.Fatal("没有发出请求")
	}
	if got := d.reqs[0].Header.Get("Cookie"); !strings.Contains(got, "MUSIC_U=abc") {
		t.Errorf("取链请求没带登录态 Cookie: %q", got)
	}
}
