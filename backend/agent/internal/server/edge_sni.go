package server

import (
	"errors"
	"fmt"
	"strings"
)

// Gateway 只解析 TLS ClientHello 的 server_name 扩展用于选路，不建立 TLS 会话、
// 不解密后续记录，源站证书与私钥始终留在源站
//（docs/design/DOOPS_EDGE_TUNNEL.md §3、§6.3）。

const defaultEdgeHelloBytes = 4096

func edgeHelloBytes() int64 {
	return positiveEnvInt64("DOOPS_EDGE_HELLO_BYTES", defaultEdgeHelloBytes)
}

const (
	tlsRecordHeaderBytes    = 5
	tlsRecordHandshake      = 0x16
	tlsVersionMajor         = 0x03
	tlsHandshakeClientHello = 0x01
	tlsHandshakeHeaderBytes = 4
	tlsExtensionServerName  = 0x0000
	tlsServerNameHostName   = 0x00
	tlsHelloRandomBytes     = 32
	tlsHelloVersionBytes    = 2
)

// errEdgeHelloIncomplete 表示已收字节还不足以判定，调用方应继续读。
// 其余错误都是终态：连接必须直接关闭，不回 TLS alert。
var errEdgeHelloIncomplete = errors.New("edge: tls client hello incomplete")

var errEdgeHelloNoServerName = errors.New("edge: client hello has no server_name extension")

func edgeMalformedHello(what string) error {
	return fmt.Errorf("edge: malformed client hello (%s)", what)
}

// extractTLSServerName 从已缓存的字节里取出 SNI 主机名，返回值已小写并去掉末尾点。
func extractTLSServerName(buffered []byte) (string, error) {
	message, err := assembleTLSClientHello(buffered)
	if err != nil {
		return "", err
	}
	return parseTLSClientHelloServerName(message)
}

// assembleTLSClientHello 把可能跨多个 TLS record 分片的第一个握手消息拼回完整字节。
func assembleTLSClientHello(buffered []byte) ([]byte, error) {
	cursor := edgeCursor{buf: buffered}
	var (
		body       []byte
		messageLen = -1
	)
	for {
		if messageLen >= 0 && len(body) >= tlsHandshakeHeaderBytes+messageLen {
			return body[:tlsHandshakeHeaderBytes+messageLen], nil
		}
		header, ok := cursor.take(tlsRecordHeaderBytes)
		if !ok {
			return nil, errEdgeHelloIncomplete
		}
		if header[0] != tlsRecordHandshake {
			return nil, fmt.Errorf("edge: not a tls handshake record (content type 0x%02x)", header[0])
		}
		if header[1] != tlsVersionMajor {
			return nil, fmt.Errorf("edge: unsupported tls record version 0x%02x%02x", header[1], header[2])
		}
		length := int(header[3])<<8 | int(header[4])
		if length == 0 {
			return nil, errors.New("edge: empty tls handshake record")
		}
		fragment, ok := cursor.take(length)
		if !ok {
			return nil, errEdgeHelloIncomplete
		}
		body = append(body, fragment...)
		if messageLen >= 0 || len(body) < tlsHandshakeHeaderBytes {
			continue
		}
		if body[0] != tlsHandshakeClientHello {
			return nil, fmt.Errorf("edge: first handshake message is not client hello (type 0x%02x)", body[0])
		}
		messageLen = int(body[1])<<16 | int(body[2])<<8 | int(body[3])
		total := tlsHandshakeHeaderBytes + messageLen
		if limit := int(edgeHelloBytes()); total > limit {
			return nil, fmt.Errorf("edge: client hello %d bytes exceeds limit %d", total, limit)
		}
	}
}

// parseTLSClientHelloServerName 解析完整的 ClientHello 握手消息。到这一步字节已齐，
// 任何长度不符都是对端畸形，不再返回 errEdgeHelloIncomplete。
func parseTLSClientHelloServerName(message []byte) (string, error) {
	cursor := edgeCursor{buf: message}
	if _, ok := cursor.take(tlsHandshakeHeaderBytes); !ok {
		return "", edgeMalformedHello("handshake header")
	}
	if _, ok := cursor.take(tlsHelloVersionBytes + tlsHelloRandomBytes); !ok {
		return "", edgeMalformedHello("client version and random")
	}
	if _, ok := cursor.takeVector8(); !ok {
		return "", edgeMalformedHello("session id")
	}
	if _, ok := cursor.takeVector16(); !ok {
		return "", edgeMalformedHello("cipher suites")
	}
	if _, ok := cursor.takeVector8(); !ok {
		return "", edgeMalformedHello("compression methods")
	}
	extensions, ok := cursor.takeVector16()
	if !ok {
		return "", errEdgeHelloNoServerName
	}
	remaining := edgeCursor{buf: extensions}
	for remaining.remaining() > 0 {
		extensionType, ok := remaining.uint16()
		if !ok {
			return "", edgeMalformedHello("extension type")
		}
		body, ok := remaining.takeVector16()
		if !ok {
			return "", edgeMalformedHello("extension body")
		}
		if extensionType == tlsExtensionServerName {
			return parseTLSServerNameExtension(body)
		}
	}
	return "", errEdgeHelloNoServerName
}

func parseTLSServerNameExtension(body []byte) (string, error) {
	cursor := edgeCursor{buf: body}
	list, ok := cursor.takeVector16()
	if !ok {
		return "", edgeMalformedHello("server_name list")
	}
	entries := edgeCursor{buf: list}
	for entries.remaining() > 0 {
		nameType, ok := entries.uint8()
		if !ok {
			return "", edgeMalformedHello("server_name entry type")
		}
		name, ok := entries.takeVector16()
		if !ok {
			return "", edgeMalformedHello("server_name entry body")
		}
		if nameType != tlsServerNameHostName {
			continue
		}
		host := strings.TrimSuffix(strings.ToLower(strings.TrimSpace(string(name))), ".")
		if host == "" {
			return "", edgeMalformedHello("empty host_name")
		}
		return host, nil
	}
	return "", errEdgeHelloNoServerName
}
