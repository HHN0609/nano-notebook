package collector

import (
	"context"
	"sync"
	"testing"
	"time"
)

func TestClickHouseStoreCoalescesConcurrentDurableWrites(t *testing.T) {
	const writes = 12
	var mu sync.Mutex
	batchSizes := make([]int, 0, 1)
	store := &ClickHouseStore{
		batchDelay: 25 * time.Millisecond,
		writeBatchFn: func(_ context.Context, requests []*clickHouseWriteRequest) error {
			mu.Lock()
			batchSizes = append(batchSizes, len(requests))
			mu.Unlock()
			return nil
		},
	}
	start := make(chan struct{})
	errors := make(chan error, writes)
	for range writes {
		go func() {
			<-start
			errors <- store.enqueueWrite(clickHouseWriteRequest{done: make(chan error, 1)})
		}()
	}
	close(start)
	for range writes {
		if err := <-errors; err != nil {
			t.Fatal(err)
		}
	}
	mu.Lock()
	defer mu.Unlock()
	if len(batchSizes) != 1 || batchSizes[0] != writes {
		t.Fatalf("batch sizes=%v want=[%d]", batchSizes, writes)
	}
}

func TestClickHouseStoreCoalescesConcurrentTraceExistenceProbes(t *testing.T) {
	const probes = 12
	var mu sync.Mutex
	batchSizes := make([]int, 0, 1)
	store := &ClickHouseStore{
		probeDelay: 25 * time.Millisecond,
		probeBatchFn: func(_ context.Context, requests []*clickHouseProbeRequest) {
			mu.Lock()
			batchSizes = append(batchSizes, len(requests))
			mu.Unlock()
			for _, request := range requests {
				request.done <- clickHouseProbeResult{}
			}
		},
	}
	start := make(chan struct{})
	errors := make(chan error, probes)
	for range probes {
		go func() {
			<-start
			_, err := store.traceExists(context.Background(), TraceDescriptor{TraceID: "trace", NotebookID: "notebook"})
			errors <- err
		}()
	}
	close(start)
	for range probes {
		if err := <-errors; err != nil {
			t.Fatal(err)
		}
	}
	mu.Lock()
	defer mu.Unlock()
	if len(batchSizes) != 1 || batchSizes[0] != probes {
		t.Fatalf("probe batch sizes=%v want=[%d]", batchSizes, probes)
	}
}
