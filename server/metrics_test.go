package server

import (
	"context"
	"net"
	"testing"
	"time"

	pb "github.com/brotherlogic/ghwebhook/proto/ghwebhook/v1"
	pstore_client "github.com/brotherlogic/pstore/client"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/testutil"
	"google.golang.org/grpc"
)

func TestMetricsRegistered(t *testing.T) {
	// Initialize label values to trigger registration in gatherer and verify label count
	IncomingEventsTotal.WithLabelValues("pull_request", "200")
	OutgoingEventsTotal.WithLabelValues("pull_request", "http://localhost:8080", "success")
	RegistrationsTotal.WithLabelValues("brotherlogic/ghwebhook", "http://localhost:8080")
	RegistrationStrikesTotal.WithLabelValues("brotherlogic/ghwebhook", "http://localhost:8080")
	RegistrationRemovalsTotal.WithLabelValues("brotherlogic/ghwebhook", "http://localhost:8080", "max_strikes")

	metricNames := []string{
		"ghwebhook_incoming_events_total",
		"ghwebhook_outgoing_events_total",
		"ghwebhook_registrations_total",
		"ghwebhook_registration_strikes_total",
		"ghwebhook_registration_removals_total",
	}

	mfs, err := prometheus.DefaultGatherer.Gather()
	if err != nil {
		t.Fatalf("failed to gather metrics: %v", err)
	}

	found := make(map[string]bool)
	for _, mf := range mfs {
		found[mf.GetName()] = true
	}

	for _, name := range metricNames {
		if !found[name] {
			var gathered []string
			for k := range found {
				gathered = append(gathered, k)
			}
			t.Errorf("metric %s not found in default registry. Gathered: %v", name, gathered)
		}
	}
}

func TestRegistrationMetrics(t *testing.T) {
	ps := pstore_client.GetTestClient()
	s := NewServer(ps)

	repo := "metrics-test/repo"
	addr := "127.0.0.1:9090"

	// Reset/initialize metrics to 0
	RegistrationsTotal.WithLabelValues(repo, addr).Set(0)
	RegistrationStrikesTotal.DeleteLabelValues(repo, addr)
	RegistrationRemovalsTotal.DeleteLabelValues(repo, addr, "max_strikes")

	// 1. Register a service and check that RegistrationsTotal is set to 1.0
	_, err := s.Register(context.Background(), &pb.RegistrationRequest{
		RepoFullName:   repo,
		ServiceAddress: addr,
	})
	if err != nil {
		t.Fatalf("Register failed: %v", err)
	}

	val := testutil.ToFloat64(RegistrationsTotal.WithLabelValues(repo, addr))
	if val != 1.0 {
		t.Errorf("Expected RegistrationsTotal to be 1.0, got %f", val)
	}

	// 2. Clear local memory and test ScanRegistrations (simulating startup scan)
	RegistrationsTotal.WithLabelValues(repo, addr).Set(0)
	err = s.ScanRegistrations(context.Background())
	if err != nil {
		t.Fatalf("ScanRegistrations failed: %v", err)
	}
	val = testutil.ToFloat64(RegistrationsTotal.WithLabelValues(repo, addr))
	if val != 1.0 {
		t.Errorf("Expected ScanRegistrations to restore RegistrationsTotal to 1.0, got %f", val)
	}

	// 3. Test strikes and removal metrics
	// Set up always-failing handler
	handler := &failingWebhookHandler{maxFails: 100}
	lis, _ := net.Listen("tcp", "127.0.0.1:0")
	grpcSrv := grpc.NewServer()
	pb.RegisterWebhookHandlerServer(grpcSrv, handler)
	go grpcSrv.Serve(lis)
	defer grpcSrv.Stop()

	testAddr := lis.Addr().String()
	s.backoffs = []time.Duration{1 * time.Millisecond}

	// Register the test handler address
	_, err = s.Register(context.Background(), &pb.RegistrationRequest{
		RepoFullName:   repo,
		ServiceAddress: testAddr,
	})
	if err != nil {
		t.Fatalf("Register failed: %v", err)
	}

	// Initial strikes should be 0
	strikeVal := testutil.ToFloat64(RegistrationStrikesTotal.WithLabelValues(repo, testAddr))
	if strikeVal != 0 {
		t.Errorf("Expected initial strikes to be 0, got %f", strikeVal)
	}

	// Trigger 3 failed deliveries
	event := &pb.WebhookEvent{Header: &pb.EventHeader{EventType: "pull_request"}}
	for i := 0; i < 3; i++ {
		s.routeEvent(context.Background(), event, repo)
	}

	// Verify strikes accumulated (should be 3)
	strikeVal = testutil.ToFloat64(RegistrationStrikesTotal.WithLabelValues(repo, testAddr))
	if strikeVal != 3 {
		t.Errorf("Expected strikes to be 3, got %f", strikeVal)
	}

	// Verify removal count and registrations gauge reset to 0
	removalVal := testutil.ToFloat64(RegistrationRemovalsTotal.WithLabelValues(repo, testAddr, "max_strikes"))
	if removalVal != 1.0 {
		t.Errorf("Expected removals to be 1.0, got %f", removalVal)
	}

	regVal := testutil.ToFloat64(RegistrationsTotal.WithLabelValues(repo, testAddr))
	if regVal != 0 {
		t.Errorf("Expected RegistrationsTotal for removed client to be 0, got %f", regVal)
	}
}


