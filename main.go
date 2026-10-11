// Package main implements dockhand-mcp, an MCP server and CLI for the containers and stacks a Dockhand manages.
package main

import (
	"os"

	c "github.com/gookit/color"
	"github.com/katbyte/dockhand-mcp/cli"
	"github.com/katbyte/go-kt/clog"
)

func main() {
	// the log level comes from DOCKHAND_LOG; read it once here, before anything logs
	clog.SetLevelFromEnv("DOCKHAND_LOG")

	cmd, err := cli.Make()
	if err != nil {
		clog.Log.Error(c.Sprintf("<red>dockhand-mcp: building cmd</> %v", err))

		os.Exit(1)
	}

	if err := cmd.Execute(); err != nil {
		clog.Log.Error(c.Sprintf("<red>dockhand-mcp:</> %v", err))

		os.Exit(1)
	}

	os.Exit(0)
}
