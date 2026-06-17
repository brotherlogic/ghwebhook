package main

import (
	"fmt"
	"log"
	"net"
	"net/http"

	pb "github.com/brotherlogic/ghwebhook/proto/ghwebhook/v1"
	"github.com/brotherlogic/ghwebhook/server"
	pstore_client "github.com/brotherlogic/pstore/client"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	"google.golang.org/grpc"
	"google.golang.org/grpc/health"
	"google.golang.org/grpc/health/grpc_health_v1"
)

func main() {
	// 1. Initialize PStore Client
	ps, err := pstore_client.GetClient()
	if err != nil {
		log.Fatalf("Failed to initialize pstore client: %v", err)
	}

	// 2. Initialize GHWebhook Server
	s := server.NewServer(ps)

	// 3. Start gRPC Server (for registration and health)
	grpcPort := 50051
	lis, err := net.Listen("tcp", fmt.Sprintf(":%d", grpcPort))
	if err != nil {
		log.Fatalf("Failed to listen on gRPC port %d: %v", grpcPort, err)
	}
	grpcServer := grpc.NewServer()
	pb.RegisterRegistrationServiceServer(grpcServer, s)

	// Register standard gRPC health service
	healthServer := health.NewServer()
	grpc_health_v1.RegisterHealthServer(grpcServer, healthServer)
	healthServer.SetServingStatus("ghwebhook", grpc_health_v1.HealthCheckResponse_SERVING)

	go func() {
		log.Printf("Starting gRPC server on port %d...", grpcPort)
		if err := grpcServer.Serve(lis); err != nil {
			log.Fatalf("gRPC server failed: %v", err)
		}
	}()

	// 4. Start HTTP Server (for webhooks and healthz)
	httpPort := 8080
	log.Printf("Starting HTTP server on port %d...", httpPort)
	if err := http.ListenAndServe(fmt.Sprintf(":%d", httpPort), setupHandler(s)); err != nil {
		log.Fatalf("HTTP server failed: %v", err)
	}
}

func setupHandler(s *server.Server) http.Handler {
	mux := http.NewServeMux()
	mux.Handle("/metrics", promhttp.Handler())
	mux.Handle("/", s)
	return mux
}
