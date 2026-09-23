package handlers

import (
	"net/http"
	"testing"

	"github.com/Harsh-2002/Orva/backend/internal/pool"
)

func TestInvocationAdmissionOverloadMapsToRetryable429(t *testing.T) {
	status, opts := invokeError(pool.ErrInvocationQueueFull, nil, "request-1")
	if status != http.StatusTooManyRequests {
		t.Fatalf("status = %d, want 429", status)
	}
	if opts.Code != "INVOCATION_QUEUE_FULL" || opts.RetryAfterS != 1 {
		t.Fatalf("error envelope = %#v", opts)
	}
}
