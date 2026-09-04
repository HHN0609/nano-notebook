package compose

import (
	"os"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

func TestDeployWorkflowBuildsEveryRepositoryOwnedProductionImage(t *testing.T) {
	workflow := readDeploymentFile(t, "../../.github/workflows/deploy.yml")

	required := map[string]string{
		"nano-notebook-nginx":                 "infra/nginx/Dockerfile",
		"nano-notebook-control-plane":         "infra/control-plane/Dockerfile",
		"nano-notebook-worker":                "infra/worker/Dockerfile",
		"nano-notebook-collector":             "infra/collector/Dockerfile",
		"nano-notebook-agent-trace-processor": "infra/agent-trace-processor/Dockerfile",
		"nano-notebook-document-renderer":     "infra/document-renderer/Dockerfile",
		"nano-notebook-web-reader":            "infra/web-reader/Dockerfile",
	}
	for repository, dockerfile := range required {
		if !strings.Contains(workflow, repository) {
			t.Errorf("deploy workflow is missing ECR image %s", repository)
		}
		if !strings.Contains(workflow, dockerfile) {
			t.Errorf("deploy workflow is missing Dockerfile %s", dockerfile)
		}
	}
}

func TestDeployWorkflowValidatesBeforeReconcileAndWaitsForHealth(t *testing.T) {
	workflow := readDeploymentFile(t, "../../.github/workflows/deploy.yml")

	if strings.Contains(workflow, "docker compose -f infra/compose/compose.prod.yaml down") {
		t.Fatal("deploy workflow tears down the production stack before reconciliation")
	}
	for _, required := range []string{
		"workflow_dispatch:",
		"NANO_PUBLIC_HOST",
		"docker compose -f infra/compose/compose.prod.yaml config --quiet",
		"docker compose -f infra/compose/compose.prod.yaml pull",
		"docker compose -f infra/compose/compose.prod.yaml up -d --remove-orphans --wait",
		"curl --fail --silent --show-error http://127.0.0.1/health",
		"curl --fail --silent --show-error http://127.0.0.1/version",
	} {
		if !strings.Contains(workflow, required) {
			t.Errorf("deploy workflow is missing %q", required)
		}
	}
}

func TestProductionWebBaseURLUsesThePublicHost(t *testing.T) {
	compose := readDeploymentFile(t, "compose.prod.yaml")
	if !strings.Contains(compose, "NANO_WEB_BASE_URL: http://${NANO_PUBLIC_HOST}") {
		t.Fatal("production web base URL is not derived from NANO_PUBLIC_HOST")
	}
}

func TestProductionEntryServicesExposeHealthChecks(t *testing.T) {
	data, err := os.ReadFile("compose.prod.yaml")
	if err != nil {
		t.Fatal(err)
	}
	var file composeFile
	if err := yaml.Unmarshal(data, &file); err != nil {
		t.Fatal(err)
	}

	controlPlane := file.Services["control-plane"]
	if got := strings.Join(controlPlane.Healthcheck.Test, " "); !strings.Contains(got, "127.0.0.1:8080/health") {
		t.Fatalf("control-plane health check does not cover /health: %q", got)
	}
	nginx := file.Services["nginx"]
	if nginx.DependsOn["control-plane"].Condition != "service_healthy" {
		t.Fatalf("nginx starts before control-plane is healthy: %#v", nginx.DependsOn["control-plane"])
	}
	if got := strings.Join(nginx.Healthcheck.Test, " "); !strings.Contains(got, "127.0.0.1/health") {
		t.Fatalf("nginx health check does not cover the public health path: %q", got)
	}
}

func readDeploymentFile(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	return string(data)
}
