// Package qq 对接 QQ 音乐的公开接口。
//
// 搜索走 client_search_cp；取链走 musicu.fcg 的 vkey.GetVkeyServer。
// 实现依据是开源项目 listen1 的做法（MIT）：
//   - 模块名必须是 req_1，且**必须带 filename**，否则返回 result=104003、purl 为空；
//   - filename 形如 M500<mid><mid>.mp3，前缀决定音质；
//   - 直链 = sip[0] + purl，purl 为空说明是 VIP 资源。
package qq

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/lxsrcs/lx-source-next/pkg/apierr"
	"github.com/lxsrcs/lx-source-next/pkg/source"
)

const (
	// DefaultSearchBase 是搜索接口根地址。
	DefaultSearchBase = "https://c.y.qq.com"
	// DefaultUA 默认请求标识。
	DefaultUA = "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/120.0.0.0 Safari/537.36"
	// DefaultReferer 是必需的来源标识。
	DefaultReferer = "https://y.qq.com/"
	// maxBody 响应体读取上限。
	maxBody = 8 << 20
)

// Doer 抽象 HTTP 执行器。
type Doer interface {
	Do(req *http.Request) (*http.Response, error)
}

// Config 是接入参数。
type Config struct {
	Src        string
	SearchBase string
	UserAgent  string
	Referer    string
	Expire     time.Duration
	// Cookie 是登录态凭据（含 uin 与 qqmusic_key）。
	Cookie string
	// MusicURL 是带签名的取链接口；留空用内置默认值。
	MusicURL string
	// Uin / Ukey 是登录态里的 QQ 号与会话票据；留空时匿名。
	Uin  string
	Ukey string
}

// Client 实现 source.Source 与 source.Searcher。
type Client struct {
	cfg  Config
	doer Doer
	now  func() time.Time
}

var (
	_ source.Source   = (*Client)(nil)
	_ source.Searcher = (*Client)(nil)
)

// New 创建客户端。
func New(cfg Config, doer Doer) *Client {
	if cfg.Src == "" {
		cfg.Src = "tx"
	}
	if cfg.SearchBase == "" {
		cfg.SearchBase = DefaultSearchBase
	}
	if cfg.UserAgent == "" {
		cfg.UserAgent = DefaultUA
	}
	if cfg.Referer == "" {
		cfg.Referer = DefaultReferer
	}
	if cfg.Expire <= 0 {
		cfg.Expire = 30 * time.Minute
	}
	if doer == nil {
		doer = &http.Client{Timeout: 10 * time.Second}
	}
	return &Client{cfg: cfg, doer: doer, now: time.Now}
}

// Src 实现 source.Source。
func (c *Client) Src() string { return c.cfg.Src }

// URL 实现 source.Source：走带签名的 musics.fcg 取直链。
//
// 匿名只对免费曲目给完整链接；付费曲目退到试听片段（见 vkey.go）。
func (c *Client) URL(ctx context.Context, id, q string) (source.LinkInfo, error) {
	id = strings.TrimSpace(id)
	if id == "" {
		return source.LinkInfo{}, apierr.New(apierr.CodeBadRequest, "缺少 QQ 音乐 songmid")
	}
	return c.urlTx(ctx, id, q)
}

// vkeyMidurl 是 vkey 响应里每条候选链接的描述。
type vkeyMidurl struct {
	Purl   string `json:"purl"`
	Result int    `json:"result"`
}

// vkeyData 是 req_0 / req_1 共用的数据段（两者字段完全一致，统一成一个类型）。
type vkeyData struct {
	Sip        []string    `json:"sip"`
	Midurlinfo []vkeyMidurl `json:"midurlinfo"`
}

// vkeyResp 兼容两种模块名：musics.fcg 用 req_0，旧接口用 req_1。
type vkeyResp struct {
	Req0 struct {
		Code int      `json:"code"`
		Data vkeyData `json:"data"`
	} `json:"req_0"`
	Req1 struct {
		Code int      `json:"code"`
		Data vkeyData `json:"data"`
	} `json:"req_1"`
}

// ParseVkey 从 vkey 响应里拼出直链。
func ParseVkey(body []byte) (string, error) {
	var resp vkeyResp
	if err := json.Unmarshal(body, &resp); err != nil {
		return "", fmt.Errorf("qq: 响应不是 JSON: %w", err)
	}
	data := resp.Req0.Data
	code := resp.Req0.Code
	if len(data.Midurlinfo) == 0 {
		data = resp.Req1.Data
		code = resp.Req1.Code
	}
	if len(data.Midurlinfo) == 0 {
		return "", fmt.Errorf("qq: 响应里没有 midurlinfo（code=%d）", code)
	}
	info := data.Midurlinfo[0]
	if info.Purl == "" {
		return "", fmt.Errorf("qq: purl 为空（result=%d，通常是 VIP 或需要登录）", info.Result)
	}
	if len(data.Sip) == 0 {
		return "", fmt.Errorf("qq: 响应里没有 sip 服务器地址")
	}
	return data.Sip[0] + info.Purl, nil
}

// Lyric 实现 source.Source。
func (c *Client) Lyric(context.Context, string) (source.LyricInfo, error) {
	return source.LyricInfo{}, apierr.New(apierr.CodeUnavailable, "qq: 暂未接入歌词")
}

// Movie 实现 source.Source。
func (c *Client) Movie(context.Context, string, string) (source.LinkInfo, error) {
	return source.LinkInfo{}, apierr.New(apierr.CodeUnavailable, "qq: 该能力尚未接入")
}

// Music 实现 source.Source。
func (c *Client) Music(context.Context, string) (source.MusicInfo, error) {
	return source.MusicInfo{}, apierr.New(apierr.CodeUnavailable, "qq: 该能力尚未接入")
}

// Video 实现 source.Source。
func (c *Client) Video(context.Context, string) (source.MovieInfo, error) {
	return source.MovieInfo{}, apierr.New(apierr.CodeUnavailable, "qq: 该能力尚未接入")
}

// Pic 实现 source.Source。
func (c *Client) Pic(context.Context, string, string) (string, error) {
	return "", apierr.New(apierr.CodeUnavailable, "qq: 该能力尚未接入")
}

// Search 实现 source.Searcher。
func (c *Client) Search(ctx context.Context, keyword string, page, size int) (source.SearchResult, error) {
	keyword = strings.TrimSpace(keyword)
	if keyword == "" {
		return source.SearchResult{}, apierr.New(apierr.CodeBadRequest, "缺少搜索关键词")
	}
	if page <= 0 {
		page = 1
	}
	if size <= 0 || size > 50 {
		size = 10
	}
	q := url.Values{}
	q.Set("w", keyword)
	q.Set("format", "json")
	q.Set("n", strconv.Itoa(size))
	q.Set("p", strconv.Itoa(page))

	u := strings.TrimRight(c.cfg.SearchBase, "/") + "/soso/fcgi-bin/client_search_cp?" + q.Encode()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return source.SearchResult{}, apierr.Wrap(apierr.CodeInternal, "qq: 构造请求失败", err)
	}
	c.setHeaders(req)
	body, err := c.do(req)
	if err != nil {
		return source.SearchResult{}, err
	}
	res, err := ParseSearch(body)
	if err != nil {
		return source.SearchResult{}, apierr.Wrap(apierr.CodeUnavailable, "qq: 解析搜索结果失败", err)
	}
	return res, nil
}

// ParseSearch 解析搜索响应。
func ParseSearch(body []byte) (source.SearchResult, error) {
	var resp struct {
		Code int `json:"code"`
		Data struct {
			Song struct {
				Totalnum int `json:"totalnum"`
				List     []struct {
					Songmid    string `json:"songmid"`
					Songname   string `json:"songname"`
					Albumname  string `json:"albumname"`
					Interval   int    `json:"interval"`
					Singer     []struct {
						Name string `json:"name"`
					} `json:"singer"`
				} `json:"list"`
			} `json:"song"`
		} `json:"data"`
	}
	if err := json.Unmarshal(body, &resp); err != nil {
		return source.SearchResult{}, fmt.Errorf("qq: 响应不是 JSON: %w", err)
	}
	out := source.SearchResult{Total: resp.Data.Song.Totalnum}
	for _, s := range resp.Data.Song.List {
		if s.Songmid == "" || s.Songname == "" {
			continue
		}
		item := source.MusicInfo{
			ID:   s.Songmid,
			Name: s.Songname,
			Al:   source.AlbumInfo{Name: s.Albumname},
			Dt:   uint32(s.Interval),
		}
		for _, a := range s.Singer {
			if a.Name != "" {
				item.Ar = append(item.Ar, source.ArtistInfo{Name: a.Name})
			}
		}
		out.Items = append(out.Items, item)
	}
	if out.Total == 0 {
		out.Total = len(out.Items)
	}
	return out, nil
}

func (c *Client) setHeaders(req *http.Request) {
	req.Header.Set("User-Agent", c.cfg.UserAgent)
	req.Header.Set("Referer", c.cfg.Referer)
	if c.cfg.Cookie != "" {
		req.Header.Set("Cookie", c.cfg.Cookie)
	}
}

func (c *Client) do(req *http.Request) ([]byte, error) {
	resp, err := c.doer.Do(req)
	if err != nil {
		return nil, apierr.Wrap(apierr.CodeUnavailable, "qq: 请求上游失败", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, apierr.Newf(apierr.CodeUnavailable, "qq: 上游返回 HTTP %d", resp.StatusCode)
	}
	return io.ReadAll(io.LimitReader(resp.Body, maxBody))
}

// cookieUIN 从 Cookie 里取 QQ 号，取不到返回 "0"（匿名）。
//
// QQ 的 Cookie 里是 uin=o0123456789 这种带 o 前缀的写法，接口要的是纯数字。
func cookieUIN(cookie string) string {
	for _, part := range strings.Split(cookie, ";") {
		part = strings.TrimSpace(part)
		for _, key := range []string{"uin=", "p_uin=", "p_skey_uin="} {
			if !strings.HasPrefix(part, key) {
				continue
			}
			v := strings.TrimPrefix(part, key)
			v = strings.TrimPrefix(v, "o")
			if v != "" && isDigits(v) {
				return v
			}
		}
	}
	return "0"
}

// isDigits 判断字符串是否为纯数字。
func isDigits(s string) bool {
	if s == "" {
		return false
	}
	for i := 0; i < len(s); i++ {
		if s[i] < 0x30 || s[i] > 0x39 {
			return false
		}
	}
	return true
}
