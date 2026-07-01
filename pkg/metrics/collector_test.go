// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at http://mozilla.org/MPL/2.0/.

package metrics //nolint:testpackage

import (
	"context"
	"errors"
	"fmt"
	"net/netip"
	"strings"
	"testing"
	"time"

	"github.com/cosi-project/runtime/pkg/resource"
	"github.com/cosi-project/runtime/pkg/resource/meta"
	"github.com/cosi-project/runtime/pkg/state"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/siderolabs/talos/pkg/machinery/client"
	cluster "github.com/siderolabs/talos/pkg/machinery/resources/cluster"
	runtime "github.com/siderolabs/talos/pkg/machinery/resources/runtime"
	"google.golang.org/grpc/metadata"
)

//
// mockState implements state.State with controllable Get/List.
// Unused methods panic if called at runtime.
//

type mockState struct {
	state.State

	getFn  func(ctx context.Context, ptr resource.Pointer, opts ...state.GetOption) (resource.Resource, error)
	listFn func(ctx context.Context, kind resource.Kind, opts ...state.ListOption) (resource.List, error)
}

func (s *mockState) Get(ctx context.Context, ptr resource.Pointer, opts ...state.GetOption) (resource.Resource, error) {
	if s.getFn != nil {
		return s.getFn(ctx, ptr, opts...)
	}

	return nil, fmt.Errorf("unexpected Get: %s", ptr)
}

func (s *mockState) List(ctx context.Context, kind resource.Kind, opts ...state.ListOption) (resource.List, error) {
	if s.listFn != nil {
		return s.listFn(ctx, kind, opts...)
	}

	return resource.List{}, fmt.Errorf("unexpected List: %s/%s", kind.Namespace(), kind.Type())
}

//
// Mock resource for extractResourceDefinitionSpec
//

type mockResource struct {
	resource.Resource
	metadata *resource.Metadata
	spec     any
}

func (m *mockResource) Metadata() *resource.Metadata { return m.metadata }
func (m *mockResource) Spec() any                    { return m.spec }

func ptrMetadata(m resource.Metadata) *resource.Metadata { return &m }

func TestExtractResourceDefinitionSpec(t *testing.T) {
	tests := []struct { //nolint:govet
		name    string
		spec    any
		want    *resourceDefinitionSpec
		wantErr bool
	}{
		{
			name: "full spec",
			spec: map[string]any{
				"type":             "secrets.kubernetes.tools.io",
				"defaultNamespace": "kube-system",
				"sensitivity":      "sensitive",
				"printColumns": []any{
					map[string]any{"name": "Ready", "jsonPath": "{.status.ready}"},
				},
			},
			want: &resourceDefinitionSpec{
				Type:             "secrets.kubernetes.tools.io",
				DefaultNamespace: "kube-system",
				Sensitivity:      "sensitive",
				PrintColumns:     []meta.PrintColumn{{Name: "Ready", JSONPath: "{.status.ready}"}},
			},
		},
		{
			name: "minimal spec",
			spec: map[string]any{
				"type":             "some.type",
				"defaultNamespace": "default",
			},
			want: &resourceDefinitionSpec{
				Type:             "some.type",
				DefaultNamespace: "default",
			},
		},
		{
			name:    "nil spec",
			spec:    nil,
			wantErr: true,
		},
		{
			name:    "wrong type for spec",
			spec:    "not a map",
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			m := &mockResource{spec: tt.spec}

			got, err := extractResourceDefinitionSpec(m)

			if (err != nil) != tt.wantErr {
				t.Fatalf("extractResourceDefinitionSpec() error = %v, wantErr %v", err, tt.wantErr)
			}

			if err != nil {
				return
			}

			if got.Type != tt.want.Type {
				t.Errorf("Type = %q, want %q", got.Type, tt.want.Type)
			}

			if got.DefaultNamespace != tt.want.DefaultNamespace {
				t.Errorf("DefaultNamespace = %q, want %q", got.DefaultNamespace, tt.want.DefaultNamespace)
			}

			if got.Sensitivity != tt.want.Sensitivity {
				t.Errorf("Sensitivity = %q, want %q", got.Sensitivity, tt.want.Sensitivity)
			}

			if len(got.PrintColumns) != len(tt.want.PrintColumns) {
				t.Fatalf("PrintColumns len = %d, want %d", len(got.PrintColumns), len(tt.want.PrintColumns))
			}

			for i := range got.PrintColumns {
				if got.PrintColumns[i].Name != tt.want.PrintColumns[i].Name {
					t.Errorf("PrintColumns[%d].Name = %q, want %q", i, got.PrintColumns[i].Name, tt.want.PrintColumns[i].Name)
				}

				if got.PrintColumns[i].JSONPath != tt.want.PrintColumns[i].JSONPath {
					t.Errorf("PrintColumns[%d].JSONPath = %q, want %q", i, got.PrintColumns[i].JSONPath, tt.want.PrintColumns[i].JSONPath)
				}
			}
		})
	}
}

//
// descriptor tests
//

func TestResourceMetricName(t *testing.T) {
	got := resourceMetricName("network", "NodeAddresses.net.talos.dev")
	want := "talos_cosi_resource_network_nodeaddresses_net_talos_dev"
	if got != want {
		t.Errorf("resourceMetricName() = %q, want %q", got, want)
	}
}

func TestResourceLabelNamesSanitizeAndDedupePrintColumns(t *testing.T) {
	got := resourceLabelNames([]meta.PrintColumn{
		{Name: "Last Heartbeat"},
		{Name: "Node ID"},
		{Name: "IPv4/IPv6"},
		{Name: "1 Ready"},
		{Name: "Node-ID"},
		{Name: "meta__node"},
	})
	want := []string{
		"meta__node", "meta__namespace", "meta__type", "meta__id",
		"last_heartbeat", "node_id", "ipv4_ipv6", "_1_ready", "node_id_2", "meta__node_2",
	}

	if len(got) != len(want) {
		t.Fatalf("resourceLabelNames() len = %d, want %d: %v", len(got), len(want), got)
	}

	for i := range want {
		if got[i] != want[i] {
			t.Errorf("resourceLabelNames()[%d] = %q, want %q", i, got[i], want[i])
		}
	}
}

func TestExtractJSONPath(t *testing.T) {
	data := map[string]any{
		"spec": map[string]any{
			"image":    "nginx:latest",
			"replicas": 3,
		},
	}

	got, err := extractJSONPath(data, "{.spec.image}")
	if err != nil {
		t.Fatalf("extractJSONPath() error = %v", err)
	}

	if got != "nginx:latest" {
		t.Errorf("extractJSONPath() = %q, want nginx:latest", got)
	}
}

func TestSanitizeLabelValue(t *testing.T) {
	got := sanitizeLabelValue(`foo"bar`, 3)
	want := "foo..."
	if got != want {
		t.Errorf("sanitizeLabelValue() = %q, want %q", got, want)
	}
}

func TestCOSIResourceCountDescriptorUsesDoubleUnderscoreLabels(t *testing.T) {
	desc := talosCOSIResourceCount.String()
	for _, label := range []string{"meta__node", "meta__namespace", "meta__type"} {
		if !strings.Contains(desc, label) {
			t.Errorf("descriptor missing label %q: %s", label, desc)
		}
	}
}

//
// discoverNodes tests
//

func TestDiscoverNodesCacheHit(t *testing.T) {
	opts := DefaultOptions()
	opts.NodeCacheTTL = time.Hour
	c := newTalosCollector(context.Background(), nil, opts)

	c.nodeCache.Lock()
	c.nodeCache.nodes = []string{"10.5.0.2", "10.5.0.3"}
	c.nodeCache.expiresAt = time.Now().Add(time.Hour)
	c.nodeCache.Unlock()

	nodes, err := c.discoverNodes(t.Context())
	if err != nil {
		t.Fatalf("discoverNodes() error = %v", err)
	}

	if len(nodes) != 2 || nodes[0] != "10.5.0.2" {
		t.Errorf("unexpected nodes: %v", nodes)
	}
}

func TestDiscoverNodesCacheMissCallsList(t *testing.T) {
	mock := &mockState{}
	mock.listFn = func(ctx context.Context, kind resource.Kind, opts ...state.ListOption) (resource.List, error) {
		// Expecting cluster.Member list call.
		if kind.Namespace() != "cluster" {
			return resource.List{}, fmt.Errorf("unexpected namespace: %s", kind.Namespace())
		}

		member := cluster.NewMember(cluster.NamespaceName, "node1")
		member.TypedSpec().Addresses = []netip.Addr{netip.MustParseAddr("10.5.0.2"), netip.MustParseAddr("10.5.0.2")}

		return resource.List{Items: []resource.Resource{member}}, nil
	}

	c := newTalosCollector(context.Background(), &client.Client{COSI: mock}, DefaultOptions())

	// Expire cache so it refresh.
	c.nodeCache.Lock()
	c.nodeCache.expiresAt = time.Now().Add(-time.Hour)
	c.nodeCache.Unlock()

	nodes, err := c.discoverNodes(t.Context())
	if err != nil {
		t.Fatalf("discoverNodes() error = %v", err)
	}

	if len(nodes) != 1 || nodes[0] != "10.5.0.2" {
		t.Errorf("unexpected nodes: %v", nodes)
	}

	// Cache should be populated.
	c.nodeCache.RLock()

	if len(c.nodeCache.nodes) != 1 {
		t.Error("cache should have 1 node")
	}

	if !time.Now().Before(c.nodeCache.expiresAt) {
		t.Error("cache expiry should be in the future")
	}

	c.nodeCache.RUnlock()
}

func TestDiscoverNodesCacheMissError(t *testing.T) {
	mock := &mockState{}
	mock.listFn = func(ctx context.Context, kind resource.Kind, opts ...state.ListOption) (resource.List, error) {
		return resource.List{}, errors.New("connection refused")
	}

	c := newTalosCollector(context.Background(), &client.Client{COSI: mock}, DefaultOptions())
	c.nodeCache.Lock()
	c.nodeCache.expiresAt = time.Now().Add(-time.Hour)
	c.nodeCache.Unlock()

	_, err := c.discoverNodes(t.Context())
	if err == nil {
		t.Fatal("expected error")
	}
}

//
// getVersion tests
//

func TestGetVersionSuccess(t *testing.T) {
	mock := &mockState{}
	mock.getFn = func(ctx context.Context, ptr resource.Pointer, opts ...state.GetOption) (resource.Resource, error) {
		v := runtime.NewVersion()
		v.TypedSpec().Version = "v1.12.0"

		return v, nil
	}

	c := newTalosCollector(context.Background(), &client.Client{COSI: mock}, DefaultOptions())

	ver, err := c.getVersion(t.Context(), "10.5.0.2")
	if err != nil {
		t.Fatalf("getVersion() error = %v", err)
	}

	if ver != "v1.12.0" {
		t.Errorf("version = %q, want %q", ver, "v1.12.0")
	}
}

func TestGetVersionError(t *testing.T) {
	mock := &mockState{}
	mock.getFn = func(ctx context.Context, ptr resource.Pointer, opts ...state.GetOption) (resource.Resource, error) {
		return nil, errors.New("not found")
	}

	c := newTalosCollector(context.Background(), &client.Client{COSI: mock}, DefaultOptions())

	_, err := c.getVersion(t.Context(), "10.5.0.2")
	if err == nil {
		t.Fatal("expected error")
	}
}

func TestCollectResourcesUsesNodeContext(t *testing.T) {
	mock := &mockState{}
	mock.listFn = func(ctx context.Context, kind resource.Kind, opts ...state.ListOption) (resource.List, error) {
		md, ok := metadata.FromOutgoingContext(ctx)
		if !ok {
			t.Fatal("expected outgoing metadata")
		}

		if got := md.Get("node"); len(got) != 1 || got[0] != "10.5.0.2" {
			t.Fatalf("node metadata = %v, want [10.5.0.2]", got)
		}

		switch kind.Type() {
		case meta.ResourceDefinitionType:
			return resource.List{Items: []resource.Resource{&mockResource{
				metadata: ptrMetadata(resource.NewMetadata(meta.NamespaceName, meta.ResourceDefinitionType, "testresources.example.dev", resource.VersionUndefined)),
				spec: map[string]any{
					"type":             "TestResources.example.dev",
					"defaultNamespace": "default",
					"printColumns": []any{
						map[string]any{"name": "Last Heartbeat", "jsonPath": "{.status.lastHeartbeat}"},
					},
				},
			}}}, nil
		case "TestResources.example.dev":
			return resource.List{Items: []resource.Resource{&mockResource{
				metadata: ptrMetadata(resource.NewMetadata("default", "TestResources.example.dev", "resource-1", resource.VersionUndefined)),
				spec: map[string]any{
					"status": map[string]any{"lastHeartbeat": "now"},
				},
			}}}, nil
		default:
			return resource.List{}, fmt.Errorf("unexpected type: %s", kind.Type())
		}
	}

	c := newTalosCollector(context.Background(), &client.Client{COSI: mock}, DefaultOptions())
	ch := make(chan prometheus.Metric, 1)

	if err := c.collectResources(t.Context(), "10.5.0.2", ch); err != nil {
		t.Fatalf("collectResources() error = %v", err)
	}

	close(ch)

	if got := len(ch); got != 1 {
		t.Fatalf("collected metrics = %d, want 1", got)
	}
}

//
// collector.Describe tests
//

func TestDescribe(t *testing.T) {
	c := newTalosCollector(context.Background(), nil, DefaultOptions())
	ch := make(chan *prometheus.Desc, 10)
	c.Describe(ch)
	close(ch)

	descs := make([]*prometheus.Desc, 0, 10)
	for d := range ch {
		descs = append(descs, d)
	}

	if len(descs) != 0 {
		t.Fatalf("expected unchecked collector to describe 0 descriptors, got %d", len(descs))
	}
}

//
// collector.Collect tests
//

func TestCollectDiscoverFails(t *testing.T) {
	mock := &mockState{}
	mock.listFn = func(ctx context.Context, kind resource.Kind, opts ...state.ListOption) (resource.List, error) {
		return resource.List{}, errors.New("discovery failed")
	}

	c := newTalosCollector(context.Background(), &client.Client{COSI: mock}, DefaultOptions())
	c.nodeCache.Lock()
	c.nodeCache.expiresAt = time.Now().Add(-time.Hour)
	c.nodeCache.Unlock()

	ch := make(chan prometheus.Metric, 10)
	c.Collect(ch)
	close(ch)

	n := 0
	for range ch {
		n++
	}

	if n != 2 {
		t.Errorf("expected 2 metrics (build info and collection error) on discovery failure, got %d", n)
	}
}

func TestMergeContextsCancelsOnParentCancel(t *testing.T) {
	parent, parentCancel := context.WithCancel(t.Context())
	request, requestCancel := context.WithCancel(t.Context())
	defer requestCancel()

	ctx, cancel := mergeContexts(parent, request)
	defer cancel()

	parentCancel()

	select {
	case <-ctx.Done():
	case <-time.After(time.Second):
		t.Fatal("merged context was not canceled")
	}
}

func TestMergeContextsCancelsOnRequestCancel(t *testing.T) {
	parent, parentCancel := context.WithCancel(t.Context())
	defer parentCancel()
	request, requestCancel := context.WithCancel(t.Context())

	ctx, cancel := mergeContexts(parent, request)
	defer cancel()

	requestCancel()

	select {
	case <-ctx.Done():
	case <-time.After(time.Second):
		t.Fatal("merged context was not canceled")
	}
}
