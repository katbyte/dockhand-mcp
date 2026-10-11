package tools

import (
	"cmp"
	"context"
	"fmt"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/katbyte/dockhand-mcp/sdk/dockhand"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

const (
	// The labels compose puts on every container it makes, which is how a container is known to belong to a stack and to be one of its services.
	labelStack   = "com.docker.compose.project"
	labelService = "com.docker.compose.service"

	// The states and health Docker gives a container that mean something is wrong with it.
	stateRunning    = "running"
	stateRestarting = "restarting"
	stateExited     = "exited"
	stateDead       = "dead"
	healthUnhealthy = "unhealthy"

	// defaultContainers is how many containers a list gives unless told otherwise: a row is a few dozen tokens, and a host can hold hundreds
	defaultContainers = 200
	// shortID is how much of a container's id is given: what docker itself shows, and enough to name it
	shortID = 12
)

// containerRow is one container as container_list answers with it.
type containerRow struct {
	Env      string   `json:"env"               jsonschema:"the environment (Docker host) the container is on"`
	Name     string   `json:"name"`
	ID       string   `json:"id"                jsonschema:"the start of the container's id, as docker shows it"`
	Image    string   `json:"image"`
	State    string   `json:"state"             jsonschema:"running, exited, restarting, paused, created or dead"`
	Status   string   `json:"status"            jsonschema:"Docker's own words for the state: Up 2 hours (healthy), Exited (3) 11 seconds ago"`
	Health   string   `json:"health,omitempty"  jsonschema:"what the container's own health check says: healthy, unhealthy or starting; absent for a container with none"`
	Restarts int      `json:"restarts"          jsonschema:"how many times Docker has started the container again after it exited"`
	Stack    string   `json:"stack,omitempty"   jsonschema:"the compose stack (project) the container belongs to; absent for one started on its own"`
	Service  string   `json:"service,omitempty" jsonschema:"the compose service the container is"`
	Ports    []string `json:"ports,omitempty"   jsonschema:"the ports published on the host, each as host->container/protocol; a port that is exposed and not published is the container's alone"`
	Created  string   `json:"created,omitempty" jsonschema:"when the container was made, in UTC"`
	Problem  string   `json:"problem,omitempty" jsonschema:"what is wrong with the container, when something is: unhealthy, restarting, or exited with an error"`
}

// exitCode reads the code out of Docker's words for a container that has exited: Exited (3) 11 seconds ago.
var exitCode = regexp.MustCompile(`^Exited \((\d+)\)`)

// upForSeconds reads, out of Docker's words for a running container, that it has been up for less than a minute: Up Less than a second, Up 12
// seconds.
var upForSeconds = regexp.MustCompile(`^Up (Less than a second|\d+ seconds?)`)

// killed is the code a container exits with when Docker kills it, which is what stopping one does when it does not stop by itself within the time it
// is given.
const killed = "137"

// problemOf says what is wrong with a container, "" when nothing is: its own health check failing, Docker starting it over and over, or its having
// exited with anything but 0. One that keeps exiting is only sometimes caught between runs, as restarting: caught while it runs, it is one Docker has
// started again and that has been up for seconds, which is said as that. A list cannot tell a container that was stopped on purpose from one that
// fell over, only how each ended, so the one code that stopping a container gives is said to be that.
func problemOf(state, health, status string, restarts int) string {
	switch {
	case state == stateRestarting:
		return fmt.Sprintf("restarting: Docker has started it again %d times", restarts)
	case state == stateDead:
		return "dead: Docker could not remove it"
	case health == healthUnhealthy:
		return "unhealthy: its health check is failing"
	case state == stateRunning && restarts > 0 && upForSeconds.MatchString(status):
		return fmt.Sprintf("started again by Docker %d times, the last only seconds ago", restarts)
	case state == stateExited:
		code := exitCode.FindStringSubmatch(status)
		switch {
		case len(code) < 2 || code[1] == "0":
		case code[1] == killed:
			return "exited with code 137: it was killed, which is how a container ends when it is stopped and does not stop by itself"
		default:
			return "exited with code " + code[1]
		}
	default:
	}

	return ""
}

// publishedPorts is a container's ports as a person writes them, each once: a port published on every address is listed by Docker once for each.
func publishedPorts(ports []dockhand.GetContainersResponsePorts) []string {
	out := make([]string, 0, len(ports))
	for _, p := range ports {
		port := strconv.Itoa(p.PrivatePort) + "/" + p.Type
		if p.PublicPort != 0 {
			port = strconv.Itoa(p.PublicPort) + "->" + port
		}
		if !slices.Contains(out, port) {
			out = append(out, port)
		}
	}
	slices.Sort(out)

	return out
}

// containerRowOf is a container of an environment as the tools give one.
func containerRowOf(env string, c *dockhand.GetContainersResponse) containerRow {
	row := containerRow{Env: env, Name: c.Name, ID: c.Id, Image: c.Image, State: c.State, Status: c.Status, Health: c.Health, Restarts: c.RestartCount, Stack: c.Labels[labelStack], Service: c.Labels[labelService], Ports: publishedPorts(c.Ports), Problem: problemOf(c.State, c.Health, c.Status, c.RestartCount)}
	if len(row.ID) > shortID {
		row.ID = row.ID[:shortID]
	}
	if c.Created > 0 {
		row.Created = time.Unix(int64(c.Created), 0).UTC().Format(time.RFC3339)
	}

	return row
}

// containersOf is every container on an environment, running or not.
func (r *registry) containersOf(ctx context.Context, env environment) ([]dockhand.GetContainersResponse, error) {
	listed, err := r.dock.GetContainers(ctx, dockhand.GetContainersOperationOptions{Env: &env.ID, All: new(true)})
	if err != nil {
		return nil, refusal(err)
	}

	return listed.Model, nil
}

func registerContainerTools(r *registry) {
	type containerListIn struct {
		envArg

		Stack    string `json:"stack,omitempty"    jsonschema:"only the containers of this compose stack (project), by its name"`
		Name     string `json:"name,omitempty"     jsonschema:"only containers whose name contains this, whatever its case"`
		State    string `json:"state,omitempty"    jsonschema:"only containers in this state: running, exited, restarting, paused, created or dead"`
		Problems bool   `json:"problems,omitempty" jsonschema:"only containers something is wrong with: unhealthy, restarting or just started again by Docker, or exited with an error"`
		Limit    int    `json:"limit,omitempty"    jsonschema:"the most containers to list, 200 unless given; the counts are of every container that matched"`
	}
	type containerListOut struct {
		Containers []containerRow `json:"containers"       jsonschema:"the containers that matched, by environment and then by name"`
		Total      int            `json:"total"            jsonschema:"how many containers matched, on every environment asked; more than are listed when the list was cut at the limit"`
		ByState    map[string]int `json:"by_state"         jsonschema:"how many of those are in each state"`
		Problems   int            `json:"problems"         jsonschema:"how many of those have something wrong with them"`
		Failed     []envFailure   `json:"failed,omitempty" jsonschema:"the environments that could not be asked, each with why; their containers are in no count"`
		Note       string         `json:"note,omitempty"`
	}
	add(r, readTool, &mcp.Tool{
		Name:        "container_list",
		Description: "List the containers on every Docker host (environment) the Dockhand manages, or on one: each with its host, name, image, state, Docker's words for that state, what its own health check says, how often Docker has restarted it, the compose stack and service it belongs to, and the ports it publishes. Running or not, all are listed. Narrow it to one environment, one stack, a part of a name or a state; or ask for problems alone, which is every container that is unhealthy, restarting or exited with an error, each saying which. A host that cannot be asked is named with the reason beside what the others answered. This is where to look first for what is running where, and for what is not running that should be.",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in containerListIn) (*mcp.CallToolResult, containerListOut, error) {
		if in.Limit < 0 {
			return nil, containerListOut{}, fmt.Errorf("limit is %d: it is how many containers to list at most, a number above 0, or left out for %d", in.Limit, defaultContainers)
		}
		limit := cmp.Or(in.Limit, defaultContainers)

		envs, err := r.pick(ctx, in.envArg)
		if err != nil {
			return nil, containerListOut{}, err
		}

		out := containerListOut{Containers: []containerRow{}, ByState: map[string]int{}}
		for _, answer := range ask(ctx, envs, r.containersOf) {
			if answer.Err != nil {
				out.Failed = append(out.Failed, envFailure{Env: answer.Env.Name, Error: answer.Err.Error()})

				continue
			}

			rows := make([]containerRow, 0, len(answer.Value))
			for i := range answer.Value {
				row := containerRowOf(answer.Env.Name, &answer.Value[i])
				if (in.Stack != "" && row.Stack != in.Stack) || (in.Name != "" && !strings.Contains(strings.ToLower(row.Name), strings.ToLower(in.Name))) || (in.State != "" && !strings.EqualFold(row.State, in.State)) || (in.Problems && row.Problem == "") {
					continue
				}
				rows = append(rows, row)
			}
			slices.SortFunc(rows, func(a, b containerRow) int { return cmp.Compare(a.Name, b.Name) })

			for i := range rows {
				out.Total++
				out.ByState[rows[i].State]++
				if rows[i].Problem != "" {
					out.Problems++
				}
				if len(out.Containers) < limit {
					out.Containers = append(out.Containers, rows[i])
				}
			}
		}

		if len(out.Failed) == len(envs) {
			return nil, out, fmt.Errorf("no environment could be asked for its containers: %s: %s", out.Failed[0].Env, out.Failed[0].Error)
		}
		if out.Total > len(out.Containers) {
			out.Note = note(out.Note, fmt.Sprintf("%d containers matched and the first %d are listed: narrow it by env, stack, name or state, or raise limit", out.Total, len(out.Containers)))
		}
		if len(out.Failed) > 0 {
			out.Note = note(out.Note, fmt.Sprintf("%d of the %d environments could not be asked, and their containers are missing from this", len(out.Failed), len(envs)))
		}

		return nil, out, nil
	})
}
