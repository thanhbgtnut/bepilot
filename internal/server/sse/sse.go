// Package sse adapts the internal agent event stream to a Server-Sent Events
// response over Hertz. It ships two wire formats behind the same Writer: the
// Anthropic-style frames used by /v1/messages and the AG-UI protocol frames
// used by /v1/ag-ui.
package sse

import (
	"context"
	"sync"
	"time"

	"github.com/cloudwego/hertz/pkg/app"
	"github.com/cloudwego/hertz/pkg/protocol/consts"
	hsse "github.com/hertz-contrib/sse"

	"github.com/thanhenti/bepilot/internal/agent/events"
)

// Mapper turns one internal event into zero or more SSE frames. Implementations
// are stateful and are called from a single goroutine.
type Mapper interface {
	Map(events.Event) []*hsse.Event
}

// Writer serialises agent events to an SSE stream via a Mapper, and (when a
// keep-alive frame is configured) emits periodic pings so intermediaries don't
// close an idle stream.
type Writer struct {
	stream *hsse.Stream
	mapper Mapper
	ping   *hsse.Event // nil disables keep-alive pings

	mu       sync.Mutex
	lastSend time.Time
	closed   bool
}

// NewWriter prepares an Anthropic-style SSE stream. inputTokens seeds the usage
// reported in message_start.
func NewWriter(c *app.RequestContext, inputTokens int) *Writer {
	return newWriter(c, events.NewAnthropicMapper(inputTokens),
		&hsse.Event{Event: "ping", Data: []byte(`{"type":"ping"}`)})
}

// NewAGUIWriter prepares an AG-UI protocol SSE stream for one run and returns
// the Writer together with its mapper (the caller inspects Started/Ended to
// close a partial stream). AG-UI has no ping frame, so keep-alives are off.
func NewAGUIWriter(c *app.RequestContext, threadID, runID string) (*Writer, *events.AGUIMapper) {
	m := events.NewAGUIMapper(threadID, runID)
	return newWriter(c, m, nil), m
}

func newWriter(c *app.RequestContext, m Mapper, ping *hsse.Event) *Writer {
	c.SetStatusCode(consts.StatusOK)
	c.Response.Header.Set("Content-Type", "text/event-stream; charset=utf-8")
	c.Response.Header.Set("Cache-Control", "no-cache")
	c.Response.Header.Set("Connection", "keep-alive")
	c.Response.Header.Set("X-Accel-Buffering", "no")
	return &Writer{
		stream:   hsse.NewStream(c),
		mapper:   m,
		ping:     ping,
		lastSend: time.Now(),
	}
}

// Emit maps and writes one internal event. It is safe for concurrent use.
func (w *Writer) Emit(ev events.Event) {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.closed {
		return
	}
	for _, frame := range w.mapper.Map(ev) {
		_ = w.stream.Publish(frame)
	}
	w.lastSend = time.Now()
}

// Publish writes one already-built frame directly, bypassing the mapper. Used
// for transport-level lifecycle frames the handler owns.
func (w *Writer) Publish(frame *hsse.Event) {
	if frame == nil {
		return
	}
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.closed {
		return
	}
	_ = w.stream.Publish(frame)
	w.lastSend = time.Now()
}

// Ping writes the keep-alive frame if one is configured and the stream has been
// idle.
func (w *Writer) Ping() {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.closed || w.ping == nil || time.Since(w.lastSend) < time.Second {
		return
	}
	_ = w.stream.Publish(w.ping)
	w.lastSend = time.Now()
}

// Close marks the writer done; further Emit / Publish calls are no-ops.
func (w *Writer) Close() {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.closed = true
}

// RunPinger drives Ping on an interval until ctx is cancelled. Call in a
// goroutine for the lifetime of the request. A no-op when keep-alives are off.
func (w *Writer) RunPinger(ctx context.Context, every time.Duration) {
	if w.ping == nil {
		return
	}
	if every <= 0 {
		every = 15 * time.Second
	}
	t := time.NewTicker(every)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			w.Ping()
		}
	}
}
