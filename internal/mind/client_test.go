package mind

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestTokenOnlyInTheHeader(t *testing.T) {
	var sawAuth, sawURL string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		sawAuth, sawURL = r.Header.Get("Authorization"), r.URL.String()
		if !strings.HasPrefix(r.Header.Get("User-Agent"), "windy-node/") {
			t.Errorf("UA %q", r.Header.Get("User-Agent"))
		}
		_, _ = w.Write([]byte(`{"node_id":"nd_0123456789abcdef","state":"pending","poll_after_s":30}`))
	}))
	defer srv.Close()
	me, err := New(srv.URL, "0.1.0").Me(context.Background(), "wnt_secret")
	if err != nil || me.State != "pending" || me.PollAfterS != 30 {
		t.Fatalf("%+v %v", me, err)
	}
	if sawAuth != "Bearer wnt_secret" || strings.Contains(sawURL, "wnt_") {
		t.Fatalf("auth %q url %q", sawAuth, sawURL)
	}
}

func TestEnrolSendsCodeAndFacts(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var b map[string]any
		_ = json.NewDecoder(r.Body).Decode(&b)
		if r.URL.Path != "/v1/nodes/enrol" || b["code"] != "ABCD-1234" || b["facts"] == nil || r.Header.Get("Authorization") != "" {
			w.WriteHeader(400)
			_, _ = w.Write([]byte(`{"error":{"code":"code_invalid","message":"That code isn't valid."}}`))
			return
		}
		w.WriteHeader(201)
		_, _ = w.Write([]byte(`{"node_id":"nd_0123456789abcdef","node_token":"wnt_x","state":"pending"}`))
	}))
	defer srv.Close()
	c := New(srv.URL, "0.1.0")
	e, err := c.Enrol(context.Background(), "ABCD-1234", map[string]any{"os": "linux", "arch": "x86_64"})
	if err != nil || e.NodeID != "nd_0123456789abcdef" || e.NodeToken != "wnt_x" {
		t.Fatalf("%+v %v", e, err)
	}
	_, err = c.Enrol(context.Background(), "", map[string]any{"os": "linux"})
	var ae *APIError
	if !errors.As(err, &ae) || ae.Status != 400 || ae.Code != "code_invalid" {
		t.Fatalf("%v", err)
	}
}

func TestMissingReadingsRouteIsNotSupported(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(404)
		_, _ = w.Write([]byte(`{"detail":"Not Found"}`)) // FastAPI's bare 404: the route doesn't exist
	}))
	defer srv.Close()
	err := New(srv.URL, "0.1.0").PostReadings(context.Background(), "wnt_x", []json.RawMessage{json.RawMessage(`{}`)})
	if !errors.Is(err, ErrNotSupported) {
		t.Fatalf("%v", err)
	}
}
