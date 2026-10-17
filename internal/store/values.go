package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/mikesmitty/interlock/openlcb"
)

// ValuesRecord is the cached configuration values of one node: every
// variable in the segments that hold event IDs, keyed by "space:address".
type ValuesRecord struct {
	NodeID          openlcb.NodeID
	SoftwareVersion string
	Values          map[string]string
	ReadAt          time.Time
}

// NodeValues returns a node's cached values, or nil if there are none.
func (s *Store) NodeValues(ctx context.Context, id openlcb.NodeID) (*ValuesRecord, error) {
	rec := &ValuesRecord{NodeID: id}
	var raw, readAt string
	err := s.db.QueryRowContext(ctx,
		`SELECT software_version, values_json, read_at FROM node_values WHERE node_id = ?`, id.String()).
		Scan(&rec.SoftwareVersion, &raw, &readAt)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read values of %v: %w", id, err)
	}
	if err := json.Unmarshal([]byte(raw), &rec.Values); err != nil {
		return nil, fmt.Errorf("stored values of %v: %w", id, err)
	}
	rec.ReadAt = parseTime(readAt)
	return rec, nil
}

// SaveNodeValues stores a node's values, replacing any previous set. The
// node must already be saved.
func (s *Store) SaveNodeValues(ctx context.Context, rec ValuesRecord) error {
	raw, err := json.Marshal(rec.Values)
	if err != nil {
		return fmt.Errorf("save values of %v: %w", rec.NodeID, err)
	}
	_, err = s.db.ExecContext(ctx, `
		INSERT INTO node_values (node_id, software_version, values_json, read_at) VALUES (?, ?, ?, ?)
		ON CONFLICT(node_id) DO UPDATE SET
			software_version = excluded.software_version, values_json = excluded.values_json, read_at = excluded.read_at`,
		rec.NodeID.String(), rec.SoftwareVersion, string(raw), formatTime(rec.ReadAt))
	if err != nil {
		return fmt.Errorf("save values of %v: %w", rec.NodeID, err)
	}
	return nil
}

// DeleteNodeValues drops a node's cached values.
func (s *Store) DeleteNodeValues(ctx context.Context, id openlcb.NodeID) error {
	if _, err := s.db.ExecContext(ctx, `DELETE FROM node_values WHERE node_id = ?`, id.String()); err != nil {
		return fmt.Errorf("delete values of %v: %w", id, err)
	}
	return nil
}
