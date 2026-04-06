package main

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/joho/godotenv"
	"github.com/robfig/cron/v3"

	"github.com/balli/aws-s3-gda-cleaner/internal/approval"
	"github.com/balli/aws-s3-gda-cleaner/internal/config"
	"github.com/balli/aws-s3-gda-cleaner/internal/deleter"
	"github.com/balli/aws-s3-gda-cleaner/internal/engine"
	"github.com/balli/aws-s3-gda-cleaner/internal/notifier"
	"github.com/balli/aws-s3-gda-cleaner/internal/scanner"
)

func main() {
	// Structured logging
	slog.SetDefault(slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{
		Level: slog.LevelInfo,
	})))

	slog.Info("starting S3 GDA Cleaner")

	// Load .env file if present (ignored if missing)
	if err := godotenv.Load(); err != nil {
		slog.Debug(".env file not loaded", "error", err)
	}

	// Load configuration
	cfg, err := config.Load()
	if err != nil {
		slog.Error("failed to load configuration", "error", err)
		os.Exit(1)
	}
	slog.Info("configuration loaded",
		"bucket", cfg.S3Bucket,
		"prefix", cfg.S3Prefix,
		"local_path", cfg.LocalPath,
		"deletion_behavior", cfg.DeletionBehavior,
		"stale_time", cfg.StaleTime,
		"cron", cfg.CronExpression,
	)

	// Initialize AWS S3 client
	ctx := context.Background()
	awsCfg, err := awsconfig.LoadDefaultConfig(ctx, awsconfig.WithRegion(cfg.AWSRegion))
	if err != nil {
		slog.Error("failed to load AWS config", "error", err)
		os.Exit(1)
	}
	s3Client := s3.NewFromConfig(awsCfg)

	// Initialize components
	scnr := scanner.New(s3Client, cfg.S3Bucket, cfg.S3Prefix)
	del := deleter.New(s3Client, cfg.S3Bucket)
	ntf := notifier.NewSMTPNotifier(
		cfg.SMTPHost, cfg.SMTPPort,
		cfg.SMTPUsername, cfg.SMTPPassword,
		cfg.SMTPFrom, cfg.SMTPTo,
	)

	// Create engine (approval server wired below)
	eng := engine.New(cfg, scnr, del, ntf)

	// Create approval server with engine's deletion handler
	approvalSrv := approval.NewServer(cfg.ApprovalTokenLifetime, eng.ExecuteApprovedDeletion)
	eng.SetApprovalServer(approvalSrv)

	// Start HTTP server
	httpServer := &http.Server{
		Addr:              fmt.Sprintf(":%d", cfg.ListenPort),
		Handler:           approvalSrv.Handler(),
		ReadHeaderTimeout: 10 * time.Second,
	}
	go func() {
		slog.Info("HTTP server starting", "port", cfg.ListenPort)
		if err := httpServer.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			slog.Error("HTTP server error", "error", err)
			os.Exit(1)
		}
	}()

	// Set up cron scheduler
	c := cron.New()
	_, err = c.AddFunc(cfg.CronExpression, func() {
		eng.Run(context.Background())
	})
	if err != nil {
		slog.Error("failed to add cron job", "error", err, "expression", cfg.CronExpression)
		os.Exit(1)
	}
	c.Start()
	slog.Info("cron scheduler started", "expression", cfg.CronExpression)

	// Wait for shutdown signal
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGTERM, syscall.SIGINT)
	sig := <-sigCh
	slog.Info("received shutdown signal", "signal", sig)

	// Graceful shutdown
	c.Stop()
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	if err := httpServer.Shutdown(shutdownCtx); err != nil {
		slog.Error("HTTP server shutdown error", "error", err)
	}
	slog.Info("shutdown complete")
}
