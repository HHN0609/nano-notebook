package agenttraceprocessor

import (
	"context"
	"errors"
	"fmt"
	"math"
	"strings"
	"time"

	"github.com/huangxinxinyu/nano-notebook/internal/platform/metrics"
	"github.com/twmb/franz-go/pkg/kgo"
)

type FranzConsumerConfig struct {
	Brokers          []string
	Topic            string
	PurgeTopic       string
	GroupID          string
	ClientID         string
	MaxPollRecords   int
	FetchMaxBytes    int64
	FetchMaxWait     time.Duration
	SessionTimeout   time.Duration
	RebalanceTimeout time.Duration
	Metrics          *metrics.Catalog
}

// FranzConsumer keeps a polled batch rebalance-fenced until every message is
// committed. A transiently failed suffix is returned again by the next Poll.
type FranzConsumer struct {
	client                *kgo.Client
	maxPollRecords        int
	pending               []*kgo.Record
	pendingHighWatermarks map[string]int64
}

// defaultConsumerResetOffset makes a fresh projection group rebuild from the
// earliest retained Agent Trace message. Existing groups still resume from
// their committed offsets; this only defines the safe fallback when no commit
// exists (including migration and disaster-recovery groups).
func defaultConsumerResetOffset() kgo.Offset {
	return kgo.NewOffset().AtStart()
}

func NewFranzConsumer(config FranzConsumerConfig) (*FranzConsumer, error) {
	config.Topic = strings.TrimSpace(config.Topic)
	config.PurgeTopic = strings.TrimSpace(config.PurgeTopic)
	config.GroupID = strings.TrimSpace(config.GroupID)
	config.ClientID = strings.TrimSpace(config.ClientID)
	brokers := make([]string, 0, len(config.Brokers))
	for _, broker := range config.Brokers {
		if broker = strings.TrimSpace(broker); broker != "" {
			brokers = append(brokers, broker)
		}
	}
	if len(brokers) == 0 || config.Topic == "" || config.GroupID == "" || config.ClientID == "" ||
		config.MaxPollRecords < 1 || config.FetchMaxBytes < 1 || config.FetchMaxBytes > math.MaxInt32 ||
		config.FetchMaxWait <= 0 || config.SessionTimeout <= 0 || config.RebalanceTimeout <= 0 {
		return nil, errors.New("Agent Trace Kafka Consumer configuration is incomplete or unbounded")
	}
	if config.PurgeTopic == config.Topic {
		return nil, errors.New("Agent Trace and purge Kafka topics must be distinct")
	}
	topics := []string{config.Topic}
	if config.PurgeTopic != "" {
		topics = append(topics, config.PurgeTopic)
	}
	options := []kgo.Opt{
		kgo.SeedBrokers(brokers...),
		kgo.ClientID(config.ClientID),
		kgo.ConsumerGroup(config.GroupID),
		kgo.ConsumeTopics(topics...),
		kgo.ConsumeResetOffset(defaultConsumerResetOffset()),
		kgo.DisableAutoCommit(),
		kgo.BlockRebalanceOnPoll(),
		kgo.FetchMaxBytes(int32(config.FetchMaxBytes)),
		kgo.FetchMaxWait(config.FetchMaxWait),
		kgo.SessionTimeout(config.SessionTimeout),
		kgo.RebalanceTimeout(config.RebalanceTimeout),
	}
	if config.Metrics != nil {
		options = append(options,
			kgo.OnPartitionsAssigned(func(context.Context, *kgo.Client, map[string][]int32) {
				config.Metrics.AgentTraceConsumerRebalances.WithLabelValues("assigned").Inc()
			}),
			kgo.OnPartitionsRevoked(func(context.Context, *kgo.Client, map[string][]int32) {
				config.Metrics.AgentTraceConsumerRebalances.WithLabelValues("revoked").Inc()
			}),
			kgo.OnPartitionsLost(func(context.Context, *kgo.Client, map[string][]int32) {
				config.Metrics.AgentTraceConsumerRebalances.WithLabelValues("lost").Inc()
			}),
		)
	}
	client, err := kgo.NewClient(options...)
	if err != nil {
		return nil, fmt.Errorf("create Agent Trace Kafka Consumer: %w", err)
	}
	return &FranzConsumer{client: client, maxPollRecords: config.MaxPollRecords, pendingHighWatermarks: make(map[string]int64)}, nil
}

func (c *FranzConsumer) Ping(ctx context.Context) error {
	if c == nil || c.client == nil {
		return errors.New("nil Agent Trace Kafka Consumer")
	}
	return c.client.Ping(ctx)
}

func (c *FranzConsumer) Poll(ctx context.Context) ([]Message, error) {
	if c == nil || c.client == nil {
		return nil, errors.New("nil Agent Trace Kafka Consumer")
	}
	if len(c.pending) == 0 {
		fetches := c.client.PollRecords(ctx, c.maxPollRecords)
		if fetchErrors := fetches.Errors(); len(fetchErrors) > 0 {
			joined := make([]error, 0, len(fetchErrors))
			for _, fetchErr := range fetchErrors {
				joined = append(joined, fmt.Errorf("fetch %s/%d: %w", fetchErr.Topic, fetchErr.Partition, fetchErr.Err))
			}
			c.client.AllowRebalance()
			return nil, errors.Join(joined...)
		}
		c.pending = fetches.Records()
		fetches.EachPartition(func(partition kgo.FetchTopicPartition) {
			c.pendingHighWatermarks[fmt.Sprintf("%s/%d", partition.Topic, partition.Partition)] = partition.HighWatermark
		})
	}
	messages := make([]Message, len(c.pending))
	for index, record := range c.pending {
		key := fmt.Sprintf("%s/%d", record.Topic, record.Partition)
		highWatermark := c.pendingHighWatermarks[key]
		if highWatermark < record.Offset+1 {
			highWatermark = record.Offset + 1
		}
		messages[index] = Message{
			Topic: record.Topic, Partition: record.Partition, Offset: record.Offset,
			HighWatermark: highWatermark, Timestamp: record.Timestamp,
			Key: append([]byte(nil), record.Key...), Value: append([]byte(nil), record.Value...),
		}
	}
	return messages, nil
}

func (c *FranzConsumer) Commit(ctx context.Context, messages []Message) error {
	if c == nil || c.client == nil {
		return errors.New("nil Agent Trace Kafka Consumer")
	}
	if len(messages) == 0 {
		return nil
	}
	records := make([]*kgo.Record, 0, len(messages))
	for _, message := range messages {
		var found *kgo.Record
		for _, record := range c.pending {
			if record.Topic == message.Topic && record.Partition == message.Partition && record.Offset == message.Offset {
				found = record
				break
			}
		}
		if found == nil {
			return fmt.Errorf("Kafka commit references an unpolled offset %s/%d/%d", message.Topic, message.Partition, message.Offset)
		}
		records = append(records, found)
	}
	if err := c.client.CommitRecords(ctx, records...); err != nil {
		return err
	}
	committed := make(map[string]struct{}, len(messages))
	for _, message := range messages {
		committed[positionKey(message.Topic, message.Partition, message.Offset)] = struct{}{}
	}
	remaining := c.pending[:0]
	for _, record := range c.pending {
		if _, ok := committed[positionKey(record.Topic, record.Partition, record.Offset)]; !ok {
			remaining = append(remaining, record)
		}
	}
	c.pending = remaining
	if len(c.pending) == 0 {
		clear(c.pendingHighWatermarks)
	}
	return nil
}

func (c *FranzConsumer) AllowRebalance() {
	if c != nil && c.client != nil && len(c.pending) == 0 {
		c.client.AllowRebalance()
	}
}

func (c *FranzConsumer) Close() {
	if c != nil && c.client != nil {
		c.client.AllowRebalance()
		c.client.Close()
	}
}

func positionKey(topic string, partition int32, offset int64) string {
	return fmt.Sprintf("%s/%d/%d", topic, partition, offset)
}
