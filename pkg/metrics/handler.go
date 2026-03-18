package metrics

import (
	"context"
	"net/http"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/collectors"
	"github.com/prometheus/client_golang/prometheus/promhttp"

	"github.com/siderolabs/talos/pkg/machinery/client"
)

func HandlerFor(ctx context.Context, c *client.Client, opts Options) http.Handler {
	reg := prometheus.NewRegistry()

	reg.MustRegister(
		collectors.NewGoCollector(),
		collectors.NewProcessCollector(collectors.ProcessCollectorOpts{}),
		newTalosCollector(c, opts),
	)

	return promhttp.HandlerFor(reg, promhttp.HandlerOpts{})
}
