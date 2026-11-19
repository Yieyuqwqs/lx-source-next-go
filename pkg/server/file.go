package server

import (
	"errors"
	"fmt"
	"mime"
	"net/http"
	"os"
	"path"
	"strconv"
	"strings"
	"time"

	"github.com/lxsrcs/lx-source-next/pkg/apierr"
	cachepolicy "github.com/lxsrcs/lx-source-next/pkg/cache/policy"
	"github.com/lxsrcs/lx-source-next/pkg/httpx"
)

// FileStore 是「能把缓存键解析成磁盘路径」的驱动能力。
//
// 只有本地文件缓存具备这一能力，因此它不属于 cache.Cache 接口，
// 而是组合根按需注入的窄接口。换成 Redis 驱动时把它留空即可，
// 文件路由会自动降级为 503。
type FileStore interface {
	Path(key string) (string, error)
}

// errBadPath 表示请求路径不合规。
var errBadPath = errors.New("非法的文件路径")

// 文件类型。键的形态是 {source}/{musicid}/{quality}.{ext}。
var contentTypes = map[string]string{
	".mp3":  "audio/mpeg",
	".flac": "audio/flac",
	".m4a":  "audio/mp4",
	".aac":  "audio/aac",
	".ogg":  "audio/ogg",
	".opus": "audio/ogg",
	".wav":  "audio/wav",
	".mp4":  "video/mp4",
	".mkv":  "video/x-matroska",
	".jpg":  "image/jpeg",
	".jpeg": "image/jpeg",
	".png":  "image/png",
	".webp": "image/webp",
	".lrc":  "text/plain; charset=utf-8",
	".txt":  "text/plain; charset=utf-8",
}

// buildFileKey 由 {source}/{musicid}/{name} 三段拼出缓存键，并逐段校验。
//
// 强制三段：这个函数只服务于该路由的形状，
// 少一段或多一段都属于异常输入，直接拒绝，不要拼出畸形键。
//
// 这里是路径穿越的第一道闸：即使 ServeMux 已经把路径规范化，
// 编码过的 %2e%2e、%00、反斜杠等仍会原样落到 PathValue 里，
// 所以必须自己再验一遍字符集。第二道闸在缓存驱动内部（cache.File.rel）。
func buildFileKey(parts ...string) (string, error) {
	if len(parts) != 3 {
		return "", fmt.Errorf("%w: 需要 3 段，实际 %d 段", errBadPath, len(parts))
	}
	for _, p := range parts {
		if !validSegment(p) {
			return "", fmt.Errorf("%w: 段 %q 非法", errBadPath, p)
		}
	}
	return strings.Join(parts, "/"), nil
}

// validSegment 校验单段：非空、长度受限、字符集白名单、拒绝点段与隐藏段。
func validSegment(p string) bool {
	if p == "" || len(p) > 255 {
		return false
	}
	if p == "." || p == ".." || strings.HasPrefix(p, ".") {
		return false
	}
	for i := 0; i < len(p); i++ {
		c := p[i]
		ok := c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' ||
			c >= '0' && c <= '9' || c == '.' || c == '-' || c == '_'
		if !ok {
			return false
		}
	}
	return true
}

// contentType 按扩展名推断类型；推断不出时退化为二进制流。
func contentType(name string) string {
	ext := strings.ToLower(path.Ext(name))
	if ct, ok := contentTypes[ext]; ok {
		return ct
	}
	if ct := mime.TypeByExtension(ext); ct != "" {
		return ct
	}
	return "application/octet-stream"
}

// etag 由文件大小与修改时间构造强校验器。
//
// 内容一旦变化，大小或 mtime 必变，ETag 随之变化；
// http.ServeContent 会拿它处理 If-None-Match / If-Range。
func etag(size int64, mod time.Time) string {
	return `"` + strconv.FormatInt(size, 16) + "-" + strconv.FormatInt(mod.UnixNano(), 16) + `"`
}

// handleFile 提供缓存文件下载，走标准 HTTP 语义。
//
// 取代了原版自签临时链（Sign / Acip / Dyna / Expi）：
// 有效期交给 Cache-Control，校验交给 ETag + If-None-Match，
// 断点续传交给 Range —— 全部由 http.ServeContent 实现。
func (a *App) handleFile(w http.ResponseWriter, r *http.Request) {
	if a.deps.Files == nil {
		httpx.Fail(w, apierr.New(apierr.CodeUnavailable, "当前没有可用的文件缓存驱动"))
		return
	}

	sourceCode := r.PathValue("source")
	// 以 _ 开头的是内部缓存键（如 _meta/...），不属于对外文件
	if strings.HasPrefix(sourceCode, "_") {
		httpx.Fail(w, apierr.New(apierr.CodeNotFound, "缓存中不存在该文件"))
		return
	}
	key, err := buildFileKey(sourceCode, r.PathValue("musicid"), r.PathValue("name"))
	if err != nil {
		httpx.Fail(w, apierr.New(apierr.CodeBadRequest, "文件路径非法"))
		return
	}

	p, err := a.deps.Files.Path(key)
	if err != nil {
		httpx.Fail(w, apierr.New(apierr.CodeBadRequest, "文件路径非法"))
		return
	}

	fh, err := os.Open(p)
	if err != nil {
		if os.IsNotExist(err) {
			httpx.Fail(w, apierr.New(apierr.CodeNotFound, "缓存中不存在该文件"))
			return
		}
		a.deps.Logger.Error("打开缓存文件失败", "key", key, "err", err)
		httpx.Fail(w, apierr.Wrap(apierr.CodeInternal, "读取缓存文件失败", err))
		return
	}
	defer fh.Close()

	st, err := fh.Stat()
	if err != nil || st.IsDir() {
		httpx.Fail(w, apierr.New(apierr.CodeNotFound, "缓存中不存在该文件"))
		return
	}

	h := w.Header()
	h.Set("Content-Type", contentType(r.PathValue("name")))
	h.Set("ETag", etag(st.Size(), st.ModTime()))
	h.Set("X-Content-Type-Options", "nosniff")
	// 有效期与缓存策略层保持一致：配置为 0 时用策略的默认 TTL
	ttl := a.dynamic().Data.TTL
	if ttl <= 0 {
		ttl = int(cachepolicy.DefaultTTL / time.Second)
	}
	h.Set("Cache-Control", "public, max-age="+strconv.Itoa(ttl))

	// ServeContent 负责 Range / If-Range / If-None-Match / If-Modified-Since，
	// 并在这些条件下返回 206 / 304。
	http.ServeContent(w, r, r.PathValue("name"), st.ModTime(), fh)
}
