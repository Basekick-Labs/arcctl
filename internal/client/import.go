package client

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
)

// ImportResult is the server's response shape for CSV + Parquet imports
// (POST /api/v1/import/{csv,parquet}). Mirrors arc/internal/api/import.go's
// ImportResult struct.
type ImportResult struct {
	Database          string   `json:"database"`
	Measurement       string   `json:"measurement"`
	RowsImported      int64    `json:"rows_imported"`
	PartitionsCreated int      `json:"partitions_created"`
	TimeRangeMin      string   `json:"time_range_min,omitempty"`
	TimeRangeMax      string   `json:"time_range_max,omitempty"`
	Columns           []string `json:"columns"`
	DurationMs        int64    `json:"duration_ms"`
}

// LPImportResult is the server's response shape for Line Protocol imports
// (POST /api/v1/import/lp). LP files self-declare their measurements via
// line syntax, so the result reports one or more measurements rather than
// a single one.
type LPImportResult struct {
	Database     string   `json:"database"`
	Measurements []string `json:"measurements"`
	RowsImported int64    `json:"rows_imported"`
	Precision    string   `json:"precision"`
	DurationMs   int64    `json:"duration_ms"`
}

// TLEImportResult is the server's response shape for TLE (two-line element)
// satellite-data imports (POST /api/v1/import/tle).
type TLEImportResult struct {
	Database       string   `json:"database"`
	Measurement    string   `json:"measurement"`
	SatelliteCount int      `json:"satellite_count"`
	RowsImported   int64    `json:"rows_imported"`
	ParseWarnings  []string `json:"parse_warnings,omitempty"`
	DurationMs     int64    `json:"duration_ms"`
}

// importEnvelope is the outer { "status": "ok", "result": {...} } wrapper
// the server uses for every successful import response. We unmarshal the
// inner `result` into the format-specific struct.
type importEnvelope struct {
	Status string          `json:"status"`
	Result json.RawMessage `json:"result"`
}

// CSVImportOptions are the optional knobs accepted by the CSV import
// endpoint as query parameters. Zero-valued fields are not sent (the
// server applies its defaults).
type CSVImportOptions struct {
	// TimeColumn names the column to use as the row timestamp.
	// Server default: "time".
	TimeColumn string

	// TimeFormat tells the server how to parse TimeColumn values.
	// Valid: "", "epoch_s", "epoch_ms", "epoch_us", "epoch_ns".
	// Empty means "let DuckDB infer" (works for ISO-8601 strings).
	TimeFormat string

	// Delimiter is the field separator. Server default: ",".
	Delimiter string

	// SkipRows is the count of header rows to skip before parsing.
	// Server default: 0.
	SkipRows int
}

// ParquetImportOptions mirrors the (much smaller) Parquet option set.
type ParquetImportOptions struct {
	// TimeColumn names the column to use as the row timestamp.
	// Server default: "time".
	TimeColumn string
}

// LPImportOptions configures a Line Protocol import.
type LPImportOptions struct {
	// Precision is one of "ns", "us", "ms", "s". Empty = server default ("ns").
	Precision Precision

	// MeasurementFilter, when non-empty, drops any LP line whose
	// measurement doesn't match. Server returns 400 if no lines match.
	MeasurementFilter string
}

// TLEImportOptions configures a TLE import.
type TLEImportOptions struct {
	// Measurement overrides the default ("satellite_tle"). Sent via the
	// quirky x-arc-measurement header rather than a query param.
	Measurement string
}

// ImportCSV uploads a CSV file via POST /api/v1/import/csv. The file is
// streamed end-to-end (multipart writer wraps an os.File reader), so
// large files do not buffer in memory.
//
// Server requires the `measurement` query param (not derivable from the
// file). Server-side admin auth required.
func (c *Client) ImportCSV(ctx context.Context, filePath, database, measurement string, opts CSVImportOptions) (*ImportResult, error) {
	// Explicitly reject negative SkipRows. Previously this branch was
	// `opts.SkipRows > 0` which silently dropped negative values — the
	// cobra layer rejects them at RunE today, but any future caller
	// that bypasses the cobra path (a programmatic test helper, a
	// follow-up command) would re-introduce the silent-drop bug.
	// Surfacing it here makes the contract explicit at the client
	// boundary. (Gemini PR #3 round 4.)
	if opts.SkipRows < 0 {
		return nil, fmt.Errorf("CSVImportOptions.SkipRows must be >= 0 (got %d)", opts.SkipRows)
	}

	q := url.Values{}
	q.Set("measurement", measurement)
	if opts.TimeColumn != "" {
		q.Set("time_column", opts.TimeColumn)
	}
	if opts.TimeFormat != "" {
		q.Set("time_format", opts.TimeFormat)
	}
	if opts.Delimiter != "" {
		q.Set("delimiter", opts.Delimiter)
	}
	if opts.SkipRows > 0 {
		q.Set("skip_rows", fmt.Sprintf("%d", opts.SkipRows))
	}

	envelope, err := c.uploadMultipart(ctx, "/api/v1/import/csv", filePath, database, q, nil)
	if err != nil {
		return nil, err
	}
	var out ImportResult
	if err := json.Unmarshal(envelope.Result, &out); err != nil {
		return nil, fmt.Errorf("decode csv import result: %w", err)
	}
	return &out, nil
}

// ImportParquet uploads a Parquet file via POST /api/v1/import/parquet.
// Measurement is required (Parquet files don't carry a measurement name).
func (c *Client) ImportParquet(ctx context.Context, filePath, database, measurement string, opts ParquetImportOptions) (*ImportResult, error) {
	q := url.Values{}
	q.Set("measurement", measurement)
	if opts.TimeColumn != "" {
		q.Set("time_column", opts.TimeColumn)
	}

	envelope, err := c.uploadMultipart(ctx, "/api/v1/import/parquet", filePath, database, q, nil)
	if err != nil {
		return nil, err
	}
	var out ImportResult
	if err := json.Unmarshal(envelope.Result, &out); err != nil {
		return nil, fmt.Errorf("decode parquet import result: %w", err)
	}
	return &out, nil
}

// ImportLP uploads a Line Protocol file via POST /api/v1/import/lp.
// Measurement is OPTIONAL (LP lines self-declare); when set it acts as a
// server-side filter. Server auto-detects gzip via magic bytes; clients
// can pass either compressed or plain LP and the server figures it out.
func (c *Client) ImportLP(ctx context.Context, filePath, database string, opts LPImportOptions) (*LPImportResult, error) {
	// ValidPrecision("") returns true (empty == "let server apply default"),
	// so the bare check is sufficient — no need for an outer != "" guard.
	if !ValidPrecision(string(opts.Precision)) {
		return nil, fmt.Errorf("invalid precision %q (must be one of ns, us, ms, s)", opts.Precision)
	}

	q := url.Values{}
	if opts.Precision != "" {
		q.Set("precision", string(opts.Precision))
	}
	if opts.MeasurementFilter != "" {
		q.Set("measurement", opts.MeasurementFilter)
	}

	envelope, err := c.uploadMultipart(ctx, "/api/v1/import/lp", filePath, database, q, nil)
	if err != nil {
		return nil, err
	}
	var out LPImportResult
	if err := json.Unmarshal(envelope.Result, &out); err != nil {
		return nil, fmt.Errorf("decode lp import result: %w", err)
	}
	return &out, nil
}

// ImportTLE uploads a TLE (two-line element / satellite tracking) file
// via POST /api/v1/import/tle. The measurement override is sent via
// header `x-arc-measurement` rather than a query param — quirky server
// design, mirrored faithfully here so the server's defaulting behavior
// works correctly.
func (c *Client) ImportTLE(ctx context.Context, filePath, database string, opts TLEImportOptions) (*TLEImportResult, error) {
	var extraHeaders map[string]string
	if opts.Measurement != "" {
		extraHeaders = map[string]string{"x-arc-measurement": opts.Measurement}
	}

	envelope, err := c.uploadMultipart(ctx, "/api/v1/import/tle", filePath, database, nil, extraHeaders)
	if err != nil {
		return nil, err
	}
	var out TLEImportResult
	if err := json.Unmarshal(envelope.Result, &out); err != nil {
		return nil, fmt.Errorf("decode tle import result: %w", err)
	}
	return &out, nil
}

// uploadMultipart is the shared multipart-upload primitive used by every
// import command. Builds a multipart body with one `file` field that
// streams from disk, sets the database header + any per-call extras,
// posts to the given path, and returns the decoded envelope.
//
// The multipart body is built via io.Pipe so the file is NEVER fully
// buffered in memory — even a 500MB CSV streams chunk-by-chunk over the
// wire. The pipe writer runs in a goroutine so the HTTP client's reader
// drives the upload rate.
func (c *Client) uploadMultipart(
	ctx context.Context,
	path string,
	filePath string,
	database string,
	query url.Values,
	extraHeaders map[string]string,
) (*importEnvelope, error) {
	if database == "" {
		return nil, fmt.Errorf("database is required")
	}

	// Open file up front so a missing-file error surfaces immediately
	// (before we set up the goroutine + HTTP request). os.Open
	// already includes the path in its PathError, so we wrap without
	// duplicating it.
	f, err := os.Open(filePath)
	if err != nil {
		return nil, err
	}
	// f is closed inside the goroutine after the multipart writer
	// finishes; we MUST NOT defer Close here or we'd race with the
	// goroutine's final read.

	// Compose URL with optional query params.
	u := c.cfg.Endpoint + path
	if len(query) > 0 {
		u += "?" + query.Encode()
	}

	// io.Pipe to stream the multipart body without buffering.
	pr, pw := io.Pipe()
	mw := multipart.NewWriter(pw)

	// Guarantee pr is closed when this function exits, regardless of
	// path. Without this, an early failure in http.NewRequestWithContext
	// or http.Do (DNS failure, TLS handshake failure, ctx cancelled
	// before send) would leave pr open — the writer goroutine then
	// blocks on pw.Write forever, leaking the goroutine and the
	// underlying file handle. io.PipeReader.Close is idempotent per
	// Go's documentation, so a redundant close on the happy path
	// (where the transport already closed pr after reading the body
	// to EOF) is harmless. (Gemini PR #3 round 3 finding — High.)
	defer pr.Close()

	go func() {
		// Inside the goroutine we own both `f` and `mw` and are
		// responsible for closing the pipe on every path so the reader
		// side (HTTP request body) eventually sees EOF or a real error
		// rather than blocking forever.
		defer f.Close()

		part, err := mw.CreateFormFile("file", filepath.Base(filePath))
		if err != nil {
			// Don't call mw.Close() on error paths — it would write
			// the trailing boundary into a stream whose preceding
			// content is already known-bad, producing a multipart
			// payload that's syntactically valid but semantically
			// truncated. Just signal the error on the pipe and let
			// the reader surface it. (Gemini PR #3 finding.)
			_ = pw.CloseWithError(fmt.Errorf("create form file: %w", err))
			return
		}
		if _, err := io.Copy(part, f); err != nil {
			_ = pw.CloseWithError(fmt.Errorf("stream file body: %w", err))
			return
		}
		// Happy path: close the multipart writer so the trailing
		// boundary is written, then close the pipe cleanly so the
		// reader sees EOF.
		if err := mw.Close(); err != nil {
			_ = pw.CloseWithError(fmt.Errorf("close multipart writer: %w", err))
			return
		}
		_ = pw.Close()
	}()

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, u, pr)
	if err != nil {
		return nil, fmt.Errorf("build import request: %w", err)
	}

	// Database goes via x-arc-database header for consistency with
	// query/write (the server also accepts ?db= as a fallback).
	c.setCommonHeaders(req, database)
	for k, v := range extraHeaders {
		// Refuse to clobber the multipart Content-Type. Setting that
		// header from extraHeaders would silently lose the boundary
		// and break every upload in confusing ways; force callers to
		// route Content-Type through the canonical path instead.
		if http.CanonicalHeaderKey(k) == "Content-Type" {
			continue
		}
		req.Header.Set(k, v)
	}
	// Set Content-Type LAST so neither setCommonHeaders nor the
	// extraHeaders loop can accidentally overwrite the boundary
	// parameter computed by mw.FormDataContentType().
	req.Header.Set("Content-Type", mw.FormDataContentType())

	resp, err := c.http.Do(req)
	if err != nil {
		return nil, fmt.Errorf("import upload: %w", err)
	}
	defer resp.Body.Close()

	// Import responses include a small JSON result; cap the read at
	// 16 MiB to defend against a misbehaving proxy returning HTML.
	body, err := io.ReadAll(io.LimitReader(resp.Body, 16<<20))
	if err != nil {
		return nil, fmt.Errorf("read import response: %w", err)
	}

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, decodeWriteError(resp.StatusCode, body)
	}

	var env importEnvelope
	if err := json.Unmarshal(body, &env); err != nil {
		return nil, fmt.Errorf("decode import envelope: %w", err)
	}
	return &env, nil
}
