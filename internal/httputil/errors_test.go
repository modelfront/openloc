package httputil

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/jackc/pgx/v5"
)

func TestDBError_Nil(t *testing.T) {
	w := httptest.NewRecorder()
	if DBError(w, nil, "project") {
		t.Fatal("expected false for nil error")
	}
	if w.Code != http.StatusOK { // default, nothing written
		t.Errorf("expected default 200, got %d", w.Code)
	}
}

func TestDBError_NoRows(t *testing.T) {
	w := httptest.NewRecorder()
	if !DBError(w, pgx.ErrNoRows, "project") {
		t.Fatal("expected true for ErrNoRows")
	}
	if w.Code != http.StatusNotFound {
		t.Errorf("expected 404, got %d", w.Code)
	}
	var resp map[string]any
	json.NewDecoder(w.Body).Decode(&resp)
	errObj := resp["error"].(map[string]any)
	if errObj["message"] != "project not found" {
		t.Errorf("expected 'project not found', got %q", errObj["message"])
	}
}

func TestDBError_OtherError(t *testing.T) {
	w := httptest.NewRecorder()
	if !DBError(w, errors.New("connection reset"), "TM") {
		t.Fatal("expected true for non-nil error")
	}
	if w.Code != http.StatusInternalServerError {
		t.Errorf("expected 500, got %d", w.Code)
	}
	var resp map[string]any
	json.NewDecoder(w.Body).Decode(&resp)
	errObj := resp["error"].(map[string]any)
	if errObj["message"] != "failed to get TM" {
		t.Errorf("expected 'failed to get TM', got %q", errObj["message"])
	}
}

func TestBadRequest(t *testing.T) {
	w := httptest.NewRecorder()
	BadRequest(w, "name is required")
	if w.Code != http.StatusBadRequest {
		t.Errorf("expected 400, got %d", w.Code)
	}
}

func TestNotFound(t *testing.T) {
	w := httptest.NewRecorder()
	NotFound(w, "job")
	if w.Code != http.StatusNotFound {
		t.Errorf("expected 404, got %d", w.Code)
	}
	var resp map[string]any
	json.NewDecoder(w.Body).Decode(&resp)
	if resp["error"].(map[string]any)["message"] != "job not found" {
		t.Errorf("unexpected message")
	}
}

func TestServerError(t *testing.T) {
	w := httptest.NewRecorder()
	ServerError(w, "create", "template")
	if w.Code != http.StatusInternalServerError {
		t.Errorf("expected 500, got %d", w.Code)
	}
	var resp map[string]any
	json.NewDecoder(w.Body).Decode(&resp)
	if resp["error"].(map[string]any)["message"] != "failed to create template" {
		t.Errorf("unexpected message")
	}
}
