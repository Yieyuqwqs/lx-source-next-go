package server

import "testing"

// TestCacheKeySegment 合法 id 原样保留，非法 id 走 md5。
func TestCacheKeySegment(t *testing.T) {
	legit := []string{"347230", "a1b2-c3_d4.e5", "000027oMO61wWi55"}
	for _, s := range legit {
		if got := cacheKeySegment(s); got != s {
			t.Errorf("cacheKeySegment(%q) = %q，合法值不该变形", s, got)
		}
	}
	bad := []string{"", "a/b", "with space", "中文", "x:y", "a?b=c"}
	for _, s := range bad {
		got := cacheKeySegment(s)
		if got == s {
			t.Errorf("cacheKeySegment(%q) 应该被转义", s)
		}
		if len(got) != 32 {
			t.Errorf("cacheKeySegment(%q) = %q，md5 应为 32 位", s, got)
		}
	}
	// 同一个输入必须稳定
	if cacheKeySegment("中文") != cacheKeySegment("中文") {
		t.Error("映射必须稳定")
	}
}
