package metrics

import "time"

type Options struct {
	NodeCacheTTL  time.Duration
	Namespaces    []string
	ResourceTypes []string
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
