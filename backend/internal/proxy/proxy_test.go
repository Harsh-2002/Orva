package proxy

import (
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/Harsh-2002/Orva/backend/internal/database"
)

func TestNew(t *testing.T) {
	p := New()
	if p == nil {
		t.Fatal("expected non-nil proxy")
	}
}

func TestStreamingConfigCachesAndRefreshes(t *testing.T) {
	db, err := database.New(filepath.Join(t.TempDir(), "proxy.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if err := db.Migrate(); err != nil {
		t.Fatal(err)
	}
	p := &Proxy{DB: db}
	if enabled, keepalive := p.streamingConfig(); enabled != 1 || keepalive != 15 {
		t.Fatalf("defaults = %d/%d", enabled, keepalive)
	}
	if err := db.SetSystemConfig("streaming_enabled", "0"); err != nil {
		t.Fatal(err)
	}
	if err := db.SetSystemConfig("stream_keepalive_seconds", "9"); err != nil {
		t.Fatal(err)
	}
	if enabled, keepalive := p.streamingConfig(); enabled != 1 || keepalive != 15 {
		t.Fatalf("unexpired cache = %d/%d", enabled, keepalive)
	}
	p.streamSettings.Store(&streamSettings{enabled: 1, keepaliveSeconds: 15, expiresAt: time.Now().Add(-time.Second)})
	const clients = 40
	var wg sync.WaitGroup
	for range clients {
		wg.Add(1)
		go func() {
			defer wg.Done()
			enabled, keepalive := p.streamingConfig()
			if !(enabled == 1 && keepalive == 15 || enabled == 0 && keepalive == 9) {
				t.Errorf("inconsistent config = %d/%d", enabled, keepalive)
			}
		}()
	}
	wg.Wait()
	if enabled, keepalive := p.streamingConfig(); enabled != 0 || keepalive != 9 {
		t.Fatalf("refreshed config = %d/%d", enabled, keepalive)
	}
}

// Integration tests for the proxy require nsjail and rootfs to be installed.
// They are covered by E2E tests via the invoke handler, not unit tests here.
