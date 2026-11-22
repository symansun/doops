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
	defaultToolExecutionTimeout    = 30 * time.Minute
	defaultCompletedTaskMaxAge     = 24 * time.Hour
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

// DefaultToolExecutionTimeout is the Agent-side limit when ToolTimeoutEnv is empty.
const DefaultToolExecutionTimeout = defaultToolExecutionTimeout

func maxToolExecutionDuration() time.Duration {
	return positiveEnvDuration(ToolTimeoutEnv, DefaultToolExecutionTimeout)
}

func maxCompletedTaskAge() time.Duration {
	return positiveEnvDuration("DOOPS_COMPLETED_TASK_MAX_AGE", defaultCompletedTaskMaxAge)
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

func positiveEnvDuration(name string, fallback time.Duration) time.Duration {
	raw := strings.TrimSpace(os.Getenv(name))
	if raw == "" {
		return fallback
	}
	d, err := time.ParseDuration(raw)
	if err == nil && d > 0 {
		return d
	}
	n, err := strconv.ParseInt(raw, 10, 64)
	if err != nil || n <= 0 {
		return fallback
	}
	return time.Duration(n) * time.Second
}
