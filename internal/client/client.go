package client

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"time"
)

// Client is an HTTP client for the Orva API.
type Client struct {
	BaseURL string
	APIKey  string
	HTTP    *http.Client
}

// NewClient creates a new Orva API client.
func NewClient(baseURL, apiKey string) *Client {
	return &Client{
		BaseURL: baseURL,
		APIKey:  apiKey,
		HTTP: &http.Client{
			Timeout: 120 * time.Second,
		},
	}
}

// Request is a flexible HTTP request descriptor for callers that need more
// control than the Get/Post/Put/Delete helpers give — custom method, raw
// body, extra headers, query string, or unbounded streaming. The simple
// helpers below are thin wrappers over Send.
type Request struct {
	Method      string            // defaults to GET
	Path        string            // path under BaseURL (leading slash)
	Query       url.Values        // optional query parameters
	Body        io.Reader         // optional raw body
	ContentType string            // Content-Type for Body
	Accept      string            // Accept header; defaults to application/json
	Headers     map[string]string // extra headers (override the above)
	NoTimeout   bool              // skip the 120s client timeout (for streaming)
	Ctx         context.Context   // optional request context (for cancellation)
}

// Send issues the described request and returns the live response. The
// caller owns resp.Body and must close it.
func (c *Client) Send(r Request) (*http.Response, error) {
	u := c.BaseURL + r.Path
	if len(r.Query) > 0 {
		u += "?" + r.Query.Encode()
	}

	method := r.Method
	if method == "" {
		method = http.MethodGet
	}

	ctx := r.Ctx
	if ctx == nil {
		ctx = context.Background()
	}
	req, err := http.NewRequestWithContext(ctx, method, u, r.Body)
	if err != nil {
		return nil, fmt.Errorf("create request: %w", err)
	}

	if r.ContentType != "" {
		req.Header.Set("Content-Type", r.ContentType)
	}
	accept := r.Accept
	if accept == "" {
		accept = "application/json"
	}
	req.Header.Set("Accept", accept)
	if c.APIKey != "" {
		req.Header.Set("X-Orva-API-Key", c.APIKey)
	}
	for k, v := range r.Headers {
		req.Header.Set(k, v)
	}

	httpClient := c.HTTP
	if r.NoTimeout {
		// A copy with no overall deadline so long-lived streams (SSE,
		// streamed invocations, build-log follows) aren't killed at 120s.
		httpClient = &http.Client{Transport: c.HTTP.Transport}
	}

	resp, err := httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("request failed: %w", err)
	}
	return resp, nil
}

// Do sends an HTTP request with an optional JSON-encoded body.
func (c *Client) Do(method, path string, body any) (*http.Response, error) {
	var r Request
	r.Method = method
	r.Path = path
	if body != nil {
		data, err := json.Marshal(body)
		if err != nil {
			return nil, fmt.Errorf("marshal body: %w", err)
		}
		r.Body = bytes.NewReader(data)
		r.ContentType = "application/json"
	}
	return c.Send(r)
}

// Get sends a GET request.
func (c *Client) Get(path string) (*http.Response, error) {
	return c.Do(http.MethodGet, path, nil)
}

// Post sends a POST request with a JSON body.
func (c *Client) Post(path string, body any) (*http.Response, error) {
	return c.Do(http.MethodPost, path, body)
}

// Put sends a PUT request with a JSON body.
func (c *Client) Put(path string, body any) (*http.Response, error) {
	return c.Do(http.MethodPut, path, body)
}

// Patch sends a PATCH request with a JSON body.
func (c *Client) Patch(path string, body any) (*http.Response, error) {
	return c.Do(http.MethodPatch, path, body)
}

// Delete sends a DELETE request.
func (c *Client) Delete(path string) (*http.Response, error) {
	return c.Do(http.MethodDelete, path, nil)
}

// Stream issues a GET that is expected to stream (SSE, chunked) and returns
// the live response with no client-side timeout. The caller owns resp.Body.
func (c *Client) Stream(path string, query url.Values) (*http.Response, error) {
	return c.Send(Request{Path: path, Query: query, Accept: "text/event-stream", NoTimeout: true})
}
