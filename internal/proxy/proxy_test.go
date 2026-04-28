package proxy

import (
	"testing"
)

func TestNew(t *testing.T) {
	p := New()
	if p == nil {
		t.Fatal("expected non-nil proxy")
	}
}

// Integration tests for the proxy require nsjail and rootfs to be installed.
// They are covered by E2E tests via the invoke handler, not unit tests here.
