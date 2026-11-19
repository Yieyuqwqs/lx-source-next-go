// Package wy 对接网易云的公开接口。
//
// 能力来源（均为公开实现，参考 NeteaseCloudMusicApi / listen1）：
//   - 搜索：music.163.com/api/search/get（未加密，直接返回 JSON）
//   - 歌词：music.163.com/api/song/lyric（含 lrc / tlyric / klyric）
//   - 直链：interface3.music.163.com/eapi/song/enhance/player/url（eapi 加密，见 eapi.go）
package wy

import (
	"context"
	"encoding/json"
	"errors"
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
	// DefaultBase 是接口根地址。
	DefaultBase = "https://music.163.com"
	// DefaultEapiBase 是 eapi 取链接口的根地址。
	DefaultEapiBase = "https://interface3.music.163.com"
	// DefaultUA 默认请求标识。
	DefaultUA = "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/120.0.0.0 Safari/537.36"
	// DefaultReferer 是必需的来源标识，缺失会被拒。
	DefaultReferer = "https://music.163.com/"
	// maxBody 响应体读取上限。
	maxBody = 8 << 20
)

// Doer 抽象 HTTP 执行器。
type Doer interface {
	Do(req *http.Request) (*http.Response, error)
}

// Config 是接入参数。
type Config struct {
	Src       string
	Base      string
	EapiBase  string
	UserAgent string
	Referer   string
	Expire    time.Duration
	// Cookie 是登录态凭据（形如 "MUSIC_U=xxx"），留空表示匿名。
	//
	// 匿名时官方接口只回 url:null；带上自己的 Cookie 就能直接拿到
	// 真实地址（含无损），这是最稳的一条路，不依赖任何第三方。
	Cookie string
	// AggURL 是第三方签名服务地址；非空时取直链优先走它。
	//
	// 官方接口在未登录时只返回 url:null，匿名部署只能借这条路拿地址；
	// 留空则退回只走官方接口（行为与未接入聚合时一致）。
	AggURL string
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
		cfg.Src = "wy"
	}
	if cfg.Base == "" {
		cfg.Base = DefaultBase
	}
	if cfg.EapiBase == "" {
		cfg.EapiBase = DefaultEapiBase
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

// URL 实现 source.Source：三条通道依次尝试（weapi → 聚合 → eapi）。
//
// weapi 是匿名可用的老接口，免费曲目能直接拿到 128k 地址；
// 聚合服务需要可用的第三方签名；eapi 需要登录态 Cookie。
// 三条都不通时，把各自的原因一起带回，避免只看到一句「取直链失败」。
func (c *Client) URL(ctx context.Context, id, q string) (source.LinkInfo, error) {
	id = strings.TrimSpace(id)
	if id == "" {
		return source.LinkInfo{}, apierr.New(apierr.CodeBadRequest, "缺少网易云歌曲 ID")
	}

	var reasons []string

	if li, err := c.urlWeapi(ctx, id, q); err == nil {
		return li, nil
	} else {
		reasons = append(reasons, "weapi: "+err.Error())
	}

	if c.cfg.AggURL != "" {
		if li, err := c.urlAgg(ctx, id, q); err == nil {
			return li, nil
		} else {
			reasons = append(reasons, "聚合: "+err.Error())
		}
	}

	if li, err := c.urlEapi(ctx, id, q); err == nil {
		return li, nil
	} else {
		reasons = append(reasons, "eapi: "+err.Error())
	}

	return source.LinkInfo{}, apierr.Wrap(apierr.CodeUnavailable,
		"wy: 所有取链通道都没拿到直链",
		errors.New(strings.Join(reasons, " | ")))
}

// urlEapi 走官方 eapi 加密接口取直链。
//
// 把 {"ids":"[id]","br":br} 用 eapi 加密后以表单 POST，
// 响应里 data[0].url 即直链；url 为 null 表示需要会员或无版权，如实报错。
func (c *Client) urlEapi(ctx context.Context, id, q string) (source.LinkInfo, error) {
	br, canonical := bitrateFor(q)

	// 用结构体而不是 map：字段顺序固定，便于黄金测试
	params, err := EapiParams("/api/song/enhance/player/url", urlPayload{
		IDs: "[" + id + "]",
		Br:  br,
	})
	if err != nil {
		return source.LinkInfo{}, apierr.Wrap(apierr.CodeInternal, "wy: 生成 eapi 参数失败", err)
	}

	form := url.Values{}
	form.Set("params", params)
	u := strings.TrimRight(c.cfg.EapiBase, "/") + "/eapi/song/enhance/player/url"

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, u, strings.NewReader(form.Encode()))
	if err != nil {
		return source.LinkInfo{}, apierr.Wrap(apierr.CodeInternal, "wy: 构造请求失败", err)
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("User-Agent", c.cfg.UserAgent)
	req.Header.Set("Referer", c.cfg.Referer)
	// 环境标识必须有；配了登录态就一起带上，那样才拿得到真实地址
	req.Header.Set("Cookie", c.cookieHeader())

	body, err := c.post(req)
	if err != nil {
		return source.LinkInfo{}, err
	}
	link, err := ParsePlayerURL(body)
	if err != nil {
		return source.LinkInfo{}, apierr.Wrap(apierr.CodeUnavailable, "wy: 取直链失败", err)
	}
	return source.LinkInfo{
		Qual: canonical,
		Expi: c.now().Add(c.cfg.Expire).Unix(),
		Link: link,
	}, nil
}

// urlPayload 是 eapi 取链的请求体。
type urlPayload struct {
	IDs string `json:"ids"`
	Br  int    `json:"br"`
}

// bitrateFor 把音质映射成网易云的 br 值。
func bitrateFor(q string) (int, string) {
	c, ok := quality.Canonical(q)
	if !ok {
		return 128000, quality.Q128
	}
	switch c {
	case quality.Q192:
		return 192000, quality.Q192
	case quality.Q320:
		return 320000, quality.Q320
	case quality.QFlac:
		return 999000, quality.QFlac
	case quality.QFlac24:
		return 1999000, quality.QFlac24
	}
	return 128000, quality.Q128
}

// ParsePlayerURL 从 eapi 响应里取直链。
func ParsePlayerURL(body []byte) (string, error) {
	var resp struct {
		Data []struct {
			URL  *string `json:"url"`
			Br   int     `json:"br"`
			Code int     `json:"code"`
		} `json:"data"`
	}
	if err := json.Unmarshal(body, &resp); err != nil {
		return "", fmt.Errorf("wy: 响应不是 JSON: %w", err)
	}
	if len(resp.Data) == 0 {
		return "", errors.New("wy: 响应里没有 data")
	}
	d := resp.Data[0]
	if d.URL == nil || *d.URL == "" {
		return "", fmt.Errorf("wy: 直链为空（code=%d，通常是会员曲目或无版权）", d.Code)
	}
	return *d.URL, nil
}

// Lyric 实现 source.Source。
func (c *Client) Lyric(ctx context.Context, id string) (source.LyricInfo, error) {
	id = strings.TrimSpace(id)
	if id == "" {
		return source.LyricInfo{}, apierr.New(apierr.CodeBadRequest, "缺少网易云歌曲 ID")
	}
	q := url.Values{}
	q.Set("id", id)
	q.Set("lv", "-1")
	q.Set("kv", "-1")
	q.Set("tv", "-1")

	body, err := c.get(ctx, "/api/song/lyric", q)
	if err != nil {
		return source.LyricInfo{}, err
	}
	li, err := ParseLyric(body)
	if err != nil {
		return source.LyricInfo{}, apierr.Wrap(apierr.CodeUnavailable, "wy: 解析歌词失败", err)
	}
	return li, nil
}

// Movie 实现 source.Source。
func (c *Client) Movie(context.Context, string, string) (source.LinkInfo, error) {
	return source.LinkInfo{}, apierr.New(apierr.CodeUnavailable, "wy: 该能力尚未接入")
}

// Music 实现 source.Source。
func (c *Client) Music(context.Context, string) (source.MusicInfo, error) {
	return source.MusicInfo{}, apierr.New(apierr.CodeUnavailable, "wy: 该能力尚未接入")
}

// Video 实现 source.Source。
func (c *Client) Video(context.Context, string) (source.MovieInfo, error) {
	return source.MovieInfo{}, apierr.New(apierr.CodeUnavailable, "wy: 该能力尚未接入")
}

// Pic 实现 source.Source。
func (c *Client) Pic(context.Context, string, string) (string, error) {
	return "", apierr.New(apierr.CodeUnavailable, "wy: 该能力尚未接入")
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
	if size <= 0 {
		size = 10
	}
	if size > 50 {
		size = 50
	}
	q := url.Values{}
	q.Set("s", keyword)
	q.Set("type", "1")
	q.Set("limit", strconv.Itoa(size))
	q.Set("offset", strconv.Itoa((page-1)*size))

	body, err := c.get(ctx, "/api/search/get", q)
	if err != nil {
		return source.SearchResult{}, err
	}
	res, err := ParseSearch(body)
	if err != nil {
		return source.SearchResult{}, apierr.Wrap(apierr.CodeUnavailable, "wy: 解析搜索结果失败", err)
	}
	return res, nil
}

// get 发起 GET 请求。Referer 是必需项。
func (c *Client) get(ctx context.Context, path string, params url.Values) ([]byte, error) {
	u := strings.TrimRight(c.cfg.Base, "/") + path
	if len(params) > 0 {
		u += "?" + params.Encode()
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return nil, apierr.Wrap(apierr.CodeInternal, "wy: 构造请求失败", err)
	}
	req.Header.Set("User-Agent", c.cfg.UserAgent)
	req.Header.Set("Referer", c.cfg.Referer)
	req.Header.Set("Cookie", c.cookieHeader())
	return c.post(req)
}

// post 实际执行请求（GET 也走这里）。
func (c *Client) post(req *http.Request) ([]byte, error) {
	resp, err := c.doer.Do(req)
	if err != nil {
		return nil, apierr.Wrap(apierr.CodeUnavailable, "wy: 请求上游失败", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, apierr.Newf(apierr.CodeUnavailable, "wy: 上游返回 HTTP %d", resp.StatusCode)
	}
	return io.ReadAll(io.LimitReader(resp.Body, maxBody))
}

// ParseSearch 解析搜索响应。
func ParseSearch(body []byte) (source.SearchResult, error) {
	var resp struct {
		Code   int `json:"code"`
		Result struct {
			SongCount int `json:"songCount"`
			Songs     []struct {
				ID       int64  `json:"id"`
				Name     string `json:"name"`
				Duration int64  `json:"duration"`
				Artists  []struct {
					Name string `json:"name"`
				} `json:"artists"`
				Album struct {
					Name string `json:"name"`
				} `json:"album"`
			} `json:"songs"`
		} `json:"result"`
	}
	if err := json.Unmarshal(body, &resp); err != nil {
		return source.SearchResult{}, fmt.Errorf("wy: 响应不是 JSON: %w", err)
	}
	out := source.SearchResult{Total: resp.Result.SongCount}
	for _, s := range resp.Result.Songs {
		if s.ID == 0 || s.Name == "" {
			continue
		}
		item := source.MusicInfo{
			ID:   strconv.FormatInt(s.ID, 10),
			Name: s.Name,
			Al:   source.AlbumInfo{Name: s.Album.Name},
			Dt:   uint32(s.Duration / 1000), // 上游给毫秒，这里统一成秒
		}
		for _, a := range s.Artists {
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

// ParseLyric 解析歌词响应。
//
// 响应结构：{"lrc":{"lyric":"..."},"tlyric":{...},"klyric":{...}}
func ParseLyric(body []byte) (source.LyricInfo, error) {
	var resp struct {
		Lrc struct {
			Lyric string `json:"lyric"`
		} `json:"lrc"`
		TLyric struct {
			Lyric string `json:"lyric"`
		} `json:"tlyric"`
		KLyric struct {
			Lyric string `json:"lyric"`
		} `json:"klyric"`
	}
	if err := json.Unmarshal(body, &resp); err != nil {
		return source.LyricInfo{}, fmt.Errorf("wy: 响应不是 JSON: %w", err)
	}
	if strings.TrimSpace(resp.Lrc.Lyric) == "" {
		return source.LyricInfo{}, fmt.Errorf("wy: 响应里没有歌词（可能无版权或纯音乐）")
	}
	return source.LyricInfo{
		Lyric:   resp.Lrc.Lyric,
		TLyric:  resp.TLyric.Lyric,
		LxLyric: resp.KLyric.Lyric, // klyric 是逐字歌词，正好对应 lxlyric
	}, nil
}

// cookieHeader 组装请求要用的 Cookie：
// 环境标识 os=pc 永远带上；配置了登录态就拼在前面（同名时以配置为准）。
func (c *Client) cookieHeader() string {
	ck := strings.TrimSpace(c.cfg.Cookie)
	if ck == "" {
		return "os=pc"
	}
	if strings.Contains(ck, "os=") {
		return ck
	}
	return ck + "; os=pc"
}
