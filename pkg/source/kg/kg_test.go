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

// 复刻实测响应形状
const searchBody = `{"status":1,"errcode":0,"data":{"total":480,"info":[
{"hash":"1f7e7659b9fff4fb6f71562a61bbc25f","songname":"晴天","singername":"周杰伦","album_name":"叶惠美","duration":269,"bitrate":128,"sqfilesize":36226753},
{"hash":"b3a52a7a958bf0aed0ebfba2e9a818b7","songname":"稻香","singername":"周杰伦","album_name":"魔杰座","duration":223,"bitrate":320}]}}`

const infoBody = `{"hash":"1F7E7659B9FFF4FB6F71562A61BBC25F","authors":[{"author_name":"周杰伦"}],"url":"http://fs.audio.kugou.com/abc.mp3","fileName":"晴天","bitRate":128000}`

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

func TestParseSearch(t *testing.T) {
	res, err := ParseSearch([]byte(searchBody))
	if err != nil {
		t.Fatalf("ParseSearch: %v", err)
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
		t.Errorf("Name = %q", first.Name)
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

func TestParseSongURL(t *testing.T) {
	link, err := ParseSongURL([]byte(infoBody))
	if err != nil {
		t.Fatalf("ParseSongURL: %v", err)
	}
	if link != "http://fs.audio.kugou.com/abc.mp3" {
		t.Errorf("link = %q", link)
	}
}

func TestParseSongURLErrors(t *testing.T) {
	bad := []string{
		``,
		`not json`,
		`{"errcode":2,"error":"请求参数无效"}`,
		`{"url":""}`,
	}
	for _, b := range bad {
		if _, err := ParseSongURL([]byte(b)); err == nil {
			t.Errorf("%q 应报错", b)
		}
	}
	// 上游 errcode 要带出 msg
	_, err := ParseSongURL([]byte(`{"errcode":2,"error":"请求参数无效"}`))
	if err == nil || !strings.Contains(err.Error(), "请求参数无效") {
		t.Errorf("应带出上游 msg: %v", err)
	}
}

func TestSearchRequest(t *testing.T) {
	d := &fakeDoer{body: searchBody}
	c := New(Config{SearchBase: "https://kg.invalid"}, d)

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
	d := &fakeDoer{body: infoBody}
	fixed := time.Unix(1700000000, 0)
	c := New(Config{InfoBase: "http://kg.invalid", Expire: 10 * time.Minute}, d)
	c.now = func() time.Time { return fixed }

	li, err := c.URL(context.Background(), "1f7e7659b9fff4fb6f71562a61bbc25f", "320k")
	if err != nil {
		t.Fatalf("URL: %v", err)
	}
	if li.Link != "http://fs.audio.kugou.com/abc.mp3" {
		t.Errorf("Link = %q", li.Link)
	}
	if li.Expi != fixed.Add(10*time.Minute).Unix() {
		t.Errorf("Expi = %d", li.Expi)
	}
	q := d.reqs[0].URL.Query()
	if q.Get("cmd") != "playInfo" || q.Get("hash") != "1f7e7659b9fff4fb6f71562a61bbc25f" {
		t.Errorf("查询参数 = %v", q)
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
		if r.URL.Query().Get("cmd") != "playInfo" {
			http.Error(w, "bad cmd", http.StatusBadRequest)
			return
		}
		io.WriteString(w, infoBody)
	}))
	defer srv.Close()

	c := New(Config{InfoBase: srv.URL}, nil)
	li, err := c.URL(context.Background(), "1f7e7659b9fff4fb6f71562a61bbc25f", "128k")
	if err != nil {
		t.Fatalf("URL: %v", err)
	}
	if !strings.HasPrefix(li.Link, "http") {
		t.Errorf("Link = %q", li.Link)
	}
}

// TestParseSongURLTolerant 覆盖「直链字段名不固定」的场景。
func TestParseSongURLTolerant(t *testing.T) {
	cases := []string{
		`{"url":"http://a.com/x.mp3"}`,
		`{"data":{"play_url":"http://a.com/x.mp3"}}`,
		`{"result":{"list":[{"src":"http://a.com/x.flac"}]}}`,
		`{"url_320":"http://a.com/x.mp3","url_128":"http://a.com/y.mp3"}`,
	}
	for _, c := range cases {
		got, err := ParseSongURL([]byte(c))
		if err != nil {
			t.Errorf("%s 解析失败: %v", c, err)
			continue
		}
		if !strings.HasPrefix(got, "http") {
			t.Errorf("%s → %q", c, got)
		}
	}

	// 只有非媒体地址时不能瞎返回
	if _, err := ParseSongURL([]byte(`{"avatar":"http://a.com/pic.jpg"}`)); err == nil {
		t.Error("非媒体地址不应被当成直链")
	}
}

// TestSearchBaseIsHTTP 守住这个真实踩过的坑：该域名走 https 会证书校验失败。
func TestSearchBaseIsHTTP(t *testing.T) {
	c := New(Config{}, nil)
	_ = c
	if !strings.HasPrefix(DefaultSearchBase, "http://") {
		t.Errorf("搜索地址必须走 http，当前 %q", DefaultSearchBase)
	}
}
