package server

import (
	"context"
	"fmt"
	"testing"

	pb "github.com/brotherlogic/ghwebhook/proto/ghwebhook/v1"
	pstore_client "github.com/brotherlogic/pstore/client"
	pstore_pb "github.com/brotherlogic/pstore/proto"
	"google.golang.org/protobuf/proto"
)

func TestRegister(t *testing.T) {
	// Initialize in-memory pstore client
	s := NewServer(pstore_client.GetTestClient())

	req := &pb.RegistrationRequest{
		RepoFullName:   "brotherlogic/ghwebhook",
		ServiceAddress: "localhost:50051",
	}

	resp, err := s.Register(context.Background(), req)
	if err != nil {
		t.Fatalf("Register failed: %v", err)
	}

	if !resp.Success {
		t.Errorf("Expected success, got failure: %s", resp.Message)
	}

	// Verify that the key exists in pstore
	key := fmt.Sprintf("ghwebhook/reg/%s/%s", req.RepoFullName, req.ServiceAddress)
	readResp, err := s.pstore.Read(context.Background(), &pstore_pb.ReadRequest{Key: key})
	if err != nil {
		t.Fatalf("Read from pstore failed: %v", err)
	}

	if readResp.Value == nil {
		t.Fatal("Expected value in pstore, got nil")
	}

	// Verify the content using manual unmarshal because TestClient drops type URL
	readReq := &pb.RegistrationRequest{}
	err = proto.Unmarshal(readResp.Value.Value, readReq)
	if err != nil {
		t.Fatalf("Failed to unmarshal pstore value: %v", err)
	}

	if readReq.RepoFullName != req.RepoFullName || readReq.ServiceAddress != req.ServiceAddress {
		t.Errorf("Stored data mismatch. Got %v, want %v", readReq, req)
	}
}

func TestGetRegistrations(t *testing.T) {
	s := NewServer(pstore_client.GetTestClient())

	repo := "brotherlogic/ghwebhook"
	services := []string{"localhost:50051", "localhost:50052"}

	for _, addr := range services {
		_, err := s.Register(context.Background(), &pb.RegistrationRequest{
			RepoFullName:   repo,
			ServiceAddress: addr,
		})
		if err != nil {
			t.Fatalf("Failed to register %s: %v", addr, err)
		}
	}

	regs, err := s.getRegistrations(context.Background(), repo)
	if err != nil {
		t.Fatalf("getRegistrations failed: %v", err)
	}

	if len(regs) != 2 {
		t.Errorf("Expected 2 registrations, got %d", len(regs))
	}
}
