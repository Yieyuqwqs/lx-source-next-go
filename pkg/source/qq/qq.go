// Package qq 对接 QQ 音乐的公开接口。
//
// 搜索走 client_search_cp；取链走 musicu.fcg 的 vkey.GetVkeyServer。
// 实现依据是开源项目 listen1 的做法（MIT）：
//   - 模块名必须是 req_1，且**必须带 filename**，否则返回 result=104003、purl 为空；
//   - filename 形如 M500<mid><mid>.mp3，前缀决定音质；
//   - 直链 = sip[0] + purl，purl 为空说明是 VIP 资源。
package qq

import (
	"bytes"
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
	"github.com/lxsrcs/lx-source-next/pkg/quality"
	"github.com/lxsrcs/lx-source-next/pkg/source"
)

const (
	// DefaultSearchBase 是搜索接口根地址。
	DefaultSearchBase = "https://c.y.qq.com"
	// DefaultVkeyURL 是 vkey 接口地址。
	DefaultVkeyURL = "https://u.y.qq.com/cgi-bin/musicu.fcg"
	// DefaultUA 默认请求标识。
	DefaultUA = "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/120.0.0.0 Safari/537.36"
	// DefaultReferer 是必需的来源标识。
	DefaultReferer = "https://y.qq.com/"
	// guid 是固定设备号，匿名请求用。
	guid = "10000"
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
	VkeyURL    string
	UserAgent  string
	Referer    string
	Expire     time.Duration
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
	if cfg.VkeyURL == "" {
		cfg.VkeyURL = DefaultVkeyURL
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

// fileConfig 是音质 → (filename 前缀, 后缀) 的映射。
//
// 服务端对 320k 的匿名请求常常不响应，listen1 也据此默认降到 128k。
var fileConfig = map[string]struct{ prefix, ext, canonical string }{
	quality.Q128:    {"M500", ".mp3", quality.Q128},
	quality.Q320:    {"M800", ".mp3", quality.Q320},
	quality.QFlac:   {"F000", ".flac", quality.QFlac},
	quality.QFlac24: {"F000", ".flac", quality.QFlac24},
}

// URL 实现 source.Source：用 songmid 取直链。
func (c *Client) URL(ctx context.Context, id, q string) (source.LinkInfo, error) {
	id = strings.TrimSpace(id)
	if id == "" {
		return source.LinkInfo{}, apierr.New(apierr.CodeBadRequest, "缺少 QQ 音乐 songmid")
	}
	q = strings.TrimSpace(q)
	if q == "" {
		q = quality.Q128
	}
	canonical, ok := quality.Canonical(q)
	if !ok {
		canonical = quality.Q128
	}
	fc, ok := fileConfig[canonical]
	if !ok {
		// 不支持的音质回落到 128k，和服务端行为一致
		fc = fileConfig[quality.Q128]
		canonical = quality.Q128
	}

	// filename 的格式是 前缀 + mid + mid + 后缀，两个 mid 都要有
	filename := fc.prefix + id + id + fc.ext

	body := map[string]any{
		"req_1": map[string]any{
			"module": "vkey.GetVkeyServer",
			"method": "CgiGetVkey",
			"param": map[string]any{
				"filename":  []string{filename},
				"guid":      guid,
				"songmid":   []string{id},
				"songtype":  []int{0},
				"uin":       "0",
				"loginflag": 1,
				"platform":  "20",
			},
		},
		"loginUin": "0",
		"comm": map[string]any{
			"uin":    "0",
			"format": "json",
			"ct":     24,
			"cv":     0,
		},
	}

	raw, err := c.postJSON(ctx, c.cfg.VkeyURL, body)
	if err != nil {
		return source.LinkInfo{}, err
	}
	link, err := ParseVkey(raw)
	if err != nil {
		return source.LinkInfo{}, apierr.Wrap(apierr.CodeUnavailable, "qq: 取直链失败", err)
	}
	return source.LinkInfo{
		Qual: canonical,
		Expi: c.now().Add(c.cfg.Expire).Unix(),
		Link: link,
	}, nil
}

// ParseVkey 从 vkey 响应里拼出直链。
func ParseVkey(body []byte) (string, error) {
	var resp struct {
		Req1 struct {
			Code int `json:"code"`
			Data struct {
				Sip        []string `json:"sip"`
				Midurlinfo []struct {
					Purl   string `json:"purl"`
					Result int    `json:"result"`
					Errtype string `json:"errtype"`
				} `json:"midurlinfo"`
			} `json:"data"`
		} `json:"req_1"`
	}
	if err := json.Unmarshal(body, &resp); err != nil {
		return "", fmt.Errorf("qq: 响应不是 JSON: %w", err)
	}
	if len(resp.Req1.Data.Midurlinfo) == 0 {
		return "", fmt.Errorf("qq: 响应里没有 midurlinfo（code=%d）", resp.Req1.Code)
	}
	info := resp.Req1.Data.Midurlinfo[0]
	if info.Purl == "" {
		return "", fmt.Errorf("qq: purl 为空（result=%d，通常是 VIP 或需要登录）", info.Result)
	}
	if len(resp.Req1.Data.Sip) == 0 {
		return "", fmt.Errorf("qq: 响应里没有 sip 服务器地址")
	}
	return resp.Req1.Data.Sip[0] + info.Purl, nil
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

// postJSON 发一个 JSON POST。
func (c *Client) postJSON(ctx context.Context, u string, payload any) ([]byte, error) {
	b, err := json.Marshal(payload)
	if err != nil {
		return nil, apierr.Wrap(apierr.CodeInternal, "qq: 序列化请求失败", err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, u, bytes.NewReader(b))
	if err != nil {
		return nil, apierr.Wrap(apierr.CodeInternal, "qq: 构造请求失败", err)
	}
	req.Header.Set("Content-Type", "application/json")
	c.setHeaders(req)
	return c.do(req)
}

func (c *Client) setHeaders(req *http.Request) {
	req.Header.Set("User-Agent", c.cfg.UserAgent)
	req.Header.Set("Referer", c.cfg.Referer)
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
