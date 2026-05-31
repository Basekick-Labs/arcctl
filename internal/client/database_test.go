package client

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestListDatabases_Success(t *testing.T) {
	var gotPath, gotMethod, gotAuth string
	gotDB := "sentinel" // sentinel: should be overwritten to "" since this op is cross-db
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath, gotMethod, gotAuth = r.URL.Path, r.Method, r.Header.Get("Authorization")
		gotDB = r.Header.Get("x-arc-database")
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"databases":[{"name":"metrics","measurement_count":3,"created_at":"2026-05-31T12:00:00Z"},{"name":"logs","measurement_count":1}],"count":2}`)
	}))
	defer srv.Close()

	c := freshClient(t, srv, "metrics") // client default DB is "metrics"
	list, err := c.ListDatabases(context.Background())
	if err != nil {
		t.Fatalf("ListDatabases: %v", err)
	}
	if gotPath != "/api/v1/databases" {
		t.Errorf("path = %q", gotPath)
	}
	if gotMethod != "GET" {
		t.Errorf("method = %q", gotMethod)
	}
	if gotAuth != "Bearer test-token" {
		t.Errorf("auth = %q", gotAuth)
	}
	// Cross-db op: x-arc-database header should NOT be sent even though
	// the client has a default.
	if gotDB != "" {
		t.Errorf("x-arc-database = %q; expected empty for cross-db list", gotDB)
	}
	if list.Count != 2 || len(list.Databases) != 2 {
		t.Errorf("decoded %+v", list)
	}
	if list.Databases[0].Name != "metrics" || list.Databases[0].MeasurementCount != 3 {
		t.Errorf("first db = %+v", list.Databases[0])
	}
	if list.Databases[1].CreatedAt != "" {
		t.Errorf("second db should have omitempty CreatedAt, got %q", list.Databases[1].CreatedAt)
	}
}

func TestGetDatabase_Success(t *testing.T) {
	var gotPath string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		_, _ = io.WriteString(w, `{"name":"metrics","measurement_count":7}`)
	}))
	defer srv.Close()

	c := freshClient(t, srv, "")
	info, err := c.GetDatabase(context.Background(), "metrics")
	if err != nil {
		t.Fatalf("GetDatabase: %v", err)
	}
	if gotPath != "/api/v1/databases/metrics" {
		t.Errorf("path = %q", gotPath)
	}
	if info.Name != "metrics" || info.MeasurementCount != 7 {
		t.Errorf("info = %+v", info)
	}
}

func TestGetDatabase_NotFound(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
		_, _ = io.WriteString(w, `{"error":"Database 'nope' not found"}`)
	}))
	defer srv.Close()

	c := freshClient(t, srv, "")
	_, err := c.GetDatabase(context.Background(), "nope")
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if !strings.Contains(err.Error(), "not found") {
		t.Errorf("error %q lacks server message", err)
	}
	if !strings.Contains(err.Error(), "HTTP 404") {
		t.Errorf("error %q lacks status", err)
	}
}

func TestGetDatabase_EscapesName(t *testing.T) {
	// Name with characters that require URL escaping. Server should
	// receive the encoded form in the path.
	var gotPath string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.EscapedPath()
		_, _ = io.WriteString(w, `{"name":"weird/name","measurement_count":0}`)
	}))
	defer srv.Close()

	c := freshClient(t, srv, "")
	if _, err := c.GetDatabase(context.Background(), "weird/name"); err != nil {
		t.Fatalf("GetDatabase: %v", err)
	}
	if gotPath != "/api/v1/databases/weird%2Fname" {
		t.Errorf("path = %q; expected /api/v1/databases/weird%%2Fname", gotPath)
	}
}

func TestGetDatabase_EmptyName(t *testing.T) {
	// Defensive client-side check: don't even send the request.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Fatal("server should not be called for empty name")
	}))
	defer srv.Close()
	c := freshClient(t, srv, "")
	if _, err := c.GetDatabase(context.Background(), ""); err == nil {
		t.Fatal("expected error, got nil")
	}
}

func TestCreateDatabase_Success(t *testing.T) {
	var (
		gotPath   string
		gotMethod string
		gotCT     string
		gotBody   []byte
	)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath, gotMethod = r.URL.Path, r.Method
		gotCT = r.Header.Get("Content-Type")
		gotBody, _ = io.ReadAll(r.Body)
		w.WriteHeader(http.StatusCreated)
		_, _ = io.WriteString(w, `{"name":"new_db","measurement_count":0,"created_at":"2026-05-31T12:00:00Z"}`)
	}))
	defer srv.Close()

	c := freshClient(t, srv, "")
	info, err := c.CreateDatabase(context.Background(), "new_db")
	if err != nil {
		t.Fatalf("CreateDatabase: %v", err)
	}
	if gotMethod != "POST" || gotPath != "/api/v1/databases" {
		t.Errorf("path/method = %q / %q", gotPath, gotMethod)
	}
	if gotCT != "application/json" {
		t.Errorf("content-type = %q", gotCT)
	}
	var sentReq createDatabaseRequest
	if err := json.Unmarshal(gotBody, &sentReq); err != nil {
		t.Fatalf("body not JSON: %v", err)
	}
	if sentReq.Name != "new_db" {
		t.Errorf("sent name = %q", sentReq.Name)
	}
	if info.Name != "new_db" || info.CreatedAt == "" {
		t.Errorf("decoded info = %+v", info)
	}
}

func TestCreateDatabase_Conflict(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusConflict)
		_, _ = io.WriteString(w, `{"error":"Database 'metrics' already exists"}`)
	}))
	defer srv.Close()

	c := freshClient(t, srv, "")
	_, err := c.CreateDatabase(context.Background(), "metrics")
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if !strings.Contains(err.Error(), "already exists") {
		t.Errorf("error %q lacks server message", err)
	}
}

func TestCreateDatabase_ReservedName(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
		_, _ = io.WriteString(w, `{"error":"Database name 'system' is reserved"}`)
	}))
	defer srv.Close()

	c := freshClient(t, srv, "")
	_, err := c.CreateDatabase(context.Background(), "system")
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if !strings.Contains(err.Error(), "reserved") {
		t.Errorf("error %q lacks server message", err)
	}
}

func TestDeleteDatabase_Success(t *testing.T) {
	var (
		gotPath   string
		gotMethod string
		gotQuery  string
	)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath, gotMethod = r.URL.Path, r.Method
		gotQuery = r.URL.RawQuery
		w.WriteHeader(http.StatusNoContent)
	}))
	defer srv.Close()

	c := freshClient(t, srv, "")
	if err := c.DeleteDatabase(context.Background(), "old_db"); err != nil {
		t.Fatalf("DeleteDatabase: %v", err)
	}
	if gotMethod != "DELETE" {
		t.Errorf("method = %q", gotMethod)
	}
	if gotPath != "/api/v1/databases/old_db" {
		t.Errorf("path = %q", gotPath)
	}
	// confirm=true is required by the server; client always sends it.
	if gotQuery != "confirm=true" {
		t.Errorf("query = %q; expected confirm=true", gotQuery)
	}
}

func TestDeleteDatabase_DeleteDisabled(t *testing.T) {
	// Server sends 403 with the explicit "enable in arc.toml" message
	// when delete is gated. We surface the message verbatim so the
	// operator knows what to do.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusForbidden)
		_, _ = io.WriteString(w, `{"error":"Delete operations are disabled. Set delete.enabled=true in arc.toml to enable."}`)
	}))
	defer srv.Close()

	c := freshClient(t, srv, "")
	err := c.DeleteDatabase(context.Background(), "x")
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if !strings.Contains(err.Error(), "delete.enabled=true") {
		t.Errorf("error %q lacks the server's enable hint", err)
	}
}

func TestDeleteDatabase_NotAdmin(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusForbidden)
		_, _ = io.WriteString(w, `{"error":"Admin permission required"}`)
	}))
	defer srv.Close()

	c := freshClient(t, srv, "")
	err := c.DeleteDatabase(context.Background(), "x")
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if !strings.Contains(err.Error(), "Admin permission required") {
		t.Errorf("error %q lacks server message", err)
	}
}

func TestListMeasurements_Success(t *testing.T) {
	var gotPath string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		_, _ = io.WriteString(w, `{"database":"metrics","measurements":[{"name":"cpu","file_count":12},{"name":"mem"}],"count":2}`)
	}))
	defer srv.Close()

	c := freshClient(t, srv, "")
	list, err := c.ListMeasurements(context.Background(), "metrics")
	if err != nil {
		t.Fatalf("ListMeasurements: %v", err)
	}
	if gotPath != "/api/v1/databases/metrics/measurements" {
		t.Errorf("path = %q", gotPath)
	}
	if list.Database != "metrics" || list.Count != 2 || len(list.Measurements) != 2 {
		t.Errorf("decoded %+v", list)
	}
	if list.Measurements[0].FileCount != 12 || list.Measurements[1].FileCount != 0 {
		t.Errorf("file counts off: %+v", list.Measurements)
	}
}

func TestListMeasurements_DatabaseNotFound(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
		_, _ = io.WriteString(w, `{"error":"Database 'nope' not found"}`)
	}))
	defer srv.Close()

	c := freshClient(t, srv, "")
	_, err := c.ListMeasurements(context.Background(), "nope")
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if !strings.Contains(err.Error(), "not found") {
		t.Errorf("error %q lacks server message", err)
	}
}

func TestDefaultDatabase(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	defer srv.Close()
	c := freshClient(t, srv, "metrics")
	if c.DefaultDatabase() != "metrics" {
		t.Errorf("DefaultDatabase() = %q, want metrics", c.DefaultDatabase())
	}
}
