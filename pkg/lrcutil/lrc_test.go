package lrcutil

import (
	"strings"
	"testing"
)

func TestParseBasic(t *testing.T) {
	src := "[ti:测试]\n[ar:某人]\n[00:01.00]第一行\n[00:02.50]第二行\n"
	l, err := Parse(src)
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if got := l.Meta["ti"]; got != "测试" {
		t.Errorf("meta ti = %q, want 测试", got)
	}
	if got := l.Meta["ar"]; got != "某人" {
		t.Errorf("meta ar = %q, want 某人", got)
	}
	want := []Line{{1000, "第一行"}, {2500, "第二行"}}
	if len(l.Lines) != len(want) {
		t.Fatalf("lines = %d, want %d", len(l.Lines), len(want))
	}
	for i := range want {
		if l.Lines[i] != want[i] {
			t.Errorf("lines[%d] = %+v, want %+v", i, l.Lines[i], want[i])
		}
	}
}

func TestParseMultiStamp(t *testing.T) {
	l, err := Parse("[00:01.00][00:05.00]重复\n")
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if len(l.Lines) != 2 {
		t.Fatalf("lines = %d, want 2", len(l.Lines))
	}
	if l.Lines[0].Time != 1000 || l.Lines[1].Time != 5000 {
		t.Errorf("times = %d,%d", l.Lines[0].Time, l.Lines[1].Time)
	}
}

func TestParseSorts(t *testing.T) {
	l, err := Parse("[00:03.00]c\n[00:01.00]a\n[00:02.00]b\n")
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	got := []string{}
	for _, ln := range l.Lines {
		got = append(got, ln.Text)
	}
	if strings.Join(got, "") != "abc" {
		t.Errorf("order = %v, want abc", got)
	}
}

func TestParseStampForms(t *testing.T) {
	cases := []struct {
		name string
		raw  string
		want int64
	}{
		// 标准百分秒
		{"centisecond", "00:01.00", 1000},
		{"centisecond50", "00:02.50", 2500},
		// 一位小数右补零
		{"one-digit", "00:01.5", 1500},
		// 三毫秒位
		{"millisecond", "00:01.505", 1505},
		// 两位显式小数，不能当成 50ms
		{"two-digit-frac", "00:01.05", 1050},
		// 无小数
		{"no-frac", "01:30", 90000},
		// 网易云双冒号：[00:40:59] 应等价于 [00:40.59]
		{"wy-double-colon", "00:40:59", 40590},
		// 时:分:秒.小数
		{"hour", "01:02:03.50", 3723500},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, ok := parseStamp(c.raw)
			if !ok {
				t.Fatalf("parseStamp(%q) 解析失败", c.raw)
			}
			if got != c.want {
				t.Errorf("parseStamp(%q) = %d, want %d", c.raw, got, c.want)
			}
		})
	}
}

func TestParseStampRejects(t *testing.T) {
	for _, raw := range []string{"", "abc", "1", "1:2:3:4", "-1:00", "00:-1.00", "00:01.zz", "ti:标题"} {
		if _, ok := parseStamp(raw); ok {
			t.Errorf("parseStamp(%q) 应失败，却返回成功", raw)
		}
	}
}

func TestOffset(t *testing.T) {
	l, err := Parse("[offset:100]\n[00:01.00]a\n")
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if got := l.Offset(); got != 100 {
		t.Errorf("Offset = %d, want 100", got)
	}
	// offset 不应自动应用到时间戳
	if l.Lines[0].Time != 1000 {
		t.Errorf("time = %d, want 1000（Parse 不应自动应用 offset）", l.Lines[0].Time)
	}

	bad, err := Parse("[offset:xx]\n[00:01.00]a\n")
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if got := bad.Offset(); got != 0 {
		t.Errorf("非法 offset 应返回 0，得到 %d", got)
	}
}

func TestShift(t *testing.T) {
	in := []Line{{1000, "a"}, {2000, "b"}}
	out := Shift(in, -1500)
	if out[0].Time != 0 {
		t.Errorf("out[0] = %d, want 0（负数截到 0）", out[0].Time)
	}
	if out[1].Time != 500 {
		t.Errorf("out[1] = %d, want 500", out[1].Time)
	}
	if in[0].Time != 1000 {
		t.Errorf("Shift 修改了入参")
	}
}

func TestFormat(t *testing.T) {
	lines := []Line{{0, "a"}, {1500, "b"}, {3723500, "c"}}
	got := Format(lines)
	want := "[00:00.00] a\n[00:01.50] b\n[62:03.50] c\n"
	if got != want {
		t.Errorf("Format =\n%q\nwant\n%q", got, want)
	}
}

func TestRoundTrip(t *testing.T) {
	src := "[ti:x]\n[00:01.00]第一行\n[00:02.50]第二行\n"
	l1, err := Parse(src)
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	l2, err := Parse(Format(l1.Lines))
	if err != nil {
		t.Fatalf("再解析: %v", err)
	}
	if len(l1.Lines) != len(l2.Lines) {
		t.Fatalf("行数 %d != %d", len(l1.Lines), len(l2.Lines))
	}
	for i := range l1.Lines {
		if l1.Lines[i] != l2.Lines[i] {
			t.Errorf("lines[%d] %+v != %+v", i, l1.Lines[i], l2.Lines[i])
		}
	}
}

func TestParseEmpty(t *testing.T) {
	for _, src := range []string{"", "\n\n", "纯文本没有时间戳"} {
		if _, err := Parse(src); err == nil {
			t.Errorf("Parse(%q) 应返回错误", src)
		}
	}
}

func TestParseBlankTextSkipped(t *testing.T) {
	l, err := Parse("[00:01.00]a\n[00:02.00]\n[00:03.00]c\n")
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if len(l.Lines) != 2 {
		t.Fatalf("lines = %d, want 2（空文本行应跳过）", len(l.Lines))
	}
}

func FuzzParse(f *testing.F) {
	seeds := []string{
		"",
		"[00:01.00]a",
		"[00:40:59]wy",
		"[ti:x][ar:y]",
		"[01:02:03.50]h",
		"[99999999:99999999.999999]big",
		"[[[[[]]]]]",
		"[:::]",
	}
	for _, s := range seeds {
		f.Add(s)
	}
	f.Fuzz(func(t *testing.T, src string) {
		l, err := Parse(src)
		if err != nil {
			return
		}
		// 不 panic、时间非负、结果有序
		for i, ln := range l.Lines {
			if ln.Time < 0 {
				t.Fatalf("负时间 %d", ln.Time)
			}
			if i > 0 && l.Lines[i-1].Time > ln.Time {
				t.Fatalf("结果未排序: %d > %d", l.Lines[i-1].Time, ln.Time)
			}
		}
		// Format 结果必须能被重新解析回来
		if len(l.Lines) == 0 {
			return
		}
		if _, err := Parse(Format(l.Lines)); err != nil {
			t.Fatalf("Format 后无法再解析: %v", err)
		}
	})
}

func TestParseMetaOnly(t *testing.T) {
	l, err := Parse("[ti:只有标签]\n[ar:某人]\n")
	if err != nil {
		t.Fatalf("只有标签应解析成功，得到错误: %v", err)
	}
	if len(l.Lines) != 0 {
		t.Errorf("lines = %d, want 0", len(l.Lines))
	}
	if l.Meta["ti"] != "只有标签" {
		t.Errorf("meta ti = %q", l.Meta["ti"])
	}
}
