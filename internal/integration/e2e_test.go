// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at http://mozilla.org/MPL/2.0/.

//go:build integration

package integration

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/siderolabs/talos-exporter/pkg/metrics"
)

const (
	exporterAddr = "http://localhost:9104/metrics"
)

func TestExporterServesMetrics(t *testing.T) {
	ctx, cancel := context.WithTimeout(t.Context(), 30*time.Second)
	defer cancel()

	addr := os.Getenv("EXPORTER_ADDR")
	if addr == "" {
		addr = exporterAddr
	}

	// Verify exporter is reachable with retries.
	var resp *http.Response
	for range 10 {
		req, reqErr := http.NewRequestWithContext(ctx, http.MethodGet, addr, http.NoBody)
		if reqErr != nil {
			t.Fatalf("creating request: %v", reqErr)
		}

		var err error
		resp, err = http.DefaultClient.Do(req)
		if err == nil {
			break
		}

		time.Sleep(500 * time.Millisecond)
	}

	if resp == nil {
		t.Fatal("exporter not reachable")
	}

	defer func() {
		if closeErr := resp.Body.Close(); closeErr != nil {
			t.Errorf("closing response body: %v", closeErr)
		}
	}()

	if resp.StatusCode != http.StatusOK {
		body, readErr := io.ReadAll(resp.Body)
		if readErr != nil {
			t.Fatalf("reading response body: %v", readErr)
		}

		t.Fatalf("status = %d, want %d; body = %s", resp.StatusCode, http.StatusOK, body)
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("reading response body: %v", err)
	}

	metricsContent := string(body)

	// Verify key metrics are present.
	expectedMetrics := []string{
		"talos_version_info",
		"talos_cosi_resources",
	}

	for _, m := range expectedMetrics {
		if !strings.Contains(metricsContent, m) {
			t.Errorf("expected metric %q not found in response", m)
		}
	}

	t.Logf("metrics response length: %d bytes", len(metricsContent))
}

func TestCollectorDefaultOptions(t *testing.T) {
	opts := metrics.DefaultOptions()

	if opts.MaxLabelLen != 64 {
		t.Errorf("MaxLabelLen = %d, want %d", opts.MaxLabelLen, 64)
	}

	if opts.NodeCacheTTL != 30*time.Second {
		t.Errorf("NodeCacheTTL = %v, want %v", opts.NodeCacheTTL, 30*time.Second)
	}
}

func TestMain(m *testing.M) {
	fmt.Println("=== Talos Exporter Integration Tests ===")

	os.Exit(m.Run())
}
