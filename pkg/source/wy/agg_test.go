package wy

import (
	"context"
	"net/http"
	"strings"
	"testing"
)

const aggOKBody = `{"url":"https://m701.music.126.net/2026/a.mp3","br":320,"size":13042460}`

// TestAggURL 校验聚合取链：请求形状正确、音质以对端回显为准。
func TestAggURL(t *testing.T) {
	// 第一个空响应留给 weapi（URL() 的第一通道），聚合是第二个请求
	d := &fakeDoer{seq: []string{"", aggOKBody}}
	c := New(Config{AggURL: "https://agg.invalid/api.php"}, d)

	li, err := c.URL(context.Background(), "347230", "320k")
	if err != nil {
		t.Fatalf("URL: %v", err)
	}
	if li.Link != "https://m701.music.126.net/2026/a.mp3" {
		t.Errorf("Link = %q", li.Link)
	}
	if li.Qual != "320k" {
		t.Errorf("Qual = %q", li.Qual)
	}
	if len(d.reqs) != 2 {
		t.Fatalf("应为 weapi 失败 + 聚合命中两次请求，实际 %d 次", len(d.reqs))
	}
	req := d.reqs[1]
	if req.Method != http.MethodGet {
		t.Errorf("聚合请求必须是 GET，实际 %s", req.Method)
	}
	if !strings.HasSuffix(req.URL.Path, "/api.php") {
		t.Errorf("路径 = %q", req.URL.Path)
	}
	q := req.URL.Query()
	if q.Get("types") != "url" || q.Get("source") != "netease" {
		t.Errorf("查询参数 = %v", q)
	}
	if q.Get("id") != "347230" || q.Get("br") != "320" {
		t.Errorf("id/br = %q/%q", q.Get("id"), q.Get("br"))
	}
}

// TestAggQualityFromEcho 对端只给 128k 时要如实低报。
func TestAggQualityFromEcho(t *testing.T) {
	d := &fakeDoer{body: `{"url":"https://x/a.mp3","br":128,"size":1}`}
	c := New(Config{AggURL: "https://agg.invalid/api.php"}, d)
	li, err := c.URL(context.Background(), "1", "flac")
	if err != nil {
		t.Fatalf("URL: %v", err)
	}
	if li.Qual != "128k" {
		t.Errorf("要的是 flac、对端给 128，Qual 应为 128k，实际 %q", li.Qual)
	}
}

// TestAggFallsBackToEapi 聚合拿不到时退回官方接口。
func TestAggFallsBackToEapi(t *testing.T) {
	eapiBody := `{"data":[{"id":1,"url":"http://m8.music.126.net/e.mp3","br":320000,"code":200}]}`
	d := &fakeDoer{seq: []string{"", `{"url":"","br":0,"size":0}`, eapiBody}}
	c := New(Config{EapiBase: "https://wy.invalid", AggURL: "https://agg.invalid/api.php"}, d)

	li, err := c.URL(context.Background(), "1", "320k")
	if err != nil {
		t.Fatalf("URL: %v", err)
	}
	if li.Link != "http://m8.music.126.net/e.mp3" {
		t.Errorf("Link = %q", li.Link)
	}
	if len(d.reqs) != 3 {
		t.Fatalf("应为 weapi + 聚合 + 官方三次请求，实际 %d 次", len(d.reqs))
	}
	if d.reqs[2].Method != http.MethodPost {
		t.Errorf("官方接口应为 POST，实际 %s", d.reqs[2].Method)
	}
}

// TestAggBothFail 两条路都失败时，错误里要同时说明两个原因。
func TestAggBothFail(t *testing.T) {
	d := &fakeDoer{seq: []string{"", `{"url":"","br":0,"size":0}`, `{"data":[{"url":null,"code":404}]}`}}
	c := New(Config{EapiBase: "https://wy.invalid", AggURL: "https://agg.invalid/api.php"}, d)

	_, err := c.URL(context.Background(), "1", "128k")
	if err == nil {
		t.Fatal("两条路都不通时必须报错")
	}
	msg := err.Error()
	for _, want := range []string{"weapi", "聚合", "eapi"} {
		if !strings.Contains(msg, want) {
			t.Errorf("错误应带 %s 的原因，实际: %v", want, msg)
		}
	}
}

// TestAggBRMapping 校验音质与聚合 br 的双向映射。
func TestAggBRMapping(t *testing.T) {
	cases := []struct {
		qual string
		br   int
	}{{"128k", 128}, {"320k", 320}, {"flac", 740}, {"flac24bit", 999}, {"", 320}}
	for _, c := range cases {
		if got := aggBR(c.qual); got != c.br {
			t.Errorf("aggBR(%q) = %d, want %d", c.qual, got, c.br)
		}
	}
	back := []struct {
		br   int
		qual string
	}{{128, "128k"}, {192, "192k"}, {320, "320k"}, {740, "flac"}, {999, "flac24bit"}, {0, "128k"}}
	for _, c := range back {
		if got := aggQuality(c.br); got != c.qual {
			t.Errorf("aggQuality(%d) = %q, want %q", c.br, got, c.qual)
		}
	}
}
