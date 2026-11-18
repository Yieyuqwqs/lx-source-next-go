package quality

import (
	"reflect"
	"testing"
)

func TestCanonical(t *testing.T) {
	cases := map[string]string{
		"128k":   Q128,
		"128":    Q128,
		"320K":   Q320,
		" flac ": QFlac,
		"fl24":   QFlac24,
		"hires":  QFlac24,
		"FLAC24": QFlac24,
		"atmos":  QDolby,
		"sky":    QSky,
		"master": QMaster,
		"temp":   "temp",
		"orig":   "orig",
	}
	for in, want := range cases {
		got, ok := Canonical(in)
		if !ok || got != want {
			t.Errorf("Canonical(%q) = %q,%v want %q", in, got, ok, want)
		}
	}
	for _, bad := range []string{"", "  ", "abc", "999k"} {
		if _, ok := Canonical(bad); ok {
			t.Errorf("Canonical(%q) 应失败", bad)
		}
	}
}

func TestRankOrder(t *testing.T) {
	prev := 0
	for _, q := range List() {
		r, ok := Rank(q)
		if !ok {
			t.Fatalf("Rank(%q) 失败", q)
		}
		if r <= prev {
			t.Errorf("等级未递增: %q rank=%d", q, r)
		}
		prev = r
	}
	if _, ok := Rank("temp"); ok {
		t.Error("temp 不应参与高低比较")
	}
}

func TestChain(t *testing.T) {
	got := Chain("flac24bit")
	want := []string{QFlac24, QFlac, Q320, Q192, Q128}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("Chain = %v, want %v", got, want)
	}
	if got := Chain("temp"); !reflect.DeepEqual(got, []string{"temp"}) {
		t.Errorf("特殊音质链 = %v", got)
	}
	if Chain("abc") != nil {
		t.Error("未知音质应返回 nil")
	}
}

func TestResolve(t *testing.T) {
	// 请求无损，可用项里有更高音质也不能越级
	if got, ok := Resolve("flac", []string{"128k", "flac", "flac24bit"}); !ok || got != QFlac {
		t.Errorf("Resolve = %q,%v want flac", got, ok)
	}
	// 无损拿不到，回落到 320k
	if got, ok := Resolve("flac", []string{"128k", "320k"}); !ok || got != Q320 {
		t.Errorf("回落结果 = %q,%v want 320k", got, ok)
	}
	// 别名应被识别
	if got, ok := Resolve("hires", []string{"fl24", "320k"}); !ok || got != QFlac24 {
		t.Errorf("别名解析 = %q,%v", got, ok)
	}
	// 完全无法满足
	if _, ok := Resolve("master", []string{"abc"}); ok {
		t.Error("全不可用时应失败")
	}
	// 特殊音质只认同名
	if got, ok := Resolve("temp", []string{"128k", "temp"}); !ok || got != "temp" {
		t.Errorf("特殊音质 = %q,%v", got, ok)
	}
	if _, ok := Resolve("temp", []string{"128k"}); ok {
		t.Error("特殊音质不应回落")
	}
}
