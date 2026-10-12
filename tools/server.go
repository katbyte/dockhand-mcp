package tools

import (
	"context"
	"fmt"

	"github.com/katbyte/dockhand-mcp/sdk/dockhand"
	"github.com/katbyte/go-kt/version"
	"github.com/katbyte/pandorest/client"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// note adds a sentence to a note that may already hold one.
func note(have, more string) string {
	if have == "" {
		return more
	}

	return have + "; " + more
}

// refusal says, of an error that is the server asking who is calling or turning them away, what the operator is to do about it; any other error is
// handed back as it came.
func refusal(err error) error {
	switch {
	case client.IsUnauthorized(err):
		return fmt.Errorf("%w (set --token, DOCKHAND_TOKEN, to an API token of this Dockhand's)", err)
	case client.IsForbidden(err):
		return fmt.Errorf("%w (the role of the user the API token belongs to decides what these tools may do)", err)
	default:
		return err
	}
}

func registerServerTools(r *registry) {
	// serverInfoOut names this binary's version beside the server's, because both change what a caller can rely on: the server's decides what the API
	// answers, and this binary's decides which tools and fixes are in play. The second is the one that goes stale unnoticed - an MCP client keeps the
	// binary it started with.
	type serverInfoOut struct {
		DockhandMCPVersion string `json:"dockhand_mcp_version"     jsonschema:"the build of this MCP server answering, e.g. v0.1.1+4@g8909c7c: the tag, the commits since it, and the commit"`
		SDKAPIVersion      string `json:"sdk_api_version"          jsonschema:"the Dockhand version whose API this MCP server's client was generated from; a server ahead of it may answer with fields the client does not read"`
		URL                string `json:"url"                      jsonschema:"the address the Dockhand is asked at"`
		Version            string `json:"version,omitempty"        jsonschema:"the server's own version: the newest release in the changelog it carries"`
		Released           string `json:"released,omitempty"       jsonschema:"the date of that release"`
		Login              bool   `json:"login"                    jsonschema:"whether the server has its login switched on. Off, it asks nobody who they are: anyone who can reach it can do everything it can"`
		Database           string `json:"database,omitempty"       jsonschema:"what the server keeps its own records in, as it names it: sqlite, say"`
		DatabaseHealthy    bool   `json:"database_healthy"         jsonschema:"the server's own check of that database: every table it expects is there and no migration is waiting"`
		SchemaVersion      string `json:"schema_version,omitempty" jsonschema:"the newest migration applied to that database"`
		Environments       int    `json:"environments"             jsonschema:"how many Docker hosts the server manages; env_list describes each"`
		Note               string `json:"note,omitempty"`
	}
	add(r, readTool, &mcp.Tool{
		Name:        "server_info",
		Description: "Check connectivity to the Dockhand and describe it: its version, whether its login is switched on, the health of its own database, and how many Docker hosts (environments) it manages. Also names this MCP server's own build and the Dockhand version its client was generated from. A Dockhand that cannot be reached, or that refuses the API token, is an error that says which. Start here, then env_list for the hosts.",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, _ struct{}) (*mcp.CallToolResult, serverInfoOut, error) {
		out := serverInfoOut{DockhandMCPVersion: version.Version, SDKAPIVersion: dockhand.APIVersion, URL: r.dock.Client.BaseURL}

		// the one call a server answers whoever asks: it says whether the others will want a token
		session, err := r.dock.GetAuthSession(ctx)
		if err != nil {
			return nil, out, fmt.Errorf("the Dockhand at %s could not be asked: %w", out.URL, err)
		}
		out.Login = session.Answer.AuthEnabled

		// what it manages is the first thing a token is needed for, so a token that is wrong or missing is said here, as that
		environments, err := r.dock.GetEnvironments(ctx)
		if err != nil {
			return nil, out, fmt.Errorf("the Dockhand at %s answers, and would not list its environments: %w", out.URL, refusal(err))
		}
		out.Environments = len(environments.Answer)

		// the rest describes a server that is known to answer, and one of them failing is said rather than hiding the others
		if changelog, err := r.dock.GetChangelog(ctx); err != nil {
			out.Note = note(out.Note, "its version could not be read: "+err.Error())
		} else if len(changelog.Answer) > 0 {
			out.Version, out.Released = changelog.Answer[0].Version, changelog.Answer[0].Date
		}

		if database, err := r.dock.GetHealthDatabase(ctx); err != nil {
			out.Note = note(out.Note, "its database's health could not be read: "+err.Error())
		} else {
			out.Database, out.DatabaseHealthy, out.SchemaVersion = database.Answer.Database, database.Answer.Healthy, database.Answer.SchemaVersion
		}

		if !out.Login {
			out.Note = note(out.Note, "this Dockhand's login is switched off: it takes no API token, and every tool here works for whoever can reach it")
		}
		if out.Environments == 0 {
			out.Note = note(out.Note, "it manages no Docker host yet, so there is nothing for the container and stack tools to read")
		}

		return nil, out, nil
	})
}
