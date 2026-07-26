package realtime

import (
	"context"
	"log/slog"
	"sync"
	"time"

	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

type notificationExecutor interface {
	Exec(context.Context, string, ...any) (pgconn.CommandTag, error)
}

func NotifySourceDiscovery(ctx context.Context, db notificationExecutor, sessionID string) error {
	_, err := db.Exec(ctx, `select pg_notify($1,$2)`, SourceDiscoveryChannel, sessionID)
	return err
}

func NotifyNotebookSources(ctx context.Context, db notificationExecutor, notebookID string) error {
	_, err := db.Exec(ctx, `select pg_notify($1,$2)`, NotebookSourcesChannel, notebookID)
	return err
}

const (
	SourceDiscoveryChannel = "nano_source_discovery_sessions"
	NotebookSourcesChannel = "nano_notebook_sources"
)

type SourceListener struct {
	pool        *pgxpool.Pool
	onDiscovery func(string)
	onSources   func(string)
	ready       chan struct{}
	readyOnce   sync.Once
}

func NewSourceListener(pool *pgxpool.Pool, onDiscovery, onSources func(string)) *SourceListener {
	return &SourceListener{pool: pool, onDiscovery: onDiscovery, onSources: onSources, ready: make(chan struct{})}
}

func (l *SourceListener) Ready() <-chan struct{} {
	return l.ready
}

func (l *SourceListener) Run(ctx context.Context) error {
	for ctx.Err() == nil {
		if err := l.listen(ctx); err != nil && ctx.Err() == nil {
			slog.Warn("Source projection listener disconnected", "error", err)
			timer := time.NewTimer(time.Second)
			select {
			case <-ctx.Done():
				timer.Stop()
			case <-timer.C:
			}
		}
	}
	return nil
}

func (l *SourceListener) listen(ctx context.Context) error {
	connection, err := l.pool.Acquire(ctx)
	if err != nil {
		return err
	}
	defer connection.Release()
	if _, err := connection.Exec(ctx, `listen nano_source_discovery_sessions; listen nano_notebook_sources`); err != nil {
		return err
	}
	l.readyOnce.Do(func() { close(l.ready) })
	if l.onDiscovery != nil {
		l.onDiscovery("")
	}
	if l.onSources != nil {
		l.onSources("")
	}
	for ctx.Err() == nil {
		notification, err := connection.Conn().WaitForNotification(ctx)
		if err != nil {
			if ctx.Err() != nil {
				return nil
			}
			return err
		}
		switch notification.Channel {
		case SourceDiscoveryChannel:
			if l.onDiscovery != nil {
				l.onDiscovery(notification.Payload)
			}
		case NotebookSourcesChannel:
			if l.onSources != nil {
				l.onSources(notification.Payload)
			}
		}
	}
	return nil
}
