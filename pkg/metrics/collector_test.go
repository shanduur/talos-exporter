// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at http://mozilla.org/MPL/2.0/.

package metrics //nolint:testpackage

import (
	"context"
	"errors"
	"fmt"
	"net/netip"
	"testing"
	"time"

	"github.com/cosi-project/runtime/pkg/resource"
	"github.com/cosi-project/runtime/pkg/resource/meta"
	"github.com/cosi-project/runtime/pkg/state"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/siderolabs/talos/pkg/machinery/client"
	cluster "github.com/siderolabs/talos/pkg/machinery/resources/cluster"
	runtime "github.com/siderolabs/talos/pkg/machinery/resources/runtime"
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
// Pure function tests
//

func TestSanitizeLabelValue(t *testing.T) {
	tests := []struct { //nolint:govet
		name   string
		input  string
		maxLen int
		want   string
	}{
		{name: "empty", input: "", maxLen: 64, want: ""},
		{name: "no change", input: "hello", maxLen: 64, want: "hello"},
		{name: "double quote", input: `he"llo`, maxLen: 64, want: "he'llo"},
		{name: "json bracket open", input: `["val`, maxLen: 64, want: "[val"},
		{name: "json bracket close", input: `val"]`, maxLen: 64, want: "val']"},
		{name: "truncate", input: "abcdefghij", maxLen: 5, want: "abcde..."},
		{name: "maxLen zero", input: "longvalue", maxLen: 0, want: "longvalue"},
		{name: "combined", input: `["he"llo"]`, maxLen: 64, want: "[he'llo']"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := sanitizeLabelValue(tt.input, tt.maxLen)
			if got != tt.want {
				t.Errorf("sanitizeLabelValue(%q, %d) = %q, want %q", tt.input, tt.maxLen, got, tt.want)
			}
		})
	}
}

func TestToSnakeCase(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  string
	}{
		{name: "empty", input: "", want: ""},
		{name: "already snake", input: "already_snake", want: "already_snake"},
		{name: "camelCase", input: "camelCase", want: "camel_case"},
		{name: "PascalCase", input: "PascalCase", want: "pascal_case"},
		{name: "single upper", input: "Name", want: "name"},
		{name: "acronym sequence", input: "JSONPath", want: "json_path"},
		{name: "trailing acronym", input: "parseJSON", want: "parse_json"},
		{name: "lowercase only", input: "lowercase", want: "lowercase"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := toSnakeCase(tt.input)
			if got != tt.want {
				t.Errorf("toSnakeCase(%q) = %q, want %q", tt.input, got, tt.want)
			}
		})
	}
}

func TestExtractJSONPath(t *testing.T) {
	data := map[string]any{
		"name": "test-resource",
		"spec": map[string]any{
			"image":    "nginx:latest",
			"replicas": 3,
			"nested":   map[string]any{"key": "value"},
		},
	}

	tests := []struct {
		name    string
		expr    string
		want    string
		wantErr bool
	}{
		{name: "top field", expr: "{.name}", want: "test-resource"},
		{name: "nested field", expr: "{.spec.image}", want: "nginx:latest"},
		{name: "deep nested", expr: "{.spec.nested.key}", want: "value"},
		{name: "int to string", expr: "{.spec.replicas}", want: "3"},
		{name: "bad expr", expr: "{.", wantErr: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := extractJSONPath(data, tt.expr)
			if (err != nil) != tt.wantErr {
				t.Errorf("extractJSONPath() error = %v, wantErr %v", err, tt.wantErr)

				return
			}

			if got != tt.want {
				t.Errorf("extractJSONPath() = %q, want %q", got, tt.want)
			}
		})
	}
}

//
// Mock resource for extractResourceDefinitionSpec
//

type mockResource struct {
	resource.Resource
	spec any
}

func (m *mockResource) Spec() any { return m.spec }

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
// getOrCreateDescriptor tests
//

func TestGetOrCreateDescriptor(t *testing.T) {
	c := newTalosCollector(nil, DefaultOptions())

	desc1 := c.getOrCreateDescriptor("ns1", "type1", nil)
	if desc1 == nil {
		t.Fatal("expected non-nil descriptor")
	}

	// Same key returns cached.
	desc2 := c.getOrCreateDescriptor("ns1", "type1", nil)
	if desc1 != desc2 {
		t.Error("expected cached descriptor for same key")
	}

	// Different key returns new.
	desc3 := c.getOrCreateDescriptor("ns2", "type2", nil)
	if desc3 == desc1 {
		t.Error("expected different descriptor for different key")
	}

	// With columns: different descriptor.
	desc4 := c.getOrCreateDescriptor("ns1", "type1", []meta.PrintColumn{
		{Name: "Ready", JSONPath: "{.status.ready}"},
	})
	if desc4 == desc1 {
		t.Error("expected different descriptor when columns differ")
	}
}

//
// discoverNodes tests
//

func TestDiscoverNodesCacheHit(t *testing.T) {
	opts := DefaultOptions()
	opts.NodeCacheTTL = time.Hour
	c := newTalosCollector(nil, opts)

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
		member.TypedSpec().Addresses = []netip.Addr{netip.MustParseAddr("10.5.0.2")}

		return resource.List{Items: []resource.Resource{member}}, nil
	}

	c := newTalosCollector(&client.Client{COSI: mock}, DefaultOptions())

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

	c := newTalosCollector(&client.Client{COSI: mock}, DefaultOptions())
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

	c := newTalosCollector(&client.Client{COSI: mock}, DefaultOptions())

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

	c := newTalosCollector(&client.Client{COSI: mock}, DefaultOptions())

	_, err := c.getVersion(t.Context(), "10.5.0.2")
	if err == nil {
		t.Fatal("expected error")
	}
}

//
// collector.Describe tests
//

func TestDescribe(t *testing.T) {
	c := newTalosCollector(nil, DefaultOptions())
	ch := make(chan *prometheus.Desc, 10)
	c.Describe(ch)
	close(ch)

	descs := make([]*prometheus.Desc, 0, 10)
	for d := range ch {
		descs = append(descs, d)
	}

	if len(descs) < 2 {
		t.Fatalf("expected >= 2 descriptors, got %d", len(descs))
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

	c := newTalosCollector(&client.Client{COSI: mock}, DefaultOptions())
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

	if n != 0 {
		t.Errorf("expected 0 metrics on discovery failure, got %d", n)
	}
}
