package config

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// ---------------------------------------------------------------------------
// ProbeEmbed
// ---------------------------------------------------------------------------

func TestProbeEmbed_Success(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/embeddings" {
			http.NotFound(w, r)
			return
		}
		if r.Header.Get("Authorization") != "Bearer secret" {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		json.NewEncoder(w).Encode(map[string]any{
			"data": []map[string]any{
				{"embedding": []float64{0.1, 0.2, 0.3, 0.4}},
			},
		})
	}))
	defer srv.Close()

	res := ProbeEmbed(context.Background(), srv.URL, "test-model", "secret")
	if !res.OK {
		t.Fatalf("expected OK, got: %s", res.Detail)
	}
	if !strings.Contains(res.Detail, "dim=4") {
		t.Errorf("expected dim detail, got: %s", res.Detail)
	}
}

func TestProbeEmbed_404(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.NotFound(w, r)
	}))
	defer srv.Close()

	res := ProbeEmbed(context.Background(), srv.URL, "m", "")
	if res.OK {
		t.Fatal("expected failure")
	}
	if !strings.Contains(res.Detail, "404") {
		t.Errorf("expected 404 in detail, got: %s", res.Detail)
	}
}

func TestProbeEmbed_Malformed(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("not json"))
	}))
	defer srv.Close()

	res := ProbeEmbed(context.Background(), srv.URL, "m", "")
	if res.OK {
		t.Fatal("expected failure")
	}
	if !strings.Contains(res.Detail, "malformed") {
		t.Errorf("expected malformed detail, got: %s", res.Detail)
	}
}

func TestProbeEmbed_NoVector(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(map[string]any{"data": []map[string]any{{"embedding": []float64{}}}})
	}))
	defer srv.Close()

	res := ProbeEmbed(context.Background(), srv.URL, "m", "")
	if res.OK {
		t.Fatal("expected failure on empty vector")
	}
}

func TestProbeEmbed_Timeout(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(200 * time.Millisecond)
	}))
	defer srv.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	res := ProbeEmbed(ctx, srv.URL, "m", "")
	if res.OK {
		t.Fatal("expected failure on timeout")
	}
}

func TestProbeEmbed_NoEndpoint(t *testing.T) {
	res := ProbeEmbed(context.Background(), "", "m", "")
	if res.OK || !strings.Contains(res.Detail, "endpoint") {
		t.Errorf("expected no-endpoint failure, got %+v", res)
	}
}

// ---------------------------------------------------------------------------
// ProbeChat
// ---------------------------------------------------------------------------

func TestProbeChat_Success(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/chat/completions" {
			http.NotFound(w, r)
			return
		}
		json.NewEncoder(w).Encode(map[string]any{
			"choices": []map[string]any{
				{"message": map[string]any{"content": "ok"}},
			},
		})
	}))
	defer srv.Close()

	res := ProbeChat(context.Background(), srv.URL, "m", "")
	if !res.OK {
		t.Fatalf("expected OK, got: %s", res.Detail)
	}
}

func TestProbeChat_404(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.NotFound(w, r)
	}))
	defer srv.Close()

	res := ProbeChat(context.Background(), srv.URL, "m", "")
	if res.OK {
		t.Fatal("expected failure")
	}
}

func TestProbeChat_EmptyContent(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(map[string]any{
			"choices": []map[string]any{{"message": map[string]any{"content": ""}}},
		})
	}))
	defer srv.Close()

	res := ProbeChat(context.Background(), srv.URL, "m", "")
	if res.OK {
		t.Fatal("expected failure on empty content")
	}
}

func TestProbeChat_Malformed(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("garbage"))
	}))
	defer srv.Close()

	res := ProbeChat(context.Background(), srv.URL, "m", "")
	if res.OK {
		t.Fatal("expected failure")
	}
}

// ---------------------------------------------------------------------------
// ProbeRerank
// ---------------------------------------------------------------------------

func TestProbeRerank_Success(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/rerank" {
			http.NotFound(w, r)
			return
		}
		json.NewEncoder(w).Encode(map[string]any{
			"results": []map[string]any{{"index": 0, "score": 0.9}},
		})
	}))
	defer srv.Close()

	res := ProbeRerank(context.Background(), srv.URL, "m", "")
	if !res.OK {
		t.Fatalf("expected OK, got: %s", res.Detail)
	}
}

func TestProbeRerank_404(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.NotFound(w, r)
	}))
	defer srv.Close()

	res := ProbeRerank(context.Background(), srv.URL, "m", "")
	if res.OK {
		t.Fatal("expected failure")
	}
}

// ---------------------------------------------------------------------------
// ProbeService dispatch
// ---------------------------------------------------------------------------

func TestProbeService_Dispatch(t *testing.T) {
	cases := []struct {
		service string
		path    string
	}{
		{"embed", "/v1/embeddings"},
		{"llm", "/v1/chat/completions"},
		{"triplex", "/v1/chat/completions"},
		{"nuextract", "/v1/chat/completions"},
		{"rerank", "/v1/rerank"},
	}
	for _, tc := range cases {
		t.Run(tc.service, func(t *testing.T) {
			var gotPath string
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				gotPath = r.URL.Path
				// Return a generic-but-valid envelope for each shape.
				switch tc.service {
				case "embed":
					json.NewEncoder(w).Encode(map[string]any{"data": []map[string]any{{"embedding": []float64{0.1}}}})
				case "rerank":
					json.NewEncoder(w).Encode(map[string]any{"results": []map[string]any{}})
				default:
					json.NewEncoder(w).Encode(map[string]any{"choices": []map[string]any{{"message": map[string]any{"content": "ok"}}}})
				}
			}))
			defer srv.Close()

			res := ProbeService(context.Background(), tc.service, srv.URL, "m", "")
			if !res.OK {
				t.Fatalf("expected OK for %s, got: %s", tc.service, res.Detail)
			}
			if gotPath != tc.path {
				t.Errorf("expected %s to hit %s, got %s", tc.service, tc.path, gotPath)
			}
		})
	}
}

func TestProbeService_Unknown(t *testing.T) {
	res := ProbeService(context.Background(), "weird", "http://x", "m", "")
	if res.OK || !strings.Contains(res.Detail, "unknown") {
		t.Errorf("expected unknown-service failure, got %+v", res)
	}
}

// ---------------------------------------------------------------------------
// joinURL
// ---------------------------------------------------------------------------

func TestJoinURL(t *testing.T) {
	cases := []struct{ base, path, want string }{
		{"http://x", "/v1/foo", "http://x/v1/foo"},
		{"http://x/", "/v1/foo", "http://x/v1/foo"},
		{"http://x/", "v1/foo", "http://x/v1/foo"},
		{"http://x", "", "http://x"},
	}
	for _, tc := range cases {
		got := joinURL(tc.base, tc.path)
		if got != tc.want {
			t.Errorf("joinURL(%q, %q) = %q, want %q", tc.base, tc.path, got, tc.want)
		}
	}
}
