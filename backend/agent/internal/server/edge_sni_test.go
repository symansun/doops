package server

import (
	"crypto/tls"
	"errors"
	"net"
	"strings"
	"testing"
	"time"
)

func TestExtractTLSServerNameFromRealClientHello(t *testing.T) {
	hello := captureClientHello(t, &tls.Config{ServerName: "amp.demo.example.com"})
	serverName, err := extractTLSServerName(hello)
	if err != nil {
		t.Fatalf("extract server name: %v", err)
	}
	if serverName != "amp.demo.example.com" {
		t.Fatalf("server name = %q, want amp.demo.example.com", serverName)
	}
}

func TestExtractTLSServerNameLowercasesHost(t *testing.T) {
	hello := captureClientHello(t, &tls.Config{ServerName: "AMP.DEMO.Example.COM"})
	serverName, err := extractTLSServerName(hello)
	if err != nil {
		t.Fatalf("extract server name: %v", err)
	}
	if serverName != "amp.demo.example.com" {
		t.Fatalf("server name = %q, want it lowercased", serverName)
	}
}

func TestExtractTLSServerNameAcceptsFragmentedRecords(t *testing.T) {
	hello := captureClientHello(t, &tls.Config{ServerName: "webide.demo.example.com"})
	for _, chunk := range []int{1, 17, 64} {
		fragmented := refragmentClientHello(t, hello, chunk)
		serverName, err := extractTLSServerName(fragmented)
		if err != nil {
			t.Fatalf("extract from %d-byte fragments: %v", chunk, err)
		}
		if serverName != "webide.demo.example.com" {
			t.Fatalf("server name from %d-byte fragments = %q", chunk, serverName)
		}
	}
}

func TestExtractTLSServerNameReportsIncompleteUntilHelloIsWhole(t *testing.T) {
	hello := captureClientHello(t, &tls.Config{ServerName: "amp.demo.example.com"})
	for size := 1; size < len(hello); size++ {
		if _, err := extractTLSServerName(hello[:size]); !errors.Is(err, errEdgeHelloIncomplete) {
			t.Fatalf("extract from first %d of %d bytes: err = %v, want errEdgeHelloIncomplete", size, len(hello), err)
		}
	}
	if _, err := extractTLSServerName(hello); err != nil {
		t.Fatalf("extract from the whole hello: %v", err)
	}
}

func TestExtractTLSServerNameRejectsHelloWithoutServerName(t *testing.T) {
	hello := captureClientHello(t, &tls.Config{InsecureSkipVerify: true})
	if _, err := extractTLSServerName(hello); !errors.Is(err, errEdgeHelloNoServerName) {
		t.Fatalf("extract error = %v, want errEdgeHelloNoServerName", err)
	}
}

func TestExtractTLSServerNameRejectsNonTLSTrafficImmediately(t *testing.T) {
	cases := []struct {
		name  string
		input string
		want  string
	}{
		{"http request", "GET / HTTP/1.1\r\nHost: amp.demo.example.com\r\n\r\n", "not a tls handshake record"},
		{"ssh banner", "SSH-2.0-OpenSSH_9.6\r\n", "not a tls handshake record"},
		{"bad record version", "\x16\x02\x03\x00\x05hello", "unsupported tls record version"},
		{"empty record", "\x16\x03\x01\x00\x00", "empty tls handshake record"},
		{"not a client hello", "\x16\x03\x01\x00\x05\x02\x00\x00\x01\x00", "is not client hello"},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			_, err := extractTLSServerName([]byte(testCase.input))
			if err == nil {
				t.Fatalf("extract accepted %s", testCase.name)
			}
			if errors.Is(err, errEdgeHelloIncomplete) {
				t.Fatalf("extract asked for more bytes on %s, want a terminal error", testCase.name)
			}
			if !strings.Contains(err.Error(), testCase.want) {
				t.Fatalf("extract error = %v, want it to mention %q", err, testCase.want)
			}
		})
	}
}

func TestExtractTLSServerNameRejectsHelloOverLimit(t *testing.T) {
	t.Setenv("DOOPS_EDGE_HELLO_BYTES", "128")
	hello := captureClientHello(t, &tls.Config{ServerName: "amp.demo.example.com"})
	if len(hello) <= 128 {
		t.Skipf("captured hello is only %d bytes, cannot exceed the 128-byte limit", len(hello))
	}
	_, err := extractTLSServerName(hello)
	if err == nil || !strings.Contains(err.Error(), "exceeds limit") {
		t.Fatalf("extract error = %v, want it to reject a hello above the limit", err)
	}
}

// TestExtractTLSServerNameSurvivesCorruptedBytes 保证任何字节序列都只产生 error，
// 不 panic：这段解析直接暴露在公网 443 上，是唯一无鉴权的输入面。
func TestExtractTLSServerNameSurvivesCorruptedBytes(t *testing.T) {
	hello := captureClientHello(t, &tls.Config{ServerName: "amp.demo.example.com"})
	for offset := 0; offset < len(hello); offset++ {
		for _, mask := range []byte{0xFF, 0x01, 0x80} {
			corrupted := make([]byte, len(hello))
			copy(corrupted, hello)
			corrupted[offset] ^= mask
			_, _ = extractTLSServerName(corrupted)
		}
		_, _ = extractTLSServerName(hello[offset:])
	}
}

// captureClientHello 用真实的 crypto/tls 客户端产出 ClientHello 字节，
// 避免手写样本与实际浏览器流量脱节。
func captureClientHello(t *testing.T, config *tls.Config) []byte {
	t.Helper()
	client, server := net.Pipe()
	t.Cleanup(func() {
		_ = client.Close()
		_ = server.Close()
	})
	go func() {
		_ = tls.Client(client, config).Handshake()
	}()
	if err := server.SetReadDeadline(time.Now().Add(10 * time.Second)); err != nil {
		t.Fatalf("set read deadline: %v", err)
	}
	buffer := make([]byte, 16<<10)
	n, err := server.Read(buffer)
	if err != nil {
		t.Fatalf("read client hello: %v", err)
	}
	if n <= tlsRecordHeaderBytes {
		t.Fatalf("captured only %d bytes, expected a full client hello", n)
	}
	return buffer[:n]
}

// refragmentClientHello 把一条完整的握手记录重切成多条小 record，
// 复现客户端把 ClientHello 分片发送的情况。
func refragmentClientHello(t *testing.T, record []byte, chunk int) []byte {
	t.Helper()
	if len(record) <= tlsRecordHeaderBytes {
		t.Fatalf("record is only %d bytes", len(record))
	}
	body := record[tlsRecordHeaderBytes:]
	var out []byte
	for len(body) > 0 {
		size := min(chunk, len(body))
		out = append(out, record[0], record[1], record[2], byte(size>>8), byte(size))
		out = append(out, body[:size]...)
		body = body[size:]
	}
	return out
}
