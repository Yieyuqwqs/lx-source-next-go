package mg

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

const searchJSON = `{"code":"000000","info":"成功","songResultData":{"result":[
{"contentId":"600902000008474046","copyrightId":"60053905618","name":"纯音乐钢琴",
 "singers":[{"name":"某人"}],"albums":[{"name":"专辑"}],
 "newRateFormats":[{"formatType":"PQ","size":"3834150"},{"formatType":"HQ","size":"9585060"},
                   {"formatType":"SQ","size":"23979247"},{"formatType":"ZQ","size":"45019059"}]}]}}`

// TestSearch 解析搜索结果并带上各档音质规格。
func TestSearch(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.Contains(r.URL.Path, "search_all.do") {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(searchJSON))
	}))
	defer srv.Close()

	c := New(Config{Base: srv.URL}, nil)
	res, err := c.Search(context.Background(), "纯音乐", 1, 10)
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if len(res.Items) != 1 {
		t.Fatalf("Items = %d", len(res.Items))
	}
	it := res.Items[0]
	if it.ID != "600902000008474046" || it.Name != "纯音乐钢琴" {
		t.Errorf("item = %+v", it)
	}
	if len(it.File) != 4 {
		t.Fatalf("File = %+v", it.File)
	}
	if it.File[3].Qual != "flac24bit" {
		t.Errorf("24bit 档位 = %q", it.File[3].Qual)
	}
	// 搜索时要记住 copyrightId，取链要用
	if c.copyrightOf(it.ID) != "60053905618" {
		t.Errorf("没有记住 copyrightId")
	}
}

// TestURLFollows302 302 的 Location 就是真实地址。
func TestURLFollows302(t *testing.T) {
	var gotTone string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotTone = r.URL.Query().Get("toneFlag")
		if r.URL.Query().Get("toneFlag") == "HQ" {
			w.Header().Set("Location", "https://freetyst.example/abc.mp3")
			w.WriteHeader(http.StatusFound)
			return
		}
		w.Write([]byte(`{"code":"200000","info":"暂不提供试听地址"}`))
	}))
	defer srv.Close()

	c := New(Config{Base: srv.URL}, nil)
	c.remember("cid", "crid")

	li, err := c.URL(context.Background(), "cid", "320k")
	if err != nil {
		t.Fatalf("URL: %v", err)
	}
	if li.Link != "https://freetyst.example/abc.mp3" {
		t.Errorf("Link = %q", li.Link)
	}
	if li.Qual != "320k" {
		t.Errorf("Qual = %q", li.Qual)
	}
	if gotTone != "HQ" {
		t.Errorf("toneFlag = %q，320k 应为 HQ", gotTone)
	}
}

// TestURLFallsBack 高音质没权限时逐档降到 PQ。
func TestURLFallsBack(t *testing.T) {
	var tones []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		tone := r.URL.Query().Get("toneFlag")
		tones = append(tones, tone)
		if tone == "PQ" {
			w.Header().Set("Location", "https://freetyst.example/pq.mp3")
			w.WriteHeader(http.StatusFound)
			return
		}
		w.Write([]byte(`{"code":"200000","info":"暂不提供试听地址"}`))
	}))
	defer srv.Close()

	c := New(Config{Base: srv.URL}, nil)
	c.remember("cid", "crid")

	li, err := c.URL(context.Background(), "cid", "flac24bit")
	if err != nil {
		t.Fatalf("URL: %v", err)
	}
	if li.Qual != "128k" {
		t.Errorf("应降级到 128k，实际 %q", li.Qual)
	}
	if strings.Join(tones, ",") != "ZQ,SQ,HQ,PQ" {
		t.Errorf("降档顺序 = %v", tones)
	}
}

// TestURLWithoutRememberedCopyright 没搜过的歌也要能取链。
//
// 换源点播、或进程重启后再点，都会命中这条路径：早期版本在这里直接 503，
// 播放器侧表现就是「换源失败」。现在用占位值 "0" 走匿名。
func TestURLWithoutRememberedCopyright(t *testing.T) {
	var gotCR string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotCR = r.URL.Query().Get("copyrightId")
		w.Header().Set("Location", "https://freetyst.example/anon.mp3")
		w.WriteHeader(http.StatusFound)
	}))
	defer srv.Close()

	c := New(Config{Base: srv.URL}, nil) // 故意不 remember
	li, err := c.URL(context.Background(), "cid", "128k")
	if err != nil {
		t.Fatalf("URL: %v", err)
	}
	if gotCR != anonymousCopyrightID {
		t.Errorf("copyrightId = %q，want %q", gotCR, anonymousCopyrightID)
	}
	if li.Link != "https://freetyst.example/anon.mp3" {
		t.Errorf("Link = %q", li.Link)
	}
}

// TestURLPrefersRememberedCopyright 搜过的歌用真实 copyrightId。
func TestURLPrefersRememberedCopyright(t *testing.T) {
	var gotCR string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotCR = r.URL.Query().Get("copyrightId")
		w.Header().Set("Location", "https://freetyst.example/real.mp3")
		w.WriteHeader(http.StatusFound)
	}))
	defer srv.Close()

	c := New(Config{Base: srv.URL}, nil)
	c.remember("cid", "crid")
	if _, err := c.URL(context.Background(), "cid", "128k"); err != nil {
		t.Fatalf("URL: %v", err)
	}
	if gotCR != "crid" {
		t.Errorf("copyrightId = %q，want crid", gotCR)
	}
}

// TestToneMapping 音质与 toneFlag 的双向映射。
func TestToneMapping(t *testing.T) {
	cases := []struct{ qual, tone string }{
		{"128k", "PQ"}, {"320k", "HQ"}, {"flac", "SQ"}, {"flac24bit", "ZQ"}, {"", "PQ"},
	}
	for _, c := range cases {
		if got := toneFlag(c.qual); got != c.tone {
			t.Errorf("toneFlag(%q) = %q, want %q", c.qual, got, c.tone)
		}
	}
	back := []struct{ tone, qual string }{
		{"PQ", "128k"}, {"HQ", "320k"}, {"SQ", "flac"}, {"ZQ", "flac24bit"},
	}
	for _, c := range back {
		if got := qualityOfTone(c.tone); got != c.qual {
			t.Errorf("qualityOfTone(%q) = %q, want %q", c.tone, got, c.qual)
		}
	}
}
