package server

import "encoding/binary"

// edgeCursor 在只读字节切片上顺序取值，用于解析 TLS ClientHello 与 PROXY protocol 头。
// 剩余字节不足时所有方法返回 false 且不移动位置，让调用方能把"还需继续读"与
// "字节非法"区分开。
type edgeCursor struct {
	buf []byte
	pos int
}

func (c *edgeCursor) remaining() int {
	return len(c.buf) - c.pos
}

func (c *edgeCursor) take(n int) ([]byte, bool) {
	if n < 0 || c.remaining() < n {
		return nil, false
	}
	out := c.buf[c.pos : c.pos+n]
	c.pos += n
	return out, true
}

func (c *edgeCursor) uint8() (uint8, bool) {
	raw, ok := c.take(1)
	if !ok {
		return 0, false
	}
	return raw[0], true
}

func (c *edgeCursor) uint16() (uint16, bool) {
	raw, ok := c.take(2)
	if !ok {
		return 0, false
	}
	return binary.BigEndian.Uint16(raw), true
}

// takeVector8 与 takeVector16 读取 TLS 风格的变长向量：先读长度前缀，再读同长内容。
func (c *edgeCursor) takeVector8() ([]byte, bool) {
	saved := c.pos
	length, ok := c.uint8()
	if !ok {
		return nil, false
	}
	body, ok := c.take(int(length))
	if !ok {
		c.pos = saved
		return nil, false
	}
	return body, true
}

func (c *edgeCursor) takeVector16() ([]byte, bool) {
	saved := c.pos
	length, ok := c.uint16()
	if !ok {
		return nil, false
	}
	body, ok := c.take(int(length))
	if !ok {
		c.pos = saved
		return nil, false
	}
	return body, true
}

func cloneEdgePayload(payload []byte) []byte {
	if len(payload) == 0 {
		return nil
	}
	out := make([]byte, len(payload))
	copy(out, payload)
	return out
}
