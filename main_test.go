package main

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/brotherlogic/ghwebhook/server"
	pstore_client "github.com/brotherlogic/pstore/client"
)

func TestMetricsEndpoint(t *testing.T) {
	// Initialize a mock server
	ps, err := pstore_client.GetClient()
	if err != nil {
		t.Fatalf("Failed to get pstore client: %v", err)
	}
	s := server.NewServer(ps)
	handler := setupHandler(s)

	req := httptest.NewRequest("GET", "/metrics", nil)
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)

	// Since we haven't implemented /metrics yet, this should return 404 (NotFound)
	// under the current setup handler. Our assertion expects 200 OK, which will make the test RED.
	if w.Code != http.StatusOK {
		t.Errorf("Expected status code 200, got %d", w.Code)
	}
}
