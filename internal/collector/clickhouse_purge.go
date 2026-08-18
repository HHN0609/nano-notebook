package collector

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/huangxinxinyu/nano-notebook/internal/agentobs"
)

const (
	clickHousePurgeTombstoned           = "tombstoned"
	clickHousePurgeReplayObjectsRemoved = "replay_objects_removed"
	clickHousePurgeReplayRefsRemoved    = "replay_refs_removed"
	clickHousePurgeRawRecordsRemoved    = "raw_records_removed"
	clickHousePurgeSummariesRemoved     = "summaries_removed"
	clickHousePurgeSpanAnalyticsRemoved = "span_analytics_removed"
	clickHousePurgeComplete             = "complete"
)

var clickHousePurgeStages = []string{
	clickHousePurgeTombstoned,
	clickHousePurgeReplayObjectsRemoved,
	clickHousePurgeReplayRefsRemoved,
	clickHousePurgeRawRecordsRemoved,
	clickHousePurgeSummariesRemoved,
	clickHousePurgeSpanAnalyticsRemoved,
	clickHousePurgeComplete,
}

type clickHousePurgeState struct {
	commandID     string
	commandSHA256 string
	traceID       agentobs.TraceID
	stage         string
}

func (s *ClickHouseStore) TombstoneTrace(ctx context.Context, command PurgeCommand) error {
	if s == nil || s.connection == nil || s.replayObjects == nil {
		return errors.New("nil Collector ClickHouse purge Store")
	}
	source, ok := KafkaSourcePositionFromContext(ctx)
	if !ok || strings.TrimSpace(source.Topic) == "" || source.Partition < 0 || source.Offset < 0 {
		return errors.New("Collector ClickHouse purge is missing its Kafka source position")
	}
	commandSHA256, err := purgeCommandSHA256(command)
	if err != nil {
		return err
	}
	state, found, err := s.loadPurgeState(ctx, command.CommandID)
	if err != nil {
		return err
	}
	if found && (state.commandSHA256 != commandSHA256 || state.traceID != command.TraceID) {
		return &PurgeCommandError{Code: CodeIdentityConflict, Err: errors.New("Collector ClickHouse purge command identity changed")}
	}
	if !found {
		if err := s.ensureTombstone(ctx, command, commandSHA256, source); err != nil {
			return err
		}
		state = clickHousePurgeState{commandID: command.CommandID, commandSHA256: commandSHA256, traceID: command.TraceID, stage: clickHousePurgeTombstoned}
		if err := s.writePurgeState(ctx, state, source); err != nil {
			return err
		}
	}
	for state.stage != clickHousePurgeComplete {
		next, err := s.advanceClickHousePurge(ctx, state)
		if err != nil {
			return err
		}
		state.stage = next
		if err := s.writePurgeState(ctx, state, source); err != nil {
			return err
		}
	}
	return nil
}

func purgeCommandSHA256(command PurgeCommand) (string, error) {
	encoded, err := json.Marshal(struct {
		CommandID      string           `json:"command_id"`
		CommandVersion int              `json:"command_version"`
		Kind           string           `json:"kind"`
		TraceID        agentobs.TraceID `json:"trace_id"`
		RunID          string           `json:"run_id"`
		RequestedAt    int64            `json:"requested_at_unix_nano"`
		ProducerID     string           `json:"producer_id"`
	}{
		CommandID: command.CommandID, CommandVersion: command.CommandVersion, Kind: command.Kind,
		TraceID: command.TraceID, RunID: command.RunID, RequestedAt: command.RequestedAt.UnixNano(), ProducerID: command.ProducerID,
	})
	if err != nil {
		return "", err
	}
	digest := sha256.Sum256(encoded)
	return hex.EncodeToString(digest[:]), nil
}

func (s *ClickHouseStore) ensureTombstone(ctx context.Context, command PurgeCommand, commandSHA256 string, source KafkaSourcePosition) error {
	rows, err := s.connection.Query(ctx, `
		SELECT command_id, command_sha256 FROM obs_trace_tombstones FINAL WHERE trace_id = ?
	`, string(command.TraceID))
	if err != nil {
		return err
	}
	for rows.Next() {
		var existingID, existingSHA256 string
		if err := rows.Scan(&existingID, &existingSHA256); err != nil {
			rows.Close()
			return err
		}
		if existingID == command.CommandID && existingSHA256 != commandSHA256 {
			rows.Close()
			return &PurgeCommandError{Code: CodeIdentityConflict, Err: errors.New("Collector ClickHouse Tombstone command identity changed")}
		}
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return err
	}
	rows.Close()
	version := uint64(source.Offset) + 1
	if err := s.connection.Exec(ctx, `
		INSERT INTO obs_trace_tombstones (
			trace_id, command_sha256, command_id, command_version, run_id, producer_id,
			requested_at, requested_at_unix_nano, source_topic, source_partition, source_offset, tombstone_version
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
	`, string(command.TraceID), commandSHA256, command.CommandID, uint16(command.CommandVersion), command.RunID,
		command.ProducerID, command.RequestedAt.UTC(), command.RequestedAt.UnixNano(), source.Topic, source.Partition, source.Offset, version); err != nil {
		return err
	}
	tombstoned, err := s.isTombstoned(ctx, command.TraceID)
	if err != nil {
		return err
	}
	if !tombstoned {
		return errors.New("Collector ClickHouse Tombstone was not committed")
	}
	return nil
}

func (s *ClickHouseStore) loadPurgeState(ctx context.Context, commandID string) (clickHousePurgeState, bool, error) {
	rows, err := s.connection.Query(ctx, `
		SELECT command_id, command_sha256, trace_id, stage
		FROM obs_trace_purge_state FINAL WHERE command_id = ?
	`, commandID)
	if err != nil {
		return clickHousePurgeState{}, false, err
	}
	defer rows.Close()
	var state clickHousePurgeState
	found := false
	for rows.Next() {
		var traceID string
		if err := rows.Scan(&state.commandID, &state.commandSHA256, &traceID, &state.stage); err != nil {
			return clickHousePurgeState{}, false, err
		}
		state.traceID = agentobs.TraceID(traceID)
		if found {
			return clickHousePurgeState{}, false, &PurgeCommandError{Code: CodeIdentityConflict, Err: errors.New("Collector ClickHouse purge command has conflicting identities")}
		}
		found = true
	}
	if err := rows.Err(); err != nil {
		return clickHousePurgeState{}, false, err
	}
	if found && purgeStageVersion(state.stage) == 0 {
		return clickHousePurgeState{}, false, errors.New("Collector ClickHouse purge state is invalid")
	}
	return state, found, nil
}

func (s *ClickHouseStore) writePurgeState(ctx context.Context, state clickHousePurgeState, source KafkaSourcePosition) error {
	version := purgeStageVersion(state.stage)
	if version == 0 {
		return errors.New("Collector ClickHouse purge stage is invalid")
	}
	return s.connection.Exec(ctx, `
		INSERT INTO obs_trace_purge_state (
			command_id, command_sha256, trace_id, stage, tombstone_ack, replay_objects_ack,
			replay_refs_ack, raw_records_ack, summaries_ack, span_analytics_ack,
			source_topic, source_partition, source_offset, state_version
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
	`, state.commandID, state.commandSHA256, string(state.traceID), state.stage,
		version >= 1, version >= 2, version >= 3, version >= 4, version >= 5, version >= 6,
		source.Topic, source.Partition, source.Offset, version)
}

func purgeStageVersion(stage string) uint64 {
	for index, candidate := range clickHousePurgeStages {
		if stage == candidate {
			return uint64(index + 1)
		}
	}
	return 0
}

func (s *ClickHouseStore) advanceClickHousePurge(ctx context.Context, state clickHousePurgeState) (string, error) {
	switch state.stage {
	case clickHousePurgeTombstoned:
		rows, err := s.connection.Query(ctx, `SELECT DISTINCT object_key FROM obs_replay_payload_refs FINAL WHERE trace_id = ?`, string(state.traceID))
		if err != nil {
			return "", err
		}
		var keys []string
		for rows.Next() {
			var key string
			if err := rows.Scan(&key); err != nil {
				rows.Close()
				return "", err
			}
			keys = append(keys, key)
		}
		if err := rows.Err(); err != nil {
			rows.Close()
			return "", err
		}
		rows.Close()
		for _, key := range keys {
			if err := s.replayObjects.Delete(ctx, key); err != nil {
				return "", fmt.Errorf("delete purged ClickHouse Replay object: %w", err)
			}
		}
		return clickHousePurgeReplayObjectsRemoved, nil
	case clickHousePurgeReplayObjectsRemoved:
		return clickHousePurgeReplayRefsRemoved, s.deleteTraceRows(ctx, "obs_replay_payload_refs", state.traceID)
	case clickHousePurgeReplayRefsRemoved:
		return clickHousePurgeRawRecordsRemoved, s.deleteTraceRows(ctx, "obs_trace_records_raw", state.traceID)
	case clickHousePurgeRawRecordsRemoved:
		return clickHousePurgeSummariesRemoved, s.deleteTraceRows(ctx, "obs_trace_summaries", state.traceID)
	case clickHousePurgeSummariesRemoved:
		return clickHousePurgeSpanAnalyticsRemoved, s.deleteTraceRows(ctx, "obs_span_analytics", state.traceID)
	case clickHousePurgeSpanAnalyticsRemoved:
		return clickHousePurgeComplete, nil
	default:
		return "", errors.New("Collector ClickHouse purge state is invalid")
	}
}

func (s *ClickHouseStore) deleteTraceRows(ctx context.Context, table string, traceID agentobs.TraceID) error {
	switch table {
	case "obs_replay_payload_refs", "obs_trace_records_raw", "obs_trace_summaries", "obs_span_analytics":
	default:
		return errors.New("Collector ClickHouse purge table is invalid")
	}
	return s.connection.Exec(ctx, "ALTER TABLE "+table+" DELETE WHERE trace_id = ? SETTINGS mutations_sync = 2", string(traceID))
}

func (s *ClickHouseStore) isTombstoned(ctx context.Context, traceID agentobs.TraceID) (bool, error) {
	var count uint64
	if err := s.connection.QueryRow(ctx, `SELECT count() FROM obs_trace_tombstones FINAL WHERE trace_id = ?`, string(traceID)).Scan(&count); err != nil {
		return false, err
	}
	return count != 0, nil
}
