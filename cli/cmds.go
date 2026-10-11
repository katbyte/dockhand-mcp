// Package cli implements the dockhand-mcp command line interface: the cobra commands, flag and config handling, and the MCP server exposing
// Dockhand's tools to AI clients.
package cli

import (
	"fmt"
	"strings"

	"github.com/katbyte/dockhand-mcp/tools"
	"github.com/katbyte/go-kt/version"
	"github.com/spf13/cobra"
)

// firstSentence trims a tool description to its opening sentence, so the list stays one line per tool. A full stop that ends an abbreviation ends no
// sentence.
func firstSentence(s string) string {
	for from := 0; ; {
		i := strings.Index(s[from:], ". ")
		if i < 0 {
			return s
		}
		end := from + i
		if !endsInAbbreviation(s[:end+1]) {
			return s[:end+1]
		}
		from = end + 2
	}
}

// endsInAbbreviation reports whether a stretch of text ends in a dotted abbreviation rather than a sentence.
func endsInAbbreviation(s string) bool {
	lower := strings.ToLower(s)
	for _, abbr := range []string{"e.g.", "i.e.", "etc.", "vs."} {
		if strings.HasSuffix(lower, abbr) {
			return true
		}
	}

	return false
}

func Make() (*cobra.Command, error) {
	root := &cobra.Command{
		Use:   "dockhand-mcp [command]",
		Short: "dockhand-mcp is an MCP server and CLI for the containers and stacks a Dockhand manages",
		Long: `An MCP server (and CLI) for Dockhand: see every Docker host it manages and what runs
on each, read logs, and find what is wrong, from an AI client such as Claude Code. One
process works on one Dockhand, and through it on every host that Dockhand manages.
Complete documentation is available at https://github.com/katbyte/dockhand-mcp`,
		RunE: func(cmd *cobra.Command, _ []string) error {
			_, _ = fmt.Fprintln(cmd.OutOrStdout(), "Run \"dockhand-mcp help\" for more information about available dockhand-mcp commands.")

			return nil
		},
	}

	root.AddCommand(&cobra.Command{
		Use:           "version",
		Short:         "Print the version number of dockhand-mcp",
		Long:          `Print the version number of dockhand-mcp`,
		Args:          cobra.NoArgs,
		SilenceErrors: true,
		Run: func(cmd *cobra.Command, _ []string) {
			_, _ = fmt.Fprintln(cmd.OutOrStdout(), "dockhand-mcp "+version.Version)
		},
	})

	root.AddCommand(toolsCmd())
	root.AddCommand(infoCmd())
	root.AddCommand(serveCmd())

	if err := configureFlags(root); err != nil {
		return nil, fmt.Errorf("unable to configure flags: %w", err)
	}

	return root, nil
}

func toolsCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "tools",
		Short: "List the tools and toolsets, and what the current flags would register",
		Long: `Lists every tool dockhand-mcp would register with the current flags, grouped by toolset,
with its kind (read, write or delete) and what it does.

Needs no server: it reports what would be registered, not what a Dockhand accepts.

  dockhand-mcp tools                               # the default set (core), reads only
  dockhand-mcp tools --toolsets all                # every tool that only reads
  dockhand-mcp tools --toolsets all --enable-write # and the ones that change something
  dockhand-mcp tools -q                            # names only`,
		Args:          cobra.NoArgs,
		SilenceErrors: true,
		RunE: func(cmd *cobra.Command, _ []string) error {
			cmd.SilenceUsage = true
			out := cmd.OutOrStdout()
			quiet, _ := cmd.Flags().GetBool("quiet")

			f := GetFlags()
			opts, err := f.ToolOptions()
			if err != nil {
				return err
			}
			list, err := tools.Describe(opts)
			if err != nil {
				return err
			}

			if quiet {
				for _, t := range list {
					_, _ = fmt.Fprintln(out, t.Name)
				}

				return nil
			}

			byset := map[string][]tools.ToolInfo{}
			counts := map[string]int{}
			for _, t := range list {
				byset[t.Toolset] = append(byset[t.Toolset], t)
				counts[t.Kind]++
			}

			for _, set := range tools.ToolsetOrder {
				in := byset[set]
				if len(in) == 0 {
					continue
				}
				_, _ = fmt.Fprintf(out, "\n%s (%d)\n", set, len(in))
				for _, t := range in {
					_, _ = fmt.Fprintf(out, "  %-32s %-6s %s\n", t.Name, t.Kind, firstSentence(t.Description))
				}
			}
			_, _ = fmt.Fprintf(out, "\n%d tools: %d read, %d write, %d delete\n", len(list), counts["read"], counts["write"], counts["delete"])
			switch {
			case !f.EnableWrite:
				_, _ = fmt.Fprintln(out, "only tools that read are listed; --enable-write registers the ones that change something, and --enable-delete beside it the ones that delete")
			case !f.EnableDelete:
				_, _ = fmt.Fprintln(out, "delete tools are hidden; --enable-delete registers them")
			default:
			}
			_, _ = fmt.Fprintf(out, "\ntoolsets: all, %s\n", strings.Join(tools.ToolsetNames(), ", "))
			_, _ = fmt.Fprintf(out, "families: %s\n", strings.Join(tools.FamilyNames(), ", "))
			_, _ = fmt.Fprintf(out, "select with --toolsets / DOCKHAND_TOOLSETS; core is always included. Default is %s - use --toolsets all for every tool. --allow-tools adds tools to the sets by name, and with no --toolsets registers only the tools it names; --deny-tools takes tools away.\n", strings.Join(DefaultToolsets, ","))

			return nil
		},
	}
	cmd.Flags().BoolP("quiet", "q", false, "print tool names only")

	return cmd
}

func infoCmd() *cobra.Command {
	return &cobra.Command{
		Use:           "info",
		Short:         "Check connectivity and print the Dockhand's version and the hosts it manages",
		Long:          `Connects to the configured Dockhand and prints its address and version, whether its login is switched on, and the Docker hosts (environments) it manages with how each is reached. A Dockhand that cannot be asked, or that refuses the API token, fails the command with the reason.`,
		Args:          cobra.NoArgs,
		SilenceErrors: true,
		RunE: func(cmd *cobra.Command, _ []string) error {
			cmd.SilenceUsage = true
			out := cmd.OutOrStdout()

			dock, err := GetFlags().NewClient()
			if err != nil {
				return err
			}

			// the one call a server answers whoever asks, so a server that is not there is told from one that wants a token
			session, err := dock.GetAuthSession(cmd.Context())
			if err != nil {
				return fmt.Errorf("the Dockhand at %s could not be asked: %w", dock.Client.BaseURL, err)
			}
			login := "off"
			if session.Model.AuthEnabled != nil && *session.Model.AuthEnabled {
				login = "on"
			}

			environments, err := dock.GetEnvironments(cmd.Context())
			if err != nil {
				return fmt.Errorf("the Dockhand at %s answers, with its login %s, and would not list its environments: %w", dock.Client.BaseURL, login, err)
			}

			release := "a version it would not give"
			if changelog, err := dock.GetChangelog(cmd.Context()); err == nil && len(changelog.Model) > 0 {
				release = changelog.Model[0].Version
			}

			_, _ = fmt.Fprintf(out, "Dockhand %s at %s, login %s, %d environments\n", release, dock.Client.BaseURL, login, len(environments.Model))
			for i := range environments.Model {
				e := &environments.Model[i]
				_, _ = fmt.Fprintf(out, "  %s (%d): %s\n", e.Name, e.Id, e.ConnectionType)
			}

			return nil
		},
	}
}
