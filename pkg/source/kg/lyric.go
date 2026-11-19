package kg

// 酷狗歌词：先 krcs 搜索拿候选（id + accesskey），再 download 取 base64 的 LRC。
// 两条都是匿名接口，只需要带上酷狗官网的 Referer。

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"io"
	"net/http"
	"net/url"
	"strings"

	"github.com/lxsrcs/lx-source-next/pkg/apierr"
	"github.com/lxsrcs/lx-source-next/pkg/source"
)

const (
	// lyricSearchURL 是候选查询入口。
	lyricSearchURL = "https://krcs.kugou.com/search"
	// lyricDownloadURL 是歌词下载入口。
	lyricDownloadURL = "https://lyrics.kugou.com/download"
	// lyricReferer 是这两个接口要求的来源标识。
	lyricReferer = "https://www.kugou.com/"
)

// lyricCandidate 是候选歌词。
type lyricCandidate struct {
	ID        string `json:"id"`
	AccessKey string `json:"accesskey"`
	Song      string `json:"song"`
	Singer    string `json:"singer"`
}

// lyricSearchResp 是候选查询响应。
type lyricSearchResp struct {
	Status     int              `json:"status"`
	Candidates []lyricCandidate `json:"candidates"`
}

// lyricDownloadResp 是下载响应。
type lyricDownloadResp struct {
	Status  int    `json:"status"`
	Fmt     string `json:"fmt"`
	Content string `json:"content"`
}

// getWithReferer 发一个带 Referer 的 GET（歌词接口必需）。
func (c *Client) getWithReferer(ctx context.Context, u, referer string) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return nil, apierr.Wrap(apierr.CodeInternal, "kg: 构造请求失败", err)
	}
	req.Header.Set("User-Agent", c.cfg.UserAgent)
	req.Header.Set("Referer", referer)
	resp, err := c.doer.Do(req)
	if err != nil {
		return nil, apierr.Wrap(apierr.CodeUnavailable, "kg: 请求上游失败", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, apierr.Newf(apierr.CodeUnavailable, "kg: 上游返回 HTTP %d", resp.StatusCode)
	}
	return readLimited(resp)
}

// Lyric 实现 source.Source：取歌词。
//
// 参数 id 是歌曲的 128k hash（与取链一致），上游用它反查歌词候选。
func (c *Client) Lyric(ctx context.Context, id string) (source.LyricInfo, error) {
	hash := strings.TrimSpace(id)
	if hash == "" {
		return source.LyricInfo{}, apierr.New(apierr.CodeBadRequest, "缺少酷狗 hash")
	}

	q := url.Values{}
	q.Set("ver", "1")
	q.Set("man", "yes")
	q.Set("client", "mobi")
	q.Set("keyword", "")
	q.Set("duration", "")
	q.Set("hash", hash)
	body, err := c.getWithReferer(ctx, lyricSearchURL+"?"+q.Encode(), lyricReferer)
	if err != nil {
		return source.LyricInfo{}, err
	}
	var sr lyricSearchResp
	if err := json.Unmarshal(body, &sr); err != nil {
		return source.LyricInfo{}, apierr.Wrap(apierr.CodeUnavailable, "kg: 歌词候选不是 JSON", err)
	}
	if len(sr.Candidates) == 0 {
		return source.LyricInfo{}, apierr.Newf(apierr.CodeUnavailable, "kg: 没有找到歌词候选（status=%d）", sr.Status)
	}
	cand := sr.Candidates[0]

	dq := url.Values{}
	dq.Set("ver", "1")
	dq.Set("client", "pc")
	dq.Set("id", cand.ID)
	dq.Set("accesskey", cand.AccessKey)
	dq.Set("fmt", "lrc")
	dq.Set("charset", "utf8")
	body, err = c.getWithReferer(ctx, lyricDownloadURL+"?"+dq.Encode(), lyricReferer)
	if err != nil {
		return source.LyricInfo{}, err
	}
	var dr lyricDownloadResp
	if err := json.Unmarshal(body, &dr); err != nil {
		return source.LyricInfo{}, apierr.Wrap(apierr.CodeUnavailable, "kg: 歌词下载响应不是 JSON", err)
	}
	if dr.Content == "" {
		return source.LyricInfo{}, apierr.Newf(apierr.CodeUnavailable, "kg: 歌词内容为空（status=%d fmt=%s）", dr.Status, dr.Fmt)
	}
	text, err := base64.StdEncoding.DecodeString(dr.Content)
	if err != nil {
		return source.LyricInfo{}, apierr.Wrap(apierr.CodeUnavailable, "kg: 歌词 base64 解码失败", err)
	}
	lrc := strings.TrimPrefix(string(text), "\ufeff")
	if strings.TrimSpace(lrc) == "" {
		return source.LyricInfo{}, apierr.New(apierr.CodeUnavailable, "kg: 歌词解码后为空")
	}
	return source.LyricInfo{Lyric: lrc}, nil
}

// readLimited 读取响应体（与 getURL 共用上限）。
func readLimited(resp *http.Response) ([]byte, error) {
	return io.ReadAll(io.LimitReader(resp.Body, maxBody))
}
