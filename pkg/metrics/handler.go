// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at http://mozilla.org/MPL/2.0/.

package metrics

import (
	"context"
	"net/http"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/collectors"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	machineryclient "github.com/siderolabs/talos/pkg/machinery/client"

	exporterclient "github.com/siderolabs/talos-exporter/pkg/client"
)

// HandlerFor creates an http.Handler that serves Prometheus metrics for a Talos cluster.
// It registers Go runtime, process, and custom Talos metric collectors.
func HandlerFor(ctx context.Context, c *machineryclient.Client, opts Options) http.Handler {
	cache := &nodeCache{}

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		collectCtx, cancel := mergeContexts(ctx, r.Context())
		defer cancel()

		reg := prometheus.NewRegistry()
		reg.MustRegister(
			collectors.NewGoCollector(),
			collectors.NewProcessCollector(collectors.ProcessCollectorOpts{}),
			newTalosCollectorWithCache(collectCtx, c, opts, cache),
		)
		reg.MustRegister(exporterclient.GRPCCollectors()...)

		promhttp.HandlerFor(reg, promhttp.HandlerOpts{}).ServeHTTP(w, r)
	})
}

func mergeContexts(parent, request context.Context) (context.Context, context.CancelFunc) {
	if parent == nil {
		parent = context.Background()
	}

	ctx, cancel := context.WithCancel(request)

	go func() {
		select {
		case <-parent.Done():
			cancel()
		case <-ctx.Done():
		}
	}()

	return ctx, cancel
}
