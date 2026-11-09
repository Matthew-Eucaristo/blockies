// Package client implements [boards.API] over HTTP, for the CLI and for an
// MCP server that talks to a running board server.
package client

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/Matthew-Eucaristo/blockies/boards"
)

// Client talks to a board server.
type Client struct {
	Base string
	HTTP *http.Client
}

// New returns a client for the server at base (for example http://127.0.0.1:4318).
func New(base string) *Client {
	return &Client{Base: strings.TrimRight(base, "/"), HTTP: &http.Client{Timeout: 30 * time.Second}}
}

var _ boards.API = (*Client)(nil)

// ErrUnreachable wraps network errors, so callers can tell "no server" from
// "the server said no".
var ErrUnreachable = errors.New("server unreachable")

// Ping reports whether a board server answers at Base.
func (c *Client) Ping(ctx context.Context) bool {
	ctx, cancel := context.WithTimeout(ctx, 500*time.Millisecond)
	defer cancel()
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, c.Base+"/healthz", nil)
	resp, err := c.HTTP.Do(req)
	if err != nil {
		return false
	}
	_ = resp.Body.Close()
	return resp.StatusCode == http.StatusOK
}

func (c *Client) Write(ctx context.Context, id, src string) (boards.Result, error) {
	var r boards.Result
	return r, c.do(ctx, http.MethodPut, "/api/boards/"+url.PathEscape(id), src, &r)
}

func (c *Client) Append(ctx context.Context, id, lines string) (boards.Result, error) {
	var r boards.Result
	return r, c.do(ctx, http.MethodPost, "/api/boards/"+url.PathEscape(id)+"/lines", lines, &r)
}

func (c *Client) Read(ctx context.Context, id string) (boards.Result, error) {
	var r boards.Result
	return r, c.do(ctx, http.MethodGet, "/api/boards/"+url.PathEscape(id), "", &r)
}

func (c *Client) List(ctx context.Context) ([]boards.Result, error) {
	var r struct {
		Boards []boards.Result `json:"boards"`
	}
	return r.Boards, c.do(ctx, http.MethodGet, "/api/boards", "", &r)
}

func (c *Client) Delete(ctx context.Context, id string) error {
	return c.do(ctx, http.MethodDelete, "/api/boards/"+url.PathEscape(id), "", nil)
}

func (c *Client) do(ctx context.Context, method, path, body string, out any) error {
	var rd io.Reader
	if body != "" {
		rd = strings.NewReader(body)
	}
	req, err := http.NewRequestWithContext(ctx, method, c.Base+path, rd)
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "text/plain; charset=utf-8")
	resp, err := c.HTTP.Do(req)
	if err != nil {
		return fmt.Errorf("%w at %s: %w", ErrUnreachable, c.Base, err)
	}
	defer func() { _ = resp.Body.Close() }()
	b, err := io.ReadAll(io.LimitReader(resp.Body, 64<<20))
	if err != nil {
		return err
	}
	if resp.StatusCode >= 400 {
		var e boards.Error
		if json.Unmarshal(b, &e) == nil && e.Code != "" {
			return &e
		}
		return fmt.Errorf("%s %s: %s", method, path, resp.Status)
	}
	if out == nil || len(b) == 0 {
		return nil
	}
	return json.Unmarshal(b, out)
}
