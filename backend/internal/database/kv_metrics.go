package database

import (
	"context"
	"errors"
	"sync/atomic"
	"time"
)

var kvOperationNames = [...]string{"get", "put", "delete", "list", "incr", "cas", "batch"}

type kvOperationMetric struct {
	count     atomic.Uint64
	errors    atomic.Uint64
	timeouts  atomic.Uint64
	latencyNS atomic.Uint64
}

type kvMetrics struct {
	operations [len(kvOperationNames)]kvOperationMetric
	rollbacks  atomic.Uint64
}

type KVOperationStats struct {
	Operation      string
	Count          uint64
	Errors         uint64
	Timeouts       uint64
	LatencyTotalNS uint64
}

type KVMetricsSnapshot struct {
	Operations []KVOperationStats
	Rollbacks  uint64
}

func kvOperationIndex(operation string) int {
	for i, name := range kvOperationNames {
		if name == operation {
			return i
		}
	}
	return -1
}

func (db *Database) observeKV(operation string, started time.Time, err error) {
	i := kvOperationIndex(operation)
	if i < 0 {
		return
	}
	metric := &db.kv.operations[i]
	metric.count.Add(1)
	metric.latencyNS.Add(uint64(time.Since(started)))
	if err != nil && !errors.Is(err, ErrKVNotFound) {
		metric.errors.Add(1)
	}
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		metric.timeouts.Add(1)
	}
}

func (db *Database) KVMetrics() KVMetricsSnapshot {
	out := KVMetricsSnapshot{Operations: make([]KVOperationStats, 0, len(kvOperationNames)), Rollbacks: db.kv.rollbacks.Load()}
	for i, name := range kvOperationNames {
		metric := &db.kv.operations[i]
		out.Operations = append(out.Operations, KVOperationStats{
			Operation: name, Count: metric.count.Load(), Errors: metric.errors.Load(),
			Timeouts: metric.timeouts.Load(), LatencyTotalNS: metric.latencyNS.Load(),
		})
	}
	return out
}
