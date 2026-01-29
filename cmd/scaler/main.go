package main

import (
	"context"
	"log/slog"
	"net"
	"os"
	"os/signal"
	"syscall"

	"google.golang.org/grpc"
	"google.golang.org/grpc/health"
	"google.golang.org/grpc/health/grpc_health_v1"
	"google.golang.org/grpc/reflection"

	"github.com/jorgelopez/rabbitmq-burst-scaler/internal/scaler"
	pb "github.com/jorgelopez/rabbitmq-burst-scaler/proto"
)

func main() {
	// Setup logger
	logLevel := slog.LevelInfo
	if os.Getenv("LOG_LEVEL") == "debug" {
		logLevel = slog.LevelDebug
	}

	logger := slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{
		Level: logLevel,
	}))

	slog.SetDefault(logger)

	// Get port from environment or default to 9090
	port := os.Getenv("GRPC_PORT")
	if port == "" {
		port = "9090"
	}

	// Create listener
	listener, err := net.Listen("tcp", ":"+port)
	if err != nil {
		logger.Error("failed to create listener", "error", err, "port", port)
		os.Exit(1)
	}

	// Create gRPC server
	grpcServer := grpc.NewServer()

	// Create and register the burst scaler
	burstScaler := scaler.New(logger)
	pb.RegisterExternalScalerServer(grpcServer, burstScaler)

	// Register health service
	healthServer := health.NewServer()
	grpc_health_v1.RegisterHealthServer(grpcServer, healthServer)
	healthServer.SetServingStatus("", grpc_health_v1.HealthCheckResponse_SERVING)

	// Enable reflection for debugging
	reflection.Register(grpcServer)

	// Setup graceful shutdown
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, syscall.SIGINT, syscall.SIGTERM)

	go func() {
		sig := <-sigChan
		logger.Info("received shutdown signal", "signal", sig)

		// Set health status to not serving
		healthServer.SetServingStatus("", grpc_health_v1.HealthCheckResponse_NOT_SERVING)

		// Gracefully stop the gRPC server
		grpcServer.GracefulStop()

		// Close the scaler
		if err := burstScaler.Close(); err != nil {
			logger.Error("error closing scaler", "error", err)
		}

		cancel()
	}()

	logger.Info("starting gRPC server", "port", port)

	if err := grpcServer.Serve(listener); err != nil {
		logger.Error("gRPC server error", "error", err)
		os.Exit(1)
	}

	<-ctx.Done()
	logger.Info("server shutdown complete")
}
