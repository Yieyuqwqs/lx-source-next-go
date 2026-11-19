package logx

import "testing"

// TestPublishAfterCancel 取消订阅后再写日志不能 panic。
//
// 回归用：cancel 以前会 close 通道，而 publish 在锁外非阻塞发送，
// 两者并发就是经典的 send on closed channel 崩溃。
func TestPublishAfterCancel(t *testing.T) {
	h := NewHub(8)
	ch, cancel := h.Subscribe(4)
	cancel()

	// 取消之后继续写日志（模拟客户端断开后其它请求仍在打日志）
	for i := 0; i < 200; i++ {
		h.publish(Record{Msg: "after cancel"})
	}
	// 通道已无人接收，但不应被关闭；能读到零值也不算问题
	select {
	case <-ch:
	default:
	}

	if n := h.Subscribers(); n != 0 {
		t.Errorf("取消后订阅者数量 = %d，应为 0", n)
	}
}

// TestPublishNeverBlocks publish 对慢订阅者必须是非阻塞的。
func TestPublishNeverBlocks(t *testing.T) {
	h := NewHub(4)
	_, cancel := h.Subscribe(1) // 缓冲只有 1，且没人读
	defer cancel()

	for i := 0; i < 100; i++ {
		h.publish(Record{Msg: "blocking?"})
	}
}
