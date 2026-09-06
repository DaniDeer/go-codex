package main

import (
	"fmt"
	"net"
	"os"
	"time"
)

// mustFreeAddr reserves an OS-assigned free TCP port on localhost, then
// releases it immediately so AttachMux/AttachRouter's own *http.Server
// can bind to it.
func mustFreeAddr() string {
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		fmt.Fprintf(os.Stderr, "reserve free port failed: %v\n", err)
		os.Exit(1)
	}
	addr := l.Addr().String()
	_ = l.Close()
	return addr
}

// waitForReady polls addr until it accepts TCP connections — Serve wires
// routes synchronously before starting its listener goroutine, so demo
// requests below must wait for it to actually be listening first.
func waitForReady(addr string) {
	for range 100 {
		conn, err := net.DialTimeout("tcp", addr, 50*time.Millisecond)
		if err == nil {
			_ = conn.Close()
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
}

// must exits the program if err is non-nil — used for startup wiring
// (Register/AttachMux/AttachRouter), where a failure means a malformed
// declaration, caught eagerly rather than on the first incoming request.
func must(err error, what string) {
	if err != nil {
		fmt.Fprintf(os.Stderr, "%s failed: %v\n", what, err)
		os.Exit(1)
	}
}
