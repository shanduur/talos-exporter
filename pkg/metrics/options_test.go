// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at http://mozilla.org/MPL/2.0/.

package metrics //nolint:testpackage

import (
	"testing"
	"time"
)

func TestDefaultOptions(t *testing.T) {
	o := DefaultOptions()

	if o.NodeCacheTTL != 30*time.Second {
		t.Errorf("NodeCacheTTL = %v, want %v", o.NodeCacheTTL, 30*time.Second)
	}

	if o.MaxLabelLen != 64 {
		t.Errorf("MaxLabelLen = %d, want %d", o.MaxLabelLen, 64)
	}

	if o.Aggregate {
		t.Errorf("Aggregate = true, want false")
	}

	if o.Namespaces != nil {
		t.Errorf("Namespaces = %v, want nil", o.Namespaces)
	}

	if o.ResourceTypes != nil {
		t.Errorf("ResourceTypes = %v, want nil", o.ResourceTypes)
	}
}
