package server

import (
	_ "embed"
	"net/http"
	"strconv"
	"strings"
)

// 脚本模板随二进制内嵌。
//
//go:embed assets/lxscript.js
var lxScriptTemplate string

// lxScriptVersion 是自定义源脚本的版本号。
const lxScriptVersion = "0.1.2"

// lxScriptName 是自定义源在播放器里显示的名字。
const lxScriptName = "lx-source-next"

// handleLXScript 输出可直接导入播放器的自定义源脚本。
//
// 地址在返回时按请求推导（支持反向代理头），
// 密钥可按需注入，因此同一份模板能适配不同部署。
func (a *App) handleLXScript(w http.ResponseWriter, r *http.Request) {
	script := lxScriptTemplate
	for _, rep := range []struct{ key, val string }{
		{"__API_ADDR__", a.baseURL(r) + "/api/v0"},
		{"__API_KEY__", a.lxScriptKey(r)},
		{"__NAME__", lxScriptName},
		{"__DESC__", "自建解析源，支持酷我"},
		{"__VERSION__", lxScriptVersion},
	} {
		// 值来自请求（Host、key 查询参数），必须转义后再嵌进 JS 字面量，
		// 否则一个带单引号的 key 就能闭合字符串、注入任意代码。
		script = strings.ReplaceAll(script, rep.key, jsString(rep.val))
	}

	h := w.Header()
	h.Set("Content-Type", "application/javascript; charset=utf-8")
	// 脚本内容随请求变化（地址、密钥），不能被中间层缓存
	h.Set("Cache-Control", "no-store")
	if r.URL.Query().Has("raw") {
		h.Set("Content-Disposition", `attachment; filename="`+lxScriptName+`.js"`)
	}
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte(script))
}

// lxScriptKey 决定要不要把接口密钥写进脚本。
//
// 优先级：显式查询参数 > 配置里开启的 Auto。都没有则为空，
// 此时脚本不带密钥，适用于未启用 ApiPass 的部署。
func (a *App) lxScriptKey(r *http.Request) string {
	if k := r.URL.Query().Get("key"); k != "" {
		return k
	}
	p := a.dynamic().Auth.ApiPass
	if p.Enable && p.Auto {
		return p.Value
	}
	return ""
}

// baseURL 推导对外的服务地址。
//
// 只有显式信任代理时才读 X-Forwarded-*，避免被伪造的头带偏。
func (a *App) baseURL(r *http.Request) string {
	scheme := "http"
	if r.TLS != nil {
		scheme = "https"
	}
	host := r.Host
	prefix := ""

	if a.deps.Options.TrustProxy {
		if v := r.Header.Get("X-Forwarded-Proto"); v != "" {
			scheme = firstValue(v)
		}
		if v := r.Header.Get("X-Forwarded-Host"); v != "" {
			host = firstValue(v)
		}
		if v := r.Header.Get("X-Forwarded-Port"); v != "" && !strings.Contains(host, ":") {
			host = host + ":" + firstValue(v)
		}
		if v := r.Header.Get("X-Forwarded-Prefix"); v != "" {
			prefix = "/" + strings.Trim(firstValue(v), "/")
		}
	}
	return scheme + "://" + host + prefix
}

// firstValue 取逗号分隔头的第一个值。
func firstValue(v string) string {
	first, _, _ := strings.Cut(v, ",")
	return strings.TrimSpace(first)
}

// jsString 把任意字符串安全地包成单引号 JS 字面量（不含外层引号）。
//
// 用 strconv.Quote 得到 Go 风格转义后再剥掉外层双引号，
// 结果对 JS 同样合法：转义了 \ " 与所有控制字符。
// 最后把任何残留的单引号替换掉，保证不会闭合调用方的单引号字面量。
func jsString(s string) string {
	q := strconv.Quote(s)
	if len(q) >= 2 {
		q = q[1 : len(q)-1]
	}
	q = strings.ReplaceAll(q, "'", `\x27`)
	// 模板里用的是单引号，Go 转义出的 \" 在单引号字面量里也合法，
	// 但统一成 \x22 更不容易出歧义
	return strings.ReplaceAll(q, `\"`, `\x22`)
}

// 编译期确认脚本里用到的占位符都被替换（防止模板改名后漏改）。
var lxScriptPlaceholders = []string{"__API_ADDR__", "__API_KEY__", "__NAME__", "__DESC__", "__VERSION__"}

func init() {
	for _, p := range lxScriptPlaceholders {
		if !strings.Contains(lxScriptTemplate, p) {
			panic("lxscript 模板缺少占位符: " + p)
		}
	}
}
