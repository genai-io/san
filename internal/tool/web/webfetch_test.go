package web

import (
	"context"
	"net"
	"testing"
)

func TestPublicDialerBlocksInternalAddresses(t *testing.T) {
	ln, err := net.Listen("tcp", ":0")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = ln.Close() }()
	_, port, _ := net.SplitHostPort(ln.Addr().String())

	for _, host := range []string{"0.0.0.0", "::", "127.0.0.1", "::ffff:127.0.0.1", "10.0.0.1", "169.254.169.254"} {
		conn, err := publicDialer.DialContext(context.Background(), "tcp", net.JoinHostPort(host, port))
		if err == nil {
			_ = conn.Close()
			t.Errorf("dialed internal address %s", host)
		}
	}
}
