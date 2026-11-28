package server

import (
	"fmt"
	"time"
)

// notificationSender 抽象了发送 notifications/message 的能力。
type notificationSender func(notificationEvent)

type notificationEvent struct {
	Kind   string
	Data   string
	Tool   string
	Status string
	Path   string
}

func rawNotification(text string) notificationEvent {
	return notificationEvent{Kind: "raw", Data: text}
}

func progressNotification(text string) notificationEvent {
	return notificationEvent{Kind: "progress", Data: text}
}

func assistantDeltaNotification(text string) notificationEvent {
	return notificationEvent{Kind: "assistant_delta", Data: text}
}

func toolNotification(toolName, status string) notificationEvent {
	return notificationEvent{
		Kind:   "tool",
		Data:   fmt.Sprintf("[tool:%s] %s", toolName, status),
		Tool:   toolName,
		Status: status,
	}
}

func errorNotification(text string) notificationEvent {
	return notificationEvent{Kind: "error", Data: text}
}

// slowProgressHeartbeatInterval controls how often slowProgressHeartbeat emits
// a control-plane progress heartbeat. Tests may shorten it temporarily.
var slowProgressHeartbeatInterval = 5 * time.Second

// slowProgressHeartbeat 定期发送正在执行心跳标签，使用独立的 progress 通道避免污染 raw 命令输出流。
func slowProgressHeartbeat(doneCh chan struct{}, pushProgress notificationSender) {
	ticker := time.NewTicker(slowProgressHeartbeatInterval)
	defer ticker.Stop()
	elapsed := 0
	for {
		select {
		case <-doneCh:
			return
		case <-ticker.C:
			elapsed += 5
			pushProgress(progressNotification(fmt.Sprintf("\r\033[K[agent] ⏳ 命令后台执行中... (耗时 %ds)", elapsed)))
		}
	}
}
