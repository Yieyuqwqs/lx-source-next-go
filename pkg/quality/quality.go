// Package quality 统一音质命名与回落策略。
//
// 原先各源各写一套映射（wy.ConvertUrl / kw.ParseQual / kg.ConvertDetail …），
// 别名还对不上。这里把「别名 → 规范名」和「降级顺序」收敛成一张表。
package quality

import "strings"

// 规范音质名，从低到高。
const (
	Q128    = "128k"
	Q192    = "192k"
	Q320    = "320k"
	QFlac   = "flac"
	QFlac24 = "flac24bit"
	QDolby  = "dolby"
	QSky    = "sky"
	QMaster = "master"
)

// order 是规范名 → 等级。等级越大音质越好。
var order = map[string]int{
	Q128:    1,
	Q192:    2,
	Q320:    3,
	QFlac:   4,
	QFlac24: 5,
	QDolby:  6,
	QSky:    7,
	QMaster: 8,
}

// alias 是历史别名/平台写法 → 规范名。
var alias = map[string]string{
	"128":      Q128,
	"192":      Q192,
	"320":      Q320,
	"flac":     QFlac,
	"lossless": QFlac,
	"fl24":     QFlac24,
	"hires":    QFlac24,
	"flac24":   QFlac24,
	"atmos":    QDolby,
	"dolby":    QDolby,
	"sky":      QSky,
	"master":   QMaster,
	"sq":       QFlac,
	"hq":       Q320,
}

// nonFallback 是不参与高低比较的特殊音质，遇到时不做回落。
var nonFallback = map[string]struct{}{
	"temp": {}, // 试听
	"orig": {}, // 电台 / 云盘原声
}

// Canonical 把任意写法归一化成规范名。
func Canonical(s string) (string, bool) {
	k := strings.ToLower(strings.TrimSpace(s))
	if k == "" {
		return "", false
	}
	if _, ok := nonFallback[k]; ok {
		return k, true
	}
	if _, ok := order[k]; ok {
		return k, true
	}
	if v, ok := alias[k]; ok {
		return v, true
	}
	return "", false
}

// Rank 返回音质等级；特殊音质与未知值返回 false。
func Rank(s string) (int, bool) {
	c, ok := Canonical(s)
	if !ok {
		return 0, false
	}
	r, ok := order[c]
	return r, ok
}

// IsFallback 判断该音质是否参与高低回落。
func IsFallback(s string) bool {
	_, ok := Rank(s)
	return ok
}

// Chain 返回从请求音质逐级降到底的候选序列。
// 特殊音质（temp / orig）返回只含自身的单元素链。
func Chain(req string) []string {
	c, ok := Canonical(req)
	if !ok {
		return nil
	}
	if _, special := nonFallback[c]; special {
		return []string{c}
	}
	top, ok := order[c]
	if !ok {
		return nil
	}
	chain := make([]string, 0, len(order))
	for name, r := range order {
		if r <= top {
			chain = append(chain, name)
		}
	}
	// 按等级从高到低排序（插入排序，规模极小）
	for i := 1; i < len(chain); i++ {
		for j := i; j > 0 && order[chain[j]] > order[chain[j-1]]; j-- {
			chain[j], chain[j-1] = chain[j-1], chain[j]
		}
	}
	return chain
}

// Resolve 在不高于请求音质的可用项里挑最好的一个。
//
// available 里可以是别名写法，返回值统一是规范名。
func Resolve(req string, available []string) (string, bool) {
	topReq, ok := Canonical(req)
	if !ok {
		return "", false
	}
	top, isFallback := order[topReq]
	if !isFallback {
		// 特殊音质：只有完全同名才算命中
		for _, a := range available {
			if c, ok := Canonical(a); ok && c == topReq {
				return c, true
			}
		}
		return "", false
	}
	best := ""
	bestRank := 0
	for _, a := range available {
		c, ok := Canonical(a)
		if !ok {
			continue
		}
		r, ok := order[c]
		if !ok || r > top {
			continue
		}
		if r > bestRank {
			best, bestRank = c, r
		}
	}
	if best == "" {
		return "", false
	}
	return best, true
}

// List 返回全部规范音质名，从低到高。
func List() []string {
	return []string{Q128, Q192, Q320, QFlac, QFlac24, QDolby, QSky, QMaster}
}
