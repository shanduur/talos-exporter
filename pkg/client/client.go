// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at http://mozilla.org/MPL/2.0/.

// Package client provides a convenience wrapper for creating Talos API clients.
package client

import (
	"context"
	"fmt"

	"github.com/siderolabs/talos/pkg/machinery/client"
	clientconfig "github.com/siderolabs/talos/pkg/machinery/client/config"
)

// TalosClientOptions holds parameters for creating a Talos API client.
type TalosClientOptions struct {
	TalosConfig     string
	SideroV1KeysDir string
	Cluster         string
	Context         string
	Endpoints       []string
}

// New creates a new Talos API client from the provided options.
// It reads the Talos configuration, applies endpoint/cluster/context overrides,
// and returns an initialized client ready for use.
func New(
	ctx context.Context,
	opts TalosClientOptions,
) (*client.Client, error) {
	cfg, err := clientconfig.Open(opts.TalosConfig)
	if err != nil {
		return nil, fmt.Errorf("opening Talos config: %w", err)
	}

	o := []client.OptionFunc{
		client.WithConfig(cfg),
		client.WithDefaultGRPCDialOptions(),
		client.WithSideroV1KeysDir(clientconfig.CustomSideroV1KeysDirPath(opts.SideroV1KeysDir)),
	}

	if len(opts.Endpoints) > 0 {
		o = append(o, client.WithEndpoints(opts.Endpoints...))
	}

	if opts.Cluster != "" {
		o = append(o, client.WithCluster(opts.Cluster))
	}

	if opts.Context != "" {
		o = append(o, client.WithContextName(opts.Context))
	}

	c, err := client.New(ctx, o...)
	if err != nil {
		return nil, fmt.Errorf("creating client: %w", err)
	}

	return c, nil
}
