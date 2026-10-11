package cli

import (
	"github.com/katbyte/dockhand-mcp/tools"
	"github.com/katbyte/go-kt/clog"
	"github.com/katbyte/go-kt/mcp/server"
	"github.com/katbyte/go-kt/version"
	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/spf13/cobra"
)

func serveCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "serve",
		Short: "Run the MCP server (stdio by default, HTTP with --listen)",
		Long: `Runs the MCP server for AI clients such as Claude Code.

Without --listen it speaks MCP over stdio: register it in .mcp.json with the DOCKHAND_*
environment variables set. With --listen (DOCKHAND_LISTEN, e.g. :8080) it serves the
Streamable HTTP transport at /mcp instead, for an always-on deployment such as the
docker-compose.yml in this repo. --auth-token (DOCKHAND_AUTH_TOKEN) is then required, and
clients must send "Authorization: Bearer <token>"; to serve with no token at all, which
lets anyone who can reach the port use every tool, say so with --allow-no-auth
(DOCKHAND_ALLOW_NO_AUTH=true).

Every tool only reads unless --enable-write (DOCKHAND_ENABLE_WRITE=true) is given, and
none deletes without --enable-delete beside it.

The Dockhand is not contacted at start: one that is down is something the tools report,
not a reason to have no tools.`,
		Args:          cobra.NoArgs,
		SilenceErrors: true,
		RunE: func(cmd *cobra.Command, _ []string) error {
			cmd.SilenceUsage = true

			f := GetFlags()
			dock, err := f.NewClient()
			if err != nil {
				return err
			}

			srv := mcp.NewServer(&mcp.Implementation{
				Name:    "dockhand",
				Title:   "Dockhand",
				Version: version.Version,
			}, nil)

			opts, err := f.ToolOptions()
			if err != nil {
				return err
			}
			registered, err := tools.RegisterAll(srv, dock, opts)
			if err != nil {
				return err
			}
			clog.Log.Infof("registered %d tools for the Dockhand at %s", len(registered), dock.Client.BaseURL)

			return server.Run(cmd.Context(), srv, f.serveOptions())
		},
	}
}

// serveOptions is how this tool serves: stdio, or HTTP at --listen behind --auth-token, with the health probe a container asks outside the check.
func (f *FlagData) serveOptions() server.Options {
	return server.Options{
		Listen:      f.Listen,
		AuthToken:   f.AuthToken,
		AllowNoAuth: f.AllowNoAuth,
		Name:        "dockhand-mcp",
		EnvPrefix:   "DOCKHAND",
	}
}
