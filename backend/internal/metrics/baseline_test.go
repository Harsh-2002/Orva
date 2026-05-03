package metrics

import (
	"sync"
	"testing"
)

func TestBaseline_EmptyReturnsZero(t *testing.T) {
	b := NewBaselines()
	p95, p99, mean, n := b.Snapshot("fn_x")
	if p95 != 0 || p99 != 0 || mean != 0 || n != 0 {
		t.Fatalf("empty buffer should be zero; got p95=%v p99=%v mean=%v n=%d", p95, p99, mean, n)
	}
}

func TestBaseline_SingleSampleIsP95(t *testing.T) {
	b := NewBaselines()
	b.Record("fn_x", 100)
	p95, _, _, n := b.Snapshot("fn_x")
	if n != 1 {
		t.Fatalf("n=1 expected, got %d", n)
	}
	if p95 != 100 {
		t.Fatalf("p95=100 expected, got %v", p95)
	}
}

func TestBaseline_PercentileMatchesManualSort(t *testing.T) {
	b := NewBaselines()
	// Insert 1..100. Sorted, P95 = element at index int((100-1)*0.95) = 94 → value 95.
	for i := 1; i <= 100; i++ {
		b.Record("fn_x", int64(i))
	}
	p95, p99, _, n := b.Snapshot("fn_x")
	if n != 100 {
		t.Fatalf("n=100 expected, got %d", n)
	}
	if p95 != 95 {
		t.Errorf("p95=95 expected, got %v", p95)
	}
	if p99 != 99 {
		t.Errorf("p99=99 expected, got %v", p99)
	}
}

func TestBaseline_OutlierBelowSampleMin(t *testing.T) {
	b := NewBaselines()
	// Below baselineMinForOutlier — never an outlier even if duration is huge.
	for i := 1; i < baselineMinForOutlier; i++ {
		b.Record("fn_x", 50)
	}
	isOutlier, _ := b.Classify("fn_x", 9999)
	if isOutlier {
		t.Fatalf("expected no outlier flag below %d samples", baselineMinForOutlier)
	}
}

func TestBaseline_OutlierAboveThreshold(t *testing.T) {
	b := NewBaselines()
	for i := 0; i < baselineMinForOutlier; i++ {
		b.Record("fn_x", 50)
	}
	// p95 ≈ 50; 2× = 100; 150 should fire.
	isOutlier, p95 := b.Classify("fn_x", 150)
	if !isOutlier {
		t.Fatalf("expected outlier flag (p95=%d, dur=150)", p95)
	}
	if p95 == 0 {
		t.Fatalf("baseline_p95 should be returned even when classifying")
	}
	// Below threshold should not fire.
	if isOutlier2, _ := b.Classify("fn_x", 80); isOutlier2 {
		t.Errorf("80ms should not be outlier when p95=50, threshold=100")
	}
}

func TestBaseline_FIFOEvictsOldest(t *testing.T) {
	b := NewBaselines()
	// 200 samples means the first 100 should be evicted; only 101..200 remain.
	for i := 1; i <= 200; i++ {
		b.Record("fn_x", int64(i))
	}
	_, _, mean, n := b.Snapshot("fn_x")
	if n != baselineSamples {
		t.Fatalf("expected n=%d after 200 records, got %d", baselineSamples, n)
	}
	// mean of 101..200 = 150.5
	if mean < 150 || mean > 151 {
		t.Errorf("expected mean ≈150.5 (eviction kept newest), got %v", mean)
	}
}

func TestBaseline_ConcurrentRecord(t *testing.T) {
	b := NewBaselines()
	const goroutines = 100
	const perG = 20
	var wg sync.WaitGroup
	for g := 0; g < goroutines; g++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; i < perG; i++ {
				b.Record("fn_x", 50)
			}
		}()
	}
	wg.Wait()
	_, _, _, n := b.Snapshot("fn_x")
	if n != baselineSamples {
		t.Fatalf("expected fully-filled buffer (%d), got %d", baselineSamples, n)
	}
}

type fakeUpdater struct {
	calls []string
}

func (f *fakeUpdater) UpdateOutlier(execID string, isOutlier bool, baselineP95MS int64) {
	tag := "ok"
	if isOutlier {
		tag = "outlier"
	}
	f.calls = append(f.calls, execID+":"+tag)
}

func TestFinalizeExecution_ColdStartSkipsBaseline(t *testing.T) {
	b := NewBaselines()
	upd := &fakeUpdater{}
	// Cold-start success — should NOT feed the baseline. Without warm
	// samples, classification returns no-outlier, no back-write.
	b.FinalizeExecution(upd, "exec_1", "fn_x", "success", true, 100)
	_, _, _, n := b.Snapshot("fn_x")
	if n != 0 {
		t.Errorf("cold start fed the baseline, n=%d", n)
	}
}

func TestFinalizeExecution_ErrorSkipsBaseline(t *testing.T) {
	b := NewBaselines()
	upd := &fakeUpdater{}
	b.FinalizeExecution(upd, "exec_1", "fn_x", "error", false, 50)
	_, _, _, n := b.Snapshot("fn_x")
	if n != 0 {
		t.Errorf("error fed the baseline, n=%d", n)
	}
}

func TestFinalizeExecution_WarmSuccessFeeds(t *testing.T) {
	b := NewBaselines()
	upd := &fakeUpdater{}
	for i := 0; i < baselineMinForOutlier; i++ {
		b.FinalizeExecution(upd, "warm_x", "fn_x", "success", false, 50)
	}
	_, _, _, n := b.Snapshot("fn_x")
	if n != baselineMinForOutlier {
		t.Fatalf("expected %d samples, got %d", baselineMinForOutlier, n)
	}
	// Now an outlier feeds the baseline AND triggers a back-write.
	upd.calls = nil
	b.FinalizeExecution(upd, "exec_outlier", "fn_x", "success", false, 9999)
	if len(upd.calls) == 0 {
		t.Fatalf("expected back-write call")
	}
	if upd.calls[0] != "exec_outlier:outlier" {
		t.Errorf("expected outlier back-write, got %v", upd.calls)
	}
}
