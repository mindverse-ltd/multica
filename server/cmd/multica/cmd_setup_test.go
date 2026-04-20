package main

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestResolveSelfHostURLs(t *testing.T) {
	t.Run("defaults to localhost ports", func(t *testing.T) {
		serverURL, appURL := resolveSelfHostURLs("", "", 8080, 3000)
		if serverURL != "http://localhost:8080" {
			t.Fatalf("serverURL = %q, want %q", serverURL, "http://localhost:8080")
		}
		if appURL != "http://localhost:3000" {
			t.Fatalf("appURL = %q, want %q", appURL, "http://localhost:3000")
		}
	})

	t.Run("keeps explicit app url", func(t *testing.T) {
		serverURL, appURL := resolveSelfHostURLs("http://115.190.235.210:14000", "https://app.example.com", 8080, 3000)
		if serverURL != "http://115.190.235.210:14000" {
			t.Fatalf("serverURL = %q, want exact input", serverURL)
		}
		if appURL != "https://app.example.com" {
			t.Fatalf("appURL = %q, want %q", appURL, "https://app.example.com")
		}
	})

	t.Run("infers app url from remote same-origin server url", func(t *testing.T) {
		serverURL, appURL := resolveSelfHostURLs("http://115.190.235.210:14000", "", 8080, 3000)
		if serverURL != "http://115.190.235.210:14000" {
			t.Fatalf("serverURL = %q, want exact input", serverURL)
		}
		if appURL != "http://115.190.235.210:14000" {
			t.Fatalf("appURL = %q, want %q", appURL, "http://115.190.235.210:14000")
		}
	})

	t.Run("keeps localhost frontend default when backend is localhost", func(t *testing.T) {
		_, appURL := resolveSelfHostURLs("http://localhost:14000", "", 8080, 3000)
		if appURL != "http://localhost:3000" {
			t.Fatalf("appURL = %q, want %q", appURL, "http://localhost:3000")
		}
	})

	t.Run("infers app url from websocket remote server url", func(t *testing.T) {
		_, appURL := resolveSelfHostURLs("wss://api.example.com/ws", "", 8080, 3000)
		if appURL != "https://api.example.com" {
			t.Fatalf("appURL = %q, want %q", appURL, "https://api.example.com")
		}
	})
}

func TestProbeServer(t *testing.T) {
	t.Run("accepts health endpoint", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Path != "/health" {
				t.Fatalf("unexpected path %q", r.URL.Path)
			}
			w.WriteHeader(http.StatusOK)
		}))
		defer srv.Close()

		if !probeServer(srv.URL) {
			t.Fatal("probeServer() = false, want true")
		}
	})

	t.Run("falls back to api me behind same-origin proxy", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			switch r.URL.Path {
			case "/health":
				http.NotFound(w, r)
			case "/api/me":
				w.WriteHeader(http.StatusUnauthorized)
			default:
				t.Fatalf("unexpected path %q", r.URL.Path)
			}
		}))
		defer srv.Close()

		if !probeServer(srv.URL) {
			t.Fatal("probeServer() = false, want true")
		}
	})

	t.Run("returns false when all probes fail", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusBadGateway)
			_, _ = fmt.Fprintln(w, "bad gateway")
		}))
		defer srv.Close()

		if probeServer(srv.URL) {
			t.Fatal("probeServer() = true, want false")
		}
	})
}
