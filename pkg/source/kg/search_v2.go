package kg

// 本文件是酷狗新版搜索（song_search_v2）的解析与 hash 记录。
//
// 旧接口 mobilecdn/api/v3/search/song 只给 128/320/无损三个 hash，
// 新版还会给 ResFileHash（24bit 母带），这正是「能拿 flac24bit」的关键：
// 每档音质对应一个独立 hash，取链时要按档位选对 hash。

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/lxsrcs/lx-source-next/pkg/quality"
	"github.com/lxsrcs/lx-source-next/pkg/source"
)

// defaultSearchV2Base 是新版搜索地址。
const defaultSearchV2Base = "https://songsearch.kugou.com"

// v2List 是 song_search_v2 的单条结果。
type v2List struct {
	SongName     string `json:"SongName"`
	SingerName   string `json:"SingerName"`
	AlbumName    string `json:"AlbumName"`
	Duration     int    `json:"Duration"`
	FileHash     string `json:"FileHash"`
	HQFileHash   string `json:"HQFileHash"`
	SQFileHash   string `json:"SQFileHash"`
	ResFileHash  string `json:"ResFileHash"`
	SuperFileHash string `json:"SuperFileHash"`
	Privilege    int    `json:"Privilege"`
	PayType      int    `json:"PayType"`
}

// cleanName 去掉搜索结果里的高亮标签。
func cleanName(s string) string {
	s = strings.ReplaceAll(s, "<em>", "")
	s = strings.ReplaceAll(s, "</em>", "")
	return strings.TrimSpace(s)
}

// validHash 过滤空 hash 与占位 "0"。
func validHash(h string) bool {
	h = strings.TrimSpace(h)
	return h != "" && h != "0"
}

// ParseSearchV2 解析新版搜索响应，并返回「档位 → hash」表（供调用方记录）。
func ParseSearchV2(body []byte) (source.SearchResult, map[string]map[string]string, error) {
	var resp struct {
		Status int `json:"status"`
		Data   struct {
			Total int      `json:"total"`
			Lists []v2List `json:"lists"`
		} `json:"data"`
	}
	if err := json.Unmarshal(body, &resp); err != nil {
		return source.SearchResult{}, nil, fmt.Errorf("kg: 搜索响应不是 JSON: %w", err)
	}

	out := source.SearchResult{Total: resp.Data.Total}
	tables := make(map[string]map[string]string, len(resp.Data.Lists))
	for _, it := range resp.Data.Lists {
		name := cleanName(it.SongName)
		if !validHash(it.FileHash) || name == "" {
			continue
		}
		item := source.MusicInfo{
			ID:   it.FileHash,
			Name: name,
			Al:   source.AlbumInfo{Name: it.AlbumName},
			Dt:   uint32(it.Duration),
		}
		if it.SingerName != "" {
			item.Ar = []source.ArtistInfo{{Name: it.SingerName}}
		}

		table := map[string]string{
			"128": it.FileHash,
		}
		if validHash(it.HQFileHash) {
			table["320"] = it.HQFileHash
		}
		if validHash(it.SQFileHash) {
			table["flac"] = it.SQFileHash
		}
		// ResFileHash 是 24bit 母带；没有时退回 SuperFileHash
		switch {
		case validHash(it.ResFileHash):
			table["high"] = it.ResFileHash
		case validHash(it.SuperFileHash):
			table["high"] = it.SuperFileHash
		}

		for _, q := range []struct{ qual string; key string }{
			{quality.Q128, "128"}, {quality.Q320, "320"},
			{quality.QFlac, "flac"}, {quality.QFlac24, "high"},
		} {
			if h, ok := table[q.key]; ok {
				item.File = append(item.File, source.FileInfo{Qual: q.qual, Hash: h})
			}
		}

		out.Items = append(out.Items, item)
		// 每个已知 hash 都指向同一张表，取链时用任意一个都能查到别的档位
		for _, h := range table {
			tables[h] = table
		}
	}
	if out.Total == 0 {
		out.Total = len(out.Items)
	}
	return out, tables, nil
}
