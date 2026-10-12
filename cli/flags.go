package cli

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/katbyte/dockhand-mcp/sdk/dockhand"
	"github.com/katbyte/dockhand-mcp/tools"
	"github.com/katbyte/go-kt/clog"
	"github.com/katbyte/pandorest/client"
	"github.com/spf13/cobra"
	"github.com/spf13/viper"
)

type FlagData struct {
	Server       string   `mapstructure:"server"`
	Token        string   `mapstructure:"token"`
	ReadOnly     bool     `mapstructure:"read-only"`
	EnableWrite  bool     `mapstructure:"enable-write"`
	EnableDelete bool     `mapstructure:"enable-delete"`
	Toolsets     []string `mapstructure:"toolsets"`
	AllowTools   []string `mapstructure:"allow-tools"`
	DenyTools    []string `mapstructure:"deny-tools"`
	Listen       string   `mapstructure:"listen"`
	AuthToken    string   `mapstructure:"auth-token"`
	AllowNoAuth  bool     `mapstructure:"allow-no-auth"`
}

// configName is the settings file read from $HOME and from the working directory.
const configName = ".dockhand-mcp"

// envNames binds each flag to its environment variable.
var envNames = map[string]string{ //nolint:gosec // G101: these are env var names, not credentials
	"server":        "DOCKHAND_SERVER",
	"token":         "DOCKHAND_TOKEN",
	"read-only":     "DOCKHAND_READ_ONLY",
	"enable-write":  "DOCKHAND_ENABLE_WRITE",
	"enable-delete": "DOCKHAND_ENABLE_DELETE",
	"toolsets":      "DOCKHAND_TOOLSETS",
	"allow-tools":   "DOCKHAND_ALLOW_TOOLS",
	"deny-tools":    "DOCKHAND_DENY_TOOLS",
	"listen":        "DOCKHAND_LISTEN",
	"auth-token":    "DOCKHAND_AUTH_TOKEN",
	"allow-no-auth": "DOCKHAND_ALLOW_NO_AUTH",
}

func configureFlags(root *cobra.Command) error {
	pflags := root.PersistentFlags()

	pflags.StringP("server", "s", "", "the address the Dockhand web console answers on, e.g. http://dockhand:3000")
	pflags.StringP("token", "t", "", "a Dockhand API token (consider exporting to DOCKHAND_TOKEN instead); leave it out for a Dockhand with its login switched off, which takes none")
	pflags.Bool("enable-write", false, "register the tools that change something; without it every tool only reads")
	pflags.Bool("enable-delete", false, "register the tools that delete as well; needs --enable-write")
	pflags.Bool("read-only", false, "register only tools that never change anything: the default, said out loud, and refused beside --enable-write")
	pflags.StringSlice("toolsets", nil, "groups of tools to register: all, "+strings.Join(tools.ToolsetOrder, ", ")+" (core is the default and always included), or a resource family like env")
	pflags.StringSlice("allow-tools", nil, "tools to register by name, prefix globs like env_*, or the essential preset: added to the --toolsets given, or with no --toolsets the only tools registered")
	pflags.StringSlice("deny-tools", nil, "never register these tools: names or prefix globs like *_delete; it is how a toolset is narrowed")
	pflags.String("listen", "", "serve MCP over HTTP on this address (e.g. :8080) instead of stdio")
	pflags.String("auth-token", "", "bearer token required on the HTTP endpoint (consider exporting to DOCKHAND_AUTH_TOKEN instead)")
	pflags.Bool("allow-no-auth", false, "serve HTTP with no bearer token: anyone who can reach the port can use every tool")

	for name, env := range envNames {
		if err := viper.BindPFlag(name, pflags.Lookup(name)); err != nil {
			return fmt.Errorf("error binding '%s' flag: %w", name, err)
		}

		if err := viper.BindEnv(name, env); err != nil {
			return fmt.Errorf("error binding '%s' to env '%s' : %w", name, env, err)
		}
	}

	readConfigFiles()

	// a config file spells a setting the way the environment does, with or without the prefix - READ_ONLY or DOCKHAND_READ_ONLY for --read-only - and
	// viper only matches a key spelled as the flag is, so every two-word setting would be ignored. Carried across as defaults, they still lose to a
	// flag or the environment.
	for name, env := range envNames {
		for _, alt := range []string{strings.ReplaceAll(name, "-", "_"), strings.ToLower(env)} {
			if alt != name && viper.InConfig(alt) {
				viper.SetDefault(name, viper.Get(alt))

				break
			}
		}
	}

	return nil
}

// readConfigFiles reads ~/.dockhand-mcp and then ./.dockhand-mcp over it, key by key: the home file holds the global settings and a project's file
// changes the ones it names. viper on its own reads only the first file it finds, so a project file that set one thing would lose the server and
// token from home.
func readConfigFiles() {
	viper.SetConfigType("env")

	var files []string
	if home, err := os.UserHomeDir(); err == nil {
		files = append(files, filepath.Join(home, configName))
	}
	if wd, err := os.Getwd(); err == nil && (len(files) == 0 || filepath.Join(wd, configName) != files[0]) {
		files = append(files, filepath.Join(wd, configName))
	}

	read := false
	for _, f := range files {
		if _, err := os.Stat(f); err != nil {
			continue
		}
		viper.SetConfigFile(f)
		merge := viper.ReadInConfig
		if read {
			merge = viper.MergeInConfig
		}
		if err := merge(); err != nil {
			clog.Log.Errorf("Error reading config file %s: %v", f, err)

			continue
		}
		read = true
	}
}

// GetFlags returns the fully populated FlagData. We must unmarshal from Viper instead of using globally bound pflags variables because pflags only
// parses command-line arguments. Viper merges environment variables (and config files) on top of the CLI flags.
func GetFlags() *FlagData {
	var f FlagData
	if err := viper.Unmarshal(&f); err != nil {
		clog.Log.Fatalf("failed to unmarshal configuration: %v", err)
	}

	return &f
}

// NewClient builds the client for the configured Dockhand: with the token when one is given, and with none for a Dockhand that has its login switched
// off. Which of the two a server is, it says itself when asked.
func (f *FlagData) NewClient() (*dockhand.Client, error) {
	if strings.TrimSpace(f.Server) == "" {
		return nil, errors.New("no server is configured: give --server (DOCKHAND_SERVER) the address the Dockhand web console answers on, e.g. http://dockhand:3000")
	}

	// the client says what it sends and is sent to the process's log, at the level DOCKHAND_LOG sets
	return dockhand.Connect(f.Server, f.Token, client.WithLog(clog.Log))
}

// DefaultToolsets is what the binary registers when neither --toolsets nor --allow-tools is given: enough to see what the Dockhand is and what runs
// on its hosts, and the audits that look for what is wrong there, which is what the server is for; nothing that writes. Every tool's definition is
// spent from a client's context before a question is asked, so the rest is asked for with --toolsets, or --toolsets all for everything.
var DefaultToolsets = []string{"core", "audit"}

// ToolOptions maps the flags onto the tool registration options. An allow list with no --toolsets is the whole choice: it already says which tools to
// load, so the default set is not added to it. Beside --toolsets it adds its tools to the sets.
//
// A session only reads unless --enable-write says otherwise. --read-only is taken because the other katbyte MCP servers are started with it, and here
// says what is already so: beside --enable-write it is a contradiction, and refused.
func (f *FlagData) ToolOptions() (tools.Options, error) {
	if f.ReadOnly && f.EnableWrite {
		return tools.Options{}, errors.New("--read-only and --enable-write contradict each other: give one")
	}
	sets := f.Toolsets
	if len(sets) == 0 && len(f.AllowTools) == 0 {
		sets = DefaultToolsets
	}

	return tools.Options{
		EnableWrite:  f.EnableWrite,
		EnableDelete: f.EnableDelete,
		Toolsets:     sets,
		Allow:        f.AllowTools,
		Deny:         f.DenyTools,
	}, nil
}
