// Package ocr talks to an OCR sidecar implementing the Shop OCR contract:
//
//	POST /ocr     multipart "image" → Result
//	GET  /healthz → Health
package ocr

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"strings"
	"time"
)

type Line struct {
	Text string     `json:"text"`
	Conf float64    `json:"conf"`
	Box  [4]float64 `json:"box"` // x0, y0, x1, y1 in pixels
}

type Result struct {
	Engine  string `json:"engine"`
	Version string `json:"version"`
	MS      int64  `json:"ms"`
	Width   int    `json:"width"`
	Height  int    `json:"height"`
	Lines   []Line `json:"lines"`
}

type Health struct {
	Engine string `json:"engine"`
	Ready  bool   `json:"ready"`
}

type Client struct {
	baseURL string
	http    *http.Client
}

func New(baseURL string) *Client {
	return &Client{
		baseURL: strings.TrimRight(baseURL, "/"),
		http:    &http.Client{Timeout: 120 * time.Second},
	}
}

func (c *Client) Health(ctx context.Context) (Health, error) {
	var h Health
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.baseURL+"/healthz", nil)
	if err != nil {
		return h, err
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return h, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return h, fmt.Errorf("ocr healthz: %s", resp.Status)
	}
	return h, json.NewDecoder(resp.Body).Decode(&h)
}

// Recognize sends one image to the sidecar.
func (c *Client) Recognize(ctx context.Context, filename string, image io.Reader) (Result, error) {
	var res Result
	var body bytes.Buffer
	mw := multipart.NewWriter(&body)
	fw, err := mw.CreateFormFile("image", filename)
	if err != nil {
		return res, err
	}
	if _, err := io.Copy(fw, image); err != nil {
		return res, err
	}
	if err := mw.Close(); err != nil {
		return res, err
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+"/ocr", &body)
	if err != nil {
		return res, err
	}
	req.Header.Set("Content-Type", mw.FormDataContentType())
	resp, err := c.http.Do(req)
	if err != nil {
		return res, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		msg, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		return res, fmt.Errorf("ocr: %s: %s", resp.Status, bytes.TrimSpace(msg))
	}
	return res, json.NewDecoder(resp.Body).Decode(&res)
}
