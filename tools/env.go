package tools

import (
	"context"
	"fmt"
	"net"
	"strconv"

	"github.com/katbyte/dockhand-mcp/sdk/dockhand"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// The ways Dockhand reaches a Docker host, as it names them.
const (
	connectionSocket = "socket"
)

// environmentRow is one Docker host as env_list answers with it.
type environmentRow struct {
	Env        string `json:"env"               jsonschema:"the environment's name"`
	ID         int    `json:"id"                jsonschema:"the environment's number, which Dockhand's own API takes"`
	Connection string `json:"connection"        jsonschema:"how Dockhand reaches the host: socket (a Docker socket on the machine Dockhand itself runs on), direct (a Docker API at an address), hawser-standard (a Hawser agent on the host that Dockhand calls) or hawser-edge (a Hawser agent that calls Dockhand)"`
	Address    string `json:"address,omitempty" jsonschema:"where the host, or the agent in front of it, is reached; absent for a socket"`
	Online     *bool  `json:"online,omitempty"  jsonschema:"whether Dockhand says it can reach the host; absent when it would not say"`

	Containers *containerCounts `json:"containers,omitempty" jsonschema:"how many containers the host has, by state; absent for a host that is not online"`
	Stacks     *stackCounts     `json:"stacks,omitempty"     jsonschema:"how many compose stacks the host has, by state; absent for a host that is not online"`
}

// containerCounts is how many containers a host has, by state.
type containerCounts struct {
	Total          int `json:"total"`
	Running        int `json:"running"`
	Stopped        int `json:"stopped"`
	Paused         int `json:"paused"`
	Restarting     int `json:"restarting"      jsonschema:"containers Docker is starting again after they exited"`
	Unhealthy      int `json:"unhealthy"       jsonschema:"containers whose own health check is failing"`
	PendingUpdates int `json:"pending_updates" jsonschema:"containers Dockhand has found a newer image for"`
}

// stackCounts is how many compose stacks a host has, by state.
type stackCounts struct {
	Total   int `json:"total"`
	Running int `json:"running"`
	Partial int `json:"partial" jsonschema:"stacks with some of their containers running and some not"`
	Stopped int `json:"stopped"`
}

// count reads a whole number out of an object the API sends with no declared shape; one that is not there is none.
func count(counts map[string]any, key string) int {
	n, ok := counts[key].(float64)
	if !ok {
		return 0
	}

	return int(n)
}

// environmentRowOf is an environment as Dockhand lists it, before anything is known of how its host is doing.
func environmentRowOf(e *dockhand.GetEnvironmentsResponse) environmentRow {
	row := environmentRow{Env: e.Name, ID: e.Id, Connection: e.ConnectionType}
	if e.ConnectionType != connectionSocket && e.Host != "" {
		row.Address = e.Host
		if e.Port != 0 {
			row.Address = net.JoinHostPort(e.Host, strconv.Itoa(e.Port))
		}
	}

	return row
}

func registerEnvironmentTools(r *registry) {
	type envListOut struct {
		Environments []environmentRow `json:"environments"   jsonschema:"every Docker host the Dockhand manages, in the order it lists them"`
		Note         string           `json:"note,omitempty"`
	}
	add(r, readTool, &mcp.Tool{
		Name:        "env_list",
		Description: "List the Docker hosts (environments) the Dockhand manages: each one's name and number, how Dockhand reaches it (its own socket, an address, or a Hawser agent), whether it is online, and how many containers and compose stacks it has by state - running, stopped, restarting, unhealthy, with updates waiting. This is the one call that shows every host at a glance, and where a host that is down or has something unhealthy on it is first seen.",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, _ struct{}) (*mcp.CallToolResult, envListOut, error) {
		environments, err := r.dock.GetEnvironments(ctx)
		if err != nil {
			return nil, envListOut{}, fmt.Errorf("the environments could not be listed: %w", refusal(err))
		}

		out := envListOut{Environments: make([]environmentRow, 0, len(environments.Model))}
		for i := range environments.Model {
			out.Environments = append(out.Environments, environmentRowOf(&environments.Model[i]))
		}
		if len(out.Environments) == 0 {
			out.Note = "this Dockhand manages no Docker host yet"

			return nil, out, nil
		}

		// how each host is doing is one call for all of them, which asks every host and so is the slow part; without it the hosts are still listed,
		// and nothing is said of how any is doing
		stats, err := r.dock.GetDashboardStats(ctx)
		if err != nil {
			out.Note = "whether each host is online, and what it holds, could not be read: " + err.Error()
		}

		byID := map[int]*dockhand.GetDashboardStatsResponse{}
		for i := range stats.Model {
			byID[stats.Model[i].Id] = &stats.Model[i]
		}
		offline := 0
		for i := range out.Environments {
			row := &out.Environments[i]
			stat, ok := byID[row.ID]
			if !ok {
				continue
			}

			row.Online = stat.Online
			if !yes(stat.Online) {
				offline++

				continue
			}
			row.Containers = &containerCounts{Total: count(stat.Containers, "total"), Running: count(stat.Containers, "running"), Stopped: count(stat.Containers, "stopped"), Paused: count(stat.Containers, "paused"), Restarting: count(stat.Containers, "restarting"), Unhealthy: count(stat.Containers, "unhealthy"), PendingUpdates: count(stat.Containers, "pendingUpdates")}
			row.Stacks = &stackCounts{Total: count(stat.Stacks, "total"), Running: count(stat.Stacks, "running"), Partial: count(stat.Stacks, "partial"), Stopped: count(stat.Stacks, "stopped")}
		}
		if offline > 0 {
			out.Note = fmt.Sprintf("%d of the %d hosts could not be reached by Dockhand, and are listed with online false and no counts", offline, len(out.Environments))
		}

		return nil, out, nil
	})
}
