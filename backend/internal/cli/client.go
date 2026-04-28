package cli

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
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

// Do sends an HTTP request with optional JSON body and returns the response.
func (c *Client) Do(method, path string, body any) (*http.Response, error) {
	url := c.BaseURL + path

	var req *http.Request
	var err error

	if body != nil {
		data, jsonErr := json.Marshal(body)
		if jsonErr != nil {
			return nil, fmt.Errorf("marshal body: %w", jsonErr)
		}
		req, err = http.NewRequest(method, url, bytes.NewReader(data))
		if err != nil {
			return nil, fmt.Errorf("create request: %w", err)
		}
		req.Header.Set("Content-Type", "application/json")
	} else {
		req, err = http.NewRequest(method, url, nil)
		if err != nil {
			return nil, fmt.Errorf("create request: %w", err)
		}
	}

	if c.APIKey != "" {
		req.Header.Set("X-Orva-API-Key", c.APIKey)
	}
	req.Header.Set("Accept", "application/json")

	resp, err := c.HTTP.Do(req)
	if err != nil {
		return nil, fmt.Errorf("request failed: %w", err)
	}
	return resp, nil
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

// Delete sends a DELETE request.
func (c *Client) Delete(path string) (*http.Response, error) {
	return c.Do(http.MethodDelete, path, nil)
}
