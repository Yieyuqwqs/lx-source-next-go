package qq

import (
	"context"
	"strings"
	"testing"
)

// 歌词接口返回 base64；这两个常量是 "测试歌词" 与英文翻译的 base64。
const (
	lrcB64   = "WzAwOjAxLjAwXea1i+ivleatjOivjQpbMDA6MDIuMDBd56ys5LqM6KGM"
	transB64 = "WzAwOjAxLjAwXXRlc3QgbHlyaWM="
)

// TestLyric 校验歌词请求形状与 base64 解码。
func TestLyric(t *testing.T) {
	body := `{"retcode":0,"code":0,"lyric":"` + lrcB64 + `","trans":"` + transB64 + `"}`
	d := &fakeDoer{body: []byte(body)}
	c := New(Config{SearchBase: "https://qq.invalid"}, d)

	li, err := c.Lyric(context.Background(), "0027oMO61wWi55")
	if err != nil {
		t.Fatalf("Lyric: %v", err)
	}
	if !strings.Contains(li.Lyric, "测试歌词") {
		t.Errorf("歌词解码不对: %q", li.Lyric)
	}
	if !strings.Contains(li.TLyric, "test lyric") {
		t.Errorf("翻译解码不对: %q", li.TLyric)
	}

	req := d.reqs[0]
	if !strings.Contains(req.URL.Path, "fcg_query_lyric_new.fcg") {
		t.Errorf("路径 = %q", req.URL.Path)
	}
	if req.URL.Query().Get("songmid") != "0027oMO61wWi55" {
		t.Errorf("songmid = %q", req.URL.Query().Get("songmid"))
	}
	if req.Header.Get("Referer") == "" {
		t.Error("缺 Referer")
	}
}

// TestLyricEmpty 没有歌词时要说明原因。
func TestLyricEmpty(t *testing.T) {
	d := &fakeDoer{body: []byte(`{"retcode":0,"lyric":"","trans":""}`)}
	c := New(Config{SearchBase: "https://qq.invalid"}, d)
	if _, err := c.Lyric(context.Background(), "x"); err == nil {
		t.Fatal("空歌词应报错")
	} else if !strings.Contains(err.Error(), "歌词") {
		t.Errorf("错误应说明没有歌词: %v", err)
	}
}
