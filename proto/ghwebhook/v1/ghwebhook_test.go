package v1_test

import (
	"context"
	"testing"

	pb "github.com/brotherlogic/ghwebhook/proto/ghwebhook/v1"
	"google.golang.org/grpc"
)

type mockRegistrationServiceClient struct {
	pb.RegistrationServiceClient
	unregisterFunc func(ctx context.Context, in *pb.UnregisterRequest, opts ...grpc.CallOption) (*pb.UnregisterResponse, error)
}

func (m *mockRegistrationServiceClient) Unregister(ctx context.Context, in *pb.UnregisterRequest, opts ...grpc.CallOption) (*pb.UnregisterResponse, error) {
	if m.unregisterFunc != nil {
		return m.unregisterFunc(ctx, in, opts...)
	}
	return nil, nil
}

func TestUnregisterMessagesAndService(t *testing.T) {
	req := &pb.UnregisterRequest{
		RepoFullName:   "owner/repo",
		ServiceAddress: "service.namespace.svc:50051",
	}

	if req.GetRepoFullName() != "owner/repo" {
		t.Errorf("expected repo_full_name 'owner/repo', got %q", req.GetRepoFullName())
	}
	if req.GetServiceAddress() != "service.namespace.svc:50051" {
		t.Errorf("expected service_address 'service.namespace.svc:50051', got %q", req.GetServiceAddress())
	}

	resp := &pb.UnregisterResponse{
		Success: true,
		Message: "unregistered successfully",
	}

	if !resp.GetSuccess() {
		t.Errorf("expected success true, got false")
	}
	if resp.GetMessage() != "unregistered successfully" {
		t.Errorf("expected message 'unregistered successfully', got %q", resp.GetMessage())
	}

	var client pb.RegistrationServiceClient = &mockRegistrationServiceClient{
		unregisterFunc: func(ctx context.Context, in *pb.UnregisterRequest, opts ...grpc.CallOption) (*pb.UnregisterResponse, error) {
			return resp, nil
		},
	}

	res, err := client.Unregister(context.Background(), req)
	if err != nil {
		t.Fatalf("client.Unregister failed: %v", err)
	}
	if !res.GetSuccess() {
		t.Errorf("expected success true in client response")
	}
}
