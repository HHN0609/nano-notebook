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

func TestDefaultTraceTopologyUsesKafkaAndClickHouse(t *testing.T) {
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
	if !contains(stageB.Profiles, "postgres-trace-rollback") || stageB.Environment["NANO_AGENT_TRACE_PROCESSOR_STORE"] != "postgres" ||
		stageB.DependsOn["observability-postgres"].Condition != "service_healthy" {
		t.Fatalf("PostgreSQL rollback processor=%#v", stageB)
	}
	if len(stageC.Profiles) != 0 || stageC.Environment["NANO_AGENT_TRACE_PROCESSOR_STORE"] != "clickhouse" ||
		stageC.Environment["NANO_CLICKHOUSE_ADDR"] != "clickhouse:9000" || stageC.DependsOn["clickhouse"].Condition != "service_healthy" {
		t.Fatalf("default ClickHouse processor=%#v", stageC)
	}
	for name, service := range map[string]composeService{"postgres-rollback": stageB, "clickhouse-default": stageC} {
		if service.Build.Context != "../.." || service.Build.Dockerfile != "infra/agent-trace-processor/Dockerfile" ||
			service.DependsOn["kafka-init"].Condition != "service_completed_successfully" ||
			service.DependsOn["minio-init"].Condition != "service_completed_successfully" {
			t.Fatalf("%s processor dependencies/build=%#v", name, service)
		}
	}
	collector := file.Services["collector"]
	if len(collector.Profiles) != 0 || collector.Environment["NANO_COLLECTOR_STORE"] != "clickhouse" ||
		collector.DependsOn["clickhouse"].Condition != "service_healthy" {
		t.Fatalf("default Collector=%#v", collector)
	}
	if _, ok := collector.DependsOn["observability-postgres"]; ok {
		t.Fatal("default Collector still depends on Observability PostgreSQL")
	}
	if !contains(file.Services["observability-postgres"].Profiles, "postgres-trace-rollback") {
		t.Fatal("Observability PostgreSQL is still part of the default Compose topology")
	}
}

func TestProductionTraceTopologyUsesKafkaAndClickHouse(t *testing.T) {
	data, err := os.ReadFile("compose.prod.yaml")
	if err != nil {
		t.Fatal(err)
	}
	var file composeFile
	if err := yaml.Unmarshal(data, &file); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"control-plane", "worker"} {
		service := file.Services[name]
		if service.Environment["NANO_AGENT_TRACE_TRANSPORT"] != "kafka" ||
			service.Environment["NANO_AGENT_TRACE_KAFKA_BROKERS"] != "kafka:19092" {
			t.Fatalf("%s is not a Kafka Trace producer: %#v", name, service.Environment)
		}
	}
	processor, ok := file.Services["agent-trace-processor-clickhouse"]
	if !ok || processor.Environment["NANO_AGENT_TRACE_PROCESSOR_STORE"] != "clickhouse" ||
		processor.DependsOn["kafka-init"].Condition != "service_completed_successfully" ||
		processor.DependsOn["clickhouse"].Condition != "service_healthy" {
		t.Fatalf("production ClickHouse processor=%#v", processor)
	}
	collector := file.Services["collector"]
	if collector.Environment["NANO_COLLECTOR_STORE"] != "clickhouse" || collector.DependsOn["clickhouse"].Condition != "service_healthy" {
		t.Fatalf("production Collector=%#v", collector)
	}
	if _, ok := collector.DependsOn["observability-postgres"]; ok {
		t.Fatal("production Collector still depends on Observability PostgreSQL")
	}
	if !contains(file.Services["observability-postgres"].Profiles, "postgres-trace-rollback") {
		t.Fatal("production Observability PostgreSQL is still enabled by default")
	}
}

func TestStartAndGoGateUseDefaultClickHouseTraceTopology(t *testing.T) {
	start, err := os.ReadFile("../../scripts/start")
	if err != nil {
		t.Fatal(err)
	}
	startScript := string(start)
	for _, required := range []string{"NANO_AGENT_TRACE_TRANSPORT", "NANO_COLLECTOR_URL", "agent-trace-processor-clickhouse", "collector"} {
		if !strings.Contains(startScript, required) {
			t.Errorf("scripts/start is missing %s", required)
		}
	}
	if strings.Contains(startScript, "scripts/prepare-observability-db") || strings.Contains(startScript, "go run ./cmd/collector") {
		t.Error("scripts/start still starts the PostgreSQL-era Collector path")
	}
	testGo, err := os.ReadFile("../../scripts/test-go")
	if err != nil {
		t.Fatal(err)
	}
	testScript := string(testGo)
	if !strings.Contains(testScript, "NANO_TEST_CLICKHOUSE_ADDR") || !strings.Contains(testScript, "clickhouse") {
		t.Error("scripts/test-go does not provision and exercise ClickHouse")
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
