package sandbox

import (
	"context"
	"encoding/json"
	"net"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// parkFirstWrite is a host connection whose FIRST Write returns only after the
// gate opens. The bytes are already on the wire, so the client reads the reply
// while the host goroutine that wrote it is still "between the write and what
// comes after it": the window in which the host used to still count the slot
// as taken. Later writes pass straight through.
type parkFirstWrite struct {
	net.Conn
	gate  chan struct{}
	count atomic.Int32
}

func (c *parkFirstWrite) Write(p []byte) (int, error) {
	n, err := c.Conn.Write(p)
	if c.count.Add(1) == 1 {
		<-c.gate
	}
	return n, err
}

// A client that stays within MaxSDKInflight must never be refused. It sends a
// replacement the instant it reads a reply. The host used to free its in-flight
// slot only after writing the reply, so the replacement was counted as the 33rd
// and answered 429 (found end to end: 200 concurrent kv.incr from one function).
// With the old order the refusal's own reply queues behind the parked writer, so
// the 33rd handler call never happens; with the fix it happens promptly.
func TestSDKInflightSlotIsFreedBeforeTheReplyIsSent(t *testing.T) {
	host, child, err := newSDKPair()
	if err != nil {
		t.Fatal(err)
	}
	client, err := net.FileConn(child)
	_ = child.Close()
	if err != nil {
		t.Fatal(err)
	}
	gate := make(chan struct{})
	var calls atomic.Int32
	srv := &sdkServer{
		conn: &parkFirstWrite{Conn: host, gate: gate}, functionID: "fn-host",
		handler: func(context.Context, SDKCaller, string, json.RawMessage) (any, *SDKError) {
			calls.Add(1)
			return map[string]int{"v": 1}, nil
		},
		state:     func() *dispatchState { return nil },
		violation: func() {},
		kill:      func() {},
	}
	done := make(chan struct{})
	var once sync.Once
	open := func() { once.Do(func() { close(gate) }) }
	go func() { srv.serve(); close(done) }()
	t.Cleanup(func() {
		open()
		_ = client.Close()
		_ = host.Close()
		<-done
	})

	for id := uint64(1); id <= MaxSDKInflight; id++ {
		sendReq(t, client, id, "kv.get", "", `{"key":"k"}`)
	}
	if r := readReply(t, client); !r.OK {
		t.Fatalf("first reply failed: %+v", r.E)
	}
	// Its writer is parked; the client now has 31 outstanding and is within its limit.
	sendReq(t, client, MaxSDKInflight+1, "kv.get", "", `{"key":"k"}`)
	deadline := time.Now().Add(2 * time.Second)
	for calls.Load() < MaxSDKInflight+1 && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
	}
	got := calls.Load()
	open()
	if got != MaxSDKInflight+1 {
		t.Fatalf("handler ran %d times, want %d: a client within the %d-request limit was refused", got, MaxSDKInflight+1, MaxSDKInflight)
	}
	for i := 0; i < MaxSDKInflight; i++ {
		if r := readReply(t, client); !r.OK {
			t.Fatalf("a client within the limit was refused: %+v", r.E)
		}
	}
}
