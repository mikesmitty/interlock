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

// NodeRecord is what the store keeps about a node between runs.
type NodeRecord struct {
	ID        openlcb.NodeID
	FirstSeen time.Time
	LastSeen  time.Time
	// SNIP is nil for a node that was never identified.
	SNIP      *openlcb.SNIP
	Protocols openlcb.Protocols
}

// CDIRecord is a node's cached CDI.
type CDIRecord struct {
	NodeID          openlcb.NodeID
	SoftwareVersion string
	XML             []byte
	FetchedAt       time.Time
}

// Nodes returns every stored node.
func (s *Store) Nodes(ctx context.Context) ([]NodeRecord, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT id, first_seen, last_seen, snip, protocols FROM nodes`)
	if err != nil {
		return nil, fmt.Errorf("list nodes: %w", err)
	}
	defer rows.Close()
	var out []NodeRecord
	for rows.Next() {
		var (
			rec         NodeRecord
			id          string
			first, last string
			snip        sql.NullString
			protocols   int64
		)
		if err := rows.Scan(&id, &first, &last, &snip, &protocols); err != nil {
			return nil, fmt.Errorf("read node: %w", err)
		}
		rec.ID, err = openlcb.ParseNodeID(id)
		if err != nil {
			return nil, fmt.Errorf("stored node %q: %w", id, err)
		}
		rec.FirstSeen = parseTime(first)
		rec.LastSeen = parseTime(last)
		rec.Protocols = openlcb.Protocols(protocols)
		if snip.Valid && snip.String != "" {
			var sn openlcb.SNIP
			if err := json.Unmarshal([]byte(snip.String), &sn); err != nil {
				return nil, fmt.Errorf("stored node %s SNIP: %w", id, err)
			}
			rec.SNIP = &sn
		}
		out = append(out, rec)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("list nodes: %w", err)
	}
	return out, nil
}

// SaveNode inserts or updates a node.
func (s *Store) SaveNode(ctx context.Context, rec NodeRecord) error {
	var snip any
	if rec.SNIP != nil {
		b, err := json.Marshal(rec.SNIP)
		if err != nil {
			return fmt.Errorf("save node %v: %w", rec.ID, err)
		}
		snip = string(b)
	}
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO nodes (id, first_seen, last_seen, snip, protocols) VALUES (?, ?, ?, ?, ?)
		ON CONFLICT(id) DO UPDATE SET
			first_seen = MIN(first_seen, excluded.first_seen),
			last_seen = MAX(last_seen, excluded.last_seen),
			snip = COALESCE(excluded.snip, snip),
			protocols = CASE WHEN excluded.protocols != 0 THEN excluded.protocols ELSE protocols END`,
		rec.ID.String(), formatTime(rec.FirstSeen), formatTime(rec.LastSeen), snip, int64(rec.Protocols))
	if err != nil {
		return fmt.Errorf("save node %v: %w", rec.ID, err)
	}
	return nil
}

// DeleteNode forgets a node and its cached CDI.
func (s *Store) DeleteNode(ctx context.Context, id openlcb.NodeID) error {
	if _, err := s.db.ExecContext(ctx, `DELETE FROM nodes WHERE id = ?`, id.String()); err != nil {
		return fmt.Errorf("delete node %v: %w", id, err)
	}
	return nil
}

// CDI returns a node's cached CDI, or nil if there is none.
func (s *Store) CDI(ctx context.Context, id openlcb.NodeID) (*CDIRecord, error) {
	rec := &CDIRecord{NodeID: id}
	var fetched string
	err := s.db.QueryRowContext(ctx,
		`SELECT software_version, xml, fetched_at FROM node_cdi WHERE node_id = ?`, id.String()).
		Scan(&rec.SoftwareVersion, &rec.XML, &fetched)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read CDI of %v: %w", id, err)
	}
	rec.FetchedAt = parseTime(fetched)
	return rec, nil
}

// SaveCDI stores a node's CDI, replacing any previous one. The node must
// already be saved.
func (s *Store) SaveCDI(ctx context.Context, rec CDIRecord) error {
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO node_cdi (node_id, software_version, xml, fetched_at) VALUES (?, ?, ?, ?)
		ON CONFLICT(node_id) DO UPDATE SET
			software_version = excluded.software_version, xml = excluded.xml, fetched_at = excluded.fetched_at`,
		rec.NodeID.String(), rec.SoftwareVersion, rec.XML, formatTime(rec.FetchedAt))
	if err != nil {
		return fmt.Errorf("save CDI of %v: %w", rec.NodeID, err)
	}
	return nil
}

// DeleteCDI drops a node's cached CDI.
func (s *Store) DeleteCDI(ctx context.Context, id openlcb.NodeID) error {
	if _, err := s.db.ExecContext(ctx, `DELETE FROM node_cdi WHERE node_id = ?`, id.String()); err != nil {
		return fmt.Errorf("delete CDI of %v: %w", id, err)
	}
	return nil
}

func formatTime(t time.Time) string {
	if t.IsZero() {
		t = time.Now()
	}
	return t.UTC().Format(time.RFC3339Nano)
}

func parseTime(s string) time.Time {
	t, err := time.Parse(time.RFC3339Nano, s)
	if err != nil {
		return time.Time{}
	}
	return t
}
