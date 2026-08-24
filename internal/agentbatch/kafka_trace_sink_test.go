package agentbatch_test

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/huangxinxinyu/nano-notebook/internal/agentbatch"
	"github.com/huangxinxinyu/nano-notebook/internal/agentobs"
	"github.com/huangxinxinyu/nano-notebook/internal/collector"
	"github.com/huangxinxinyu/nano-notebook/internal/replay"
	"github.com/twmb/franz-go/pkg/kgo"
)

func TestKafkaTraceSinkOffersOneRecordWithoutWaitingForAcknowledgement(t *testing.T) {
	producer := &asyncKafkaProducer{bufferedRecords: 1, bufferedBytes: 512}
	observer := &recordingKafkaTraceObserver{}
	sink, err := agentbatch.NewKafkaTraceSink(agentbatch.KafkaTraceSinkConfig{
		ProducerID: "nano-worker/test", Topic: "nano.observability.agent-trace.v1",
		Producer: producer, MaxMessageBytes: 512 * 1024, Observer: observer,
	})
	if err != nil {
		t.Fatal(err)
	}
	envelope := traceEnvelope("direct-record")
	envelope.Record.Attributes = append(envelope.Record.Attributes,
		agentobs.String(replay.ModelRequestAttachmentKey, "019bf000-0000-7000-8000-000000000202"))
	envelope.Attachments = []collector.AttachmentDescriptor{{
		AttachmentID: "019bf000-0000-7000-8000-000000000202", RecordIdentityKey: envelope.Record.IdentityKey,
		Class: replay.ClassModelRequest, SchemaVersion: 1, PlaintextSHA256: strings.Repeat("a", 64),
		StagingObjectKey: "replay/attachment-1", CiphertextBytes: 42, CiphertextSHA256: strings.Repeat("b", 64),
		Compression: replay.CompressionGZIP, Encryption: replay.EncryptionAES256GCM, KeyID: "dev-key-v1",
		WrappedKey: bytes.Repeat([]byte{1}, 32), Nonce: bytes.Repeat([]byte{2}, 12), ExpiresAt: time.Unix(1_800_000_000, 0).UTC(),
	}}
	callerCtx, cancel := context.WithCancel(context.Background())
	if err := sink.Offer(callerCtx, envelope); err != nil {
		t.Fatalf("Offer: %v", err)
	}
	cancel()

	message, produceCtx, callback := producer.singleOffer(t)
	if produceCtx.Err() != nil {
		t.Fatalf("producer context followed canceled caller context: %v", produceCtx.Err())
	}
	if message.Topic != "nano.observability.agent-trace.v1" || string(message.Key) != string(envelope.Trace.TraceID) {
		t.Fatalf("message=%#v", message)
	}
	wire, err := agentbatch.DecodeKafkaTraceEnvelope(message.Value)
	if err != nil {
		t.Fatal(err)
	}
	if wire.ProducerID != "nano-worker/test" || len(wire.Chunk.Records) != 1 ||
		wire.Chunk.Records[0].Record.IdentityKey != "direct-record" || len(wire.Chunk.Attachments) != 1 ||
		wire.Chunk.Attachments[0].AttachmentID != "019bf000-0000-7000-8000-000000000202" ||
		wire.Chunk.Attachments[0].RecordSequence != 0 || wire.Chunk.Records[0].CanonicalSHA256 == "" {
		t.Fatalf("wire envelope=%#v", wire)
	}
	if len(observer.deliveries) != 0 {
		t.Fatalf("Offer waited for callback: deliveries=%#v", observer.deliveries)
	}
	callback(nil)
	if got := observer.singleDelivery(t); got != agentbatch.KafkaTraceAcknowledged {
		t.Fatalf("delivery=%q", got)
	}
	if observer.bufferedRecords != 1 || observer.bufferedBytes != 512 {
		t.Fatalf("buffered records=%d bytes=%d", observer.bufferedRecords, observer.bufferedBytes)
	}
}

func TestKafkaTraceSinkClassifiesAsynchronousProducerFailures(t *testing.T) {
	tests := []struct {
		name string
		err  error
		want agentbatch.KafkaTraceDeliveryResult
	}{
		{name: "buffer full", err: kgo.ErrMaxBuffered, want: agentbatch.KafkaTraceBufferFull},
		{name: "delivery timeout", err: kgo.ErrRecordTimeout, want: agentbatch.KafkaTraceTimedOut},
		{name: "other failure", err: errors.New("broker rejected record"), want: agentbatch.KafkaTraceFailed},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			producer := &asyncKafkaProducer{}
			observer := &recordingKafkaTraceObserver{}
			sink, err := agentbatch.NewKafkaTraceSink(agentbatch.KafkaTraceSinkConfig{
				ProducerID: "nano-worker/test", Topic: "agent-trace", Producer: producer,
				MaxMessageBytes: 512 * 1024, Observer: observer,
			})
			if err != nil {
				t.Fatal(err)
			}
			if err := sink.Offer(context.Background(), traceEnvelope("async-failure")); err != nil {
				t.Fatalf("Offer propagated asynchronous failure: %v", err)
			}
			_, _, callback := producer.singleOffer(t)
			callback(tt.err)
			if got := observer.singleDelivery(t); got != tt.want {
				t.Fatalf("delivery=%q want=%q", got, tt.want)
			}
		})
	}
}

func TestKafkaTraceSinkRejectsInvalidOversizedAndPostShutdownOffers(t *testing.T) {
	producer := &asyncKafkaProducer{}
	observer := &recordingKafkaTraceObserver{}
	sink, err := agentbatch.NewKafkaTraceSink(agentbatch.KafkaTraceSinkConfig{
		ProducerID: "nano-worker/test", Topic: "agent-trace", Producer: producer,
		MaxMessageBytes: 32, Observer: observer,
	})
	if err != nil {
		t.Fatal(err)
	}
	invalid := traceEnvelope("invalid")
	invalid.Record.TraceID = "different-trace"
	if err := sink.Offer(context.Background(), invalid); err == nil {
		t.Fatal("invalid envelope was accepted")
	}
	if err := sink.Offer(context.Background(), traceEnvelope("oversized")); !errors.Is(err, agentbatch.ErrKafkaTraceMessageTooLarge) {
		t.Fatalf("oversized Offer error=%v", err)
	}
	if err := sink.Shutdown(context.Background()); err != nil {
		t.Fatalf("Shutdown: %v", err)
	}
	if err := sink.Shutdown(context.Background()); err != nil {
		t.Fatalf("second Shutdown: %v", err)
	}
	if producer.flushCalls != 1 || producer.closeCalls != 1 {
		t.Fatalf("flush=%d close=%d", producer.flushCalls, producer.closeCalls)
	}
	if err := sink.Offer(context.Background(), traceEnvelope("after-shutdown")); !errors.Is(err, agentbatch.ErrShutdown) {
		t.Fatalf("post-shutdown Offer error=%v", err)
	}
	if len(producer.offers) != 0 {
		t.Fatalf("synchronous rejections reached Kafka: %d offers", len(producer.offers))
	}
	if len(observer.rejections) != 3 {
		t.Fatalf("rejections=%v", observer.rejections)
	}
}

func TestKafkaTraceSinkConcurrentOfferAndShutdownNeverProducesAfterFlushStarts(t *testing.T) {
	producer := &asyncKafkaProducer{}
	sink, err := agentbatch.NewKafkaTraceSink(agentbatch.KafkaTraceSinkConfig{
		ProducerID: "nano-worker/test", Topic: "agent-trace", Producer: producer, MaxMessageBytes: 512 * 1024,
	})
	if err != nil {
		t.Fatal(err)
	}
	var wait sync.WaitGroup
	for index := 0; index < 100; index++ {
		wait.Add(1)
		go func() {
			defer wait.Done()
			err := sink.Offer(context.Background(), traceEnvelope("concurrent"))
			if err != nil && !errors.Is(err, agentbatch.ErrShutdown) {
				t.Errorf("Offer error=%v", err)
			}
		}()
	}
	if err := sink.Shutdown(context.Background()); err != nil {
		t.Fatal(err)
	}
	wait.Wait()
	producer.mu.Lock()
	defer producer.mu.Unlock()
	if producer.offersAfterFlush != 0 || producer.flushCalls != 1 || producer.closeCalls != 1 {
		t.Fatalf("offers after flush=%d flush=%d close=%d", producer.offersAfterFlush, producer.flushCalls, producer.closeCalls)
	}
}

type asyncKafkaOffer struct {
	ctx      context.Context
	message  agentbatch.KafkaMessage
	callback func(error)
}

type asyncKafkaProducer struct {
	mu               sync.Mutex
	offers           []asyncKafkaOffer
	bufferedRecords  int64
	bufferedBytes    int64
	flushCalls       int
	closeCalls       int
	flushStarted     bool
	offersAfterFlush int
}

func (p *asyncKafkaProducer) TryProduce(ctx context.Context, message agentbatch.KafkaMessage, callback func(error)) {
	p.mu.Lock()
	if p.flushStarted {
		p.offersAfterFlush++
	}
	p.offers = append(p.offers, asyncKafkaOffer{ctx: ctx, message: message, callback: callback})
	p.mu.Unlock()
}

func (p *asyncKafkaProducer) Flush(context.Context) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.flushStarted = true
	p.flushCalls++
	return nil
}

func (p *asyncKafkaProducer) BufferedRecords() int64 { return p.bufferedRecords }
func (p *asyncKafkaProducer) BufferedBytes() int64   { return p.bufferedBytes }
func (p *asyncKafkaProducer) Close() {
	p.mu.Lock()
	p.closeCalls++
	p.mu.Unlock()
}

func (p *asyncKafkaProducer) singleOffer(t *testing.T) (agentbatch.KafkaMessage, context.Context, func(error)) {
	t.Helper()
	p.mu.Lock()
	defer p.mu.Unlock()
	if len(p.offers) != 1 {
		t.Fatalf("offers=%d want=1", len(p.offers))
	}
	return p.offers[0].message, p.offers[0].ctx, p.offers[0].callback
}

type kafkaTraceDelivery struct {
	result  agentbatch.KafkaTraceDeliveryResult
	latency time.Duration
}

type recordingKafkaTraceObserver struct {
	mu              sync.Mutex
	rejections      []agentbatch.KafkaTraceRejectionReason
	deliveries      []kafkaTraceDelivery
	bufferedRecords int64
	bufferedBytes   int64
	shutdownFailure int
}

func (o *recordingKafkaTraceObserver) KafkaTraceOfferRejected(reason agentbatch.KafkaTraceRejectionReason) {
	o.mu.Lock()
	o.rejections = append(o.rejections, reason)
	o.mu.Unlock()
}

func (o *recordingKafkaTraceObserver) KafkaTraceDelivery(result agentbatch.KafkaTraceDeliveryResult, latency time.Duration) {
	o.mu.Lock()
	o.deliveries = append(o.deliveries, kafkaTraceDelivery{result: result, latency: latency})
	o.mu.Unlock()
}

func (o *recordingKafkaTraceObserver) KafkaTraceBuffered(records, bytes int64) {
	o.mu.Lock()
	o.bufferedRecords, o.bufferedBytes = records, bytes
	o.mu.Unlock()
}

func (o *recordingKafkaTraceObserver) KafkaTraceShutdownFailure() {
	o.mu.Lock()
	o.shutdownFailure++
	o.mu.Unlock()
}

func (o *recordingKafkaTraceObserver) singleDelivery(t *testing.T) agentbatch.KafkaTraceDeliveryResult {
	t.Helper()
	o.mu.Lock()
	defer o.mu.Unlock()
	if len(o.deliveries) != 1 {
		t.Fatalf("deliveries=%d want=1", len(o.deliveries))
	}
	return o.deliveries[0].result
}

func traceEnvelope(identity string) agentbatch.Envelope {
	traceID := agentobs.TraceID("trace-batch")
	rootID := agentobs.SpanID("root-batch")
	return agentbatch.Envelope{
		Trace: collector.TraceDescriptor{
			TraceID: traceID, RunID: "run-batch", ChatID: "chat-batch", NotebookID: "notebook-batch",
			RootSpanID: rootID, AgentName: "nano-research-agent", SchemaVersion: 1, SemanticConventionVersion: 1,
		},
		Record: agentobs.Record{
			SchemaVersion: 1, SemanticConventionVersion: 1, IdentityKey: identity,
			Kind: agentobs.RecordEvent, TraceID: traceID, SpanID: rootID,
			Name: "nano.batch.event", OccurredAt: time.Unix(1_700_300_000, 0).UTC(), PayloadVersion: 1,
		},
	}
}

func committedResult(batch collector.Batch) collector.BatchResult {
	result := collector.BatchResult{BatchID: batch.BatchID, Chunks: make([]collector.ChunkResult, len(batch.Chunks))}
	for index, chunk := range batch.Chunks {
		result.Chunks[index] = collector.ChunkResult{
			TraceID: chunk.Trace.TraceID, Status: collector.ChunkCommitted, CommittedThrough: len(chunk.Records),
		}
	}
	return result
}
