// Package identity resolves the LCC node ID Interlock itself runs as.
package identity

import (
	"context"
	"fmt"

	"github.com/mikesmitty/interlock/openlcb"
)

// SettingKey is where the generated node ID is kept in the store.
const SettingKey = "node_id"

// Settings is the part of the store identity needs.
type Settings interface {
	Setting(ctx context.Context, key string) (string, bool, error)
	SetSetting(ctx context.Context, key, value string) error
}

// Resolve returns the node ID to use. A configured ID (dotted hex) wins.
// Otherwise the ID generated on an earlier start is read from settings, and
// if there is none a new locally assigned ID is generated and saved so the
// node keeps the same identity across restarts.
func Resolve(ctx context.Context, configured string, settings Settings) (openlcb.NodeID, error) {
	if configured != "" {
		id, err := openlcb.ParseNodeID(configured)
		if err != nil {
			return 0, fmt.Errorf("configured node ID: %w", err)
		}
		return id, nil
	}

	stored, ok, err := settings.Setting(ctx, SettingKey)
	if err != nil {
		return 0, err
	}
	if ok {
		id, err := openlcb.ParseNodeID(stored)
		if err != nil {
			return 0, fmt.Errorf("stored node ID: %w", err)
		}
		return id, nil
	}

	id, err := openlcb.RandomLocalNodeID()
	if err != nil {
		return 0, err
	}
	if err := settings.SetSetting(ctx, SettingKey, id.String()); err != nil {
		return 0, err
	}
	return id, nil
}
