package server

import (
	"context"
	"encoding/json"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/lxsrcs/lx-source-next/pkg/apierr"
	"github.com/lxsrcs/lx-source-next/pkg/cache"
	"github.com/lxsrcs/lx-source-next/pkg/httpx"
	"github.com/lxsrcs/lx-source-next/pkg/source"
)

// v0Method 是 v0 接口的方法。
type v0Method int

const (
	methodURL v0Method = iota
	methodLyric
	methodMovie
	methodMusic
	methodVideo
	methodPic
)

// v0Aliases 是方法名 → 内部方法。
//
// 别名表是唯一的分发真源：新增别名只改这里，handler 不用动。
var v0Aliases = map[string]v0Method{
	"url":   methodURL,
	"link":  methodURL,
	"lrc":   methodLyric,
	"lyric": methodLyric,
	"mov":   methodMovie,
	"mv":    methodMovie,
	"mid":   methodMusic,
	"vid":   methodVideo,
	"pic":   methodPic,
}

// v0 内部缓存键的命名空间前缀。
//
// 以 _ 开头，与平台二字码区分开；文件路由会拒绝这个前缀，
// 避免内部元数据被当成音频文件对外提供。
const internalPrefix = "_meta/"

// linkExt 是 v0 的 ext 结构。
type linkExt struct {
	Qual string `json:"qual"`
	Expi int64  `json:"expi"`
}

// handleV0 是 v0 契约的统一入口。
func (a *App) handleV0(w http.ResponseWriter, r *http.Request) {
	method, ok := v0Aliases[strings.ToLower(r.PathValue("method"))]
	if !ok {
		httpx.Fail(w, apierr.Newf(apierr.CodeBadRequest, "未知方法 %q", r.PathValue("method")))
		return
	}
	if a.deps.Sources.Len() == 0 {
		httpx.Fail(w, apierr.New(apierr.CodeUnavailable, "没有可用的解析源"))
		return
	}
	code := strings.ToLower(r.PathValue("source"))
	src, ok := a.deps.Sources.Get(code)
	if !ok {
		httpx.Fail(w, apierr.Newf(apierr.CodeNotFound, "未知来源 %q", code))
		return
	}
	if !a.sourceEnabled(code) {
		httpx.Fail(w, apierr.Newf(apierr.CodeUnavailable, "来源 %q 已在配置里停用", code))
		return
	}
	id := r.PathValue("id")
	quality := r.PathValue("quality")
	jmp := r.URL.Query().Has("jmp")

	switch method {
	case methodURL:
		a.v0URL(w, r, src, id, quality, jmp)
	case methodLyric:
		a.v0Lyric(w, r, src, id, jmp)
	case methodMovie:
		a.v0Movie(w, r, src, id, quality, jmp)
	case methodMusic:
		a.v0Music(w, r, src, id)
	case methodVideo:
		a.v0Video(w, r, src, id)
	case methodPic:
		a.v0Pic(w, r, src, id, quality, jmp)
	}
}

// v0URL 处理取播放链接。
//
// 走缓存：同一 (source,id,quality) 在 TTL 内只向上游要一次。
func (a *App) v0URL(w http.ResponseWriter, r *http.Request, src source.Source, id, quality string, jmp bool) {
	// 先把音质归一到该来源真正支持的档位：
	// 同一份文件不该因为请求写法不同而在缓存里存多份。
	q := quality
	if n, ok := src.(source.QualityNormalizer); ok {
		if norm := n.NormalizeQuality(quality); norm != "" {
			q = norm
		}
	}
	key := internalPrefix + src.Src() + "/" + id + "-" + orDefault(q, "default") + ".url"
	load := func(ctx context.Context) ([]byte, error) {
		li, err := src.URL(ctx, id, quality)
		if err != nil {
			return nil, err
		}
		return json.Marshal(li)
	}
	meta := cache.Meta{Source: src.Src(), Quality: q}
	raw, err := a.cachedJSON(r.Context(), key, meta, load)
	if err != nil {
		a.failWithErrorAudio(w, r, err)
		return
	}
	var li source.LinkInfo
	if err := json.Unmarshal(raw, &li); err != nil {
		httpx.Fail(w, apierr.Wrap(apierr.CodeInternal, "缓存数据损坏", err))
		return
	}
	// 缓存 TTL 与直链有效期是两件事：命中缓存也可能是一条已经过期的直链，
	// 这里作废重取一次，避免把死链发给客户端。
	if linkExpired(li) {
		if err := a.invalidate(r.Context(), key); err != nil {
			a.deps.Logger.Warn("作废过期直链缓存失败", "key", key, "err", err)
		}
		if raw, err = a.cachedJSON(r.Context(), key, meta, load); err != nil {
			a.failWithErrorAudio(w, r, err)
			return
		}
		if err := json.Unmarshal(raw, &li); err != nil {
			httpx.Fail(w, apierr.Wrap(apierr.CodeInternal, "缓存数据损坏", err))
			return
		}
	}
	if jmp {
		// 按文档：jmp 支持 url / mv / pic，出错时仍以 JSON 返回
		redirectTo(w, r, li.Link)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, httpx.Envelope{
		Code: 0,
		Data: li.Link,
		Ext:  linkExt{Qual: li.Qual, Expi: li.Expi},
	})
}

// v0Lyric 处理取歌词。带 jmp 时直接吐出纯文本，便于浏览器/播放器直接使用。
func (a *App) v0Lyric(w http.ResponseWriter, r *http.Request, src source.Source, id string, jmp bool) {
	key := internalPrefix + src.Src() + "/" + id + ".lrc"
	// 歌词没有音质概念，Quality 留空即按「可缓存」处理
	raw, err := a.cachedJSON(r.Context(), key, cache.Meta{Source: src.Src()},
		func(ctx context.Context) ([]byte, error) {
			li, err := src.Lyric(ctx, id)
			if err != nil {
				return nil, err
			}
			return json.Marshal(li)
		})
	if err != nil {
		httpx.Fail(w, err)
		return
	}
	var li source.LyricInfo
	if err := json.Unmarshal(raw, &li); err != nil {
		httpx.Fail(w, apierr.Wrap(apierr.CodeInternal, "缓存数据损坏", err))
		return
	}
	if jmp {
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		w.WriteHeader(http.StatusOK)
		writeAll(w, li.Lyric, li.TLyric, li.RLyric)
		return
	}
	httpx.OK(w, li)
}

// v0Movie 处理取视频直链。
func (a *App) v0Movie(w http.ResponseWriter, r *http.Request, src source.Source, id, quality string, jmp bool) {
	li, err := src.Movie(r.Context(), id, quality)
	if err != nil {
		httpx.Fail(w, err)
		return
	}
	if jmp {
		redirectTo(w, r, li.Link)
		return
	}
	httpx.OK(w, li)
}

// v0Music 处理取音乐信息。
func (a *App) v0Music(w http.ResponseWriter, r *http.Request, src source.Source, id string) {
	mi, err := src.Music(r.Context(), id)
	if err != nil {
		httpx.Fail(w, err)
		return
	}
	httpx.OK(w, mi)
}

// v0Video 处理取视频信息。
func (a *App) v0Video(w http.ResponseWriter, r *http.Request, src source.Source, id string) {
	vi, err := src.Video(r.Context(), id)
	if err != nil {
		httpx.Fail(w, err)
		return
	}
	httpx.OK(w, vi)
}

// v0Pic 处理取封面。
func (a *App) v0Pic(w http.ResponseWriter, r *http.Request, src source.Source, id, size string, jmp bool) {
	link, err := src.Pic(r.Context(), id, size)
	if err != nil {
		httpx.Fail(w, err)
		return
	}
	if jmp {
		redirectTo(w, r, link)
		return
	}
	httpx.OK(w, link)
}

// cachedJSON 是缓存读穿的薄封装：没有缓存时直接取上游。
func (a *App) cachedJSON(ctx context.Context, key string, m cache.Meta, load cache.ReadFunc) ([]byte, error) {
	if a.deps.Cache == nil {
		return load(ctx)
	}
	return a.deps.Cache.Get(ctx, key, m, load)
}

// redirectTo 发 302。
//
// 直链本身有时效，绝不能被中间层缓存，因此显式 no-store。
// 目标为空说明上游没给出可用链接，此时仍按文档返回 JSON 错误。
func redirectTo(w http.ResponseWriter, r *http.Request, link string) {
	if link == "" {
		httpx.Fail(w, apierr.New(apierr.CodeUnavailable, "没有可跳转的链接"))
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	http.Redirect(w, r, link, http.StatusFound)
}

func orDefault(v, def string) string {
	if v == "" {
		return def
	}
	return v
}

func writeAll(w http.ResponseWriter, parts ...string) {
	for _, p := range parts {
		if p == "" {
			continue
		}
		_, _ = w.Write([]byte(p))
	}
}

// linkExpired 判断直链是否已过期（留 30 秒余量，避免临界点把死链发出去）。
func linkExpired(li source.LinkInfo) bool {
	return li.Expi > 0 && time.Now().Unix() >= li.Expi-30
}

// invalidate 作废一个内部缓存键。
func (a *App) invalidate(ctx context.Context, key string) error {
	if a.deps.Cache == nil {
		return nil
	}
	return a.deps.Cache.Invalidate(ctx, key)
}

// failWithErrorAudio 按 v0 约定处理取链失败：
// 配置了错误音频就跳过去（客户端能继续播），否则按普通错误返回。
func (a *App) failWithErrorAudio(w http.ResponseWriter, r *http.Request, err error) {
	e := a.dynamic().Errs
	if strings.TrimSpace(e.ErrMp3) != "" {
		cc := "no-store"
		if e.Expi > 0 {
			cc = "public, max-age=" + strconv.Itoa(e.Expi)
		}
		w.Header().Set("Cache-Control", cc)
		http.Redirect(w, r, e.ErrMp3, http.StatusFound)
		return
	}
	httpx.Fail(w, err)
}
