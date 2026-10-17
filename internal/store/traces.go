package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"
)

// TraceRecord is one automation run as stored: the trace itself is
// opaque JSON written by the automation package.
type TraceRecord struct {
	ID           string
	AutomationID string
	StartedAt    time.Time
	FinishedAt   time.Time
	Result       string
	JSON         []byte
}

// SaveTrace stores a trace, replacing any earlier version of it.
func (s *Store) SaveTrace(ctx context.Context, rec TraceRecord) error {
	var finished any
	if !rec.FinishedAt.IsZero() {
		finished = formatTime(rec.FinishedAt)
	}
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO automation_traces (id, automation_id, started_at, finished_at, result, trace_json)
		VALUES (?, ?, ?, ?, ?, ?)
		ON CONFLICT(id) DO UPDATE SET
			finished_at = excluded.finished_at, result = excluded.result, trace_json = excluded.trace_json`,
		rec.ID, rec.AutomationID, formatTime(rec.StartedAt), finished, rec.Result, string(rec.JSON))
	if err != nil {
		return fmt.Errorf("save trace %s: %w", rec.ID, err)
	}
	return nil
}

// Trace returns one trace, or nil if there is none.
func (s *Store) Trace(ctx context.Context, id string) (*TraceRecord, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT id, automation_id, started_at, finished_at, result, trace_json FROM automation_traces WHERE id = ?`, id)
	if err != nil {
		return nil, fmt.Errorf("read trace %s: %w", id, err)
	}
	defer rows.Close()
	recs, err := scanTraces(rows)
	if err != nil {
		return nil, fmt.Errorf("read trace %s: %w", id, err)
	}
	if len(recs) == 0 {
		return nil, nil
	}
	return &recs[0], nil
}

// Traces returns an automation's traces, newest first, at most limit of
// them (all when limit is zero).
func (s *Store) Traces(ctx context.Context, automationID string, limit int) ([]TraceRecord, error) {
	if limit <= 0 {
		limit = -1
	}
	rows, err := s.db.QueryContext(ctx, `
		SELECT id, automation_id, started_at, finished_at, result, trace_json FROM automation_traces
		WHERE automation_id = ? ORDER BY started_at DESC, id DESC LIMIT ?`, automationID, limit)
	if err != nil {
		return nil, fmt.Errorf("read traces of %s: %w", automationID, err)
	}
	defer rows.Close()
	recs, err := scanTraces(rows)
	if err != nil {
		return nil, fmt.Errorf("read traces of %s: %w", automationID, err)
	}
	return recs, nil
}

// LatestTraces returns the newest trace of every automation.
func (s *Store) LatestTraces(ctx context.Context) ([]TraceRecord, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT t.id, t.automation_id, t.started_at, t.finished_at, t.result, t.trace_json FROM automation_traces t
		WHERE t.started_at = (SELECT MAX(started_at) FROM automation_traces WHERE automation_id = t.automation_id)`)
	if err != nil {
		return nil, fmt.Errorf("read latest traces: %w", err)
	}
	defer rows.Close()
	recs, err := scanTraces(rows)
	if err != nil {
		return nil, fmt.Errorf("read latest traces: %w", err)
	}
	return recs, nil
}

// PruneTraces drops all but the newest keep traces of an automation.
func (s *Store) PruneTraces(ctx context.Context, automationID string, keep int) error {
	_, err := s.db.ExecContext(ctx, `
		DELETE FROM automation_traces WHERE automation_id = ? AND id NOT IN (
			SELECT id FROM automation_traces WHERE automation_id = ? ORDER BY started_at DESC, id DESC LIMIT ?)`,
		automationID, automationID, keep)
	if err != nil {
		return fmt.Errorf("prune traces of %s: %w", automationID, err)
	}
	return nil
}

// DeleteTraces drops every trace of an automation.
func (s *Store) DeleteTraces(ctx context.Context, automationID string) error {
	if _, err := s.db.ExecContext(ctx, `DELETE FROM automation_traces WHERE automation_id = ?`, automationID); err != nil {
		return fmt.Errorf("delete traces of %s: %w", automationID, err)
	}
	return nil
}

func scanTraces(rows *sql.Rows) ([]TraceRecord, error) {
	var out []TraceRecord
	for rows.Next() {
		var rec TraceRecord
		var started string
		var finished sql.NullString
		var raw string
		if err := rows.Scan(&rec.ID, &rec.AutomationID, &started, &finished, &rec.Result, &raw); err != nil {
			return nil, err
		}
		rec.StartedAt = parseTime(started)
		if finished.Valid {
			rec.FinishedAt = parseTime(finished.String)
		}
		rec.JSON = []byte(raw)
		out = append(out, rec)
	}
	if err := rows.Err(); err != nil && !errors.Is(err, sql.ErrNoRows) {
		return nil, err
	}
	return out, nil
}
