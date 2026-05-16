package main

import (
	"fmt"
	"log"
	"net"
	"net/http"

	pstore_client "github.com/brotherlogic/pstore/client"
	pb "github.com/brotherlogic/ghwebhook/proto/ghwebhook/v1"
	"github.com/brotherlogic/ghwebhook/server"
	"google.golang.org/grpc"
)

func main() {
	// 1. Initialize PStore Client
	ps, err := pstore_client.GetClient()
	if err != nil {
		log.Fatalf("Failed to initialize pstore client: %v", err)
	}

	// 2. Initialize GHWebhook Server
	s := server.NewServer(ps)

	// 3. Start gRPC Server (for registration)
	grpcPort := 50051
	lis, err := net.Listen("tcp", fmt.Sprintf(":%d", grpcPort))
	if err != nil {
		log.Fatalf("Failed to listen on gRPC port %d: %v", grpcPort, err)
	}
	grpcServer := grpc.NewServer()
	pb.RegisterRegistrationServiceServer(grpcServer, s)
	
	go func() {
		log.Printf("Starting gRPC server on port %d...", grpcPort)
		if err := grpcServer.Serve(lis); err != nil {
			log.Fatalf("gRPC server failed: %v", err)
		}
	}()

	// 4. Start HTTP Server (for webhooks)
	httpPort := 8080
	log.Printf("Starting HTTP server on port %d...", httpPort)
	if err := http.ListenAndServe(fmt.Sprintf(":%d", httpPort), s); err != nil {
		log.Fatalf("HTTP server failed: %v", err)
	}
}
