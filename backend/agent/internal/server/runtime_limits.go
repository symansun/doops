package server

import (
	"os"
	"strconv"
	"strings"
	"time"
)

const (
	defaultMaxWorkspaceUploadBytes = int64(512 << 20)
	defaultWorkspaceUploadMaxAge   = 24 * time.Hour
	defaultMaxFileReadBytes        = int64(1 << 20)
	defaultMaxFileWriteBytes       = int64(16 << 20)
	defaultWorkspacePullChunkBytes = int64(512 << 10)
	defaultWebSocketMessageBytes   = int64(32 << 20)
)

func workspacePullChunkBytes() int64 {
	return positiveEnvInt64("DOOPS_WORKSPACE_PULL_CHUNK_BYTES", defaultWorkspacePullChunkBytes)
}

func maxWorkspaceUploadBytes() int64 {
	raw := strings.TrimSpace(os.Getenv("DOOPS_MAX_WORKSPACE_UPLOAD_BYTES"))
	if raw == "" {
		return defaultMaxWorkspaceUploadBytes
	}
	n, err := strconv.ParseInt(raw, 10, 64)
	if err != nil || n <= 0 {
		return defaultMaxWorkspaceUploadBytes
	}
	return n
}

func maxFileReadBytes() int64 {
	return positiveEnvInt64("DOOPS_MAX_FILE_READ_BYTES", defaultMaxFileReadBytes)
}

func maxFileWriteBytes() int64 {
	return positiveEnvInt64("DOOPS_MAX_FILE_WRITE_BYTES", defaultMaxFileWriteBytes)
}

func maxWebSocketMessageBytes() int64 {
	return positiveEnvInt64("DOOPS_MAX_WS_MESSAGE_BYTES", defaultWebSocketMessageBytes)
}

// ToolTimeoutEnv names the Agent-side limit for one tool execution. It also
// caps a Gateway timeout that the Agent is asked to honor.
const ToolTimeoutEnv = "DOOPS_TOOL_TIMEOUT"

// Both budgets below are one row of the timeout table in timeout_defaults.go
// (see backend/docs/RUNTIME_LIMITS.md). The table is resolved and range-checked
// once at startup, so these getters never read the environment: a value that
// stops parsing mid-process cannot exist, and a typo stops the process instead
// of silently falling back to the default.
func maxToolExecutionDuration() time.Duration {
	return timeouts.ToolExecutionTimeout
}

func maxCompletedTaskAge() time.Duration {
	return timeouts.CompletedTaskMaxAge
}

func positiveEnvInt64(name string, fallback int64) int64 {
	raw := strings.TrimSpace(os.Getenv(name))
	if raw == "" {
		return fallback
	}
	n, err := strconv.ParseInt(raw, 10, 64)
	if err != nil || n <= 0 {
		return fallback
	}
	return n
}
