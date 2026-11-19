package kg

import (
	"context"
	"strings"
	"testing"
)

// lrcB64 是 "[00:01.00]测试歌词" 的 base64。
const lrcB64 = "WzAwOjAxLjAwXea1i+ivleatjOivjQpbMDA6MDIuMDBd56ys5LqM6KGM"

// TestLyric 校验「候选 → 下载」两步流程与 base64 解码。
func TestLyric(t *testing.T) {
	search := `{"status":200,"candidates":[{"id":"346467937","accesskey":"KEY123","song":"海阔天空","singer":"BEYOND"}]}`
	download := `{"status":200,"fmt":"lrc","content":"` + lrcB64 + `"}`
	d := &fakeDoer{seq: []string{search, download}}
	c := New(Config{}, d)

	li, err := c.Lyric(context.Background(), "C41E80A18D1448FA47086372999C7F43")
	if err != nil {
		t.Fatalf("Lyric: %v", err)
	}
	if !strings.Contains(li.Lyric, "测试歌词") {
		t.Errorf("歌词解码不对: %q", li.Lyric)
	}
	if len(d.reqs) != 2 {
		t.Fatalf("应为候选 + 下载两次请求，实际 %d 次", len(d.reqs))
	}
	if d.reqs[0].URL.Query().Get("hash") != "C41E80A18D1448FA47086372999C7F43" {
		t.Errorf("hash = %q", d.reqs[0].URL.Query().Get("hash"))
	}
	if d.reqs[1].URL.Query().Get("accesskey") != "KEY123" || d.reqs[1].URL.Query().Get("fmt") != "lrc" {
		t.Errorf("下载参数 = %v", d.reqs[1].URL.Query())
	}
	for i, r := range d.reqs {
		if r.Header.Get("Referer") == "" {
			t.Errorf("第 %d 个请求缺 Referer", i+1)
		}
	}
}

// TestLyricNoCandidate 没候选时给明确错误。
func TestLyricNoCandidate(t *testing.T) {
	d := &fakeDoer{seq: []string{`{"status":200,"candidates":[]}`}}
	c := New(Config{}, d)
	_, err := c.Lyric(context.Background(), "abc")
	if err == nil || !strings.Contains(err.Error(), "候选") {
		t.Errorf("应提示没有候选: %v", err)
	}
}
