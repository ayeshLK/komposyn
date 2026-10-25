// Package mcpworkspace exposes a bounded, read-only workspace through MCP.
package mcpworkspace

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

const maxFileBytes = 1 << 20
const maxEntries = 200

type Workspace struct {
	root *os.Root
	path string
}

func New(path string) (*Workspace, error) {
	abs, err := filepath.Abs(path)
	if err != nil {
		return nil, err
	}
	abs, err = filepath.EvalSymlinks(abs)
	if err != nil {
		return nil, fmt.Errorf("resolve workspace root: %w", err)
	}
	root, err := os.OpenRoot(abs)
	if err != nil {
		return nil, fmt.Errorf("open workspace: %w", err)
	}
	return &Workspace{root: root, path: abs}, nil
}
func (w *Workspace) Close() error     { return w.root.Close() }
func (w *Workspace) RootPath() string { return w.path }
func (w *Workspace) Server() *mcp.Server {
	s := mcp.NewServer(&mcp.Implementation{Name: "komposyn-workspace", Version: "0.1.0"}, nil)
	mcp.AddTool(s, &mcp.Tool{Name: "list_files", Description: "List names and types in a workspace directory (read only)."}, w.list)
	mcp.AddTool(s, &mcp.Tool{Name: "read_file", Description: "Read a UTF-8 workspace file up to 1 MiB (read only)."}, w.read)
	return s
}

type listArgs struct {
	Path string `json:"path"`
}
type readArgs struct {
	Path string `json:"path"`
}

func (w *Workspace) list(ctx context.Context, _ *mcp.CallToolRequest, a listArgs) (*mcp.CallToolResult, any, error) {
	if err := ctx.Err(); err != nil {
		return nil, nil, err
	}
	p, err := clean(a.Path)
	if err != nil {
		return nil, nil, err
	}
	dir, err := w.root.Open(p)
	if err != nil {
		return nil, nil, err
	}
	defer dir.Close()
	entries, err := dir.ReadDir(maxEntries + 1)
	if err != nil {
		return nil, nil, err
	}
	if len(entries) > maxEntries {
		entries = entries[:maxEntries]
	}
	type entry struct {
		Name string `json:"name"`
		Kind string `json:"kind"`
	}
	listing := make([]entry, 0, len(entries))
	for _, e := range entries {
		kind := "file"
		if e.IsDir() {
			kind = "directory"
		}
		listing = append(listing, entry{Name: e.Name(), Kind: kind})
	}
	encoded, _ := json.Marshal(listing)
	return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: string(encoded)}}}, nil, nil
}
func (w *Workspace) read(ctx context.Context, _ *mcp.CallToolRequest, a readArgs) (*mcp.CallToolResult, any, error) {
	if err := ctx.Err(); err != nil {
		return nil, nil, err
	}
	p, err := clean(a.Path)
	if err != nil {
		return nil, nil, err
	}
	f, err := w.root.Open(p)
	if err != nil {
		return nil, nil, err
	}
	defer f.Close()
	data, err := io.ReadAll(io.LimitReader(f, maxFileBytes+1))
	if err != nil {
		return nil, nil, err
	}
	if len(data) > maxFileBytes {
		return nil, nil, errors.New("file exceeds 1 MiB read limit")
	}
	if !utf8.Valid(data) {
		return nil, nil, errors.New("file is not valid UTF-8 text")
	}
	return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: string(data)}}}, nil, nil
}
func clean(p string) (string, error) {
	if p == "" || len(p) > 4096 || filepath.IsAbs(p) || strings.Contains(p, "\\") || !fs.ValidPath(p) || p == "." {
		return "", errors.New("path must be a workspace-relative file or directory")
	}
	return p, nil
}

type Client struct {
	session *mcp.ClientSession
	server  *mcp.ServerSession
	root    string
}

func Connect(ctx context.Context, w *Workspace) (*Client, error) {
	ct, st := mcp.NewInMemoryTransports()
	srv := w.Server()
	ss, err := srv.Connect(ctx, st, nil)
	if err != nil {
		return nil, err
	}
	cl := mcp.NewClient(&mcp.Implementation{Name: "komposyn-harness", Version: "0.1.0"}, nil)
	cs, err := cl.Connect(ctx, ct, nil)
	if err != nil {
		_ = ss.Close()
		return nil, err
	}
	return &Client{session: cs, server: ss, root: w.path}, nil
}
func (c *Client) RootPath() string { return c.root }
func (c *Client) Close() error {
	if c.session != nil {
		_ = c.session.Close()
	}
	if c.server != nil {
		_ = c.server.Close()
	}
	return nil
}
func (c *Client) ListTools(ctx context.Context) ([]*mcp.Tool, error) {
	r, e := c.session.ListTools(ctx, nil)
	if e != nil {
		return nil, e
	}
	return r.Tools, nil
}
func (c *Client) Call(ctx context.Context, name, path string) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	var args any
	switch name {
	case "list_files":
		args = map[string]any{"path": path}
	case "read_file":
		args = map[string]any{"path": path}
	default:
		return "", errors.New("unsupported workspace tool")
	}
	r, e := c.session.CallTool(ctx, &mcp.CallToolParams{Name: name, Arguments: args})
	if e != nil {
		return "", e
	}
	var b strings.Builder
	for _, item := range r.Content {
		if t, ok := item.(*mcp.TextContent); ok {
			b.WriteString(t.Text)
		}
	}
	if r.IsError {
		return b.String(), errors.New(b.String())
	}
	if b.Len() > maxFileBytes+maxEntries*256 {
		return "", errors.New("tool result exceeds limit")
	}
	return b.String(), nil
}
