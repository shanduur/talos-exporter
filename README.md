# Talos Exporter

Prometheus exporter for [Talos Linux](https://www.talos.dev/) cluster metrics.
Discovers cluster nodes, collects COSI resources, and exposes them as Prometheus metrics.

## Metrics

| Name | Description |
|------|-------------|
| `talos_version_info` | Talos version per node (gauge) |
| `talos_cosi_resources` | Count of COSI resources per namespace/type (gauge) |
| `talos_cosi_resource_<namespace>_<type>` | Presence of individual COSI resources with metadata and print-column labels (gauge, per-resource mode) |
| `talos_collection_error` | Collection errors observed during the current scrape (gauge) |
| `talos_grpc_client_*` | Talos gRPC client request counters and latency histograms |

## Modes

### Per-resource (default)
Each COSI resource type becomes its own metric family with stable metadata labels and sanitized print-column labels.

### Aggregate (`--aggregate`)
Emits one metric per resource type with a count of resources, reducing cardinality.

## Development

### Prerequisites

- Go 1.26+
- Docker (for `make` targets)
- Make

### Commands

```bash
# Run linters
make lint

# Run tests
make unit-tests

# Format code
make fmt

# Build
make talos-exporter
```

## License

[Mozilla Public License 2.0](LICENSE)
