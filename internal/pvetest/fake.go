// Package pvetest is a shared test helper that stands up a fake PVE
// HTTP server for unit tests across cmd/pmox and internal packages.
// Tests register per-route responders; every request is recorded so
// tests can assert the sequence of calls and their bodies.
package pvetest

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/eugenetaranov/pmox/internal/pveclient"
)

// Hit is one recorded HTTP request.
type Hit struct {
	Method string
	Path   string
	Body   string
}

// Responder handles a matched request. body is the request body read
// into a string (already consumed from r.Body).
type Responder func(w http.ResponseWriter, r *http.Request, body string)

// Server is a stateful fake PVE server.
type Server struct {
	t   *testing.T
	srv *httptest.Server

	mu       sync.Mutex
	hits     []Hit
	handlers []routeHandler
	fallback Responder
}

type routeHandler struct {
	method string // empty matches any method
	match  string // path substring
	fn     Responder
}

// New creates a running httptest.Server. It is closed automatically via
// t.Cleanup. By default, an unhandled request fails the test and returns
// 404; call SetFallback to customize.
func New(t *testing.T) *Server {
	t.Helper()
	s := &Server{t: t}
	s.srv = httptest.NewServer(http.HandlerFunc(s.serve))
	t.Cleanup(s.srv.Close)
	return s
}

// URL returns the base URL of the fake server.
func (s *Server) URL() string { return s.srv.URL }

// Client returns a pveclient wired to this server. The client uses the
// httptest server's TLS-aware http.Client so in-test requests bypass
// certificate verification.
func (s *Server) Client() *pveclient.Client {
	c := pveclient.New(s.srv.URL, "t@pam!x", "secret", false)
	c.HTTPClient = s.srv.Client()
	return c
}

// Handle registers a responder for any request whose method matches and
// whose path contains the given substring. Handlers are matched in the
// order they were registered — first match wins.
func (s *Server) Handle(method, pathContains string, fn Responder) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.handlers = append(s.handlers, routeHandler{method: method, match: pathContains, fn: fn})
}

// SetFallback registers a responder invoked when no Handle rule matches.
func (s *Server) SetFallback(fn Responder) {
	s.mu.Lock()
	s.fallback = fn
	s.mu.Unlock()
}

// Hits returns a snapshot of every recorded request, in order.
func (s *Server) Hits() []Hit {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]Hit, len(s.hits))
	copy(out, s.hits)
	return out
}

// Count returns the number of recorded hits whose method matches (empty
// means any) and whose path contains pathContains.
func (s *Server) Count(method, pathContains string) int {
	s.mu.Lock()
	defer s.mu.Unlock()
	n := 0
	for _, h := range s.hits {
		if method != "" && h.Method != method {
			continue
		}
		if !strings.Contains(h.Path, pathContains) {
			continue
		}
		n++
	}
	return n
}

// Bodies returns every recorded body whose method+path matches.
func (s *Server) Bodies(method, pathContains string) []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	var out []string
	for _, h := range s.hits {
		if method != "" && h.Method != method {
			continue
		}
		if !strings.Contains(h.Path, pathContains) {
			continue
		}
		out = append(out, h.Body)
	}
	return out
}

// OrderedPaths returns a list of "METHOD PATH" strings for every hit,
// in order, for sequence assertions.
func (s *Server) OrderedPaths() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]string, 0, len(s.hits))
	for _, h := range s.hits {
		out = append(out, h.Method+" "+h.Path)
	}
	return out
}

func (s *Server) serve(w http.ResponseWriter, r *http.Request) {
	body, _ := io.ReadAll(r.Body)
	bs := string(body)
	s.mu.Lock()
	s.hits = append(s.hits, Hit{Method: r.Method, Path: r.URL.Path, Body: bs})
	handlers := make([]routeHandler, len(s.handlers))
	copy(handlers, s.handlers)
	fallback := s.fallback
	s.mu.Unlock()

	for _, h := range handlers {
		if h.method != "" && h.method != r.Method {
			continue
		}
		if !strings.Contains(r.URL.Path, h.match) {
			continue
		}
		h.fn(w, r, bs)
		return
	}
	if fallback != nil {
		fallback(w, r, bs)
		return
	}
	s.t.Errorf("pvetest: unhandled %s %s", r.Method, r.URL.Path)
	http.NotFound(w, r)
}

// --- Convenience responders -------------------------------------------------

// TaskOK is a Responder that returns a terminal OK task-status payload.
// Use it on `/tasks/...` handlers so WaitTask polls immediately succeed.
func TaskOK(w http.ResponseWriter, _ *http.Request, _ string) {
	_, _ = io.WriteString(w, `{"data":{"status":"stopped","exitstatus":"OK"}}`)
}

// JSON is a Responder that writes the given literal JSON body. Use it
// for canned {"data":...} responses.
func JSON(body string) Responder {
	return func(w http.ResponseWriter, _ *http.Request, _ string) {
		_, _ = io.WriteString(w, body)
	}
}
