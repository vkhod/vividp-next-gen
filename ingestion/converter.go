// ═══════════════════════════════════════════════════════════════════════════════
// ingestion/converter.go
// HTTP client for the conversion service (POST /convert).
// Replaces the old in-process ImageMagick/Ghostscript calls.
// ═══════════════════════════════════════════════════════════════════════════════
package ingestion

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
)

// ConversionResult describes the pages returned by the conversion service.
type ConversionResult struct {
	Pages []ConvertedPage
}

// ConvertedPage is one JPEG page produced by the conversion service.
type ConvertedPage struct {
	Key       string `json:"key"`
	PageNum   int    `json:"page_num"`
	SizeBytes int64  `json:"size_bytes"`
}

// ConversionClient calls the conversion service over HTTP.
type ConversionClient struct {
	baseURL    string
	httpClient *http.Client
}

func NewConversionClient(baseURL string) *ConversionClient {
	return &ConversionClient{
		baseURL:    baseURL,
		httpClient: &http.Client{Timeout: 0}, // no global timeout — set per-request via context
	}
}

// Convert requests the conversion service to convert sourceKey to per-page JPEGs.
// sourceKey must already be uploaded to the jobs bucket before calling this.
func (c *ConversionClient) Convert(ctx context.Context, jobID, tenantID, systemID, sourceKey string) (*ConversionResult, error) {
	reqBody := map[string]any{
		"source_key":    sourceKey,
		"source_bucket": "jobs",
		"job_id":        jobID,
		"tenant_id":     tenantID,
		"system_id":     systemID,
		"target_format": "jpeg_pages",
		"options": map[string]int{
			"dpi":     150,
			"quality": 85,
		},
	}

	data, _ := json.Marshal(reqBody)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+"/convert", bytes.NewReader(data))
	if err != nil {
		return nil, fmt.Errorf("build request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("conversion service request: %w", err)
	}
	defer resp.Body.Close()

	body, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("conversion service returned %d: %s", resp.StatusCode, body)
	}

	var result struct {
		Pages []ConvertedPage `json:"pages"`
	}
	if err := json.Unmarshal(body, &result); err != nil {
		return nil, fmt.Errorf("parse conversion response: %w", err)
	}

	return &ConversionResult{Pages: result.Pages}, nil
}
