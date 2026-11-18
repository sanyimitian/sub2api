package main

import (
	"errors"
	"net"
	"net/http"
	"testing"
	"time"
)

func TestStartHTTPServerServesInitialProbe(t *testing.T) {
	server := &http.Server{
		Addr: "127.0.0.1:0",
		Handler: http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(http.StatusNoContent)
		}),
	}
	t.Cleanup(func() { _ = server.Close() })
	starts := 0
	serverErrors, err := startHTTPServer(server, func(addr net.Addr) {
		starts++
		client := &http.Client{Timeout: 2 * time.Second}
		resp, requestErr := client.Get("http://" + addr.String())
		if requestErr != nil {
			t.Errorf("initial probe could not reach the server: %v", requestErr)
			return
		}
		defer resp.Body.Close()
		if resp.StatusCode != http.StatusNoContent {
			t.Errorf("initial probe returned %d", resp.StatusCode)
		}
	})
	if err != nil {
		t.Fatal(err)
	}
	if starts != 1 {
		t.Fatalf("expected one background startup, got %d", starts)
	}
	_ = server.Close()
	select {
	case serveErr := <-serverErrors:
		if !errors.Is(serveErr, http.ErrServerClosed) {
			t.Fatalf("unexpected server exit: %v", serveErr)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("server did not stop")
	}
}

func TestStartHTTPServerDoesNotStartProbesWhenBindFails(t *testing.T) {
	occupied, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer occupied.Close()
	server := &http.Server{Addr: occupied.Addr().String()}
	started := false
	serverErrors, err := startHTTPServer(server, func(net.Addr) { started = true })
	if err == nil {
		_ = server.Close()
		t.Fatal("expected the occupied address to fail")
	}
	if started || serverErrors != nil {
		t.Fatal("background probes must not start without a listener")
	}
}
