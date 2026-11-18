package cache

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"time"
)

// ErrInvalidKey 表示键无法安全映射为文件路径。
var ErrInvalidKey = errors.New("cache: 非法键")

// File 是本地文件缓存。
//
// 布局：<root>/<key> 存原始字节，<root>/<key>.meta 存过期时间。
// 数据文件保持字节原样，便于后续用 http.ServeContent 直接提供 Range 下载；
// 元信息放在旁路文件里，不去动数据文件的 mtime。
type File struct {
	root string
	now  func() time.Time
	seq  atomic.Uint64
}

type fileMeta struct {
	// ExpiresAt 为零值表示不过期。
	ExpiresAt time.Time `json:"expiresAt,omitempty"`
}

// NewFile 创建本地文件缓存，必要时创建根目录。
func NewFile(root string) (*File, error) {
	if strings.TrimSpace(root) == "" {
		return nil, errors.New("cache: 缓存根目录为空")
	}
	abs, err := filepath.Abs(root)
	if err != nil {
		return nil, fmt.Errorf("cache: 解析缓存目录: %w", err)
	}
	if err := os.MkdirAll(abs, 0o755); err != nil {
		return nil, fmt.Errorf("cache: 创建缓存目录: %w", err)
	}
	return &File{root: abs, now: time.Now}, nil
}

// WithClock 注入时钟，便于测试。
func (f *File) WithClock(c func() time.Time) *File {
	if c != nil {
		f.now = c
	}
	return f
}

// Root 返回缓存根目录。
func (f *File) Root() string { return f.root }

// Path 返回键对应的数据文件路径。
//
// 该能力属于本地驱动的特有能力（用于直接提供文件下载），
// 不属于 Cache 接口，取用方需要显式断言。
func (f *File) Path(key string) (string, error) {
	rel, err := f.rel(key)
	if err != nil {
		return "", err
	}
	return filepath.Join(f.root, rel), nil
}

// rel 把键安全地映射为相对路径。
//
// 键的形态是 {source}/{musicid}/{quality}.{ext}，
// 这里逐段白名单校验，拒绝空段、点段和路径分隔符，杜绝目录穿越。
func (f *File) rel(key string) (string, error) {
	if key == "" || len(key) > 512 {
		return "", ErrInvalidKey
	}
	parts := strings.Split(key, "/")
	if len(parts) == 0 {
		return "", ErrInvalidKey
	}
	for _, p := range parts {
		if p == "" || p == "." || p == ".." {
			return "", fmt.Errorf("%w: 空段或点段", ErrInvalidKey)
		}
		if len(p) > 255 {
			return "", fmt.Errorf("%w: 段过长", ErrInvalidKey)
		}
		if strings.HasPrefix(p, ".") {
			return "", fmt.Errorf("%w: 段以点开头", ErrInvalidKey)
		}
		for i := 0; i < len(p); i++ {
			c := p[i]
			ok := c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' ||
				c >= '0' && c <= '9' || c == '.' || c == '-' || c == '_'
			if !ok {
				return "", fmt.Errorf("%w: 含非法字符 %q", ErrInvalidKey, c)
			}
		}
	}
	return filepath.Join(parts...), nil
}

// Get 实现 Cache。
func (f *File) Get(_ context.Context, key string) ([]byte, bool, error) {
	dataPath, err := f.Path(key)
	if err != nil {
		return nil, false, err
	}

	meta, err := f.readMeta(dataPath)
	if err != nil {
		return nil, false, err
	}
	if meta != nil && !meta.ExpiresAt.IsZero() && f.now().After(meta.ExpiresAt) {
		// 过期即视为未命中，顺手清理，避免磁盘无限增长
		_ = f.remove(dataPath)
		return nil, false, nil
	}

	data, err := os.ReadFile(dataPath)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, false, nil
		}
		return nil, false, fmt.Errorf("cache: 读取 %s: %w", dataPath, err)
	}
	return data, true, nil
}

// Set 实现 Cache。
func (f *File) Set(_ context.Context, key string, data []byte, ttl time.Duration) error {
	dataPath, err := f.Path(key)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(dataPath), 0o755); err != nil {
		return fmt.Errorf("cache: 创建目录: %w", err)
	}

	if err := f.writeAtomic(dataPath, data, 0o644); err != nil {
		return err
	}

	if ttl > 0 {
		m := fileMeta{ExpiresAt: f.now().Add(ttl)}
		b, err := json.Marshal(m)
		if err != nil {
			return fmt.Errorf("cache: 序列化元信息: %w", err)
		}
		if err := f.writeAtomic(dataPath+metaSuffix, b, 0o644); err != nil {
			return err
		}
	} else {
		// 无 TTL 时清掉可能残留的旧元信息，否则会被旧过期时间误杀
		_ = os.Remove(dataPath + metaSuffix)
	}
	return nil
}

// Del 实现 Cache。同时删除数据与元信息。
func (f *File) Del(_ context.Context, key string) error {
	dataPath, err := f.Path(key)
	if err != nil {
		return err
	}
	return f.remove(dataPath)
}

const metaSuffix = ".meta"

func (f *File) remove(dataPath string) error {
	var firstErr error
	if err := os.Remove(dataPath); err != nil && !os.IsNotExist(err) {
		firstErr = err
	}
	if err := os.Remove(dataPath + metaSuffix); err != nil && !os.IsNotExist(err) && firstErr == nil {
		firstErr = err
	}
	return firstErr
}

func (f *File) readMeta(dataPath string) (*fileMeta, error) {
	b, err := os.ReadFile(dataPath + metaSuffix)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("cache: 读取元信息: %w", err)
	}
	var m fileMeta
	if err := json.Unmarshal(b, &m); err != nil {
		// 元信息损坏不应连累数据读取，按「无过期」处理
		return nil, nil
	}
	return &m, nil
}

// writeAtomic 先写临时文件再改名，避免读到半截内容。
func (f *File) writeAtomic(path string, data []byte, perm os.FileMode) error {
	dir := filepath.Dir(path)
	tmp := filepath.Join(dir, fmt.Sprintf(".tmp-%d-%d", os.Getpid(), f.seq.Add(1)))
	fh, err := os.OpenFile(tmp, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, perm)
	if err != nil {
		return fmt.Errorf("cache: 创建临时文件: %w", err)
	}
	if _, err := fh.Write(data); err != nil {
		fh.Close()
		os.Remove(tmp)
		return fmt.Errorf("cache: 写入临时文件: %w", err)
	}
	if err := fh.Sync(); err != nil {
		fh.Close()
		os.Remove(tmp)
		return fmt.Errorf("cache: 落盘: %w", err)
	}
	if err := fh.Close(); err != nil {
		os.Remove(tmp)
		return fmt.Errorf("cache: 关闭临时文件: %w", err)
	}
	if err := os.Rename(tmp, path); err != nil {
		os.Remove(tmp)
		return fmt.Errorf("cache: 改名: %w", err)
	}
	return nil
}
