package wy

import (
	"context"
	"io"
	"net/http"
	"strings"
	"testing"
)

const searchBody = `{"code":200,"result":{"songCount":100,"songs":[
{"id":347230,"name":"海阔天空","duration":326000,"artists":[{"name":"BEYOND"}],"album":{"name":"乐与怒"}},
{"id":1330348068,"name":"晴天","duration":269000,"artists":[{"name":"周杰伦"}],"album":{"name":"叶惠美"}}]}}`

const lyricBody = `{"lrc":{"lyric":"[00:00.000] 作词 : 黄家驹\n[00:01.000] 作曲 : 黄家驹"},"tlyric":{"lyric":"[00:00.000] Lyrics : Wong Ka Kui"},"klyric":{"lyric":"[00:00.000]<0,36>作<36,36>词"}}`

type fakeDoer struct {
	reqs []*http.Request
	body string
}

func (f *fakeDoer) Do(req *http.Request) (*http.Response, error) {
	f.reqs = append(f.reqs, req)
	return &http.Response{StatusCode: http.StatusOK, Header: make(http.Header),
		Body: io.NopCloser(strings.NewReader(f.body))}, nil
}

func TestParseSearch(t *testing.T) {
	res, err := ParseSearch([]byte(searchBody))
	if err != nil {
		t.Fatalf("ParseSearch: %v", err)
	}
	if res.Total != 100 {
		t.Errorf("Total = %d", res.Total)
	}
	if len(res.Items) != 2 {
		t.Fatalf("Items = %d", len(res.Items))
	}
	first := res.Items[0]
	if first.ID != "347230" {
		t.Errorf("ID = %q", first.ID)
	}
	if first.Name != "海阔天空" {
		t.Errorf("Name = %q", first.Name)
	}
	if len(first.Ar) != 1 || first.Ar[0].Name != "BEYOND" {
		t.Errorf("Ar = %+v", first.Ar)
	}
	// 毫秒 → 秒
	if first.Dt != 326 {
		t.Errorf("Dt = %d, want 326（上游是毫秒）", first.Dt)
	}
}

func TestParseLyric(t *testing.T) {
	li, err := ParseLyric([]byte(lyricBody))
	if err != nil {
		t.Fatalf("ParseLyric: %v", err)
	}
	if !strings.Contains(li.Lyric, "黄家驹") {
		t.Errorf("Lyric = %q", li.Lyric)
	}
	if !strings.Contains(li.TLyric, "Wong Ka Kui") {
		t.Errorf("TLyric = %q", li.TLyric)
	}
	// klyric 是逐字歌词，映射到 lxlyric
	if !strings.Contains(li.LxLyric, "<0,36>") {
		t.Errorf("LxLyric = %q", li.LxLyric)
	}
}

func TestParseLyricEmpty(t *testing.T) {
	if _, err := ParseLyric([]byte(`{"lrc":{"lyric":""}}`)); err == nil {
		t.Error("空歌词应报错（可能是纯音乐或无版权）")
	}
}

func TestSearchRequestNeedsReferer(t *testing.T) {
	d := &fakeDoer{body: searchBody}
	c := New(Config{Base: "https://wy.invalid"}, d)
	if _, err := c.Search(context.Background(), "周杰伦", 1, 3); err != nil {
		t.Fatalf("Search: %v", err)
	}
	req := d.reqs[0]
	if req.Header.Get("Referer") == "" {
		t.Error("缺少 Referer，网易云会拒绝")
	}
	q := req.URL.Query()
	if q.Get("s") != "周杰伦" || q.Get("type") != "1" || q.Get("limit") != "3" {
		t.Errorf("查询参数 = %v", q)
	}
}

func TestLyricRequest(t *testing.T) {
	d := &fakeDoer{body: lyricBody}
	c := New(Config{Base: "https://wy.invalid"}, d)
	li, err := c.Lyric(context.Background(), "347230")
	if err != nil {
		t.Fatalf("Lyric: %v", err)
	}
	if li.Lyric == "" {
		t.Error("歌词为空")
	}
	q := d.reqs[0].URL.Query()
	if q.Get("id") != "347230" || q.Get("lv") != "-1" {
		t.Errorf("查询参数 = %v", q)
	}
}

func TestURLUnavailable(t *testing.T) {
	c := New(Config{}, &fakeDoer{})
	_, err := c.URL(context.Background(), "1", "320k")
	if err == nil {
		t.Fatal("匿名环境不应能取网易云直链")
	}
	if !strings.Contains(err.Error(), "加密") {
		t.Errorf("错误信息应说明原因: %v", err)
	}
}
