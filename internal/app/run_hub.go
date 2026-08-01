package app

import (
	"sync"

	"github.com/huangxinxinyu/nano-notebook/internal/platform/metrics"
)

// runHub is the leak surface named directly in docs/sprint/SPRINT-12-PRD.md
// section 4.6: an SSE subscriber map with no bound on how many entries it
// can accumulate if unsubscribe is ever missed. nano_runhub_subscribers and
// nano_runhub_runs_tracked (both catalog-level gauges, shared across every
// runHub instance on the Server) are the direct observability for that.
type runHub struct {
	mu          sync.Mutex
	subscribers map[string]map[chan struct{}]struct{}
	metrics     *metrics.Catalog
}

func newRunHub() *runHub {
	return &runHub{subscribers: make(map[string]map[chan struct{}]struct{})}
}

func newRunHubWithMetrics(catalog *metrics.Catalog) *runHub {
	return &runHub{subscribers: make(map[string]map[chan struct{}]struct{}), metrics: catalog}
}

func (h *runHub) subscribe(runID string) (<-chan struct{}, func()) {
	ch := make(chan struct{}, 1)
	h.mu.Lock()
	if h.subscribers[runID] == nil {
		h.subscribers[runID] = make(map[chan struct{}]struct{})
		if h.metrics != nil {
			h.metrics.RunHubRunsTracked.Inc()
		}
	}
	h.subscribers[runID][ch] = struct{}{}
	if h.metrics != nil {
		h.metrics.RunHubSubscribers.Inc()
	}
	h.mu.Unlock()
	return ch, func() {
		h.mu.Lock()
		if _, ok := h.subscribers[runID][ch]; ok {
			delete(h.subscribers[runID], ch)
			if h.metrics != nil {
				h.metrics.RunHubSubscribers.Dec()
			}
		}
		if len(h.subscribers[runID]) == 0 {
			delete(h.subscribers, runID)
			if h.metrics != nil {
				h.metrics.RunHubRunsTracked.Dec()
			}
		}
		h.mu.Unlock()
	}
}

func (h *runHub) notify(runID string) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if runID == "" {
		for _, subscribers := range h.subscribers {
			wakeSubscribers(subscribers)
		}
		return
	}
	wakeSubscribers(h.subscribers[runID])
}

func wakeSubscribers(subscribers map[chan struct{}]struct{}) {
	for ch := range subscribers {
		select {
		case ch <- struct{}{}:
		default:
		}
	}
}
