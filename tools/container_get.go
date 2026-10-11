package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"maps"
	"slices"
	"strings"
	"time"

	"github.com/katbyte/dockhand-mcp/sdk/dockhand"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// inspected is the part of Docker's own inspect document, which Dockhand hands on as it gets it, that the tools read. Docker's document is some two
// hundred fields; these are the ones that say what a container is, how it is doing and what it was given.
type inspected struct {
	ID           string   `json:"Id"`
	Name         string   `json:"Name"`
	Created      string   `json:"Created"`
	Path         string   `json:"Path"`
	Args         []string `json:"Args"`
	RestartCount int      `json:"RestartCount"`
	State        struct {
		Status     string `json:"Status"`
		Running    bool   `json:"Running"`
		OOMKilled  bool   `json:"OOMKilled"`
		ExitCode   int    `json:"ExitCode"`
		Error      string `json:"Error"`
		StartedAt  string `json:"StartedAt"`
		FinishedAt string `json:"FinishedAt"`
		Health     *struct {
			Status        string `json:"Status"`
			FailingStreak int    `json:"FailingStreak"`
			Log           []struct {
				End      string `json:"End"`
				ExitCode int    `json:"ExitCode"`
				Output   string `json:"Output"`
			} `json:"Log"`
		} `json:"Health"`
	} `json:"State"`
	Config struct {
		Image  string            `json:"Image"`
		Env    []string          `json:"Env"`
		Labels map[string]string `json:"Labels"`
		User   string            `json:"User"`
	} `json:"Config"`
	HostConfig struct {
		RestartPolicy struct {
			Name string `json:"Name"`
		} `json:"RestartPolicy"`
		Memory       int64    `json:"Memory"`
		NanoCpus     int64    `json:"NanoCpus"`
		Privileged   bool     `json:"Privileged"`
		NetworkMode  string   `json:"NetworkMode"`
		Binds        []string `json:"Binds"`
		PortBindings map[string][]struct {
			HostIP   string `json:"HostIp"`
			HostPort string `json:"HostPort"`
		} `json:"PortBindings"`
	} `json:"HostConfig"`
	Mounts []struct {
		Type        string `json:"Type"`
		Name        string `json:"Name"`
		Source      string `json:"Source"`
		Destination string `json:"Destination"`
		RW          bool   `json:"RW"`
	} `json:"Mounts"`
	NetworkSettings struct {
		Ports map[string][]struct {
			HostIP   string `json:"HostIp"`
			HostPort string `json:"HostPort"`
		} `json:"Ports"`
		Networks map[string]struct {
			IPAddress string `json:"IPAddress"`
		} `json:"Networks"`
	} `json:"NetworkSettings"`
}

// publishes is the ports the container was made to publish, as docker run writes them: whether it is running or not, which the ports it has open
// right now do not say.
func (d *inspected) publishes() []string {
	published := []string{}
	for _, port := range slices.Sorted(maps.Keys(d.HostConfig.PortBindings)) {
		inside, protocol, _ := strings.Cut(port, "/")
		for _, to := range d.HostConfig.PortBindings[port] {
			entry := to.HostPort + ":" + inside
			if to.HostIP != "" {
				entry = to.HostIP + ":" + entry
			}
			if protocol != "" && protocol != "tcp" {
				entry += "/" + protocol
			}
			published = append(published, entry)
		}
	}

	return published
}

// never is how Docker writes a time that has not come: it is when a container that is still running is said to have stopped.
const never = "0001-01-01T00:00:00Z"

// dockerTime is a time as Docker wrote it, as every tool gives one: UTC, to the second. "" is a time Docker did not give, or its "never".
func dockerTime(written string) string {
	if written == "" || written == never {
		return ""
	}
	t, err := time.Parse(time.RFC3339Nano, written)
	if err != nil {
		return ""
	}

	return t.UTC().Format(time.RFC3339)
}

// variable is one of the variables a container was started with.
type variable struct {
	Name   string `json:"name"`
	Value  string `json:"value"            jsonschema:"the variable's value, or *** in place of one that is hidden"`
	Hidden bool   `json:"hidden,omitempty" jsonschema:"the value, or a password in it, is hidden: reveal_secrets shows it"`
}

// mount is something of the host's, or a volume, that a container sees.
type mount struct {
	Type        string `json:"type"                jsonschema:"bind for a path of the host's, volume for a Docker volume"`
	Source      string `json:"source"              jsonschema:"the volume's name, or the path on the host"`
	Destination string `json:"destination"         jsonschema:"where the container sees it"`
	ReadOnly    bool   `json:"read_only,omitempty"`
}

// health is what a container's own health check says.
type health struct {
	Status        string `json:"status"                 jsonschema:"healthy, unhealthy or starting"`
	FailingStreak int    `json:"failing_streak"         jsonschema:"how many checks in a row have failed"`
	LastChecked   string `json:"last_checked,omitempty" jsonschema:"when the latest check finished, in UTC"`
	LastExitCode  int    `json:"last_exit_code"         jsonschema:"what the latest check exited with: 0 is healthy"`
	LastOutput    string `json:"last_output,omitempty"  jsonschema:"what the latest check printed, which is usually why it failed"`
}

// containerDetail is one container as container_get answers with it.
type containerDetail struct {
	Env     string `json:"env"               jsonschema:"the environment (Docker host) the container is on"`
	Name    string `json:"name"`
	ID      string `json:"id"                jsonschema:"the container's whole id"`
	Image   string `json:"image"`
	Created string `json:"created,omitempty" jsonschema:"when the container was made, in UTC"`
	Command string `json:"command,omitempty" jsonschema:"what the container runs, with any secret on the command line hidden"`
	User    string `json:"user,omitempty"    jsonschema:"the user it runs as, when the image's own was overridden"`
	Stack   string `json:"stack,omitempty"   jsonschema:"the compose stack (project) the container belongs to"`
	Service string `json:"service,omitempty" jsonschema:"the compose service the container is"`

	State     string  `json:"state"               jsonschema:"running, exited, restarting, paused, created or dead"`
	Started   string  `json:"started,omitempty"   jsonschema:"when it last started, in UTC"`
	Finished  string  `json:"finished,omitempty"  jsonschema:"when it last stopped, in UTC; absent for one that never has"`
	ExitCode  *int    `json:"exit_code,omitempty" jsonschema:"what it exited with, for one that is not running"`
	OOMKilled bool    `json:"oom_killed"          jsonschema:"the host killed it for using more memory than it may"`
	Error     string  `json:"error,omitempty"     jsonschema:"what Docker says went wrong starting it"`
	Health    *health `json:"health,omitempty"    jsonschema:"what its own health check says; absent for a container with none"`
	Restarts  int     `json:"restarts"            jsonschema:"how many times Docker has started it again after it exited"`
	Problem   string  `json:"problem,omitempty"   jsonschema:"what is wrong with the container, when something is"`

	RestartPolicy string  `json:"restart_policy,omitempty"     jsonschema:"when Docker starts it again by itself: no, always, unless-stopped or on-failure"`
	MemoryLimit   int64   `json:"memory_limit_bytes,omitempty" jsonschema:"the most memory it may use; absent for one with no limit"`
	CPULimit      float64 `json:"cpu_limit,omitempty"          jsonschema:"how many CPUs it may use; absent for one with no limit"`
	Privileged    bool    `json:"privileged,omitempty"         jsonschema:"it runs with every capability of the host"`
	NetworkMode   string  `json:"network_mode,omitempty"`

	Environment []variable        `json:"environment"    jsonschema:"the variables it was started with, by name; a value whose name says it is a secret is hidden"`
	Mounts      []mount           `json:"mounts"`
	Ports       []string          `json:"ports"          jsonschema:"each port as host->container/protocol, or the container's alone for one that is exposed and not published"`
	Networks    map[string]string `json:"networks"       jsonschema:"its address on each network it is on"`
	Labels      map[string]string `json:"labels"         jsonschema:"its labels; a value whose name says it is a secret is hidden"`
	Hidden      int               `json:"hidden"         jsonschema:"how many values above are hidden as secrets; reveal_secrets shows them"`
	Note        string            `json:"note,omitempty"`
}

// detailOf is a container's inspect document as the tools give it, with its secrets hidden unless they are asked for.
func detailOf(at *located, doc *inspected, reveal bool) containerDetail {
	out := containerDetail{
		Env: at.Env.Name, Name: strings.TrimPrefix(doc.Name, "/"), ID: doc.ID, Image: doc.Config.Image, Created: dockerTime(doc.Created), User: doc.Config.User,
		Stack: doc.Config.Labels[labelStack], Service: doc.Config.Labels[labelService],
		State: doc.State.Status, Started: dockerTime(doc.State.StartedAt), Finished: dockerTime(doc.State.FinishedAt), OOMKilled: doc.State.OOMKilled, Error: doc.State.Error, Restarts: doc.RestartCount,
		RestartPolicy: doc.HostConfig.RestartPolicy.Name, MemoryLimit: doc.HostConfig.Memory, CPULimit: float64(doc.HostConfig.NanoCpus) / 1e9, Privileged: doc.HostConfig.Privileged, NetworkMode: doc.HostConfig.NetworkMode,
		Environment: make([]variable, 0, len(doc.Config.Env)), Mounts: make([]mount, 0, len(doc.Mounts)), Ports: []string{}, Networks: map[string]string{}, Labels: map[string]string{},
	}
	hide := func(name, value string) (string, bool) {
		if reveal {
			return value, false
		}
		shown, hidden := maskValue(name, value)
		if hidden {
			out.Hidden++
		}

		return shown, hidden
	}

	command := strings.TrimSpace(doc.Path + " " + strings.Join(doc.Args, " "))
	if !reveal {
		var hidden int
		command, hidden = maskText(command)
		out.Hidden += hidden
	}
	out.Command = command

	if !doc.State.Running {
		out.ExitCode = &doc.State.ExitCode
	}
	if h := doc.State.Health; h != nil {
		out.Health = &health{Status: h.Status, FailingStreak: h.FailingStreak}
		if len(h.Log) > 0 {
			last := h.Log[len(h.Log)-1]
			out.Health.LastChecked, out.Health.LastExitCode, out.Health.LastOutput = dockerTime(last.End), last.ExitCode, strings.TrimSpace(last.Output)
			if !reveal {
				var hidden int
				out.Health.LastOutput, hidden = maskText(out.Health.LastOutput)
				out.Hidden += hidden
			}
		}
	}
	// what is wrong is said the way the list says it, from what the list knows of the container, with what only this knows added
	out.Problem = at.row().Problem
	if doc.State.OOMKilled {
		out.Problem = note(out.Problem, "the host killed it for using more memory than it may")
	}

	for _, entry := range doc.Config.Env {
		name, value, _ := strings.Cut(entry, "=")
		shown, hidden := hide(name, value)
		out.Environment = append(out.Environment, variable{Name: name, Value: shown, Hidden: hidden})
	}
	slices.SortFunc(out.Environment, func(a, b variable) int { return strings.Compare(a.Name, b.Name) })

	for _, m := range doc.Mounts {
		source := m.Source
		if m.Type == "volume" && m.Name != "" {
			source = m.Name
		}
		out.Mounts = append(out.Mounts, mount{Type: m.Type, Source: source, Destination: m.Destination, ReadOnly: !m.RW})
	}
	slices.SortFunc(out.Mounts, func(a, b mount) int { return strings.Compare(a.Destination, b.Destination) })

	for port, bound := range doc.NetworkSettings.Ports {
		if len(bound) == 0 {
			out.Ports = append(out.Ports, port)
		}
		for _, b := range bound {
			// a port published on every address is listed by Docker once for each
			if published := b.HostPort + "->" + port; !slices.Contains(out.Ports, published) {
				out.Ports = append(out.Ports, published)
			}
		}
	}
	slices.Sort(out.Ports)

	for name, network := range doc.NetworkSettings.Networks {
		out.Networks[name] = network.IPAddress
	}
	for name, value := range doc.Config.Labels {
		out.Labels[name], _ = hide(name, value)
	}
	if out.Hidden > 0 {
		out.Note = hiddenNote(out.Hidden, "told by their names")
	}

	return out
}

// inspect reads a container's inspect document from the environment it is on.
func (r *registry) inspect(ctx context.Context, at *located) (*inspected, error) {
	answer, err := r.dock.GetContainersByIdInspect(ctx, at.Container.Id, dockhand.GetContainersByIdInspectOperationOptions{Env: &at.Env.ID})
	if err != nil {
		return nil, fmt.Errorf("%s on %s could not be inspected: %w", at.Container.Name, at.Env.Name, refusal(err))
	}

	var doc inspected
	if err := json.Unmarshal(answer.Model, &doc); err != nil {
		return nil, fmt.Errorf("%s on %s: what was answered is not Docker's inspect document: %w", at.Container.Name, at.Env.Name, err)
	}

	return &doc, nil
}

func registerContainerGetTool(r *registry) {
	type containerGetIn struct {
		containerArg
		revealArg
	}
	add(r, readTool, &mcp.Tool{
		Name:        "container_get",
		Description: "Describe one container in full: what it runs and from which image, its state with when it started and stopped, its exit code and whether the host killed it for memory, what its health check says and the output of the latest check, how often Docker has restarted it and when it would again, its memory and CPU limits, the variables it was started with, its mounts, ports, networks and labels. Name it as container_list does, by name or the start of its id; env is only needed when the same name is on more than one host. Values whose names say they are secrets - passwords, tokens, keys - are hidden, here and in the command line, unless reveal_secrets asks for them. This is where to look for why a container will not stay up.",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in containerGetIn) (*mcp.CallToolResult, containerDetail, error) {
		at, err := r.find(ctx, in.containerArg)
		if err != nil {
			return nil, containerDetail{}, err
		}
		doc, err := r.inspect(ctx, &at)
		if err != nil {
			return nil, containerDetail{}, err
		}

		return nil, detailOf(&at, doc, in.RevealSecrets), nil
	})
}
