package tools

import (
	"cmp"
	"context"
	"fmt"
	"slices"
	"strings"

	"github.com/katbyte/dockhand-mcp/sdk/dockhand"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// stackService is one container of a stack as stack_list answers with it.
type stackService struct {
	Service   string   `json:"service"           jsonschema:"the compose service the container is"`
	Container string   `json:"container"         jsonschema:"the container's name"`
	Image     string   `json:"image"`
	State     string   `json:"state"             jsonschema:"running, exited, restarting, paused, created or dead"`
	Status    string   `json:"status"            jsonschema:"Docker's own words for the state: Up 2 hours (healthy), Exited (3) 11 seconds ago"`
	Health    string   `json:"health,omitempty"  jsonschema:"what the container's own health check says: healthy, unhealthy or starting; absent for a container with none"`
	Restarts  int      `json:"restarts"          jsonschema:"how many times Docker has started the container again after it exited"`
	Ports     []string `json:"ports,omitempty"   jsonschema:"the ports published on the host, each as host:container/protocol"`
	Problem   string   `json:"problem,omitempty" jsonschema:"what is wrong with the container, when something is: unhealthy, restarting, or exited with an error"`
}

// stackRow is one compose stack as stack_list answers with it.
type stackRow struct {
	Env      string         `json:"env"      jsonschema:"the environment (Docker host) the stack is on"`
	Stack    string         `json:"stack"    jsonschema:"the stack's name: its compose project"`
	Status   string         `json:"status"   jsonschema:"running when every one of its containers is, partial when some are, stopped when none is"`
	Services []stackService `json:"services" jsonschema:"the stack's containers, by service"`
	Problems int            `json:"problems" jsonschema:"how many of its containers have something wrong with them"`
}

// stackRowOf is a stack of an environment as the tools give one.
func stackRowOf(env string, s *dockhand.GetStacksResponse) stackRow {
	row := stackRow{Env: env, Stack: s.Name, Status: s.Status, Services: make([]stackService, 0, len(s.ContainerDetails))}
	for i := range s.ContainerDetails {
		c := &s.ContainerDetails[i]
		service := stackService{Service: c.Service, Container: c.Name, Image: c.Image, State: c.State, Status: c.Status, Health: c.Health, Restarts: c.RestartCount, Problem: problemOf(c.State, c.Health, c.Status, c.RestartCount)}
		for _, p := range c.Ports {
			// a port published on every address is listed once for each
			if p.Display != "" && !slices.Contains(service.Ports, p.Display) {
				service.Ports = append(service.Ports, p.Display)
			}
		}
		slices.Sort(service.Ports)
		if service.Problem != "" {
			row.Problems++
		}
		row.Services = append(row.Services, service)
	}
	slices.SortFunc(row.Services, func(a, b stackService) int {
		return cmp.Or(cmp.Compare(a.Service, b.Service), cmp.Compare(a.Container, b.Container))
	})

	return row
}

// stacksOf is every compose stack on an environment.
func (r *registry) stacksOf(ctx context.Context, env environment) ([]dockhand.GetStacksResponse, error) {
	listed, err := r.dock.GetStacks(ctx, dockhand.GetStacksOperationOptions{Env: &env.ID})
	if err != nil {
		return nil, refusal(err)
	}

	return listed.Model, nil
}

func registerStackTools(r *registry) {
	type stackListIn struct {
		envArg

		Name     string `json:"name,omitempty"     jsonschema:"only stacks whose name contains this, whatever its case"`
		Problems bool   `json:"problems,omitempty" jsonschema:"only stacks something is wrong with: not every container running, or one of them unhealthy, restarting or exited with an error"`
	}
	type stackListOut struct {
		Stacks   []stackRow     `json:"stacks"           jsonschema:"the stacks that matched, by environment and then by name"`
		Total    int            `json:"total"            jsonschema:"how many stacks matched, on every environment asked"`
		ByStatus map[string]int `json:"by_status"        jsonschema:"how many of those are running, partial and stopped"`
		Failed   []envFailure   `json:"failed,omitempty" jsonschema:"the environments that could not be asked, each with why; their stacks are in no count"`
		Note     string         `json:"note,omitempty"`
	}
	add(r, readTool, &mcp.Tool{
		Name:        "stack_list",
		Description: "List the compose stacks on every Docker host (environment) the Dockhand manages, or on one: each with its host, whether it is running, partial or stopped, and its containers by service with their image, state, health, restarts and published ports. A stack is whatever compose project has containers on the host, whether Dockhand made it or someone at the host did. Narrow it to one environment or a part of a name; or ask for problems alone, which is every stack that is not wholly running or has a container that is unhealthy, restarting or exited with an error. A host that cannot be asked is named with the reason beside what the others answered. Containers that belong to no stack are in container_list alone.",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in stackListIn) (*mcp.CallToolResult, stackListOut, error) {
		envs, err := r.pick(ctx, in.envArg)
		if err != nil {
			return nil, stackListOut{}, err
		}

		out := stackListOut{Stacks: []stackRow{}, ByStatus: map[string]int{}}
		for _, answer := range ask(ctx, envs, r.stacksOf) {
			if answer.Err != nil {
				out.Failed = append(out.Failed, envFailure{Env: answer.Env.Name, Error: answer.Err.Error()})

				continue
			}

			rows := make([]stackRow, 0, len(answer.Value))
			for i := range answer.Value {
				row := stackRowOf(answer.Env.Name, &answer.Value[i])
				if (in.Name != "" && !strings.Contains(strings.ToLower(row.Stack), strings.ToLower(in.Name))) || (in.Problems && row.Problems == 0 && row.Status == stackRunning) {
					continue
				}
				rows = append(rows, row)
			}
			slices.SortFunc(rows, func(a, b stackRow) int { return cmp.Compare(a.Stack, b.Stack) })

			for i := range rows {
				out.Total++
				out.ByStatus[rows[i].Status]++
			}
			out.Stacks = append(out.Stacks, rows...)
		}

		if len(out.Failed) == len(envs) {
			return nil, out, fmt.Errorf("no environment could be asked for its stacks: %s: %s", out.Failed[0].Env, out.Failed[0].Error)
		}
		if len(out.Failed) > 0 {
			out.Note = fmt.Sprintf("%d of the %d environments could not be asked, and their stacks are missing from this", len(out.Failed), len(envs))
		}

		return nil, out, nil
	})
}

// What Dockhand calls a stack every container of which is running, and one none of which is.
const (
	stackRunning = "running"
	stackStopped = "stopped"
)
