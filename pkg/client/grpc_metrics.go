// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at http://mozilla.org/MPL/2.0/.

package client

import (
	"context"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/stats"
	"google.golang.org/grpc/status"
)

type grpcMetricsContextKey struct{}

type grpcMetricsContext struct {
	method string
	start  time.Time
}

var (
	grpcClientStartedTotal = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "talos_grpc_client_started_total",
			Help: "Total number of Talos gRPC client RPCs started.",
		},
		[]string{"grpc_method"},
	)

	grpcClientHandledTotal = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "talos_grpc_client_handled_total",
			Help: "Total number of Talos gRPC client RPCs completed by status code.",
		},
		[]string{"grpc_method", "grpc_code"},
	)

	grpcClientHandlingSeconds = prometheus.NewHistogramVec(
		prometheus.HistogramOpts{
			Name:    "talos_grpc_client_handling_seconds",
			Help:    "Talos gRPC client RPC handling duration in seconds.",
			Buckets: prometheus.DefBuckets,
		},
		[]string{"grpc_method", "grpc_code"},
	)
)

// GRPCCollectors returns Prometheus collectors for Talos client gRPC instrumentation.
func GRPCCollectors() []prometheus.Collector {
	return []prometheus.Collector{
		grpcClientStartedTotal,
		grpcClientHandledTotal,
		grpcClientHandlingSeconds,
	}
}

type grpcStatsHandler struct{}

func (grpcStatsHandler) TagRPC(ctx context.Context, info *stats.RPCTagInfo) context.Context {
	return context.WithValue(ctx, grpcMetricsContextKey{}, grpcMetricsContext{
		method: info.FullMethodName,
		start:  time.Now(),
	})
}

func (grpcStatsHandler) HandleRPC(ctx context.Context, stat stats.RPCStats) {
	metricsCtx, _ := ctx.Value(grpcMetricsContextKey{}).(grpcMetricsContext)
	if metricsCtx.method == "" {
		return
	}

	switch s := stat.(type) {
	case *stats.Begin:
		grpcClientStartedTotal.WithLabelValues(metricsCtx.method).Inc()
	case *stats.End:
		code := codes.OK
		if s.Error != nil {
			code = status.Code(s.Error)
		}

		codeLabel := code.String()
		grpcClientHandledTotal.WithLabelValues(metricsCtx.method, codeLabel).Inc()
		grpcClientHandlingSeconds.WithLabelValues(metricsCtx.method, codeLabel).Observe(time.Since(metricsCtx.start).Seconds())
	}
}

func (grpcStatsHandler) TagConn(ctx context.Context, _ *stats.ConnTagInfo) context.Context {
	return ctx
}

func (grpcStatsHandler) HandleConn(context.Context, stats.ConnStats) {}
