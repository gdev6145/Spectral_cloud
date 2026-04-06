// Package webhook provides a fire-and-forget HTTP webhook dispatcher.
// Each dispatched event is sent as a JSON POST with an optional HMAC-SHA256
// signature in the X-Spectral-Signature header (format: "sha256=<hex>").
package webhook

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"log"
	"net/http"
	"strings"
	"time"

	"github.com/gdev6145/Spectral_cloud/pkg/events"
)

// Dispatcher sends events to a configured HTTP endpoint.
type Dispatcher struct {
	url    string
	secret string
	client *http.Client
}

// New creates a Dispatcher. url is the webhook endpoint; secret is an optional
// HMAC-SHA256 signing key. A zero or negative timeout defaults to 5 seconds.
func New(url, secret string, timeout time.Duration) *Dispatcher {
	if timeout <= 0 {
		timeout = 5 * time.Second
	}
	return &Dispatcher{
		url:    strings.TrimSpace(url),
		secret: strings.TrimSpace(secret),
		client: &http.Client{Timeout: timeout},
	}
}

// Enabled reports whether a webhook URL is configured.
func (d *Dispatcher) Enabled() bool {
	return d.url != ""
}

// Dispatch sends event to the configured URL in a background goroutine.
// If the dispatcher is not enabled, the call is a no-op.
// The ctx is used for the HTTP request; callers typically pass a background
// context to avoid cancelling in-flight requests on graceful shutdown.
func (d *Dispatcher) Dispatch(ctx context.Context, event events.Event) {
	if !d.Enabled() {
		return
	}
	go func() {
		body, err := json.Marshal(event)
		if err != nil {
			log.Printf("webhook: marshal error: %v", err)
			return
		}
		req, err := http.NewRequestWithContext(ctx, http.MethodPost, d.url, bytes.NewReader(body))
		if err != nil {
			log.Printf("webhook: build request error: %v", err)
			return
		}
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("X-Spectral-Event", string(event.Type))
		if d.secret != "" {
			req.Header.Set("X-Spectral-Signature", "sha256="+sign(body, d.secret))
		}
		resp, err := d.client.Do(req)
		if err != nil {
			log.Printf("webhook: dispatch error for %s: %v", event.Type, err)
			return
		}
		_ = resp.Body.Close()
		if resp.StatusCode >= 400 {
			log.Printf("webhook: %s returned HTTP %d for event %s", d.url, resp.StatusCode, event.Type)
		}
	}()
}

// VerifySignature verifies an X-Spectral-Signature header value against the
// payload bytes and secret. Returns true if the signature is valid.
func VerifySignature(signatureHeader string, body []byte, secret string) bool {
	const prefix = "sha256="
	if !strings.HasPrefix(signatureHeader, prefix) {
		return false
	}
	provided := strings.TrimPrefix(signatureHeader, prefix)
	expected := sign(body, secret)
	if len(provided) != len(expected) {
		return false
	}
	var diff byte
	for i := 0; i < len(provided); i++ {
		diff |= provided[i] ^ expected[i]
	}
	return diff == 0
}

func sign(body []byte, secret string) string {
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write(body)
	return hex.EncodeToString(mac.Sum(nil))
}
