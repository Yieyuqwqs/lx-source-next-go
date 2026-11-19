package qq

import (
	"context"
	"strings"
	"testing"
)

// TestCookieUIN 校验从 Cookie 里取 QQ 号（带 o 前缀是常态）。
func TestCookieUIN(t *testing.T) {
	cases := []struct{ cookie, want string }{
		{"uin=o0123456789; qqmusic_key=abc", "0123456789"},
		{"qqmusic_key=abc; uin=1234567890", "1234567890"},
		{"p_uin=o998877", "998877"},
		{"qqmusic_key=abc", "0"},
		{"", "0"},
		{"uin=oabc", "0"},
	}
	for _, c := range cases {
		if got := cookieUIN(c.cookie); got != c.want {
			t.Errorf("cookieUIN(%q) = %q, want %q", c.cookie, got, c.want)
		}
	}
}

// TestURLSendsCredential 配了 Cookie 后：请求要带 uin（不是 0）与 Cookie 头。
func TestURLSendsCredential(t *testing.T) {
	d := &fakeDoer{body: []byte(vkeyBody)}
	c := New(Config{MusicURL: "https://qq.invalid/musics.fcg", Cookie: "uin=o0123456789; qqmusic_key=abc"}, d)

	if _, err := c.URL(context.Background(), "0027oMO61wWi55", "320k"); err != nil {
		t.Fatalf("URL: %v", err)
	}
	body := string(d.last)
	if !strings.Contains(body, `"uin":"0123456789"`) {
		t.Errorf("请求体没有带上 Cookie 里的 uin: %s", body)
	}
	if got := d.reqs[0].Header.Get("Cookie"); !strings.Contains(got, "qqmusic_key=abc") {
		t.Errorf("请求没带 Cookie: %q", got)
	}
}

// TestURLAnonymousUin 没配 Cookie 时退回匿名 uin=0（服务端会拒，但形状要对）。
func TestURLAnonymousUin(t *testing.T) {
	d := &fakeDoer{body: []byte(vkeyBody)}
	c := New(Config{MusicURL: "https://qq.invalid/musics.fcg"}, d)

	if _, err := c.URL(context.Background(), "0027oMO61wWi55", "128k"); err != nil {
		t.Fatalf("URL: %v", err)
	}
	if body := string(d.last); !strings.Contains(body, `"uin":"0"`) {
		t.Errorf("匿名时应使用 uin=0: %s", body)
	}
	if got := d.reqs[0].Header.Get("Cookie"); got != "" {
		t.Errorf("匿名时不该带 Cookie: %q", got)
	}
}
