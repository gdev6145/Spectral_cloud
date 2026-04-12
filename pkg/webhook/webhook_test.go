package webhook

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gdev6145/Spectral_cloud/pkg/events"
)

func TestEnabled(t *testing.T) {
	if New("", "", 0).Enabled() {
		t.Fatal("expected disabled with empty URL")
	}
	if !New("http://example.com/hook", "", 0).Enabled() {
		t.Fatal("expected enabled with URL set")
	}
}

func TestDefaultTimeout(t *testing.T) {
	d := New("http://example.com", "", 0)
	if d.client.Timeout != 5*time.Second {
		t.Fatalf("expected 5s default timeout, got %v", d.client.Timeout)
	}
}

func TestCustomTimeout(t *testing.T) {
	d := New("http://example.com", "", 2*time.Second)
	if d.client.Timeout != 2*time.Second {
		t.Fatalf("expected 2s timeout, got %v", d.client.Timeout)
	}
}

func TestVerifySignature_Valid(t *testing.T) {
	body := []byte(`{"type":"block.added"}`)
	secret := "test-secret"
	sig := "sha256=" + sign(body, secret)

	if !VerifySignature(sig, body, secret) {
		t.Fatal("expected valid signature to pass")
	}
}

func TestVerifySignature_Invalid(t *testing.T) {
	body := []byte(`{"type":"block.added"}`)
	if VerifySignature("sha256=badhash", body, "secret") {
		t.Fatal("expected invalid signature to fail")
	}
}

func TestVerifySignature_MissingPrefix(t *testing.T) {
	if VerifySignature("abc123", []byte("body"), "secret") {
		t.Fatal("expected failure without sha256= prefix")
	}
}

func TestVerifySignature_WrongSecret(t *testing.T) {
	body := []byte("payload")
	sig := "sha256=" + sign(body, "correct-secret")
	if VerifySignature(sig, body, "wrong-secret") {
		t.Fatal("expected failure with wrong secret")
	}
}

func TestVerifySignature_EmptyBody(t *testing.T) {
	body := []byte{}
	sig := "sha256=" + sign(body, "s")
	if !VerifySignature(sig, body, "s") {
		t.Fatal("expected valid signature for empty body")
	}
}

func TestDispatch_SendsPayload(t *testing.T) {
	received := make(chan events.Event, 1)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		var ev events.Event
		_ = json.Unmarshal(body, &ev)
		received <- ev
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	d := New(srv.URL, "", 5*time.Second)
	ev := events.Event{Type: "block.added", Data: map[string]any{"height": 1}}
	d.Dispatch(context.Background(), ev)

	select {
	case got := <-received:
		if got.Type != "block.added" {
			t.Fatalf("expected block.added, got %q", got.Type)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for webhook delivery")
	}
}

func TestDispatch_SetsSignatureHeader(t *testing.T) {
	sigHeader := make(chan string, 1)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		sigHeader <- r.Header.Get("X-Spectral-Signature")
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	d := New(srv.URL, "my-secret", 5*time.Second)
	d.Dispatch(context.Background(), events.Event{Type: "test.event"})

	select {
	case sig := <-sigHeader:
		if !strings.HasPrefix(sig, "sha256=") {
			t.Fatalf("expected sha256= prefix, got %q", sig)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for webhook")
	}
}

func TestDispatch_SetsEventTypeHeader(t *testing.T) {
	eventHeader := make(chan string, 1)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		eventHeader <- r.Header.Get("X-Spectral-Event")
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	d := New(srv.URL, "", 5*time.Second)
	d.Dispatch(context.Background(), events.Event{Type: "route.added"})

	select {
	case h := <-eventHeader:
		if h != "route.added" {
			t.Fatalf("expected route.added header, got %q", h)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for webhook")
	}
}

func TestDispatch_NoOpWhenDisabled(t *testing.T) {
	called := false
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called = true
	}))
	defer srv.Close()

	d := New("", "", 0)
	d.Dispatch(context.Background(), events.Event{Type: "test"})
	time.Sleep(100 * time.Millisecond)
	if called {
		t.Fatal("disabled dispatcher should not call server")
	}
}

func TestDispatch_SignatureMatchesBody(t *testing.T) {
	type capture struct {
		sig  string
		body []byte
	}
	ch := make(chan capture, 1)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		ch <- capture{sig: r.Header.Get("X-Spectral-Signature"), body: body}
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	secret := "verify-me"
	d := New(srv.URL, secret, 5*time.Second)
	d.Dispatch(context.Background(), events.Event{Type: "signed.event"})

	select {
	case c := <-ch:
		if !VerifySignature(c.sig, c.body, secret) {
			t.Fatal("signature on delivered payload does not verify")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timed out")
	}
}

func TestDispatch_HTTP4xxLogged(t *testing.T) {
	// 4xx responses should not panic; they log a warning
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadRequest) // 400
	}))
	defer srv.Close()

	d := New(srv.URL, "", 5*time.Second)
	d.Dispatch(context.Background(), events.Event{Type: "test.4xx"})
	time.Sleep(200 * time.Millisecond) // let goroutine complete
}

func TestDispatch_ServerDown(t *testing.T) {
	// Should not panic when server is unreachable
	d := New("http://127.0.0.1:19999", "", 500*time.Millisecond)
	d.Dispatch(context.Background(), events.Event{Type: "test.down"})
	time.Sleep(600 * time.Millisecond)
}
