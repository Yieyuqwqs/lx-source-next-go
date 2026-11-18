// Package mock 提供一个确定性的假来源。
//
// 它存在的唯一目的：在没有真实上游的情况下，让
// 路由 → 鉴权 → 限流 → 缓存 → Source → 响应
// 这条完整链路可以被真实地跑通与测试。
//
// 接真实平台时，它应当从注册表里移除。
package mock

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/lxsrcs/lx-source-next/pkg/apierr"
	"github.com/lxsrcs/lx-source-next/pkg/lrcutil"
	"github.com/lxsrcs/lx-source-next/pkg/quality"
	"github.com/lxsrcs/lx-source-next/pkg/source"
)

// NotFoundID 是会让 mock 返回 404 的特殊 ID，便于验证错误链路。
const NotFoundID = "notfound"

// Source 是假来源。
type Source struct {
	// Code 平台二字码，默认 mock。
	Code string
	// Quals 声称支持的音质，用于演示回落。
	Quals []string
	now   func() time.Time
}

// New 创建假来源。
func New() *Source {
	return &Source{
		Code:  "mock",
		Quals: []string{"128k", "320k", "flac"},
		now:   time.Now,
	}
}

// Src 实现 source.Source。
func (s *Source) Src() string { return s.Code }

// checkID 统一校验 ID，并支持用特殊 ID 触发 404。
func checkID(id string) error {
	switch {
	case id == "":
		return apierr.New(apierr.CodeBadRequest, "缺少音乐 ID")
	case strings.EqualFold(id, NotFoundID):
		return apierr.Newf(apierr.CodeNotFound, "mock 中不存在 ID %q", id)
	}
	return nil
}

// URL 实现 source.Source。
//
// 用 quality.Resolve 演示真实的回落语义：请求 master 会落到 flac。
func (s *Source) URL(_ context.Context, id, q string) (source.LinkInfo, error) {
	if err := checkID(id); err != nil {
		return source.LinkInfo{}, err
	}
	actual, ok := quality.Resolve(q, s.Quals)
	if !ok {
		actual = "128k"
	}
	return source.LinkInfo{
		Qual: actual,
		Expi: s.now().Add(time.Hour).Unix(),
		Link: fmt.Sprintf("https://mock.invalid/audio/%s/%s.mp3", id, actual),
	}, nil
}

// Lyric 实现 source.Source。
//
// 这里刻意走 lrcutil 生成，顺带验证歌词层的真实产出。
func (s *Source) Lyric(_ context.Context, id string) (source.LyricInfo, error) {
	if err := checkID(id); err != nil {
		return source.LyricInfo{}, err
	}
	kara := &lrcutil.Kara{
		Meta: map[string]string{"ti": "Mock Song " + id, "ar": "Mock Artist"},
		Lines: []lrcutil.KLine{
			{Start: 0, Dur: 1000, Words: []lrcutil.Word{{Start: 0, Dur: 400, Text: "第一"}, {Start: 400, Dur: 600, Text: "句"}}},
			{Start: 1000, Dur: 1000, Words: []lrcutil.Word{{Start: 0, Dur: 500, Text: "第二"}, {Start: 500, Dur: 500, Text: "句"}}},
		},
	}
	return source.LyricInfo{
		Lyric:   kara.ToLRC(),
		TLyric:  "[00:00.00] first line\n[00:01.00] second line\n",
		LxLyric: kara.ToLrcx(),
	}, nil
}

// Movie 实现 source.Source。
func (s *Source) Movie(_ context.Context, id, q string) (source.LinkInfo, error) {
	if err := checkID(id); err != nil {
		return source.LinkInfo{}, err
	}
	qual := q
	if qual == "" {
		qual = "720p"
	}
	return source.LinkInfo{
		Qual: qual,
		Expi: s.now().Add(time.Hour).Unix(),
		Link: fmt.Sprintf("https://mock.invalid/video/%s/%s.mp4", id, qual),
	}, nil
}

// Music 实现 source.Source。
func (s *Source) Music(_ context.Context, id string) (source.MusicInfo, error) {
	if err := checkID(id); err != nil {
		return source.MusicInfo{}, err
	}
	return source.MusicInfo{
		ID:   id,
		Name: "Mock Song " + id,
		Ar:   []source.ArtistInfo{{ID: "1", Name: "Mock Artist"}},
		Al:   source.AlbumInfo{ID: "2", Name: "Mock Album", Covr: "https://mock.invalid/cover/" + id + ".jpg"},
		Dt:   248,
		Mv:   "90001",
		Fee:  0,
		File: []source.FileInfo{
			{Qual: "128k", Size: "3970000"},
			{Qual: "320k", Size: "9920000"},
			{Qual: "flac", Size: "28300000"},
		},
	}, nil
}

// Video 实现 source.Source。
func (s *Source) Video(_ context.Context, id string) (source.MovieInfo, error) {
	if err := checkID(id); err != nil {
		return source.MovieInfo{}, err
	}
	return source.MovieInfo{
		ID:   id,
		Name: "Mock MV " + id,
		Desc: "由 mock 来源生成的占位视频",
		Covr: "https://mock.invalid/mv/" + id + ".jpg",
		File: []source.FileInfo{{Qual: "720p", Size: "10485760"}, {Qual: "1080p", Size: "20971520"}},
	}, nil
}

// Pic 实现 source.Source。
func (s *Source) Pic(_ context.Context, id, size string) (string, error) {
	if err := checkID(id); err != nil {
		return "", err
	}
	if size == "" {
		size = "m"
	}
	return fmt.Sprintf("https://mock.invalid/pic/%s-%s.jpg", id, size), nil
}
