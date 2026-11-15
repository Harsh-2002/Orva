package commands

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	cli "github.com/Harsh-2002/Orva/internal/client"
)

// A delete outlasting the client's overall timeout must still succeed: the
// server answers only when a large function is gone, which can take minutes.
func TestDeleteFunctionWaitOutlastsClientTimeout(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodDelete || r.URL.Path != "/api/v1/functions/fn-1" {
			t.Errorf("unexpected request %s %s", r.Method, r.URL.Path)
		}
		time.Sleep(300 * time.Millisecond)
		_, _ = w.Write([]byte(`{"status":"deleted","id":"fn-1"}`))
	}))
	defer srv.Close()
	client := cli.NewClient(srv.URL, "k")
	client.HTTP.Timeout = 50 * time.Millisecond // stands in for the 120 s default

	raw, err := deleteFunctionWait(context.Background(), client, "fn-1")
	if err != nil {
		t.Fatalf("delete outlasting the client timeout failed: %v", err)
	}
	if string(raw) != `{"status":"deleted","id":"fn-1"}` {
		t.Fatalf("body = %s", raw)
	}
}

func TestDeleteFunctionWaitStopsWhenCancelled(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		select {
		case <-time.After(5 * time.Second):
		case <-r.Context().Done():
		}
	}))
	defer srv.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	start := time.Now()
	if _, err := deleteFunctionWait(ctx, cli.NewClient(srv.URL, "k"), "fn-1"); err == nil {
		t.Fatal("expected an error after cancellation")
	}
	if time.Since(start) > 3*time.Second {
		t.Fatal("cancellation did not stop the wait")
	}
}

func TestDeleteFunctionWaitReportsAPIErrors(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write([]byte(`{"error":{"code":"INTERNAL","message":"function delete did not finish; it is marked deleting, retry DELETE to resume"}}`))
	}))
	defer srv.Close()
	if _, err := deleteFunctionWait(context.Background(), cli.NewClient(srv.URL, "k"), "fn-1"); err == nil {
		t.Fatal("expected the API error")
	}
}

// Long transfers (backup download, restore and deploy upload, VACUUM) must not
// be cut off by the shared client's overall timeout.
func TestSendLongOutlastsClientTimeout(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body) // a slow upload is read to the end
		for i := 0; i < 4; i++ {      // and the reply trickles out
			_, _ = w.Write([]byte("chunk;"))
			w.(http.Flusher).Flush()
			time.Sleep(100 * time.Millisecond)
		}
		_, _ = w.Write(body)
	}))
	defer srv.Close()
	client := cli.NewClient(srv.URL, "k")
	client.HTTP.Timeout = 50 * time.Millisecond

	pr, pw := io.Pipe()
	go func() {
		defer pw.Close()
		for i := 0; i < 3; i++ {
			_, _ = pw.Write([]byte("up;"))
			time.Sleep(100 * time.Millisecond)
		}
	}()
	resp, err := sendLong(context.Background(), client, http.MethodPost, "/x", pr, "application/octet-stream")
	if err != nil {
		t.Fatalf("long transfer cut off by the client timeout: %v", err)
	}
	defer resp.Body.Close()
	got, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("reading the reply: %v", err)
	}
	if want := "chunk;chunk;chunk;chunk;up;up;up;"; string(got) != want {
		t.Fatalf("body = %q, want %q", got, want)
	}
}
