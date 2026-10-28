package layoutsim

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"

	"github.com/mikesmitty/interlock/internal/trackplan"
)

// Client reaches the Interlock the simulated layout is run by, through
// its HTTP API.
type Client struct {
	// Base is Interlock's address, as in http://127.0.0.1:8080.
	Base string
	HTTP *http.Client
}

func (c *Client) do(ctx context.Context, method, path string, body, out any) error {
	var r io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return err
		}
		r = bytes.NewReader(b)
	}
	req, err := http.NewRequestWithContext(ctx, method, strings.TrimSuffix(c.Base, "/")+path, r)
	if err != nil {
		return err
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	hc := c.HTTP
	if hc == nil {
		hc = http.DefaultClient
	}
	resp, err := hc.Do(req)
	if err != nil {
		return err
	}
	defer func() { _ = resp.Body.Close() }()
	data, err := io.ReadAll(io.LimitReader(resp.Body, 16<<20))
	if err != nil {
		return err
	}
	if resp.StatusCode/100 != 2 {
		var e struct {
			Error string `json:"error"`
		}
		if json.Unmarshal(data, &e) == nil && e.Error != "" {
			return fmt.Errorf("%s %s: %s", method, path, e.Error)
		}
		return fmt.Errorf("%s %s: %s", method, path, resp.Status)
	}
	if out == nil {
		return nil
	}
	if err := json.Unmarshal(data, out); err != nil {
		return fmt.Errorf("%s %s: %w", method, path, err)
	}
	return nil
}

// Plan gets the track plan and its revision.
func (c *Client) Plan(ctx context.Context) (trackplan.State, error) {
	var st trackplan.State
	err := c.do(ctx, http.MethodGet, "/api/v1/trackplan", nil, &st)
	return st, err
}

// PutPlan saves a plan over the revision named.
func (c *Client) PutPlan(ctx context.Context, plan trackplan.Plan, revision string) (trackplan.State, error) {
	var st trackplan.State
	err := c.do(ctx, http.MethodPut, "/api/v1/trackplan", map[string]any{"plan": plan, "revision": revision}, &st)
	return st, err
}

// AddToRoster adds the trains the roster has no locomotive for at their
// address, so they can be driven.
func (c *Client) AddToRoster(ctx context.Context, trains []TrainSpec) ([]string, error) {
	var list struct {
		Trains []struct {
			Address uint16 `json:"address"`
			Long    bool   `json:"long"`
			Roster  bool   `json:"roster"`
		} `json:"trains"`
	}
	if err := c.do(ctx, http.MethodGet, "/api/v1/trains", nil, &list); err != nil {
		return nil, err
	}
	var added []string
	for _, spec := range trains {
		found := false
		for _, t := range list.Trains {
			found = found || (t.Roster && t.Address == spec.Address && t.Long == spec.long())
		}
		if found {
			continue
		}
		entry := map[string]any{"name": spec.Name, "address": spec.Address, "long": spec.long()}
		if err := c.do(ctx, http.MethodPost, "/api/v1/trains", entry, nil); err != nil {
			return added, err
		}
		added = append(added, spec.Name)
	}
	return added, nil
}
