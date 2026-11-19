// Package config 负责配置的分层、校验与原子热重载。
//
// 分层：
//   - Bootstrap：进程级参数（监听地址、日志），启动后不可变；
//   - Dynamic：业务参数（鉴权、源、缓存规则），可热更。
//
// 热更模型：解析 → 校验 → 计算哈希 → 与当前快照比对 → 原子换指针。
// 任何一步失败都保留旧快照，读方永远看不到半成品。
// 本包不认识缓存、不认识 HTTP，只负责「当前这份配置是什么」。
package config

import (
	"bytes"
	"crypto/sha256"
	"errors"
	"fmt"
	"time"

	"github.com/pelletier/go-toml/v2"
)

// Dynamic 是业务配置，支持热更。
type Dynamic struct {
	Auth Auth `toml:"Auth"`
	Apis Apis `toml:"Apis"`
	Srcs Srcs `toml:"Srcs"`
	Data Data `toml:"Data"`
	Errs Errs `toml:"Errs"`
}

// Auth 是访问控制配置。
type Auth struct {
	BanList   BanList   `toml:"BanList"`
	RateLimit RateLimit `toml:"RateLimit"`
	ApiPass   ApiPass   `toml:"ApiPass"`
}

// BanList 是 IP 黑白名单。
type BanList struct {
	Enable bool     `toml:"Enable"`
	Strict bool     `toml:"Strict"`
	White  []string `toml:"White"`
	Black  []string `toml:"Black"`
}

// RateLimit 是限流配置。
type RateLimit struct {
	Enable bool `toml:"Enable"`
	// Block 检测分区（秒）。
	Block int `toml:"Block"`
	// Globle 全局上限。
	Globle int `toml:"Globle"`
	// Single 单键上限。
	Single int `toml:"Single"`
}

// ApiPass 是接口密钥配置。
type ApiPass struct {
	Enable bool `toml:"Enable"`
	// Auto 是否把密钥自动注入脚本；默认关闭。
	Auto bool `toml:"Auto"`
	// Value 密钥值。
	Value string `toml:"Value"`
}

// Apis 是接口开关。
type Apis struct {
	// WebLog 是否开放 SSE 日志（需要密钥）。
	WebLog bool `toml:"WebLog"`
	// ApiOnly 是否只挂接口、不挂前端。
	ApiOnly bool `toml:"ApiOnly"`
}

// Srcs 是各来源开关。
type Srcs struct {
	Wy SrcFlag `toml:"Wy"`
	Mg SrcFlag `toml:"Mg"`
	Kw SrcFlag `toml:"Kw"`
	Kg SrcFlag `toml:"Kg"`
	Tx SrcFlag `toml:"Tx"`
	Bi SrcFlag `toml:"Bi"`
}

// SrcFlag 表示单个来源是否启用。
type SrcFlag struct {
	Enable bool `toml:"Enable"`
}

// Data 是缓存配置。
type Data struct {
	// Mode 缓存模式：0 禁用，1 安全，2 极速。
	Mode int `toml:"Mode"`
	// Type 缓存类型：0 本地。
	Type int `toml:"Type"`
	// TTL 缓存有效期（秒）；<=0 时用策略层默认值。
	TTL int `toml:"TTL"`
	// Rule 缓存规则。
	Rule CacheRule `toml:"Rule"`
}

// CacheRule 决定哪些平台与音质进入缓存。
//
// 列表语义：启用排除项时列表表示「不缓存的集合」，否则表示「缓存的集合」；
// 列表为空表示不限制。
type CacheRule struct {
	// ExcP 平台列表是否为排除语义。
	ExcP bool `toml:"ExcP"`
	// Plat 平台二字码列表。
	Plat []string `toml:"Plat"`
	// ExcQ 音质列表是否为排除语义。
	ExcQ bool `toml:"ExcQ"`
	// Qual 音质列表。
	Qual []string `toml:"Qual"`
	// AllowSpecial 是否缓存特殊音质（temp 试听 / orig 原声）。
	AllowSpecial bool `toml:"AllowSpecial"`
}

// Errs 是错误兜底配置。
type Errs struct {
	// Expi 错误音频有效期（秒）。
	Expi int `toml:"Expi"`
	// ErrMp3 自定义错误音频地址。
	ErrMp3 string `toml:"ErrMp3"`
}

// 校验用的边界常量。
const (
	maxRateBlock  = 3600
	maxRateSingle = 100000
)

// Default 返回一份可直接运行的默认配置。
func Default() *Dynamic {
	return &Dynamic{
		Auth: Auth{
			BanList:   BanList{White: []string{"127.0.0.1", "::1"}},
			RateLimit: RateLimit{Block: 30, Globle: 60, Single: 15},
			ApiPass:   ApiPass{Enable: false},
		},
		Srcs: Srcs{
			Wy: SrcFlag{Enable: true},
			Mg: SrcFlag{Enable: true},
			Kw: SrcFlag{Enable: true},
			Kg: SrcFlag{Enable: true},
			Tx: SrcFlag{Enable: true},
		},
		Data: Data{Mode: 1, Type: 0},
		Errs: Errs{Expi: 120},
	}
}

// Parse 解析并校验 TOML 配置。
//
// 使用 DisallowUnknownFields：拼错的键直接报错，而不是被静默忽略——
// 配置里静默失效的字段是最难排查的一类问题。
func Parse(raw []byte) (*Dynamic, error) {
	var d Dynamic
	dec := toml.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&d); err != nil {
		return nil, fmt.Errorf("config: 解析失败: %w", err)
	}
	if err := d.Validate(); err != nil {
		return nil, err
	}
	return &d, nil
}

// Validate 检查配置语义。只有校验通过才会被发布。
func (d *Dynamic) Validate() error {
	if d == nil {
		return errors.New("config: 配置为空")
	}
	rl := d.Auth.RateLimit
	if rl.Block < 0 || rl.Block > maxRateBlock {
		return fmt.Errorf("config: Auth.RateLimit.Block 需在 0..%d 之间，当前 %d", maxRateBlock, rl.Block)
	}
	if rl.Globle < 0 {
		return fmt.Errorf("config: Auth.RateLimit.Globle 不能为负，当前 %d", rl.Globle)
	}
	if rl.Single < 0 || rl.Single > maxRateSingle {
		return fmt.Errorf("config: Auth.RateLimit.Single 需在 0..%d 之间，当前 %d", maxRateSingle, rl.Single)
	}
	if d.Auth.ApiPass.Enable && d.Auth.ApiPass.Value == "" {
		return errors.New("config: 启用了 ApiPass 但 Value 为空")
	}
	if d.Data.Mode < 0 || d.Data.Mode > 2 {
		return fmt.Errorf("config: Data.Mode 只能是 0/1/2，当前 %d", d.Data.Mode)
	}
	if d.Data.Type != 0 {
		return fmt.Errorf("config: Data.Type 目前只支持 0（本地文件缓存），当前 %d", d.Data.Type)
	}
	if d.Errs.Expi < 0 {
		return fmt.Errorf("config: Errs.Expi 不能为负，当前 %d", d.Errs.Expi)
	}
	return nil
}

// Fingerprint 返回配置内容的指纹，用于判断是否真的变化。
func Fingerprint(raw []byte) [sha256.Size]byte {
	return sha256.Sum256(raw)
}

// Snapshot 是某一时刻的不可变配置视图。
//
// 读取方拿到指针后可以放心使用：内容不会再被修改，
// 后续热更只会换一个新指针，不会原地改老对象。
type Snapshot struct {
	// Dyn 是不可变的业务配置。
	Dyn *Dynamic
	// Hash 是原始内容的 SHA-256。
	Hash [sha256.Size]byte
	// LoadedAt 是发布时间。
	LoadedAt time.Time
	// Version 单调递增，便于日志与排查。
	Version uint64
}
