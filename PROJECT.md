# Github Webhook

GH Webhook is a GitHub webhook proxy for Kubernetes. It provides a centralized webhook handler in a cluster and proxies updates to relevant downstream handlers. It converts incoming JSON webhooks into Proto objects, enabling gRPC-based management of updates.

## Architecture & Design

### Webhook Types
The proxy handles various incoming GitHub webhooks, currently focusing on:
* PR Creation / Updates
* Issue Creation / Updates

The proxy is designed for extensibility, using a generic gRPC interface that can be easily expanded to support new event types.

### Service Registration
Services register themselves with the proxy via a gRPC Registration API.
* **Mapping:** 1-to-N (multiple services can register for the same repository).
* **Persistence:** Registrations are persisted using `github.com/brotherlogic/pstore` to ensure continuity across proxy restarts.
* **Cadence:** Services are expected to re-register at a regular cadence to maintain their active status.

### Security
All incoming webhooks are validated using **GitHub Webhook Secrets** (HMAC-SHA256). The proxy verifies the `X-Hub-Signature-256` header before processing any payload.

### Reliable Delivery
* **Retries:** Each delivery attempt includes a retry loop with exponential backoff to handle transient network issues.
* **Failure Policy:** A registration is automatically dropped if the proxy records three consecutive delivery failures (after all retries for a given webhook have exhausted).

## Implementation Roadmap

1. **Core Proto Definitions:** Define the gRPC service and messages using `oneof` for event payloads.
2. **Persistent Registration System:** Implement the `pstore`-backed registration API.
3. **Webhook Ingress & Security:** HTTP server with signature validation.
4. **Routing Engine:** Logic for mapping repos to services and JSON-to-Proto conversion.
5. **Reliable Delivery:** Implementation of the retry logic and "3 strikes" policy.
6. **K8s Integration:** Manifests for deployment and secret management.
