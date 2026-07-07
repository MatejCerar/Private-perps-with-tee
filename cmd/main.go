package main

import (
	"context"

	"net/http"
	"os"
	"os/signal"
	"syscall"

	"github.com/flare-foundation/go-flare-common/pkg/logger"

	"extension-scaffold/internal/config"
	extension "extension-scaffold/internal/extension"
)

func main() {
	e := extension.New(config.ExtensionPort, config.SignPort)

	// Graceful shutdown.
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, os.Interrupt, syscall.SIGTERM)
	go func() {
		<-sigChan
		e.Stop() // stop the background reconciler
		ctx, cancel := context.WithTimeout(context.Background(), config.TimeoutShutdown)
		defer cancel()
		_ = e.Server.Shutdown(ctx)
		os.Exit(0)
	}()

	logger.Infof("starting extension server on :%d", config.ExtensionPort)
	err := e.Server.ListenAndServe()
	if err != nil && err != http.ErrServerClosed {
		logger.Fatalf("server: %v", err)
	}
}
