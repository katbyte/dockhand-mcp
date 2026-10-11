package cli

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/katbyte/dockhand-mcp/tools"
	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
)

// viper is global, so none of these can run in parallel and each resets it.
const (
	testServer = "http://dockhand:3000"
	testUse    = "dockhand-mcp"
	// on is how a boolean is switched on from the environment or a file
	on = "true"
	// a pattern for tools
	envGlob = "env_*"
)

// load drives the real flag wiring against a temporary home and working directory, with command-line arguments parsed as well.
func load(t *testing.T, home, wd string, args ...string) *FlagData {
	t.Helper()

	isolate(t)
	t.Setenv("HOME", home)
	t.Chdir(wd)

	root := &cobra.Command{Use: testUse}
	if err := configureFlags(root); err != nil {
		t.Fatalf("configureFlags: %v", err)
	}
	if err := root.ParseFlags(args); err != nil {
		t.Fatalf("ParseFlags: %v", err)
	}

	return GetFlags()
}

func write(t *testing.T, dir, body string) {
	t.Helper()

	if err := os.WriteFile(filepath.Join(dir, configName), []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
}

// ~/.dockhand-mcp holds the global settings and a project's own file changes the ones it names: the two are merged key by key, so a project file that
// sets one thing keeps the server and token from home.
//
//nolint:paralleltest // viper is global state; these mutate it
func TestConfigFiles(t *testing.T) {
	home, project := t.TempDir(), t.TempDir()
	write(t, home, "SERVER="+testServer+"\nTOKEN=from-home\nTOOLSETS=all\n")

	t.Run("home alone", func(t *testing.T) {
		f := load(t, home, t.TempDir())
		if f.Server != testServer || f.Token != "from-home" || !slices.Equal(f.Toolsets, []string{"all"}) {
			t.Errorf("flags = %+v", f)
		}
	})

	t.Run("a project file changes what it names", func(t *testing.T) {
		write(t, project, "TOOLSETS=core\nREAD_ONLY=true\n")
		f := load(t, home, project)
		if !slices.Equal(f.Toolsets, []string{"core"}) || !f.ReadOnly {
			t.Errorf("the project's settings were not read: %+v", f)
		}
		if f.Server != testServer || f.Token != "from-home" {
			t.Errorf("the project file lost the server and token from home: %+v", f)
		}
	})

	t.Run("no config at all is not an error", func(t *testing.T) {
		if f := load(t, t.TempDir(), t.TempDir()); f.Server != "" || f.Token != "" || f.ReadOnly {
			t.Errorf("flags = %+v, want nothing set", f)
		}
	})

	t.Run("home is the working directory", func(t *testing.T) {
		if f := load(t, home, home); f.Server != testServer {
			t.Errorf("one file read as both = %+v", f)
		}
	})
}

// A flag beats the environment, and the environment a config file, so a container can override what is baked into an image.
func TestPrecedence(t *testing.T) {
	home := t.TempDir()
	write(t, home, "SERVER=http://from-file:3000\nTOKEN=from-file\nLISTEN=:1\n")

	f := load(t, home, t.TempDir())
	if f.Server != "http://from-file:3000" || f.Listen != ":1" {
		t.Errorf("from the file alone = %+v", f)
	}

	isolate(t)
	t.Setenv("DOCKHAND_SERVER", "http://from-env:3000")
	t.Setenv("HOME", home)
	t.Chdir(t.TempDir())
	root := &cobra.Command{Use: testUse}
	if err := configureFlags(root); err != nil {
		t.Fatal(err)
	}
	f = GetFlags()
	if f.Server != "http://from-env:3000" {
		t.Errorf("server = %v, want the environment's", f.Server)
	}
	// and a key the environment did not set still comes from the file
	if f.Token != "from-file" {
		t.Errorf("token = %v, want the config file's", f.Token)
	}

	if err := root.ParseFlags([]string{flagServer, "http://from-flag:3000"}); err != nil {
		t.Fatal(err)
	}
	if f = GetFlags(); f.Server != "http://from-flag:3000" {
		t.Errorf("server = %v, want the flag's", f.Server)
	}
}

// A config file names a setting the way the environment does, with or without the prefix: READ_ONLY or DOCKHAND_READ_ONLY for --read-only. viper
// matches a file's keys against the flags' own spelling, so every two-word setting in a file would be ignored without a word.
func TestConfigFileTwoWordKeys(t *testing.T) {
	for name, body := range map[string]string{
		"without the prefix": "SERVER=" + testServer + "\nENABLE_WRITE=true\nENABLE_DELETE=true\nALLOW_TOOLS=env_*,server_info\nDENY_TOOLS=*_info\nAUTH_TOKEN=s3cret\nALLOW_NO_AUTH=true\n",
		"with the prefix":    "DOCKHAND_SERVER=" + testServer + "\nDOCKHAND_ENABLE_WRITE=true\nDOCKHAND_ENABLE_DELETE=true\nDOCKHAND_ALLOW_TOOLS=env_*,server_info\nDOCKHAND_DENY_TOOLS=*_info\nDOCKHAND_AUTH_TOKEN=s3cret\nDOCKHAND_ALLOW_NO_AUTH=true\n",
	} {
		home := t.TempDir()
		write(t, home, body)
		f := load(t, home, t.TempDir())
		if f.Server != testServer || !f.EnableWrite || !f.EnableDelete || !f.AllowNoAuth || f.AuthToken != "s3cret" || !slices.Equal(f.AllowTools, []string{envGlob, "server_info"}) || !slices.Equal(f.DenyTools, []string{"*_info"}) {
			t.Errorf("%s: flags = %+v", name, f)
		}
	}

	// and each still loses to the environment
	home := t.TempDir()
	write(t, home, "AUTH_TOKEN=from-file\n")
	isolate(t)
	t.Setenv("DOCKHAND_AUTH_TOKEN", "from-env")
	t.Setenv("DOCKHAND_READ_ONLY", on)
	t.Setenv("HOME", home)
	t.Chdir(t.TempDir())
	if err := configureFlags(&cobra.Command{Use: testUse}); err != nil {
		t.Fatal(err)
	}
	if f := GetFlags(); f.AuthToken != "from-env" || !f.ReadOnly {
		t.Errorf("flags = %+v, want the environment's", f)
	}
}

// Every flag has its environment variable, named for it with the tool's prefix, so a setting can be given either way and neither is forgotten.
//
//nolint:paralleltest // viper is global state; these mutate it
func TestEnvironmentBindings(t *testing.T) {
	isolate(t)

	root := &cobra.Command{Use: testUse}
	if err := configureFlags(root); err != nil {
		t.Fatal(err)
	}

	bound := 0
	root.PersistentFlags().VisitAll(func(flag *pflag.Flag) {
		bound++
		want := "DOCKHAND_" + strings.ToUpper(strings.ReplaceAll(flag.Name, "-", "_"))
		if envNames[flag.Name] != want {
			t.Errorf("--%s is bound to %q, want %s", flag.Name, envNames[flag.Name], want)
		}
	})
	if bound != len(envNames) {
		t.Errorf("%d flags and %d environment variables: each flag has one and no variable is left over", bound, len(envNames))
	}
}

// The client is made with the token when one is given and with none when none is, and an address that will not do is refused before anything is asked
// of it.
//
//nolint:paralleltest // viper is global state; these mutate it
func TestNewClient(t *testing.T) {
	if _, err := load(t, t.TempDir(), t.TempDir()).NewClient(); err == nil || !strings.Contains(err.Error(), "no server is configured") || !strings.Contains(err.Error(), "DOCKHAND_SERVER") {
		t.Errorf("no server = %v", err)
	}

	dock, err := load(t, t.TempDir(), t.TempDir(), flagServer, testServer+"/").NewClient()
	if err != nil || dock.Client.BaseURL != testServer {
		t.Errorf("a server and no token = %v, %v: a Dockhand with its login off takes none", dock, err)
	}

	dock, err = load(t, t.TempDir(), t.TempDir(), flagServer, testServer, flagToken, " a-token ").NewClient()
	if err != nil || dock.Client.BaseURL != testServer {
		t.Errorf("a server and a token = %v, %v", dock, err)
	}

	if _, err := load(t, t.TempDir(), t.TempDir(), flagServer, "ftp://dockhand").NewClient(); err == nil || !strings.Contains(err.Error(), "must be the address") {
		t.Errorf("an address that is none = %v", err)
	}
}

// options is the tool options the flags given come to; it fails the test when they are refused.
func options(t *testing.T, args ...string) tools.Options {
	t.Helper()

	opts, err := load(t, t.TempDir(), t.TempDir(), args...).ToolOptions()
	if err != nil {
		t.Fatalf("%v: %v", args, err)
	}

	return opts
}

// The binary registers core unless told otherwise, with nothing that changes anything, and an allow list given with no toolsets chooses from every
// tool.
//
//nolint:paralleltest // viper is global state; these mutate it
func TestToolOptions(t *testing.T) {
	opts := options(t)
	if !slices.Equal(opts.Toolsets, DefaultToolsets) || opts.EnableWrite || opts.EnableDelete || len(opts.Allow) != 0 {
		t.Errorf("with nothing asked for = %+v", opts)
	}

	opts = options(t, "--allow-tools", envGlob)
	if len(opts.Toolsets) != 0 || !slices.Equal(opts.Allow, []string{envGlob}) {
		t.Errorf("an allow list alone = %+v, want it to choose from every tool", opts)
	}

	opts = options(t, "--toolsets", "env,all", "--allow-tools", envGlob, "--deny-tools", "*_info", "--enable-write", "--enable-delete")
	if !slices.Equal(opts.Toolsets, []string{"env", "all"}) || !slices.Equal(opts.Allow, []string{envGlob}) || !slices.Equal(opts.Deny, []string{"*_info"}) || !opts.EnableWrite || !opts.EnableDelete {
		t.Errorf("everything asked for = %+v", opts)
	}
}

// A session only reads unless writes are enabled. --read-only is taken, as the other servers are started with it, and says what is already so; beside
// --enable-write it is a contradiction.
func TestTheWriteSwitch(t *testing.T) {
	if opts := options(t, "--read-only"); opts.EnableWrite || opts.EnableDelete {
		t.Errorf("--read-only = %+v", opts)
	}
	if opts := options(t, "--enable-write"); !opts.EnableWrite || opts.EnableDelete {
		t.Errorf("--enable-write = %+v, want writes and no deletes", opts)
	}

	if _, err := load(t, t.TempDir(), t.TempDir(), "--read-only", "--enable-write").ToolOptions(); err == nil || !strings.Contains(err.Error(), "contradict") {
		t.Errorf("--read-only beside --enable-write = %v", err)
	}

	// from the environment and from a file, as every setting
	isolate(t)
	t.Setenv("DOCKHAND_ENABLE_WRITE", on)
	if err := configureFlags(&cobra.Command{Use: testUse}); err != nil {
		t.Fatal(err)
	}
	if f := GetFlags(); !f.EnableWrite {
		t.Error("DOCKHAND_ENABLE_WRITE=true did not enable writes")
	}
	home := t.TempDir()
	write(t, home, "ENABLE_WRITE=true\n")
	if f := load(t, home, t.TempDir()); !f.EnableWrite {
		t.Error("ENABLE_WRITE=true in the config file did not enable writes")
	}
}
