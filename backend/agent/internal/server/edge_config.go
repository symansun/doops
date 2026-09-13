package server

import "time"

const (
	defaultEdgeConnQueue        = 8
	defaultEdgeQuantum          = 2
	defaultEdgeMemBudgetBytes   = 512 << 20
	defaultEdgeWriteStall       = 30 * time.Second
	defaultEdgeConnIDQuarantine = 30 * time.Second
	defaultEdgeDialTimeout      = 5 * time.Second
	defaultEdgeHelloTimeout     = 5 * time.Second
	defaultEdgeMaxConns         = 4096
	defaultEdgeMaxConnsPerIP    = 64
	defaultEdgeMaxSessions      = 8
	defaultEdgeMaxAgentConns    = 512
	edgeBudgetPauseRatio        = 0.75
	edgeBudgetResumeRatio       = 0.40
	edgeOpeningAckSlack         = 1 * time.Second
)

func edgeConnQueueSize() int {
	return int(positiveEnvInt64("DOOPS_EDGE_CONN_QUEUE", defaultEdgeConnQueue))
}

func edgeSchedQuantum() int {
	return int(positiveEnvInt64("DOOPS_EDGE_QUANTUM", defaultEdgeQuantum))
}

func edgeMemBudgetBytes() int64 {
	return positiveEnvInt64("DOOPS_EDGE_MEM_BUDGET", defaultEdgeMemBudgetBytes)
}

func edgeWriteStall() time.Duration {
	return positiveEnvDuration("DOOPS_EDGE_WRITE_STALL", defaultEdgeWriteStall)
}

func edgeConnIDQuarantine() time.Duration {
	return positiveEnvDuration("DOOPS_EDGE_CONN_ID_QUARANTINE", defaultEdgeConnIDQuarantine)
}

func edgeHelloTimeout() time.Duration {
	return positiveEnvDuration("DOOPS_EDGE_HELLO_TIMEOUT", defaultEdgeHelloTimeout)
}

func edgeMaxConns() int64 {
	return positiveEnvInt64("DOOPS_EDGE_MAX_CONNS", defaultEdgeMaxConns)
}

func edgeMaxConnsPerIP() int64 {
	return positiveEnvInt64("DOOPS_EDGE_MAX_CONNS_PER_IP", defaultEdgeMaxConnsPerIP)
}
