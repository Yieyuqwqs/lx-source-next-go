// Package mg 对接咪咕音乐的 App 接口（MIGUM2.0）。
//
// 两条上游：
//   - 搜索 / 元信息：/v1.0/content/search_all.do（匿名可用，含各档音质规格）
//   - 播放地址：/v1.0/content/sub/listenSong.do（匿名只对免费曲目 302 到真实文件）
//
// 实测（2026-11-20）：免费曲目 PQ/HQ 都能拿到 302 直链并下载成功；
// 带 vip 标记的档位（如 ZQ 24bit）匿名会回「暂不提供试听地址」，必须带登录态。
package mg

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/lxsrcs/lx-source-next/pkg/apierr"
	"github.com/lxsrcs/lx-source-next/pkg/quality"
	"github.com/lxsrcs/lx-source-next/pkg/source"
)

const (
	// DefaultBase 是 App 接口根地址。
	DefaultBase = "https://app.pd.nf.migu.cn"
	// DefaultUA 是 App 客户端标识。
	DefaultUA = "Mozilla/5.0 (Linux; Android 13) AppleWebKit/537.36 Chrome/120 Mobile Safari/537.36"
	// DefaultChannel 是渠道号；缺了会被判成异常客户端。
	DefaultChannel = "014000D"
	// DefaultUserID 是匿名请求用的占位用户号。
	DefaultUserID = "15512345678"
	// maxBody 响应体上限。
	maxBody = 8 << 20
)

// Doer 抽象 HTTP 执行器。
type Doer interface {
	Do(req *http.Request) (*http.Response, error)
}

// Config 是接入参数。
type Config struct {
	Src     string
	Base    string
	UserAgent string
	Channel string
	UserID  string
	// Cookie 是登录态；带上后 vip 档位（含 24bit）才有机会返回地址。
	Cookie string
	Expire time.Duration
}

// Client 实现 source.Source 与 source.Searcher。
type Client struct {
	cfg  Config
	doer Doer
	now  func() time.Time

	mu   sync.RWMutex
	crID map[string]string // contentId → copyrightId
}

var (
	_ source.Source   = (*Client)(nil)
	_ source.Searcher = (*Client)(nil)
)

// New 创建客户端。
func New(cfg Config, doer Doer) *Client {
	if cfg.Src == "" {
		cfg.Src = "mg"
	}
	if cfg.Base == "" {
		cfg.Base = DefaultBase
	}
	if cfg.UserAgent == "" {
		cfg.UserAgent = DefaultUA
	}
	if cfg.Channel == "" {
		cfg.Channel = DefaultChannel
	}
	if cfg.UserID == "" {
		cfg.UserID = DefaultUserID
	}
	if cfg.Expire <= 0 {
		cfg.Expire = 30 * time.Minute
	}
	if doer == nil {
		cli := &http.Client{
			Timeout: 10 * time.Second,
			// 取链靠 302 跳转，必须自己读 Location，不能自动跟随
			CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
		}
		doer = cli
	}
	return &Client{cfg: cfg, doer: doer, now: time.Now, crID: make(map[string]string)}
}

// Src 实现 source.Source。
func (c *Client) Src() string { return c.cfg.Src }

// toneFlag 把规范音质映射成咪咕的 toneFlag。
func toneFlag(qual string) string {
	c, ok := quality.Canonical(qual)
	if !ok {
		return "PQ"
	}
	switch c {
	case quality.Q320:
		return "HQ"
	case quality.QFlac:
		return "SQ"
	case quality.QFlac24:
		return "ZQ"
	}
	return "PQ"
}

// toneFallback 返回从请求档位逐档降到 PQ 的顺序（只降不升）。
func toneFallback(qual string) []string {
	order := []string{"ZQ", "SQ", "HQ", "PQ"}
	top := toneFlag(qual)
	for i, t := range order {
		if t == top {
			return order[i:]
		}
	}
	return order[len(order)-1:]
}

// qualityOfTone 把 toneFlag 反解成规范音质。
func qualityOfTone(t string) string {
	switch t {
	case "ZQ":
		return quality.QFlac24
	case "SQ":
		return quality.QFlac
	case "HQ":
		return quality.Q320
	}
	return quality.Q128
}

// searchAll 是搜索响应。
type searchAll struct {
	Code    string `json:"code"`
	Info    string `json:"info"`
	SongRes struct {
		TotalCount string `json:"totalCount"`
		Result     []struct {
			ContentID   string `json:"contentId"`
			CopyrightID string `json:"copyrightId"`
			Name        string `json:"name"`
			LyricURL    string `json:"lyricUrl"`
			Singers     []struct {
				Name string `json:"name"`
			} `json:"singers"`
			Albums []struct {
				Name string `json:"name"`
			} `json:"albums"`
			RateFormats []struct {
				FormatType string `json:"formatType"`
				Size       string `json:"size"`
			} `json:"newRateFormats"`
		} `json:"result"`
	} `json:"songResultData"`
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
	q.Set("ua", "Android_migu")
	q.Set("version", "5.0")
	q.Set("text", keyword)
	q.Set("pageNo", strconv.Itoa(page))
	q.Set("pageSize", strconv.Itoa(size))
	q.Set("searchSwitch", `{"song":1,"singer":0,"album":0,"playlist":0,"mv":0}`)

	body, err := c.getJSON(ctx, "/MIGUM2.0/v1.0/content/search_all.do?"+q.Encode())
	if err != nil {
		return source.SearchResult{}, err
	}
	var resp searchAll
	if err := json.Unmarshal(body, &resp); err != nil {
		return source.SearchResult{}, apierr.Wrap(apierr.CodeUnavailable, "mg: 解析搜索响应失败", err)
	}
	if resp.Code != "" && resp.Code != "000000" {
		return source.SearchResult{}, apierr.Newf(apierr.CodeUnavailable, "mg: 搜索失败（code=%s %s）", resp.Code, resp.Info)
	}

	out := source.SearchResult{}
	for _, it := range resp.SongRes.Result {
		if it.ContentID == "" || strings.TrimSpace(it.Name) == "" {
			continue
		}
		item := source.MusicInfo{ID: it.ContentID, Name: strings.TrimSpace(it.Name)}
		for _, s := range it.Singers {
			if s.Name != "" {
				item.Ar = append(item.Ar, source.ArtistInfo{Name: s.Name})
			}
		}
		if len(it.Albums) > 0 {
			item.Al = source.AlbumInfo{Name: it.Albums[0].Name}
		}
		for _, f := range it.RateFormats {
			switch f.FormatType {
			case "PQ":
				item.File = append(item.File, source.FileInfo{Qual: quality.Q128, Size: f.Size})
			case "HQ":
				item.File = append(item.File, source.FileInfo{Qual: quality.Q320, Size: f.Size})
			case "SQ":
				item.File = append(item.File, source.FileInfo{Qual: quality.QFlac, Size: f.Size})
			case "ZQ":
				item.File = append(item.File, source.FileInfo{Qual: quality.QFlac24, Size: f.Size})
			}
		}
		out.Items = append(out.Items, item)
		c.remember(it.ContentID, it.CopyrightID)
	}
	if n, err := strconv.Atoi(strings.TrimSpace(resp.SongRes.TotalCount)); err == nil && n > 0 {
		out.Total = n
	} else {
		out.Total = len(out.Items)
	}
	return out, nil
}

// URL 实现 source.Source：按档位请求 listenSong，读 302 的 Location。
func (c *Client) URL(ctx context.Context, id, qual string) (source.LinkInfo, error) {
	id = strings.TrimSpace(id)
	if id == "" {
		return source.LinkInfo{}, apierr.New(apierr.CodeBadRequest, "缺少咪咕 contentId")
	}
	cr := c.copyrightOf(id)
	if cr == "" {
		return source.LinkInfo{}, apierr.New(apierr.CodeUnavailable,
			"mg: 缺少 copyrightId（该 contentId 不是本次进程搜索出来的，先搜一次再点播放）")
	}

	var lastErr error
	for _, tone := range toneFallback(qual) {
		link, err := c.listenURL(ctx, id, cr, tone)
		if err != nil {
			lastErr = err
			continue
		}
		return source.LinkInfo{
			Qual: qualityOfTone(tone),
			Expi: c.now().Add(c.cfg.Expire).Unix(),
			Link: link,
		}, nil
	}
	if lastErr == nil {
		lastErr = fmt.Errorf("mg: 没有可用的档位")
	}
	return source.LinkInfo{}, apierr.Wrap(apierr.CodeUnavailable, "mg: 各档位都拿不到播放地址", lastErr)
}

// listenSong 是取链响应（无 302 时的 JSON 形态）。
type listenSong struct {
	Code    string `json:"code"`
	Info    string `json:"info"`
	PlayURL string `json:"playUrl"`
}

// listenURL 请求一次 listenSong，返回 302 目标或 JSON 里的 playUrl。
func (c *Client) listenURL(ctx context.Context, contentID, copyrightID, tone string) (string, error) {
	q := url.Values{}
	q.Set("channel", c.cfg.Channel)
	q.Set("copyrightId", copyrightID)
	q.Set("contentId", contentID)
	q.Set("toneFlag", tone)
	q.Set("userId", c.cfg.UserID)
	q.Set("netType", "00")
	u := strings.TrimRight(c.cfg.Base, "/") + "/MIGUM2.0/v1.0/content/sub/listenSong.do?" + q.Encode()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return "", apierr.Wrap(apierr.CodeInternal, "mg: 构造请求失败", err)
	}
	c.setHeaders(req)

	resp, err := c.doer.Do(req)
	if err != nil {
		return "", apierr.Wrap(apierr.CodeUnavailable, "mg: 请求上游失败", err)
	}
	defer resp.Body.Close()

	// 302：Location 就是真实文件地址
	if resp.StatusCode == http.StatusFound || resp.StatusCode == http.StatusMovedPermanently {
		if loc := resp.Header.Get("Location"); loc != "" {
			return loc, nil
		}
		return "", fmt.Errorf("mg: 302 但没有 Location")
	}
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("mg: 上游返回 HTTP %d", resp.StatusCode)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, maxBody))
	if err != nil {
		return "", apierr.Wrap(apierr.CodeUnavailable, "mg: 读取响应失败", err)
	}
	var res listenSong
	if err := json.Unmarshal(body, &res); err != nil {
		return "", fmt.Errorf("mg: 取链响应不是 JSON: %w", err)
	}
	if res.PlayURL != "" {
		return res.PlayURL, nil
	}
	if res.Code == "" {
		res.Code = "unknown"
	}
	return "", fmt.Errorf("mg: 未返回播放地址（code=%s %s）", res.Code, res.Info)
}

// Lyric 实现 source.Source：歌词走搜索里带的 lyricUrl。
func (c *Client) Lyric(ctx context.Context, id string) (source.LyricInfo, error) {
	return source.LyricInfo{}, apierr.New(apierr.CodeUnavailable, "mg: 歌词暂未接入（需按 contentId 反查 lyricUrl）")
}

// Movie / Music / Video / Pic：暂时如实报不可用。
func (c *Client) Movie(context.Context, string, string) (source.LinkInfo, error) {
	return source.LinkInfo{}, apierr.New(apierr.CodeUnavailable, "mg: 该能力尚未接入")
}

// Music 实现 source.Source。
func (c *Client) Music(context.Context, string) (source.MusicInfo, error) {
	return source.MusicInfo{}, apierr.New(apierr.CodeUnavailable, "mg: 该能力尚未接入")
}

// Video 实现 source.Source。
func (c *Client) Video(context.Context, string) (source.MovieInfo, error) {
	return source.MovieInfo{}, apierr.New(apierr.CodeUnavailable, "mg: 该能力尚未接入")
}

// Pic 实现 source.Source。
func (c *Client) Pic(context.Context, string, string) (string, error) {
	return "", apierr.New(apierr.CodeUnavailable, "mg: 该能力尚未接入")
}

func (c *Client) setHeaders(req *http.Request) {
	req.Header.Set("User-Agent", c.cfg.UserAgent)
	req.Header.Set("channel", c.cfg.Channel)
	if c.cfg.Cookie != "" {
		req.Header.Set("Cookie", c.cfg.Cookie)
	}
}

// getJSON 发 GET 并返回响应体。
func (c *Client) getJSON(ctx context.Context, path string) ([]byte, error) {
	u := strings.TrimRight(c.cfg.Base, "/") + path
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return nil, apierr.Wrap(apierr.CodeInternal, "mg: 构造请求失败", err)
	}
	c.setHeaders(req)
	resp, err := c.doer.Do(req)
	if err != nil {
		return nil, apierr.Wrap(apierr.CodeUnavailable, "mg: 请求上游失败", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, apierr.Newf(apierr.CodeUnavailable, "mg: 上游返回 HTTP %d", resp.StatusCode)
	}
	return io.ReadAll(io.LimitReader(resp.Body, maxBody))
}

// remember 记住 contentId → copyrightId，取链时要用。
func (c *Client) remember(contentID, copyrightID string) {
	if contentID == "" || copyrightID == "" {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if len(c.crID) > 8192 {
		c.crID = make(map[string]string)
	}
	c.crID[contentID] = copyrightID
}

// copyrightOf 取 contentId 对应的 copyrightId。
func (c *Client) copyrightOf(contentID string) string {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.crID[contentID]
}
