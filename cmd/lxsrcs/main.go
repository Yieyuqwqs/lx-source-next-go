// Command lxsrcs 是组合根：
// 它是唯一创建具体实现、并把它们互相接起来的地方。
package main

import (
	"context"
	"flag"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	"github.com/lxsrcs/lx-source-next/pkg/cache"
	cachepolicy "github.com/lxsrcs/lx-source-next/pkg/cache/policy"
	"github.com/lxsrcs/lx-source-next/pkg/config"
	"github.com/lxsrcs/lx-source-next/pkg/logx"
	"github.com/lxsrcs/lx-source-next/pkg/server"
	"github.com/lxsrcs/lx-source-next/pkg/source"
	"github.com/lxsrcs/lx-source-next/pkg/source/kg"
	"github.com/lxsrcs/lx-source-next/pkg/source/kw"
	"github.com/lxsrcs/lx-source-next/pkg/source/wy"
	"github.com/lxsrcs/lx-source-next/pkg/source/mock"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, "启动失败:", err)
		os.Exit(1)
	}
}

func run() error {
	confDir := flag.String("conf", "data", "配置目录")
	flag.Parse()

	bootPath := filepath.Join(*confDir, "bootstrap.toml")
	dynPath := filepath.Join(*confDir, "dynamic.toml")

	// ---- 1. 配置目录：缺失时用内置模板生成，绝不覆盖已有文件 ----
	created, err := config.EnsureFiles(*confDir)
	if err != nil {
		return err
	}

	boot, err := config.LoadBootstrap(bootPath)
	if err != nil {
		return err
	}

	// ---- 2. 日志：标准库 slog + 自定义 Handler 扇出到 SSE ----
	level := slog.LevelInfo
	if boot.Debug {
		level = slog.LevelDebug
	}
	var sink slog.Handler = slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: level})
	if !boot.Console {
		sink = slog.NewTextHandler(discard{}, &slog.HandlerOptions{Level: level})
	}
	hub := logx.NewHub(512)
	logger := slog.New(hub.Handler(sink))
	slog.SetDefault(logger)

	for _, p := range created {
		logger.Info("已生成默认配置", "path", p)
	}

	// ---- 2.5 DNS：Android/Termux 上静态二进制的纯解析器常拿不到配置，
	//             这里在首次出网前决定是否接管 ----
	installResolver(boot, logger)

	// ---- 3. 动态配置：先给默认值，随后异步加载真实配置 ----
	holder := config.NewHolder(&config.Snapshot{
		Dyn:      config.Default(),
		LoadedAt: time.Now(),
		Version:  1,
	})
	reloader := config.NewReloader(holder, config.FileLoader{Path: dynPath}, logger)

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	// ---- 4. 热重载独立运行，与 HTTP 生命周期解耦 ----
	interval := 2 * time.Second
	if d, err := time.ParseDuration(boot.ReloadInterval); err == nil && d > 0 {
		interval = d
	}
	go func() {
		if err := reloader.Run(ctx, interval); err != nil && ctx.Err() == nil {
			logger.Error("热重载退出", "err", err)
		}
	}()

	// ---- 5. 缓存：路径属启动期，规则与 TTL 属热更期 ----
	fileCache, err := cache.NewFile(boot.CacheDir)
	if err != nil {
		return err
	}
	cachePolicy := cachepolicy.NewDynamic(func() cachepolicy.Config {
		d := holder.Dynamic()
		return cachepolicy.Config{
			Mode:         d.Data.Mode,
			TTL:          time.Duration(d.Data.TTL) * time.Second,
			Plats:        d.Data.Rule.Plat,
			ExcludePlat:  d.Data.Rule.ExcP,
			Quals:        d.Data.Rule.Qual,
			ExcludeQual:  d.Data.Rule.ExcQ,
			AllowSpecial: d.Data.Rule.AllowSpecial,
		}
	})
	cacheReader := cache.NewReader(fileCache, cachePolicy, cache.ReaderOptions{Logger: logger})
	logger.Info("缓存已就绪", "dir", fileCache.Root())

	// ---- 6. 解析源：接真实平台时替换这张表即可，handler 不用动 ----
	kwSrc := kw.New(kw.Config{
		Strategy: boot.Kw.Strategy,
		BaseURL:  boot.Kw.BaseURL,
		Key:      []byte(boot.Kw.Key),
	}, nil)

	sources := source.Registry{
		"mock": mock.New(),
		"kw":   kwSrc,
		"kg":   kg.New(kg.Config{}, nil),
		"wy":   wy.New(wy.Config{}, nil),
	}
	logger.Info("已注册解析源", "codes", sources.Codes())
	logger.Info(kwSrc.Describe())

	// ---- 7. 装配应用：所有依赖显式注入 ----
	app := server.New(server.Deps{
		Config:  holder,
		Logger:  logger,
		LogHub:  hub,
		Limiter: nil, // 用内置内存实现；换 Redis 时只改这一行
		Cache:   cacheReader,
		Files:   fileCache,
		Sources: sources,
		Options: server.Options{
			TrustProxy:       boot.Proxy,
			RealIPHeaders:    boot.RealIPHeaders(),
			EnableRequestLog: true,
			EnableAccess:     true,
			EnableApiKey:     true,
			EnableRateLimit:  true,
		},
	})

	// ---- 8. 跑起来 ----
	return app.Run(ctx, boot.Listen)
}

// discard 是 io.Writer 的空实现。
type discard struct{}

func (discard) Write(p []byte) (int, error) { return len(p), nil }
