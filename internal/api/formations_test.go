package api

import (
	"net/http"
	"testing"
)

func TestCarAndFormationEndpoints(t *testing.T) {
	ta, _ := newTrainAPI(t)
	if rec, _ := ta.do(t, http.MethodPost, "/api/v1/trains", `{"name": "Motor", "address": 101, "length": 114}`); rec.Code != http.StatusCreated {
		t.Fatalf("POST trains = %d", rec.Code)
	}
	rec, body := ta.do(t, http.MethodPost, "/api/v1/cars", `{"road_name": "NYCT", "road_number": "6402", "length": 114}`)
	if rec.Code != http.StatusCreated || body["id"] != "nyct-6402" || body["name"] != "NYCT 6402" {
		t.Fatalf("POST cars = %d %v", rec.Code, body)
	}
	if rec, _ := ta.do(t, http.MethodPost, "/api/v1/cars", `{"length": -1, "name": "Bad"}`); rec.Code != http.StatusBadRequest {
		t.Errorf("a negative length = %d", rec.Code)
	}
	if rec, body = ta.do(t, http.MethodPatch, "/api/v1/cars/nyct-6402", `{"length": 120}`); rec.Code != http.StatusOK || body["length"] != 120.0 {
		t.Errorf("PATCH car = %d %v", rec.Code, body)
	}

	rec, body = ta.do(t, http.MethodPost, "/api/v1/formations", `{"name": "6 local", "vehicles": [{"locomotive": "motor"}, {"car": "nyct-6402", "reverse": true}]}`)
	if rec.Code != http.StatusCreated || body["id"] != "6-local" || body["length"] != 234.0 {
		t.Fatalf("POST formations = %d %v", rec.Code, body)
	}
	if rec, _ := ta.do(t, http.MethodPost, "/api/v1/formations", `{"name": "Again", "vehicles": [{"car": "nyct-6402"}]}`); rec.Code != http.StatusBadRequest {
		t.Errorf("a car in two trains = %d", rec.Code)
	}
	if rec, body = ta.do(t, http.MethodPatch, "/api/v1/formations/6-local", `{"vehicles": [{"car": "nyct-6402"}, {"locomotive": "motor", "reverse": true}]}`); rec.Code != http.StatusOK {
		t.Errorf("PATCH formation = %d %v", rec.Code, body)
	}
	rec, body = ta.do(t, http.MethodGet, "/api/v1/formations", "")
	if list, _ := body["formations"].([]any); rec.Code != http.StatusOK || len(list) != 1 {
		t.Errorf("GET formations = %d %v", rec.Code, body)
	}
	if rec, _ := ta.do(t, http.MethodDelete, "/api/v1/cars/nyct-6402", ""); rec.Code != http.StatusNoContent {
		t.Errorf("DELETE car = %d", rec.Code)
	}
	if rec, body = ta.do(t, http.MethodGet, "/api/v1/formations/6-local", ""); rec.Code != http.StatusOK || len(body["vehicles"].([]any)) != 1 {
		t.Errorf("after removing the car = %d %v", rec.Code, body)
	}
	if rec, _ := ta.do(t, http.MethodDelete, "/api/v1/formations/6-local", ""); rec.Code != http.StatusNoContent {
		t.Errorf("DELETE formation = %d", rec.Code)
	}
	if rec, _ := ta.do(t, http.MethodGet, "/api/v1/formations/6-local", ""); rec.Code != http.StatusNotFound {
		t.Errorf("GET a deleted formation = %d", rec.Code)
	}
}
