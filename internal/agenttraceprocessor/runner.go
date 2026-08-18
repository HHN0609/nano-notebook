package agenttraceprocessor

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/huangxinxinyu/nano-notebook/internal/platform/metrics"
)

type Consumer interface {
	Poll(context.Context) ([]Message, error)
	Commit(context.Context, []Message) error
	AllowRebalance()
}

type Handler interface {
	Process(context.Context, Message) (Disposition, error)
}

type RunnerConfig struct {
	Consumer     Consumer
	Handler      Handler
	RetryBackoff time.Duration
	ReportError  func(error)
	Metrics      *metrics.Catalog
	Now          func() time.Time
}

type Runner struct {
	consumer     Consumer
	handler      Handler
	retryBackoff time.Duration
	reportError  func(error)
	metrics      *metrics.Catalog
	now          func() time.Time
}

func NewRunner(config RunnerConfig) (*Runner, error) {
	if config.Consumer == nil || config.Handler == nil {
		return nil, errors.New("Agent Trace Processor Runner configuration is incomplete")
	}
	if config.RetryBackoff <= 0 {
		config.RetryBackoff = 250 * time.Millisecond
	}
	if config.ReportError == nil {
		config.ReportError = func(error) {}
	}
	if config.Now == nil {
		config.Now = time.Now
	}
	return &Runner{
		consumer: config.Consumer, handler: config.Handler,
		retryBackoff: config.RetryBackoff, reportError: config.ReportError, metrics: config.Metrics, now: config.Now,
	}, nil
}

func (r *Runner) RunOnce(ctx context.Context) error {
	if r == nil || r.consumer == nil || r.handler == nil {
		return errors.New("nil Agent Trace Processor Runner")
	}
	messages, err := r.consumer.Poll(ctx)
	if err != nil {
		return fmt.Errorf("poll Agent Trace Kafka messages: %w", err)
	}
	if r.metrics != nil {
		bytes := 0
		oldest := make(map[int32]time.Time)
		for _, message := range messages {
			bytes += len(message.Key) + len(message.Value)
			partition := fmt.Sprint(message.Partition)
			lag := message.HighWatermark - message.Offset - 1
			if lag < 0 {
				lag = 0
			}
			r.metrics.AgentTraceConsumerLag.WithLabelValues(partition).Set(float64(lag))
			if !message.Timestamp.IsZero() && (oldest[message.Partition].IsZero() || message.Timestamp.Before(oldest[message.Partition])) {
				oldest[message.Partition] = message.Timestamp
			}
		}
		r.metrics.AgentTraceProcessorBatchRecords.Observe(float64(len(messages)))
		r.metrics.AgentTraceProcessorBatchBytes.Observe(float64(bytes))
		for partition, timestamp := range oldest {
			r.metrics.AgentTraceOldestMessageAge.WithLabelValues(fmt.Sprint(partition)).Set(maxSeconds(r.now().Sub(timestamp)))
		}
	}
	defer r.consumer.AllowRebalance()
	type traceWork struct {
		messageIndexes []int
	}
	type messageResult struct {
		processed bool
		err       error
	}
	groups := make([]traceWork, 0)
	groupIndexes := make(map[string]int)
	for messageIndex, message := range messages {
		key := string(message.Key)
		index, ok := groupIndexes[key]
		if !ok {
			index = len(groups)
			groupIndexes[key] = index
			groups = append(groups, traceWork{})
		}
		groups[index].messageIndexes = append(groups[index].messageIndexes, messageIndex)
	}
	results := make([]messageResult, len(messages))
	var wait sync.WaitGroup
	for _, group := range groups {
		wait.Add(1)
		go func() {
			defer wait.Done()
			for _, messageIndex := range group.messageIndexes {
				message := messages[messageIndex]
				started := time.Now()
				disposition, processErr := r.handler.Process(ctx, message)
				if r.metrics != nil {
					r.metrics.AgentTraceProcessorDuration.WithLabelValues(string(disposition)).Observe(time.Since(started).Seconds())
				}
				if processErr != nil || disposition != Commit {
					if processErr == nil {
						processErr = fmt.Errorf("processor returned disposition %q", disposition)
					}
					results[messageIndex].err = fmt.Errorf("process Agent Trace Kafka message at %s/%d/%d: %w", message.Topic, message.Partition, message.Offset, processErr)
					return
				}
				results[messageIndex].processed = true
			}
		}()
	}
	wait.Wait()
	processed := make([]Message, 0, len(messages))
	processingErrors := make([]error, 0, len(groups))
	type partitionKey struct {
		topic     string
		partition int32
	}
	blocked := make(map[partitionKey]bool)
	for index, result := range results {
		if result.err != nil {
			processingErrors = append(processingErrors, result.err)
		}
		message := messages[index]
		partition := partitionKey{topic: message.Topic, partition: message.Partition}
		if blocked[partition] {
			continue
		}
		if !result.processed {
			blocked[partition] = true
			continue
		}
		processed = append(processed, message)
	}
	if len(processed) > 0 {
		if err := r.consumer.Commit(ctx, processed); err != nil {
			if r.metrics != nil {
				r.metrics.AgentTraceOffsetCommitFailures.Inc()
			}
			return fmt.Errorf("commit processed Agent Trace Kafka offsets: %w", err)
		}
		if r.metrics != nil {
			for _, message := range processed {
				if !message.Timestamp.IsZero() {
					r.metrics.AgentTraceSearchableFreshness.Set(maxSeconds(r.now().Sub(message.Timestamp)))
				}
			}
		}
	}
	return errors.Join(processingErrors...)
}

func maxSeconds(duration time.Duration) float64 {
	if duration < 0 {
		return 0
	}
	return duration.Seconds()
}

func (r *Runner) Run(ctx context.Context) error {
	for {
		if err := ctx.Err(); err != nil {
			return nil
		}
		if err := r.RunOnce(ctx); err != nil {
			if ctx.Err() != nil {
				return nil
			}
			r.reportError(err)
			timer := time.NewTimer(r.retryBackoff)
			select {
			case <-ctx.Done():
				timer.Stop()
				return nil
			case <-timer.C:
			}
		}
	}
}
