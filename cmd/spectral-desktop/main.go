// spectral-desktop is a thin launcher that starts spectral-cloud in-process
// and opens a browser window to the dashboard UI.
//
// Usage:
//
//	spectral-desktop [--port 8080] [--data-dir ./data] [--api-key KEY]
package main

import (
	"flag"
	"fmt"
	"log"
	"net"
	"net/http"
	"os"
	"os/exec"
	"runtime"
	"time"
)

func main() {
	port := flag.String("port", "8080", "HTTP port for the server")
	dataDir := flag.String("data-dir", "data", "Data directory")
	apiKey := flag.String("api-key", "", "API key (optional)")
	flag.Parse()

	// Apply settings as env vars before the server binary reads them.
	// (When embedded, the server reads os.Getenv directly.)
	setIfEmpty("PORT", *port)
	setIfEmpty("DATA_DIR", *dataDir)
	if *apiKey != "" {
		setIfEmpty("API_KEY", *apiKey)
	}

	url := fmt.Sprintf("http://localhost:%s/ui/", *port)
	log.Printf("spectral-desktop: starting server on :%s", *port)
	log.Printf("spectral-desktop: dashboard → %s", url)

	// Start server in background goroutine.
	go func() {
		// Import the server binary from PATH or adjacent directory.
		bin, err := findBinary("spectral-cloud")
		if err != nil {
			log.Fatalf("spectral-cloud binary not found: %v\nBuild it with: go build ./cmd/spectral-cloud", err)
		}
		cmd := exec.Command(bin)
		cmd.Stdout = os.Stdout
		cmd.Stderr = os.Stderr
		cmd.Env = os.Environ()
		if err := cmd.Start(); err != nil {
			log.Fatalf("failed to start spectral-cloud: %v", err)
		}
	}()

	// Wait until the server is listening (up to 10s).
	if err := waitReady(fmt.Sprintf("localhost:%s", *port), 10*time.Second); err != nil {
		log.Printf("warning: server did not become ready: %v", err)
	}

	// Open browser.
	if err := openBrowser(url); err != nil {
		log.Printf("could not open browser automatically: %v", err)
		fmt.Printf("\nOpen your browser to: %s\n", url)
	}

	// Block forever (the subprocess holds the server).
	select {}
}

func setIfEmpty(key, val string) {
	if os.Getenv(key) == "" && val != "" {
		_ = os.Setenv(key, val)
	}
}

func findBinary(name string) (string, error) {
	// 1. Same directory as this binary.
	self, _ := os.Executable()
	if self != "" {
		dir := self[:len(self)-len("/spectral-desktop")]
		if i := lastSlash(self); i >= 0 {
			dir = self[:i]
		}
		candidate := dir + "/" + name
		if _, err := os.Stat(candidate); err == nil {
			return candidate, nil
		}
	}
	// 2. PATH.
	return exec.LookPath(name)
}

func lastSlash(s string) int {
	for i := len(s) - 1; i >= 0; i-- {
		if s[i] == '/' || s[i] == '\\' {
			return i
		}
	}
	return -1
}

func waitReady(addr string, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		conn, err := net.DialTimeout("tcp", addr, 500*time.Millisecond)
		if err == nil {
			_ = conn.Close()
			// Give HTTP a moment to be ready.
			time.Sleep(200 * time.Millisecond)
			if httpReady("http://" + addr + "/health") {
				return nil
			}
		}
		time.Sleep(300 * time.Millisecond)
	}
	return fmt.Errorf("timed out waiting for %s", addr)
}

func httpReady(url string) bool {
	c := &http.Client{Timeout: time.Second}
	resp, err := c.Get(url)
	if err != nil {
		return false
	}
	_ = resp.Body.Close()
	return resp.StatusCode == 200
}

func openBrowser(url string) error {
	var cmd string
	var args []string
	switch runtime.GOOS {
	case "linux":
		cmd = "xdg-open"
		args = []string{url}
	case "darwin":
		cmd = "open"
		args = []string{url}
	case "windows":
		cmd = "rundll32"
		args = []string{"url.dll,FileProtocolHandler", url}
	default:
		return fmt.Errorf("unsupported OS: %s", runtime.GOOS)
	}
	return exec.Command(cmd, args...).Start()
}
