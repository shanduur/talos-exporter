// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at http://mozilla.org/MPL/2.0/.

package metrics

import "time"

// Options configures the Talos metrics collector behavior.
type Options struct {
	Namespaces    []string
	ResourceTypes []string
	NodeCacheTTL  time.Duration
	MaxLabelLen   int
	Aggregate     bool
}

// DefaultOptions returns an Options with sensible defaults.
func DefaultOptions() Options {
	return Options{
		NodeCacheTTL: 30 * time.Second,
		MaxLabelLen:  64,
		Aggregate:    false,
	}
}
