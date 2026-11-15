package sandbox

import (
	"fmt"
	"io"
	"sort"
	"sync/atomic"
	"time"
)

// Counters for the SDK channel. The metrics package has no labelled counters,
// so these are plain atomics rendered in Prometheus text by WriteSDKMetrics.
var (
	sdkOpenChannels       atomic.Int64
	sdkProtocolViolations atomic.Uint64
	sdkWriteFailures      atomic.Uint64
	sdkOpStats            = newSDKOpStats()
)

// status classes keep label cardinality bounded: 14 ops x 4 classes.
var sdkStatusClasses = [...]string{"ok", "4xx", "5xx", "other"}

type sdkOpStat struct {
	calls   [len(sdkStatusClasses)]atomic.Uint64
	nanos   atomic.Uint64
	latency atomic.Uint64 // number of timed calls
}

func newSDKOpStats() map[string]*sdkOpStat {
	m := make(map[string]*sdkOpStat, len(sdkOps)+1)
	for op := range sdkOps {
		m[op] = &sdkOpStat{}
	}
	m["unknown"] = &sdkOpStat{}
	return m
}

func sdkStatusClass(status int) int {
	switch {
	case status >= 200 && status < 300:
		return 0
	case status >= 400 && status < 500:
		return 1
	case status >= 500 && status < 600:
		return 2
	}
	return 3
}

// sdkRecord counts one call. d == 0 means "answered before the handler ran"
// (refusals), which contributes to the count but not to the latency sum.
func sdkRecord(op string, status int, d time.Duration) {
	st := sdkOpStats[op]
	if st == nil {
		st = sdkOpStats["unknown"]
	}
	st.calls[sdkStatusClass(status)].Add(1)
	if d > 0 {
		st.nanos.Add(uint64(d))
		st.latency.Add(1)
	}
}

// SDKOpenChannels reports the number of SDK channels currently served.
func SDKOpenChannels() int64 { return sdkOpenChannels.Load() }

// WriteSDKMetrics renders the SDK channel metrics in Prometheus text format.
func WriteSDKMetrics(w io.Writer) {
	fmt.Fprint(w, "# HELP orva_sdk_calls_total SDK calls by transport, op and status class.\n# TYPE orva_sdk_calls_total counter\n")
	ops := make([]string, 0, len(sdkOpStats))
	for op := range sdkOpStats {
		ops = append(ops, op)
	}
	sort.Strings(ops)
	for _, op := range ops {
		st := sdkOpStats[op]
		for i, class := range sdkStatusClasses {
			if n := st.calls[i].Load(); n > 0 {
				fmt.Fprintf(w, "orva_sdk_calls_total{transport=\"channel\",op=%q,status=%q} %d\n", op, class, n)
			}
		}
	}
	fmt.Fprint(w, "# HELP orva_sdk_call_duration_ms_sum Time spent in SDK handlers, in milliseconds.\n# TYPE orva_sdk_call_duration_ms_sum counter\n")
	for _, op := range ops {
		st := sdkOpStats[op]
		if n := st.latency.Load(); n > 0 {
			fmt.Fprintf(w, "orva_sdk_call_duration_ms_sum{transport=\"channel\",op=%q} %.3f\n", op, float64(st.nanos.Load())/1e6)
			fmt.Fprintf(w, "orva_sdk_call_duration_ms_count{transport=\"channel\",op=%q} %d\n", op, n)
		}
	}
	fmt.Fprintf(w, "# HELP orva_sdk_channels_open Open worker SDK channels.\n# TYPE orva_sdk_channels_open gauge\norva_sdk_channels_open %d\n", sdkOpenChannels.Load())
	fmt.Fprintf(w, "# HELP orva_sdk_protocol_violations_total Channels closed for a protocol violation.\n# TYPE orva_sdk_protocol_violations_total counter\norva_sdk_protocol_violations_total %d\n", sdkProtocolViolations.Load())
	fmt.Fprintf(w, "# HELP orva_sdk_write_failures_total Workers killed because the sandbox stopped reading SDK replies.\n# TYPE orva_sdk_write_failures_total counter\norva_sdk_write_failures_total %d\n", sdkWriteFailures.Load())
}
