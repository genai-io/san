package inspector

import (
	"net/http"
	"testing"
)

// DNS rebinding: a page on evil.example rebinds its name to 127.0.0.1, and the
// browser then sends "Host: evil.example:port" to the inspector.
func TestServerRefusesNonLoopbackHost(t *testing.T) {
	_, _, server := fixture(t)
	for host, want := range map[string]int{
		"evil.example:1234": http.StatusForbidden,
		"evil.example":      http.StatusForbidden,
		"127.0.0.1.nip.io":  http.StatusForbidden,
		"localhost:1234":    http.StatusOK,
		"[::1]:1234":        http.StatusOK,
	} {
		req, err := http.NewRequest(http.MethodGet, server.URL+"/api/sessions", nil)
		if err != nil {
			t.Fatal(err)
		}
		req.Host = host
		resp, err := server.Client().Do(req)
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		if resp.StatusCode != want {
			t.Errorf("Host %q: status %d, want %d", host, resp.StatusCode, want)
		}
	}
}
