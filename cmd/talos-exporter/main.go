// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at http://mozilla.org/MPL/2.0/.

// Package main implements the talos-exporter entrypoint.
package main

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"time"

	"github.com/siderolabs/talos/pkg/machinery/constants"
	"github.com/spf13/pflag"

	"github.com/siderolabs/talos-exporter/pkg/client"
	"github.com/siderolabs/talos-exporter/pkg/metrics"
)

// Options represents the full set of talos-exporter configuration.
type Options struct {
	HTTPListenAddress string
	client.TalosClientOptions
	MetricsOptions metrics.Options
	NodeCacheTTL   time.Duration
	LogLevel       slog.Level
}

func main() {
	var (
		opts        Options
		logLevelStr string
	)

	pflag.StringVar(
		&opts.TalosConfig, "talosconfig", "",
		fmt.Sprintf(
			"The location to save the generated Talos configuration file to. Defaults to '%s' env variable if set, otherwise '%s' and '%s' in order.",
			constants.TalosConfigEnvVar,
			filepath.Join("$HOME", constants.TalosDir, constants.TalosconfigFilename),
			filepath.Join(constants.ServiceAccountMountPath, constants.TalosconfigFilename),
		),
	)
	pflag.StringVar(
		&opts.SideroV1KeysDir, "siderov1-keys-dir", "",
		fmt.Sprintf(
			"The path to the SideroV1 auth PGP keys directory. Defaults to '%s' env variable if set, otherwise '%s'. Only valid for Contexts that use SideroV1 auth.",
			constants.SideroV1KeysDirEnvVar,
			filepath.Join("$HOME", constants.TalosDir, constants.SideroV1KeysDir),
		),
	)
	pflag.StringVar(&opts.Cluster, "cluster", "", "Cluster to connect to if a proxy endpoint is used.")
	pflag.StringVar(&opts.Context, "context", "", "Context to be used in command")
	pflag.StringSliceVar(&opts.Endpoints, "endpoints", nil, "Comma-separated list of API endpoints. Defaults to endpoints from Talos config.")
	pflag.StringVar(&opts.HTTPListenAddress, "listen", ":9090", "HTTP listen address")
	pflag.DurationVar(&opts.NodeCacheTTL, "node-cache-ttl", 30*time.Second, "How long to cache discovered nodes")
	pflag.StringVar(&logLevelStr, "log-level", "info", "Log level (debug, info, warn, error)")

	pflag.StringSliceVar(&opts.MetricsOptions.Namespaces, "namespace", nil, "Only collect resources from these namespaces (empty = all namespaces)")
	pflag.StringSliceVar(&opts.MetricsOptions.ResourceTypes, "resource-type", nil, "Only collect resources of these types (empty = all types)")
	pflag.IntVar(&opts.MetricsOptions.MaxLabelLen, "max-label-len", 64, "Maximum length of label values (0 = unlimited)")
	pflag.BoolVar(&opts.MetricsOptions.Aggregate, "aggregate", false, "Aggregate resources by type instead of emitting per-resource metrics")

	pflag.Parse()

	var logLevel slog.Level

	switch logLevelStr {
	case "debug":
		logLevel = slog.LevelDebug
	case "info":
		logLevel = slog.LevelInfo
	case "warn":
		logLevel = slog.LevelWarn
	case "error":
		logLevel = slog.LevelError
	default:
		fmt.Fprintf(os.Stderr, "unknown log level: %s\n", logLevelStr)
		os.Exit(1)
	}

	opts.LogLevel = logLevel

	slog.SetDefault(slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{
		Level: logLevel,
	})))

	if err := run(context.Background(), opts); err != nil {
		slog.Error("failed to run", "error", err)
		os.Exit(1)
	}
}
