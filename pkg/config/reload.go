package config

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"time"
)

// Loader 提供配置原始字节。抽象成接口后，Reloader 不依赖文件系统，
// 测试可以直接喂字节，也可以换成配置中心、数据库、K8s ConfigMap。
type Loader interface {
	Read() ([]byte, error)
}

// FileLoader 从文件读取配置。
type FileLoader struct {
	Path string
}

// Read 实现 Loader。
func (f FileLoader) Read() ([]byte, error) {
	if f.Path == "" {
		return nil, errors.New("config: 配置路径为空")
	}
	b, err := os.ReadFile(f.Path)
	if err != nil {
		return nil, fmt.Errorf("config: 读取 %s: %w", f.Path, err)
	}
	return b, nil
}

// BytesLoader 用于测试与嵌入式默认值。
type BytesLoader struct {
	Data []byte
	Err  error
}

// Read 实现 Loader。
func (b BytesLoader) Read() ([]byte, error) {
	if b.Err != nil {
		return nil, b.Err
	}
	return b.Data, nil
}

// Outcome 描述一次重载的结果。
type Outcome struct {
	// Changed 是否发布了新快照。
	Changed bool
	// Skipped 内容与当前一致，直接跳过（连解析都省了）。
	Skipped bool
	// Version 发布后的版本号。
	Version uint64
}

// Reloader 把磁盘上的配置原子地搬到内存快照。
//
// 它只认识 Holder 和 Loader，不认识 HTTP、缓存、数据库。
// 因此“热重载会不会清缓存”这个问题在类型层面就不成立了：
// Reloader 根本没有缓存可以碰。
type Reloader struct {
	holder *Holder
	loader Loader
	logger *slog.Logger
	now    func() time.Time
	// lastErr 记录上一次已经打过的错误文案。
	// 配置持续不可读时每轮都会失败，去重后只记一条，避免日志刷屏。
	lastErr string
}

// NewReloader 创建重载器。logger 可为 nil。
func NewReloader(holder *Holder, loader Loader, logger *slog.Logger) *Reloader {
	if logger == nil {
		logger = slog.New(slog.NewTextHandler(io.Discard, nil))
	}
	return &Reloader{
		holder: holder,
		loader: loader,
		logger: logger,
		now:    time.Now,
	}
}

// WithClock 注入时钟，便于测试。
func (r *Reloader) WithClock(f func() time.Time) *Reloader {
	if f != nil {
		r.now = f
	}
	return r
}

// Once 执行一次重载，幂等。
//
// 顺序经过刻意设计：
//  1. 读原始字节——失败则保留旧快照；
//  2. 先算哈希再解析——内容没变就完全不解析，省掉整次 TOML 解码与校验；
//  3. 解析 + 校验得到完整新对象——失败则保留旧快照，旧配置继续服务；
//  4. 原子换指针——读者要么看到旧快照，要么看到新快照，没有中间态。
func (r *Reloader) Once() (Outcome, error) {
	raw, err := r.loader.Read()
	if err != nil {
		return Outcome{}, err
	}
	hash := Fingerprint(raw)

	if cur := r.holder.Current(); cur != nil && cur.Hash == hash {
		return Outcome{Skipped: true, Version: cur.Version}, nil
	}

	dyn, err := Parse(raw)
	if err != nil {
		return Outcome{}, err
	}

	_, created := r.holder.Swap(dyn, hash, r.now())
	r.logger.Info("配置已热更新", "version", created.Version, "hash", fmt.Sprintf("%x", hash[:4]))
	return Outcome{Changed: true, Version: created.Version}, nil
}

// Run 周期性触发重载，直到 ctx 结束。
//
// 采用「轮询 + 内容哈希」而不是文件事件：
//   - 不依赖 inotify，跨文件系统一致；
//   - 编辑器的改名保存（rename）、原子写都不会漏事件；
//   - 天然去抖——同一份内容不会因为多次写而被重放。
//
// 如果需要文件事件来降低延迟，把 Reloader.Once 接到任意事件源即可，
// 本类型不需要改动。
func (r *Reloader) Run(ctx context.Context, interval time.Duration) error {
	if interval <= 0 {
		interval = time.Second
	}
	if _, err := r.Once(); err != nil {
		r.logError(err)
	}

	tick := time.NewTicker(interval)
	defer tick.Stop()
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-tick.C:
			if _, err := r.Once(); err != nil {
				r.logError(err)
			}
		}
	}
}

// logError 记录热更错误，同一文案只记一次。
//
// 配置文件被删掉时，轮询每 N 秒就会失败一次；如果不去重，
// 终端会被同一条错误刷满屏（这在 Termux 里尤其难用）。
func (r *Reloader) logError(err error) {
	msg := err.Error()
	if msg == r.lastErr {
		return
	}
	r.lastErr = msg
	r.logger.Error("配置加载失败，保留当前配置", "err", err)
}

// ResetErrorState 在加载成功后清空去重状态，让下次故障还能被记录。
func (r *Reloader) ResetErrorState() {
	r.lastErr = ""
}
