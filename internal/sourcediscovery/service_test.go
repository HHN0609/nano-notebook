package sourcediscovery

import (
	"context"
	"testing"
	"time"
)

func TestServiceDrainsJobsAndStopsWithContext(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	processor := &serviceProcessor{results: []bool{true, false}, cancel: cancel}
	service := NewService(processor, time.Millisecond)

	if err := service.Run(ctx); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if processor.calls != 2 {
		t.Fatalf("ProcessNext calls = %d, want 2", processor.calls)
	}
}

type serviceProcessor struct {
	results []bool
	calls   int
	cancel  context.CancelFunc
}

func (p *serviceProcessor) ProcessNext(context.Context) (bool, error) {
	result := p.results[p.calls]
	p.calls++
	if p.calls == len(p.results) {
		p.cancel()
	}
	return result, nil
}
