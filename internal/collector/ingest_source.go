package collector

import "context"

// KafkaSourcePosition is the immutable broker coordinate of one ingested
// Durable Agent Trace message.
type KafkaSourcePosition struct {
	Topic     string
	Partition int32
	Offset    int64
}

type kafkaSourcePositionContextKey struct{}

func ContextWithKafkaSourcePosition(ctx context.Context, source KafkaSourcePosition) context.Context {
	return context.WithValue(ctx, kafkaSourcePositionContextKey{}, source)
}

func KafkaSourcePositionFromContext(ctx context.Context) (KafkaSourcePosition, bool) {
	if ctx == nil {
		return KafkaSourcePosition{}, false
	}
	source, ok := ctx.Value(kafkaSourcePositionContextKey{}).(KafkaSourcePosition)
	return source, ok
}
