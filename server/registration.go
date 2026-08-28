package server

import (
	"context"
	"fmt"
	"sync"
	"time"

	pb "github.com/brotherlogic/ghwebhook/proto/ghwebhook/v1"
	pstore_client "github.com/brotherlogic/pstore/client"
	pstore_pb "github.com/brotherlogic/pstore/proto"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/anypb"
)

type Server struct {
	pb.UnimplementedRegistrationServiceServer
	pstore pstore_client.PStoreClient

	connLock sync.Mutex
	conns    map[string]pb.WebhookHandlerClient

	strikeLock sync.Mutex
	strikes    map[string]int

	ghClient   GitHubHookClient
	ingressURL string

	// Configurable for testing
	backoffs []time.Duration
}

// ServerOption configures a Server instance.
type ServerOption func(*Server)

// WithGitHubClient configures the GitHub client for the Server.
func WithGitHubClient(client GitHubHookClient) ServerOption {
	return func(s *Server) {
		s.ghClient = client
	}
}

// WithIngressURL configures the ingress URL for the Server.
func WithIngressURL(ingressURL string) ServerOption {
	return func(s *Server) {
		s.ingressURL = ingressURL
	}
}

func NewServer(pstore pstore_client.PStoreClient, opts ...ServerOption) *Server {
	s := &Server{
		pstore:   pstore,
		conns:    make(map[string]pb.WebhookHandlerClient),
		strikes:  make(map[string]int),
		backoffs: []time.Duration{time.Second, 2 * time.Second, 4 * time.Second, 60 * time.Second},
	}
	for _, opt := range opts {
		opt(s)
	}
	return s
}

// GetGitHubClient returns the configured GitHubHookClient.
func (s *Server) GetGitHubClient() GitHubHookClient {
	return s.ghClient
}

// GetIngressURL returns the configured Ingress URL.
func (s *Server) GetIngressURL() string {
	return s.ingressURL
}

func (s *Server) Register(ctx context.Context, req *pb.RegistrationRequest) (*pb.RegistrationResponse, error) {
	key := fmt.Sprintf("ghwebhook/reg/%s/%s", req.RepoFullName, req.ServiceAddress)

	val, err := anypb.New(req)
	if err != nil {
		return &pb.RegistrationResponse{Success: false, Message: err.Error()}, nil
	}

	_, err = s.pstore.Write(ctx, &pstore_pb.WriteRequest{
		Key:   key,
		Value: val,
	})
	if err != nil {
		return &pb.RegistrationResponse{Success: false, Message: err.Error()}, nil
	}

	// Reset strikes on successful re-registration
	s.strikeLock.Lock()
	delete(s.strikes, key)
	s.strikeLock.Unlock()

	// Track active registration metrics
	RegistrationsTotal.WithLabelValues(req.RepoFullName, req.ServiceAddress).Set(1)

	return &pb.RegistrationResponse{Success: true}, nil
}

// Unregister removes an existing registration for a repository and cleans up associated state.
func (s *Server) Unregister(ctx context.Context, req *pb.UnregisterRequest) (*pb.UnregisterResponse, error) {
	if req == nil || req.GetRepoFullName() == "" || req.GetServiceAddress() == "" {
		return nil, status.Errorf(codes.InvalidArgument, "repo_full_name and service_address must be provided")
	}

	key := fmt.Sprintf("ghwebhook/reg/%s/%s", req.RepoFullName, req.ServiceAddress)

	readResp, err := s.pstore.Read(ctx, &pstore_pb.ReadRequest{Key: key})
	if err != nil || readResp == nil || readResp.Value == nil || len(readResp.Value.Value) == 0 {
		return nil, status.Errorf(codes.NotFound, "registration not found")
	}

	_, err = s.pstore.Delete(ctx, &pstore_pb.DeleteRequest{Key: key})
	if err != nil {
		return nil, err
	}

	// Clean up in-memory strikes
	s.strikeLock.Lock()
	delete(s.strikes, key)
	s.strikeLock.Unlock()

	// Update gauge metric
	RegistrationsTotal.WithLabelValues(req.RepoFullName, req.ServiceAddress).Set(0)

	// Query remaining registrations for the repository
	remaining, err := s.getRegistrations(ctx, req.RepoFullName)
	if err == nil && len(remaining) == 0 && s.ghClient != nil {
		go s.deleteGitHubWebhookAsync(req.RepoFullName)
	}

	return &pb.UnregisterResponse{Success: true}, nil
}

func (s *Server) deleteGitHubWebhookAsync(repoFullName string) {
	// Async GitHub webhook cleanup logic implemented in sub-issue #57
}

func (s *Server) ScanRegistrations(ctx context.Context) error {
	resp, err := s.pstore.GetKeys(ctx, &pstore_pb.GetKeysRequest{Prefix: "ghwebhook/reg/"})
	if err != nil {
		return err
	}

	for _, key := range resp.Keys {
		readResp, err := s.pstore.Read(ctx, &pstore_pb.ReadRequest{Key: key})
		if err != nil {
			return err
		}

		reg := &pb.RegistrationRequest{}
		err = proto.Unmarshal(readResp.Value.Value, reg)
		if err != nil {
			return err
		}

		RegistrationsTotal.WithLabelValues(reg.RepoFullName, reg.ServiceAddress).Set(1)
	}

	return nil
}


func (s *Server) getRegistrations(ctx context.Context, repo string) ([]*pb.RegistrationRequest, error) {
	prefix := fmt.Sprintf("ghwebhook/reg/%s/", repo)
	resp, err := s.pstore.GetKeys(ctx, &pstore_pb.GetKeysRequest{Prefix: prefix})
	if err != nil {
		return nil, err
	}

	var registrations []*pb.RegistrationRequest
	for _, key := range resp.Keys {
		readResp, err := s.pstore.Read(ctx, &pstore_pb.ReadRequest{Key: key})
		if err != nil {
			return nil, err
		}

		reg := &pb.RegistrationRequest{}
		// Using manual unmarshal because pstore's TestClient drops TypeURL
		err = proto.Unmarshal(readResp.Value.Value, reg)
		if err != nil {
			return nil, err
		}
		registrations = append(registrations, reg)
	}

	return registrations, nil
}

func (s *Server) getClient(address string) (pb.WebhookHandlerClient, error) {
	s.connLock.Lock()
	defer s.connLock.Unlock()

	if client, ok := s.conns[address]; ok {
		return client, nil
	}

	// We use insecure for now as this is internal cluster traffic
	conn, err := grpc.Dial(address, grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		return nil, err
	}

	client := pb.NewWebhookHandlerClient(conn)
	s.conns[address] = client
	return client, nil
}

func (s *Server) deliverWithRetry(ctx context.Context, event *pb.WebhookEvent, address string) error {
	client, err := s.getClient(address)
	if err != nil {
		return err
	}

	var lastErr error
	for i, backoff := range s.backoffs {
		_, lastErr = client.ReceiveWebhook(ctx, event)
		if lastErr == nil {
			return nil
		}

		// Don't sleep after the last attempt
		if i < len(s.backoffs)-1 {
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-time.After(backoff):
			}
		}
	}

	return lastErr
}
