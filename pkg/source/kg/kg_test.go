package kg

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// searchBody 复刻实测的 song_search_v2 响应形状（含 24bit 的 ResFileHash）。
const searchBody = `{"status":1,"data":{"total":480,"lists":[
{"SongName":"<em>晴天</em>","SingerName":"周杰伦","AlbumName":"叶惠美","Duration":269,
 "FileHash":"1f7e7659b9fff4fb6f71562a61bbc25f","HQFileHash":"0d04734e75820beae8aa202e758931db",
 "SQFileHash":"ef79af82f05aa5242ab2aaa22ca7de78","ResFileHash":"183a319be152a9f9f57e2dda1aa6ef56","Privilege":8},
{"SongName":"<em>稻香</em>","SingerName":"周杰伦","AlbumName":"魔杰座","Duration":223,
 "FileHash":"b3a52a7a958bf0aed0ebfba2e9a818b7","Privilege":0}]}}`

type fakeDoer struct {
	reqs   []*http.Request
	body   string
	status int
}

func (f *fakeDoer) Do(req *http.Request) (*http.Response, error) {
	f.reqs = append(f.reqs, req)
	st := f.status
	if st == 0 {
		st = http.StatusOK
	}
	return &http.Response{StatusCode: st, Header: make(http.Header),
		Body: io.NopCloser(strings.NewReader(f.body))}, nil
}

func TestParseSearchV2(t *testing.T) {
	res, tables, err := ParseSearchV2([]byte(searchBody))
	if err != nil {
		t.Fatalf("ParseSearchV2: %v", err)
	}
	if len(tables) == 0 {
		t.Fatal("没有记录 hash 表")
	}
	if res.Total != 480 {
		t.Errorf("Total = %d", res.Total)
	}
	if len(res.Items) != 2 {
		t.Fatalf("Items = %d", len(res.Items))
	}
	first := res.Items[0]
	if first.ID != "1f7e7659b9fff4fb6f71562a61bbc25f" {
		t.Errorf("ID = %q", first.ID)
	}
	if first.Name != "晴天" {
		t.Errorf("Name 应去掉 <em> 标签，得到 %q", first.Name)
	}
	if len(first.Ar) != 1 || first.Ar[0].Name != "周杰伦" {
		t.Errorf("Ar = %+v", first.Ar)
	}
	if first.Al.Name != "叶惠美" {
		t.Errorf("Al = %+v", first.Al)
	}
	if first.Dt != 269 {
		t.Errorf("Dt = %d", first.Dt)
	}
}

func TestSearchRequest(t *testing.T) {
	d := &fakeDoer{body: searchBody}
	c := New(Config{}, d)

	res, err := c.Search(context.Background(), "周杰伦", 2, 5)
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if len(res.Items) != 2 {
		t.Errorf("Items = %d", len(res.Items))
	}
	q := d.reqs[0].URL.Query()
	if q.Get("keyword") != "周杰伦" || q.Get("page") != "2" || q.Get("pagesize") != "5" {
		t.Errorf("查询参数 = %v", q)
	}
	if !strings.Contains(d.reqs[0].Header.Get("User-Agent"), "Mozilla") {
		t.Error("缺少 UA")
	}
}

func TestURLRequest(t *testing.T) {
	d := &fakeDoer{body: `{"status":1,"url":"http://fs.audio.kugou.com/abc.mp3"}`}
	fixed := time.Unix(1700000000, 0)
	c := New(Config{V5URL: "http://kg.invalid/v5/url", Expire: 10 * time.Minute}, d)
	c.now = func() time.Time { return fixed }

	li, err := c.URL(context.Background(), "1f7e7659b9fff4fb6f71562a61bbc25f", "flac")
	if err != nil {
		t.Fatalf("URL: %v", err)
	}
	if li.Link != "http://fs.audio.kugou.com/abc.mp3" {
		t.Errorf("Link = %q", li.Link)
	}
	if li.Qual != "flac" {
		t.Errorf("Qual = %q", li.Qual)
	}
	if li.Expi != fixed.Add(10*time.Minute).Unix() {
		t.Errorf("Expi = %d", li.Expi)
	}
	q := d.reqs[0].URL.Query()
	if q.Get("hash") != "1f7e7659b9fff4fb6f71562a61bbc25f" {
		t.Errorf("hash = %q", q.Get("hash"))
	}
	if q.Get("quality") != "flac" {
		t.Errorf("quality = %q，应为 flac", q.Get("quality"))
	}
	if q.Get("signature") == "" {
		t.Error("缺少 signature")
	}
	if got := d.reqs[0].Header.Get("Kg-Thash"); got != "5760185" {
		t.Errorf("缺 v5 客户端头，Kg-Thash=%q", got)
	}
}

func TestURLRejectsEmpty(t *testing.T) {
	c := New(Config{}, &fakeDoer{})
	if _, err := c.URL(context.Background(), "  ", "128k"); err == nil {
		t.Error("空 hash 应报错")
	}
}

func TestUnsupportedCapabilities(t *testing.T) {
	c := New(Config{}, &fakeDoer{})
	ctx := context.Background()
	if _, err := c.Lyric(ctx, "x"); err == nil {
		t.Error("Lyric 应报不可用")
	}
	if _, err := c.Pic(ctx, "x", "m"); err == nil {
		t.Error("Pic 应报不可用")
	}
}

func TestHTTPTestServer(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("signature") == "" {
			http.Error(w, "no signature", http.StatusBadRequest)
			return
		}
		io.WriteString(w, `{"status":1,"url":"http://fs.example/abc.flac"}`)
	}))
	defer srv.Close()

	c := New(Config{V5URL: srv.URL}, nil)
	li, err := c.URL(context.Background(), "1f7e7659b9fff4fb6f71562a61bbc25f", "flac24bit")
	if err != nil {
		t.Fatalf("URL: %v", err)
	}
	if !strings.HasPrefix(li.Link, "http") {
		t.Errorf("Link = %q", li.Link)
	}
}
