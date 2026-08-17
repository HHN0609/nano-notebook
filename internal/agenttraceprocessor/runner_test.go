package agenttraceprocessor_test

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/huangxinxinyu/nano-notebook/internal/agenttraceprocessor"
	"github.com/huangxinxinyu/nano-notebook/internal/platform/metrics"
	"github.com/prometheus/client_golang/prometheus"
	dto "github.com/prometheus/client_model/go"
)

func TestRunnerCommitsProcessedMessagesInOrderAndReleasesRebalance(t *testing.T) {
	consumer := &fakeConsumer{polls: [][]agenttraceprocessor.Message{{
		{Topic: traceTopic, Partition: 0, Offset: 1},
		{Topic: traceTopic, Partition: 0, Offset: 2},
	}}}
	handler := &fakeHandler{disposition: agenttraceprocessor.Commit}
	runner := newRunner(t, consumer, handler)

	if err := runner.RunOnce(context.Background()); err != nil {
		t.Fatal(err)
	}
	if len(consumer.committed) != 2 || consumer.committed[0].Offset != 1 || consumer.committed[1].Offset != 2 || consumer.allowCalls != 1 {
		t.Fatalf("committed=%#v allow_calls=%d", consumer.committed, consumer.allowCalls)
	}
}

func TestRunnerLeavesFailedMessageUncommittedAndReleasesRebalance(t *testing.T) {
	consumer := &fakeConsumer{polls: [][]agenttraceprocessor.Message{{
		{Topic: traceTopic, Partition: 0, Offset: 1},
		{Topic: traceTopic, Partition: 0, Offset: 2},
	}}}
	handler := &fakeHandler{dispositions: []agenttraceprocessor.Disposition{agenttraceprocessor.Commit, agenttraceprocessor.Retry}}
	runner := newRunner(t, consumer, handler)

	err := runner.RunOnce(context.Background())
	if err == nil || len(consumer.committed) != 1 || consumer.committed[0].Offset != 1 || consumer.allowCalls != 1 {
		t.Fatalf("error=%v committed=%#v allow_calls=%d", err, consumer.committed, consumer.allowCalls)
	}
}

func TestRunnerProcessesTraceKeysConcurrentlyButCommitsPartitionPrefixInOrder(t *testing.T) {
	consumer := &fakeConsumer{polls: [][]agenttraceprocessor.Message{{
		{Topic: traceTopic, Partition: 0, Offset: 1, Key: []byte("trace-a")},
		{Topic: traceTopic, Partition: 0, Offset: 2, Key: []byte("trace-b")},
		{Topic: traceTopic, Partition: 0, Offset: 3, Key: []byte("trace-a")},
		{Topic: traceTopic, Partition: 0, Offset: 4, Key: []byte("trace-b")},
	}}}
	handler := newTraceKeyConcurrencyHandler()
	runner := newRunner(t, consumer, handler)
	done := make(chan error, 1)
	go func() { done <- runner.RunOnce(context.Background()) }()

	for range 2 {
		<-handler.started
	}
	close(handler.release)
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	if handler.sameTraceOverlap || handler.maxActive < 2 {
		t.Fatalf("same_trace_overlap=%v max_active=%d", handler.sameTraceOverlap, handler.maxActive)
	}
	if got := handler.offsets["trace-a"]; len(got) != 2 || got[0] != 1 || got[1] != 3 {
		t.Fatalf("trace-a offsets=%v", got)
	}
	if got := handler.offsets["trace-b"]; len(got) != 2 || got[0] != 2 || got[1] != 4 {
		t.Fatalf("trace-b offsets=%v", got)
	}
	if len(consumer.committed) != 4 {
		t.Fatalf("committed=%v", consumer.committed)
	}
}

func TestRunnerDoesNotCommitStoredSuffixPastFailedPartitionOffset(t *testing.T) {
	consumer := &fakeConsumer{polls: [][]agenttraceprocessor.Message{{
		{Topic: traceTopic, Partition: 0, Offset: 1, Key: []byte("trace-fails")},
		{Topic: traceTopic, Partition: 0, Offset: 2, Key: []byte("trace-stored")},
	}}}
	runner := newRunner(t, consumer, keyFailureHandler{failedKey: "trace-fails"})
	if err := runner.RunOnce(context.Background()); err == nil {
		t.Fatal("failed lower offset was not reported")
	}
	if len(consumer.committed) != 0 {
		t.Fatalf("committed suffix past failed offset: %v", consumer.committed)
	}
}

func TestRunnerRecordsBoundedKafkaToSearchableMetrics(t *testing.T) {
	now := time.Date(2026, 8, 17, 12, 0, 0, 0, time.UTC)
	consumer := &fakeConsumer{polls: [][]agenttraceprocessor.Message{{{
		Topic: traceTopic, Partition: 3, Offset: 7, HighWatermark: 10, Timestamp: now.Add(-2 * time.Second), Value: []byte("payload"),
	}}}}
	registry := metrics.NewRegistry()
	catalog := metrics.NewCatalog(registry)
	runner, err := agenttraceprocessor.NewRunner(agenttraceprocessor.RunnerConfig{
		Consumer: consumer, Handler: &fakeHandler{disposition: agenttraceprocessor.Commit}, Metrics: catalog, Now: func() time.Time { return now },
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := runner.RunOnce(context.Background()); err != nil {
		t.Fatal(err)
	}
	if got := metricFloat(t, catalog.AgentTraceConsumerLag.WithLabelValues("3")); got != 2 {
		t.Fatalf("partition lag=%v", got)
	}
	if got := metricFloat(t, catalog.AgentTraceSearchableFreshness); got != 2 {
		t.Fatalf("searchable freshness=%v", got)
	}
}

func metricFloat(t *testing.T, metric prometheus.Metric) float64 {
	t.Helper()
	value := &dto.Metric{}
	if err := metric.Write(value); err != nil {
		t.Fatal(err)
	}
	if value.Counter != nil {
		return value.Counter.GetValue()
	}
	return value.Gauge.GetValue()
}

func newRunner(t *testing.T, consumer agenttraceprocessor.Consumer, handler agenttraceprocessor.Handler) *agenttraceprocessor.Runner {
	t.Helper()
	runner, err := agenttraceprocessor.NewRunner(agenttraceprocessor.RunnerConfig{Consumer: consumer, Handler: handler})
	if err != nil {
		t.Fatal(err)
	}
	return runner
}

type fakeConsumer struct {
	polls      [][]agenttraceprocessor.Message
	committed  []agenttraceprocessor.Message
	allowCalls int
}

func (f *fakeConsumer) Poll(context.Context) ([]agenttraceprocessor.Message, error) {
	if len(f.polls) == 0 {
		return nil, errors.New("no poll configured")
	}
	messages := f.polls[0]
	f.polls = f.polls[1:]
	return messages, nil
}

func (f *fakeConsumer) Commit(_ context.Context, messages []agenttraceprocessor.Message) error {
	f.committed = append(f.committed, messages...)
	return nil
}

func (f *fakeConsumer) AllowRebalance() { f.allowCalls++ }

type fakeHandler struct {
	disposition  agenttraceprocessor.Disposition
	dispositions []agenttraceprocessor.Disposition
}

type traceKeyConcurrencyHandler struct {
	mu               sync.Mutex
	started          chan struct{}
	release          chan struct{}
	active           map[string]int
	offsets          map[string][]int64
	maxActive        int
	sameTraceOverlap bool
}

type keyFailureHandler struct{ failedKey string }

func (h keyFailureHandler) Process(_ context.Context, message agenttraceprocessor.Message) (agenttraceprocessor.Disposition, error) {
	if string(message.Key) == h.failedKey {
		return agenttraceprocessor.Retry, errors.New("transient failure")
	}
	return agenttraceprocessor.Commit, nil
}

func newTraceKeyConcurrencyHandler() *traceKeyConcurrencyHandler {
	return &traceKeyConcurrencyHandler{
		started: make(chan struct{}, 2), release: make(chan struct{}),
		active: make(map[string]int), offsets: make(map[string][]int64),
	}
}

func (h *traceKeyConcurrencyHandler) Process(_ context.Context, message agenttraceprocessor.Message) (agenttraceprocessor.Disposition, error) {
	key := string(message.Key)
	h.mu.Lock()
	if h.active[key] != 0 {
		h.sameTraceOverlap = true
	}
	h.active[key]++
	h.offsets[key] = append(h.offsets[key], message.Offset)
	active := 0
	for _, count := range h.active {
		active += count
	}
	if active > h.maxActive {
		h.maxActive = active
	}
	isFirst := len(h.offsets[key]) == 1
	h.mu.Unlock()
	if isFirst {
		h.started <- struct{}{}
		<-h.release
	}
	h.mu.Lock()
	h.active[key]--
	h.mu.Unlock()
	return agenttraceprocessor.Commit, nil
}

func (f *fakeHandler) Process(context.Context, agenttraceprocessor.Message) (agenttraceprocessor.Disposition, error) {
	if len(f.dispositions) > 0 {
		disposition := f.dispositions[0]
		f.dispositions = f.dispositions[1:]
		if disposition == agenttraceprocessor.Retry {
			return disposition, errors.New("transient failure")
		}
		return disposition, nil
	}
	return f.disposition, nil
}
