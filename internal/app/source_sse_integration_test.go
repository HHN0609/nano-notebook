package app_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/huangxinxinyu/nano-notebook/internal/chat"
	"github.com/huangxinxinyu/nano-notebook/internal/realtime"
	"github.com/huangxinxinyu/nano-notebook/internal/source"
	"github.com/huangxinxinyu/nano-notebook/internal/sourcediscovery"
	"github.com/jackc/pgx/v5"
)

func TestSourceDiscoverySSESendsInitialAuthorizedSnapshot(t *testing.T) {
	api := newTestAPI(t)
	owner := api.register(t, "discovery-sse-owner@example.com")
	notebookID := createSourceTestNotebook(t, api, owner, "discovery-sse")
	ownerID := sourceTestUserID(t, api, "discovery-sse-owner@example.com")
	if err := api.db.WithRequestPrincipal(context.Background(), ownerID, func(tx pgx.Tx) error {
		_, err := sourcediscovery.NewStore(tx).CreateSession(context.Background(), sourcediscovery.CreateSessionCommand{
			ID: "dsc_sse_initial", JobID: "dscjob_sse_initial", NotebookID: notebookID, UserID: ownerID,
			Origin: sourcediscovery.OriginManual, Query: "event driven sources",
		})
		return err
	}); err != nil {
		t.Fatal(err)
	}

	body, headers := openInitialSSE(t, api, "/api/v1/source-discovery-sessions/dsc_sse_initial/events", owner)
	if !strings.HasPrefix(headers.Get("Content-Type"), "text/event-stream") ||
		!strings.Contains(body, "event: discovery") || !strings.Contains(body, `"id":"dsc_sse_initial"`) ||
		!strings.Contains(body, `"status":"searching"`) || !strings.Contains(body, `"query":"event driven sources"`) {
		t.Fatalf("headers=%v body=%s", headers, body)
	}
}

func TestSourceDiscoverySSEProjectsCommittedRetry(t *testing.T) {
	api := newTestAPI(t)
	owner := api.register(t, "discovery-sse-live@example.com")
	notebookID := createSourceTestNotebook(t, api, owner, "discovery-sse-live")
	ownerID := sourceTestUserID(t, api, "discovery-sse-live@example.com")
	ctx := context.Background()
	if err := api.db.WithRequestPrincipal(ctx, ownerID, func(tx pgx.Tx) error {
		_, err := sourcediscovery.NewStore(tx).CreateSession(ctx, sourcediscovery.CreateSessionCommand{
			ID: "dsc_sse_retry", JobID: "dscjob_sse_retry", NotebookID: notebookID, UserID: ownerID,
			Origin: sourcediscovery.OriginManual, Query: "retry by event",
		})
		return err
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := api.db.Pool().Exec(ctx, `
		update source_discovery_sessions set status='failed',error_code='provider_failed',completed_at=now() where id='dsc_sse_retry';
		update source_discovery_jobs set status='failed' where id='dscjob_sse_retry';
	`); err != nil {
		t.Fatal(err)
	}

	stopListener := startSourceProjectionListener(t, api)
	defer stopListener()
	requestCtx, cancel := context.WithCancel(context.Background())
	defer cancel()
	writer, done := startSSE(t, api, requestCtx, "/api/v1/source-discovery-sessions/dsc_sse_retry/events", owner)
	waitForSSEFlush(t, writer, done)
	if !strings.Contains(writer.body(), `"status":"failed"`) {
		t.Fatalf("initial projection=%s", writer.body())
	}
	if err := api.db.WithRequestPrincipal(ctx, ownerID, func(tx pgx.Tx) error {
		_, err := sourcediscovery.NewStore(tx).RetryFailedSession(ctx, "dsc_sse_retry", "dscjob_sse_retry_next")
		return err
	}); err != nil {
		t.Fatal(err)
	}
	waitForSSEFlush(t, writer, done)
	if !strings.Contains(writer.body(), `"status":"searching"`) {
		t.Fatalf("updated projection=%s", writer.body())
	}
	cancel()
	waitForSSEStop(t, done)
}

func TestNotebookSourcesSSESendsInitialSourcesProjection(t *testing.T) {
	api, owner, _, chatID := newChatFixture(t, "sources-sse-owner@example.com")
	ownerID := sourceTestUserID(t, api, "sources-sse-owner@example.com")
	var notebookID string
	if err := api.db.Pool().QueryRow(context.Background(), `select notebook_id from chat_chats where id=$1`, chatID).Scan(&notebookID); err != nil {
		t.Fatal(err)
	}
	if err := api.db.WithRequestPrincipal(context.Background(), ownerID, func(tx pgx.Tx) error {
		_, err := source.NewStore(tx).CreateUploaded(context.Background(), source.CreateUploadedCommand{
			ID: "src_sse_processing", NotebookID: notebookID, Title: "Event source.txt", Format: source.FormatTXT,
			MediaType: "text/plain", ByteSize: 12, ContentSHA256: strings.Repeat("a", 64), OriginalObjectKey: "sources/src_sse_processing/original",
		})
		return err
	}); err != nil {
		t.Fatal(err)
	}

	path := "/api/v1/notebooks/" + notebookID + "/sources/events?chat_id=" + url.QueryEscape(chatID)
	body, headers := openInitialSSE(t, api, path, owner)
	if !strings.HasPrefix(headers.Get("Content-Type"), "text/event-stream") ||
		!strings.Contains(body, "event: sources") || !strings.Contains(body, `"id":"src_sse_processing"`) ||
		!strings.Contains(body, `"state":"processing"`) || !strings.Contains(body, `"source_ids":[]`) {
		t.Fatalf("headers=%v body=%s", headers, body)
	}
}

func TestNotebookSourcesSSEProjectsCommittedChatSelection(t *testing.T) {
	api, owner, _, chatID := newChatFixture(t, "sources-sse-live@example.com")
	ownerID := sourceTestUserID(t, api, "sources-sse-live@example.com")
	var notebookID string
	if err := api.db.Pool().QueryRow(context.Background(), `select notebook_id from chat_chats where id=$1`, chatID).Scan(&notebookID); err != nil {
		t.Fatal(err)
	}
	installReadyEvidenceSetFixture(t, api, notebookID, "src_sse_ready", "evr_sse_ready", "", "")
	stopListener := startSourceProjectionListener(t, api)
	defer stopListener()
	requestCtx, cancel := context.WithCancel(context.Background())
	defer cancel()
	path := "/api/v1/notebooks/" + notebookID + "/sources/events?chat_id=" + url.QueryEscape(chatID)
	writer, done := startSSE(t, api, requestCtx, path, owner)
	waitForSSEFlush(t, writer, done)
	if !strings.Contains(writer.body(), `"source_ids":[]`) {
		t.Fatalf("initial projection=%s", writer.body())
	}
	if err := api.db.WithRequestPrincipal(context.Background(), ownerID, func(tx pgx.Tx) error {
		_, err := chat.NewStore(tx).ReplaceSourceSelection(context.Background(), ownerID, chatID, []string{"src_sse_ready"})
		return err
	}); err != nil {
		t.Fatal(err)
	}
	waitForSSEFlush(t, writer, done)
	if !strings.Contains(writer.body(), `"source_ids":["src_sse_ready"]`) {
		t.Fatalf("updated projection=%s", writer.body())
	}
	cancel()
	waitForSSEStop(t, done)
}

func TestSourceDiscoverySSEIgnoresUncommittedAndUnrelatedWakeups(t *testing.T) {
	api := newTestAPI(t)
	owner := api.register(t, "discovery-sse-isolation@example.com")
	notebookID := createSourceTestNotebook(t, api, owner, "discovery-sse-isolation")
	ownerID := sourceTestUserID(t, api, "discovery-sse-isolation@example.com")
	ctx := context.Background()
	if err := api.db.WithRequestPrincipal(ctx, ownerID, func(tx pgx.Tx) error {
		_, err := sourcediscovery.NewStore(tx).CreateSession(ctx, sourcediscovery.CreateSessionCommand{
			ID: "dsc_sse_isolation", JobID: "dscjob_sse_isolation", NotebookID: notebookID, UserID: ownerID,
			Origin: sourcediscovery.OriginManual, Query: "isolated events",
		})
		return err
	}); err != nil {
		t.Fatal(err)
	}
	stopListener := startSourceProjectionListener(t, api)
	defer stopListener()
	requestCtx, cancel := context.WithCancel(context.Background())
	defer cancel()
	writer, done := startSSE(t, api, requestCtx, "/api/v1/source-discovery-sessions/dsc_sse_isolation/events", owner)
	waitForSSEFlush(t, writer, done)

	tx, err := api.db.Pool().Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if err := realtime.NotifySourceDiscovery(ctx, tx, "dsc_sse_isolation"); err != nil {
		t.Fatal(err)
	}
	expectNoSSEFlush(t, writer, done)
	if err := tx.Rollback(ctx); err != nil {
		t.Fatal(err)
	}
	if err := realtime.NotifySourceDiscovery(ctx, api.db.Pool(), "dsc_unrelated"); err != nil {
		t.Fatal(err)
	}
	expectNoSSEFlush(t, writer, done)
	if err := realtime.NotifySourceDiscovery(ctx, api.db.Pool(), "dsc_sse_isolation"); err != nil {
		t.Fatal(err)
	}
	waitForSSEFlush(t, writer, done)
	cancel()
	waitForSSEStop(t, done)
}

func TestSourceSSEEndpointsRejectNonMembers(t *testing.T) {
	api := newTestAPI(t)
	owner := api.register(t, "source-sse-auth-owner@example.com")
	notebookID := createSourceTestNotebook(t, api, owner, "source-sse-auth")
	ownerID := sourceTestUserID(t, api, "source-sse-auth-owner@example.com")
	if err := api.db.WithRequestPrincipal(context.Background(), ownerID, func(tx pgx.Tx) error {
		_, err := sourcediscovery.NewStore(tx).CreateSession(context.Background(), sourcediscovery.CreateSessionCommand{
			ID: "dsc_sse_private", JobID: "dscjob_sse_private", NotebookID: notebookID, UserID: ownerID,
			Origin: sourcediscovery.OriginManual, Query: "private events",
		})
		return err
	}); err != nil {
		t.Fatal(err)
	}
	intruder := api.register(t, "source-sse-auth-intruder@example.com")
	for _, path := range []string{
		"/api/v1/source-discovery-sessions/dsc_sse_private/events",
		"/api/v1/notebooks/" + notebookID + "/sources/events",
	} {
		request := httptest.NewRequest(http.MethodGet, path, nil)
		request.AddCookie(intruder)
		response := httptest.NewRecorder()
		api.handler.ServeHTTP(response, request)
		if response.Code != http.StatusNotFound {
			t.Fatalf("GET %s status=%d body=%s", path, response.Code, response.Body.String())
		}
	}
}

func TestSourceListenerReconnectReloadsActiveDiscoveryStream(t *testing.T) {
	api := newTestAPI(t)
	owner := api.register(t, "discovery-sse-reconnect@example.com")
	notebookID := createSourceTestNotebook(t, api, owner, "discovery-sse-reconnect")
	ownerID := sourceTestUserID(t, api, "discovery-sse-reconnect@example.com")
	if err := api.db.WithRequestPrincipal(context.Background(), ownerID, func(tx pgx.Tx) error {
		_, err := sourcediscovery.NewStore(tx).CreateSession(context.Background(), sourcediscovery.CreateSessionCommand{
			ID: "dsc_sse_reconnect", JobID: "dscjob_sse_reconnect", NotebookID: notebookID, UserID: ownerID,
			Origin: sourcediscovery.OriginManual, Query: "reconnect events",
		})
		return err
	}); err != nil {
		t.Fatal(err)
	}
	requestCtx, cancelRequest := context.WithCancel(context.Background())
	defer cancelRequest()
	writer, streamDone := startSSE(t, api, requestCtx, "/api/v1/source-discovery-sessions/dsc_sse_reconnect/events", owner)
	waitForSSEFlush(t, writer, streamDone)

	listenerCtx, cancelListener := context.WithCancel(context.Background())
	listener := realtime.NewSourceListener(api.db.Pool(), api.server.NotifySourceDiscovery, api.server.NotifyNotebookSources)
	listenerDone := make(chan error, 1)
	go func() { listenerDone <- listener.Run(listenerCtx) }()
	select {
	case <-listener.Ready():
	case <-time.After(3 * time.Second):
		t.Fatal("Source listener did not reconnect")
	}
	waitForSSEFlush(t, writer, streamDone)
	cancelListener()
	select {
	case err := <-listenerDone:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("Source listener did not stop")
	}
	cancelRequest()
	waitForSSEStop(t, streamDone)
}

func openInitialSSE(t *testing.T, api *testAPI, path string, cookie *http.Cookie) (string, http.Header) {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	request := httptest.NewRequest(http.MethodGet, path, nil).WithContext(ctx)
	request.AddCookie(cookie)
	writer := newStreamingRecorder()
	done := make(chan struct{})
	go func() {
		api.handler.ServeHTTP(writer, request)
		close(done)
	}()
	select {
	case <-writer.flushes:
		cancel()
	case <-done:
		cancel()
		t.Fatalf("SSE request ended before its initial projection: status=%d body=%s", writer.status, writer.body())
	case <-time.After(3 * time.Second):
		cancel()
		t.Fatal("SSE request did not send its initial projection")
	}
	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatal("SSE request did not stop after cancellation")
	}
	return writer.body(), writer.header.Clone()
}

func startSourceProjectionListener(t *testing.T, api *testAPI) func() {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	listener := realtime.NewSourceListener(api.db.Pool(), api.server.NotifySourceDiscovery, api.server.NotifyNotebookSources)
	done := make(chan error, 1)
	go func() { done <- listener.Run(ctx) }()
	select {
	case <-listener.Ready():
	case <-time.After(3 * time.Second):
		cancel()
		t.Fatal("Source listener did not become ready")
	}
	return func() {
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
}

func startSSE(t *testing.T, api *testAPI, ctx context.Context, path string, cookie *http.Cookie) (*streamingRecorder, <-chan struct{}) {
	t.Helper()
	request := httptest.NewRequest(http.MethodGet, path, nil).WithContext(ctx)
	request.AddCookie(cookie)
	writer := newStreamingRecorder()
	done := make(chan struct{})
	go func() {
		api.handler.ServeHTTP(writer, request)
		close(done)
	}()
	return writer, done
}

func waitForSSEFlush(t *testing.T, writer *streamingRecorder, done <-chan struct{}) {
	t.Helper()
	select {
	case <-writer.flushes:
	case <-done:
		t.Fatalf("SSE ended before update: status=%d body=%s", writer.status, writer.body())
	case <-time.After(3 * time.Second):
		t.Fatalf("SSE update timed out: body=%s", writer.body())
	}
}

func expectNoSSEFlush(t *testing.T, writer *streamingRecorder, done <-chan struct{}) {
	t.Helper()
	select {
	case <-writer.flushes:
		t.Fatalf("SSE unexpectedly emitted an unrelated projection: body=%s", writer.body())
	case <-done:
		t.Fatalf("SSE ended while checking isolation: status=%d body=%s", writer.status, writer.body())
	case <-time.After(150 * time.Millisecond):
	}
}

func waitForSSEStop(t *testing.T, done <-chan struct{}) {
	t.Helper()
	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatal("SSE did not stop")
	}
}
