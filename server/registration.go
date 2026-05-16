package server

import (
	"context"
	"fmt"
	"sync"

	pstore_client "github.com/brotherlogic/pstore/client"
	pstore_pb "github.com/brotherlogic/pstore/proto"
	pb "github.com/brotherlogic/ghwebhook/proto/ghwebhook/v1"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/anypb"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
)

type Server struct {
	pstore pstore_client.PStoreClient
	
	connLock sync.Mutex
	conns    map[string]pb.WebhookHandlerClient
}

func NewServer(pstore pstore_client.PStoreClient) *Server {
	return &Server{
		pstore: pstore,
		conns:  make(map[string]pb.WebhookHandlerClient),
	}
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

	return &pb.RegistrationResponse{Success: true}, nil
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
