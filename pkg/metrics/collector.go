// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at http://mozilla.org/MPL/2.0/.

package metrics

import (
	"bytes"
	"context"
	"fmt"
	"log/slog"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/cosi-project/runtime/pkg/resource"
	"github.com/cosi-project/runtime/pkg/resource/meta"
	"github.com/cosi-project/runtime/pkg/safe"
	"github.com/cosi-project/runtime/pkg/state"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/siderolabs/talos/pkg/machinery/client"
	cluster "github.com/siderolabs/talos/pkg/machinery/resources/cluster"
	runtime "github.com/siderolabs/talos/pkg/machinery/resources/runtime"
	"go.yaml.in/yaml/v4"
	"k8s.io/client-go/util/jsonpath"

	"github.com/siderolabs/talos-exporter/internal/version"
)

var (
	talosExporterBuildInfo = prometheus.NewDesc(
		"talos_exporter_build_info",
		"Build information about the talos-exporter binary.",
		[]string{"version", "sha", "name"},
		nil,
	)

	talosVersionInfo = prometheus.NewDesc(
		"talos_version_info",
		"Talos version info",
		[]string{"version", "node"},
		nil,
	)

	talosCOSIResourceCount = prometheus.NewDesc(
		"talos_cosi_resources",
		"Count of COSI resources per type",
		[]string{"meta__node", "meta__namespace", "meta__type"},
		nil,
	)

	talosCollectionError = prometheus.NewDesc(
		"talos_collection_error",
		"Errors encountered while collecting Talos metrics. Value is always 1 for each error in the current scrape.",
		[]string{"stage", "meta__node", "meta__namespace", "meta__type", "error"},
		nil,
	)
)

type talosCollector struct {
	logger *slog.Logger

	nodeCache *nodeCache
	client    *client.Client
	ctx       context.Context
	options   Options
}

type nodeCache struct {
	expiresAt time.Time
	nodes     []string
	sync.RWMutex
}

func newTalosCollector(ctx context.Context, c *client.Client, opts Options) *talosCollector {
	return newTalosCollectorWithCache(ctx, c, opts, &nodeCache{})
}

func newTalosCollectorWithCache(ctx context.Context, c *client.Client, opts Options, cache *nodeCache) *talosCollector {
	return &talosCollector{
		logger:    slog.Default(),
		nodeCache: cache,
		client:    c,
		ctx:       ctx,
		options:   opts,
	}
}

func (c *talosCollector) Describe(ch chan<- *prometheus.Desc) {
	// Dynamic COSI resource metric names and label sets are discovered at scrape time.
	// Sending no descriptors marks this collector as unchecked, avoiding a descriptor cache.
}

func (c *talosCollector) Collect(ch chan<- prometheus.Metric) {
	c.logger.Info("starting metrics collection")
	defer c.logger.Info("finished metrics collection")

	// Emit build info once per scrape.
	ch <- prometheus.MustNewConstMetric(talosExporterBuildInfo, prometheus.GaugeValue, 1,
		version.Tag, version.SHA, version.Name,
	)

	ctx := c.ctx
	if ctx == nil {
		ctx = context.Background()
	}

	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()

	nodes, err := c.discoverNodes(ctx)
	if err != nil {
		c.logger.Error("failed to discover nodes", "error", err)
		emitCollectionError(ch, "discover_nodes", "", "", "", err, c.options.MaxLabelLen)

		return
	}

	c.logger.Debug("discovered nodes", "count", len(nodes), "nodes", nodes)

	for _, node := range nodes {
		if err := c.collectForNode(ctx, node, ch); err != nil {
			c.logger.Error("failed to collect metrics for node", "node", node, "error", err)
		}
	}
}

func (c *talosCollector) discoverNodes(ctx context.Context) ([]string, error) {
	c.nodeCache.RLock()

	if time.Now().Before(c.nodeCache.expiresAt) {
		nodes := c.nodeCache.nodes
		c.nodeCache.RUnlock()
		c.logger.Debug("using cached nodes", "count", len(nodes))

		return nodes, nil
	}

	c.nodeCache.RUnlock()

	c.nodeCache.Lock()
	defer c.nodeCache.Unlock()

	if time.Now().Before(c.nodeCache.expiresAt) {
		return c.nodeCache.nodes, nil
	}

	c.logger.Debug("refreshing node cache")

	items, err := safe.StateListAll[*cluster.Member](ctx, c.client.COSI)
	if err != nil {
		return nil, fmt.Errorf("failed to list cluster members: %w", err)
	}

	seen := make(map[string]struct{})
	var nodes []string

	for res := range items.All() {
		for _, addr := range res.TypedSpec().Addresses {
			node := addr.String()
			if _, ok := seen[node]; ok {
				continue
			}

			seen[node] = struct{}{}
			nodes = append(nodes, node)
		}
	}

	c.nodeCache.nodes = nodes
	c.nodeCache.expiresAt = time.Now().Add(c.options.NodeCacheTTL)

	c.logger.Info("discovered nodes", "count", len(nodes))

	return nodes, nil
}

func (c *talosCollector) collectForNode(ctx context.Context, node string, ch chan<- prometheus.Metric) error {
	c.logger.Debug("collecting metrics for node", "node", node)

	version, err := c.getVersion(ctx, node)
	if err != nil {
		c.logger.Warn("failed to get Talos version", "node", node, "error", err)
		emitCollectionError(ch, "get_version", node, runtime.NamespaceName, runtime.VersionType, err, c.options.MaxLabelLen)

		version = "unknown"
	}

	c.logger.Debug("talos version", "node", node, "version", version)

	ch <- prometheus.MustNewConstMetric(
		talosVersionInfo,
		prometheus.GaugeValue,
		1,
		version, node,
	)

	return c.collectResources(ctx, node, ch)
}

func (c *talosCollector) getVersion(ctx context.Context, node string) (string, error) {
	nodeCtx := client.WithNode(ctx, node)

	r, err := safe.ReaderGet[*runtime.Version](
		nodeCtx, c.client.COSI,
		resource.NewMetadata(runtime.NamespaceName, runtime.VersionType, "version", resource.VersionUndefined),
	)
	if err != nil {
		return "", fmt.Errorf("failed to get version: %w", err)
	}

	return r.TypedSpec().Version, nil
}

func (c *talosCollector) collectResources(ctx context.Context, node string, ch chan<- prometheus.Metric) error {
	nodeCtx := client.WithNode(ctx, node)

	rdList, err := c.client.COSI.List(
		nodeCtx,
		resource.NewMetadata(meta.NamespaceName, meta.ResourceDefinitionType, "", resource.VersionUndefined),
		state.WithListUnmarshalOptions(state.WithSkipProtobufUnmarshal()),
	)
	if err != nil {
		emitCollectionError(ch, "list_resource_definitions", node, meta.NamespaceName, meta.ResourceDefinitionType, err, c.options.MaxLabelLen)

		return fmt.Errorf("failed to list resource definitions: %w", err)
	}

	c.logger.Debug("resource definitions listed",
		"node", node,
		"count", len(rdList.Items))

	var totalResources int

	for _, rd := range rdList.Items {
		rdMeta := rd.Metadata()

		rdDef, err := extractResourceDefinitionSpec(rd)
		if err != nil {
			c.logger.Debug("failed to extract resource definition spec",
				"id", rdMeta.ID(),
				"error", err)
			emitCollectionError(ch, "extract_resource_definition", node, rdMeta.Namespace(), rdMeta.Type(), err, c.options.MaxLabelLen)

			continue
		}

		actualType := rdDef.Type
		actualNamespace := rdDef.DefaultNamespace

		if len(c.options.Namespaces) > 0 && !slices.Contains(c.options.Namespaces, actualNamespace) {
			continue
		}

		if len(c.options.ResourceTypes) > 0 && !slices.Contains(c.options.ResourceTypes, actualType) {
			continue
		}

		resources, err := c.client.COSI.List(
			nodeCtx,
			resource.NewMetadata(rdDef.DefaultNamespace, rdDef.Type, "", resource.VersionUndefined),
			state.WithListUnmarshalOptions(state.WithSkipProtobufUnmarshal()),
		)
		if err != nil {
			c.logger.Warn("failed to list resources",
				"node", node,
				"namespace", actualNamespace,
				"type", actualType,
				"error", err)
			emitCollectionError(ch, "list_resources", node, actualNamespace, actualType, err, c.options.MaxLabelLen)

			continue
		}

		if len(resources.Items) == 0 {
			continue
		}

		totalResources += len(resources.Items)

		if c.options.Aggregate {
			ch <- prometheus.MustNewConstMetric(
				talosCOSIResourceCount,
				prometheus.GaugeValue,
				float64(len(resources.Items)),
				node, actualNamespace, actualType,
			)

			continue
		}

		c.logger.Debug("found resources",
			"node", node,
			"namespace", actualNamespace,
			"type", actualType,
			"count", len(resources.Items))

		labelNames := resourceLabelNames(rdDef.PrintColumns)
		desc := prometheus.NewDesc(
			resourceMetricName(actualNamespace, actualType),
			"COSI resource presence",
			labelNames,
			nil,
		)

		for _, res := range resources.Items {
			labelValues := []string{node, actualNamespace, actualType, res.Metadata().ID()}

			for _, col := range rdDef.PrintColumns {
				value, err := extractJSONPathFromResource(res, col.JSONPath)
				if err != nil {
					c.logger.Debug("failed to extract jsonpath",
						"path", col.JSONPath,
						"resource", res.Metadata().ID(),
						"error", err)
					emitCollectionError(ch, "extract_print_column", node, actualNamespace, actualType, err, c.options.MaxLabelLen)

					value = ""
				}

				labelValues = append(labelValues, sanitizeLabelValue(value, c.options.MaxLabelLen))
			}

			ch <- prometheus.MustNewConstMetric(
				desc,
				prometheus.GaugeValue,
				1,
				labelValues...,
			)
		}
	}

	c.logger.Info("finished collecting resources for node",
		"node", node,
		"total_resources", totalResources)

	return nil
}

type resourceDefinitionSpec struct {
	Type             resource.Type
	DefaultNamespace resource.Namespace
	Sensitivity      string
	PrintColumns     []meta.PrintColumn
}

func extractResourceDefinitionSpec(rd resource.Resource) (*resourceDefinitionSpec, error) {
	spec := rd.Spec()

	yml, err := yaml.Marshal(spec)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal spec: %w", err)
	}

	var unstructured any
	if err := yaml.Unmarshal(yml, &unstructured); err != nil {
		return nil, fmt.Errorf("failed to unmarshal spec: %w", err)
	}

	m, ok := unstructured.(map[string]any)
	if !ok {
		return nil, fmt.Errorf("unexpected spec format")
	}

	result := &resourceDefinitionSpec{}

	if t, ok := m["type"].(string); ok {
		result.Type = t
	}

	if ns, ok := m["defaultNamespace"].(string); ok {
		result.DefaultNamespace = ns
	}

	if sens, ok := m["sensitivity"].(string); ok {
		result.Sensitivity = sens
	}

	if cols, ok := m["printColumns"].([]any); ok {
		for _, col := range cols {
			if colMap, ok := col.(map[string]any); ok {
				pc := meta.PrintColumn{}
				if name, ok := colMap["name"].(string); ok {
					pc.Name = name
				}

				if jp, ok := colMap["jsonPath"].(string); ok {
					pc.JSONPath = jp
				}

				result.PrintColumns = append(result.PrintColumns, pc)
			}
		}
	}

	return result, nil
}

func emitCollectionError(ch chan<- prometheus.Metric, stage, node, namespace, resourceType string, err error, maxLabelLen int) {
	if err == nil {
		return
	}

	ch <- prometheus.MustNewConstMetric(
		talosCollectionError,
		prometheus.GaugeValue,
		1,
		stage,
		node,
		namespace,
		resourceType,
		sanitizeLabelValue(err.Error(), maxLabelLen),
	)
}

func resourceMetricName(namespace resource.Namespace, resourceType resource.Type) string {
	return "talos_cosi_resource_" + sanitizeMetricPart(namespace) + "_" + sanitizeMetricPart(resourceType)
}

func resourceLabelNames(columns []meta.PrintColumn) []string {
	labels := []string{"meta__node", "meta__namespace", "meta__type", "meta__id"}
	seen := make(map[string]int, len(labels)+len(columns))

	for _, label := range labels {
		seen[label] = 1
	}

	for _, col := range columns {
		base := sanitizeLabelName(col.Name)
		label := base

		for seen[label] > 0 {
			seen[base]++
			label = fmt.Sprintf("%s_%d", base, seen[base])
		}

		if seen[base] == 0 {
			seen[base] = 1
		}

		seen[label] = 1
		labels = append(labels, label)
	}

	return labels
}

func sanitizeMetricPart(s string) string {
	return sanitizeIdentifier(s, true)
}

func sanitizeLabelName(s string) string {
	return sanitizeIdentifier(s, true)
}

func sanitizeIdentifier(s string, lower bool) string {
	var b strings.Builder
	lastUnderscore := false

	for _, r := range s {
		if lower {
			r = asciiLower(r)
		}

		if isIdentifierRune(r) {
			b.WriteRune(r)
			lastUnderscore = false

			continue
		}

		if !lastUnderscore {
			b.WriteByte('_')
			lastUnderscore = true
		}
	}

	result := strings.Trim(b.String(), "_")
	if result == "" {
		return "unknown"
	}

	first, _ := utf8FirstRune(result)
	if !isIdentifierFirstRune(first) {
		return "_" + result
	}

	return result
}

func utf8FirstRune(s string) (rune, int) {
	for i, r := range s {
		return r, i
	}

	return 0, 0
}

func isIdentifierRune(r rune) bool {
	return r == '_' || isASCIILower(r) || isASCIIUpper(r) || isASCIIDigit(r)
}

func isIdentifierFirstRune(r rune) bool {
	return r == '_' || isASCIILower(r) || isASCIIUpper(r)
}

func asciiLower(r rune) rune {
	if isASCIIUpper(r) {
		return r + 'a' - 'A'
	}

	return r
}

func isASCIIUpper(r rune) bool {
	return r >= 'A' && r <= 'Z'
}

func isASCIILower(r rune) bool {
	return r >= 'a' && r <= 'z'
}

func isASCIIDigit(r rune) bool {
	return r >= '0' && r <= '9'
}

func extractJSONPathFromResource(res resource.Resource, jsonPathExpr string) (string, error) {
	spec := res.Spec()

	yml, err := yaml.Marshal(spec)
	if err != nil {
		return "", fmt.Errorf("failed to marshal spec: %w", err)
	}

	var unstructured any
	if err := yaml.Unmarshal(yml, &unstructured); err != nil {
		return "", fmt.Errorf("failed to unmarshal spec: %w", err)
	}

	return extractJSONPath(unstructured, jsonPathExpr)
}

func extractJSONPath(data any, jsonPathExpr string) (string, error) {
	expr := jsonpath.New("extract")
	if err := expr.Parse(jsonPathExpr); err != nil {
		return "", fmt.Errorf("failed to parse jsonpath: %w", err)
	}

	expr = expr.AllowMissingKeys(true)

	var buf bytes.Buffer
	if err := expr.Execute(&buf, data); err != nil {
		return "", fmt.Errorf("failed to execute jsonpath: %w", err)
	}

	return strings.TrimSpace(buf.String()), nil
}

func sanitizeLabelValue(s string, maxLen int) string {
	s = strings.ReplaceAll(s, "[\"", "[")
	s = strings.ReplaceAll(s, "]\"", "]")
	s = strings.ReplaceAll(s, "\"", "'")

	if maxLen > 0 && len(s) > maxLen {
		s = s[:maxLen] + "..."
	}

	return s
}
