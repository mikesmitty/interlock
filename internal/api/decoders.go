package api

import (
	"compress/gzip"
	"encoding/json"
	"net/http"
	"strings"

	"github.com/mikesmitty/interlock/internal/decoder"
)

// Decoders reads the DCC decoder definitions.
type Decoders interface {
	Status() decoder.Status
	Index() (*decoder.Index, error)
}

// The decoder definition endpoints:
//
//	GET /api/v1/decoders         where the definitions come from
//	GET /api/v1/decoders/index   every manufacturer and decoder family

func (a *API) decoderStatus(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, a.deps.Decoders.Status())
}

func (a *API) decoderIndex(w http.ResponseWriter, r *http.Request) {
	idx, err := a.deps.Decoders.Index()
	if err != nil {
		writeError(w, http.StatusBadGateway, "The decoder definitions could not be read: "+err.Error())
		return
	}
	writeJSONCompressed(w, r, idx)
}

// writeJSONCompressed writes a large body gzipped when the client takes
// it; the decoder index is over half a megabyte as JSON and a tenth of
// that compressed.
func writeJSONCompressed(w http.ResponseWriter, r *http.Request, body any) {
	if !strings.Contains(r.Header.Get("Accept-Encoding"), "gzip") {
		writeJSON(w, http.StatusOK, body)
		return
	}
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Content-Encoding", "gzip")
	w.Header().Add("Vary", "Accept-Encoding")
	w.WriteHeader(http.StatusOK)
	gz := gzip.NewWriter(w)
	if err := json.NewEncoder(gz).Encode(body); err != nil {
		return
	}
	_ = gz.Close()
}
