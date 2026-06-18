// Package freeport allocates free local TCP ports for transient test inbounds.
package freeport

import "net"

// Free returns an OS-assigned free TCP port on 127.0.0.1. There is an
// inherent TOCTOU window between returning the port and a caller binding it;
// for test-harness use (one client SOCKS inbound per proxy) this is fine.
func Free() (int, error) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return 0, err
	}
	defer ln.Close()
	return ln.Addr().(*net.TCPAddr).Port, nil
}
