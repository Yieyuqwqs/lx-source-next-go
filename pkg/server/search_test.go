package server

import (
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/lxsrcs/lx-source-next/pkg/config"
	"github.com/lxsrcs/lx-source-next/pkg/source"
	"github.com/lxsrcs/lx-source-next/pkg/source/kw"
	"github.com/lxsrcs/lx-source-next/pkg/source/mock"
)

// searchBody 复刻酷我搜索接口的真实形状（单引号伪 JSON）。
const searchBody = `{'HIT':'3600','TOTAL':'3600','abslist:[{'ALBUM':'魔杰座','ARTIST':'周杰伦','DC_TARGETID':'474678847','DURATION':'210','FORMATS':'MP3128|MP3H','SONGNAME':'给我一首歌的时间'}]}`

// doerFunc 把固定响应喂给客户端。
type doerFunc struct {
	body string
	reqs []*http.Request
}

func (d *doerFunc) Do(req *http.Request) (*http.Response, error) {
	d.reqs = append(d.reqs, req)
	return &http.Response{
		StatusCode: http.StatusOK,
		Header:     make(http.Header),
		Body:       io.NopCloser(strings.NewReader(d.body)),
	}, nil
}

func newSearchApp(t *testing.T, d *doerFunc) *App {
	t.Helper()
	cfg, err := config.Parse([]byte(""))
	if err != nil {
		t.Fatalf("解析配置: %v", err)
	}
	holder := config.NewHolder(&config.Snapshot{Dyn: cfg, LoadedAt: time.Now(), Version: 1})
	src := kw.New(kw.Config{SearchHost: "https://search.invalid"}, d)
	return New(Deps{
		Config:  holder,
		Sources: source.Registry{"kw": src},
		Options: Options{},
	})
}

func TestSearchRoute(t *testing.T) {
	d := &doerFunc{body: searchBody}
	app := newSearchApp(t, d)

	rec := get(t, app, "/api/v1/search?pt=kw&q=%E5%91%A8%E6%9D%B0%E4%BC%A6")
	if rec.Code != http.StatusOK {
		t.Fatalf("状态码 = %d，响应 %s", rec.Code, rec.Body.String())
	}

	m := decode(t, rec)
	data, _ := m["data"].(map[string]any)
	if data["total"].(float64) != 3600 {
		t.Errorf("total = %v", data["total"])
	}
	items, _ := data["items"].([]any)
	if len(items) != 1 {
		t.Fatalf("items = %d", len(items))
	}
	item, _ := items[0].(map[string]any)
	if item["id"] != "474678847" {
		t.Errorf("id = %v", item["id"])
	}
	if item["name"] != "给我一首歌的时间" {
		t.Errorf("name = %v", item["name"])
	}

	// 请求确实打到了 search 域名，并带上关键词
	if len(d.reqs) != 1 {
		t.Fatalf("上游请求数 = %d", len(d.reqs))
	}
	if !strings.HasPrefix(d.reqs[0].URL.String(), "https://search.invalid/r.s?") {
		t.Errorf("请求地址 = %s", d.reqs[0].URL)
	}
	if !strings.Contains(d.reqs[0].URL.RawQuery, "all=%E5%91%A8%E6%9D%B0%E4%BC%A6") {
		t.Errorf("关键字未传出: %s", d.reqs[0].URL.RawQuery)
	}
}

func TestSearchRouteErrors(t *testing.T) {
	d := &doerFunc{body: searchBody}
	app := newSearchApp(t, d)

	// 缺少关键词
	if rec := get(t, app, "/api/v1/search?pt=kw"); rec.Code != http.StatusBadRequest {
		t.Errorf("缺少 q 应 400，实际 %d", rec.Code)
	}
	// 缺少平台
	if rec := get(t, app, "/api/v1/search?q=x"); rec.Code != http.StatusBadRequest {
		t.Errorf("缺少 pt 应 400，实际 %d", rec.Code)
	}
	// 未知平台
	if rec := get(t, app, "/api/v1/search?pt=zz&q=x"); rec.Code != http.StatusNotFound {
		t.Errorf("未知平台应 404，实际 %d", rec.Code)
	}
}

func TestSearchUnsupportedSource(t *testing.T) {
	cfg, _ := config.Parse([]byte(""))
	holder := config.NewHolder(&config.Snapshot{Dyn: cfg, LoadedAt: time.Now(), Version: 1})
	app := New(Deps{
		Config:  holder,
		Sources: source.Registry{"mock": mock.New()}, // mock 不实现 Searcher
		Options: Options{},
	})
	rec := get(t, app, "/api/v1/search?pt=mock&q=x")
	if rec.Code != http.StatusServiceUnavailable {
		t.Errorf("不支持的来源应 503，实际 %d", rec.Code)
	}
}
