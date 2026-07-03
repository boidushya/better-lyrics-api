package main

import (
	"bufio"
	"fmt"
	"io"
	"net"
	"net/http"
	"testing"
	"time"
)

// TestNewHTTPServerSetsTimeouts guards against the fd-exhaustion outage:
// a server built with http.ListenAndServe has zero timeouts, so idle or
// stuck keep-alive connections are never reaped and accumulate as open
// file descriptors until accept() fails with EMFILE. Every timeout below
// must stay non-zero.
func TestNewHTTPServerSetsTimeouts(t *testing.T) {
	srv := newHTTPServer(":8080", http.NewServeMux())

	if srv.Addr != ":8080" {
		t.Errorf("Addr = %q, want %q", srv.Addr, ":8080")
	}
	if srv.Handler == nil {
		t.Error("Handler must not be nil")
	}

	timeouts := []struct {
		name string
		got  time.Duration
	}{
		{"ReadHeaderTimeout", srv.ReadHeaderTimeout},
		{"ReadTimeout", srv.ReadTimeout},
		{"WriteTimeout", srv.WriteTimeout},
		{"IdleTimeout", srv.IdleTimeout},
	}
	for _, tc := range timeouts {
		if tc.got <= 0 {
			t.Errorf("%s = %v, must be > 0 to prevent connection/fd leak", tc.name, tc.got)
		}
	}
}

// TestNewHTTPServerReapsIdleConnections reproduces the leak scenario end to end:
// an idle keep-alive connection must be closed by the server. With IdleTimeout
// unset (the old bug), the read below would block until the test deadline
// instead of returning EOF.
func TestNewHTTPServerReapsIdleConnections(t *testing.T) {
	srv := newHTTPServer("127.0.0.1:0", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	}))
	srv.IdleTimeout = 150 * time.Millisecond // shorten so the test is fast

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	go srv.Serve(ln)
	defer srv.Close()

	conn, err := net.Dial("tcp", ln.Addr().String())
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer conn.Close()

	if _, err := fmt.Fprintf(conn, "GET / HTTP/1.1\r\nHost: test\r\nConnection: keep-alive\r\n\r\n"); err != nil {
		t.Fatalf("write request: %v", err)
	}

	br := bufio.NewReader(conn)
	resp, err := http.ReadResponse(br, nil)
	if err != nil {
		t.Fatalf("read response: %v", err)
	}
	resp.Body.Close()

	// Connection is now idle. The server must close it after IdleTimeout.
	conn.SetReadDeadline(time.Now().Add(2 * time.Second))
	if _, err := br.ReadByte(); err != io.EOF {
		t.Fatalf("expected server to close idle connection (EOF), got: %v", err)
	}
}
