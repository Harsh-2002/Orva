package pool

import (
	"testing"
	"time"
)

func TestScalerEvaluationWaitKeepsFirstWakeImmediate(t *testing.T) {
	now := time.Now()
	if got := scalerEvaluationWait(time.Time{}, now); got != 0 {
		t.Fatalf("first wake delayed by %s", got)
	}
	if got := scalerEvaluationWait(now, now.Add(5*time.Millisecond)); got != 15*time.Millisecond {
		t.Fatalf("burst wake delay = %s, want 15ms", got)
	}
	if got := scalerEvaluationWait(now, now.Add(minScalerEvaluateInterval)); got != 0 {
		t.Fatalf("elapsed interval still delayed by %s", got)
	}
}
