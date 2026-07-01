// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at http://mozilla.org/MPL/2.0/.

package client_test

import (
	"testing"

	"github.com/siderolabs/talos-exporter/pkg/client"
)

func TestNewEmptyOptions(t *testing.T) {
	_, err := client.New(t.Context(), client.TalosClientOptions{})
	if err != nil {
		t.Fatalf("New with empty options error = %v", err)
	}
}

func TestGRPCCollectors(t *testing.T) {
	collectors := client.GRPCCollectors()
	if len(collectors) != 3 {
		t.Fatalf("GRPCCollectors() len = %d, want 3", len(collectors))
	}
}
