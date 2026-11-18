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

// reverseBR 是 br → 规范音质。
var reverseBR = map[string]string{
	br128:    quality.Q128,
	br192:    quality.Q192,
	br320:    quality.Q320,
	brFlac:   quality.QFlac,
	brFlac24: quality.QFlac24,
}

// BitrateFor 把任意音质写法映射成酷我的 br 参数，同时返回规范音质名。
func BitrateFor(q string) (br, canonical string, ok bool) {
	c, ok := quality.Canonical(q)
	if !ok {
		return "", "", false
	}
	br, ok = brTable[c]
	if !ok {
		return "", "", false
	}
	return br, c, true
}

// QualityFromBitrate 把上游返回的 br 反解成规范音质名。
func QualityFromBitrate(br string) (string, bool) {
	c, ok := reverseBR[br]
	return c, ok
}

// SupportedQualities 返回酷我支持的规范音质，按从低到高排列。
func SupportedQualities() []string {
	return []string{quality.Q128, quality.Q192, quality.Q320, quality.QFlac, quality.QFlac24}
}

// ResolveQuality 把请求音质回落到酷我真正支持的档位。
func ResolveQuality(req string) (br, canonical string, ok bool) {
	c, ok := quality.Resolve(req, SupportedQualities())
	if !ok {
		return "", "", false
	}
	br, ok = brTable[c]
	return br, c, ok
}
