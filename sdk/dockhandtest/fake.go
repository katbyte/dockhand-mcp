// Package dockhandtest is a canned Dockhand for tests: it answers the API calls a test tells it to and records what it was asked. Every request it
// gets is also held against the definitions sdk/dockhand is generated from - the call must be one the API has, and every query parameter sent must be
// one that call takes - so a test that only meant to check an answer also proves the request was one the real server takes.
//
// The live suites are what prove the canned answers match a real server.
package dockhandtest

import (
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"sync"
	"testing"

	"github.com/katbyte/dockhand-mcp/sdk/dockhand"
	"github.com/katbyte/dockhand-mcp/sdk/pandorest/workarounds"
	"github.com/katbyte/pandorest/config"
	"github.com/katbyte/pandorest/definitions"
)

// Token is the API token the canned server expects, in the shape Dockhand mints them.
const Token = "dh_0000000000000000000000000000000000000000000"

// contentJSON is what the API's own answers are sent as.
const contentJSON = "application/json"

// Request is one call the canned server was sent.
type Request struct {
	Method string
	// Path is the call's path as sent: /api/containers/web/logs
	Path  string
	Query url.Values
	// Accept is what the call said it would take for an answer: JSON alone, or anything for a call asked as a job
	Accept string
	// Body is what was sent with it, as it came
	Body string
}

// Server is a canned Dockhand.
type Server struct {
	// URL is the address to give a client
	URL string

	tb   testing.TB
	defs *definitions.Service
	// open is a Dockhand with its login switched off, which is sent no token
	open bool

	mu      sync.Mutex
	answers map[string]answer
	then    map[string]func(Request)
	seen    []Request
}

// answer is what a call is answered with.
type answer struct {
	status      int
	contentType string
	body        string
}

// loaded reads the definitions once for every canned server in a test binary.
var loaded = sync.OnceValues(func() (*definitions.Service, error) {
	_, here, _, ok := runtime.Caller(0)
	if !ok {
		return nil, errors.New("where this package is on disk could not be told")
	}

	svc, _ := config.Find(workarounds.Services, workarounds.Service)
	svc, err := svc.In(filepath.Join(filepath.Dir(here), "..", "..")).Resolve()
	if err != nil {
		return nil, err
	}

	return definitions.Load(svc.Path(svc.Definitions))
})

// New starts a canned Dockhand with its login on, closed when the test ends. It answers nothing until told to (Answer, Fail): a call with no answer
// fails the test.
func New(tb testing.TB) *Server {
	tb.Helper()

	defs, err := loaded()
	if err != nil {
		tb.Fatalf("reading the API definitions: %v", err)
	}
	s := &Server{tb: tb, defs: defs, answers: map[string]answer{}}
	srv := httptest.NewServer(http.HandlerFunc(s.serve))
	tb.Cleanup(srv.Close)
	s.URL = srv.URL

	return s
}

// NewOpen starts a canned Dockhand with its login switched off: it expects no token, and fails the test when sent one.
func NewOpen(tb testing.TB) *Server {
	tb.Helper()

	s := New(tb)
	s.open = true

	return s
}

// Client is a client for the canned server, made the way its login wants.
func (s *Server) Client() *dockhand.Client {
	s.tb.Helper()

	token := Token
	if s.open {
		token = ""
	}
	c, err := dockhand.Connect(s.URL, token)
	if err != nil {
		s.tb.Fatal(err)
	}

	return c
}

// Answer makes a call - a method and a path, "GET /api/environments" - answer 200 with the JSON given.
func (s *Server) Answer(call, body string) {
	s.set(call, answer{status: http.StatusOK, contentType: contentJSON, body: body})
}

// AnswerMade is Answer for a call that answers 201, as the ones that make something and say so by their status do.
func (s *Server) AnswerMade(call, body string) {
	s.set(call, answer{status: http.StatusCreated, contentType: contentJSON, body: body})
}

// AnswerWhen is Answer for a call sent a query parameter with a value: one environment's containers for that environment, where Answer is the same
// containers whichever is asked for. A call that matches no such answer falls back to the call's own.
func (s *Server) AnswerWhen(call, param, value, body string) {
	s.set(call+"\x00"+param+"="+value, answer{status: http.StatusOK, contentType: contentJSON, body: body})
}

// AnswerText makes a call answer 200 with something that is not JSON: a stream of events, a file.
func (s *Server) AnswerText(call, contentType, body string) {
	s.set(call, answer{status: http.StatusOK, contentType: contentType, body: body})
}

// AnswerTextWhen is AnswerText for a call sent a query parameter with a value: the archive of one path inside a container, where another path has
// another.
func (s *Server) AnswerTextWhen(call, param, value, contentType, body string) {
	s.set(call+"\x00"+param+"="+value, answer{status: http.StatusOK, contentType: contentType, body: body})
}

// Fail makes a call answer as Dockhand does when it refuses one: the status, and its reason as {"error": ...}.
func (s *Server) Fail(call string, status int, message string) {
	s.set(call, answer{status: status, contentType: contentJSON, body: fmt.Sprintf(`{"error":%q}`, message)})
}

// FailWhen is Fail for a call sent a query parameter with a value.
func (s *Server) FailWhen(call, param, value string, status int, message string) {
	s.set(call+"\x00"+param+"="+value, answer{status: status, contentType: contentJSON, body: fmt.Sprintf(`{"error":%q}`, message)})
}

// Unauthorized makes every call answer as a Dockhand with its login on does to a token it does not take, whatever was set for it.
func (s *Server) Unauthorized() {
	s.set("*", answer{status: http.StatusUnauthorized, contentType: contentJSON, body: `{"error":"Unauthorized","message":"Authentication required"}`})
}

func (s *Server) set(key string, a answer) {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.answers[key] = a
}

// Then has the canned server do something when a call is made, before it answers: change what another call answers from then on, which is how a write
// comes to be read back. A nil do is nothing more to be done.
func (s *Server) Then(call string, do func(Request)) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.then == nil {
		s.then = map[string]func(Request){}
	}
	s.then[call] = do
}

// Requests are the calls made of one kind, in order.
func (s *Server) Requests(call string) []Request {
	s.mu.Lock()
	defer s.mu.Unlock()

	var out []Request
	for _, r := range s.seen {
		if r.Method+" "+r.Path == call {
			out = append(out, r)
		}
	}

	return out
}

// Last is the most recent call made of one kind; it fails the test when there was none.
func (s *Server) Last(call string) Request {
	s.tb.Helper()

	made := s.Requests(call)
	if len(made) == 0 {
		s.tb.Fatalf("%s was never called", call)
	}

	return made[len(made)-1]
}

// Calls is every call made, in order, as "METHOD /path".
func (s *Server) Calls() []string {
	s.mu.Lock()
	defer s.mu.Unlock()

	out := make([]string, 0, len(s.seen))
	for _, r := range s.seen {
		out = append(out, r.Method+" "+r.Path)
	}

	return out
}

func (s *Server) serve(w http.ResponseWriter, r *http.Request) {
	body, err := io.ReadAll(r.Body)
	if err != nil {
		s.tb.Errorf("%s %s: its body could not be read: %v", r.Method, r.URL.Path, err)
	}
	req := Request{Method: r.Method, Path: r.URL.Path, Query: r.URL.Query(), Accept: r.Header.Get("Accept"), Body: string(body)}
	call := req.Method + " " + req.Path
	s.check(r, req)

	s.mu.Lock()
	do := s.then[call]
	s.mu.Unlock()
	if do != nil {
		do(req)
	}

	s.mu.Lock()
	s.seen = append(s.seen, req)
	a, answered := s.answers[call]
	// an answer for what this call was sent comes before the call's own; when two parameters each have one, the first by name does, so a test gets
	// the same answer every run
	names := make([]string, 0, len(req.Query))
	for param := range req.Query {
		names = append(names, param)
	}
	slices.Sort(names)
	for _, param := range slices.Backward(names) {
		for _, value := range req.Query[param] {
			if specific, ok := s.answers[call+"\x00"+param+"="+value]; ok {
				a, answered = specific, true
			}
		}
	}
	if everything, ok := s.answers["*"]; ok {
		a, answered = everything, true
	}
	s.mu.Unlock()

	if !answered {
		s.tb.Errorf("the client called %s, which this test gave the canned server no answer for", call)
		http.NotFound(w, r)

		return
	}
	w.Header().Set("Content-Type", a.contentType)
	w.WriteHeader(a.status)
	_, _ = w.Write([]byte(a.body))
}

// check holds a request against the API's definitions and the rules a client keeps.
func (s *Server) check(r *http.Request, req Request) {
	call := req.Method + " " + req.Path

	got := r.Header.Get("Authorization")
	switch {
	case s.open && got != "":
		s.tb.Errorf("%s: a Dockhand with its login off is sent no token, and Authorization was set", call)
	case !s.open && got != "Bearer "+Token:
		s.tb.Errorf("%s: the token is sent as a bearer header, and Authorization was %q", call, got)
	default:
	}
	if strings.Contains(r.URL.RawQuery, Token) {
		s.tb.Errorf("%s: the token was sent in the address, which puts it in logs", call)
	}

	op := s.operation(req.Method, req.Path)
	if op == nil {
		s.tb.Errorf("the client called %s, which the API definitions (Dockhand %s) do not have", call, s.defs.APIVersion)

		return
	}
	takes := make([]string, 0, len(op.Options))
	for i := range op.Options {
		if op.Options[i].In == definitions.InQuery {
			takes = append(takes, op.Options[i].Name)
		}
	}
	for name := range req.Query {
		if !slices.Contains(takes, name) {
			s.tb.Errorf("%s was sent the query parameter %q, which the API definitions do not give it (it takes %s)", call, name, strings.Join(takes, ", "))
		}
	}
}

// operation is the API operation a method and a path as sent belong to: the one whose path matches segment for segment, a placeholder standing for
// any one segment, and the one with the fewest placeholders when several do.
func (s *Server) operation(method, path string) *definitions.Operation {
	sent := strings.Split(strings.Trim(path, "/"), "/")

	var (
		found *definitions.Operation
		fewer = -1
	)
	for _, op := range s.defs.Operations() {
		if op.Method != method {
			continue
		}
		template := strings.Split(strings.Trim(op.Path, "/"), "/")
		if len(template) != len(sent) {
			continue
		}

		placeholders, matches := 0, true
		for i, segment := range template {
			switch {
			case strings.HasPrefix(segment, "{") && strings.HasSuffix(segment, "}"):
				placeholders++
			case segment != sent[i]:
				matches = false
			default:
			}
		}
		if matches && (fewer < 0 || placeholders < fewer) {
			found, fewer = op, placeholders
		}
	}

	return found
}
