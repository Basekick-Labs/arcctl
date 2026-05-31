package client

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
)

// Precision is the timestamp precision sent on a line-protocol write
// via the `?precision=` query param. Empty maps to nanoseconds (Arc's
// default).
type Precision string

const (
	PrecisionNS Precision = "ns"
	PrecisionUS Precision = "us"
	PrecisionMS Precision = "ms"
	PrecisionS  Precision = "s"
)

// ValidPrecision reports whether s is one of the four precisions Arc
// accepts. Empty string is treated as valid (server applies its default).
func ValidPrecision(s string) bool {
	switch Precision(s) {
	case "", PrecisionNS, PrecisionUS, PrecisionMS, PrecisionS:
		return true
	}
	return false
}

// WriteLineProtocol POSTs raw line-protocol bytes to /api/v1/write/line-protocol.
//
// The body io.Reader is streamed — we never buffer it fully. Callers
// passing an os.File or a stdin pipe get true streaming behaviour.
//
// `precision` may be empty (Arc applies nanosecond default).
// `database` overrides the client's default database.
//
// Returns nil on success (HTTP 204 No Content) and a decoded server
// error on any non-2xx response.
func (c *Client) WriteLineProtocol(ctx context.Context, body io.Reader, database string, precision Precision) error {
	if !ValidPrecision(string(precision)) {
		return fmt.Errorf("invalid precision %q (must be one of ns, us, ms, s)", precision)
	}

	u, err := url.Parse(c.cfg.Endpoint + "/api/v1/write/line-protocol")
	if err != nil {
		return fmt.Errorf("build write URL: %w", err)
	}
	if precision != "" {
		q := u.Query()
		q.Set("precision", string(precision))
		u.RawQuery = q.Encode()
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, u.String(), body)
	if err != nil {
		return fmt.Errorf("build write request: %w", err)
	}
	req.Header.Set("Content-Type", "text/plain")
	c.setCommonHeaders(req, database)

	resp, err := c.http.Do(req)
	if err != nil {
		return fmt.Errorf("write: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 200 && resp.StatusCode < 300 {
		// Drain any small body Arc emits (currently 204 with empty
		// body, but defensive against future changes).
		_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 4<<10))
		return nil
	}

	respBody, _ := io.ReadAll(io.LimitReader(resp.Body, 64<<10))
	return decodeWriteError(resp.StatusCode, respBody)
}

// decodeWriteError handles the write endpoint's error shape, which
// differs from query: `{"error": "..."}` with no `success` field.
// Falls back to truncated raw body for non-JSON responses (proxies).
func decodeWriteError(status int, body []byte) error {
	var er struct {
		Error string `json:"error"`
	}
	if err := json.Unmarshal(body, &er); err == nil && er.Error != "" {
		return fmt.Errorf("arc: %s (HTTP %d)", er.Error, status)
	}
	const maxRawLen = 512
	raw := string(body)
	if len(raw) > maxRawLen {
		raw = raw[:maxRawLen] + "...[truncated]"
	}
	return fmt.Errorf("arc: HTTP %d: %s", status, raw)
}
