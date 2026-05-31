package client

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strconv"
)

// ArrowResponse wraps the raw Arrow IPC stream from /api/v1/query/arrow.
//
// Wire contract (verified against arc/internal/api/query_arrow.go):
//   - On error: HTTP non-2xx + JSON `{"success": false, "error": "..."}`.
//   - On success: HTTP 200 + `Content-Type: application/vnd.apache.arrow.stream`,
//     body is a streaming Arrow IPC payload. Server-side execution time
//     is emitted as the `Arc-Execution-Time-Ms` HTTP trailer, available
//     only after the body has been read to EOF.
//
// The caller is responsible for Close()-ing this response (which closes
// the underlying HTTP body); call ExecutionTimeMs() only after reading
// Body to EOF.
type ArrowResponse struct {
	// Body is the Arrow IPC stream. Caller reads/copies as needed.
	Body io.ReadCloser

	// resp is held so we can read trailers after the body's drained.
	resp *http.Response
}

// QueryArrow runs a SQL query and returns the response wrapping the
// Arrow IPC stream. The caller MUST Close() the returned response.
//
// On a 4xx/5xx response, the error is decoded from the JSON body
// (same shape as QueryJSON) and the response body is fully consumed
// + closed before the function returns — callers don't need to clean
// up on the error path.
func (c *Client) QueryArrow(ctx context.Context, sql, database string) (*ArrowResponse, error) {
	body, err := json.Marshal(queryRequest{SQL: sql})
	if err != nil {
		return nil, fmt.Errorf("encode query: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.cfg.Endpoint+"/api/v1/query/arrow", bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("build request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/vnd.apache.arrow.stream")
	c.setCommonHeaders(req, database)

	resp, err := c.http.Do(req)
	if err != nil {
		return nil, fmt.Errorf("arrow query: %w", err)
	}

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		// Drain + close the error body ourselves so the caller's
		// error-path doesn't need a defer.
		respBody, _ := io.ReadAll(io.LimitReader(resp.Body, 64<<10))
		_ = resp.Body.Close()
		return nil, decodeServerError(resp.StatusCode, respBody)
	}

	return &ArrowResponse{
		Body: resp.Body,
		resp: resp,
	}, nil
}

// Close closes the underlying HTTP body. nil-safe (returns nil if the
// response or its Body is nil). Callers should defer this once and not
// double-close; while net/http's bodyEOFSignal happens to be idempotent
// today, the io.ReadCloser contract does not require it.
func (a *ArrowResponse) Close() error {
	if a == nil || a.Body == nil {
		return nil
	}
	return a.Body.Close()
}

// ExecutionTimeMs returns the server's execution time from the
// `Arc-Execution-Time-Ms` HTTP trailer. Only valid after Body has been
// read to EOF — earlier calls return (0, false). Calling before EOF is
// not an error, just a missed read.
func (a *ArrowResponse) ExecutionTimeMs() (int64, bool) {
	if a == nil || a.resp == nil {
		return 0, false
	}
	v := a.resp.Trailer.Get(ArrowExecutionTimeTrailer)
	if v == "" {
		return 0, false
	}
	n, err := strconv.ParseInt(v, 10, 64)
	if err != nil {
		return 0, false
	}
	return n, true
}
