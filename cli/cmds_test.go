package cli

import (
	"bytes"
	"fmt"
	"net/http"
	"slices"
	"strings"
	"testing"

	"github.com/katbyte/dockhand-mcp/sdk/dockhandtest"
	"github.com/katbyte/dockhand-mcp/tools"
	"github.com/spf13/viper"
)

// viper is global, so none of these can run in parallel.
const (
	coreTool   = "env_list"
	cmdServe   = "serve"
	flagServer = "--server"
	flagToken  = "--token"
)

// isolate keeps a developer's own settings out of a run: with a real server configured in ~/.dockhand-mcp or the environment, info would connect to
// it.
func isolate(t *testing.T) {
	t.Helper()

	viper.Reset()
	t.Cleanup(viper.Reset)
	t.Setenv("HOME", t.TempDir())
	t.Chdir(t.TempDir())
	for _, env := range envNames {
		t.Setenv(env, "")
	}
}

// run executes the command tree the binary builds and returns what it printed.
func run(t *testing.T, args ...string) (string, error) {
	t.Helper()

	isolate(t)

	root, err := Make()
	if err != nil {
		t.Fatalf("Make: %v", err)
	}
	var out bytes.Buffer
	root.SetOut(&out)
	root.SetErr(&out)
	root.SetArgs(args)
	err = root.Execute()

	return out.String(), err
}

//nolint:paralleltest // viper is global state; Make mutates it
func TestMakeBuildsTheCommands(t *testing.T) {
	out, err := run(t, "--help")
	if err != nil {
		t.Fatalf("--help: %v", err)
	}
	for _, want := range []string{cmdServe, "info", "version", "tools"} {
		if !strings.Contains(out, want) {
			t.Errorf("%q is missing from --help:\n%s", want, out)
		}
	}

	out, err = run(t)
	if err != nil || !strings.Contains(out, "dockhand-mcp help") {
		t.Errorf("bare invocation = %q, %v", out, err)
	}
}

//nolint:paralleltest // viper is global state; Make mutates it
func TestVersionCommand(t *testing.T) {
	out, err := run(t, "version")
	if err != nil || !strings.HasPrefix(out, "dockhand-mcp ") {
		t.Errorf("version = %q, %v", out, err)
	}
}

// A command that talks to the Dockhand refuses to start without one, before any connection is attempted, and says what is missing; and one that could
// only serve what it should not is refused as well.
//
//nolint:paralleltest // viper is global state; Make mutates it
func TestCommandsNeedAServer(t *testing.T) {
	for name, tc := range map[string]struct {
		args []string
		want string
	}{
		"info with nothing":              {[]string{"info"}, "no server is configured"},
		"serve with nothing":             {[]string{cmdServe}, "no server is configured"},
		"serve with no address in it":    {[]string{cmdServe, flagServer, "dockhand"}, "must be the address"},
		"serve with a password in it":    {[]string{cmdServe, flagServer, "http://admin:hunter2@dockhand:3000"}, "must not contain credentials"},
		"serve over http with no bearer": {[]string{cmdServe, flagServer, testServer, "--listen", "127.0.0.1:0"}, "DOCKHAND_AUTH_TOKEN"},
		"serve with an unknown toolset":  {[]string{cmdServe, flagServer, testServer, "--toolsets", "nope"}, `unknown toolset "nope"`},
		"serve deleting without writing": {[]string{cmdServe, flagServer, testServer, "--enable-delete"}, "--enable-delete needs --enable-write"},
		"serve read-only and writing":    {[]string{cmdServe, flagServer, testServer, "--read-only", "--enable-write"}, "contradict"},
	} {
		_, err := run(t, tc.args...)
		if err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Errorf("%s = %v, want an error saying %q", name, err, tc.want)
		}
		if err != nil && strings.Contains(err.Error(), "hunter2") {
			t.Errorf("%s repeats the password given: %v", name, err)
		}
	}
}

// info asks the Dockhand what it is and which hosts it manages, with a token when one is given and with none when none is.
//
//nolint:paralleltest // viper is global state; Make mutates it
func TestInfoCommand(t *testing.T) {
	const (
		environments = `[{"id":1,"name":"alpha","connectionType":"socket"},{"id":2,"name":"beta","connectionType":"hawser-standard","host":"agent-beta","port":2376}]`
		changelog    = `[{"version":"1.0.51","date":"2026-10-03","changes":[]}]`
	)

	dock := dockhandtest.New(t)
	dock.Answer("GET /api/auth/session", `{"authenticated":false,"authEnabled":true}`)
	dock.Answer("GET /api/environments", environments)
	dock.Answer("GET /api/changelog", changelog)

	out, err := run(t, "info", flagServer, dock.URL, flagToken, dockhandtest.Token)
	if err != nil {
		t.Fatalf("info: %v\n%s", err, out)
	}
	for _, want := range []string{"Dockhand 1.0.51 at " + dock.URL + ", login on, 2 environments", "alpha (1): socket", "beta (2): hawser-standard"} {
		if !strings.Contains(out, want) {
			t.Errorf("%q missing from info:\n%s", want, out)
		}
	}
	if strings.Contains(out, dockhandtest.Token) {
		t.Errorf("info printed the token:\n%s", out)
	}

	// a Dockhand with its login off is asked with no token at all
	open := dockhandtest.NewOpen(t)
	open.Answer("GET /api/auth/session", `{"authenticated":false,"authEnabled":false}`)
	open.Answer("GET /api/environments", `[]`)
	open.Answer("GET /api/changelog", changelog)
	out, err = run(t, "info", flagServer, open.URL)
	if err != nil || !strings.Contains(out, "login off, 0 environments") {
		t.Errorf("info of a Dockhand with no login = %q, %v", out, err)
	}

	// one that wants a token and is given none says so, with its login said to be on
	dock.Fail("GET /api/environments", http.StatusUnauthorized, "Unauthorized")
	_, err = run(t, "info", flagServer, dock.URL, flagToken, dockhandtest.Token)
	if err == nil || !strings.Contains(err.Error(), "with its login on") || !strings.Contains(err.Error(), "the API token was refused") {
		t.Errorf("info with the token refused = %v", err)
	}

	// and one that is not there is told from one that answers
	dock.Fail("GET /api/auth/session", http.StatusInternalServerError, "Internal Error")
	_, err = run(t, "info", flagServer, dock.URL, flagToken, dockhandtest.Token)
	if err == nil || !strings.Contains(err.Error(), "could not be asked") {
		t.Errorf("info of a Dockhand that does not answer = %v", err)
	}
}

// `dockhand-mcp tools` needs no server, so it has to work with nothing configured.
//
//nolint:paralleltest // viper is global state; Make mutates it
func TestToolsCommand(t *testing.T) {
	out, err := run(t, "tools")
	if err != nil {
		t.Fatalf("tools: %v", err)
	}

	// the default is core and the audits, so their tools are listed, every one a read, and counted
	core, audits := tools.Toolsets["core"], tools.Toolsets["audit"]
	listed := len(core) + len(audits)
	for _, want := range slices.Concat([]string{fmt.Sprintf("core (%d)", len(core)), fmt.Sprintf("audit (%d)", len(audits)), fmt.Sprintf("%d tools: %d read, 0 write, 0 delete", listed, listed)}, core, audits) {
		if !strings.Contains(out, want) {
			t.Errorf("%q missing from `tools`:\n%s", want, out)
		}
	}
	// and it says how to get more
	for _, want := range []string{"toolsets: all, ", "families: ", "--toolsets all", "only tools that read are listed; --enable-write registers the ones that change something", "--enable-delete", "DOCKHAND_TOOLSETS"} {
		if !strings.Contains(out, want) {
			t.Errorf("%q missing from the footer:\n%s", want, out)
		}
	}
	for line := range strings.SplitSeq(out, "\n") {
		if strings.HasPrefix(line, "families: ") && (!strings.Contains(line, "server") || !strings.Contains(line, "env")) {
			t.Errorf("the families listed = %q", line)
		}
	}
	// one line a tool: its name, its kind and its first sentence
	for line := range strings.SplitSeq(out, "\n") {
		if strings.Contains(line, coreTool) && (!strings.Contains(line, " read ") || strings.Count(line, ". ") > 0) {
			t.Errorf("the line for %s is not its name, kind and one sentence: %q", coreTool, line)
		}
	}
}

//nolint:paralleltest // viper is global state; Make mutates it
func TestToolsCommandSelections(t *testing.T) {
	out, err := run(t, "tools", "--toolsets", "env", "-q")
	if err != nil {
		t.Fatalf("tools --toolsets env: %v", err)
	}
	// -q prints names only: the family asked for, and core beside it
	listed := strings.Fields(out)
	for _, name := range listed {
		if !strings.HasPrefix(name, "env_") && !slices.Contains(tools.Toolsets["core"], name) {
			t.Errorf("tools --toolsets env -q lists %s, which is neither an env tool nor core's", name)
		}
	}
	if !slices.Contains(listed, coreTool) || !slices.Contains(listed, "server_info") || strings.Contains(out, " read ") {
		t.Errorf("tools --toolsets env -q = %q", out)
	}

	// an allow list with no toolsets chooses from every tool
	out, err = run(t, "tools", "--allow-tools", "env_*,server_info", "--deny-tools", "*_info,*_test,*_get,*_disk", "-q")
	if err != nil || strings.TrimSpace(out) != coreTool {
		t.Errorf("tools --allow-tools env_*,server_info --deny-tools *_info,*_test,*_get,*_disk -q = %q, %v", out, err)
	}

	// beside a toolset it adds what it names to the set, and core comes with the set
	out, err = run(t, "tools", "--toolsets", "setup", "--allow-tools", "stack_restart", "--enable-write", "-q")
	if listed = strings.Fields(out); err != nil || !slices.Contains(listed, "stack_restart") || !slices.Contains(listed, "env_create") || !slices.Contains(listed, coreTool) || slices.Contains(listed, "stack_stop") || slices.Contains(listed, "env_delete") {
		t.Errorf("tools --toolsets setup --allow-tools stack_restart --enable-write -q = %q, %v", out, err)
	}

	// what the listing says is held back follows what was enabled
	out, err = run(t, "tools", "--toolsets", "all", "--enable-write")
	if err != nil || !strings.Contains(out, "delete tools are hidden; --enable-delete registers them") || strings.Contains(out, "only tools that read") {
		t.Errorf("with --enable-write: %v\n%s", err, out)
	}
	out, err = run(t, "tools", "--toolsets", "all", "--enable-write", "--enable-delete")
	if err != nil || strings.Contains(out, "hidden") || strings.Contains(out, "only tools that read") {
		t.Errorf("with everything enabled the listing still says something is held back: %v\n%s", err, out)
	}
	// a delete is a change, and --read-only is the opposite of --enable-write
	if _, err = run(t, "tools", "--enable-delete"); err == nil || !strings.Contains(err.Error(), "--enable-delete needs --enable-write") {
		t.Errorf("tools --enable-delete alone = %v", err)
	}
	if _, err = run(t, "tools", "--read-only", "--enable-write"); err == nil || !strings.Contains(err.Error(), "contradict") {
		t.Errorf("tools --read-only --enable-write = %v", err)
	}

	if _, err = run(t, "tools", "--toolsets", "nope"); err == nil || !strings.Contains(err.Error(), "core") || !strings.Contains(err.Error(), "env") {
		t.Errorf("an unknown toolset = %v, want an error naming the sets and families", err)
	}
}

func TestFirstSentence(t *testing.T) {
	t.Parallel()

	for _, c := range []struct{ in, want string }{
		{"One thing. And then another.", "One thing."},
		{"No full stop at all", "No full stop at all"},
		{"Trailing full stop.", "Trailing full stop."},
		{"A kind, e.g. socket. Then more.", "A kind, e.g. socket."},
		{"Ends in etc. and goes on", "Ends in etc. and goes on"},
		{"", ""},
	} {
		if got := firstSentence(c.in); got != c.want {
			t.Errorf("firstSentence(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}
