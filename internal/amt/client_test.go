package amt

import (
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestPowerCycle_Success(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "POST" {
			t.Errorf("expected POST, got %s", r.Method)
		}
		if !strings.Contains(r.Header.Get("Content-Type"), "application/soap+xml") {
			t.Errorf("unexpected content type: %s", r.Header.Get("Content-Type"))
		}
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`<Body><ReturnValue>0</ReturnValue></Body>`))
	}))
	defer server.Close()

	// Extract host:port from test server URL
	addr := strings.TrimPrefix(server.URL, "http://")
	parts := strings.SplitN(addr, ":", 2)
	host := parts[0]

	// Create client pointing at the test server's port
	// We need to use a plain HTTP client (no digest) for the test server
	client := &Client{
		username: "admin",
		password: "password",
		port:     server.Listener.Addr().(*net.TCPAddr).Port,
		client:   server.Client(),
	}

	err := client.PowerCycle(host)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestPowerCycle_NonZeroReturnValue(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`<Body><ReturnValue>2</ReturnValue></Body>`))
	}))
	defer server.Close()

	addr := strings.TrimPrefix(server.URL, "http://")
	parts := strings.SplitN(addr, ":", 2)
	host := parts[0]

	client := &Client{
		username: "admin",
		password: "password",
		port:     server.Listener.Addr().(*net.TCPAddr).Port,
		client:   server.Client(),
	}

	err := client.PowerCycle(host)
	if err == nil {
		t.Fatal("expected error for non-zero return value")
	}
	if !strings.Contains(err.Error(), "ReturnValue") {
		t.Errorf("error should mention response, got: %v", err)
	}
}

func TestPowerCycle_HTTPError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		w.Write([]byte("unauthorized"))
	}))
	defer server.Close()

	addr := strings.TrimPrefix(server.URL, "http://")
	parts := strings.SplitN(addr, ":", 2)
	host := parts[0]

	client := &Client{
		username: "admin",
		password: "wrong",
		port:     server.Listener.Addr().(*net.TCPAddr).Port,
		client:   server.Client(),
	}

	err := client.PowerCycle(host)
	if err == nil {
		t.Fatal("expected error for HTTP 401")
	}
	if !strings.Contains(err.Error(), "401") {
		t.Errorf("error should mention status code, got: %v", err)
	}
}

func TestPowerCycle_ConnectionRefused(t *testing.T) {
	client := &Client{
		username: "admin",
		password: "password",
		port:     19999, // nothing listening
		client:   &http.Client{},
	}

	err := client.PowerCycle("127.0.0.1")
	if err == nil {
		t.Fatal("expected error for connection refused")
	}
}
