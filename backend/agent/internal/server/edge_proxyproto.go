package server

import (
	"bytes"
	"encoding/binary"
	"errors"
	"fmt"
	"net/netip"
	"strconv"
	"strings"
)

// Edge listener 通常挂在 nginx stream 之后（一个公网 IP 上与 gateway.example.com 共用 443），
// 此时 Accept 拿到的对端地址恒为 127.0.0.1，只有 PROXY protocol 头能给出真实客户端 IP
//（docs/design/DOOPS_EDGE_TUNNEL.md §3.3）。

var proxyProtocolV2Signature = []byte{0x0D, 0x0A, 0x0D, 0x0A, 0x00, 0x0D, 0x0A, 0x51, 0x55, 0x49, 0x54, 0x0A}

const (
	proxyProtocolV1Prefix      = "PROXY "
	proxyProtocolV1MaxBytes    = 107
	proxyProtocolV2HeaderBytes = 16
	proxyProtocolV2Version     = 0x2
	proxyProtocolV2CommandLoc  = 0x0
	proxyProtocolV2CommandPrxy = 0x1
	proxyProtocolV2FamilyINET  = 0x1
	proxyProtocolV2FamilyINET6 = 0x2
	proxyProtocolV2ProtoStream = 0x1
	proxyProtocolV2INETBytes   = 12
	proxyProtocolV2INET6Bytes  = 36
)

// errEdgeProxyHeaderIncomplete 表示还需继续读；其余错误都是终态。
var errEdgeProxyHeaderIncomplete = errors.New("edge: proxy protocol header incomplete")

// parseProxyProtocolHeader 返回真实客户端地址与头部占用的字节数。
//
// 解析失败一律是终态错误，绝不回落到 Accept 的对端地址：per-IP 并发配额依赖这个地址，
// 一旦回落，攻击者用单机就能绕过配额。
func parseProxyProtocolHeader(buffered []byte) (netip.AddrPort, int, error) {
	if len(buffered) == 0 {
		return netip.AddrPort{}, 0, errEdgeProxyHeaderIncomplete
	}
	if buffered[0] == proxyProtocolV2Signature[0] {
		return parseProxyProtocolV2(buffered)
	}
	return parseProxyProtocolV1(buffered)
}

// partialPrefixMatch 判断 buffered 是否仍可能长成以 prefix 开头的完整头部，
// 用来在只收到几个字节时就把明显不是 PROXY 头的流量判死，而不是一直等超时。
func partialPrefixMatch(buffered []byte, prefix string) bool {
	if len(buffered) < len(prefix) {
		return strings.HasPrefix(prefix, string(buffered))
	}
	return strings.HasPrefix(string(buffered), prefix)
}

func parseProxyProtocolV1(buffered []byte) (netip.AddrPort, int, error) {
	end := bytes.Index(buffered, []byte("\r\n"))
	if end < 0 {
		if len(buffered) >= proxyProtocolV1MaxBytes {
			return netip.AddrPort{}, 0, fmt.Errorf("edge: proxy protocol v1 header exceeds %d bytes without CRLF", proxyProtocolV1MaxBytes)
		}
		if !partialPrefixMatch(buffered, proxyProtocolV1Prefix) {
			return netip.AddrPort{}, 0, errors.New("edge: missing proxy protocol v1 prefix")
		}
		return netip.AddrPort{}, 0, errEdgeProxyHeaderIncomplete
	}
	line := string(buffered[:end])
	consumed := end + 2
	if !strings.HasPrefix(line, proxyProtocolV1Prefix) {
		return netip.AddrPort{}, 0, errors.New("edge: missing proxy protocol v1 prefix")
	}
	fields := strings.Split(line, " ")
	if len(fields) == 2 && fields[1] == "UNKNOWN" {
		return netip.AddrPort{}, 0, errors.New("edge: proxy protocol v1 UNKNOWN carries no client address")
	}
	if len(fields) != 6 {
		return netip.AddrPort{}, 0, fmt.Errorf("edge: proxy protocol v1 expects 6 fields, got %d", len(fields))
	}
	addr, err := netip.ParseAddr(fields[2])
	if err != nil {
		return netip.AddrPort{}, 0, fmt.Errorf("edge: proxy protocol v1 source address %q: %w", fields[2], err)
	}
	switch fields[1] {
	case "TCP4":
		if !addr.Is4() {
			return netip.AddrPort{}, 0, fmt.Errorf("edge: proxy protocol v1 TCP4 carries non-IPv4 source %q", fields[2])
		}
	case "TCP6":
		if addr.Is4() {
			return netip.AddrPort{}, 0, fmt.Errorf("edge: proxy protocol v1 TCP6 carries non-IPv6 source %q", fields[2])
		}
	default:
		return netip.AddrPort{}, 0, fmt.Errorf("edge: unsupported proxy protocol v1 family %q", fields[1])
	}
	port, err := strconv.ParseUint(fields[4], 10, 16)
	if err != nil {
		return netip.AddrPort{}, 0, fmt.Errorf("edge: proxy protocol v1 source port %q: %w", fields[4], err)
	}
	return netip.AddrPortFrom(addr, uint16(port)), consumed, nil
}

func parseProxyProtocolV2(buffered []byte) (netip.AddrPort, int, error) {
	if len(buffered) < len(proxyProtocolV2Signature) {
		if !bytes.HasPrefix(proxyProtocolV2Signature, buffered) {
			return netip.AddrPort{}, 0, errors.New("edge: invalid proxy protocol v2 signature")
		}
		return netip.AddrPort{}, 0, errEdgeProxyHeaderIncomplete
	}
	if !bytes.Equal(buffered[:len(proxyProtocolV2Signature)], proxyProtocolV2Signature) {
		return netip.AddrPort{}, 0, errors.New("edge: invalid proxy protocol v2 signature")
	}
	if len(buffered) < proxyProtocolV2HeaderBytes {
		return netip.AddrPort{}, 0, errEdgeProxyHeaderIncomplete
	}
	versionCommand := buffered[12]
	if version := versionCommand >> 4; version != proxyProtocolV2Version {
		return netip.AddrPort{}, 0, fmt.Errorf("edge: unsupported proxy protocol version %d", version)
	}
	length := int(binary.BigEndian.Uint16(buffered[14:proxyProtocolV2HeaderBytes]))
	consumed := proxyProtocolV2HeaderBytes + length
	if len(buffered) < consumed {
		return netip.AddrPort{}, 0, errEdgeProxyHeaderIncomplete
	}
	switch command := versionCommand & 0x0f; command {
	case proxyProtocolV2CommandPrxy:
	case proxyProtocolV2CommandLoc:
		return netip.AddrPort{}, 0, errors.New("edge: proxy protocol v2 LOCAL carries no client address")
	default:
		return netip.AddrPort{}, 0, fmt.Errorf("edge: unsupported proxy protocol v2 command %d", command)
	}
	if protocol := buffered[13] & 0x0f; protocol != proxyProtocolV2ProtoStream {
		return netip.AddrPort{}, 0, fmt.Errorf("edge: proxy protocol v2 carries non-stream protocol %d", protocol)
	}
	body := buffered[proxyProtocolV2HeaderBytes:consumed]
	switch family := buffered[13] >> 4; family {
	case proxyProtocolV2FamilyINET:
		if len(body) < proxyProtocolV2INETBytes {
			return netip.AddrPort{}, 0, fmt.Errorf("edge: proxy protocol v2 INET block %d shorter than %d", len(body), proxyProtocolV2INETBytes)
		}
		addr := netip.AddrFrom4([4]byte(body[0:4]))
		return netip.AddrPortFrom(addr, binary.BigEndian.Uint16(body[8:10])), consumed, nil
	case proxyProtocolV2FamilyINET6:
		if len(body) < proxyProtocolV2INET6Bytes {
			return netip.AddrPort{}, 0, fmt.Errorf("edge: proxy protocol v2 INET6 block %d shorter than %d", len(body), proxyProtocolV2INET6Bytes)
		}
		addr := netip.AddrFrom16([16]byte(body[0:16]))
		return netip.AddrPortFrom(addr, binary.BigEndian.Uint16(body[32:34])), consumed, nil
	default:
		return netip.AddrPort{}, 0, fmt.Errorf("edge: unsupported proxy protocol v2 address family %d", family)
	}
}
