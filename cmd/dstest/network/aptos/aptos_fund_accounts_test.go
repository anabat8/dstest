package aptos

import (
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"testing"
	"time"
)

func TestWaitForValidatorRestEndpointsReady(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.URL.Path != "/v1" {
			http.NotFound(writer, request)
			return
		}
		writer.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	serverURL, err := url.Parse(server.URL)
	if err != nil {
		t.Fatalf("parse test server URL: %v", err)
	}
	_, portText, err := net.SplitHostPort(serverURL.Host)
	if err != nil {
		t.Fatalf("split test server address: %v", err)
	}
	port, err := strconv.Atoi(portText)
	if err != nil {
		t.Fatalf("parse test server port: %v", err)
	}

	if err := waitForValidatorRestEndpoints(port, 1, time.Second); err != nil {
		t.Fatalf("ready endpoint was rejected: %v", err)
	}
}

func TestWaitForValidatorRestEndpointsTimesOut(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("reserve test port: %v", err)
	}
	port := listener.Addr().(*net.TCPAddr).Port
	if err := listener.Close(); err != nil {
		t.Fatalf("close reserved test port: %v", err)
	}

	if err := waitForValidatorRestEndpoints(port, 1, 50*time.Millisecond); err == nil {
		t.Fatal("unavailable endpoint unexpectedly reported ready")
	}
}
