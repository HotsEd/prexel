package handler

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/docker/docker/pkg/stdcopy"
	"github.com/go-chi/chi/v5"
	"github.com/prexel/prexel/internal/app"
	"github.com/prexel/prexel/internal/dockersvc"
	"github.com/prexel/prexel/internal/eventbus"
	"github.com/prexel/prexel/internal/server"
)

// EventHandler exposes the SSE-flavoured endpoints. Three flavours:
//
//	GET /api/v1/events             — every event on the bus
//	GET /api/v1/apps/{id}/events   — per-app filter (deploy.* + app.*)
//	GET /api/v1/apps/{id}/logs     — docker container log stream (not eventbus)
type EventHandler struct {
	apps    *app.Service
	servers *server.Service
	bus     *eventbus.Bus
}

// NewEventHandler wires the handler.
func NewEventHandler(apps *app.Service, servers *server.Service, bus *eventbus.Bus) *EventHandler {
	return &EventHandler{apps: apps, servers: servers, bus: bus}
}

// Global handles GET /api/v1/events (all events).
func (h *EventHandler) Global(w http.ResponseWriter, r *http.Request) {
	h.serveBus(w, r, "*")
}

// AppEvents handles GET /api/v1/apps/{id}/events — filters by app and deploy
// topics for that app. Implemented by serving two subscriptions: "app.<id>.*"
// and "deploy.*.*" filtered to this app via the payload's app_id.
//
// To keep the eventbus contract simple, we subscribe on a wildcard and discard
// events that don't carry the right app id. Deploy events embed app_id in
// payload (see deploy.Engine.publish).
func (h *EventHandler) AppEvents(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	a, err := h.apps.Get(r.Context(), id)
	if err != nil {
		writeAppError(w, err)
		return
	}
	h.serveBusFiltered(w, r, func(ev eventbus.Event) bool {
		if strings.HasPrefix(ev.Topic, "app."+a.ID+".") {
			return true
		}
		if strings.HasPrefix(ev.Topic, "deploy.") {
			// payload should have app_id
			if m, ok := ev.Payload.(map[string]any); ok {
				if v, ok := m["app_id"].(string); ok && v == a.ID {
					return true
				}
			}
		}
		return false
	})
}

// Logs handles GET /api/v1/apps/{id}/logs?tail=N — streams the running
// container's docker logs as SSE events of type "container.log".
func (h *EventHandler) Logs(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	a, err := h.apps.Get(r.Context(), id)
	if err != nil {
		writeAppError(w, err)
		return
	}
	if a.ContainerName == nil || a.ServerID == nil {
		writeError(w, http.StatusBadRequest, "no_container")
		return
	}

	tail := 100
	if v := r.URL.Query().Get("tail"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			tail = n
		}
	}

	provider, err := h.servers.Provider(r.Context(), *a.ServerID)
	if err != nil {
		writeJSON(w, http.StatusBadGateway, map[string]string{
			"error": "provider_unavailable", "message": err.Error(),
		})
		return
	}
	defer func() { _ = provider.Close() }()

	rc, err := dockersvc.StreamLogs(r.Context(), provider, *a.ContainerName, true, tail)
	if err != nil {
		writeJSON(w, http.StatusBadGateway, map[string]string{
			"error": "logs_unavailable", "message": err.Error(),
		})
		return
	}
	defer func() { _ = rc.Close() }()

	setSSEHeaders(w)
	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "streaming unsupported", http.StatusInternalServerError)
		return
	}

	// stdcopy splits stdout/stderr; we wrap a writer that emits one SSE event
	// per line per stream.
	stdoutW := newSSEStreamWriter(w, flusher, "container.log", "stdout")
	stderrW := newSSEStreamWriter(w, flusher, "container.log", "stderr")
	done := make(chan struct{})
	go func() {
		_, _ = stdcopy.StdCopy(stdoutW, stderrW, rc)
		close(done)
	}()
	select {
	case <-r.Context().Done():
	case <-done:
	}
}

// serveBus subscribes to the eventbus and streams every matching event as SSE.
func (h *EventHandler) serveBus(w http.ResponseWriter, r *http.Request, topic string) {
	h.serveBusFiltered(w, r, func(ev eventbus.Event) bool {
		if topic == "*" || topic == "" {
			return true
		}
		return strings.HasPrefix(ev.Topic, strings.TrimSuffix(topic, "*"))
	})
}

func (h *EventHandler) serveBusFiltered(w http.ResponseWriter, r *http.Request, match func(eventbus.Event) bool) {
	setSSEHeaders(w)
	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "streaming unsupported", http.StatusInternalServerError)
		return
	}
	flusher.Flush()

	lastID := r.Header.Get("Last-Event-ID")
	ch, unsub := h.bus.Subscribe("*", lastID)
	defer unsub()

	// Send a small comment as keep-alive heartbeat.
	heartbeat := time.NewTicker(15 * time.Second)
	defer heartbeat.Stop()

	for {
		select {
		case <-r.Context().Done():
			return
		case <-heartbeat.C:
			_, _ = fmt.Fprintf(w, ": keepalive %d\n\n", time.Now().Unix())
			flusher.Flush()
		case ev, ok := <-ch:
			if !ok {
				return
			}
			if match != nil && !match(ev) {
				continue
			}
			payload, _ := json.Marshal(ev.Payload)
			_, _ = fmt.Fprintf(w, "id: %s\nevent: %s\ndata: {\"topic\":%q,\"type\":%q,\"payload\":%s,\"ts\":%q}\n\n",
				ev.ID, ev.Type, ev.Topic, ev.Type, payload, ev.Timestamp.Format(time.RFC3339Nano))
			flusher.Flush()
		}
	}
}

func setSSEHeaders(w http.ResponseWriter) {
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("X-Accel-Buffering", "no")
}

// sseStreamWriter splits Docker log byte streams into lines and emits one SSE
// frame per line. It intentionally avoids buffering across writes so the
// client sees data immediately.
type sseStreamWriter struct {
	w         http.ResponseWriter
	flusher   http.Flusher
	eventType string
	stream    string
	buf       []byte
}

func newSSEStreamWriter(w http.ResponseWriter, f http.Flusher, evType, stream string) *sseStreamWriter {
	return &sseStreamWriter{w: w, flusher: f, eventType: evType, stream: stream}
}

func (s *sseStreamWriter) Write(p []byte) (int, error) {
	s.buf = append(s.buf, p...)
	for {
		i := bytesIndexByte(s.buf, '\n')
		if i < 0 {
			break
		}
		line := strings.TrimRight(string(s.buf[:i]), "\r")
		s.buf = s.buf[i+1:]
		s.emit(line)
	}
	return len(p), nil
}

func (s *sseStreamWriter) emit(line string) {
	payload, _ := json.Marshal(map[string]any{
		"stream": s.stream,
		"line":   line,
		"ts":     time.Now().UTC().Format(time.RFC3339Nano),
	})
	_, _ = fmt.Fprintf(s.w, "event: %s\ndata: %s\n\n", s.eventType, payload)
	s.flusher.Flush()
}

func bytesIndexByte(b []byte, c byte) int {
	for i, v := range b {
		if v == c {
			return i
		}
	}
	return -1
}

// guard against the unused import linter when tests aren't around.
var _ = bufio.NewScanner
var _ = io.Discard
var _ = context.Background
