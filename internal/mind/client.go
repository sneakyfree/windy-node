// Package mind talks to Windy Mind's node routes (contract node.v1). Outbound HTTPS only; the node token goes in
// the Authorization header and never in a URL.
package mind

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// DefaultURL is Windy Mind's public API.
const DefaultURL = "https://api.windymind.ai"

// ErrNotSupported means this Mind has no such route yet (404): keep the data, try again later.
var ErrNotSupported = errors.New("route not supported by this Mind yet")

// APIError is node.v1's error envelope.
type APIError struct {
	Status  int
	Code    string `json:"code"`
	Message string `json:"message"`
}

func (e *APIError) Error() string { return fmt.Sprintf("mind %d %s: %s", e.Status, e.Code, e.Message) }

type Client struct {
	BaseURL   string
	UserAgent string
	HTTP      *http.Client
}

func New(base, version string) *Client {
	if base == "" {
		base = DefaultURL
	}
	return &Client{BaseURL: strings.TrimRight(base, "/"), UserAgent: "windy-node/" + version,
		HTTP: &http.Client{Timeout: 20 * time.Second}}
}

// Enrolled is what POST /v1/nodes/enrol returns. NodeToken is shown once.
type Enrolled struct {
	NodeID    string `json:"node_id"`
	NodeToken string `json:"node_token"`
	State     string `json:"state"`
}

// Self is GET /v1/nodes/me.
type Self struct {
	NodeID     string `json:"node_id"`
	State      string `json:"state"`
	PollAfterS int    `json:"poll_after_s"`
}

func (c *Client) do(ctx context.Context, method, path, token string, body, out any) error {
	var rd io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return err
		}
		rd = bytes.NewReader(b)
	}
	req, err := http.NewRequestWithContext(ctx, method, c.BaseURL+path, rd)
	if err != nil {
		return err
	}
	req.Header.Set("User-Agent", c.UserAgent)
	req.Header.Set("Accept", "application/json")
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	resp, err := c.HTTP.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if resp.StatusCode == http.StatusNotFound && method != http.MethodGet {
		var env struct {
			Error *APIError `json:"error"`
		}
		if json.Unmarshal(raw, &env) != nil || env.Error == nil {
			return ErrNotSupported // a bare 404: the route itself does not exist here
		}
	}
	if resp.StatusCode >= 300 {
		e := &APIError{Status: resp.StatusCode}
		var env struct {
			Error *APIError `json:"error"`
		}
		if json.Unmarshal(raw, &env) == nil && env.Error != nil {
			e.Code, e.Message = env.Error.Code, env.Error.Message
		}
		return e
	}
	if out != nil && len(raw) > 0 {
		return json.Unmarshal(raw, out)
	}
	return nil
}

// Enrol trades a one-time code (shown to the owner after sign-in) for a node token.
func (c *Client) Enrol(ctx context.Context, code string, facts any) (Enrolled, error) {
	var out Enrolled
	err := c.do(ctx, http.MethodPost, "/v1/nodes/enrol", "", map[string]any{"code": code, "facts": facts}, &out)
	return out, err
}

// Me asks Mind for this node's state (pending until the owner approves it).
func (c *Client) Me(ctx context.Context, token string) (Self, error) {
	var out Self
	err := c.do(ctx, http.MethodGet, "/v1/nodes/me", token, nil, &out)
	return out, err
}

// PostReadings sends a batch (node.v1 1.1.0, proposed). ErrNotSupported until Mind has the route.
func (c *Client) PostReadings(ctx context.Context, token string, readings []json.RawMessage) error {
	return c.do(ctx, http.MethodPost, "/v1/nodes/me/readings", token, map[string]any{"readings": readings}, nil)
}
