// Package source 定义音乐来源的契约。
//
// 这一层只描述「一个平台能做什么」，不涉及 HTTP、路由、鉴权。
// 真实平台实现与 mock 实现都满足同一个接口，
// 因此上层链路可以在没有真实上游的情况下完整跑通。
package source

import "context"

// LinkInfo 是播放/视频直链及其元信息。
type LinkInfo struct {
	// Qual 实际音质，可能因回落策略与请求不同。
	Qual string `json:"qual"`
	// Expi 过期时间，Unix 秒。
	Expi int64 `json:"expi"`
	// Link 直链，视频直链也放这里。
	Link string `json:"link,omitempty"`
}

// LyricInfo 是歌词。
type LyricInfo struct {
	Lyric   string `json:"lyric"`
	TLyric  string `json:"tlyric,omitempty"`
	RLyric  string `json:"rlyric,omitempty"`
	LxLyric string `json:"lxlyric,omitempty"`
}

// ArtistInfo 是歌手。
type ArtistInfo struct {
	ID   string   `json:"id"`
	Name string   `json:"name"`
	Alia []string `json:"alia,omitempty"`
}

// AlbumInfo 是专辑。
type AlbumInfo struct {
	ID   string   `json:"id"`
	Name string   `json:"name"`
	Alia []string `json:"alia,omitempty"`
	Covr string   `json:"covr,omitempty"`
}

// FileInfo 是文件规格。
type FileInfo struct {
	Qual string `json:"qual"`
	Size string `json:"size,omitempty"`
	Sizs string `json:"sizs,omitempty"`
	Hash string `json:"hash,omitempty"`
}

// MusicInfo 是音乐信息。
type MusicInfo struct {
	ID   string       `json:"id"`
	Name string       `json:"name"`
	Alia []string     `json:"alia,omitempty"`
	Ar   []ArtistInfo `json:"ar,omitempty"`
	Al   AlbumInfo    `json:"al"`
	Dt   uint32       `json:"dt"`
	Mv   string       `json:"mv,omitempty"`
	Fee  uint32       `json:"fee"`
	File []FileInfo   `json:"file,omitempty"`
}

// MovieInfo 是视频信息。
type MovieInfo struct {
	ID   string     `json:"id"`
	Name string     `json:"name"`
	Desc string     `json:"desc,omitempty"`
	Covr string     `json:"covr,omitempty"`
	File []FileInfo `json:"file,omitempty"`
}

// Source 是单个来源的能力契约。
//
// 所有方法都接收 context，以便上游请求可被取消；
// 返回的错误应当是 apierr 的 *Error，这样 HTTP 层能直接映射状态码。
type Source interface {
	// Src 返回平台二字码。
	Src() string
	// URL 返回播放直链。
	URL(ctx context.Context, id, quality string) (LinkInfo, error)
	// Lyric 返回歌词。
	Lyric(ctx context.Context, id string) (LyricInfo, error)
	// Movie 返回视频直链。
	Movie(ctx context.Context, id, quality string) (LinkInfo, error)
	// Music 返回音乐信息。
	Music(ctx context.Context, id string) (MusicInfo, error)
	// Video 返回视频信息。
	Video(ctx context.Context, id string) (MovieInfo, error)
	// Pic 返回封面地址。
	Pic(ctx context.Context, id, size string) (string, error)
}

// Registry 是来源注册表。用 map 而非全局变量，便于测试与多实例。
type Registry map[string]Source

// Get 按二字码查找来源。
func (r Registry) Get(code string) (Source, bool) {
	s, ok := r[code]
	return s, ok
}

// Codes 返回已注册的来源码。
func (r Registry) Codes() []string {
	out := make([]string, 0, len(r))
	for k := range r {
		out = append(out, k)
	}
	return out
}

// Len 返回来源数量。
func (r Registry) Len() int { return len(r) }
