package server

import (
	"context"
	"fmt"
	"net"
	"testing"
	"time"

	pb "github.com/brotherlogic/ghwebhook/proto/ghwebhook/v1"
	pstore_client "github.com/brotherlogic/pstore/client"
	pstore_pb "github.com/brotherlogic/pstore/proto"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

type failingWebhookHandler struct {
	pb.UnimplementedWebhookHandlerServer
	failCount int
	maxFails  int
}

func (m *failingWebhookHandler) ReceiveWebhook(ctx context.Context, req *pb.WebhookEvent) (*pb.WebhookResponse, error) {
	if m.failCount < m.maxFails {
		m.failCount++
		return nil, status.Errorf(codes.Unavailable, "temporary failure")
	}
	return &pb.WebhookResponse{Success: true}, nil
}

func TestReliableDelivery_RetrySuccess(t *testing.T) {
	// 1. Setup handler that fails twice then succeeds
	handler := &failingWebhookHandler{maxFails: 2}
	lis, _ := net.Listen("tcp", "localhost:0")
	s := grpc.NewServer()
	pb.RegisterWebhookHandlerServer(s, handler)
	go s.Serve(lis)
	defer s.Stop()

	// 2. Setup Server
	ps := pstore_client.GetTestClient()
	server := NewServer(ps)
	// Inject 3 backoffs for testing (so 3 total attempts)
	server.backoffs = []time.Duration{10 * time.Millisecond, 20 * time.Millisecond, 30 * time.Millisecond}

	// 3. Deliver
	event := &pb.WebhookEvent{Header: &pb.EventHeader{EventType: "pull_request"}}
	err := server.deliverWithRetry(context.Background(), event, lis.Addr().String())
	if err != nil {
		t.Fatalf("Delivery failed despite retries: %v", err)
	}

	if handler.failCount != 2 {
		t.Errorf("Expected 2 failures, got %d", handler.failCount)
	}
}

func TestReliableDelivery_ThreeStrikesRemoval(t *testing.T) {
	// 1. Setup handler that always fails
	handler := &failingWebhookHandler{maxFails: 100}
	lis, _ := net.Listen("tcp", "localhost:0")
	s := grpc.NewServer()
	pb.RegisterWebhookHandlerServer(s, handler)
	go s.Serve(lis)
	defer s.Stop()

	// 2. Setup Server
	ps := pstore_client.GetTestClient()
	server := NewServer(ps)
	server.backoffs = []time.Duration{1 * time.Millisecond} // 1 attempt total

	// 3. Register service
	repo := "repo/test"
	addr := lis.Addr().String()
	server.Register(context.Background(), &pb.RegistrationRequest{
		RepoFullName:   repo,
		ServiceAddress: addr,
	})

	// 4. Trigger 3 consecutive failed delivery cycles
	event := &pb.WebhookEvent{Header: &pb.EventHeader{EventType: "pull_request"}}

	for i := 0; i < 3; i++ {
		server.routeEvent(context.Background(), event, repo)
	}

	// 5. Verify removal from pstore
	key := fmt.Sprintf("ghwebhook/reg/%s/%s", repo, addr)
	_, err := ps.Read(context.Background(), &pstore_pb.ReadRequest{Key: key})
	if status.Code(err) != codes.NotFound {
		t.Errorf("Expected registration to be removed from pstore, got err: %v", err)
	}
}
