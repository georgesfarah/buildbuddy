package rpcz

import (
	"context"
	"fmt"
	"html/template"
	"log"
	"net/http"
	"slices"
	"strings"
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
	Deadline  *time.Duration
	BeginTime time.Time
	Duration  time.Duration
	Messages  []string
	Parent    *rpcStats
}

// contextKey is a private type for storing values in context
type contextKey struct{}

// statsHandler tracks in-flight RPCs and implements stats.Handler
type statsHandler struct {
	mu             sync.RWMutex
	active         map[string]*rpcStats
	completed      [10]*rpcStats
	completedIdx   int
	completedCount int
}

// Split active and completed RPCs
// Keep the first N active, then sample after that
// Keep all active, N completed, and N errored
// Once an RPC is completed, it can't be modified any more
// Allow adding messages
// Keep track of parents for client RPCs

func (h *statsHandler) TagRPC(ctx context.Context, info *stats.RPCTagInfo) context.Context {
	rs := &rpcStats{
		ID:     uuid.New().String(),
		Method: info.FullMethodName,
	}
	if parent, ok := ctx.Value(contextKey{}).(*rpcStats); ok {
		rs.Parent = parent
	}
	if p, ok := peer.FromContext(ctx); ok {
		rs.PeerAddr = p.Addr.String()
	}
	return context.WithValue(ctx, contextKey{}, rs)
}

func (h *statsHandler) HandleRPC(ctx context.Context, stat stats.RPCStats) {
	val := ctx.Value(contextKey{})
	rs, ok := val.(*rpcStats)
	if !ok {
		return
	}
	switch s := stat.(type) {
	case *stats.Begin:
		h.mu.Lock()
		rs.BeginTime = s.BeginTime
		if dl, ok := ctx.Deadline(); ok {
			dur := dl.Sub(s.BeginTime)
			rs.Deadline = &dur
		}
		if rs.Parent != nil && rs.Duration == 0 {
			rs.Parent.Messages = append(rs.Parent.Messages, fmt.Sprintf("Started client call %v at %v", rs.Method, rs.BeginTime.Sub(rs.Parent.BeginTime)))
		}
		h.active[rs.ID] = rs
		h.mu.Unlock()
	case *stats.InPayload:
		h.mu.Lock()
		rs.Messages = append(rs.Messages, fmt.Sprintf("Received %v bytes at %v", s.Length, s.RecvTime.Sub(rs.BeginTime)))
		// rs.Messages = append(rs.Messages, fmt.Sprintf("Received %v", s.Payload))
		h.mu.Unlock()
	case *stats.InHeader:
		h.mu.Lock()
		rs.Messages = append(rs.Messages, fmt.Sprintf("Received header at %v", time.Since(rs.BeginTime)))
		h.mu.Unlock()
	case *stats.InTrailer:
		h.mu.Lock()
		rs.Messages = append(rs.Messages, fmt.Sprintf("Received trailer at %v", time.Since(rs.BeginTime)))
		h.mu.Unlock()
	case *stats.OutPayload:
		h.mu.Lock()
		rs.Messages = append(rs.Messages, fmt.Sprintf("Sent %v bytes at %v", s.Length, s.SentTime.Sub(rs.BeginTime)))
		// rs.Messages = append(rs.Messages, fmt.Sprintf("Sent %v", s.Payload))
		h.mu.Unlock()
	case *stats.OutHeader:
		h.mu.Lock()
		rs.Messages = append(rs.Messages, fmt.Sprintf("Sent header at %v", time.Since(rs.BeginTime)))
		h.mu.Unlock()
	case *stats.OutTrailer:
		h.mu.Lock()
		rs.Messages = append(rs.Messages, fmt.Sprintf("Received trailer at %v", time.Since(rs.BeginTime)))
		h.mu.Unlock()
	case *stats.End:
		h.mu.Lock()
		if rs.Parent != nil && rs.Duration == 0 {
			rs.Parent.Messages = append(rs.Parent.Messages, fmt.Sprintf("Finished client call %v at %v", rs.Method, s.EndTime.Sub(rs.Parent.BeginTime)))
		}
		rs.Duration = s.EndTime.Sub(rs.BeginTime)
		rs.Messages = append(rs.Messages, fmt.Sprintf("Received response %v at %v", s.Error, rs.Duration))
		h.completed[h.completedIdx] = rs
		h.completedIdx = (h.completedIdx + 1) % len(h.completed)
		h.completedCount = min(h.completedCount+1, len(h.completed))
		delete(h.active, rs.ID)
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
		server: &statsHandler{active: make(map[string]*rpcStats)},
		client: &statsHandler{active: make(map[string]*rpcStats)},
	}
}

func (h *Handler) ServerStatsHandler() stats.Handler {
	return h.server
}

func (h *Handler) ClientStatsHandler() stats.Handler {
	return h.client
}

func (z *Handler) Serve(handle func(pattern string, handler http.Handler)) {
	tmplIdx := template.Must(
		template.New("index").Parse(indexHTML),
	)
	tmplDet := template.Must(
		template.New("detail").Parse(detailHTML),
	)

	handle("/rpcz", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		type entry struct {
			Method, Direction string
			Active, Completed int
		}
		type activeAndCompleted struct{ active, completed int }
		serverCounts := make(map[string]activeAndCompleted)

		z.server.mu.RLock()
		for _, s := range z.server.active {
			c := serverCounts[s.Method]
			c.active++
			serverCounts[s.Method] = c
		}
		for _, s := range z.server.completed[:z.server.completedCount] {
			c := serverCounts[s.Method]
			c.completed++
			serverCounts[s.Method] = c
		}
		z.server.mu.RUnlock()

		clientCounts := make(map[string]activeAndCompleted)
		z.client.mu.RLock()
		for _, s := range z.client.active {
			c := clientCounts[s.Method]
			c.active++
			clientCounts[s.Method] = c
		}
		for _, s := range z.client.completed[:z.client.completedCount] {
			c := clientCounts[s.Method]
			c.completed++
			clientCounts[s.Method] = c
		}
		z.client.mu.RUnlock()

		entries := []entry{}
		for method, m := range serverCounts {
			entries = append(entries, entry{method, "incoming", m.active, m.completed})
		}
		for method, m := range clientCounts {
			entries = append(entries, entry{method, "outgoing", m.active, m.completed})
		}

		slices.SortFunc(entries, func(l, r entry) int {
			if l.Direction != r.Direction {
				return strings.Compare(l.Direction, r.Direction)
			}
			return strings.Compare(l.Method, r.Method)
		})

		if err := tmplIdx.Execute(w, entries); err != nil {
			log.Printf("index template error: %v", err)
		}
	}))

	handle("/rpcz/method", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		name := r.URL.Query().Get("name")
		dir := r.URL.Query().Get("dir")
		completed := len(r.URL.Query().Get("completed")) > 0

		var entries []*rpcStats
		stats := z.client
		if dir == "incoming" {
			stats = z.server
		}

		stats.mu.RLock()
		if completed {
			for _, s := range stats.completed[:stats.completedCount] {
				if s.Method == name {
					entries = append(entries, s)
				}
			}
		} else {
			now := time.Now()
			for _, s := range stats.active {
				if s.Method == name {
					s.Duration = now.Sub(s.BeginTime)
					entries = append(entries, s)
				}
			}
		}
		stats.mu.RUnlock()

		slices.SortFunc(entries, func(l, r *rpcStats) int {
			return l.BeginTime.Compare(r.BeginTime)
		})

		data := struct {
			Method, Dir string
			Completed   bool
			Entries     []*rpcStats
		}{name, dir, completed, entries}

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
        <th>Active</th>
		<th>Completed</th>
      </tr>
{{- range . }}
      <tr>
        <td>{{ .Method }}</td>
        <td>{{ .Direction }}</td>
        <td><a href="/rpcz/method?name={{ .Method }}&dir={{ .Direction }}">{{ .Active }}</a></td>
		<td><a href="/rpcz/method?name={{ .Method }}&dir={{ .Direction }}&completed=1">{{ .Completed }}</a></td>
      </tr>
{{- end }}
    </table>
  </body>
</html>`

const detailHTML = `<!DOCTYPE html>
<html>
  <head>
    <meta charset="UTF-8">
    <title>RPC Details for {{ .Method }} ({{ .Dir }}) ({{ if .Completed }}Completed{{ else }}Active{{ end }})</title>
  </head>
  <body>
    <h1>{{ .Method }} [{{ .Dir }}] [{{ if .Completed }}Completed{{ else }}Active{{ end }}]</h1>
    <table border="1">
      <tr>
        <th>ID</th>
        <th>Peer</th>
        <th>Started</th>
        <th>Duration</th>
        <th>Deadline</th>
        <th>Messages</th>
      </tr>
{{- range .Entries }}
      <tr>
        <td>{{ .ID }}</td>
        <td>{{ .PeerAddr }}</td>
        <td>{{ .BeginTime }}</td>
        <td>{{ .Duration }}</td>
        <td>{{ if .Deadline }}{{ .Deadline }}{{ end }}</td>
        <td>
	{{- range .Messages }}
		{{ . }}
		 </br>
	{{- end }}
		</td>
      </tr>
{{- end }}
    </table>
  </body>
</html>`
