package builder

import (
	"sync"
	"testing"
)

// capturingLogger records which job's logger received which line.
type capturingLogger struct {
	mu    sync.Mutex
	job   string
	lines []string
}

func (c *capturingLogger) Append(stream, line string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.lines = append(c.lines, line)
}

// TestConcurrentBuildsDoNotShareTheLogger pins the fix for the shared-*Builder
// race: Logger used to be set on the queue's single *Builder, so concurrent
// workers cross-attributed output and the first defer to fire nil'd the logger
// out from under every build still running.
//
// Run with -race: writing Logger on a shared Builder while another goroutine's
// Build reads it is a data race, not merely a mix-up.
func TestConcurrentBuildsDoNotShareTheLogger(t *testing.T) {
	shared := &Builder{DataDir: t.TempDir()}

	const n = 8
	var wg sync.WaitGroup
	loggers := make([]*capturingLogger, n)
	release := make(chan struct{})

	for i := 0; i < n; i++ {
		loggers[i] = &capturingLogger{job: string(rune('a' + i))}
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			// Exactly what the queue worker does now: copy, then set.
			bld := *shared
			bld.Logger = loggers[i]
			<-release // maximise overlap
			// logLines is the real consumer of b.Logger.
			logLines(&bld, "stdout", []byte(loggers[i].job))
		}(i)
	}
	close(release)
	wg.Wait()

	// Every logger must have received its own line and nothing else.
	for i, l := range loggers {
		if len(l.lines) != 1 || l.lines[0] != l.job {
			t.Errorf("logger %d got %v, want exactly [%q]", i, l.lines, l.job)
		}
	}
	if shared.Logger != nil {
		t.Errorf("the shared Builder was mutated: Logger = %v", shared.Logger)
	}
}
