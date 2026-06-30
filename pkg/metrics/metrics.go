// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at http://mozilla.org/MPL/2.0/.

// Package metrics provides Prometheus metric collection for Talos clusters.
//
// It discovers cluster nodes via the Talos API, queries COSI resources,
// and exposes them as Prometheus metrics. Two modes are supported:
// per-resource (default) and aggregate.
package metrics
