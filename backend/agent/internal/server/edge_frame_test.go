package server

import (
	"bytes"
	"errors"
	"strings"
	"testing"
)

func TestEdgeFrameRoundTripPreservesHeaderAndPayload(t *testing.T) {
	cases := []struct {
		name  string
		frame edgeFrame
	}{
		{"open", edgeFrame{Type: edgeFrameOpen, ConnID: 7, Payload: mustEncodeEdgeOpen(t, 12, "amp.jm.aiedulab.cn")}},
		{"open ack", edgeFrame{Type: edgeFrameOpenAck, ConnID: 7}},
		{"data", edgeFrame{Type: edgeFrameData, ConnID: 4294967295, Payload: bytes.Repeat([]byte{0xAB}, 1024)}},
		{"shutdown", edgeFrame{Type: edgeFrameShutdown, ConnID: 1}},
		{"close", edgeFrame{Type: edgeFrameClose, ConnID: 2, Payload: encodeEdgeClosePayload(edgeCloseBackendRefused, "dial tcp: refused")}},
		{"pause", edgeFrame{Type: edgeFramePause, ConnID: 3}},
		{"resume", edgeFrame{Type: edgeFrameResume, ConnID: 3}},
		{"routes", edgeFrame{Type: edgeFrameRoutes, ConnID: edgeControlConnID, Payload: []byte(`{"version":9}`)}},
		{"stats", edgeFrame{Type: edgeFrameStats, ConnID: edgeControlConnID, Payload: []byte(`{"conns":3}`)}},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			encoded, err := testCase.frame.encode()
			if err != nil {
				t.Fatalf("encode %s: %v", testCase.name, err)
			}
			if len(encoded) != edgeFrameHeaderBytes+len(testCase.frame.Payload) {
				t.Fatalf("encoded length = %d, want %d", len(encoded), edgeFrameHeaderBytes+len(testCase.frame.Payload))
			}
			decoded, err := decodeEdgeFrame(encoded)
			if err != nil {
				t.Fatalf("decode %s: %v", testCase.name, err)
			}
			if decoded.Type != testCase.frame.Type || decoded.ConnID != testCase.frame.ConnID || decoded.Flags != 0 {
				t.Fatalf("decoded header = %+v, want type %s conn %d", decoded, testCase.frame.Type, testCase.frame.ConnID)
			}
			if !bytes.Equal(decoded.Payload, testCase.frame.Payload) {
				t.Fatalf("decoded payload = %x, want %x", decoded.Payload, testCase.frame.Payload)
			}
		})
	}
}

func TestDecodeEdgeFrameRejectsMalformedMessages(t *testing.T) {
	t.Setenv("DOOPS_EDGE_FRAME_BYTES", "64")
	cases := []struct {
		name    string
		message []byte
		want    string
	}{
		{"truncated header", []byte{0x03, 0x00, 0x00, 0x00, 0x01}, "shorter than 6-byte header"},
		{"empty", nil, "shorter than 6-byte header"},
		{"unknown type", []byte{0x7f, 0x00, 0x00, 0x00, 0x00, 0x01, 0x41}, "unknown frame type 0x7f"},
		{"reserved flags", []byte{0x03, 0x01, 0x00, 0x00, 0x00, 0x01, 0x41}, "reserved flags must be zero"},
		{"data without payload", []byte{0x03, 0x00, 0x00, 0x00, 0x00, 0x01}, "DATA frame carries no payload"},
		{"data on control conn", []byte{0x03, 0x00, 0x00, 0x00, 0x00, 0x00, 0x41}, "DATA frame requires a non-zero conn_id"},
		{"open ack with payload", []byte{0x02, 0x00, 0x00, 0x00, 0x00, 0x01, 0x41}, "OPEN_ACK frame must carry no payload"},
		{"close without reason", []byte{0x05, 0x00, 0x00, 0x00, 0x00, 0x01}, "CLOSE frame missing reason code"},
		{"open too short", []byte{0x01, 0x00, 0x00, 0x00, 0x00, 0x01, 0x00, 0x00}, "OPEN payload 2 shorter than 4"},
		{"routes on data conn", append([]byte{0x10, 0x00, 0x00, 0x00, 0x00, 0x01}, []byte("{}")...), "ROUTES frame must use control conn_id 0"},
		{"routes without payload", []byte{0x10, 0x00, 0x00, 0x00, 0x00, 0x00}, "ROUTES frame missing payload"},
		{"payload over limit", append([]byte{0x03, 0x00, 0x00, 0x00, 0x00, 0x01}, bytes.Repeat([]byte{0x41}, 65)...), "payload 65 exceeds limit 64"},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			if _, err := decodeEdgeFrame(testCase.message); err == nil {
				t.Fatalf("decode accepted %s", testCase.name)
			} else if !strings.Contains(err.Error(), testCase.want) {
				t.Fatalf("decode error = %v, want it to mention %q", err, testCase.want)
			}
		})
	}
}

func TestDecodeEdgeFrameReportsTruncationSentinel(t *testing.T) {
	if _, err := decodeEdgeFrame([]byte{0x03}); !errors.Is(err, errEdgeFrameTruncated) {
		t.Fatalf("decode error = %v, want errEdgeFrameTruncated", err)
	}
}

func TestEdgeFrameRejectsPayloadOverLimitOnEncode(t *testing.T) {
	t.Setenv("DOOPS_EDGE_FRAME_BYTES", "16")
	frame := edgeFrame{Type: edgeFrameData, ConnID: 1, Payload: bytes.Repeat([]byte{0x41}, 17)}
	if _, err := frame.encode(); err == nil {
		t.Fatal("encode accepted a payload above the configured limit")
	}
}

func TestEdgeOpenPayloadRoundTrip(t *testing.T) {
	payload, err := encodeEdgeOpenPayload(4096, "amp.jm.aiedulab.cn")
	if err != nil {
		t.Fatalf("encode open payload: %v", err)
	}
	routeID, serverName, err := decodeEdgeOpenPayload(payload)
	if err != nil {
		t.Fatalf("decode open payload: %v", err)
	}
	if routeID != 4096 {
		t.Fatalf("route id = %d, want 4096", routeID)
	}
	if serverName != "amp.jm.aiedulab.cn" {
		t.Fatalf("server name = %q, want amp.jm.aiedulab.cn", serverName)
	}
}

func TestEdgeOpenPayloadRejectsInvalidInput(t *testing.T) {
	if _, err := encodeEdgeOpenPayload(0, "amp.jm.aiedulab.cn"); err == nil {
		t.Fatal("encode accepted route id 0")
	}
	if _, err := encodeEdgeOpenPayload(1<<32, "amp.jm.aiedulab.cn"); err == nil {
		t.Fatal("encode accepted a route id above uint32")
	}
	if _, err := encodeEdgeOpenPayload(1, "   "); err == nil {
		t.Fatal("encode accepted a blank server name")
	}
	if _, _, err := decodeEdgeOpenPayload([]byte{0x00, 0x00, 0x00, 0x01}); err == nil {
		t.Fatal("decode accepted an OPEN payload without a server name")
	}
	if _, _, err := decodeEdgeOpenPayload([]byte{0x00, 0x00, 0x00, 0x00, 'a'}); err == nil {
		t.Fatal("decode accepted route id 0")
	}
}

func TestEdgeClosePayloadRoundTrip(t *testing.T) {
	reason, detail, err := decodeEdgeClosePayload(encodeEdgeClosePayload(edgeCloseRouteForbidden, "backend not allowed"))
	if err != nil {
		t.Fatalf("decode close payload: %v", err)
	}
	if reason != edgeCloseRouteForbidden {
		t.Fatalf("reason = %s, want %s", reason, edgeCloseRouteForbidden)
	}
	if detail != "backend not allowed" {
		t.Fatalf("detail = %q, want %q", detail, "backend not allowed")
	}
	if _, _, err := decodeEdgeClosePayload(nil); err == nil {
		t.Fatal("decode accepted an empty CLOSE payload")
	}
}

func mustEncodeEdgeOpen(t *testing.T, routeID int64, serverName string) []byte {
	t.Helper()
	payload, err := encodeEdgeOpenPayload(routeID, serverName)
	if err != nil {
		t.Fatalf("encode open payload: %v", err)
	}
	return payload
}
