package qq

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"
)

type fakeDoer struct {
	reqs []*http.Request
	body []byte
	last []byte
}

func (f *fakeDoer) Do(req *http.Request) (*http.Response, error) {
	f.reqs = append(f.reqs, req)
	if req.Body != nil {
		b, _ := io.ReadAll(req.Body)
		f.last = b
	}
	return &http.Response{StatusCode: http.StatusOK, Header: make(http.Header),
		Body: io.NopCloser(strings.NewReader(string(f.body)))}, nil
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

// TestURLRequestShape 校验请求体形状——这是 listen1 实现里最关键的两点：
// 模块必须叫 req_1，且必须带 filename。
func TestURLRequestShape(t *testing.T) {
	d := &fakeDoer{body: []byte(vkeyBody)}
	c := New(Config{VkeyURL: "https://qq.invalid/cgi-bin/musicu.fcg"}, d)

	if _, err := c.URL(context.Background(), "0027oMO61wWi55", "320k"); err != nil {
		t.Fatalf("URL: %v", err)
	}

	var req map[string]any
	if err := json.Unmarshal(d.last, &req); err != nil {
		t.Fatalf("请求体不是 JSON: %v", err)
	}
	if _, ok := req["req_1"]; !ok {
		t.Error("请求体必须使用 req_1 模块名")
	}
	if _, ok := req["req_0"]; ok {
		t.Error("不应使用 req_0（服务端会返回 104003）")
	}
	param := req["req_1"].(map[string]any)["param"].(map[string]any)
	fn, _ := param["filename"].([]any)
	if len(fn) != 1 {
		t.Fatalf("filename 必须传一个元素，实际 %v", param["filename"])
	}
	// 320k 用 M800 前缀，文件名是 前缀+mid+mid+后缀
	if got := fn[0].(string); got != "M8000027oMO61wWi550027oMO61wWi55.mp3" {
		t.Errorf("filename = %q", got)
	}
	if param["guid"] != "10000" {
		t.Errorf("guid = %v", param["guid"])
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
