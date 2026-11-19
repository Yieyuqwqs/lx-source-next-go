// Package server 把各层依赖装配成一个可运行的 HTTP 应用。
//
// 这是整个工程里唯一知道「有哪些中间件、按什么顺序、路由怎么挂」的地方。
// 中间件包之间互不认识，业务包也不认识它们；
// 想加一个中间件、去掉一个中间件、调换顺序，只改本文件的 New。
package server

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"time"

	"github.com/lxsrcs/lx-source-next/pkg/apierr"
	"github.com/lxsrcs/lx-source-next/pkg/cache"
	"github.com/lxsrcs/lx-source-next/pkg/config"
	"github.com/lxsrcs/lx-source-next/pkg/httpx"
	"github.com/lxsrcs/lx-source-next/pkg/logx"
	"github.com/lxsrcs/lx-source-next/pkg/middleware"
	"github.com/lxsrcs/lx-source-next/pkg/middleware/access"
	"github.com/lxsrcs/lx-source-next/pkg/middleware/apikey"
	"github.com/lxsrcs/lx-source-next/pkg/middleware/cors"
	"github.com/lxsrcs/lx-source-next/pkg/middleware/ratelimit"
	"github.com/lxsrcs/lx-source-next/pkg/middleware/recoverer"
	"github.com/lxsrcs/lx-source-next/pkg/middleware/reqlog"
	"github.com/lxsrcs/lx-source-next/pkg/source"
)

// Deps 是装配所需的全部依赖。没有全局变量，一切显式传入。
type Deps struct {
	// Config 提供原子快照，是唯一的配置来源。
	Config *config.Holder
	// Logger 由组合根注入；中间件只认 *slog.Logger。
	Logger *slog.Logger
	// LogHub 为 SSE 日志流提供订阅。可为 nil。
	LogHub *logx.Hub
	// Limiter 可替换为 Redis 等实现。为 nil 时用内置内存实现。
	Limiter ratelimit.Limiter
	// Cache 是读穿缓存。为 nil 时不提供缓存观测。
	Cache *cache.Reader
	// Files 是缓存文件路径解析能力。为 nil 时文件路由返回 503。
	Files FileStore
	// Sources 是已注册的解析源。为空时 v0 接口返回 503。
	Sources source.Registry
	// Options 决定装哪些中间件。
	Options Options
	// SourcesEnabled 判断某来源当前是否启用（随热更即时生效）；nil 表示全开。
	SourcesEnabled func(code string) bool
}

// Options 控制中间件的装配。
type Options struct {
	// TrustProxy 决定是否信任转发头。
	TrustProxy bool
	// RealIPHeaders 自定义转发头，空时用默认。
	RealIPHeaders []string
	// EnableRequestLog 访问日志。
	EnableRequestLog bool
	// EnableAccess IP 黑白名单。
	EnableAccess bool
	// EnableApiKey 接口密钥。
	EnableApiKey bool
	// EnableRateLimit 限流。
	EnableRateLimit bool
	// EnableCors 加跨域响应头（给网页端直接调用）。
	EnableCors bool
}

// App 是装配完成的应用。
type App struct {
	deps      Deps
	handler   http.Handler
	startedAt time.Time
}

// New 装配应用。
func New(d Deps) *App {
	if d.Logger == nil {
		d.Logger = slog.New(slog.NewTextHandler(io.Discard, nil))
	}
	a := &App{deps: d, startedAt: time.Now()}
	a.handler = a.chain().Then(a.routes())
	return a
}

// Handler 返回可挂到任意 http.Server 上的 handler，便于测试与嵌入。
func (a *App) Handler() http.Handler { return a.handler }

// dynamic 取当前配置快照。
func (a *App) dynamic() *config.Dynamic {
	if a.deps.Config == nil {
		return config.Default()
	}
	if s := a.deps.Config.Current(); s != nil && s.Dyn != nil {
		return s.Dyn
	}
	return config.Default()
}

// chain 组装中间件链。顺序即语义：
//
//	客户端地址 → 访问日志 → panic 兜底 → 黑白名单 → 密钥 → 限流 → 路由
//
// 前两个是观测，后面的才是策略；策略失败直接短路，不进入业务。
func (a *App) chain() middleware.Chain {
	opt := a.deps.Options
	logger := a.deps.Logger

	var c middleware.Chain

	// 解析客户端地址必须最靠前：后续策略都依赖它。
	c = c.Append(clientAddr(opt))

	if opt.EnableCors {
		c = c.Append(cors.New())
	}

	if opt.EnableRequestLog {
		c = c.Append(reqlog.New(logger))
	}
	c = c.Append(recoverer.New(logger))

	if opt.EnableAccess {
		c = c.Append(access.New(func() access.Config {
			b := a.dynamic().Auth.BanList
			return access.Config{Enabled: b.Enable, Strict: b.Strict, White: b.White, Black: b.Black}
		}, logger))
	}

	if opt.EnableApiKey {
		c = c.Append(apikey.New(func() apikey.Rules {
			p := a.dynamic().Auth.ApiPass
			return apikey.Rules{Enabled: p.Enable, Key: p.Value}
		}, logger))
	}

	if opt.EnableRateLimit {
		c = c.Append(ratelimit.New(ratelimit.Options{
			Rules: func() ratelimit.Rules {
				r := a.dynamic().Auth.RateLimit
				window := time.Second
				if r.Block > 0 {
					window = time.Duration(r.Block) * time.Second
				}
				return ratelimit.Rules{
					Enabled: r.Enable,
					Global:  r.Globle,
					Single:  r.Single,
					Window:  window,
				}
			},
			Limiter: a.deps.Limiter,
			// 白名单来源跳过限流
			Skip:   func(r *http.Request) bool { return httpx.Trusted(r.Context()) },
			Logger: logger,
		}))
	}
	return c
}

// clientAddr 注入客户端地址解析。
func clientAddr(opt Options) middleware.Middleware {
	resolver := httpx.IPResolver{TrustProxy: opt.TrustProxy, Headers: opt.RealIPHeaders}
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			addr := resolver.Resolve(r)
			next.ServeHTTP(w, r.WithContext(httpx.WithClientAddr(r.Context(), addr)))
		})
	}
}

// routes 注册路由。Go 1.22 的 ServeMux 支持方法与通配符，无需框架。
func (a *App) routes() http.Handler {
	mux := http.NewServeMux()
	// 根路径：非 ApiOnly 时给一个极简状态页，ApiOnly 时按 404 处理
	mux.HandleFunc("GET /{$}", a.handleIndex)
	mux.HandleFunc("GET /api/v0/ping", a.handlePing)
	mux.HandleFunc("GET /api/v1/status", a.handleStatus)
	mux.HandleFunc("GET /api/v1/search", a.handleSearch)
	mux.HandleFunc("GET /api/v1/test/sse", a.handleSSE)
	// 缓存文件下载：{source}/{musicid}/{quality}.{ext}
	mux.HandleFunc("GET /api/v1/file/{source}/{musicid}/{name}", a.handleFile)

	// 自定义源脚本下载
	mux.HandleFunc("GET /api/v0/lx-script.js", a.handleLXScript)
	mux.HandleFunc("GET /api/v0/script", a.handleLXScript)

	// v0 契约：{method}/{source}/{id}/{quality}
	// quality 允许为空（文档要求此时保留尾部斜杠），因此同时注册两种形状。
	mux.HandleFunc("GET /api/v0/{method}/{source}/{id}/{quality...}", a.handleV0)
	mux.HandleFunc("GET /api/v0/{method}/{source}/{id}", a.handleV0)
	return mux
}

func (a *App) handlePing(w http.ResponseWriter, r *http.Request) {
	httpx.OK(w, "pong")
}

func (a *App) handleStatus(w http.ResponseWriter, r *http.Request) {
	body := map[string]any{
		"uptime": time.Since(a.startedAt).Round(time.Second).String(),
	}
	// 配置可能在启动瞬间还没有快照，这里不能直接解引用
	if a.deps.Config != nil {
		if snap := a.deps.Config.Current(); snap != nil {
			body["configVersion"] = snap.Version
			body["configLoaded"] = snap.LoadedAt.Format(time.RFC3339)
		}
	}
	if a.deps.LogHub != nil {
		body["logSubscribers"] = a.deps.LogHub.Subscribers()
	}
	if a.deps.Cache != nil {
		st := a.deps.Cache.Stats()
		body["cache"] = map[string]any{
			"hits":     st.Hits,
			"misses":   st.Misses,
			"loads":    st.Loads,
			"errors":   st.Errors,
			"hitRate":  st.HitRate(),
			"inFlight": a.deps.Cache.InFlight(),
		}
	}
	httpx.OK(w, body)
}

// logEvent 是 SSE 推送的事件体。刻意不用 slog.Attr 直出，
// 而是转成稳定的 map，避免内部类型影响对外协议。
type logEvent struct {
	Time  string            `json:"time"`
	Level string            `json:"level"`
	Group string            `json:"group,omitempty"`
	Msg   string            `json:"msg"`
	Attrs map[string]string `json:"attrs,omitempty"`
}

func (a *App) handleSSE(w http.ResponseWriter, r *http.Request) {
	if a.deps.LogHub == nil {
		httpx.Fail(w, apierr.New(apierr.CodeUnavailable, "日志推送未启用"))
		return
	}
	if !a.dynamic().Apis.WebLog {
		httpx.Fail(w, apierr.New(apierr.CodeForbidden, "未开启网页日志"))
		return
	}
	flusher, ok := w.(http.Flusher)
	if !ok {
		httpx.Fail(w, apierr.New(apierr.CodeInternal, "当前连接不支持流式推送"))
		return
	}

	h := w.Header()
	h.Set("Content-Type", "text/event-stream; charset=utf-8")
	h.Set("Cache-Control", "no-cache")
	h.Set("Connection", "keep-alive")
	h.Set("X-Accel-Buffering", "no")
	w.WriteHeader(http.StatusOK)

	ch, cancel := a.deps.LogHub.Subscribe(128)
	defer cancel()

	// 先回放缓冲，客户端一接上就能看到最近的日志
	for _, rec := range a.deps.LogHub.Records() {
		writeEvent(w, rec)
	}
	flusher.Flush()

	ping := time.NewTicker(20 * time.Second)
	defer ping.Stop()
	for {
		select {
		case <-r.Context().Done():
			return
		case rec, open := <-ch:
			if !open {
				return
			}
			writeEvent(w, rec)
			flusher.Flush()
		case <-ping.C:
			_, _ = io.WriteString(w, ": ping\n\n")
			flusher.Flush()
		}
	}
}

func writeEvent(w io.Writer, rec logx.Record) {
	ev := logEvent{
		Time:  rec.Time.Format(time.RFC3339),
		Level: rec.Level.String(),
		Group: rec.Group,
		Msg:   rec.Msg,
	}
	if len(rec.Attrs) > 0 {
		ev.Attrs = make(map[string]string, len(rec.Attrs))
		for _, at := range rec.Attrs {
			ev.Attrs[at.Key] = at.Value.String()
		}
	}
	b, err := json.Marshal(ev)
	if err != nil {
		return
	}
	_, _ = io.WriteString(w, "event: log\ndata: ")
	_, _ = w.Write(b)
	_, _ = io.WriteString(w, "\n\n")
}

// sourceEnabled 判断来源当前是否启用。
func (a *App) sourceEnabled(code string) bool {
	if a.deps.SourcesEnabled == nil {
		return true
	}
	return a.deps.SourcesEnabled(code)
}

// handleIndex 是根路径页面。
//
// 本服务不带前端应用，这里只给一个自检页；ApiOnly 打开时直接 404，降低被扫风险。
func (a *App) handleIndex(w http.ResponseWriter, r *http.Request) {
	if a.dynamic().Apis.ApiOnly {
		httpx.Fail(w, apierr.New(apierr.CodeNotFound, "接口模式下不提供页面"))
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_, _ = io.WriteString(w, indexHTML)
}

// indexHTML 是自检页内容（纯静态，不带任何用户输入）。
const indexHTML = `<!doctype html>
<meta charset="utf-8">
<title>lx-source-next</title>
<style>body{font:14px/1.7 system-ui;margin:2rem auto;max-width:46rem;padding:0 1rem}</style>
<h1>lx-source-next</h1>
<p>服务在运行。可用接口：</p>
<ul>
<li><code>/api/v0/ping</code> 健康检查</li>
<li><code>/api/v0/url/{source}/{id}/{quality}</code> 取播放直链</li>
<li><code>/api/v0/lrc/{source}/{id}</code> 取歌词</li>
<li><code>/api/v0/lx-script.js</code> 自定义源脚本</li>
<li><code>/api/v1/search?pt=&amp;q=</code> 搜索</li>
<li><code>/api/v1/status</code> 运行状态</li>
</ul>
`

// Run 启动服务并在 ctx 结束时优雅关闭。
func (a *App) Run(ctx context.Context, addr string) error {
	srv := &http.Server{
		Addr:              addr,
		Handler:           a.handler,
		ReadHeaderTimeout: 10 * time.Second,
	}
	errCh := make(chan error, 1)
	go func() {
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			errCh <- err
		}
	}()
	a.deps.Logger.Info("服务已启动", "addr", addr)

	select {
	case err := <-errCh:
		return err
	case <-ctx.Done():
		shutCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		a.deps.Logger.Info("正在关闭服务")
		return srv.Shutdown(shutCtx)
	}
}
