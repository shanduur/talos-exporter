// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at http://mozilla.org/MPL/2.0/.

package metrics

import (
	"bytes"
	"context"
	"fmt"
	"log/slog"
	"regexp"
	"slices"
	"strings"
	"sync"
	"time"
	"unicode"

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
)

var (
	talosVersionInfo = prometheus.NewDesc(
		"talos_version_info",
		"Talos version info",
		[]string{"version", "node"},
		nil,
	)

	talosCOSIResourceCount = prometheus.NewDesc(
		"talos_cosi_resources",
		"Count of COSI resources per type",
		[]string{"meta_node", "meta_namespace", "meta_type"},
		nil,
	)

	metricDescriptorRE = regexp.MustCompile(`[A-Z]`)
)

type talosCollector struct {
	nodeCache struct {
		expiresAt time.Time
		nodes     []string
		sync.RWMutex
	}
	metricDescriptors descriptorsCache
	client            *client.Client
	options           Options
}

type descriptorsCache struct {
	descriptors map[string]*prometheus.Desc
	sync.RWMutex
}

func newTalosCollector(c *client.Client, opts Options) *talosCollector {
	return &talosCollector{
		client:  c,
		options: opts,
		metricDescriptors: descriptorsCache{
			descriptors: make(map[string]*prometheus.Desc),
		},
	}
}

func (c *talosCollector) Describe(ch chan<- *prometheus.Desc) {
	ch <- talosVersionInfo

	ch <- talosCOSIResourceCount

	c.metricDescriptors.RLock()
	defer c.metricDescriptors.RUnlock()

	for _, desc := range c.metricDescriptors.descriptors {
		ch <- desc
	}
}

func (c *talosCollector) Collect(ch chan<- prometheus.Metric) {
	slog.Info("starting metrics collection")
	defer slog.Info("finished metrics collection")

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	nodes, err := c.discoverNodes(ctx)
	if err != nil {
		slog.Error("failed to discover nodes", "error", err)

		return
	}

	slog.Debug("discovered nodes", "count", len(nodes), "nodes", nodes)

	for _, node := range nodes {
		if err := c.collectForNode(ctx, node, ch); err != nil {
			slog.Error("failed to collect metrics for node", "node", node, "error", err)
		}
	}
}

func (c *talosCollector) discoverNodes(ctx context.Context) ([]string, error) {
	c.nodeCache.RLock()

	if time.Now().Before(c.nodeCache.expiresAt) {
		nodes := c.nodeCache.nodes
		c.nodeCache.RUnlock()
		slog.Debug("using cached nodes", "count", len(nodes))

		return nodes, nil
	}

	c.nodeCache.RUnlock()

	c.nodeCache.Lock()
	defer c.nodeCache.Unlock()

	if time.Now().Before(c.nodeCache.expiresAt) {
		return c.nodeCache.nodes, nil
	}

	slog.Debug("refreshing node cache")

	items, err := safe.StateListAll[*cluster.Member](ctx, c.client.COSI)
	if err != nil {
		return nil, fmt.Errorf("failed to list cluster members: %w", err)
	}

	var nodes []string

	for res := range items.All() {
		for _, addr := range res.TypedSpec().Addresses {
			nodes = append(nodes, addr.String())
		}
	}

	c.nodeCache.nodes = nodes
	c.nodeCache.expiresAt = time.Now().Add(c.options.NodeCacheTTL)

	slog.Info("discovered nodes", "count", len(nodes))

	return nodes, nil
}

func (c *talosCollector) collectForNode(ctx context.Context, node string, ch chan<- prometheus.Metric) error {
	slog.Debug("collecting metrics for node", "node", node)

	version, err := c.getVersion(ctx, node)
	if err != nil {
		slog.Warn("failed to get Talos version", "node", node, "error", err)

		version = "unknown"
	}

	slog.Debug("talos version", "node", node, "version", version)

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
	rdList, err := c.client.COSI.List(
		ctx,
		resource.NewMetadata(meta.NamespaceName, meta.ResourceDefinitionType, "", resource.VersionUndefined),
		state.WithListUnmarshalOptions(state.WithSkipProtobufUnmarshal()),
	)
	if err != nil {
		return fmt.Errorf("failed to list resource definitions: %w", err)
	}

	slog.Debug("resource definitions listed",
		"node", node,
		"count", len(rdList.Items))

	var totalResources int

	for _, rd := range rdList.Items {
		rdMeta := rd.Metadata()

		rdDef, err := extractResourceDefinitionSpec(rd)
		if err != nil {
			slog.Debug("failed to extract resource definition spec",
				"id", rdMeta.ID(),
				"error", err)

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
			ctx,
			resource.NewMetadata(rdDef.DefaultNamespace, rdDef.Type, "", resource.VersionUndefined),
			state.WithListUnmarshalOptions(state.WithSkipProtobufUnmarshal()),
		)
		if err != nil {
			slog.Warn("failed to list resources",
				"node", node,
				"namespace", actualNamespace,
				"type", actualType,
				"error", err)

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

		slog.Debug("found resources",
			"node", node,
			"namespace", actualNamespace,
			"type", actualType,
			"count", len(resources.Items))

		labels := []string{"meta_node", "meta_namespace", "meta_type", "meta_id"}
		for _, col := range rdDef.PrintColumns {
			labels = append(labels, toSnakeCase(col.Name))
		}

		desc := c.getOrCreateDescriptor(actualNamespace, actualType, rdDef.PrintColumns)

		for _, res := range resources.Items {
			labelValues := []string{node, actualNamespace, actualType, res.Metadata().ID()}

			for _, col := range rdDef.PrintColumns {
				value, err := extractJSONPathFromResource(res, col.JSONPath)
				if err != nil {
					slog.Debug("failed to extract jsonpath",
						"path", col.JSONPath,
						"resource", res.Metadata().ID(),
						"error", err)

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

	slog.Info("finished collecting resources for node",
		"node", node,
		"total_resources", totalResources)

	return nil
}

func (c *talosCollector) getOrCreateDescriptor(namespace, resourceType string, columns []meta.PrintColumn) *prometheus.Desc {
	labelNames := make([]string, 4, 4+len(columns))
	labelNames[0] = "meta__node"
	labelNames[1] = "meta__namespace"
	labelNames[2] = "meta__type"
	labelNames[3] = "meta__id"

	for _, col := range columns {
		labelNames = append(labelNames, toSnakeCase(col.Name))
	}

	key := fmt.Sprintf("%s/%s/%s", namespace, resourceType, labelNames)

	c.metricDescriptors.RLock()
	desc, ok := c.metricDescriptors.descriptors[key]
	c.metricDescriptors.RUnlock()

	if ok {
		return desc
	}

	c.metricDescriptors.Lock()
	defer c.metricDescriptors.Unlock()

	if desc, ok = c.metricDescriptors.descriptors[key]; ok {
		return desc
	}

	desc = prometheus.NewDesc(
		"talos_cosi_resource",
		"COSI resource presence",
		labelNames,
		nil,
	)

	c.metricDescriptors.descriptors[key] = desc

	return desc
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

func toSnakeCase(s string) string {
	if s == "" {
		return s
	}

	var result strings.Builder

	runes := []rune(s)

	for i, r := range runes {
		if unicode.IsUpper(r) {
			if i > 0 && (i+1 < len(runes) && unicode.IsLower(runes[i+1]) || unicode.IsLower(runes[i-1])) {
				result.WriteRune('_')
			}

			result.WriteRune(unicode.ToLower(r))
		} else {
			result.WriteRune(r)
		}
	}

	return metricDescriptorRE.ReplaceAllStringFunc(result.String(), func(match string) string {
		return "_" + strings.ToLower(match)
	})
}
