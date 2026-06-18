package vantage

import (
	"context"
	"net"
	"strconv"
	"time"
)

// LocalVantage probes from the local machine (a real, always-available
// vantage). Remote SSH-backed vantages implement the same interface by running
// the dial on a remote host; that wiring is added when a fleet is configured.
type LocalVantage struct {
	Label  string
	Reg    string
	Dialer func(ctx context.Context, addr string) (net.Conn, error) // nil => default
}

func (l LocalVantage) Name() string {
	if l.Label == "" {
		return "local"
	}
	return l.Label
}

func (l LocalVantage) Region() string { return l.Reg }

func (l LocalVantage) Probe(ctx context.Context, host string, port int) (bool, time.Duration, error) {
	addr := net.JoinHostPort(host, strconv.Itoa(port))
	dctx, cancel := context.WithTimeout(ctx, 6*time.Second)
	defer cancel()
	start := time.Now()
	var (
		conn net.Conn
		err  error
	)
	if l.Dialer != nil {
		conn, err = l.Dialer(dctx, addr)
	} else {
		var d net.Dialer
		conn, err = d.DialContext(dctx, "tcp", addr)
	}
	if err != nil {
		// Connection refused/timeout = blocked from this vantage (not an error).
		return false, 0, nil
	}
	_ = conn.Close()
	return true, time.Since(start), nil
}
