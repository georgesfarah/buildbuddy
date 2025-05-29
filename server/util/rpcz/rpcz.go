package rpcz

import (
	"context"
	"fmt"
	"html/template"
	"log"
	"net/http"
	"sync"
	"time"

	"github.com/google/uuid"
	"google.golang.org/grpc/peer"
	"google.golang.org/grpc/stats"
)

var DefaultHandler = NewHandler()

// rpcStats holds metadata for each in-flight RPC
type rpcStats struct {
	ID        string
	Method    string
	PeerAddr  string
	Deadline  *time.Time
	BeginTime time.Time
}

// contextKey is a private type for storing values in context
type contextKey struct{}

// statsHandler tracks in-flight RPCs and implements stats.Handler
type statsHandler struct {
	mu   sync.RWMutex
	rpcs map[string]rpcStats
}

func (h *statsHandler) TagRPC(ctx context.Context, info *stats.RPCTagInfo) context.Context {
	rs := rpcStats{
		ID:        uuid.New().String(),
		Method:    info.FullMethodName,
		BeginTime: time.Now(),
	}
	if dl, ok := ctx.Deadline(); ok {
		rs.Deadline = &dl
	}
	if p, ok := peer.FromContext(ctx); ok {
		rs.PeerAddr = p.Addr.String()
	}
	return context.WithValue(ctx, contextKey{}, rs)
}

func (h *statsHandler) HandleRPC(ctx context.Context, stat stats.RPCStats) {
	val := ctx.Value(contextKey{})
	rs, ok := val.(rpcStats)
	if !ok {
		return
	}
	switch stat.(type) {
	case *stats.Begin:
		h.mu.Lock()
		h.rpcs[rs.ID] = rs
		h.mu.Unlock()
	case *stats.End:
		h.mu.Lock()
		delete(h.rpcs, rs.ID)
		h.mu.Unlock()
	}
}

func (h *statsHandler) TagConn(ctx context.Context, _ *stats.ConnTagInfo) context.Context { return ctx }
func (h *statsHandler) HandleConn(_ context.Context, _ stats.ConnStats)                   {}

// Handler aggregates incoming and outgoing stats
type Handler struct {
	server *statsHandler
	client *statsHandler
}

// NewHandler sets up handlers for both directions
func NewHandler() *Handler {
	return &Handler{
		server: &statsHandler{rpcs: make(map[string]rpcStats)},
		client: &statsHandler{rpcs: make(map[string]rpcStats)},
	}
}

func (h *Handler) Server() stats.Handler {
	return h.server
}

func (h *Handler) Client() stats.Handler {
	return h.client
}

func (z *Handler) Serve(handle func(pattern string, handler http.Handler)) {
	funcMap := template.FuncMap{
		"since": func(t time.Time) string {
			return time.Since(t).String()
		},
	}
	tmplIdx := template.Must(
		template.New("index").Funcs(funcMap).Parse(indexHTML),
	)
	tmplDet := template.Must(
		template.New("detail").Funcs(funcMap).Parse(detailHTML),
	)

	handle("/rpcz", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		type entry struct {
			Method, Direction string
			Count             int
		}
		counts := make(map[string]map[string]int)

		z.server.mu.RLock()
		for _, s := range z.server.rpcs {
			m := counts[s.Method]
			if m == nil {
				m = map[string]int{"incoming": 0}
			}
			m["incoming"]++
			counts[s.Method] = m
		}
		z.server.mu.RUnlock()

		z.client.mu.RLock()
		for _, s := range z.client.rpcs {
			m := counts[s.Method]
			if m == nil {
				m = map[string]int{"outgoing": 0}
			}
			m["outgoing"]++
			counts[s.Method] = m
		}
		z.client.mu.RUnlock()

		entries := []entry{}
		for method, m := range counts {
			if in := m["incoming"]; in > 0 {
				entries = append(entries, entry{method, "incoming", in})
			}
			if out := m["outgoing"]; out > 0 {
				entries = append(entries, entry{method, "outgoing", out})
			}
		}

		if err := tmplIdx.Execute(w, entries); err != nil {
			log.Printf("index template error: %v", err)
		}
	}))

	handle("/rpcz/method", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		name := r.URL.Query().Get("name")
		dir := r.URL.Query().Get("dir")
		if dir != "outgoing" {
			dir = "incoming"
		}

		var entries []rpcStats
		var mu *sync.RWMutex
		var store map[string]rpcStats
		if dir == "outgoing" {
			mu, store = &z.client.mu, z.client.rpcs
		} else {
			mu, store = &z.server.mu, z.server.rpcs
		}

		mu.RLock()
		for _, s := range store {
			if s.Method == name {
				entries = append(entries, s)
			}
		}
		mu.RUnlock()

		data := struct {
			Method, Dir string
			Entries     []rpcStats
		}{name, dir, entries}

		fmt.Printf("VANJAAAAAAAAAAAAAAA - template data%+v\n", data)
		if err := tmplDet.Execute(w, data); err != nil {
			log.Printf("detail template error: %v", err)
		}
	}))
}

const indexHTML = `<!DOCTYPE html>
<html>
  <head>
    <meta charset="UTF-8">
    <title>RPC Summary</title>
  </head>
  <body>
    <h1>In-Flight RPC Summary</h1>
    <table border="1">
      <tr>
        <th>Method</th>
        <th>Direction</th>
        <th>Count</th>
      </tr>
      {{- range . }}
      <tr>
        <td><a href="/rpcz/method?name={{ .Method }}&dir={{ .Direction }}">{{ .Method }}</a></td>
        <td>{{ .Direction }}</td>
        <td>{{ .Count }}</td>
      </tr>
      {{- end }}
    </table>
  </body>
</html>`

const detailHTML = `<!DOCTYPE html>
<html>
  <head>
    <meta charset="UTF-8">
    <title>RPC Details for {{ .Method }} ({{ .Dir }})</title>
  </head>
  <body>
    <h1>{{ .Method }} [{{ .Dir }}]</h1>
    <table border="1">
      <tr>
        <th>ID</th>
        <th>Peer</th>
        <th>Started</th>
        <th>Duration</th>
        <th>Deadline</th>
      </tr>
      {{- range .Entries }}
      <tr>
        <td>{{ .ID }}</td>
        <td>{{ .PeerAddr }}</td>
        <td>{{ .BeginTime }}</td>
        <td>{{ since .BeginTime }}</td>
        <td>{{ if .Deadline }}{{ .Deadline }}{{ end }}</td>
      </tr>
      {{- end }}
    </table>
  </body>
</html>`
