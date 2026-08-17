package compose

import (
	"os"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

type composeFile struct {
	Services map[string]composeService `yaml:"services"`
	Volumes  map[string]any            `yaml:"volumes"`
}

type composeService struct {
	Image string `yaml:"image"`
	Build struct {
		Context    string `yaml:"context"`
		Dockerfile string `yaml:"dockerfile"`
	} `yaml:"build"`
	Profiles    []string          `yaml:"profiles"`
	Environment map[string]string `yaml:"environment"`
	Ports       []string          `yaml:"ports"`
	Volumes     []string          `yaml:"volumes"`
	Command     any               `yaml:"command"`
	DependsOn   map[string]struct {
		Condition string `yaml:"condition"`
	} `yaml:"depends_on"`
	Healthcheck struct {
		Test []string `yaml:"test"`
	} `yaml:"healthcheck"`
}

func TestKafkaComposeContract(t *testing.T) {
	data, err := os.ReadFile("compose.yaml")
	if err != nil {
		t.Fatalf("read compose.yaml: %v", err)
	}
	var file composeFile
	if err := yaml.Unmarshal(data, &file); err != nil {
		t.Fatalf("parse compose.yaml: %v", err)
	}

	kafka, ok := file.Services["kafka"]
	if !ok {
		t.Fatal("compose has no kafka service")
	}
	if kafka.Image != "apache/kafka:4.3.1" {
		t.Fatalf("Kafka image = %q, want pinned apache/kafka:4.3.1", kafka.Image)
	}
	if kafka.Environment["KAFKA_PROCESS_ROLES"] != "broker,controller" {
		t.Fatalf("Kafka process roles = %q", kafka.Environment["KAFKA_PROCESS_ROLES"])
	}
	if got := kafka.Environment["KAFKA_ADVERTISED_LISTENERS"]; !strings.Contains(got, "kafka:19092") || !strings.Contains(got, "localhost:59092") {
		t.Fatalf("Kafka advertised listeners do not cover container and host clients: %q", got)
	}
	if !contains(kafka.Ports, "127.0.0.1:59092:9092") {
		t.Fatalf("Kafka host port is not loopback-bound: %v", kafka.Ports)
	}
	if !contains(kafka.Volumes, "kafka-data:/var/lib/kafka/data") {
		t.Fatalf("Kafka data is not persistent: %v", kafka.Volumes)
	}
	if got := strings.Join(kafka.Healthcheck.Test, " "); !strings.Contains(got, "127.0.0.1:19092") {
		t.Fatalf("Kafka health check does not use the container-internal listener: %q", got)
	}
	if _, ok := file.Volumes["kafka-data"]; !ok {
		t.Fatal("kafka-data volume is not declared")
	}

	init, ok := file.Services["kafka-init"]
	if !ok {
		t.Fatal("compose has no kafka-init service")
	}
	if init.Image != kafka.Image {
		t.Fatalf("kafka-init image = %q, broker image = %q", init.Image, kafka.Image)
	}
	if init.DependsOn["kafka"].Condition != "service_healthy" {
		t.Fatalf("kafka-init dependency = %#v", init.DependsOn["kafka"])
	}
	if !contains(init.Volumes, "../kafka/init-topics.sh:/opt/nano/init-topics.sh:ro") {
		t.Fatalf("kafka-init does not mount the topic contract: %v", init.Volumes)
	}
}

func TestKafkaTopicContract(t *testing.T) {
	data, err := os.ReadFile("../kafka/init-topics.sh")
	if err != nil {
		t.Fatalf("read topic initializer: %v", err)
	}
	script := string(data)
	for _, topic := range []string{
		"nano.observability.agent-trace.v1",
		"nano.observability.agent-trace-purge.v1",
		"nano.observability.agent-trace-quarantine.v1",
		"nano.observability.otel-logs.v1",
		"nano.observability.otel-traces.v1",
	} {
		if !strings.Contains(script, topic) {
			t.Errorf("topic initializer is missing %s", topic)
		}
	}
	if !strings.Contains(script, "604800000") {
		t.Error("topic initializer does not pin the seven-day Kafka replay window")
	}
}

func TestClickHouseComposeContract(t *testing.T) {
	data, err := os.ReadFile("compose.yaml")
	if err != nil {
		t.Fatal(err)
	}
	var file composeFile
	if err := yaml.Unmarshal(data, &file); err != nil {
		t.Fatal(err)
	}
	clickhouse, ok := file.Services["clickhouse"]
	if !ok {
		t.Fatal("compose has no clickhouse service")
	}
	if clickhouse.Image != "clickhouse:26.3.17.56-jammy" {
		t.Fatalf("ClickHouse image=%q", clickhouse.Image)
	}
	if !contains(clickhouse.Ports, "127.0.0.1:58123:8123") || !contains(clickhouse.Ports, "127.0.0.1:59004:9000") {
		t.Fatalf("ClickHouse host ports are not loopback-bound: %v", clickhouse.Ports)
	}
	if !contains(clickhouse.Volumes, "clickhouse-data:/var/lib/clickhouse") {
		t.Fatalf("ClickHouse data is not persistent: %v", clickhouse.Volumes)
	}
	if _, ok := file.Volumes["clickhouse-data"]; !ok {
		t.Fatal("clickhouse-data volume is not declared")
	}
}

func TestAgentTraceProcessorStageBAndCComposeContracts(t *testing.T) {
	data, err := os.ReadFile("compose.yaml")
	if err != nil {
		t.Fatal(err)
	}
	var file composeFile
	if err := yaml.Unmarshal(data, &file); err != nil {
		t.Fatal(err)
	}
	stageB, ok := file.Services["agent-trace-processor-postgres"]
	if !ok {
		t.Fatal("compose has no Stage B Agent Trace Processor")
	}
	stageC, ok := file.Services["agent-trace-processor-clickhouse"]
	if !ok {
		t.Fatal("compose has no Stage C Agent Trace Processor")
	}
	if !contains(stageB.Profiles, "stage-b") || stageB.Environment["NANO_AGENT_TRACE_PROCESSOR_STORE"] != "postgres" ||
		stageB.DependsOn["observability-postgres"].Condition != "service_healthy" {
		t.Fatalf("Stage B processor=%#v", stageB)
	}
	if !contains(stageC.Profiles, "stage-c") || stageC.Environment["NANO_AGENT_TRACE_PROCESSOR_STORE"] != "clickhouse" ||
		stageC.Environment["NANO_CLICKHOUSE_ADDR"] != "clickhouse:9000" || stageC.DependsOn["clickhouse"].Condition != "service_healthy" {
		t.Fatalf("Stage C processor=%#v", stageC)
	}
	for name, service := range map[string]composeService{"stage-b": stageB, "stage-c": stageC} {
		if service.Build.Context != "../.." || service.Build.Dockerfile != "infra/agent-trace-processor/Dockerfile" ||
			service.DependsOn["kafka-init"].Condition != "service_completed_successfully" ||
			service.DependsOn["minio-init"].Condition != "service_completed_successfully" {
			t.Fatalf("%s processor dependencies/build=%#v", name, service)
		}
	}
}

func contains(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}
