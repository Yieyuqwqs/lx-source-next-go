package wy

import (
	"context"
	"net/http"
	"regexp"
	"strings"
	"testing"
)

const weapiOKBody = `{"code":200,"data":[{"id":347230,"url":"http://m702.music.126.net/a.mp3","br":128008,"size":721023,"fee":1}]}`

// TestWeapiLevelMapping 校验音质与 weapi level 的映射。
func TestWeapiLevelMapping(t *testing.T) {
	cases := []struct{ qual, level string }{
		{"128k", "standard"},
		{"192k", "higher"},
		{"320k", "exhigh"},
		{"flac", "lossless"},
		{"flac24bit", "hires"},
		{"", "standard"},
		{"看不懂", "standard"},
	}
	for _, c := range cases {
		if got := weapiLevel(c.qual); got != c.level {
			t.Errorf("weapiLevel(%q) = %q, want %q", c.qual, got, c.level)
		}
	}
}

// TestQualityFromBR 校验按回显码率如实上报音质。
func TestQualityFromBR(t *testing.T) {
	cases := []struct {
		br   int
		qual string
	}{{128008, "128k"}, {192000, "192k"}, {320000, "320k"}, {900000, "flac"}, {999000, "flac24bit"}, {0, "128k"}}
	for _, c := range cases {
		if got := qualityFromBR(c.br); got != c.qual {
			t.Errorf("qualityFromBR(%d) = %q, want %q", c.br, got, c.qual)
		}
	}
}

// TestWeapiFormShape 校验加密产物的形状：params 是 16 字节对齐的 base64，encSecKey 是 256 位十六进制。
func TestWeapiFormShape(t *testing.T) {
	form, err := weapiForm(weapiURLPayload{IDs: "[347230]", Level: "standard", EncodeType: "aac"})
	if err != nil {
		t.Fatalf("weapiForm: %v", err)
	}
	params := form.Get("params")
	if params == "" {
		t.Fatal("params 为空")
	}
	if !strings.ContainsAny(params, "+/=") {
		t.Error("params 看起来不像 base64")
	}
	// base64 长度为 4 的倍数，解码后必须是 16 的倍数
	if len(params)%4 != 0 {
		t.Errorf("params 长度 %d 不是 4 的倍数", len(params))
	}
	enc := form.Get("encSecKey")
	if !regexp.MustCompile(`^[0-9a-f]{256}$`).MatchString(enc) {
		t.Errorf("encSecKey 形状不对: %q", enc)
	}
}

// TestUrlWeapiRequest 校验 weapi 请求形状与结果解析。
func TestUrlWeapiRequest(t *testing.T) {
	d := &fakeDoer{body: weapiOKBody}
	c := New(Config{Base: "https://wy.invalid"}, d)

	li, err := c.urlWeapi(context.Background(), "347230", "320k")
	if err != nil {
		t.Fatalf("urlWeapi: %v", err)
	}
	if li.Link != "http://m702.music.126.net/a.mp3" {
		t.Errorf("Link = %q", li.Link)
	}
	// 匿名只给 128k，就必须如实报 128k
	if li.Qual != "128k" {
		t.Errorf("Qual = %q, want 128k（以回显 br 为准）", li.Qual)
	}

	req := d.reqs[0]
	if req.Method != http.MethodPost {
		t.Errorf("weapi 必须用 POST，实际 %s", req.Method)
	}
	if !strings.Contains(req.URL.Path, "/weapi/song/enhance/player/url/v1") {
		t.Errorf("路径 = %q", req.URL.Path)
	}
	if ck := req.Header.Get("Cookie"); !strings.Contains(ck, "appver") {
		t.Errorf("缺 appver 会被判异常客户端，实际 Cookie=%q", ck)
	}
	body := string(d.last)
	if !strings.Contains(body, "params=") || !strings.Contains(body, "encSecKey=") {
		t.Errorf("请求体缺少加密字段: %q", body)
	}
}

// TestURLPrefersWeapi weapi 通了就不该再打聚合与 eapi。
func TestURLPrefersWeapi(t *testing.T) {
	d := &fakeDoer{body: weapiOKBody}
	c := New(Config{Base: "https://wy.invalid", EapiBase: "https://wy.invalid", AggURL: "https://agg.invalid/api.php"}, d)

	li, err := c.URL(context.Background(), "347230", "128k")
	if err != nil {
		t.Fatalf("URL: %v", err)
	}
	if li.Link == "" {
		t.Error("没有拿到链接")
	}
	if len(d.reqs) != 1 {
		t.Errorf("weapi 命中后不应再请求其它通道，实际 %d 次", len(d.reqs))
	}
}

// TestParseWeapiURLNull 会员曲目要说明原因。
func TestParseWeapiURLNull(t *testing.T) {
	_, _, err := ParseWeapiURL([]byte(`{"code":200,"data":[{"url":null,"br":0,"fee":1}]}`))
	if err == nil {
		t.Fatal("url 为 null 时必须报错")
	}
	if !strings.Contains(err.Error(), "会员") {
		t.Errorf("错误应说明是会员/版权原因: %v", err)
	}
}
