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

// fakeDoer 记录请求；last 保存最近一次请求体，seq 用于按序返回不同响应。
type fakeDoer struct {
	reqs []*http.Request
	body string
	seq  []string
	last []byte
	n    int
}

func (f *fakeDoer) Do(req *http.Request) (*http.Response, error) {
	f.reqs = append(f.reqs, req)
	if req.Body != nil {
		b, err := io.ReadAll(req.Body)
		if err != nil {
			return nil, err
		}
		f.last = b
	}
	body := f.body
	if f.n < len(f.seq) {
		body = f.seq[f.n]
	}
	f.n++
	return &http.Response{StatusCode: http.StatusOK, Header: make(http.Header),
		Body: io.NopCloser(strings.NewReader(body))}, nil
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

// TestURLEapi 校验 eapi 取链的请求形状与结果解析。
//
// 直接调 urlEapi：URL() 的常规顺序是 weapi → 聚合 → eapi。
func TestURLEapi(t *testing.T) {
	d := &fakeDoer{body: "{\"data\":[{\"id\":347230,\"url\":\"http://m8.music.126.net/a.mp3\",\"br\":320000,\"code\":200}]}"}
	c := New(Config{EapiBase: "https://wy.invalid"}, d)

	li, err := c.urlEapi(context.Background(), "347230", "320k")
	if err != nil {
		t.Fatalf("URL: %v", err)
	}
	if li.Link != "http://m8.music.126.net/a.mp3" {
		t.Errorf("Link = %q", li.Link)
	}
	if li.Qual != "320k" {
		t.Errorf("Qual = %q", li.Qual)
	}

	req := d.reqs[0]
	if req.Method != http.MethodPost {
		t.Errorf("必须用 POST，实际 %s", req.Method)
	}
	if !strings.HasSuffix(req.URL.Path, "/eapi/song/enhance/player/url") {
		t.Errorf("路径 = %q", req.URL.Path)
	}
	if got := req.Header.Get("Cookie"); got != "os=pc" {
		t.Errorf("缺少环境 cookie os=pc，实际 %q", got)
	}
	if got := req.Header.Get("Content-Type"); !strings.Contains(got, "form-urlencoded") {
		t.Errorf("Content-Type = %q", got)
	}
	form := string(d.last)
	if !strings.HasPrefix(form, "params=") {
		t.Errorf("请求体 = %q", form)
	}
	hexPart := strings.TrimPrefix(form, "params=")
	if hexPart != strings.ToUpper(hexPart) {
		t.Error("params 必须是全大写十六进制")
	}
}

// TestURLNullURL 覆盖会员曲目：url 为 null 时要说明原因。
func TestURLNullURL(t *testing.T) {
	d := &fakeDoer{body: "{\"data\":[{\"id\":1,\"url\":null,\"br\":0,\"code\":404}]}"}
	c := New(Config{EapiBase: "https://wy.invalid"}, d)
	_, err := c.urlEapi(context.Background(), "1", "128k")
	if err == nil {
		t.Fatal("url 为 null 应报错")
	}
	if !strings.Contains(err.Error(), "会员") {
		t.Errorf("应说明原因: %v", err)
	}
}
