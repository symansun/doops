package server

import (
	"encoding/binary"
	"errors"
	"fmt"
	"math"
	"strings"
)

// Edge 隧道帧格式（docs/design/DOOPS_EDGE_TUNNEL.md §4.2）：
//
//	+--------+--------+---------------------+----------------+
//	| type   | flags  | conn_id (uint32 BE) | payload (0..N) |
//	| 1 byte | 1 byte | 4 bytes             | 变长           |
//	+--------+--------+---------------------+----------------+
//
// 一个 WebSocket 二进制消息承载且仅承载一帧。

const (
	defaultEdgeFrameBytes = 32 << 10
	edgeFrameHeaderBytes  = 6
	edgeOpenHeaderBytes   = 4
)

// edgeControlConnID 保留给隧道级控制帧（ROUTES / STATS），永不分配给数据连接。
const edgeControlConnID uint32 = 0

func edgeFramePayloadBytes() int64 {
	return positiveEnvInt64("DOOPS_EDGE_FRAME_BYTES", defaultEdgeFrameBytes)
}

type edgeFrameType uint8

const (
	edgeFrameOpen     edgeFrameType = 0x01
	edgeFrameOpenAck  edgeFrameType = 0x02
	edgeFrameData     edgeFrameType = 0x03
	edgeFrameShutdown edgeFrameType = 0x04
	edgeFrameClose    edgeFrameType = 0x05
	edgeFramePause    edgeFrameType = 0x06
	edgeFrameResume   edgeFrameType = 0x07
	edgeFrameRoutes   edgeFrameType = 0x10
	edgeFrameStats    edgeFrameType = 0x11
)

func (t edgeFrameType) String() string {
	switch t {
	case edgeFrameOpen:
		return "OPEN"
	case edgeFrameOpenAck:
		return "OPEN_ACK"
	case edgeFrameData:
		return "DATA"
	case edgeFrameShutdown:
		return "SHUTDOWN"
	case edgeFrameClose:
		return "CLOSE"
	case edgeFramePause:
		return "PAUSE"
	case edgeFrameResume:
		return "RESUME"
	case edgeFrameRoutes:
		return "ROUTES"
	case edgeFrameStats:
		return "STATS"
	}
	return fmt.Sprintf("UNKNOWN(0x%02x)", uint8(t))
}

type edgeCloseReason uint8

const (
	edgeCloseNormal          edgeCloseReason = 0
	edgeCloseBackendRefused  edgeCloseReason = 1
	edgeCloseBackendTimeout  edgeCloseReason = 2
	edgeCloseRouteForbidden  edgeCloseReason = 3
	edgeCloseQuotaExceeded   edgeCloseReason = 4
	edgeCloseUpstreamFailure edgeCloseReason = 5
	edgeCloseTunnelOverload  edgeCloseReason = 6
)

func (r edgeCloseReason) String() string {
	switch r {
	case edgeCloseNormal:
		return "normal"
	case edgeCloseBackendRefused:
		return "backend-refused"
	case edgeCloseBackendTimeout:
		return "backend-timeout"
	case edgeCloseRouteForbidden:
		return "route-forbidden"
	case edgeCloseQuotaExceeded:
		return "quota-exceeded"
	case edgeCloseUpstreamFailure:
		return "upstream-failure"
	case edgeCloseTunnelOverload:
		return "tunnel-overload"
	}
	return fmt.Sprintf("unknown(%d)", uint8(r))
}

var errEdgeFrameTruncated = errors.New("edge: frame shorter than 6-byte header")

type edgeFrame struct {
	Type    edgeFrameType
	Flags   uint8
	ConnID  uint32
	Payload []byte
}

func (f edgeFrame) appendTo(dst []byte) []byte {
	var header [edgeFrameHeaderBytes]byte
	header[0] = byte(f.Type)
	header[1] = f.Flags
	binary.BigEndian.PutUint32(header[2:], f.ConnID)
	dst = append(dst, header[:]...)
	return append(dst, f.Payload...)
}

func (f edgeFrame) encode() ([]byte, error) {
	if err := f.validate(); err != nil {
		return nil, err
	}
	return f.appendTo(make([]byte, 0, edgeFrameHeaderBytes+len(f.Payload))), nil
}

// decodeEdgeFrame 解析一个 WebSocket 二进制消息。返回的 Payload 与 message 共享底层
// 数组，调用方在复用或释放 message 之前必须自行拷贝。
func decodeEdgeFrame(message []byte) (edgeFrame, error) {
	if len(message) < edgeFrameHeaderBytes {
		return edgeFrame{}, errEdgeFrameTruncated
	}
	frame := edgeFrame{
		Type:   edgeFrameType(message[0]),
		Flags:  message[1],
		ConnID: binary.BigEndian.Uint32(message[2:edgeFrameHeaderBytes]),
	}
	if payload := message[edgeFrameHeaderBytes:]; len(payload) > 0 {
		frame.Payload = payload
	}
	if err := frame.validate(); err != nil {
		return edgeFrame{}, err
	}
	return frame, nil
}

func (f edgeFrame) validate() error {
	if f.Flags != 0 {
		return fmt.Errorf("edge: reserved flags must be zero, got 0x%02x", f.Flags)
	}
	if limit := int(edgeFramePayloadBytes()); len(f.Payload) > limit {
		return fmt.Errorf("edge: %s payload %d exceeds limit %d", f.Type, len(f.Payload), limit)
	}
	switch f.Type {
	case edgeFrameOpen:
		if err := f.requireDataConnID(); err != nil {
			return err
		}
		if len(f.Payload) < edgeOpenHeaderBytes {
			return fmt.Errorf("edge: OPEN payload %d shorter than %d", len(f.Payload), edgeOpenHeaderBytes)
		}
	case edgeFrameData:
		if err := f.requireDataConnID(); err != nil {
			return err
		}
		if len(f.Payload) == 0 {
			return errors.New("edge: DATA frame carries no payload")
		}
	case edgeFrameOpenAck, edgeFrameShutdown, edgeFramePause, edgeFrameResume:
		if err := f.requireDataConnID(); err != nil {
			return err
		}
		if len(f.Payload) != 0 {
			return fmt.Errorf("edge: %s frame must carry no payload, got %d bytes", f.Type, len(f.Payload))
		}
	case edgeFrameClose:
		if err := f.requireDataConnID(); err != nil {
			return err
		}
		if len(f.Payload) == 0 {
			return errors.New("edge: CLOSE frame missing reason code")
		}
	case edgeFrameRoutes, edgeFrameStats:
		if f.ConnID != edgeControlConnID {
			return fmt.Errorf("edge: %s frame must use control conn_id 0, got %d", f.Type, f.ConnID)
		}
		if len(f.Payload) == 0 {
			return fmt.Errorf("edge: %s frame missing payload", f.Type)
		}
	default:
		return fmt.Errorf("edge: unknown frame type 0x%02x", uint8(f.Type))
	}
	return nil
}

func (f edgeFrame) requireDataConnID() error {
	if f.ConnID == edgeControlConnID {
		return fmt.Errorf("edge: %s frame requires a non-zero conn_id", f.Type)
	}
	return nil
}

// isPriority 的帧走调度器控制队列，抢在任意 DATA 之前。
// SHUTDOWN 不在其中：它必须排在本连接剩余 DATA 之后，否则等于先发 FIN。
func (f edgeFrame) isPriority() bool {
	switch f.Type {
	case edgeFrameOpen, edgeFrameOpenAck, edgeFrameClose, edgeFramePause, edgeFrameResume, edgeFrameRoutes, edgeFrameStats:
		return true
	default:
		return false
	}
}

func encodeEdgeOpenPayload(routeID int64, serverName string) ([]byte, error) {
	if routeID <= 0 || routeID > math.MaxUint32 {
		return nil, fmt.Errorf("edge: route id %d out of uint32 range", routeID)
	}
	name := strings.TrimSpace(serverName)
	if name == "" {
		return nil, errors.New("edge: OPEN requires a server name")
	}
	payload := make([]byte, edgeOpenHeaderBytes, edgeOpenHeaderBytes+len(name))
	binary.BigEndian.PutUint32(payload, uint32(routeID))
	return append(payload, name...), nil
}

func decodeEdgeOpenPayload(payload []byte) (int64, string, error) {
	if len(payload) < edgeOpenHeaderBytes {
		return 0, "", fmt.Errorf("edge: OPEN payload %d shorter than %d", len(payload), edgeOpenHeaderBytes)
	}
	routeID := int64(binary.BigEndian.Uint32(payload[:edgeOpenHeaderBytes]))
	if routeID == 0 {
		return 0, "", errors.New("edge: OPEN carries route id 0")
	}
	name := strings.TrimSpace(string(payload[edgeOpenHeaderBytes:]))
	if name == "" {
		return 0, "", errors.New("edge: OPEN carries an empty server name")
	}
	return routeID, name, nil
}

func encodeEdgeClosePayload(reason edgeCloseReason, detail string) []byte {
	trimmed := strings.TrimSpace(detail)
	payload := make([]byte, 1, 1+len(trimmed))
	payload[0] = byte(reason)
	return append(payload, trimmed...)
}

func decodeEdgeClosePayload(payload []byte) (edgeCloseReason, string, error) {
	if len(payload) == 0 {
		return 0, "", errors.New("edge: CLOSE payload missing reason code")
	}
	return edgeCloseReason(payload[0]), string(payload[1:]), nil
}
