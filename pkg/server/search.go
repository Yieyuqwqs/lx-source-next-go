package server

import (
	"net/http"
	"strconv"
	"strings"

	"github.com/lxsrcs/lx-source-next/pkg/apierr"
	"github.com/lxsrcs/lx-source-next/pkg/httpx"
	"github.com/lxsrcs/lx-source-next/pkg/source"
)

// handleSearch 提供 v1 搜索接口。
//
// 参数与文档一致：pt 平台、q 关键词、pg 页码、sz 每页数量。
// 只有实现了 source.Searcher 的来源才可用，其余如实返回 503。
func (a *App) handleSearch(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()

	keyword := strings.TrimSpace(q.Get("q"))
	if keyword == "" {
		httpx.Fail(w, apierr.New(apierr.CodeBadRequest, "缺少搜索关键词 q"))
		return
	}
	pt := strings.ToLower(strings.TrimSpace(q.Get("pt")))
	if pt == "" {
		httpx.Fail(w, apierr.New(apierr.CodeBadRequest, "缺少平台参数 pt"))
		return
	}

	src, ok := a.deps.Sources.Get(pt)
	if !ok {
		httpx.Fail(w, apierr.Newf(apierr.CodeNotFound, "未知来源 %q", pt))
		return
	}
	if !a.sourceEnabled(pt) {
		httpx.Fail(w, apierr.Newf(apierr.CodeUnavailable, "来源 %q 已在配置里停用", pt))
		return
	}
	searcher, ok := src.(source.Searcher)
	if !ok {
		httpx.Fail(w, apierr.Newf(apierr.CodeUnavailable, "来源 %q 不支持搜索", pt))
		return
	}

	res, err := searcher.Search(r.Context(), keyword,
		atoiDefault(q.Get("pg"), 1), atoiDefault(q.Get("sz"), 10))
	if err != nil {
		httpx.Fail(w, err)
		return
	}
	httpx.OK(w, map[string]any{
		"total": res.Total,
		"items": res.Items,
	})
}

func atoiDefault(s string, def int) int {
	if s == "" {
		return def
	}
	n, err := strconv.Atoi(strings.TrimSpace(s))
	if err != nil || n <= 0 {
		return def
	}
	return n
}
