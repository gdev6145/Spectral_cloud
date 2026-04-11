package main

import (
	"context"
	"net/url"
	"testing"
)

func TestValidateFetchURLRejectsPrivateTargets(t *testing.T) {
	t.Parallel()

	tests := []string{
		"http://127.0.0.1",
		"http://10.0.0.8",
		"http://169.254.169.254/latest/meta-data",
		"http://[::1]/",
		"http://localhost:8080",
	}

	for _, raw := range tests {
		raw := raw
		t.Run(raw, func(t *testing.T) {
			t.Parallel()

			u, err := url.Parse(raw)
			if err != nil {
				t.Fatalf("parse url: %v", err)
			}
			if err := validateFetchURL(context.Background(), u); err == nil {
				t.Fatalf("expected %s to be rejected", raw)
			}
		})
	}
}

func TestValidateFetchURLRejectsNonHTTP(t *testing.T) {
	t.Parallel()

	u, err := url.Parse("ftp://example.com/file.txt")
	if err != nil {
		t.Fatalf("parse url: %v", err)
	}
	if err := validateFetchURL(context.Background(), u); err == nil {
		t.Fatal("expected non-http scheme to be rejected")
	}
}

func TestValidateFetchURLAllowsPublicLiteralIPs(t *testing.T) {
	t.Parallel()

	tests := []string{
		"http://8.8.8.8",
		"https://1.1.1.1/dns-query",
	}

	for _, raw := range tests {
		raw := raw
		t.Run(raw, func(t *testing.T) {
			t.Parallel()

			u, err := url.Parse(raw)
			if err != nil {
				t.Fatalf("parse url: %v", err)
			}
			if err := validateFetchURL(context.Background(), u); err != nil {
				t.Fatalf("expected %s to be allowed, got %v", raw, err)
			}
		})
	}
}
