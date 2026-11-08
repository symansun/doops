package server

import (
	"encoding/binary"
	"errors"
	"net/netip"
	"strings"
	"testing"
)

func TestParseProxyProtocolV1AcceptsTCP4AndTCP6(t *testing.T) {
	cases := []struct {
		name   string
		header string
		want   string
	}{
		{"tcp4", "PROXY TCP4 203.0.113.9 192.168.150.228 54321 443\r\n", "203.0.113.9:54321"},
		{"tcp6", "PROXY TCP6 2001:db8::1 2001:db8::2 40000 443\r\n", "[2001:db8::1]:40000"},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			trailer := []byte("\x16\x03\x01")
			buffered := append([]byte(testCase.header), trailer...)
			addr, consumed, err := parseProxyProtocolHeader(buffered)
			if err != nil {
				t.Fatalf("parse header: %v", err)
			}
			if addr.String() != testCase.want {
				t.Fatalf("client address = %s, want %s", addr, testCase.want)
			}
			if consumed != len(testCase.header) {
				t.Fatalf("consumed = %d, want %d", consumed, len(testCase.header))
			}
			if string(buffered[consumed:]) != string(trailer) {
				t.Fatalf("bytes after the header = %q, want the TLS record to stay intact", buffered[consumed:])
			}
		})
	}
}

func TestParseProxyProtocolV1RejectsHeadersWithoutAClientAddress(t *testing.T) {
	cases := []struct {
		name   string
		header string
		want   string
	}{
		{"unknown", "PROXY UNKNOWN\r\n", "UNKNOWN carries no client address"},
		{"missing prefix", "GET / HTTP/1.1\r\n", "missing proxy protocol v1 prefix"},
		{"wrong field count", "PROXY TCP4 203.0.113.9 192.168.150.228 443\r\n", "expects 6 fields"},
		{"unsupported family", "PROXY TCP9 203.0.113.9 192.168.150.228 1 443\r\n", "unsupported proxy protocol v1 family"},
		{"family mismatch", "PROXY TCP4 2001:db8::1 2001:db8::2 1 443\r\n", "TCP4 carries non-IPv4 source"},
		{"bad address", "PROXY TCP4 not-an-ip 192.168.150.228 1 443\r\n", "source address"},
		{"bad port", "PROXY TCP4 203.0.113.9 192.168.150.228 99999 443\r\n", "source port"},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			_, _, err := parseProxyProtocolHeader([]byte(testCase.header))
			if err == nil {
				t.Fatalf("parse accepted %s", testCase.name)
			}
			if errors.Is(err, errEdgeProxyHeaderIncomplete) {
				t.Fatalf("parse asked for more bytes on %s, want a terminal error", testCase.name)
			}
			if !strings.Contains(err.Error(), testCase.want) {
				t.Fatalf("parse error = %v, want it to mention %q", err, testCase.want)
			}
		})
	}
}

func TestParseProxyProtocolV1DistinguishesIncompleteFromInvalid(t *testing.T) {
	if _, _, err := parseProxyProtocolHeader([]byte("PRO")); !errors.Is(err, errEdgeProxyHeaderIncomplete) {
		t.Fatalf("parse of a partial prefix: err = %v, want errEdgeProxyHeaderIncomplete", err)
	}
	if _, _, err := parseProxyProtocolHeader([]byte("PROXY TCP4 203.0.113.9 192.168")); !errors.Is(err, errEdgeProxyHeaderIncomplete) {
		t.Fatalf("parse of a header without CRLF: err = %v, want errEdgeProxyHeaderIncomplete", err)
	}
	if _, _, err := parseProxyProtocolHeader([]byte("SSH-2.0")); err == nil || errors.Is(err, errEdgeProxyHeaderIncomplete) {
		t.Fatalf("parse of non-PROXY traffic: err = %v, want a terminal error", err)
	}
	overlong := "PROXY " + strings.Repeat("x", proxyProtocolV1MaxBytes)
	_, _, err := parseProxyProtocolHeader([]byte(overlong))
	if err == nil || !strings.Contains(err.Error(), "without CRLF") {
		t.Fatalf("parse of an overlong header: err = %v, want it rejected", err)
	}
}

func TestParseProxyProtocolV2AcceptsInetAndInet6(t *testing.T) {
	inet := buildProxyProtocolV2(proxyProtocolV2CommandPrxy, proxyProtocolV2FamilyINET, proxyProtocolV2ProtoStream,
		append(append([]byte{203, 0, 113, 9}, 192, 168, 150, 228), 0xD4, 0x31, 0x01, 0xBB))
	body := make([]byte, 0, proxyProtocolV2INET6Bytes)
	body = append(body, netip.MustParseAddr("2001:db8::1").AsSlice()...)
	body = append(body, netip.MustParseAddr("2001:db8::2").AsSlice()...)
	body = append(body, 0x9C, 0x40, 0x01, 0xBB)
	inet6 := buildProxyProtocolV2(proxyProtocolV2CommandPrxy, proxyProtocolV2FamilyINET6, proxyProtocolV2ProtoStream, body)

	for _, testCase := range []struct {
		name   string
		header []byte
		want   string
	}{
		{"inet", inet, "203.0.113.9:54321"},
		{"inet6", inet6, "[2001:db8::1]:40000"},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			trailer := []byte("\x16\x03\x01")
			addr, consumed, err := parseProxyProtocolHeader(append(testCase.header, trailer...))
			if err != nil {
				t.Fatalf("parse header: %v", err)
			}
			if addr.String() != testCase.want {
				t.Fatalf("client address = %s, want %s", addr, testCase.want)
			}
			if consumed != len(testCase.header) {
				t.Fatalf("consumed = %d, want %d", consumed, len(testCase.header))
			}
		})
	}
}

func TestParseProxyProtocolV2RejectsUnusableHeaders(t *testing.T) {
	inetBody := []byte{203, 0, 113, 9, 192, 168, 150, 228, 0xD4, 0x31, 0x01, 0xBB}
	badSignature := buildProxyProtocolV2(proxyProtocolV2CommandPrxy, proxyProtocolV2FamilyINET, proxyProtocolV2ProtoStream, inetBody)
	badSignature[3] = 0x00
	badVersion := buildProxyProtocolV2(proxyProtocolV2CommandPrxy, proxyProtocolV2FamilyINET, proxyProtocolV2ProtoStream, inetBody)
	badVersion[12] = 0x31

	cases := []struct {
		name   string
		header []byte
		want   string
	}{
		{"local command", buildProxyProtocolV2(proxyProtocolV2CommandLoc, proxyProtocolV2FamilyINET, proxyProtocolV2ProtoStream, inetBody), "LOCAL carries no client address"},
		{"datagram protocol", buildProxyProtocolV2(proxyProtocolV2CommandPrxy, proxyProtocolV2FamilyINET, 0x2, inetBody), "non-stream protocol"},
		{"unix family", buildProxyProtocolV2(proxyProtocolV2CommandPrxy, 0x3, proxyProtocolV2ProtoStream, inetBody), "unsupported proxy protocol v2 address family"},
		{"short inet block", buildProxyProtocolV2(proxyProtocolV2CommandPrxy, proxyProtocolV2FamilyINET, proxyProtocolV2ProtoStream, inetBody[:6]), "INET block 6 shorter than 12"},
		{"bad signature", badSignature, "invalid proxy protocol v2 signature"},
		{"bad version", badVersion, "unsupported proxy protocol version"},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			_, _, err := parseProxyProtocolHeader(testCase.header)
			if err == nil {
				t.Fatalf("parse accepted %s", testCase.name)
			}
			if errors.Is(err, errEdgeProxyHeaderIncomplete) {
				t.Fatalf("parse asked for more bytes on %s, want a terminal error", testCase.name)
			}
			if !strings.Contains(err.Error(), testCase.want) {
				t.Fatalf("parse error = %v, want it to mention %q", err, testCase.want)
			}
		})
	}
}

func TestParseProxyProtocolV2ReportsIncompleteUntilHeaderIsWhole(t *testing.T) {
	header := buildProxyProtocolV2(proxyProtocolV2CommandPrxy, proxyProtocolV2FamilyINET, proxyProtocolV2ProtoStream,
		[]byte{203, 0, 113, 9, 192, 168, 150, 228, 0xD4, 0x31, 0x01, 0xBB})
	for size := 1; size < len(header); size++ {
		if _, _, err := parseProxyProtocolHeader(header[:size]); !errors.Is(err, errEdgeProxyHeaderIncomplete) {
			t.Fatalf("parse of first %d of %d bytes: err = %v, want errEdgeProxyHeaderIncomplete", size, len(header), err)
		}
	}
	if _, _, err := parseProxyProtocolHeader(header); err != nil {
		t.Fatalf("parse of the whole header: %v", err)
	}
}

func buildProxyProtocolV2(command, family, protocol byte, body []byte) []byte {
	header := make([]byte, 0, proxyProtocolV2HeaderBytes+len(body))
	header = append(header, proxyProtocolV2Signature...)
	header = append(header, proxyProtocolV2Version<<4|command, family<<4|protocol)
	length := make([]byte, 2)
	binary.BigEndian.PutUint16(length, uint16(len(body)))
	header = append(header, length...)
	return append(header, body...)
}
