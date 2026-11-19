// Package wy 对接网易云的公开匿名接口。
//
// 实测可用：
//   - 搜索：music.163.com/api/search/get（未加密，直接返回 JSON）
//   - 歌词：music.163.com/api/song/lyric（含 lrc / tlyric / klyric）
//
// 不可用：播放直链需要 weapi/eapi 加密，匿名拿不到，这里如实报不可用。
package wy

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
	// DefaultBase 是接口根地址。
	DefaultBase = "https://music.163.com"
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
	UserAgent string
	Referer   string
}

// Client 实现 source.Source 与 source.Searcher。
type Client struct {
	cfg  Config
	doer Doer
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
	if cfg.UserAgent == "" {
		cfg.UserAgent = DefaultUA
	}
	if cfg.Referer == "" {
		cfg.Referer = DefaultReferer
	}
	if doer == nil {
		doer = &http.Client{Timeout: 10 * time.Second}
	}
	return &Client{cfg: cfg, doer: doer}
}

// Src 实现 source.Source。
func (c *Client) Src() string { return c.cfg.Src }

// URL 实现 source.Source：匿名环境拿不到直链，如实报不可用。
func (c *Client) URL(context.Context, string, string) (source.LinkInfo, error) {
	return source.LinkInfo{}, apierr.New(apierr.CodeUnavailable,
		"wy: 播放直链需要加密接口，匿名环境不可用")
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
	if size <= 0 || size > 50 {
		size = 10
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
			Dt:   uint32(s.Duration / 1000), // 上游给毫秒，我们统一用秒
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
// 响应结构：{"lrc":{"lyric":"..."},"tlyric":{"lyric":"..."},"klyric":{"lyric":"..."}}
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
		LxLyric: resp.KLyric.Lyric, // klyric 是逐字歌词，正好对应 LX 的 lxlyric
	}, nil
}
