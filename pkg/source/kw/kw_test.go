package kw

import (
	"context"
	"encoding/base64"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/lxsrcs/lx-source-next/pkg/apierr"
	"github.com/lxsrcs/lx-source-next/pkg/quality"
	"github.com/lxsrcs/lx-source-next/pkg/source"
)

var testKey = []byte("12345678")

// sampleLyricPayloadB64 是一份按真实链路构造的歌词响应（102 字节）。
//
// 明文："[ti:test]\n[ar:test]\n[00:01.00]hello\n[00:02.50]world\n"
// 它是黄金值：三段包装（头部 + zlib + Base64 + 7 字节异或）任何一处写错，
// 解码结果都会变味，因此不必再另外导出中间态。
const sampleLyricPayloadB64 = "VFA9Y29udGVudA0KbHJjeD0xDQoNCnic88xw9Q3LMPD2CDQwSapK9vQIC/RzdIqM9HMPNQwOC0uLcMtwisjONnEJzHZzLHerCsgJSw41tEwOM3QK8w0ycfRwCnVKKre1BQA8lBaa"

// sampleLyricPayload 返回解码后的样本请求体。
func sampleLyricPayload(t *testing.T) []byte {
	t.Helper()
	b, err := base64.StdEncoding.DecodeString(sampleLyricPayloadB64)
	if err != nil {
		t.Fatalf("样本 Base64 解码失败: %v", err)
	}
	return b
}

func TestDesRoundTrip(t *testing.T) {
	cases := []string{"a", "hello world", "type=convert_url2&rid=123", strings.Repeat("x", 200)}
	for _, in := range cases {
		enc, err := DesEncrypt([]byte(in), testKey)
		if err != nil {
			t.Fatalf("加密 %q: %v", in, err)
		}
		dec, err := DesDecrypt(enc, testKey)
		if err != nil {
			t.Fatalf("解密 %q: %v", in, err)
		}
		if string(dec) != in {
			t.Errorf("往返不一致: %q -> %q", in, dec)
		}
	}
}

func TestDesKeyValidation(t *testing.T) {
	for _, k := range [][]byte{nil, []byte("short"), []byte("too-long-key")} {
		if _, err := DesEncrypt([]byte("x"), k); err == nil {
			t.Errorf("密钥 %d 字节应报错", len(k))
		}
	}
}

func TestDesDecryptErrors(t *testing.T) {
	if _, err := DesDecrypt("!!!not-base64!!!", testKey); err == nil {
		t.Error("非法 Base64 应报错")
	}
	// 长度不是 8 的倍数
	raw, _ := DesEncrypt([]byte("hello"), testKey)
	if _, err := DesDecrypt(raw+"AAAA", testKey); err == nil {
		t.Error("非法密文长度应报错")
	}
}

func TestBuildMobiQuery(t *testing.T) {
	params := url.Values{}
	params.Set("rid", "123")
	params.Set("br", "320kmp3")

	q, err := BuildMobiQuery(params, testKey)
	if err != nil {
		t.Fatalf("BuildMobiQuery: %v", err)
	}
	if q == "" {
		t.Fatal("q 不应为空")
	}
	// q 必须能解回原始参数
	plain, err := DesDecrypt(q, testKey)
	if err != nil {
		t.Fatalf("解密 q: %v", err)
	}
	v, err := url.ParseQuery(string(plain))
	if err != nil {
		t.Fatalf("解析明文: %v", err)
	}
	if v.Get("rid") != "123" || v.Get("br") != "320kmp3" {
		t.Errorf("明文 = %q", plain)
	}

	if _, err := BuildMobiQuery(url.Values{}, testKey); err == nil {
		t.Error("空参数应报错")
	}
}

func TestParseConvertURL(t *testing.T) {
	cases := []struct {
		name string
		body string
		want string
	}{
		{"对象形态", `{"code":200,"data":{"url":"https://kw.invalid/a.mp3","bitrate":"320kmp3"}}`, "https://kw.invalid/a.mp3"},
		{"字符串形态", `{"code":200,"data":"https://kw.invalid/b.mp3"}`, "https://kw.invalid/b.mp3"},
		{"平铺形态", `{"url":"https://kw.invalid/c.mp3"}`, "https://kw.invalid/c.mp3"},
		{"备选字段名", `{"code":200,"data":{"playUrl":"https://kw.invalid/d.mp3"}}`, "https://kw.invalid/d.mp3"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			res, err := ParseConvertURL([]byte(c.body))
			if err != nil {
				t.Fatalf("ParseConvertURL: %v", err)
			}
			if res.URL != c.want {
				t.Errorf("URL = %q, want %q", res.URL, c.want)
			}
		})
	}
}

func TestParseConvertURLErrors(t *testing.T) {
	bad := []string{
		``,
		`not json`,
		`{"code":400,"msg":"错误请求"}`,
		`{"code":200,"data":{}}`,
	}
	for _, b := range bad {
		if _, err := ParseConvertURL([]byte(b)); err == nil {
			t.Errorf("%q 应报错", b)
		}
	}
	// 上游错误码要把 msg 带出来
	_, err := ParseConvertURL([]byte(`{"code":400,"msg":"签名错误"}`))
	if err == nil || !strings.Contains(err.Error(), "签名错误") {
		t.Errorf("应带出上游 msg，实际 %v", err)
	}
}

func TestParseLyricPayload(t *testing.T) {
	lrc, err := ParseLyricPayload(sampleLyricPayload(t))
	if err != nil {
		t.Fatalf("ParseLyricPayload: %v", err)
	}
	for _, want := range []string{"[ti:test]", "[ar:test]", "[00:01.00]hello", "[00:02.50]world"} {
		if !strings.Contains(lrc, want) {
			t.Errorf("歌词缺少 %q，实际 %q", want, lrc)
		}
	}
	if !strings.HasSuffix(lrc, "\n") {
		t.Errorf("应以换行结尾，实际 %q", lrc)
	}

	// 上游明确说没有歌词：要能和「解析失败」区分开
	if _, err := ParseLyricPayload([]byte("TP=none\n")); !errors.Is(err, ErrNoLyric) {
		t.Errorf("TP=none 应返回 ErrNoLyric，实际 %v", err)
	}
	if _, err := ParseLyricPayload(nil); err == nil {
		t.Error("空响应应报错")
	}
	if _, err := ParseLyricPayload([]byte("<html>502</html>")); err == nil {
		t.Error("非法头部应报错")
	}
	if _, err := ParseLyricPayload([]byte("TP=content\r\nlrcx=1\r\n\r\n")); err == nil {
		t.Error("载荷不足应报错")
	}
	// 解压后不是 Base64 / 没有时间轴，都不能当成功
	if _, err := ParseLyricPayload([]byte("TP=content\r\nlrcx=1\r\n\r\nnot-zlib")); err == nil {
		t.Error("非 zlib 载荷应报错")
	}
}

// fakeDoer 记录请求并返回固定响应。
type fakeDoer struct {
	reqs   []*http.Request
	body   string
	status int
	err    error
}

func (f *fakeDoer) Do(req *http.Request) (*http.Response, error) {
	f.reqs = append(f.reqs, req)
	if f.err != nil {
		return nil, f.err
	}
	st := f.status
	if st == 0 {
		st = http.StatusOK
	}
	return &http.Response{
		StatusCode: st,
		Header:     make(http.Header),
		Body:       io.NopCloser(strings.NewReader(f.body)),
	}, nil
}

func TestClientNotConfigured(t *testing.T) {
	c := New(Config{}, &fakeDoer{})
	if _, err := c.URL(context.Background(), "1", "320k"); err == nil {
		t.Error("未配置上游地址应报错")
	} else if e, ok := apierr.From(err); !ok || e.Code != apierr.CodeUnavailable {
		t.Errorf("错误码应为 unavailable，实际 %v", err)
	}

	c = New(Config{BaseURL: "https://x.invalid"}, &fakeDoer{})
	if _, err := c.URL(context.Background(), "1", "320k"); err == nil {
		t.Error("未配置密钥应报错")
	}
}

func TestClientURLSuccess(t *testing.T) {
	d := &fakeDoer{body: `{"code":200,"data":{"url":"https://kw.invalid/a.mp3","bitrate":"320kmp3"}}`}
	fixed := time.Unix(1700000000, 0)
	c := New(Config{BaseURL: "https://mobi.kuwo.cn", Key: testKey, Expire: 30 * time.Minute}, d).
		WithClock(func() time.Time { return fixed })

	li, err := c.URL(context.Background(), "123", "320k")
	if err != nil {
		t.Fatalf("URL: %v", err)
	}
	if li.Link != "https://kw.invalid/a.mp3" {
		t.Errorf("Link = %q", li.Link)
	}
	if li.Qual != quality.Q320 {
		t.Errorf("Qual = %q", li.Qual)
	}
	if li.Expi != fixed.Add(30*time.Minute).Unix() {
		t.Errorf("Expi = %d", li.Expi)
	}

	// 校验真实发出的请求：路径、f 参数、以及 q 能否解回原参数
	if len(d.reqs) != 1 {
		t.Fatalf("请求次数 = %d", len(d.reqs))
	}
	req := d.reqs[0]
	if !strings.HasSuffix(req.URL.Path, "/mobi.s") {
		t.Errorf("请求路径 = %q", req.URL.Path)
	}
	q := req.URL.Query()
	if q.Get("f") != "kuwo" {
		t.Errorf("f = %q", q.Get("f"))
	}
	plain, err := DesDecrypt(q.Get("q"), testKey)
	if err != nil {
		t.Fatalf("解出 q: %v", err)
	}
	v, err := url.ParseQuery(string(plain))
	if err != nil {
		t.Fatalf("解析 q 明文: %v", err)
	}
	for k, want := range map[string]string{
		"type": "convert_url2",
		"br":   br320,
		"rid":  "123",
		"corp": "kuwo",
	} {
		if got := v.Get(k); got != want {
			t.Errorf("参数 %s = %q, want %q", k, got, want)
		}
	}
}

func TestClientUpstreamErrors(t *testing.T) {
	// 上游非 200
	c := New(Config{BaseURL: "https://x.invalid", Key: testKey}, &fakeDoer{status: 502})
	if _, err := c.URL(context.Background(), "1", "320k"); err == nil {
		t.Error("HTTP 502 应报错")
	}
	// 网络错误
	c = New(Config{BaseURL: "https://x.invalid", Key: testKey}, &fakeDoer{err: io.ErrUnexpectedEOF})
	if _, err := c.URL(context.Background(), "1", "320k"); err == nil {
		t.Error("网络错误应报错")
	}
	// 上游 JSON 报错
	c = New(Config{BaseURL: "https://x.invalid", Key: testKey}, &fakeDoer{body: `{"code":400,"msg":"签名错误"}`})
	if _, err := c.URL(context.Background(), "1", "320k"); err == nil {
		t.Error("上游 code=400 应报错")
	}
}

func TestClientLyric(t *testing.T) {
	d := &fakeDoer{body: string(sampleLyricPayload(t))}
	c := New(Config{BaseURL: "https://mobi.kuwo.cn", Key: testKey, LyricBaseURL: "https://mlyric.invalid"}, d)

	// 带 MUSIC_ 前缀的 id 要能容忍
	li, err := c.Lyric(context.Background(), "MUSIC_123")
	if err != nil {
		t.Fatalf("Lyric: %v", err)
	}
	if !strings.Contains(li.Lyric, "[00:01.00]hello") {
		t.Errorf("Lyric = %q", li.Lyric)
	}

	got := d.reqs[0].URL
	if got.Host != "mlyric.invalid" || got.Path != "/mobi.s" {
		t.Errorf("歌词地址 = %s", got)
	}
	q := got.Query()
	if q.Get("rid") != "123" || q.Get("type") != "lyric" || q.Get("lrcx") != "0" || q.Get("f") != "web" {
		t.Errorf("查询参数 = %s", got.RawQuery)
	}
}

func TestClientLyricNoLyric(t *testing.T) {
	c := New(Config{LyricBaseURL: "https://mlyric.invalid"}, &fakeDoer{body: "TP=none\n"})
	_, err := c.Lyric(context.Background(), "1")
	if err == nil {
		t.Fatal("没有歌词应报错")
	}
	if e, ok := apierr.From(err); !ok || e.Code != apierr.CodeUnavailable {
		t.Errorf("错误码应为 unavailable，实际 %v", err)
	}

	// 空 id 是参数错误，不该打上游
	d := &fakeDoer{}
	c = New(Config{LyricBaseURL: "https://mlyric.invalid"}, d)
	if _, err := c.Lyric(context.Background(), "  "); err == nil {
		t.Error("空 id 应报错")
	}
	if len(d.reqs) != 0 {
		t.Errorf("空 id 不该发请求，实际 %d 次", len(d.reqs))
	}
}

func TestClientUnspportedCapabilities(t *testing.T) {
	c := New(Config{BaseURL: "https://x.invalid", Key: testKey}, &fakeDoer{})
	ctx := context.Background()
	if _, err := c.Movie(ctx, "1", "720p"); err == nil {
		t.Error("Movie 应返回未实现错误")
	}
	if _, err := c.Music(ctx, "1"); err == nil {
		t.Error("Music 应返回未实现错误")
	}
	if _, err := c.Video(ctx, "1"); err == nil {
		t.Error("Video 应返回未实现错误")
	}
	if _, err := c.Pic(ctx, "1", "m"); err == nil {
		t.Error("Pic 应返回未实现错误")
	}
}

func TestClientImplementsSource(t *testing.T) {
	var s source.Source = New(Config{}, nil)
	if s.Src() != "kw" {
		t.Errorf("Src = %q", s.Src())
	}
}

func TestParseAntiURL(t *testing.T) {
	ok := map[string]string{
		`https://kw.invalid/a.mp3`:                       "https://kw.invalid/a.mp3",
		"  https://kw.invalid/b.mp3\n":                   "https://kw.invalid/b.mp3",
		"https://kw.invalid/c.mp3\r\nextra":              "https://kw.invalid/c.mp3",
		`{"code":200,"data":"https://kw.invalid/d.mp3"}`: "https://kw.invalid/d.mp3",
	}
	for in, want := range ok {
		got, err := ParseAntiURL([]byte(in))
		if err != nil {
			t.Errorf("ParseAntiURL(%q) 报错: %v", in, err)
			continue
		}
		if got != want {
			t.Errorf("ParseAntiURL(%q) = %q, want %q", in, got, want)
		}
	}

	bad := []string{"", "   ", "not a url", "<html>502</html>"}
	for _, b := range bad {
		if _, err := ParseAntiURL([]byte(b)); err == nil {
			t.Errorf("ParseAntiURL(%q) 应报错", b)
		}
	}
}

func TestAntiNormalizeQuality(t *testing.T) {
	c := New(Config{Strategy: StrategyAnti}, nil)
	// anti 只支持一档，任何请求都归一
	for _, in := range []string{"128k", "192k", "320k", "flac", "flac24bit", "master", "hires"} {
		if got := c.NormalizeQuality(in); got != DefaultAntiQuality {
			t.Errorf("NormalizeQuality(%q) = %q, want %q", in, got, DefaultAntiQuality)
		}
	}
}

func TestMobiNormalizeQuality(t *testing.T) {
	c := New(Config{Strategy: StrategyMobi, Key: testKey}, nil)
	cases := map[string]string{
		"128k":   quality.Q128,
		"320k":   quality.Q320,
		"flac":   quality.QFlac,
		"hires":  quality.QFlac24,
		"master": quality.QFlac24, // 回落
	}
	for in, want := range cases {
		if got := c.NormalizeQuality(in); got != want {
			t.Errorf("NormalizeQuality(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestAntiReportsConservativeQuality(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		io.WriteString(w, "https://kw.invalid/x.mp3\n")
	}))
	defer srv.Close()

	c := New(Config{Strategy: StrategyAnti, BaseURL: srv.URL}, nil)
	// 请求 flac，但 anti 无法保证无损，必须如实低报
	li, err := c.URL(context.Background(), "1", "flac")
	if err != nil {
		t.Fatalf("URL: %v", err)
	}
	if li.Qual != DefaultAntiQuality {
		t.Errorf("Qual = %q, want %q（不得谎报无损）", li.Qual, DefaultAntiQuality)
	}
}

func TestAntiQualityConfigurable(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		io.WriteString(w, "https://kw.invalid/x.mp3\n")
	}))
	defer srv.Close()

	c := New(Config{Strategy: StrategyAnti, BaseURL: srv.URL, AntiQuality: "320k"}, nil)
	if got := c.NormalizeQuality("flac"); got != quality.Q320 {
		t.Errorf("可配置音质未生效: %q", got)
	}
}
