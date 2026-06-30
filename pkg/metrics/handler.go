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
	"github.com/siderolabs/talos/pkg/machinery/client"
)

// HandlerFor creates an http.Handler that serves Prometheus metrics for a Talos cluster.
// It registers Go runtime, process, and custom Talos metric collectors.
func HandlerFor(ctx context.Context, c *client.Client, opts Options) http.Handler {
	reg := prometheus.NewRegistry()

	reg.MustRegister(
		collectors.NewGoCollector(),
		collectors.NewProcessCollector(collectors.ProcessCollectorOpts{}),
		newTalosCollector(c, opts),
	)

	return promhttp.HandlerFor(reg, promhttp.HandlerOpts{})
}
