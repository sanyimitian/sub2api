package main

import (
	"net"
	"net/http"
)

// startHTTPServer binds before starting background work that may call this
// server. The listener can queue requests even before Serve's goroutine runs.
func startHTTPServer(server *http.Server, onListening func(net.Addr)) (<-chan error, error) {
	addr := server.Addr
	if addr == "" {
		addr = ":http"
	}
	listener, err := net.Listen("tcp", addr)
	if err != nil {
		return nil, err
	}
	serverErrors := make(chan error, 1)
	go func() {
		serverErrors <- server.Serve(listener)
		close(serverErrors)
	}()
	if onListening != nil {
		onListening(listener.Addr())
	}
	return serverErrors, nil
}
