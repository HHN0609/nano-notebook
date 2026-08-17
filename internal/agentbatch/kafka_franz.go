package agentbatch

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/twmb/franz-go/pkg/kgo"
)

// FranzKafkaConfig bounds the concrete Kafka producer used by Nano processes.
type FranzKafkaConfig struct {
	Brokers            []string
	ClientID           string
	MaxBufferedRecords int
	MaxBufferedBytes   int
	DeliveryTimeout    time.Duration
	Linger             time.Duration
}

// FranzKafkaProducer adapts franz-go to the client-neutral KafkaProducer contract.
type FranzKafkaProducer struct {
	client *kgo.Client
}

func NewFranzKafkaProducer(config FranzKafkaConfig) (*FranzKafkaProducer, error) {
	brokers := make([]string, 0, len(config.Brokers))
	for _, broker := range config.Brokers {
		if trimmed := strings.TrimSpace(broker); trimmed != "" {
			brokers = append(brokers, trimmed)
		}
	}
	config.ClientID = strings.TrimSpace(config.ClientID)
	if len(brokers) == 0 || config.ClientID == "" || config.MaxBufferedRecords < 1 || config.MaxBufferedBytes < 1 ||
		config.DeliveryTimeout <= 0 || config.Linger < 0 {
		return nil, errors.New("Agent Trace franz-go producer configuration is invalid")
	}
	client, err := kgo.NewClient(
		kgo.SeedBrokers(brokers...),
		kgo.ClientID(config.ClientID),
		kgo.RequiredAcks(kgo.AllISRAcks()),
		kgo.RecordDeliveryTimeout(config.DeliveryTimeout),
		kgo.ProducerLinger(config.Linger),
		kgo.MaxBufferedRecords(config.MaxBufferedRecords),
		kgo.MaxBufferedBytes(config.MaxBufferedBytes),
		kgo.ProducerBatchCompression(kgo.ZstdCompression(), kgo.Lz4Compression()),
	)
	if err != nil {
		return nil, err
	}
	return &FranzKafkaProducer{client: client}, nil
}

func (p *FranzKafkaProducer) ProduceSync(ctx context.Context, messages []KafkaMessage) []error {
	if p == nil || p.client == nil {
		return []error{errors.New("nil Agent Trace franz-go producer")}
	}
	records := make([]*kgo.Record, len(messages))
	for index, message := range messages {
		records[index] = &kgo.Record{
			Topic: message.Topic,
			Key:   append([]byte(nil), message.Key...),
			Value: append([]byte(nil), message.Value...),
		}
	}
	results := p.client.ProduceSync(ctx, records...)
	hasError := false
	errs := make([]error, len(results))
	for index, result := range results {
		errs[index] = result.Err
		hasError = hasError || result.Err != nil
	}
	if !hasError {
		return nil
	}
	return errs
}

func (p *FranzKafkaProducer) Ping(ctx context.Context) error {
	if p == nil || p.client == nil {
		return errors.New("nil Agent Trace franz-go producer")
	}
	return p.client.Ping(ctx)
}

func (p *FranzKafkaProducer) Close() {
	if p != nil && p.client != nil {
		p.client.Close()
	}
}
