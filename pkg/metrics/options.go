package metrics

import "time"

type Options struct {
	Namespaces    []string
	ResourceTypes []string
	NodeCacheTTL  time.Duration
	MaxLabelLen   int
	Aggregate     bool
}

func DefaultOptions() Options {
	return Options{
		NodeCacheTTL: 30 * time.Second,
		MaxLabelLen:  64,
		Aggregate:    false,
	}
}
