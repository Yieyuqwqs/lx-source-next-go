package mg

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// TestLyric 校验「搜索记下 lyricUrl → 取歌词文本」的链路。
func TestLyric(t *testing.T) {
	var srv *httptest.Server
	srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.Contains(r.URL.Path, "search_all.do"):
			w.Write([]byte(`{"code":"000000","songResultData":{"totalCount":"1","result":[
{"contentId":"cid-1","copyrightId":"crid-1","name":"歌","lyricUrl":"` + srv.URL + `/lrc/1.lrc"}]}}`))
		case strings.Contains(r.URL.Path, "/lrc/1.lrc"):
			w.Write([]byte("[00:01.00]咪咕歌词\n[00:02.00]第二行"))
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()

	c := New(Config{Base: srv.URL}, nil)
	if _, err := c.Search(context.Background(), "歌", 1, 10); err != nil {
		t.Fatalf("Search: %v", err)
	}

	li, err := c.Lyric(context.Background(), "cid-1")
	if err != nil {
		t.Fatalf("Lyric: %v", err)
	}
	if !strings.Contains(li.Lyric, "咪咕歌词") {
		t.Errorf("歌词 = %q", li.Lyric)
	}
}

// TestLyricWithoutSearch 没搜过就没有 lyricUrl，要能说清原因。
func TestLyricWithoutSearch(t *testing.T) {
	c := New(Config{Base: "https://mg.invalid"}, nil)
	_, err := c.Lyric(context.Background(), "unknown")
	if err == nil || !strings.Contains(err.Error(), "先搜索") {
		t.Errorf("应提示先搜索: %v", err)
	}
}
