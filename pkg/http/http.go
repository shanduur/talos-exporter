package http

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"time"

	"golang.org/x/sync/errgroup"
)

func Run(ctx context.Context, handlers map[string]http.Handler, addr string) error {
	mux := http.NewServeMux()

	for path, handler := range handlers {
		mux.Handle(path, handler)
	}

	var lc net.ListenConfig

	lis, err := lc.Listen(ctx, "tcp", addr)
	if err != nil {
		return fmt.Errorf("creating listener: %w", err)
	}

	srv := &http.Server{
		Handler: mux,

		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       10 * time.Second,
		WriteTimeout:      60 * time.Second,
		IdleTimeout:       120 * time.Second,

		// propagate ctx to handlers
		BaseContext: func(net.Listener) context.Context {
			return ctx
		},
	}

	g, ctx := errgroup.WithContext(ctx)

	// Run HTTP server
	g.Go(func() error {
		slog.Info("starting HTTP server", "address", lis.Addr().String())

		err := srv.Serve(lis)
		if errors.Is(err, http.ErrServerClosed) {
			return nil
		}

		return err
	})

	// Handle shutdown
	g.Go(func() error {
		<-ctx.Done()

		shutdownCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 10*time.Second)
		defer cancel()

		slog.Info("shutting down HTTP server")

		return srv.Shutdown(shutdownCtx)
	})

	if err := g.Wait(); err != nil {
		return fmt.Errorf("http server failed: %w", err)
	}

	return nil
}
