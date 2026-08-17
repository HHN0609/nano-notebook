package agenttraceprocessor

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"
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
}

type Runner struct {
	consumer     Consumer
	handler      Handler
	retryBackoff time.Duration
	reportError  func(error)
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
	return &Runner{
		consumer: config.Consumer, handler: config.Handler,
		retryBackoff: config.RetryBackoff, reportError: config.ReportError,
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
	defer r.consumer.AllowRebalance()
	type traceWork struct {
		messageIndexes []int
	}
	type messageResult struct {
		processed bool
		err       error
	}
	type traceKey struct {
		topic     string
		partition int32
		key       string
	}
	groups := make([]traceWork, 0)
	groupIndexes := make(map[traceKey]int)
	for messageIndex, message := range messages {
		key := traceKey{topic: message.Topic, partition: message.Partition, key: string(message.Key)}
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
				disposition, processErr := r.handler.Process(ctx, message)
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
			return fmt.Errorf("commit processed Agent Trace Kafka offsets: %w", err)
		}
	}
	return errors.Join(processingErrors...)
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
