package kw

import "github.com/lxsrcs/lx-source-next/pkg/quality"

// 酷我 br 参数的取值。前四个来自长期稳定的旧接口，后两个待抓包确认。
const (
	br128    = "128kmp3"
	br192    = "192kmp3"
	br320    = "320kmp3"
	brFlac   = "2000kflac"
	brFlac24 = "4000kflac"
)

// brTable 是规范音质 → 酷我 br 参数。
//
// 表里没有的音质表示酷我不提供，上层会走 quality.Resolve 回落。
var brTable = map[string]string{
	quality.Q128:    br128,
	quality.Q192:    br192,
	quality.Q320:    br320,
	quality.QFlac:   brFlac,
	quality.QFlac24: brFlac24,
}

// SupportedQualities 返回酷我支持的规范音质，按从低到高排列。
func SupportedQualities() []string {
	return []string{quality.Q128, quality.Q192, quality.Q320, quality.QFlac, quality.QFlac24}
}
