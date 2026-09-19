package service

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"time"
)

// RemoveBGClient is a minimal client for the remove.bg background-removal API.
type RemoveBGClient struct {
	apiKey   string
	endpoint string
	http     *http.Client
}

// NewRemoveBGClient creates a client for the remove.bg API.
func NewRemoveBGClient(apiKey, endpoint string) *RemoveBGClient {
	return &RemoveBGClient{
		apiKey:   apiKey,
		endpoint: endpoint,
		http:     &http.Client{Timeout: 60 * time.Second},
	}
}

// RemoveResult carries the outcome of a background-removal request.
type RemoveResult struct {
	// Data is the resulting transparent PNG (or the requested format) when successful.
	Data []byte
	// Error is the API's error message, populated when the request fails.
	Error string
}

// removeBGError mirrors the JSON error body returned by remove.bg.
type removeBGError struct {
	Errors []struct {
		Title  string `json:"title"`
		Detail string `json:"detail"`
	} `json:"errors"`
}

// Remove sends the image to remove.bg and returns the background-removed result.
// It mirrors the Python reference implementation:
//
//	requests.post('https://api.remove.bg/v1.0/removebg',
//	    files={'image_file': open('/path/to/file.jpg', 'rb')},
//	    data={'size': 'auto'},
//	    headers={'X-Api-Key': 'INSERT_YOUR_API_KEY_HERE'})
func (c *RemoveBGClient) Remove(ctx context.Context, imageData []byte, filename, size string) (*RemoveResult, error) {
	if c.apiKey == "" {
		return nil, fmt.Errorf("REMOVE_BG_API_KEY is not set")
	}

	body := &bytes.Buffer{}
	writer := multipart.NewWriter(body)

	// files={'image_file': ...}
	part, err := writer.CreateFormFile("image_file", filename)
	if err != nil {
		return nil, fmt.Errorf("create form file: %w", err)
	}
	if _, err := part.Write(imageData); err != nil {
		return nil, fmt.Errorf("write image to form: %w", err)
	}

	// data={'size': 'auto'}
	if size == "" {
		size = "auto"
	}
	if err := writer.WriteField("size", size); err != nil {
		return nil, fmt.Errorf("write size field: %w", err)
	}
	if err := writer.Close(); err != nil {
		return nil, fmt.Errorf("close multipart writer: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.endpoint, body)
	if err != nil {
		return nil, fmt.Errorf("build request: %w", err)
	}
	req.Header.Set("Content-Type", writer.FormDataContentType())
	// headers={'X-Api-Key': 'INSERT_YOUR_API_KEY_HERE'}
	req.Header.Set("X-Api-Key", c.apiKey)

	resp, err := c.http.Do(req)
	if err != nil {
		return nil, fmt.Errorf("call remove.bg: %w", err)
	}
	defer resp.Body.Close()

	data, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("read remove.bg response: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		// Mirror: else: print("Error:", response.status_code, response.text)
		detail := parseRemoveBGError(data)
		return &RemoveResult{Error: detail}, fmt.Errorf("remove.bg returned status %d: %s", resp.StatusCode, detail)
	}

	return &RemoveResult{Data: data}, nil
}

func parseRemoveBGError(data []byte) string {
	var e removeBGError
	if err := json.Unmarshal(data, &e); err != nil {
		return string(data)
	}
	if len(e.Errors) > 0 {
		msg := e.Errors[0].Title
		if e.Errors[0].Detail != "" {
			msg += ": " + e.Errors[0].Detail
		}
		return msg
	}
	return string(data)
}
