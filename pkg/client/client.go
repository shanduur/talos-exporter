package client

import (
	"context"
	"fmt"

	"github.com/siderolabs/talos/pkg/machinery/client"
	clientconfig "github.com/siderolabs/talos/pkg/machinery/client/config"
)

type TalosClientOptions struct {
	TalosConfig     string
	SideroV1KeysDir string
	Cluster         string
	Context         string
	Endpoints       []string
}

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
