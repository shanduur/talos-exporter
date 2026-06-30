package main

import (
	"context"
	"fmt"
	"net/http"

	"github.com/siderolabs/talos-exporter/pkg/client"
	pkghttp "github.com/siderolabs/talos-exporter/pkg/http"
	"github.com/siderolabs/talos-exporter/pkg/metrics"
)

func run(ctx context.Context, opts Options) error {
	cli, err := client.New(ctx, opts.TalosClientOptions)
	if err != nil {
		return fmt.Errorf("creating client: %w", err)
	}
	defer func() {
		if err := cli.Close(); err != nil {
			fmt.Printf("failed to close client: %v\n", err)
		}
	}()

	metricsOpts := metrics.Options{
		NodeCacheTTL:  opts.NodeCacheTTL,
		Namespaces:    opts.MetricsOptions.Namespaces,
		ResourceTypes: opts.MetricsOptions.ResourceTypes,
		MaxLabelLen:   opts.MetricsOptions.MaxLabelLen,
		Aggregate:     opts.MetricsOptions.Aggregate,
	}

	handlers := map[string]http.Handler{
		"/metrics": metrics.HandlerFor(ctx, cli, metricsOpts),
	}

	if err := pkghttp.Run(ctx, handlers, opts.HTTPListenAddress); err != nil {
		return fmt.Errorf("running HTTP server: %w", err)
	}

	return nil
}
