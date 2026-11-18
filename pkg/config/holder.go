package config

import (
	"sync/atomic"
	"time"
)

// Holder 持有当前配置快照，读路径完全无锁。
//
// 用 atomic.Pointer 而不是 RWMutex：配置读的频次远高于写，
// 而且写方只需换一个指针，读者永远拿到完整对象。
type Holder struct {
	p atomic.Pointer[Snapshot]
}

// NewHolder 用初始快照创建 Holder。初始快照不可为 nil。
func NewHolder(init *Snapshot) *Holder {
	if init == nil {
		init = &Snapshot{Dyn: Default(), LoadedAt: time.Now(), Version: 1}
	}
	h := &Holder{}
	h.p.Store(init)
	return h
}

// Current 返回当前快照。永不返回 nil（前提是用 NewHolder 创建）。
func (h *Holder) Current() *Snapshot {
	return h.p.Load()
}

// Swap 原子替换快照并返回旧值。版本号自动 +1。
func (h *Holder) Swap(next *Dynamic, hash [32]byte, loadedAt time.Time) (old, created *Snapshot) {
	old = h.p.Load()
	created = &Snapshot{
		Dyn:      next,
		Hash:     hash,
		LoadedAt: loadedAt,
	}
	if old != nil {
		created.Version = old.Version + 1
	} else {
		created.Version = 1
	}
	h.p.Store(created)
	return old, created
}

// Dynamic 是便捷方法，直接取当前业务配置。
func (h *Holder) Dynamic() *Dynamic {
	if s := h.p.Load(); s != nil {
		return s.Dyn
	}
	return nil
}

// Version 返回当前版本号。
func (h *Holder) Version() uint64 {
	if s := h.p.Load(); s != nil {
		return s.Version
	}
	return 0
}
