package gh_test

import (
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/saruman/runoq/internal/gh"
)

func TestLoadPrivateKey(t *testing.T) {
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("generate key: %v", err)
	}
	pemBytes := pem.EncodeToMemory(&pem.Block{
		Type:  "RSA PRIVATE KEY",
		Bytes: x509.MarshalPKCS1PrivateKey(key),
	})
	path := filepath.Join(t.TempDir(), "key.pem")
	if err := os.WriteFile(path, pemBytes, 0600); err != nil {
		t.Fatalf("write key: %v", err)
	}

	loaded, err := gh.LoadPrivateKey(path)
	if err != nil {
		t.Fatalf("LoadPrivateKey: %v", err)
	}
	if loaded == nil {
		t.Fatal("expected non-nil key")
	}
}

func TestLoadPrivateKey_InvalidPEM(t *testing.T) {
	path := filepath.Join(t.TempDir(), "bad.pem")
	if err := os.WriteFile(path, []byte("not a pem file"), 0600); err != nil {
		t.Fatalf("write file: %v", err)
	}

	_, err := gh.LoadPrivateKey(path)
	if err == nil {
		t.Fatal("expected error for invalid PEM")
	}
}

func TestMintBotToken(t *testing.T) {
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("generate key: %v", err)
	}

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			t.Errorf("expected POST, got %s", r.Method)
		}
		auth := r.Header.Get("Authorization")
		if auth == "" {
			t.Error("missing Authorization header")
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]string{"token": "test-tok-123"})
	}))
	defer srv.Close()

	// Replace the GitHub API URL by using a custom http.Client with a redirect transport.
	client := srv.Client()
	origTransport := client.Transport
	client.Transport = roundTripFunc(func(r *http.Request) (*http.Response, error) {
		r.URL.Scheme = "http"
		r.URL.Host = srv.Listener.Addr().String()
		if origTransport != nil {
			return origTransport.RoundTrip(r)
		}
		return http.DefaultTransport.RoundTrip(r)
	})

	token, err := gh.MintBotToken(client, 12345, 67890, key)
	if err != nil {
		t.Fatalf("MintBotToken: %v", err)
	}
	if token != "test-tok-123" {
		t.Fatalf("expected test-tok-123, got %s", token)
	}
}

func TestMintBotToken_HTTPError(t *testing.T) {
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("generate key: %v", err)
	}

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "internal server error", http.StatusInternalServerError)
	}))
	defer srv.Close()

	client := srv.Client()
	origTransport := client.Transport
	client.Transport = roundTripFunc(func(r *http.Request) (*http.Response, error) {
		r.URL.Scheme = "http"
		r.URL.Host = srv.Listener.Addr().String()
		if origTransport != nil {
			return origTransport.RoundTrip(r)
		}
		return http.DefaultTransport.RoundTrip(r)
	})

	_, err = gh.MintBotToken(client, 12345, 67890, key)
	if err == nil {
		t.Fatal("expected error for HTTP 500")
	}
}

func TestMintBotTokenForOwner(t *testing.T) {
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("generate key: %v", err)
	}

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.URL.Path == "/app/installations" && r.Method == http.MethodGet:
			json.NewEncoder(w).Encode([]map[string]any{
				{"id": 111, "account": map[string]string{"login": "other-org"}},
				{"id": 222, "account": map[string]string{"login": "my-org"}},
			})
		case r.Method == http.MethodPost && r.URL.Path == "/app/installations/222/access_tokens":
			json.NewEncoder(w).Encode(map[string]string{"token": "dynamic-tok"})
		default:
			http.Error(w, "unexpected request: "+r.URL.Path, 404)
		}
	}))
	defer srv.Close()

	client := srv.Client()
	origTransport := client.Transport
	client.Transport = roundTripFunc(func(r *http.Request) (*http.Response, error) {
		r.URL.Scheme = "http"
		r.URL.Host = srv.Listener.Addr().String()
		if origTransport != nil {
			return origTransport.RoundTrip(r)
		}
		return http.DefaultTransport.RoundTrip(r)
	})

	token, err := gh.MintBotTokenForOwner(client, 12345, key, "my-org")
	if err != nil {
		t.Fatalf("MintBotTokenForOwner: %v", err)
	}
	if token != "dynamic-tok" {
		t.Fatalf("expected dynamic-tok, got %s", token)
	}
}

func TestMintBotTokenForOwner_NotFound(t *testing.T) {
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("generate key: %v", err)
	}

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode([]map[string]any{
			{"id": 111, "account": map[string]string{"login": "other-org"}},
		})
	}))
	defer srv.Close()

	client := srv.Client()
	origTransport := client.Transport
	client.Transport = roundTripFunc(func(r *http.Request) (*http.Response, error) {
		r.URL.Scheme = "http"
		r.URL.Host = srv.Listener.Addr().String()
		if origTransport != nil {
			return origTransport.RoundTrip(r)
		}
		return http.DefaultTransport.RoundTrip(r)
	})

	_, err = gh.MintBotTokenForOwner(client, 12345, key, "missing-org")
	if err == nil {
		t.Fatal("expected error for missing org")
	}
	if !strings.Contains(err.Error(), "no installation found") {
		t.Fatalf("unexpected error: %v", err)
	}
}

// roundTripFunc adapts a function to http.RoundTripper.
type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) {
	return f(r)
}
