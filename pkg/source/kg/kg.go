// Package kg 对接酷狗的公开匿名接口。
//
// 实测可用的两条：
//   - 搜索：mobilecdn.kugou.com/api/v3/search/song（返回 hash、歌名、歌手、时长）
//   - 直链：m.kugou.com/app/i/getSongInfo.php?cmd=playInfo&hash=…
//
// 酷狗用 hash 作为歌曲标识，因此 URL(id) 里的 id 就是 hash。
package kg

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
	"github.com/lxsrcs/lx-source-next/pkg/source"
)

const (
	// DefaultSearchBase 是搜索接口地址。
	//
	// 必须用 http：mobilecdn.kugou.com 解析到腾讯云 CDN，
	// 其证书不包含该域名，走 https 会直接 x509 校验失败。
	DefaultSearchBase = "http://mobilecdn.kugou.com"
	// DefaultInfoBase 是歌曲信息接口地址。
	DefaultInfoBase = "http://m.kugou.com"
	// DefaultUA 默认请求标识。
	DefaultUA = "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/120.0.0.0 Safari/537.36"
	// maxBody 响应体读取上限。
	maxBody = 8 << 20
)

// Doer 抽象 HTTP 执行器，测试可注入。
type Doer interface {
	Do(req *http.Request) (*http.Response, error)
}

// Config 是接入参数。
type Config struct {
	Src        string
	SearchBase string
	InfoBase   string
	UserAgent  string
	Expire     time.Duration
	SearchPath string
	InfoPath   string
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
		cfg.Src = "kg"
	}
	if cfg.SearchBase == "" {
		cfg.SearchBase = DefaultSearchBase
	}
	if cfg.InfoBase == "" {
		cfg.InfoBase = DefaultInfoBase
	}
	if cfg.SearchPath == "" {
		cfg.SearchPath = "/api/v3/search/song"
	}
	if cfg.InfoPath == "" {
		cfg.InfoPath = "/app/i/getSongInfo.php"
	}
	if cfg.UserAgent == "" {
		cfg.UserAgent = DefaultUA
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

// URL 实现 source.Source：用 hash 取直链。
func (c *Client) URL(ctx context.Context, id, quality string) (source.LinkInfo, error) {
	id = strings.TrimSpace(id)
	if id == "" {
		return source.LinkInfo{}, apierr.New(apierr.CodeBadRequest, "缺少酷狗 hash")
	}
	q := url.Values{}
	q.Set("cmd", "playInfo")
	q.Set("hash", id)
	if quality != "" {
		q.Set("album_id", "")
	}

	body, err := c.get(ctx, c.cfg.InfoBase+c.cfg.InfoPath, q)
	if err != nil {
		return source.LinkInfo{}, err
	}
	link, err := ParseSongURL(body)
	if err != nil {
		// 实测结论：getSongInfo 只返回元信息，不含播放地址；
		// 真正能发直链的 trackercdn 需要签名，匿名请求一律 errcode=20028。
		return source.LinkInfo{}, apierr.Wrap(apierr.CodeUnavailable,
			"kg: 匿名环境取不到直链（该接口只返回元信息，trackercdn 需签名）", err)
	}
	canonical := quality
	if c, ok := normalizeQuality(quality); ok {
		canonical = c
	}
	return source.LinkInfo{
		Qual: canonical,
		Expi: c.now().Add(c.cfg.Expire).Unix(),
		Link: link,
	}, nil
}

// Lyric 实现 source.Source：酷狗公开接口暂不提供，如实报不可用。
func (c *Client) Lyric(context.Context, string) (source.LyricInfo, error) {
	return source.LyricInfo{}, apierr.New(apierr.CodeUnavailable, "kg: 暂未接入歌词")
}

// Movie 实现 source.Source。
func (c *Client) Movie(context.Context, string, string) (source.LinkInfo, error) {
	return source.LinkInfo{}, apierr.New(apierr.CodeUnavailable, "kg: 该能力尚未接入")
}

// Music 实现 source.Source。
func (c *Client) Music(context.Context, string) (source.MusicInfo, error) {
	return source.MusicInfo{}, apierr.New(apierr.CodeUnavailable, "kg: 该能力尚未接入")
}

// Video 实现 source.Source。
func (c *Client) Video(context.Context, string) (source.MovieInfo, error) {
	return source.MovieInfo{}, apierr.New(apierr.CodeUnavailable, "kg: 该能力尚未接入")
}

// Pic 实现 source.Source。
func (c *Client) Pic(context.Context, string, string) (string, error) {
	return "", apierr.New(apierr.CodeUnavailable, "kg: 该能力尚未接入")
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
	q.Set("format", "json")
	q.Set("keyword", keyword)
	q.Set("page", strconv.Itoa(page))
	q.Set("pagesize", strconv.Itoa(size))

	body, err := c.get(ctx, c.cfg.SearchBase+c.cfg.SearchPath, q)
	if err != nil {
		return source.SearchResult{}, err
	}
	res, err := ParseSearch(body)
	if err != nil {
		return source.SearchResult{}, apierr.Wrap(apierr.CodeUnavailable, "kg: 解析搜索结果失败", err)
	}
	return res, nil
}

// get 发起 GET 请求。
func (c *Client) get(ctx context.Context, base string, params url.Values) ([]byte, error) {
	u := strings.TrimRight(base, "/")
	if len(params) > 0 {
		u += "?" + params.Encode()
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return nil, apierr.Wrap(apierr.CodeInternal, "kg: 构造请求失败", err)
	}
	req.Header.Set("User-Agent", c.cfg.UserAgent)

	resp, err := c.doer.Do(req)
	if err != nil {
		return nil, apierr.Wrap(apierr.CodeUnavailable, "kg: 请求上游失败", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, apierr.Newf(apierr.CodeUnavailable, "kg: 上游返回 HTTP %d", resp.StatusCode)
	}
	return io.ReadAll(io.LimitReader(resp.Body, maxBody))
}

// ParseSongURL 从歌曲信息响应里取直链。
//
// 不依赖具体字段名：上游把直链放在哪个键里变过几次，
// 这里递归扫描所有字符串值，挑出第一个像音频/视频地址的。
// 前提是响应里本来就有直链——没有就如实报错，绝不拼接猜测。
func ParseSongURL(body []byte) (string, error) {
	var m map[string]any
	if err := json.Unmarshal(body, &m); err != nil {
		return "", fmt.Errorf("kg: 响应不是 JSON: %w", err)
	}
	if link := findMediaURL(m, 0); link != "" {
		return link, nil
	}
	// errcode 非 0 时把上游信息带出来，便于排查
	if c, ok := m["errcode"].(float64); ok && c != 0 {
		msg, _ := m["error"].(string)
		return "", fmt.Errorf("kg: 上游 errcode=%v msg=%q", c, msg)
	}
	return "", errors.New("kg: 响应里没有可用直链")
}

// findMediaURL 深度优先找第一个像媒体地址的字符串。
func findMediaURL(v any, depth int) string {
	if depth > 4 {
		return ""
	}
	switch t := v.(type) {
	case string:
		s := strings.TrimSpace(t)
		if !strings.HasPrefix(s, "http") {
			return ""
		}
		low := strings.ToLower(s)
		for _, ext := range []string{".mp3", ".flac", ".m4a", ".aac", ".ogg", ".mp4", ".mkv"} {
			if strings.Contains(low, ext) {
				return s
			}
		}
	case map[string]any:
		for _, sub := range t {
			if link := findMediaURL(sub, depth+1); link != "" {
				return link
			}
		}
	case []any:
		for _, sub := range t {
			if link := findMediaURL(sub, depth+1); link != "" {
				return link
			}
		}
	}
	return ""
}

// ParseSearch 解析搜索响应。
func ParseSearch(body []byte) (source.SearchResult, error) {
	var resp struct {
		Status int `json:"status"`
		Data   struct {
			Total int `json:"total"`
			Info  []struct {
				Hash       string `json:"hash"`
				SongName   string `json:"songname"`
				SingerName string `json:"singername"`
				AlbumName  string `json:"album_name"`
				Duration   int    `json:"duration"`
				Bitrate    int    `json:"bitrate"`
				SQFileSize int64  `json:"sqfilesize"`
			} `json:"info"`
		} `json:"data"`
	}
	if err := json.Unmarshal(body, &resp); err != nil {
		return source.SearchResult{}, fmt.Errorf("kg: 响应不是 JSON: %w", err)
	}
	out := source.SearchResult{Total: resp.Data.Total}
	for _, it := range resp.Data.Info {
		if it.Hash == "" || it.SongName == "" {
			continue
		}
		item := source.MusicInfo{
			ID:   it.Hash,
			Name: it.SongName,
			Al:   source.AlbumInfo{Name: it.AlbumName},
			Dt:   uint32(it.Duration),
		}
		if it.SingerName != "" {
			item.Ar = []source.ArtistInfo{{Name: it.SingerName}}
		}
		if it.Bitrate > 0 {
			q := "128k"
			if it.Bitrate >= 320 {
				q = "320k"
			}
			item.File = append(item.File, source.FileInfo{Qual: q})
		}
		if it.SQFileSize > 0 {
			item.File = append(item.File, source.FileInfo{Qual: "flac"})
		}
		out.Items = append(out.Items, item)
	}
	if out.Total == 0 {
		out.Total = len(out.Items)
	}
	return out, nil
}

// normalizeQuality 粗归一化；酷狗的公开接口不区分音质参数。
func normalizeQuality(q string) (string, bool) {
	switch strings.ToLower(strings.TrimSpace(q)) {
	case "128k", "":
		return "128k", true
	case "320k":
		return "320k", true
	case "flac", "flac24bit":
		return "flac", true
	}
	return "", false
}
