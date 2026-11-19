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
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/lxsrcs/lx-source-next/pkg/apierr"
	"github.com/lxsrcs/lx-source-next/pkg/source"
)

const (
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
	Src          string
	SearchV2Base string
	V5URL        string
	UserAgent    string
	Expire       time.Duration
}

// Client 实现 source.Source 与 source.Searcher。
type Client struct {
	cfg    Config
	doer   Doer
	now    func() time.Time
	mu     sync.RWMutex
	hashes map[string]map[string]string
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
	if cfg.SearchV2Base == "" {
		cfg.SearchV2Base = defaultSearchV2Base
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
	return &Client{cfg: cfg, doer: doer, now: time.Now, hashes: make(map[string]map[string]string)}
}

// Src 实现 source.Source。
func (c *Client) Src() string { return c.cfg.Src }

// URL 实现 source.Source：走 trackercdn v5（带签名）取直链。
//
// v5 的签名算法见 v5.go：每档音质对应一个独立 hash，
// 搜索时已把各档 hash 记进 Client，这里按请求档位挑对应的那个，
// 拿不到就逐档回退，并如实上报最终拿到的档位。
func (c *Client) URL(ctx context.Context, id, quality string) (source.LinkInfo, error) {
	id = strings.TrimSpace(id)
	if id == "" {
		return source.LinkInfo{}, apierr.New(apierr.CodeBadRequest, "缺少酷狗 hash")
	}
	return c.v5URLFor(ctx, c.hashTable(id), id, quality)
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
	if size <= 0 {
		size = 10
	}
	if size > 50 {
		size = 50
	}
	q := url.Values{}
	q.Set("keyword", keyword)
	q.Set("page", strconv.Itoa(page))
	q.Set("pagesize", strconv.Itoa(size))
	q.Set("platform", "WebFilter")
	q.Set("tag", "em")
	q.Set("filter", "2")
	q.Set("iscorrection", "1")
	q.Set("privilege_filter", "0")
	q.Set("userid", "0")

	u := strings.TrimRight(c.cfg.SearchV2Base, "/") + "/song_search_v2?" + q.Encode()
	body, err := c.getURL(ctx, u)
	if err != nil {
		return source.SearchResult{}, err
	}
	res, tables, err := ParseSearchV2(body)
	if err != nil {
		return source.SearchResult{}, apierr.Wrap(apierr.CodeUnavailable, "kg: 解析搜索结果失败", err)
	}
	c.recordHashes(tables)
	return res, nil
}

// recordHashes 记住「档位 → hash」表，供后续取链挑对 hash。
func (c *Client) recordHashes(tables map[string]map[string]string) {
	if len(tables) == 0 {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.hashes == nil || len(c.hashes) > 8192 {
		// 简单上界：超过就整表重建，避免长期运行无限增长
		c.hashes = make(map[string]map[string]string, len(tables))
	}
	for h, t := range tables {
		c.hashes[h] = t
	}
}

// hashTable 返回该 hash 所属的档位表；没有则返回 nil。
func (c *Client) hashTable(hash string) map[string]string {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.hashes[hash]
}

// getURL 发起 GET 并返回响应体（唯一出网点）。
func (c *Client) getURL(ctx context.Context, u string) ([]byte, error) {
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
