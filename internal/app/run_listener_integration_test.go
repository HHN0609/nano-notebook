package app_test

import (
	"context"
	"testing"
	"time"

	"github.com/huangxinxinyu/nano-notebook/internal/realtime"
)

func TestSharedRunListenerForwardsOnlyTheCommittedRunID(t *testing.T) {
	api := newTestAPI(t)
	notifications := make(chan string, 1)
	listener := realtime.NewRunListener(api.db.Pool(), func(runID string) {
		if runID != "" {
			notifications <- runID
		}
	})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- listener.Run(ctx) }()

	select {
	case <-listener.Ready():
	case <-time.After(3 * time.Second):
		t.Fatal("Run listener did not become ready")
	}
	if _, err := api.db.Pool().Exec(ctx, `select pg_notify('nano_agent_runs', 'run_committed')`); err != nil {
		t.Fatal(err)
	}
	select {
	case runID := <-notifications:
		if runID != "run_committed" {
			t.Fatalf("notification payload = %q", runID)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("Run notification was not forwarded")
	}
	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("Run listener did not stop")
	}
}

func TestSharedSourceListenerRoutesCommittedProjectionIdentities(t *testing.T) {
	api := newTestAPI(t)
	discovery := make(chan string, 1)
	sources := make(chan string, 1)
	listener := realtime.NewSourceListener(api.db.Pool(), func(sessionID string) {
		if sessionID != "" {
			discovery <- sessionID
		}
	}, func(notebookID string) {
		if notebookID != "" {
			sources <- notebookID
		}
	})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- listener.Run(ctx) }()
	select {
	case <-listener.Ready():
	case <-time.After(3 * time.Second):
		t.Fatal("Source listener did not become ready")
	}
	if _, err := api.db.Pool().Exec(ctx, `
		select pg_notify('nano_source_discovery_sessions', 'dsc_committed');
		select pg_notify('nano_notebook_sources', 'nb_committed');
	`); err != nil {
		t.Fatal(err)
	}
	select {
	case value := <-discovery:
		if value != "dsc_committed" {
			t.Fatalf("Discovery notification=%q", value)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("Discovery notification was not forwarded")
	}
	select {
	case value := <-sources:
		if value != "nb_committed" {
			t.Fatalf("Source notification=%q", value)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("Source notification was not forwarded")
	}
	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("Source listener did not stop")
	}
}
