package metrics

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestAdminServerServesMetrics(t *testing.T) {
	reg := NewRegistry()
	NewCatalog(reg)
	server := NewAdminServer(":0", reg)
	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/metrics", nil)
	server.Handler.ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200 from /metrics, got %d", rr.Code)
	}
	if !strings.Contains(rr.Body.String(), "nano_runhub_subscribers") {
		t.Fatal("expected exposition to contain a catalog metric name")
	}
	if !strings.Contains(rr.Body.String(), "go_goroutines") {
		t.Fatal("expected exposition to contain the Go runtime collector")
	}
}

func TestAdminServerServesPprofIndex(t *testing.T) {
	reg := NewRegistry()
	server := NewAdminServer(":0", reg)
	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/debug/pprof/", nil)
	server.Handler.ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200 from /debug/pprof/, got %d", rr.Code)
	}
}
