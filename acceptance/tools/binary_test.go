//go:build acceptance

// The binary, not the package. Everything else in this suite drives tools.RegisterAll in process, which is every line of tool code the binary runs;
// what it never touches is the thin layer around it - stdio framing, the flags and environment reaching the server, the HTTP routes, and what a bad
// start says. Those break silently: a stray line on stdout corrupts the protocol and a client simply fails to connect, with every other test green.
//
// So this is a smoke test: the real binary, built from this checkout, spoken to the way a client speaks to it, against the same Dockhand.
package tools_test

import (
	"bytes"
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/katbyte/dockhand-mcp/lib/testenv"
	"github.com/katbyte/go-kt/mcp/acctest"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

var (
	binOnce   sync.Once
	binPath   string
	binDir    string
	errBinary error
)

// removeBinary deletes the binary the smoke test built, at the end of the run: it is built once for the suite, so no one test can clean it up.
func removeBinary() {
	if binDir != "" {
		_ = os.RemoveAll(binDir)
	}
}

// builtBinary builds dockhand-mcp from this checkout, once per run, so the test speaks to the code in the tree rather than to whatever is installed.
func builtBinary(t *testing.T) string {
	t.Helper()

	if !suite.Ready {
		t.Skip(suite.NotReady)
	}
	binOnce.Do(func() {
		// not t.TempDir: the binary is built once and outlives the test that built it
		binDir, errBinary = os.MkdirTemp("", "dockhand-mcp-binary") //nolint:usetesting // removed by removeBinary at the end of the run
		if errBinary != nil {
			return
		}
		binPath = filepath.Join(binDir, "dockhand-mcp")
		args := []string{"build", "-o", binPath}
		if coverDir() != "" {
			// the main package too: without it the binary registers no exit hook and writes no counters at all
			args = append(args, "-cover", "-coverpkg=.,./tools/...,./lib/...,./cli/...,./sdk/...")
		}
		build := exec.CommandContext(context.Background(), "go", append(args, ".")...) //nolint:gosec // G204: go build, with the arguments the test wrote
		build.Dir = "../.."
		out, err := build.CombinedOutput()
		if err != nil {
			errBinary = fmt.Errorf("building dockhand-mcp: %w\n%s", err, out)
		}
	})
	if errBinary != nil {
		t.Fatal(errBinary)
	}

	return binPath
}

// bareEnv is an environment with nothing of ours in it: a stray DOCKHAND_* in the shell running the tests cannot change what is registered, and HOME
// is an empty directory, so a real ~/.dockhand-mcp cannot either.
func bareEnv(t *testing.T, extra ...string) []string {
	t.Helper()

	return append([]string{"PATH=" + os.Getenv("PATH"), "HOME=" + t.TempDir()}, extra...)
}

// serverEnv is the environment a client would give the binary: the Dockhand under test, with the token this run was given when its login is on and
// none when it is off.
func serverEnv(t *testing.T, extra ...string) []string {
	t.Helper()

	env := bareEnv(t, "DOCKHAND_SERVER="+world.Server)
	if world.Token != "" {
		env = append(env, "DOCKHAND_TOKEN="+world.Token)
	}

	return append(env, extra...)
}

// syncBuffer collects output a process writes while a test reads it.
type syncBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (b *syncBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()

	return b.buf.Write(p)
}

func (b *syncBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()

	return b.buf.String()
}

// teeReadCloser is a reader that also closes the process's stdout.
type teeReadCloser struct {
	io.Reader
	io.Closer
}

// stdio starts "dockhand-mcp serve" as a client does, connects an MCP session over its stdin and stdout, and keeps everything the process wrote to
// each stream so a test can assert on them. dir is the working directory, for the config file lookup.
func stdio(t *testing.T, dir string, env []string, args ...string) (session *mcp.ClientSession, wrote, wroteErr *syncBuffer) {
	t.Helper()

	ctx := context.WithoutCancel(t.Context())
	cmd := exec.CommandContext(ctx, builtBinary(t), append([]string{"serve"}, args...)...) //nolint:gosec // G204: the binary this test built itself, run with the arguments the test wrote
	cmd.Env = withCover(env)
	cmd.Dir = dir
	stdin, err := cmd.StdinPipe()
	if err != nil {
		t.Fatal(err)
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	out, errOut := &syncBuffer{}, &syncBuffer{}
	cmd.Stderr = errOut
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}

	session, err = mcp.NewClient(&mcp.Implementation{Name: "binary-test", Version: "0"}, nil).Connect(ctx, &mcp.IOTransport{Reader: teeReadCloser{Reader: io.TeeReader(stdout, out), Closer: stdout}, Writer: stdin}, nil)
	if err != nil {
		_ = cmd.Process.Kill()
		t.Fatalf("connecting to the binary over stdio: %v\nstderr: %s", err, errOut)
	}
	t.Cleanup(func() {
		_ = session.Close()
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
	})

	return session, out, errOut
}

// toolNamesOf lists the tools a session's server registered.
func toolNamesOf(t *testing.T, session *mcp.ClientSession) []string {
	t.Helper()

	res, err := session.ListTools(t.Context(), nil)
	if err != nil {
		t.Fatal(err)
	}
	listed := make([]string, 0, len(res.Tools))
	for _, tool := range res.Tools {
		listed = append(listed, tool.Name)
	}
	slices.Sort(listed)

	return listed
}

// through calls a tool that reads over the session and returns what it answered.
func through(t *testing.T, session *mcp.ClientSession, tool string) map[string]any {
	t.Helper()

	res, err := session.CallTool(t.Context(), &mcp.CallToolParams{Name: tool})
	if err != nil {
		t.Fatal(err)
	}
	if res.IsError {
		t.Fatalf("%s through the binary: %v", tool, res.Content)
	}
	out, isObject := res.StructuredContent.(map[string]any)
	if !isObject {
		t.Fatalf("%s through the binary answered %v", tool, res.StructuredContent)
	}

	return out
}

// hostsThrough is the Docker hosts the binary's Dockhand manages, by name, which proves the server's address, and its token when it wants one,
// reached the client the binary built: with its login on a Dockhand answers this to nobody else.
func hostsThrough(t *testing.T, session *mcp.ClientSession) []string {
	t.Helper()

	rows := acctest.Rows(t, through(t, session, "env_list")["environments"], "environments")
	hosts := make([]string, 0, len(rows))
	for _, row := range rows {
		hosts = append(hosts, acctest.Str(row["env"]))
	}
	slices.Sort(hosts)

	return hosts
}

// bothHosts is what hostsThrough answers of the Dockhand under test.
var bothHosts = []string{testenv.Alpha, testenv.Beta}

// MCP over stdio uses stdout for the protocol: one line of anything else - a print, a log line, a cobra message - corrupts the stream, and the client
// fails to connect with every test still green. So the binary is run at the most verbose log level and every line it writes to stdout is checked.
//

func TestBinaryStdioSpeaksOnlyTheProtocol(t *testing.T) {
	session, out, errOut := stdio(t, t.TempDir(), serverEnv(t, "DOCKHAND_LOG=trace"), "--toolsets", "all")

	if info := through(t, session, "server_info"); info["url"] != world.Server || info["version"] == nil || acctest.BoolOf(info["login"]) != (world.Stage == testenv.StageLogin) || acctest.NumOr0(info["environments"]) != len(bothHosts) {
		t.Errorf("the server through the binary = %v", info)
	}
	if hosts := hostsThrough(t, session); !slices.Equal(hosts, bothHosts) {
		t.Errorf("the hosts through the binary = %v, want %v", hosts, bothHosts)
	}
	if err := session.Close(); err != nil {
		t.Errorf("closing the session: %v", err)
	}

	for i, line := range strings.Split(strings.TrimSpace(out.String()), "\n") {
		if strings.TrimSpace(line) == "" {
			continue
		}
		var msg struct {
			JSONRPC string `json:"jsonrpc"`
		}
		if err := json.Unmarshal([]byte(line), &msg); err != nil || msg.JSONRPC != "2.0" {
			t.Fatalf("stdout line %d is not a protocol message, which breaks the stream: %q", i+1, line)
		}
	}
	// the logging that must not be on stdout does happen, on stderr: the start, and the traffic at trace - with no token in it
	logged := errOut.String()
	if !strings.Contains(logged, "registered") || !strings.Contains(logged, "/api/environments") {
		t.Errorf("stderr has no startup line and no traffic at trace level: %q", logged)
	}
	if world.Token != "" && (strings.Contains(logged, world.Token) || strings.Contains(out.String(), world.Token)) {
		t.Error("the API token is in what the binary wrote")
	}
}

// The flags and the environment have to reach the running server, not just parse: what a client lists is what was asked for.
//

func TestBinaryRegistersWhatTheFlagsAsk(t *testing.T) {
	for _, c := range []struct {
		name     string
		env      []string
		args     []string
		want     []string // tools that must be registered
		unwanted []string // tools that must not be
	}{
		{name: "the default set is core", want: []string{"server_info", "env_list", "container_list", "container_get", "container_logs", "stack_list", "stack_logs", "job_get"}, unwanted: []string{"container_restart", "env_test", "env_create", "image_list", "job_cancel"}},
		{name: "a toolset from the environment", env: []string{"DOCKHAND_TOOLSETS=setup"}, want: []string{"env_test", "env_list"}, unwanted: []string{"env_create", "env_delete", "container_restart"}},
		{name: "nothing writes unless it is said to", args: []string{"--toolsets", "all"}, want: []string{"env_test", "container_logs", "stack_list"}, unwanted: []string{"container_start", "container_stop", "container_restart", "stack_restart", "env_create", "env_delete"}},
		{name: "--read-only says the same out loud", args: []string{"--toolsets", "all", "--read-only"}, want: []string{"env_test"}, unwanted: []string{"container_restart", "env_create", "env_delete"}},
		{name: "--enable-write registers what changes and nothing that deletes", args: []string{"--toolsets", "all", "--enable-write"}, want: []string{"container_start", "container_stop", "container_restart", "stack_start", "stack_stop", "stack_restart", "env_create"}, unwanted: []string{"env_delete", "image_delete", "image_prune", "volume_delete", "network_prune"}},
		{name: "a toolset is its tools and core's, and nothing of another's", args: []string{"--toolsets", "resources", "--enable-write"}, want: []string{"image_list", "image_pull", "volume_list", "volume_files", "network_list", "network_connect", "job_get", "server_info"}, unwanted: []string{"image_delete", "volume_prune", "network_delete", "job_cancel", "container_restart", "env_test"}},
		{name: "and one kind of what it holds is a family", args: []string{"--toolsets", "volume"}, want: []string{"volume_list", "volume_get", "server_info"}, unwanted: []string{"volume_files", "volume_create", "image_list", "network_list"}},
		{name: "both from the environment register what deletes too", env: []string{"DOCKHAND_TOOLSETS=all", "DOCKHAND_ENABLE_WRITE=true", "DOCKHAND_ENABLE_DELETE=true"}, want: []string{"env_delete", "env_create", "container_restart"}},
		{name: "a family is a toolset of its own", args: []string{"--toolsets", "stack", "--enable-write"}, want: []string{"stack_restart", "stack_stop", "server_info"}, unwanted: []string{"container_restart", "env_create"}},
		{name: "--allow-tools alone is only the tools it names", args: []string{"--allow-tools", "stack_*", "--enable-write"}, want: []string{"stack_list", "stack_logs", "stack_restart"}, unwanted: []string{"server_info", "container_list", "env_list"}},
		{name: "--allow-tools beside a toolset adds to it", args: []string{"--toolsets", "core", "--allow-tools", "env_test"}, want: []string{"env_test", "server_info", "container_list"}, unwanted: []string{"container_restart", "env_create"}},
		{name: "and gets nothing past the write switch", args: []string{"--toolsets", "core", "--allow-tools", "container_restart"}, want: []string{"server_info"}, unwanted: []string{"container_restart"}},
		{name: "nor past the delete switch", args: []string{"--toolsets", "core", "--allow-tools", "env_delete", "--enable-write"}, want: []string{"server_info"}, unwanted: []string{"env_delete"}},
		{name: "--deny-tools removes from it", args: []string{"--toolsets", "manage", "--enable-write", "--deny-tools", "stack_*"}, want: []string{"container_restart", "container_list"}, unwanted: []string{"stack_list", "stack_restart", "stack_logs"}},
	} {
		t.Run(c.name, func(t *testing.T) {
			// an empty working directory: only the flags and the environment under test decide what is registered
			session, _, _ := stdio(t, t.TempDir(), serverEnv(t, c.env...), c.args...)
			listed := toolNamesOf(t, session)
			for _, want := range c.want {
				if !slices.Contains(listed, want) {
					t.Errorf("%s is not registered, of the %d that are", want, len(listed))
				}
			}
			for _, unwanted := range c.unwanted {
				if slices.Contains(listed, unwanted) {
					t.Errorf("%s is registered, of the %d that are", unwanted, len(listed))
				}
			}
		})
	}
}

// A .dockhand-mcp in the working directory is how a project configures the binary without putting a token in .mcp.json, and it is read before
// $HOME's.
//

func TestBinaryReadsTheConfigFileInTheWorkingDirectory(t *testing.T) {
	dir := t.TempDir()
	config := "server=" + world.Server + "\ntoolsets=setup\nenable_write=true\n"
	if world.Token != "" {
		config += "token=" + world.Token + "\n"
	}
	if err := os.WriteFile(filepath.Join(dir, ".dockhand-mcp"), []byte(config), 0o600); err != nil {
		t.Fatal(err)
	}

	// nothing in the environment: the file is all it has
	session, _, _ := stdio(t, dir, bareEnv(t))

	// the toolset, and a setting of two words spelled the way the environment spells it
	if listed := toolNamesOf(t, session); !slices.Contains(listed, "env_test") || !slices.Contains(listed, "env_create") || slices.Contains(listed, "env_delete") || slices.Contains(listed, "container_restart") {
		t.Errorf("the toolset and the write switch in the config file were not used: %v", listed)
	}
	if hosts := hostsThrough(t, session); !slices.Equal(hosts, bothHosts) {
		t.Errorf("the server and token in the config file did not reach the client: %v", hosts)
	}
}

// --listen has to serve the protocol at /mcp behind the bearer check, answer the health probe a container watches, stay up, and shut down when asked.
//

func TestBinaryServesHTTP(t *testing.T) {
	const token = "zzyzx-binary-token" //nolint:gosec // G101: a made-up value for the throwaway server, which is no credential of anything real

	ctx := context.WithoutCancel(t.Context())
	addr := freePort(t)
	cmd := exec.CommandContext(ctx, builtBinary(t), "serve", "--listen", addr, "--auth-token", token, "--toolsets", "all") //nolint:gosec // G204: the binary this test built itself, run with the arguments the test wrote
	cmd.Env = withCover(serverEnv(t))
	errOut := &syncBuffer{}
	cmd.Stderr, cmd.Stdout = errOut, errOut
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	exited := make(chan error, 1)
	go func() { exited <- cmd.Wait() }()
	t.Cleanup(func() { _ = cmd.Process.Kill() })

	base := "http://" + addr
	waitHealthy(t, base, exited, errOut)

	// get answers the status and the challenge, if any
	get := func(path, auth string) (status int, challenge string) {
		t.Helper()
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, base+path, http.NoBody)
		if err != nil {
			t.Fatal(err)
		}
		if auth != "" {
			req.Header.Set("Authorization", auth)
		}
		res, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		if err := res.Body.Close(); err != nil {
			t.Fatal(err)
		}

		return res.StatusCode, res.Header.Get("WWW-Authenticate")
	}

	t.Run("the endpoint is behind the bearer check", func(t *testing.T) {
		if status, challenge := get("/mcp", ""); status != http.StatusUnauthorized {
			t.Errorf("GET /mcp with no token = %d, want 401", status)
		} else if !strings.Contains(challenge, "Bearer") {
			t.Errorf("no Bearer challenge: %q", challenge)
		}
		if status, _ := get("/mcp", "Bearer nope"); status != http.StatusUnauthorized {
			t.Errorf("GET /mcp with the wrong token = %d, want 401", status)
		}
		// the Dockhand's own token is not the one this endpoint takes
		if status, _ := get("/mcp", "Bearer "+world.Token); status != http.StatusUnauthorized {
			t.Errorf("GET /mcp with the Dockhand's token = %d, want 401", status)
		}
		if status, _ := get("/nope", "Bearer "+token); status != http.StatusNotFound {
			t.Errorf("GET /nope = %d, want 404", status)
		}
	})

	// the session stays open through the shutdown below, as a client that is still connected when the container is stopped would
	var session *mcp.ClientSession
	t.Run("and serves the protocol with it", func(t *testing.T) {
		var err error
		session, err = mcp.NewClient(&mcp.Implementation{Name: "binary-test", Version: "0"}, nil).Connect(ctx, &mcp.StreamableClientTransport{Endpoint: base + "/mcp", HTTPClient: &http.Client{Transport: bearer(token)}}, nil)
		if err != nil {
			t.Fatalf("connecting over HTTP: %v\n%s", err, errOut)
		}
		t.Cleanup(func() { _ = session.Close() })

		if listed := toolNamesOf(t, session); !slices.Contains(listed, "env_test") { //nolint:contextcheck // the helper asks with the test's own context, which outlives this one's deadline for starting the binary
			t.Errorf("tools over HTTP = %v", listed)
		}
		if hosts := hostsThrough(t, session); !slices.Equal(hosts, bothHosts) { //nolint:contextcheck // as above
			t.Errorf("the hosts over HTTP = %v, want %v", hosts, bothHosts)
		}
	})

	t.Run("and shuts down when asked, with a client still connected", func(t *testing.T) {
		if session == nil {
			t.Skip("no session to hold open")
		}
		asked := time.Now()
		if err := cmd.Process.Signal(syscall.SIGTERM); err != nil {
			t.Fatal(err)
		}
		select {
		case err := <-exited:
			if err != nil {
				t.Errorf("exit after SIGTERM: %v\n%s", err, errOut)
			}
			// a container gets ten seconds to stop before it is killed
			if took := time.Since(asked); took > 5*time.Second {
				t.Errorf("took %s to stop with a client connected", took.Round(time.Millisecond))
			}
		case <-time.After(shutdownGrace):
			t.Errorf("still running %s after SIGTERM", shutdownGrace)
		}
	})
}

// coverDir is where a coverage run (make cover) writes its counters, or "" outside one. The binary is then built with coverage on and writes beside
// the suite, so what the process ran counts too: the serve path is only ever run by these tests.
func coverDir() string {
	if f := flag.Lookup("test.gocoverdir"); f != nil {
		return f.Value.String()
	}

	return ""
}

// withCover points a binary at the coverage directory during a coverage run.
func withCover(env []string) []string {
	if dir := coverDir(); dir != "" {
		return append(env, "GOCOVERDIR="+dir)
	}

	return env
}

// shutdownGrace is how long the binary gets to drain and exit.
const shutdownGrace = 15 * time.Second

// bearer adds the HTTP endpoint's token to every request.
func bearer(token string) http.RoundTripper {
	return roundTripper(func(r *http.Request) (*http.Response, error) {
		r.Header.Set("Authorization", "Bearer "+token)

		return http.DefaultTransport.RoundTrip(r)
	})
}

type roundTripper func(*http.Request) (*http.Response, error)

func (f roundTripper) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

// freePort is an address nothing is listening on, for --listen.
func freePort(t *testing.T) string {
	t.Helper()

	l, err := (&net.ListenConfig{}).Listen(t.Context(), "tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	if err := l.Close(); err != nil {
		t.Fatal(err)
	}

	return l.Addr().String()
}

// waitHealthy polls the health probe until the server answers it, failing with whatever the process said if it exits first.
func waitHealthy(t *testing.T, base string, exited <-chan error, out *syncBuffer) {
	t.Helper()

	for range 100 {
		select {
		case err := <-exited:
			t.Fatalf("the server exited before serving: %v\n%s", err, out)
		default:
		}
		req, err := http.NewRequestWithContext(t.Context(), http.MethodGet, base+"/healthz", http.NoBody)
		if err != nil {
			t.Fatal(err)
		}
		if res, err := http.DefaultClient.Do(req); err == nil {
			body, _ := io.ReadAll(res.Body)
			_ = res.Body.Close()
			if res.StatusCode == http.StatusOK && strings.TrimSpace(string(body)) == "ok" {
				return
			}
		}
		time.Sleep(100 * time.Millisecond)
	}
	t.Fatalf("no health response from the server\n%s", out)
}

// A start that cannot work has to say so and stop. A client waiting on stdio sees nothing at all otherwise, and a container restarts in a loop.
//

func TestBinaryRefusesAStartItCannotMake(t *testing.T) {
	const password = "not-a-real-password"

	for _, c := range []struct {
		name string
		env  []string
		args []string
		says string
	}{
		{name: "no server", env: bareEnv(t), says: "no server is configured"},
		{name: "a server with no address in it", env: bareEnv(t, "DOCKHAND_SERVER=dockhand"), says: "must be the address"},
		{name: "a server with a password in it", env: bareEnv(t, "DOCKHAND_SERVER=http://admin:"+password+"@dockhand:3000"), says: "must not contain credentials"},
		{name: "--listen with no auth token", args: []string{"--listen", "127.0.0.1:0"}, says: "--auth-token"},
		{name: "a toolset that does not exist", args: []string{"--toolsets", "zzyzx"}, says: "zzyzx"},
		{name: "an allow pattern matching nothing", args: []string{"--allow-tools", "zzyzx_*"}, says: "zzyzx_*"},
		{name: "deleting with no leave to change", args: []string{"--enable-delete"}, says: "--enable-delete needs --enable-write"},
		{name: "reading only and changing at once", args: []string{"--read-only", "--enable-write"}, says: "contradict each other"},
		{name: "a flag there is none of", args: []string{"--zzyzx"}, says: "zzyzx"},
	} {
		t.Run(c.name, func(t *testing.T) {
			env := c.env
			if env == nil {
				env = serverEnv(t)
			}
			cmd := exec.CommandContext(context.WithoutCancel(t.Context()), builtBinary(t), append([]string{"serve"}, c.args...)...) //nolint:gosec // G204: the binary this test built itself, run with the arguments the test wrote
			cmd.Env = withCover(env)
			cmd.Dir = t.TempDir() // no .dockhand-mcp to fall back on
			out := &syncBuffer{}
			cmd.Stdout, cmd.Stderr = out, out

			done := make(chan error, 1)
			if err := cmd.Start(); err != nil {
				t.Fatal(err)
			}
			go func() { done <- cmd.Wait() }()

			select {
			case err := <-done:
				if err == nil {
					t.Errorf("exited 0, want a refusal: %s", out)
				}
			case <-time.After(shutdownGrace):
				_ = cmd.Process.Kill()
				t.Fatalf("still running: a bad start has to fail, not hang\n%s", out)
			}
			if got := out.String(); !strings.Contains(got, c.says) {
				t.Errorf("said %q, want it to name %q", got, c.says)
			}
			if got := out.String(); strings.Contains(got, password) || (world.Token != "" && strings.Contains(got, world.Token)) {
				t.Errorf("the refusal repeats a password or the API token: %q", got)
			}
		})
	}
}

// The Dockhand is asked nothing at start, so a token that is missing or wrong is not a start the binary refuses: it is what the first tool says, in
// words a person can act on and with no token in them. A Dockhand with its login off takes no token, and is not put off by being sent one.
//

func TestBinarySaysWhatIsWrongWithItsToken(t *testing.T) {
	const wrong = "dh_zzyzxzzyzxzzyzxzzyzxzzyzxzzyzxzzyzxzzyzxzzy"

	// refusal is what server_info says through a binary started with env
	refusal := func(t *testing.T, env []string) string {
		t.Helper()

		session, out, errOut := stdio(t, t.TempDir(), env, "--toolsets", "all")
		res, err := session.CallTool(t.Context(), &mcp.CallToolParams{Name: "server_info"})
		if err != nil {
			t.Fatal(err)
		}
		if !res.IsError {
			t.Fatalf("server_info answered %v, want a refusal", res.StructuredContent)
		}
		var said strings.Builder
		for _, content := range res.Content {
			if text, isText := content.(*mcp.TextContent); isText {
				said.WriteString(text.Text)
			}
		}
		if strings.Contains(said.String()+out.String()+errOut.String(), wrong) {
			t.Error("the token that was refused is repeated in what the binary wrote")
		}

		return said.String()
	}

	if world.Stage == testenv.StageOpen {
		session, _, _ := stdio(t, t.TempDir(), bareEnv(t, "DOCKHAND_SERVER="+world.Server, "DOCKHAND_TOKEN="+wrong))
		if hosts := hostsThrough(t, session); !slices.Equal(hosts, bothHosts) {
			t.Errorf("a Dockhand with no login, sent a token it has no use for, lists %v", hosts)
		}

		return
	}

	if said := refusal(t, bareEnv(t, "DOCKHAND_SERVER="+world.Server)); !strings.Contains(said, "has its login switched on and was sent no API token") || !strings.Contains(said, "Profile, API tokens") {
		t.Errorf("with no token the binary says %q", said)
	}
	if said := refusal(t, bareEnv(t, "DOCKHAND_SERVER="+world.Server, "DOCKHAND_TOKEN="+wrong)); !strings.Contains(said, "the API token was refused: it is wrong, expired or revoked") {
		t.Errorf("with a token the Dockhand does not know the binary says %q", said)
	}
}

// The commands a person runs to check an install, rather than a client.
//

func TestBinaryCommands(t *testing.T) {
	run := func(t *testing.T, args ...string) string {
		t.Helper()
		cmd := exec.CommandContext(context.WithoutCancel(t.Context()), builtBinary(t), args...) //nolint:gosec // G204: the binary this test built itself, run with the arguments the test wrote
		cmd.Env = withCover(serverEnv(t))
		cmd.Dir = t.TempDir()
		out, err := cmd.Output()
		if err != nil {
			t.Fatalf("%v: %v", args, err)
		}

		return string(out)
	}

	if got := run(t, "version"); !strings.HasPrefix(got, "dockhand-mcp ") {
		t.Errorf("version = %q", got)
	}
	// tools needs no server, and says what it would register
	if got := run(t, "tools", "-q", "--toolsets", "all", "--enable-write", "--enable-delete"); !strings.Contains(got, "env_delete\n") || len(strings.Fields(got)) != len(suite.ToolNames(t)) {
		t.Errorf("tools -q listed %d tools, and the server registers %d", len(strings.Fields(got)), len(suite.ToolNames(t)))
	}
	login := map[testenv.Stage]string{testenv.StageOpen: "login off", testenv.StageLogin: "login on"}[world.Stage]
	if got := run(t, "info"); !strings.Contains(got, "Dockhand ") || !strings.Contains(got, " at "+world.Server+", "+login+", 2 environments") || !strings.Contains(got, testenv.Alpha+" (") || !strings.Contains(got, testenv.Beta+" (") || (world.Token != "" && strings.Contains(got, world.Token)) {
		t.Errorf("info = %q", got)
	}
}
