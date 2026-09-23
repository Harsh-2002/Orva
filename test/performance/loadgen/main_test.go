package main

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"
)

func TestClosedLoopSeparatesResponseClasses(t *testing.T) {
	var seq atomic.Int64
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if seq.Add(1)%2 == 0 {
			w.WriteHeader(http.StatusTooManyRequests)
			_, _ = fmt.Fprint(w, `{"error":{"code":"INVOCATION_QUEUE_FULL"}}`)
			return
		}
		_, _ = fmt.Fprint(w, "ok")
	}))
	defer server.Close()
	out, err := run(context.Background(), config{
		URLs: []string{server.URL}, Requests: 100, Concurrency: 10,
		Timeout: time.Second,
	})
	if err != nil {
		t.Fatal(err)
	}
	if out.Scheduled != 100 || out.Attempted != 100 || out.Unsent != 0 ||
		out.Status[200] != 50 || out.Status[429] != 50 ||
		out.ErrorCodes["INVOCATION_QUEUE_FULL"] != 50 ||
		out.LatencyByStatus[200].Count != 50 || out.LatencyByStatus[429].Count != 50 {
		t.Fatalf("unexpected report: %+v", out)
	}
}

func TestOpenLoopReportsUnsentArrivals(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(15 * time.Millisecond)
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()
	out, err := run(context.Background(), config{
		URLs: []string{server.URL}, Requests: 100, Concurrency: 2,
		Rate: 10000, Timeout: time.Second,
	})
	if err != nil {
		t.Fatal(err)
	}
	if out.Mode != "open" || out.Unsent == 0 || out.Attempted+out.Unsent != 100 ||
		out.Status[200] != out.Attempted {
		t.Fatalf("unexpected report: %+v", out)
	}
}

func TestMixedURLsReportEachFunctionSeparately(t *testing.T) {
	okServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer okServer.Close()
	failServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusServiceUnavailable)
	}))
	defer failServer.Close()
	out, err := run(context.Background(), config{
		URLs:     []string{okServer.URL, failServer.URL},
		Requests: 20, Concurrency: 4, Timeout: time.Second,
	})
	if err != nil {
		t.Fatal(err)
	}
	if out.ByURL[okServer.URL].Status[200] != 10 ||
		out.ByURL[failServer.URL].Status[503] != 10 ||
		out.Status[200] != 10 || out.Status[503] != 10 {
		t.Fatalf("mixed status attribution = %+v", out)
	}
}

func TestValidateRejectsNonHTTPAndInvalidCapacity(t *testing.T) {
	for _, c := range []config{
		{URLs: []string{"file:///tmp/x"}, Requests: 1, Concurrency: 1, Timeout: time.Second},
		{URLs: []string{"http://localhost"}, Requests: 0, Concurrency: 1, Timeout: time.Second},
		{URLs: []string{"http://localhost"}, Requests: 1, Concurrency: 0, Timeout: time.Second},
	} {
		if err := validate(c); err == nil {
			t.Fatalf("invalid config accepted: %+v", c)
		}
	}
}
