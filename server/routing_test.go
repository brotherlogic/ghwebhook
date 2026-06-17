package server

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"
	"time"

	pb "github.com/brotherlogic/ghwebhook/proto/ghwebhook/v1"
	pstore_client "github.com/brotherlogic/pstore/client"
	"google.golang.org/grpc"
)

type mockWebhookHandler struct {
	pb.UnimplementedWebhookHandlerServer
	receivedEvents []*pb.WebhookEvent
}

func (m *mockWebhookHandler) ReceiveWebhook(ctx context.Context, req *pb.WebhookEvent) (*pb.WebhookResponse, error) {
	m.receivedEvents = append(m.receivedEvents, req)
	return &pb.WebhookResponse{Success: true}, nil
}

func TestRouting_ConcurrentDelivery(t *testing.T) {
	// 1. Setup Mock Handlers
	handler1 := &mockWebhookHandler{}
	lis1, _ := net.Listen("tcp", "localhost:0")
	s1 := grpc.NewServer()
	pb.RegisterWebhookHandlerServer(s1, handler1)
	go s1.Serve(lis1)
	defer s1.Stop()

	handler2 := &mockWebhookHandler{}
	lis2, _ := net.Listen("tcp", "localhost:0")
	s2 := grpc.NewServer()
	pb.RegisterWebhookHandlerServer(s2, handler2)
	go s2.Serve(lis2)
	defer s2.Stop()

	// 2. Setup Server
	secret := "test-secret"
	os.Setenv("GH_WEBHOOK_SECRET", secret)
	defer os.Unsetenv("GH_WEBHOOK_SECRET")

	s := NewServer(pstore_client.GetTestClient())

	// 3. Register Services
	repo := "repo/test"
	s.Register(context.Background(), &pb.RegistrationRequest{
		RepoFullName:   repo,
		ServiceAddress: lis1.Addr().String(),
	})
	s.Register(context.Background(), &pb.RegistrationRequest{
		RepoFullName:   repo,
		ServiceAddress: lis2.Addr().String(),
	})

	// 4. Send Webhook
	payload := []byte(`{"action": "opened", "pull_request": {"title": "Test PR", "user": {"login": "user1"}}, "repository": {"full_name": "repo/test"}}`)
	req, _ := http.NewRequest("POST", "/webhook", bytes.NewBuffer(payload))
	req.Header.Set("X-GitHub-Event", "pull_request")
	req.Header.Set("X-Hub-Signature-256", "sha256="+computeHMAC(payload, secret))

	rr := httptest.NewRecorder()
	s.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200 OK, got %d: %s", rr.Code, rr.Body.String())
	}

	// 5. Verify Delivery
	// Give some time for concurrent delivery
	time.Sleep(100 * time.Millisecond)

	if len(handler1.receivedEvents) != 1 {
		t.Errorf("handler1 did not receive event")
	}
	if len(handler2.receivedEvents) != 1 {
		t.Errorf("handler2 did not receive event")
	}
}

// Helper for tests
func computeHMAC(payload []byte, secret string) string {
	h := hmac.New(sha256.New, []byte(secret))
	h.Write(payload)
	return hex.EncodeToString(h.Sum(nil))
}
