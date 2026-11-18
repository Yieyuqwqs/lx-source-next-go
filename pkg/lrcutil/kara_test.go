package lrcutil

import (
	"encoding/base64"
	"strings"
	"testing"
)

// 以下 fixtures 由参考实现的常量生成，用于跨实现校验：
// encrypt(KRC/QRC) 由参考算法产出二进制，本包的 Decode/Parse 必须原样还原。

const (
	fxLrcx  = "[00:00.000]<0,36>测<36,36>试<50,60>歌<80,75>词"
	fxKRC   = "a3JjMTjb6lmXhg7x5M3012cduJ+ENSRQSBPMiGuNUpRs32NlTDNNO1NQGUslejwhut6CVRI0E4UzchgkAiYOEXH6SWxAp29r5w=="
	fxQRC   = "mCWwrOMCg2jo/Gz61p8BHpsnO8ZGn/K1CSPNhwx1yFNV690Xulw+BGX6RhvmEhWnNRAdgbkmBVvdZJcADjON44RHMVXXIuN/UqXAwQMfcWsg6zKej/D+QPy4sIKqMRcsFZWotb5H2XE6bVhXVUoy9ajA1P3M0qSVJLl0LouPLgd+l/3uwJgxGpEcA9MhUnOGGAfzi1MRqFYzX0jVgiDMKI4Rm+WArtYSwFuR0p6Hbna3SYQ="
	fxNRC   = "[1000,500](1000,250,0)你(1250,250,0)好"
)

func mustBase64(t *testing.T, s string) []byte {
	t.Helper()
	b, err := base64.StdEncoding.DecodeString(s)
	if err != nil {
		t.Fatalf("fixture base64 解码失败: %v", err)
	}
	return b
}

func TestParseLrcx(t *testing.T) {
	k, err := ParseLrcx(fxLrcx)
	if err != nil {
		t.Fatalf("ParseLrcx: %v", err)
	}
	if len(k.Lines) != 1 {
		t.Fatalf("行数 = %d, want 1", len(k.Lines))
	}
	ln := k.Lines[0]
	if ln.Start != 0 {
		t.Errorf("行起始 = %d, want 0", ln.Start)
	}
	if got := ln.Text(); got != "测试歌词" {
		t.Errorf("文本 = %q, want 测试歌词", got)
	}
	if len(ln.Words) != 4 {
		t.Fatalf("词数 = %d, want 4", len(ln.Words))
	}
	cases := []struct {
		start, dur int64
		text       string
	}{{0, 36, "测"}, {36, 36, "试"}, {50, 60, "歌"}, {80, 75, "词"}}
	for i, c := range cases {
		w := ln.Words[i]
		if w.Start != c.start || w.Dur != c.dur || w.Text != c.text {
			t.Errorf("words[%d] = %+v, want {%d %d %q}", i, w, c.start, c.dur, c.text)
		}
	}
}

func TestLrcxRoundTrip(t *testing.T) {
	k, err := ParseLrcx(fxLrcx)
	if err != nil {
		t.Fatalf("ParseLrcx: %v", err)
	}
	if got, want := strings.TrimSpace(k.ToLrcx()), strings.TrimSpace(fxLrcx); got != want {
		t.Errorf("往返\n got  %q\n want %q", got, want)
	}
}

func TestDecodeKRC(t *testing.T) {
	bin := mustBase64(t, fxKRC)
	plain, err := DecodeKRC(bin)
	if err != nil {
		t.Fatalf("DecodeKRC: %v", err)
	}
	if !strings.Contains(plain, "ti:测试歌") {
		t.Fatalf("明文缺少 ti 标签: %q", plain)
	}

	k, err := ParseKRCBytes(bin)
	if err != nil {
		t.Fatalf("ParseKRCBytes: %v", err)
	}
	if k.Meta["ti"] != "测试歌" {
		t.Errorf("ti = %q", k.Meta["ti"])
	}
	if k.Meta["ar"] != "测试歌手" {
		t.Errorf("ar = %q", k.Meta["ar"])
	}
	if len(k.Lines) != 1 {
		t.Fatalf("行数 = %d, want 1", len(k.Lines))
	}
	ln := k.Lines[0]
	if ln.Start != 1000 || ln.Dur != 850 {
		t.Errorf("行 = {%d %d}, want {1000 850}", ln.Start, ln.Dur)
	}
	if got := ln.Text(); got != "HelloWorld" {
		t.Errorf("文本 = %q", got)
	}
	if len(ln.Words) != 2 || ln.Words[1].Start != 500 {
		t.Errorf("词 = %+v", ln.Words)
	}
}

func TestKRCEncodeRoundTrip(t *testing.T) {
	bin := mustBase64(t, fxKRC)
	plain, err := DecodeKRC(bin)
	if err != nil {
		t.Fatalf("DecodeKRC: %v", err)
	}
	repacked, err := EncodeKRC(plain)
	if err != nil {
		t.Fatalf("EncodeKRC: %v", err)
	}
	back, err := DecodeKRC(repacked)
	if err != nil {
		t.Fatalf("重打包后 DecodeKRC: %v", err)
	}
	if back != plain {
		t.Errorf("打包往返不一致")
	}
}

func TestQRC(t *testing.T) {
	bin := mustBase64(t, fxQRC)
	doc, err := DecodeQRC(bin)
	if err != nil {
		t.Fatalf("DecodeQRC: %v", err)
	}
	if !strings.Contains(doc, "QrcInfos") {
		t.Fatalf("解密结果不是 XML: %q", doc)
	}

	content, err := ExtractQRCLyric(doc)
	if err != nil {
		t.Fatalf("ExtractQRCLyric: %v", err)
	}

	k, err := ParseQRCContent(content)
	if err != nil {
		t.Fatalf("ParseQRCContent: %v", err)
	}
	if len(k.Lines) != 2 {
		t.Fatalf("行数 = %d, want 2", len(k.Lines))
	}
	l1, l2 := k.Lines[0], k.Lines[1]
	if l1.Start != 0 || l1.Dur != 850 {
		t.Errorf("第1行 = {%d %d}, want {0 850}", l1.Start, l1.Dur)
	}
	if got := l1.Text(); got != "HelloWorld" {
		t.Errorf("第1行文本 = %q", got)
	}
	if len(l1.Words) != 2 || l1.Words[1].Start != 500 {
		t.Errorf("第1行词 = %+v", l1.Words)
	}
	// 第2行的词时间是绝对值 2000/2250，归一化后应为 0/250
	if l2.Start != 2000 {
		t.Errorf("第2行起始 = %d, want 2000", l2.Start)
	}
	if len(l2.Words) != 2 || l2.Words[0].Start != 0 || l2.Words[1].Start != 250 {
		t.Errorf("第2行词归一化错误: %+v", l2.Words)
	}
}

func TestParseNRC(t *testing.T) {
	k, err := ParseNRC(fxNRC)
	if err != nil {
		t.Fatalf("ParseNRC: %v", err)
	}
	if len(k.Lines) != 1 {
		t.Fatalf("行数 = %d", len(k.Lines))
	}
	ln := k.Lines[0]
	if ln.Start != 1000 {
		t.Errorf("行起始 = %d, want 1000", ln.Start)
	}
	if got := ln.Text(); got != "你好" {
		t.Errorf("文本 = %q", got)
	}
	if len(ln.Words) != 2 || ln.Words[0].Start != 0 || ln.Words[1].Start != 250 {
		t.Errorf("词归一化错误: %+v", ln.Words)
	}
}

func TestToLRC(t *testing.T) {
	k, err := ParseKRCBytes(mustBase64(t, fxKRC))
	if err != nil {
		t.Fatalf("ParseKRCBytes: %v", err)
	}
	out := k.ToLRC()
	if !strings.Contains(out, "[00:01.00] HelloWorld") {
		t.Errorf("ToLRC 输出缺少歌词行:\n%s", out)
	}
	if !strings.Contains(out, "[ti:测试歌]") {
		t.Errorf("ToLRC 输出缺少标签:\n%s", out)
	}
}

func TestDecodeKRCRejectsBadMagic(t *testing.T) {
	if _, err := DecodeKRC([]byte("xxxx")); err == nil {
		t.Error("错误魔数应报错")
	}
	if _, err := DecodeKRC(nil); err == nil {
		t.Error("空输入应报错")
	}
}

func FuzzParseLrcx(f *testing.F) {
	f.Add(fxLrcx)
	f.Add("[00:00.000]<0,1>a")
	f.Add("[]<>")
	f.Fuzz(func(t *testing.T, src string) {
		k, err := ParseLrcx(src)
		if err != nil {
			return
		}
		for _, ln := range k.Lines {
			if ln.Start < 0 {
				t.Fatalf("负行起始 %d", ln.Start)
			}
			for _, w := range ln.Words {
				if w.Start < 0 || w.Dur < 0 {
					t.Fatalf("负词时间 %+v", w)
				}
			}
		}
	})
}

func FuzzDecodeKRC(f *testing.F) {
	f.Add(mustBase64Bytes(fxKRC))
	f.Fuzz(func(t *testing.T, data []byte) {
		// 只要求不 panic
		_, _ = DecodeKRC(data)
	})
}

func mustBase64Bytes(s string) []byte {
	b, _ := base64.StdEncoding.DecodeString(s)
	return b
}
