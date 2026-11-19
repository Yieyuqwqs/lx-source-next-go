package config

import (
	"bytes"
	"errors"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestEnsureFilesCreatesDefaults(t *testing.T) {
	dir := t.TempDir()

	created, err := EnsureFiles(dir)
	if err != nil {
		t.Fatalf("EnsureFiles: %v", err)
	}
	if len(created) != 2 {
		t.Fatalf("生成文件数 = %d, want 2 (%v)", len(created), created)
	}

	// 生成的配置必须能被真实解析，否则等于给用户塞了个坏文件
	dyn, err := os.ReadFile(filepath.Join(dir, DynamicFile))
	if err != nil {
		t.Fatalf("读取 dynamic: %v", err)
	}
	if _, err := Parse(dyn); err != nil {
		t.Fatalf("内置 dynamic 模板解析失败: %v", err)
	}

	bootRaw, err := os.ReadFile(filepath.Join(dir, BootstrapFile))
	if err != nil {
		t.Fatalf("读取 bootstrap: %v", err)
	}
	if _, err := ParseBootstrap(bootRaw); err != nil {
		t.Fatalf("内置 bootstrap 模板解析失败: %v", err)
	}
}

func TestEnsureFilesKeepsExisting(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, DynamicFile)
	custom := "# 用户自己的配置\n[Data]\nMode = 0\n"
	if err := os.WriteFile(path, []byte(custom), 0o644); err != nil {
		t.Fatal(err)
	}

	created, err := EnsureFiles(dir)
	if err != nil {
		t.Fatalf("EnsureFiles: %v", err)
	}
	// 只应补出 bootstrap，不动已有的 dynamic
	if len(created) != 1 || filepath.Base(created[0]) != BootstrapFile {
		t.Errorf("创建了 %v，应只创建 bootstrap", created)
	}

	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != custom {
		t.Error("已有配置被覆盖了")
	}
}

func TestEnsureFilesIdempotent(t *testing.T) {
	dir := t.TempDir()
	if _, err := EnsureFiles(dir); err != nil {
		t.Fatal(err)
	}
	created, err := EnsureFiles(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(created) != 0 {
		t.Errorf("第二次不应再创建任何文件，实际 %v", created)
	}
}

func TestEnsureFilesBadDir(t *testing.T) {
	// 用一个文件当目录，制造必然失败
	f := filepath.Join(t.TempDir(), "notadir")
	if err := os.WriteFile(f, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := EnsureFiles(filepath.Join(f, "sub")); err == nil {
		t.Error("不可创建的目录应报错")
	}
}

func TestTemplateEmbedded(t *testing.T) {
	for _, name := range []string{BootstrapFile, DynamicFile} {
		b, err := Template(name)
		if err != nil {
			t.Fatalf("Template(%s): %v", name, err)
		}
		if len(b) == 0 {
			t.Errorf("模板 %s 为空", name)
		}
	}
	if _, err := Template("nope.toml"); err == nil {
		t.Error("不存在的模板应报错")
	}
}

// TestLogErrorDedup 覆盖「配置长期缺失时日志刷屏」的修复。
func TestLogErrorDedup(t *testing.T) {
	var buf bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&buf, nil))
	r := NewReloader(NewHolder(nil), BytesLoader{Err: errors.New("boom")}, logger)

	count := func() int { return strings.Count(buf.String(), "配置加载失败") }

	r.logError(errors.New("boom"))
	r.logError(errors.New("boom"))
	r.logError(errors.New("boom"))
	if got := count(); got != 1 {
		t.Errorf("同一错误应只记录 1 次，实际 %d 次", got)
	}

	// 换了错误文案，应当再记一条
	r.logError(errors.New("另一种错误"))
	if got := count(); got != 2 {
		t.Errorf("不同错误应再记录，实际 %d 次", got)
	}

	// 恢复后重置，下次同名错误仍要能看到
	r.ResetErrorState()
	r.logError(errors.New("boom"))
	if got := count(); got != 3 {
		t.Errorf("重置后应重新记录，实际 %d 次", got)
	}
}

// TestTemplatesParseAsRootKeys 守住一个真实踩过的坑：
// 往模板末尾追加键时，如果末尾还处在某个 [表] 里，
// 这个键就会被解析成该表的子字段，导致「文档里有、结构体里没有」而报错。
func TestTemplatesParseAsRootKeys(t *testing.T) {
	raw, err := Template(BootstrapFile)
	if err != nil {
		t.Fatalf("读取模板: %v", err)
	}
	boot, err := ParseBootstrap(raw)
	if err != nil {
		t.Fatalf("bootstrap 模板解析失败: %v", err)
	}
	// DNSServer 必须落在根层，而不是被塞进 [Kw]
	if boot.DNSServer != "" {
		t.Errorf("DNSServer 默认应为空，实际 %q", boot.DNSServer)
	}
	if boot.Kw.Strategy != "anti" {
		t.Errorf("Kw.Strategy 应为 anti，实际 %q", boot.Kw.Strategy)
	}
	if boot.Listen != ":1101" {
		t.Errorf("Listen 解析错误: %q", boot.Listen)
	}

	dyn, err := Template(DynamicFile)
	if err != nil {
		t.Fatalf("读取模板: %v", err)
	}
	if _, err := Parse(dyn); err != nil {
		t.Fatalf("dynamic 模板解析失败: %v", err)
	}
}
