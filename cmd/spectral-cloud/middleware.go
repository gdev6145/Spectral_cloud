package main

import (
	"crypto/rand"
	"encoding/hex"
	"log"
	"net/http"
	"strings"
	"time"
)

// withRequestID injects an X-Request-ID header into every request/response.
// If the client supplies one it is reused; otherwise a random 16-byte hex ID
// is generated. The ID is available to downstream handlers via the
// X-Request-ID response header.
func withRequestID(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		id := strings.TrimSpace(r.Header.Get("X-Request-ID"))
		if id == "" {
			var b [16]byte
			if _, err := rand.Read(b[:]); err == nil {
				id = hex.EncodeToString(b[:])
			}
		}
		w.Header().Set("X-Request-ID", id)
		r.Header.Set("X-Request-ID", id)
		next.ServeHTTP(w, r)
	})
}

// withLogger logs each request's method, path, status code, and duration.
// It relies on statusRecorder (defined in main.go) to capture the status code.
func withLogger(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		rec := &statusRecorder{ResponseWriter: w, status: http.StatusOK}
		next.ServeHTTP(rec, r)
		dur := time.Since(start)
		reqID := r.Header.Get("X-Request-ID")
		log.Printf("method=%s path=%s status=%d duration=%s request_id=%s",
			r.Method, r.URL.Path, rec.status, dur.Round(time.Millisecond), reqID)
	})
}

// corsConfig holds the parsed CORS settings.
type corsConfig struct {
	allowedOrigins []string
	allowedMethods string
	allowedHeaders string
	maxAge         string
	allowAll       bool
}

// parseCORSConfig reads CORS settings from the environment:
//
//	CORS_ALLOWED_ORIGINS – comma-separated origins, or "*" for any. Empty = CORS disabled.
//	CORS_ALLOWED_METHODS – defaults to GET,POST,DELETE,OPTIONS
//	CORS_ALLOWED_HEADERS – defaults to Authorization,Content-Type,X-API-Key,X-Request-ID
//	CORS_MAX_AGE         – preflight cache duration in seconds, defaults to 86400
func parseCORSConfig(originsRaw, methodsRaw, headersRaw, maxAgeRaw string) corsConfig {
	originsRaw = strings.TrimSpace(originsRaw)
	if originsRaw == "" {
		return corsConfig{}
	}
	cfg := corsConfig{
		allowedMethods: "GET,POST,DELETE,OPTIONS",
		allowedHeaders: "Authorization,Content-Type,X-API-Key,X-Request-ID",
		maxAge:         "86400",
	}
	if strings.TrimSpace(methodsRaw) != "" {
		cfg.allowedMethods = strings.TrimSpace(methodsRaw)
	}
	if strings.TrimSpace(headersRaw) != "" {
		cfg.allowedHeaders = strings.TrimSpace(headersRaw)
	}
	if strings.TrimSpace(maxAgeRaw) != "" {
		cfg.maxAge = strings.TrimSpace(maxAgeRaw)
	}
	if originsRaw == "*" {
		cfg.allowAll = true
	} else {
		for _, o := range strings.Split(originsRaw, ",") {
			if o = strings.TrimSpace(o); o != "" {
				cfg.allowedOrigins = append(cfg.allowedOrigins, o)
			}
		}
	}
	return cfg
}

// withCORS applies CORS response headers. If cfg has no origins configured the
// middleware is a no-op. Preflight OPTIONS requests are handled and short-circuited.
func withCORS(next http.Handler, cfg corsConfig) http.Handler {
	if !cfg.allowAll && len(cfg.allowedOrigins) == 0 {
		return next
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		origin := r.Header.Get("Origin")
		allowed := false
		if cfg.allowAll {
			allowed = true
			w.Header().Set("Access-Control-Allow-Origin", "*")
		} else {
			for _, o := range cfg.allowedOrigins {
				if o == origin {
					allowed = true
					break
				}
			}
			if allowed {
				w.Header().Set("Access-Control-Allow-Origin", origin)
				w.Header().Add("Vary", "Origin")
			}
		}
		if allowed {
			w.Header().Set("Access-Control-Allow-Methods", cfg.allowedMethods)
			w.Header().Set("Access-Control-Allow-Headers", cfg.allowedHeaders)
			w.Header().Set("Access-Control-Max-Age", cfg.maxAge)
		}
		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		next.ServeHTTP(w, r)
	})
}
