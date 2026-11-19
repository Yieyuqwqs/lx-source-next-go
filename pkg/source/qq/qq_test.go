package qq

import (
	"context"
	"io"
	"net/http"
	"strings"
	"testing"
)

type fakeDoer struct {
	reqs []*http.Request
	body []byte
	seq  [][]byte
	last []byte
	n    int
}

func (f *fakeDoer) Do(req *http.Request) (*http.Response, error) {
	f.reqs = append(f.reqs, req)
	if req.Body != nil {
		b, _ := io.ReadAll(req.Body)
		f.last = b
	}
	body := f.body
	if f.n < len(f.seq) {
		body = f.seq[f.n]
	}
	f.n++
	return &http.Response{StatusCode: http.StatusOK, Header: make(http.Header),
		Body: io.NopCloser(strings.NewReader(string(body)))}, nil
}

const vkeyBody = `{"code":0,"req_1":{"code":0,"data":{"sip":["http://aqqmusic.tc.qq.com/"],"midurlinfo":[{"songmid":"0027oMO61wWi55","filename":"M5000027oMO61wWi550027oMO61wWi55.mp3","purl":"M5000027oMO61wWi550027oMO61wWi55.mp3?vkey=ABC&guid=10000&uin=0&fromtag=66","result":0}]}}}`

func TestParseVkey(t *testing.T) {
	link, err := ParseVkey([]byte(vkeyBody))
	if err != nil {
		t.Fatalf("ParseVkey: %v", err)
	}
	want := "http://aqqmusic.tc.qq.com/M5000027oMO61wWi550027oMO61wWi55.mp3?vkey=ABC&guid=10000&uin=0&fromtag=66"
	if link != want {
		t.Errorf("link = %q", link)
	}
}

func TestParseVkeyEmpty(t *testing.T) {
	// purl 为空 = VIP / 需登录
	_, err := ParseVkey([]byte(`{"req_1":{"code":0,"data":{"sip":["http://x/"],"midurlinfo":[{"purl":"","result":104003}]}}}`))
	if err == nil || !strings.Contains(err.Error(), "VIP") {
		t.Errorf("应说明是 VIP 或需登录: %v", err)
	}
	// 没有 sip
	_, err = ParseVkey([]byte(`{"req_1":{"data":{"midurlinfo":[{"purl":"abc"}]}}}`))
	if err == nil {
		t.Error("缺 sip 应报错")
	}
	if _, err := ParseVkey([]byte(`nope`)); err == nil {
		t.Error("非 JSON 应报错")
	}
}

// TestURLFlow 校验「曲目信息 → vkey」两步带签名的流程。
func TestURLFlow(t *testing.T) {
	infoBody := `{"req":{"code":0,"data":{"track_info":{"name":"晴天","file":{"media_mid":"003Qui1q2u1Zho"},"pay":{"pay_play":0},"vs":["064YyRNb0gDV4P"]}}}}`
	vkeyOK := `{"req_0":{"code":0,"data":{"sip":["http://aqqmusic.tc.qq.com/"],"midurlinfo":[{"purl":"M500003Qui1q2u1Zho.mp3?vkey=ABC","result":0}]}}}`
	d := &fakeDoer{seq: [][]byte{[]byte(infoBody), []byte(vkeyOK)}}
	c := New(Config{MusicURL: "https://qq.invalid/musics.fcg"}, d)

	li, err := c.URL(context.Background(), "0039MnYb0qxYhV", "128k")
	if err != nil {
		t.Fatalf("URL: %v", err)
	}
	if li.Link != "http://aqqmusic.tc.qq.com/M500003Qui1q2u1Zho.mp3?vkey=ABC" {
		t.Errorf("Link = %q", li.Link)
	}
	if li.Qual != "128k" {
		t.Errorf("Qual = %q", li.Qual)
	}
	if len(d.reqs) != 2 {
		t.Fatalf("应为信息 + vkey 两次请求，实际 %d 次", len(d.reqs))
	}
	for i, r := range d.reqs {
		if !strings.Contains(r.URL.RawQuery, "sign=") {
			t.Errorf("第 %d 个请求缺少 sign 参数: %s", i+1, r.URL.RawQuery)
		}
	}
	// 第二次请求要带 req_0 与按 media_mid 拼的 filename
	body := string(d.last)
	if !strings.Contains(body, `"req_0"`) {
		t.Errorf("vkey 请求体缺 req_0: %s", body)
	}
	if !strings.Contains(body, `M500003Qui1q2u1Zho.mp3`) {
		t.Errorf("filename 未按 media_mid 拼接: %s", body)
	}
}

// TestURLTrialFallback 付费曲目降不到档位时退到试听片段。
func TestURLTrialFallback(t *testing.T) {
	infoBody := `{"req":{"code":0,"data":{"track_info":{"name":"晴天","file":{"media_mid":"003Qui1q2u1Zho"},"pay":{"pay_play":1},"vs":["064YyRNb0gDV4P"]}}}}`
	trial := `{"req_0":{"code":0,"data":{"sip":["http://aqqmusic.tc.qq.com/"],"midurlinfo":[{"purl":"RS02064YyRNb0gDV4P.mp3?vkey=ABC","result":0}]}}}`
	// 付费且无凭据：应当只请求两次（曲目信息 + 试听），不再白打正常档位
	d := &fakeDoer{seq: [][]byte{[]byte(infoBody), []byte(trial)}}
	c := New(Config{MusicURL: "https://qq.invalid/musics.fcg"}, d)

	li, err := c.URL(context.Background(), "0039MnYb0qxYhV", "flac24bit")
	if err != nil {
		t.Fatalf("URL: %v", err)
	}
	if !strings.Contains(li.Link, "RS02") {
		t.Errorf("应退到试听片段，Link = %q", li.Link)
	}
	if len(d.reqs) != 2 {
		t.Errorf("付费且无凭据时应只发 2 次请求，实际 %d 次", len(d.reqs))
	}
}

func TestSearchAndParse(t *testing.T) {
	body := `{"code":0,"data":{"song":{"totalnum":100,"list":[{"songmid":"0039MnYb0qxYhV","songname":"晴天","albumname":"叶惠美","interval":269,"singer":[{"name":"周杰伦"}]}]}}}`
	d := &fakeDoer{body: []byte(body)}
	c := New(Config{SearchBase: "https://qq.invalid"}, d)

	res, err := c.Search(context.Background(), "周杰伦", 1, 5)
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if res.Total != 100 || len(res.Items) != 1 {
		t.Fatalf("结果 = %+v", res)
	}
	item := res.Items[0]
	if item.ID != "0039MnYb0qxYhV" || item.Name != "晴天" || item.Dt != 269 {
		t.Errorf("item = %+v", item)
	}
	if len(item.Ar) != 1 || item.Ar[0].Name != "周杰伦" {
		t.Errorf("Ar = %+v", item.Ar)
	}
	if d.reqs[0].Header.Get("Referer") == "" {
		t.Error("缺少 Referer")
	}
}
