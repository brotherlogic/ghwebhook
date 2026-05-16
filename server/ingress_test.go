package server

import (
	"bytes"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"
)

func TestWebhookIngress_NoSignature(t *testing.T) {
	s := &Server{}
	req, _ := http.NewRequest("POST", "/webhook", bytes.NewBuffer([]byte("{}")))
	rr := httptest.NewRecorder()

	s.ServeHTTP(rr, req)

	if rr.Code != http.StatusUnauthorized {
		t.Errorf("expected 401 Unauthorized, got %d", rr.Code)
	}
}

func TestWebhookIngress_ValidSignature(t *testing.T) {
	secret := "test-secret"
	os.Setenv("GH_WEBHOOK_SECRET", secret)
	defer os.Unsetenv("GH_WEBHOOK_SECRET")

	s := &Server{}
	payload := []byte(`{"action": "opened", "number": 123, "pull_request": {"title": "Test PR", "body": "Body text", "user": {"login": "user1"}}, "repository": {"full_name": "repo/test"}}`)
	
	h := hmac.New(sha256.New, []byte(secret))
	h.Write(payload)
	signature := "sha256=" + hex.EncodeToString(h.Sum(nil))

	req, _ := http.NewRequest("POST", "/webhook", bytes.NewBuffer(payload))
	req.Header.Set("X-Hub-Signature-256", signature)
	req.Header.Set("X-GitHub-Event", "pull_request")
	
	rr := httptest.NewRecorder()
	s.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Errorf("expected 200 OK, got %d: %s", rr.Code, rr.Body.String())
	}

	// We'll need a way to verify that the parsing happened correctly.
	// For now, we are just checking the status code.
}

func TestWebhookIngress_InvalidJSON(t *testing.T) {
	secret := "test-secret"
	os.Setenv("GH_WEBHOOK_SECRET", secret)
	defer os.Unsetenv("GH_WEBHOOK_SECRET")

	s := &Server{}
	payload := []byte(`{invalid-json}`)
	
	h := hmac.New(sha256.New, []byte(secret))
	h.Write(payload)
	signature := "sha256=" + hex.EncodeToString(h.Sum(nil))

	req, _ := http.NewRequest("POST", "/webhook", bytes.NewBuffer(payload))
	req.Header.Set("X-Hub-Signature-256", signature)
	req.Header.Set("X-GitHub-Event", "pull_request")
	
	rr := httptest.NewRecorder()
	s.ServeHTTP(rr, req)

	if rr.Code != http.StatusBadRequest {
		t.Errorf("expected 400 Bad Request for invalid JSON, got %d", rr.Code)
	}
}
