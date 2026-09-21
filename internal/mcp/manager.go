// Package mcp attaches Model Context Protocol servers to the running process.
//
// Each server is dialed once, its tools are discovered and registered into the
// shared tools.Registry under a namespaced name ("<prefix><server>__<tool>"),
// and stay available until the server is removed. They are registered as
// deferred tools: the model only sees their names and must load one with the
// built-in tool_search before calling it. Because the agent snapshots the
// registry on every turn, adding or removing a server takes effect on the next
// message with no restart.
//
// Servers arrive from two sources, both hot: the config file (source "file",
// reconciled by Watcher) and the API (source "api", persisted in the database).
package mcp

import (
	"context"
	"fmt"
	"log/slog"
	"reflect"
	"sort"
	"sync"
	"time"

	einotool "github.com/cloudwego/eino/components/tool"
	mcpp "github.com/cloudwego/eino-ext/components/tool/mcp"
	mcpclient "github.com/mark3labs/mcp-go/client"
	mcptransport "github.com/mark3labs/mcp-go/client/transport"
	"github.com/mark3labs/mcp-go/mcp"

	"github.com/thanhenti/bepilot/internal/config"
	"github.com/thanhenti/bepilot/internal/tools"
)

// Source identifies where a server spec came from.
type Source string

const (
	SourceFile Source = "file"
	SourceAPI  Source = "api"
)

// Manager owns the live MCP client connections.
type Manager struct {
	reg         *tools.Registry
	log         *slog.Logger
	prefix      string
	initTimeout time.Duration

	baseCtx    context.Context
	cancelBase context.CancelFunc

	mu    sync.Mutex
	conns map[string]*conn
}

type conn struct {
	spec        config.MCPServerSpec
	source      Source
	cli         mcpclient.MCPClient
	toolNames   []string // prefixed names registered for this server
	connectedAt time.Time
}

// Status is a read-only view of one attached server.
type Status struct {
	Name        string    `json:"name"`
	Transport   string    `json:"transport"`
	Source      Source    `json:"source"`
	Tools       []string  `json:"tools"`
	ConnectedAt time.Time `json:"connected_at"`
}

// New creates a Manager. prefix defaults to "mcp__" when empty.
func New(reg *tools.Registry, log *slog.Logger, prefix string, initTimeout time.Duration) *Manager {
	if prefix == "" {
		prefix = "mcp__"
	}
	if initTimeout <= 0 {
		initTimeout = 20 * time.Second
	}
	base, cancel := context.WithCancel(context.Background())
	return &Manager{
		reg:         reg,
		log:         log,
		prefix:      prefix,
		initTimeout: initTimeout,
		baseCtx:     base,
		cancelBase:  cancel,
		conns:       map[string]*conn{},
	}
}

// Add dials the server, imports its tools, and registers them. It is an error
// to add a name that is already attached — call Remove first, or use Replace.
func (m *Manager) Add(ctx context.Context, spec config.MCPServerSpec, source Source) error {
	if err := spec.Validate(); err != nil {
		return err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, ok := m.conns[spec.Name]; ok {
		return fmt.Errorf("mcp server %q is already attached", spec.Name)
	}
	return m.addLocked(ctx, spec, source)
}

// Replace atomically swaps the spec for an attached server (or adds it if new).
func (m *Manager) Replace(ctx context.Context, spec config.MCPServerSpec, source Source) error {
	if err := spec.Validate(); err != nil {
		return err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, ok := m.conns[spec.Name]; ok {
		m.removeLocked(spec.Name)
	}
	return m.addLocked(ctx, spec, source)
}

// Remove detaches a server: its tools are unregistered and the client closed.
func (m *Manager) Remove(name string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, ok := m.conns[name]; !ok {
		return fmt.Errorf("mcp server %q is not attached", name)
	}
	m.removeLocked(name)
	return nil
}

// Has reports whether a server is currently attached.
func (m *Manager) Has(name string) bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	_, ok := m.conns[name]
	return ok
}

// SourceOf returns the source of an attached server.
func (m *Manager) SourceOf(name string) (Source, bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	c, ok := m.conns[name]
	if !ok {
		return "", false
	}
	return c.source, true
}

// List returns every attached server, ordered by name.
func (m *Manager) List() []Status {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]Status, 0, len(m.conns))
	for _, c := range m.conns {
		tn := append([]string{}, c.toolNames...)
		sort.Strings(tn)
		out = append(out, Status{
			Name:        c.spec.Name,
			Transport:   c.spec.Transport,
			Source:      c.source,
			Tools:       tn,
			ConnectedAt: c.connectedAt,
		})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

// Close detaches every server and stops the manager.
func (m *Manager) Close() {
	m.mu.Lock()
	defer m.mu.Unlock()
	for name := range m.conns {
		m.removeLocked(name)
	}
	m.cancelBase()
}

// --- locked internals ---------------------------------------------------------

func (m *Manager) addLocked(ctx context.Context, spec config.MCPServerSpec, source Source) error {
	cli, err := m.dial(ctx, spec)
	if err != nil {
		return fmt.Errorf("dial mcp server %q: %w", spec.Name, err)
	}

	initCtx, cancel := context.WithTimeout(ctx, m.initTimeout)
	defer cancel()

	initReq := mcp.InitializeRequest{}
	initReq.Params.ProtocolVersion = mcp.LATEST_PROTOCOL_VERSION
	initReq.Params.ClientInfo = mcp.Implementation{Name: "bepilot", Version: "1.0.0"}
	if _, err := cli.Initialize(initCtx, initReq); err != nil {
		_ = cli.Close()
		return fmt.Errorf("initialize mcp server %q: %w", spec.Name, err)
	}

	raw, err := mcpp.GetTools(initCtx, &mcpp.Config{
		Cli:          cli,
		ToolNameList: spec.ToolAllowlist,
	})
	if err != nil {
		_ = cli.Close()
		return fmt.Errorf("list tools of mcp server %q: %w", spec.Name, err)
	}

	var names []string
	for _, bt := range raw {
		inv, ok := bt.(einotool.InvokableTool)
		if !ok {
			continue
		}
		info, err := inv.Info(initCtx)
		if err != nil {
			m.reg.Unregister(names...)
			_ = cli.Close()
			return fmt.Errorf("tool info from mcp server %q: %w", spec.Name, err)
		}
		pname := fmt.Sprintf("%s%s__%s", m.prefix, spec.Name, info.Name)
		if _, err := m.reg.Register(ctx, tools.Prefixed(inv, pname)); err != nil {
			m.reg.Unregister(names...)
			_ = cli.Close()
			return fmt.Errorf("register tool %q: %w", pname, err)
		}
		names = append(names, pname)
	}

	m.conns[spec.Name] = &conn{
		spec:        spec,
		source:      source,
		cli:         cli,
		toolNames:   names,
		connectedAt: time.Now(),
	}
	m.log.Info("mcp server attached", "name", spec.Name, "transport", spec.Transport,
		"source", source, "tools", names)
	return nil
}

func (m *Manager) removeLocked(name string) {
	c := m.conns[name]
	m.reg.Unregister(c.toolNames...)
	if err := c.cli.Close(); err != nil {
		m.log.Warn("mcp client close failed", "name", name, "err", err)
	}
	delete(m.conns, name)
	m.log.Info("mcp server detached", "name", name)
}

func (m *Manager) dial(ctx context.Context, s config.MCPServerSpec) (mcpclient.MCPClient, error) {
	switch s.Transport {
	case "stdio":
		env := make([]string, 0, len(s.Env))
		for k, v := range s.Env {
			env = append(env, k+"="+v)
		}
		// NewStdioMCPClient starts the subprocess immediately and ties it to a
		// background context, so it outlives individual requests.
		return mcpclient.NewStdioMCPClient(s.Command, env, s.Args...)

	case "sse":
		cli, err := mcpclient.NewSSEMCPClient(s.URL, mcptransport.WithHeaders(s.Headers))
		if err != nil {
			return nil, err
		}
		if err := cli.Start(m.baseCtx); err != nil {
			_ = cli.Close()
			return nil, err
		}
		return cli, nil

	case "streamable_http":
		cli, err := mcpclient.NewStreamableHttpClient(s.URL, mcptransport.WithHTTPHeaders(s.Headers))
		if err != nil {
			return nil, err
		}
		if err := cli.Start(m.baseCtx); err != nil {
			_ = cli.Close()
			return nil, err
		}
		return cli, nil

	default:
		return nil, fmt.Errorf("unsupported transport %q", s.Transport)
	}
}

// specChanged reports whether two specs differ in any connection-relevant field.
func specChanged(a, b config.MCPServerSpec) bool {
	return !reflect.DeepEqual(a, b)
}
