package metrics

import "time"

func (c *Catalog) KafkaTraceOfferRejected(reason string) {
	if c != nil {
		c.AgentTraceProducerOfferRejected.WithLabelValues(reason).Inc()
	}
}

func (c *Catalog) KafkaTraceDelivery(result string, latency time.Duration) {
	if c != nil {
		c.AgentTraceProducerDeliveries.WithLabelValues(result).Inc()
		c.AgentTraceProducerDeliveryDuration.WithLabelValues(result).Observe(latency.Seconds())
	}
}

func (c *Catalog) KafkaTraceBuffered(records, bytes int64) {
	if c != nil {
		c.AgentTraceProducerBufferedRecords.Set(float64(records))
		c.AgentTraceProducerBufferedBytes.Set(float64(bytes))
	}
}

func (c *Catalog) KafkaTraceShutdownFailure() {
	if c != nil {
		c.AgentTraceProducerShutdownFailures.Inc()
	}
}
