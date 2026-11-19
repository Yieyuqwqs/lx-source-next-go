package config

import (
	"embed"
	"fmt"
	"os"
	"path/filepath"
)

// 内置的默认配置模板。
//
// 用 embed 而不是仓库里的示例文件，是为了让「默认配置」只有一个真源：
// 二进制自带、首次运行落盘、仓库里看到的就是同一份。
//
//go:embed templates/bootstrap.toml templates/dynamic.toml
var templates embed.FS

// 模板文件名常量。
const (
	BootstrapFile = "bootstrap.toml"
	DynamicFile   = "dynamic.toml"
)

// EnsureFiles 确保配置目录里有 bootstrap.toml 与 dynamic.toml。
//
// 缺失时用内置模板生成，已存在则原样保留（绝不覆盖用户配置）。
// 返回实际新建的文件路径，便于调用方打日志。
func EnsureFiles(dir string) ([]string, error) {
	if dir == "" {
		dir = "data"
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, fmt.Errorf("config: 创建配置目录 %s: %w", dir, err)
	}

	var created []string
	for _, name := range []string{BootstrapFile, DynamicFile} {
		path := filepath.Join(dir, name)
		if _, err := os.Stat(path); err == nil {
			continue // 已存在，不碰
		} else if !os.IsNotExist(err) {
			return created, fmt.Errorf("config: 检查 %s: %w", path, err)
		}

		data, err := templates.ReadFile("templates/" + name)
		if err != nil {
			return created, fmt.Errorf("config: 读取内置模板 %s: %w", name, err)
		}
		// 原子写：先写临时文件再改名，避免留下半个配置
		tmp := path + ".tmp"
		if err := os.WriteFile(tmp, data, 0o644); err != nil {
			return created, fmt.Errorf("config: 写入 %s: %w", tmp, err)
		}
		if err := os.Rename(tmp, path); err != nil {
			os.Remove(tmp)
			return created, fmt.Errorf("config: 生成 %s: %w", path, err)
		}
		created = append(created, path)
	}
	return created, nil
}

// Template 返回内置模板内容，供测试与文档使用。
func Template(name string) ([]byte, error) {
	return templates.ReadFile("templates/" + name)
}
