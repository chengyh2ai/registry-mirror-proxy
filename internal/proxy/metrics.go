package proxy

import (
	"fmt"
	"net/http"
	"sync/atomic"
)

type Metrics struct {
	Requests      atomic.Uint64
	UpstreamFails atomic.Uint64
	CacheHits     atomic.Uint64
	CacheMisses   atomic.Uint64
	BytesSent     atomic.Uint64
}

func (m *Metrics) ServeHTTP(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "text/plain; version=0.0.4")
	fmt.Fprintf(w, "registry_mirror_requests_total %d\n", m.Requests.Load())
	fmt.Fprintf(w, "registry_mirror_upstream_failures_total %d\n", m.UpstreamFails.Load())
	fmt.Fprintf(w, "registry_mirror_cache_hits_total %d\n", m.CacheHits.Load())
	fmt.Fprintf(w, "registry_mirror_cache_misses_total %d\n", m.CacheMisses.Load())
	fmt.Fprintf(w, "registry_mirror_bytes_sent_total %d\n", m.BytesSent.Load())
}
