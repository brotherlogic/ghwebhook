# Build stage
FROM golang:1.26-alpine as builder

WORKDIR /app

# Copy go mod and sum files
COPY go.mod go.sum ./
RUN go mod download

# Copy the source code
COPY . .

# Build the binaries
RUN CGO_ENABLED=0 GOOS=linux go build -a -installsuffix cgo -o /bin/ghwebhook .
RUN CGO_ENABLED=0 GOOS=linux go build -a -installsuffix cgo -o /bin/prober ./cmd/prober

# Final stage
FROM alpine:latest

RUN apk --no-cache add ca-certificates

WORKDIR /root/

# Copy the binaries from the builder stage
COPY --from=builder /bin/ghwebhook .
COPY --from=builder /bin/prober .

# Expose ports
EXPOSE 8080 50051

# Run the binary
CMD ["./ghwebhook"]

