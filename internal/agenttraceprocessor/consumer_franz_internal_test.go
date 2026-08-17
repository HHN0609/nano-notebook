package agenttraceprocessor

import "testing"

func TestDefaultConsumerResetOffsetStartsAtEarliestRetainedRecord(t *testing.T) {
	reset := defaultConsumerResetOffset().EpochOffset()
	if reset.Offset != -2 {
		t.Fatalf("default reset offset = %d, want Kafka earliest sentinel -2", reset.Offset)
	}
}
