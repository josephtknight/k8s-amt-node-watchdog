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

// TestPowerCycle_NamespacedReturnValue replicates the real-world AMT response
// format (seen 2026-11-07 against a live Intel AMT endpoint): the firmware
// qualifies ReturnValue with a namespace prefix bound to
// CIM_PowerManagementService, not the bare <ReturnValue> the old substring
// check looked for. That mismatch caused every real power-cycle response --
// success or failure -- to be reported as "failed", which in turn meant
// Restarter never recorded a cooldown and kept re-sending power-cycle
// commands every reconcile tick while the node was still rebooting.
func TestPowerCycle_NamespacedReturnValue(t *testing.T) {
	const realWorldBody = `<?xml version="1.0" encoding="UTF-8"?><a:Envelope xmlns:a="http://www.w3.org/2003/05/soap-envelope" xmlns:g="http://schemas.dmtf.org/wbem/wscim/1/cim-schema/2/CIM_PowerManagementService"><a:Body><g:RequestPowerStateChange_OUTPUT><g:ReturnValue>0</g:ReturnValue></g:RequestPowerStateChange_OUTPUT></a:Body></a:Envelope>`

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(realWorldBody))
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
	if err != nil {
		t.Fatalf("unexpected error for namespaced success response: %v", err)
	}
}

func TestPowerCycle_NamespacedNonZeroReturnValue(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`<a:Body><g:ReturnValue>2</g:ReturnValue></a:Body>`))
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
		t.Fatal("expected error for namespaced non-zero return value")
	}
	if !strings.Contains(err.Error(), "2") {
		t.Errorf("error should mention the actual return value, got: %v", err)
	}
}

func TestPowerCycle_MissingReturnValue(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`<a:Body><a:Fault>something else entirely</a:Fault></a:Body>`))
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
		t.Fatal("expected error when response has no ReturnValue at all")
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
