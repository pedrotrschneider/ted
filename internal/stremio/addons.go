package stremio

import (
	"context"
	"encoding/json"
)

// AddonCollection is independent of watched-state snapshots and never writes
// the remote collection. Transport URLs remain private to the server adapter.
func (c *Client) AddonCollection(ctx context.Context, key string) ([]json.RawMessage, error) {
	if len(key) < 16 || len(key) > 4096 {
		return nil, ErrInvalid
	}
	var result struct {
		Addons *[]json.RawMessage `json:"addons"`
	}
	if err := c.post(ctx, "addonCollectionGet", map[string]any{"authKey": key, "update": false}, &result); err != nil {
		return nil, err
	}
	if result.Addons == nil || len(*result.Addons) > 64 {
		return nil, ErrIncomplete
	}
	return *result.Addons, nil
}
