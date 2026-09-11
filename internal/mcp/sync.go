package mcp

import (
	"context"
	"os"
	"time"

	"github.com/thanhenti/bepilot/internal/config"
)

// AttachAll adds every spec, logging (not returning) per-server failures so one
// bad server never blocks the rest. Used for startup bootstrap from the file
// and from the database.
func (m *Manager) AttachAll(ctx context.Context, specs []config.MCPServerSpec, source Source) {
	for _, s := range specs {
		if m.Has(s.Name) {
			continue
		}
		if err := m.Add(ctx, s, source); err != nil {
			m.log.Warn("mcp server attach failed", "name", s.Name, "source", source, "err", err)
		}
	}
}

// SyncFile reconciles the set of file-sourced servers against the running
// connections: it attaches new specs, redials changed ones, and detaches
// specs that disappeared from the file. Servers owned by the API are left
// untouched (a name collision is logged and the file entry ignored).
func (m *Manager) SyncFile(ctx context.Context, specs []config.MCPServerSpec) {
	desired := make(map[string]config.MCPServerSpec, len(specs))
	for _, s := range specs {
		desired[s.Name] = s
	}

	// Detach file servers no longer present.
	for _, st := range m.List() {
		if st.Source != SourceFile {
			continue
		}
		if _, ok := desired[st.Name]; !ok {
			if err := m.Remove(st.Name); err != nil {
				m.log.Warn("mcp file sync: detach failed", "name", st.Name, "err", err)
			}
		}
	}

	// Attach / redial.
	for name, spec := range desired {
		src, attached := m.SourceOf(name)
		if attached && src == SourceAPI {
			m.log.Warn("mcp file sync: name is owned by an API-registered server, ignoring file entry", "name", name)
			continue
		}
		if !attached {
			if err := m.Add(ctx, spec, SourceFile); err != nil {
				m.log.Warn("mcp file sync: attach failed", "name", name, "err", err)
			}
			continue
		}
		if m.fileSpecChanged(name, spec) {
			if err := m.Replace(ctx, spec, SourceFile); err != nil {
				m.log.Warn("mcp file sync: redial failed", "name", name, "err", err)
			}
		}
	}
}

func (m *Manager) fileSpecChanged(name string, spec config.MCPServerSpec) bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	c, ok := m.conns[name]
	if !ok {
		return true
	}
	return specChanged(c.spec, spec)
}

// Watcher polls the MCP file for changes and reconciles them. It also retries
// file servers that are declared but not currently attached (e.g. a server that
// was down at startup). It blocks until ctx is cancelled; run it in a goroutine.
func Watcher(ctx context.Context, m *Manager, path string, interval time.Duration) {
	if path == "" || interval <= 0 {
		return
	}
	var lastMod time.Time
	tick := time.NewTicker(interval)
	defer tick.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-tick.C:
		}

		fi, err := os.Stat(path)
		if err != nil {
			if !os.IsNotExist(err) {
				m.log.Warn("mcp watcher: stat failed", "path", path, "err", err)
			}
			continue
		}

		changed := fi.ModTime().After(lastMod)
		if !changed && !m.hasMissingFileServer(path) {
			continue
		}

		specs, err := config.LoadMCPServers(path)
		if err != nil {
			m.log.Warn("mcp watcher: reload failed", "path", path, "err", err)
			continue
		}
		if changed {
			m.log.Info("mcp watcher: file changed, reconciling", "path", path, "servers", len(specs))
		}
		lastMod = fi.ModTime()
		m.SyncFile(ctx, specs)
	}
}

// hasMissingFileServer reports whether the file declares a server that is not
// currently attached, so the watcher can retry it even without a file change.
func (m *Manager) hasMissingFileServer(path string) bool {
	specs, err := config.LoadMCPServers(path)
	if err != nil {
		return false
	}
	for _, s := range specs {
		if !m.Has(s.Name) {
			return true
		}
	}
	return false
}
