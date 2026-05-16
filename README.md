# GHWebhook

GHWebhook is a GitHub webhook proxy for Kubernetes. It centralizes webhook handling and routes events to registered gRPC handlers within the cluster.

## Features
- **Centralized Ingress:** Single entry point for all GitHub webhooks.
- **gRPC Routing:** Routes JSON webhooks to handlers as type-safe Proto objects.
- **Reliable Delivery:** Exponential backoff retries and a "3 strikes" removal policy.
- **Multi-tenancy:** Supports multiple services registering for the same repository.
- **Security:** HMAC-SHA256 signature validation for all incoming requests.

## Production Deployment Requirements

The following requirements must be met to deploy GHWebhook in a production Kubernetes environment (managed by `brotherlogic/prod`).

### 1. Environment Variables
- `GH_WEBHOOK_SECRET`: **(Required)** The secret key configured in GitHub for webhook signature validation.

### 2. Networking & Ports
- **HTTP Ingress (Port 8080):** 
  - Path: `/webhook` (POST) for receiving GitHub events.
  - Path: `/healthz` (GET) for liveness/readiness probes.
- **gRPC Server (Port 50051):**
  - Used by internal services to register for repositories.
  - Implements the standard gRPC Health Check service.

### 3. External Dependencies
- **PStore:** The proxy depends on `github.com/brotherlogic/pstore` for registration persistence.
  - Default Endpoint: `pstore.pstore:8080` (gRPC).

### 4. Kubernetes Probes
- **Liveness Probe (HTTP):** `GET /healthz` on port 8080.
- **Readiness Probe (gRPC):** Use standard gRPC health check on port 50051 for service `ghwebhook`.

## Development
- **Proto Generation:** `make proto`
- **Testing:** `go test ./...`
- **Docker Build:** Handled automatically by GitHub Actions on push to `main`.
