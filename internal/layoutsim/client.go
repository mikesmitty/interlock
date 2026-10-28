package layoutsim

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strconv"
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

// AddToRoster gives the trains their roster entries: it adds those the
// roster has no locomotive for at their address, fills in a length the
// roster lacks, and fits a locomotive that has no decoder with one whose
// CVs, as cvs gives them, are on record, as if read on the programming
// track. It returns the names of the trains it changed.
func (c *Client) AddToRoster(ctx context.Context, trains []TrainSpec, cvs func(TrainSpec) map[uint16]byte) ([]string, error) {
	type entry struct {
		ID        string  `json:"id"`
		Address   uint16  `json:"address"`
		Long      bool    `json:"long"`
		Roster    bool    `json:"roster"`
		Length    float64 `json:"length"`
		Installed string  `json:"installed"`
	}
	var list struct {
		Trains []entry `json:"trains"`
	}
	if err := c.do(ctx, http.MethodGet, "/api/v1/trains", nil, &list); err != nil {
		return nil, err
	}
	var changed []string
	for _, spec := range trains {
		var e *entry
		for i, t := range list.Trains {
			if t.Roster && t.Address == spec.Address && t.Long == spec.long() {
				e = &list.Trains[i]
				break
			}
		}
		switch {
		case e == nil:
			e = &entry{}
			body := map[string]any{"name": spec.Name, "address": spec.Address, "long": spec.long(), "length": spec.Length, "decoder_at": spec.Decoder}
			if err := c.do(ctx, http.MethodPost, "/api/v1/trains", body, e); err != nil {
				return changed, err
			}
		case e.Length == 0:
			body := map[string]any{"length": spec.Length, "decoder_at": spec.Decoder}
			if err := c.do(ctx, http.MethodPatch, "/api/v1/trains/"+e.ID, body, nil); err != nil {
				return changed, err
			}
		case e.Installed != "":
			continue
		}
		if e.Installed == "" && cvs != nil {
			var added struct {
				Decoder struct {
					ID string `json:"id"`
				} `json:"decoder"`
			}
			if err := c.do(ctx, http.MethodPost, "/api/v1/trains/"+e.ID+"/decoders", map[string]any{"name": "Simulated decoder", "install": true}, &added); err != nil {
				return changed, err
			}
			values := map[string]int{}
			for cv, v := range cvs(spec) {
				values[strconv.Itoa(int(cv))] = int(v)
			}
			if err := c.do(ctx, http.MethodPatch, "/api/v1/trains/"+e.ID+"/decoders/"+added.Decoder.ID+"/cvs", map[string]any{"cvs": values}, nil); err != nil {
				return changed, err
			}
		}
		changed = append(changed, spec.Name)
	}
	return changed, nil
}
