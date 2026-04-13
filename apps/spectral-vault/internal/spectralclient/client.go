package spectralclient

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// Config holds the configuration for a Spectral Cloud client.
type Config struct {
	BaseURL    string
	APIKey     string
	Tenant     string
	HTTPClient *http.Client
}

// Message represents a dequeued MQ message.
type Message struct {
	ID        string         `json:"id"`
	Topic     string         `json:"topic"`
	Tenant    string         `json:"tenant"`
	Payload   map[string]any `json:"payload"`
	CreatedAt time.Time      `json:"created_at"`
}

// Client is an HTTP client for Spectral Cloud MQ endpoints.
type Client struct {
	cfg  Config
	http *http.Client
}

// New creates a new Client. If cfg.HTTPClient is nil, a default 30-second-timeout client is used.
func New(cfg Config) *Client {
	hc := cfg.HTTPClient
	if hc == nil {
		hc = &http.Client{Timeout: 30 * time.Second}
	}
	return &Client{cfg: cfg, http: hc}
}

// Publish sends a message to the given topic.
func (c *Client) Publish(ctx context.Context, topic string, payload map[string]any) error {
	body, err := json.Marshal(map[string]any{"payload": payload})
	if err != nil {
		return fmt.Errorf("spectralclient: marshal publish body: %w", err)
	}
	url := strings.TrimRight(c.cfg.BaseURL, "/") + "/mq/" + topic
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("spectralclient: build publish request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	c.setAuthHeaders(req)

	resp, err := c.http.Do(req)
	if err != nil {
		return fmt.Errorf("spectralclient: publish: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		b, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("spectralclient: publish: unexpected status %d: %s", resp.StatusCode, string(b))
	}
	return nil
}

// Consume dequeues up to count messages from topic.
func (c *Client) Consume(ctx context.Context, topic string, count int) ([]Message, error) {
	url := fmt.Sprintf("%s/mq/%s?count=%d",
		strings.TrimRight(c.cfg.BaseURL, "/"), topic, count)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, fmt.Errorf("spectralclient: build consume request: %w", err)
	}
	c.setAuthHeaders(req)

	resp, err := c.http.Do(req)
	if err != nil {
		return nil, fmt.Errorf("spectralclient: consume: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		b, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("spectralclient: consume: unexpected status %d: %s", resp.StatusCode, string(b))
	}
	var msgs []Message
	if err = json.NewDecoder(resp.Body).Decode(&msgs); err != nil {
		return nil, fmt.Errorf("spectralclient: consume decode: %w", err)
	}
	return msgs, nil
}

// setAuthHeaders attaches authentication and tenant headers to the request.
func (c *Client) setAuthHeaders(req *http.Request) {
	key := c.cfg.APIKey
	if key != "" {
		if strings.HasPrefix(key, "sk-") || strings.HasPrefix(key, "Bearer ") {
			if strings.HasPrefix(key, "Bearer ") {
				req.Header.Set("Authorization", key)
			} else {
				req.Header.Set("Authorization", "Bearer "+key)
			}
		} else {
			req.Header.Set("X-API-Key", key)
		}
	}
	if c.cfg.Tenant != "" {
		req.Header.Set("X-Tenant-ID", c.cfg.Tenant)
	}
}
