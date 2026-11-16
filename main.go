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
)

func main() {
	if len(os.Args) > 1 && os.Args[1] == "healthcheck" {
		client := http.Client{Timeout: 4 * time.Second}
		response, err := client.Get("http://127.0.0.1:8000/healthz")
		if err != nil {
			os.Exit(1)
		}
		response.Body.Close()
		if response.StatusCode != 200 {
			os.Exit(1)
		}
		return
	}
	logger := slog.New(slog.NewJSONHandler(os.Stdout, nil))
	slog.SetDefault(logger)
	cfg, err := loadConfig()
	if err != nil {
		logger.Error("configuration invalid", "error", err)
		os.Exit(1)
	}
	app, err := NewApp(cfg, logger)
	if err != nil {
		logger.Error("startup failed", "error", err)
		os.Exit(1)
	}
	defer app.Close()
	server := &http.Server{Addr: cfg.Addr, Handler: app, ReadHeaderTimeout: 10 * time.Second, ReadTimeout: cfg.UploadTimeout + 30*time.Second, IdleTimeout: 60 * time.Second, MaxHeaderBytes: 32 << 10}
	stopping, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	shutdownDone := make(chan struct{})
	go func() {
		defer close(shutdownDone)
		<-stopping.Done()
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		if err := server.Shutdown(ctx); err != nil {
			logger.Warn("forced shutdown", "error", err)
			server.Close()
		}
	}()
	logger.Info("SWEET camp started", "version", version, "listen", cfg.Addr, "upload_slots", cfg.MaxConcurrentUploads, "queue_capacity", cfg.MaxQueuedUploads, "upload_mbps", cfg.UploadBytesPerSecond*8/1000000, "download_mbps", cfg.DownloadBytesPerSecond*8/1000000)
	if err := server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		logger.Error("server stopped", "error", err)
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	<-shutdownDone
}
