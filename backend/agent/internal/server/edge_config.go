package server

import "time"

const (
	defaultEdgeConnQueue      = 8
	defaultEdgeQuantum        = 2
	defaultEdgeMemBudgetBytes = 512 << 20
	defaultEdgeMaxConns       = 4096
	defaultEdgeMaxConnsPerIP  = 64
	defaultEdgeMaxSessions    = 8
	defaultEdgeMaxAgentConns  = 512
	edgeBudgetPauseRatio      = 0.75
	edgeBudgetResumeRatio     = 0.40
	edgeOpeningAckSlack       = 1 * time.Second
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

// The three Edge budgets below are rows of the timeout table in
// timeout_defaults.go (DOOPS_EDGE_WRITE_STALL, DOOPS_EDGE_CONN_ID_QUARANTINE,
// DOOPS_EDGE_HELLO_TIMEOUT). They used to be read from the environment on every
// call with a silent fallback; the table now refuses an invalid value at
// startup, so these only read the resolved snapshot.
func edgeWriteStall() time.Duration {
	return timeouts.EdgeWriteStall
}

func edgeConnIDQuarantine() time.Duration {
	return timeouts.EdgeConnIDQuarantine
}

func edgeHelloTimeout() time.Duration {
	return timeouts.EdgeHelloTimeout
}

func edgeMaxConns() int64 {
	return positiveEnvInt64("DOOPS_EDGE_MAX_CONNS", defaultEdgeMaxConns)
}

func edgeMaxConnsPerIP() int64 {
	return positiveEnvInt64("DOOPS_EDGE_MAX_CONNS_PER_IP", defaultEdgeMaxConnsPerIP)
}
