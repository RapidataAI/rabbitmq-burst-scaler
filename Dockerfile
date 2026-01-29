# Build stage
FROM golang:1.22-alpine AS builder

WORKDIR /app

# Install build dependencies
RUN apk add --no-cache git

# Copy go mod files
COPY go.mod go.sum ./

# Download dependencies
RUN go mod download

# Copy source code
COPY . .

# Build the binary
RUN CGO_ENABLED=0 GOOS=linux go build -ldflags="-w -s" -o /burst-scaler ./cmd/scaler

# Final stage
FROM alpine:3.19

# Add ca-certificates for HTTPS
RUN apk add --no-cache ca-certificates

# Create non-root user
RUN addgroup -g 1000 scaler && \
    adduser -u 1000 -G scaler -s /bin/sh -D scaler

WORKDIR /app

# Copy binary from builder
COPY --from=builder /burst-scaler /app/burst-scaler

# Use non-root user
USER scaler

# Expose gRPC port
EXPOSE 9090

ENTRYPOINT ["/app/burst-scaler"]
