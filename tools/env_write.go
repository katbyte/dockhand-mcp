package tools

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"

	"github.com/katbyte/dockhand-mcp/sdk/dockhand"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// The ways Dockhand reaches a Docker host beside a socket on its own machine, as it names them: an address it calls, an agent it calls, and an agent
// that calls it.
const (
	connectionDirect = "direct"
	connectionAgent  = "hawser-standard"
	connectionEdge   = "hawser-edge"
)

// connectionArgs is how a Docker host is reached, as the tools that try a connection and that save one both take it.
type connectionArgs struct {
	Connection string `json:"connection,omitempty"  jsonschema:"how Dockhand is to reach the host: socket (a Docker socket on the machine Dockhand runs on), direct (a Docker API at an address), hawser-standard (a Hawser agent on the host that Dockhand calls) or hawser-edge (a Hawser agent on the host that calls Dockhand, for a host behind a firewall or with no address of its own)"`
	SocketPath string `json:"socket_path,omitempty" jsonschema:"for socket: the socket's path as Dockhand sees it, /var/run/docker.sock unless given"`
	Host       string `json:"host,omitempty"        jsonschema:"for direct and hawser-standard: the host's name or address as Dockhand reaches it"`
	Port       int    `json:"port,omitempty"        jsonschema:"for direct and hawser-standard: the port, 2375 for Docker and 2376 for an agent unless given"`
	Protocol   string `json:"protocol,omitempty"    jsonschema:"for direct and hawser-standard: http or https, http unless given"`
	AgentToken string `json:"agent_token,omitempty" jsonschema:"for hawser-standard: the token the agent was started with. For hawser-edge: a token of your own choosing for the agent to call in with, 16 characters at least; left out, Dockhand makes one. It is sent to Dockhand and kept there, and is never given back"`
}

// checked is a connection as it is sent, once it has been found to say enough: the kind, and what that kind needs.
func (a *connectionArgs) checked() (connectionArgs, error) {
	out := *a
	out.Connection = strings.TrimSpace(out.Connection)
	switch out.Connection {
	case connectionSocket:
		if out.Host != "" || out.AgentToken != "" {
			return out, errors.New("a socket is a path on the machine Dockhand runs on: it takes socket_path, and no host or agent_token")
		}
		out.SocketPath = cmp.Or(out.SocketPath, "/var/run/docker.sock")
	case connectionDirect, connectionAgent:
		if strings.TrimSpace(out.Host) == "" {
			return out, fmt.Errorf("host is required for a %s connection: the host's name or address as Dockhand reaches it", out.Connection)
		}
		if out.Connection == connectionAgent && out.AgentToken == "" {
			return out, errors.New("agent_token is required for a hawser-standard connection: the token the agent was started with")
		}
		if out.Connection == connectionDirect && out.AgentToken != "" {
			return out, errors.New("a direct connection is to Docker itself, which takes no agent_token: for an agent, connection is hawser-standard")
		}
		defaultPort := 2375
		if out.Connection == connectionAgent {
			defaultPort = 2376
		}
		out.Port, out.Protocol = cmp.Or(out.Port, defaultPort), cmp.Or(out.Protocol, "http")
		if out.Protocol != "http" && out.Protocol != "https" {
			return out, fmt.Errorf("protocol is %q: it is http or https", out.Protocol)
		}
	case connectionEdge:
		if out.Host != "" || out.Port != 0 || out.Protocol != "" || out.SocketPath != "" {
			return out, errors.New("an agent that calls Dockhand is not reached at an address: a hawser-edge connection takes no host, port, protocol or socket_path")
		}
		if out.AgentToken != "" && len(out.AgentToken) < minAgentToken {
			return out, fmt.Errorf("agent_token is %d characters: a token of your own is %d at least, or left out for Dockhand to make one", len(out.AgentToken), minAgentToken)
		}
	case "":
		return out, errors.New("connection is required: socket, direct, hawser-standard or hawser-edge")
	default:
		return out, fmt.Errorf("connection is %q: it is socket, direct, hawser-standard or hawser-edge", out.Connection)
	}

	return out, nil
}

// reached is what trying a connection found.
type reached struct {
	Reachable     bool     `json:"reachable"                jsonschema:"whether Dockhand reached a Docker there"`
	Error         string   `json:"error,omitempty"          jsonschema:"why not, in Dockhand's words, when it did not"`
	DockerVersion string   `json:"docker_version,omitempty" jsonschema:"the version of the Docker it reached"`
	DockerHost    string   `json:"docker_host,omitempty"    jsonschema:"what that Docker calls its machine"`
	Containers    int      `json:"containers"               jsonschema:"how many containers it has"`
	Images        int      `json:"images"                   jsonschema:"how many images it has"`
	Agent         string   `json:"agent,omitempty"          jsonschema:"the name of the Hawser agent in front of it, when there is one"`
	AgentVersion  string   `json:"agent_version,omitempty"`
	AgentCan      []string `json:"agent_can,omitempty"      jsonschema:"what the agent says it can do: exec, metrics, events, compose"`
	SocketsFound  []string `json:"sockets_found,omitempty"  jsonschema:"for a socket that was not reached: the Docker sockets Dockhand does find at the usual places on its own machine; absent when it finds none"`
}

// agentOf reads what a Hawser agent said of itself out of an answer that sends it with no declared shape; what is not there, or is not what it should
// be, is left out.
func agentOf(agent map[string]any, into *reached) {
	if name, ok := agent["agentName"].(string); ok {
		into.Agent = name
	}
	if version, ok := agent["hawserVersion"].(string); ok {
		into.AgentVersion = version
	}
	can, ok := agent["capabilities"].([]any)
	if !ok {
		return
	}
	for _, c := range can {
		if s, ok := c.(string); ok {
			into.AgentCan = append(into.AgentCan, s)
		}
	}
}

// try has Dockhand try a connection that is not saved.
func (r *registry) try(ctx context.Context, c *connectionArgs) (reached, error) {
	answer, err := r.dock.PostEnvironmentsTest(ctx, dockhand.PostEnvironmentsTestRequest{ConnectionType: dockhand.ConnectionType(c.Connection), SocketPath: c.SocketPath, Host: c.Host, Port: c.Port, Protocol: dockhand.EnvironmentProtocol(c.Protocol), HawserToken: c.AgentToken})
	if err != nil {
		return reached{}, refusal(err)
	}

	out := reached{Reachable: answer.Answer.Success, Error: answer.Answer.Error}
	if info := answer.Answer.Info; info != nil {
		out.DockerVersion, out.DockerHost, out.Containers, out.Images = info.ServerVersion, info.Name, info.Containers, info.Images
	}
	agentOf(answer.Answer.Hawser, &out)

	// a socket that is not there is most often one at another path, and Dockhand can say which it has
	if c.Connection == connectionSocket && !out.Reachable {
		if found, failed := r.dock.GetEnvironmentsDetectSocket(ctx); failed == nil {
			for _, socket := range found.Answer.Sockets {
				if socket.Exists {
					out.SocketsFound = append(out.SocketsFound, socket.Path)
				}
			}
		}
	}

	return out, nil
}

// tryUnsaved checks that a connection says enough to be tried, and has Dockhand try it.
func (r *registry) tryUnsaved(ctx context.Context, c *connectionArgs) (reached, error) {
	connection, err := c.checked()
	if err != nil {
		return reached{}, fmt.Errorf("%w; or give env, to try an environment that is saved", err)
	}
	if connection.Connection == connectionEdge {
		return reached{}, errors.New("an agent that calls Dockhand cannot be tried before it is saved, since there is nothing for Dockhand to call: once the environment is saved, env tries it, which says whether its agent has called in")
	}
	found, err := r.try(ctx, &connection)
	if err != nil {
		return reached{}, fmt.Errorf("the connection could not be tried: %w", err)
	}

	return found, nil
}

// one is the single environment a call names: a change is made to one environment, never to every one for want of a name.
func (r *registry) one(ctx context.Context, in envArg) (environment, error) {
	named, err := in.named()
	if err != nil {
		return environment{}, err
	}
	if named == "" {
		return environment{}, errors.New("env is required: the environment's name or number, as env_list gives them")
	}
	picked, err := r.pick(ctx, in)
	if err != nil {
		return environment{}, err
	}

	return picked[0], nil
}

// envChangeOut is what adding or changing an environment answers.
type envChangeOut struct {
	Environment *environmentRow `json:"environment,omitempty" jsonschema:"the environment as Dockhand has it now, read back; absent for a dry run of making one"`
	Found       *reached        `json:"found,omitempty"       jsonschema:"what trying the connection found; absent for an agent that is to call Dockhand, which there is nothing to try of until it does"`
	Token       string          `json:"token,omitempty"       jsonschema:"for a hawser-edge environment made with no agent_token: the token Dockhand made for its agent to call in with. Dockhand never shows it again: it is given to whoever starts the agent, and kept nowhere else"`
	Changes     []string        `json:"changes"               jsonschema:"what was done, in words; empty when nothing needed doing"`
	DryRun      bool            `json:"dry_run,omitempty"     jsonschema:"nothing was done: changes says what would be"`
	Note        string          `json:"note,omitempty"`
}

func registerEnvironmentWriteTools(r *registry) {
	type envTestIn struct {
		envArg
		connectionArgs
	}
	type envTestOut struct {
		Env string `json:"env,omitempty" jsonschema:"the environment that was tried; absent for a connection that is not saved"`

		reached

		Note string `json:"note,omitempty"`
	}
	add(r, readTool, &mcp.Tool{
		Name:        "env_test",
		Description: "Have Dockhand try to reach a Docker host and say what it found: the Docker's version, what it calls its machine, how many containers and images it has, and the Hawser agent in front of it with what that agent can do. Give env to try an environment that is saved, or a connection - its kind, and a socket path or a host - to try one before it is saved, which changes nothing on the Dockhand. A host that cannot be reached is an answer, not an error: reachable is false, with Dockhand's reason, such as an agent token that does not match, or an agent that is to call Dockhand and has not.",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in envTestIn) (*mcp.CallToolResult, envTestOut, error) {
		named, err := in.named()
		if err != nil {
			return nil, envTestOut{}, err
		}

		// a connection that is not saved
		if named == "" {
			var out envTestOut
			if out.reached, err = r.tryUnsaved(ctx, &in.connectionArgs); err != nil {
				return nil, envTestOut{}, err
			}
			out.Note = "this connection is not saved: nothing on the Dockhand was changed by trying it"

			return nil, out, nil
		}
		if in.Connection != "" || in.Host != "" || in.SocketPath != "" || in.AgentToken != "" {
			return nil, envTestOut{}, errors.New("give env, to try an environment that is saved, or a connection, to try one that is not: not both")
		}

		env, err := r.one(ctx, in.envArg)
		if err != nil {
			return nil, envTestOut{}, err
		}
		answer, err := r.dock.PostEnvironmentsByIdTest(ctx, env.ID)
		if err != nil {
			return nil, envTestOut{}, fmt.Errorf("%s could not be tried: %w", env.Name, refusal(err))
		}

		out := envTestOut{Env: env.Name}
		out.Reachable, out.Error = answer.Answer.Success, answer.Answer.Error
		if info := answer.Answer.Info; info != nil {
			out.DockerVersion, out.DockerHost, out.Containers, out.Images = info.ServerVersion, info.Name, info.Containers, info.Images
		}
		agentOf(answer.Answer.Hawser, &out.reached)

		return nil, out, nil
	})

	type envCreateIn struct {
		Name string `json:"name" jsonschema:"what to call the environment: the name every tool's env argument then takes"`

		connectionArgs

		Labels          []string `json:"labels,omitempty"           jsonschema:"labels to give it, which Dockhand groups and filters environments by"`
		SaveUnreachable bool     `json:"save_unreachable,omitempty" jsonschema:"save it though Dockhand cannot reach it now: for a host that is not up yet. Without this an environment that cannot be reached is not saved"`

		dryRunArg
	}
	add(r, writeTool, &mcp.Tool{
		Name:        "env_create",
		Description: "Add a Docker host to the Dockhand as an environment: by a Docker socket on the machine Dockhand runs on, by the address of a Docker API, by a Hawser agent on the host that Dockhand calls, with the token the agent was started with, or by a Hawser agent that calls Dockhand (hawser-edge), for a host Dockhand cannot reach itself. A connection Dockhand makes is tried first, and a host that cannot be reached is not saved unless save_unreachable says to; the answer gives what was found there. For an agent that calls in there is nothing to try yet: the environment is saved with a token for the agent, which the answer gives once, and the host is reached when an agent started with that token calls. The environment is then read back as Dockhand has it. A name already in use is refused. A token given to Dockhand is kept there and never given back.",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in envCreateIn) (*mcp.CallToolResult, envChangeOut, error) {
		name := strings.TrimSpace(in.Name)
		if name == "" {
			return nil, envChangeOut{}, errors.New("name is required: what to call the environment")
		}
		connection, err := in.checked()
		if err != nil {
			return nil, envChangeOut{}, err
		}

		// a name is one environment's: a second by it would make every tool's env argument mean two things
		existing, err := r.environments(ctx)
		if err != nil {
			return nil, envChangeOut{}, err
		}
		if slices.ContainsFunc(existing, func(e environment) bool { return strings.EqualFold(e.Name, name) }) {
			return nil, envChangeOut{}, fmt.Errorf("an environment is already named %q: env_list gives it, and another name is needed for another", name)
		}

		// an agent that calls Dockhand is another matter from here on: nothing to try, and a token to make
		if connection.Connection == connectionEdge {
			return r.addCalledBy(ctx, name, connection.AgentToken, in.Labels, in.DryRun)
		}

		found, err := r.try(ctx, &connection)
		if err != nil {
			return nil, envChangeOut{}, fmt.Errorf("the connection could not be tried: %w", err)
		}
		out := envChangeOut{Found: &found, Changes: []string{}, DryRun: in.DryRun}
		if !found.Reachable && !in.SaveUnreachable {
			return nil, out, fmt.Errorf("Dockhand cannot reach a Docker there, so %s was not saved: %s (save_unreachable saves it all the same)", name, cmp.Or(found.Error, "it gives no reason")) //nolint:staticcheck // ST1005: the sentence begins with the server's name
		}

		what := fmt.Sprintf("the environment %s, reached by %s", name, connection.Connection)
		if in.DryRun {
			out.Changes = append(out.Changes, "would add "+what)

			return nil, out, nil
		}

		made, err := r.dock.PostEnvironments(ctx, dockhand.PostEnvironmentsRequest{Name: name, ConnectionType: dockhand.ConnectionType(connection.Connection), SocketPath: connection.SocketPath, Host: connection.Host, Port: connection.Port, Protocol: dockhand.EnvironmentProtocol(connection.Protocol), HawserToken: connection.AgentToken, Labels: in.Labels})
		if err != nil {
			return nil, out, fmt.Errorf("%s was not added: %w", name, refusal(err))
		}
		out.Changes = append(out.Changes, "added "+what)

		if back, err := r.dock.GetEnvironmentsById(ctx, made.Answer.Id); err != nil {
			out.Note = "it was added, and could not be read back: " + err.Error()
		} else {
			row := environmentRowOf(&dockhand.GetEnvironmentsAnswer{Id: back.Answer.Id, Name: back.Answer.Name, ConnectionType: back.Answer.ConnectionType, Host: back.Answer.Host, Port: back.Answer.Port})
			row.Online = &found.Reachable
			out.Environment = &row
		}
		if !found.Reachable {
			out.Note = note(out.Note, "Dockhand cannot reach it yet: "+cmp.Or(found.Error, "it gives no reason"))
		}

		return nil, out, nil
	})

	type envDeleteIn struct {
		envArg

		Confirm bool `json:"confirm,omitempty" jsonschema:"remove it: without this the answer says what would go and nothing is removed"`
	}
	type envDeleteOut struct {
		Env        string   `json:"env"`
		Removed    bool     `json:"removed"            jsonschema:"the environment was removed, and is read back as gone"`
		WouldGo    []string `json:"would_go,omitempty" jsonschema:"what removing it takes away, said before anything is removed"`
		Containers int      `json:"containers"         jsonschema:"how many containers the host has, none of which is touched"`
		Note       string   `json:"note,omitempty"`
	}
	add(r, deleteTool, &mcp.Tool{
		Name:        "env_delete",
		Description: "Remove an environment from the Dockhand: it forgets the host, how it is reached and any agent token kept for it. Nothing on the host is touched: its containers, stacks, images and volumes stay as they are and go on running, and the host can be added again. Without confirm the answer says what would go and removes nothing; with confirm it is removed and read back as gone. It names one environment, by name or number.",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in envDeleteIn) (*mcp.CallToolResult, envDeleteOut, error) {
		env, err := r.one(ctx, in.envArg)
		if err != nil {
			return nil, envDeleteOut{}, err
		}

		out := envDeleteOut{Env: env.Name}
		// how much is on the host is said beside what would go, so that what is left alone is as plain as what is not; a host that cannot be asked is
		// no reason not to forget it
		if listed, err := r.containersOf(ctx, env); err == nil {
			out.Containers = len(listed)
		}
		if !in.Confirm {
			out.WouldGo = []string{fmt.Sprintf("Dockhand's record of %s: how it reaches the host, and any agent token or TLS key kept for it", env.Name)}
			out.Note = fmt.Sprintf("nothing was removed: call again with confirm to remove it. The %d containers on the host are not touched either way", out.Containers)

			return nil, out, nil
		}

		gone, err := r.dock.DeleteEnvironmentsById(ctx, env.ID)
		if err != nil {
			return nil, out, fmt.Errorf("%s was not removed: %w", env.Name, refusal(err))
		}
		if !gone.Answer.Success {
			return nil, out, fmt.Errorf("the removal of %s was answered without its being said to have succeeded: env_list says whether it is still there", env.Name)
		}

		// read back: it is gone when the list no longer has it
		left, err := r.environments(ctx)
		if err != nil {
			out.Note = "it was removed, and the environments could not be listed afterwards: " + err.Error()

			return nil, out, nil //nolint:nilerr // the removal went through: that it could not be read back is said in the answer
		}
		if slices.ContainsFunc(left, func(e environment) bool { return e.ID == env.ID }) {
			return nil, out, fmt.Errorf("the removal of %s was answered as done, and it is still listed", env.Name)
		}
		out.Removed = true
		out.Note = fmt.Sprintf("the %d containers on the host were not touched", out.Containers)

		return nil, out, nil
	})
}
