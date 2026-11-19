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
	d := &fakeDoer{seq: [][]byte{[]byte(infoBody), []byte(vkeyBody)}}
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

// TestURLAnonymousUin 没配凭据时用内置的匿名 QQ 号（这正是匿名能拿到 vkey 的原因）。
func TestURLAnonymousUin(t *testing.T) {
	d := &fakeDoer{seq: [][]byte{[]byte(infoBody), []byte(vkeyBody)}}
	c := New(Config{MusicURL: "https://qq.invalid/musics.fcg"}, d)

	if _, err := c.URL(context.Background(), "0027oMO61wWi55", "128k"); err != nil {
		t.Fatalf("URL: %v", err)
	}
	if body := string(d.last); !strings.Contains(body, `"uin":"1535153710"`) {
		t.Errorf("匿名时应使用内置匿名 uin: %s", body)
	}
	if got := d.reqs[0].Header.Get("Cookie"); got != "" {
		t.Errorf("匿名时不该带 Cookie: %q", got)
	}
}

// infoBody 是 get_song_detail_yqq 的最小响应（取链第一步的曲目信息）。
const infoBody = `{"req":{"code":0,"data":{"track_info":{"name":"晴天","file":{"media_mid":"003Qui1q2u1Zho"},"pay":{"pay_play":0},"vs":["064YyRNb0gDV4P"]}}}}`
