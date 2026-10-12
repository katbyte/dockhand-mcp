package tools

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"maps"
	"math"
	"net"
	"slices"
	"strconv"
	"strings"

	"github.com/katbyte/dockhand-mcp/sdk/dockhand"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// restartOnFailure is the one restart policy that takes a count: how many times Docker starts a container again after it fails.
const restartOnFailure = "on-failure"

// restartPolicies is what Docker takes for when to start a container again by itself.
var restartPolicies = []string{"no", "always", "unless-stopped", restartOnFailure}

// minMemoryMB is the least memory Docker lets a container be limited to.
const minMemoryMB = 6

// shortened is the start of a container's id, which is how Docker shows one.
func shortened(id string) string {
	if len(id) > shortID {
		return id[:shortID]
	}

	return id
}

// unnamed reports whether what a container says its image is, is an id and no name: what is left when the name it was made from has gone from the
// host.
func unnamed(image string) bool {
	if strings.HasPrefix(image, "sha256:") {
		return true
	}

	return len(image) >= shortID && strings.Trim(image, "0123456789abcdef") == ""
}

// isPort is why what was given is no port as Docker takes one, or nil.
func isPort(given, what string) error {
	if n, err := strconv.Atoi(given); err != nil || n < 1 || n > 65535 {
		return fmt.Errorf("%q is no %s: a port is a number from 1 to 65535", given, what)
	}

	return nil
}

// publishedOf is the ports a container is to publish as Dockhand takes them, from the way docker run writes them: 8080:80, 127.0.0.1:8080:80,
// 53:53/udp, or 80 alone for a port of the host's choosing. Dockhand takes one host port for a port of the container.
func publishedOf(given []string) (map[string]any, error) {
	const form = `a port is written host:container, as 8080:80, with /udp after it for udp and an address before it to publish on that address alone, as 127.0.0.1:8080:80`

	published := map[string]any{}
	for _, entry := range given {
		spec, protocol, _ := strings.Cut(strings.TrimSpace(entry), "/")
		protocol = cmp.Or(strings.ToLower(protocol), "tcp")
		if protocol != "tcp" && protocol != "udp" && protocol != "sctp" {
			return nil, fmt.Errorf("ports has %q, whose protocol is %q: it is tcp, udp or sctp", entry, protocol)
		}

		var address, host, inside string
		switch parts := strings.Split(spec, ":"); len(parts) {
		case 1:
			inside = parts[0]
		case 2:
			host, inside = parts[0], parts[1]
		case 3:
			address, host, inside = parts[0], parts[1], parts[2]
		default:
			return nil, fmt.Errorf("ports has %q: %s", entry, form)
		}
		if address != "" && net.ParseIP(address) == nil {
			return nil, fmt.Errorf("ports has %q, whose address %q is no IP address: %s", entry, address, form)
		}
		if err := isPort(inside, "port of the container"); err != nil {
			return nil, fmt.Errorf("ports has %q: %w; %s", entry, err, form)
		}
		if host != "" {
			if err := isPort(host, "port of the host"); err != nil {
				return nil, fmt.Errorf("ports has %q: %w; %s", entry, err, form)
			}
		}

		key := inside + "/" + protocol
		if _, twice := published[key]; twice {
			return nil, fmt.Errorf("ports publishes %s twice: Dockhand takes one port of the host for a port of the container", key)
		}
		binding := map[string]any{"HostPort": host}
		if address != "" {
			binding["HostIp"] = address
		}
		published[key] = binding
	}

	return published, nil
}

// mountedOf is the volumes and folders a container is to mount, checked and trimmed: each a volume's name or a folder of the host, where it goes in
// the container, and ro for one the container may only read.
func mountedOf(given []string) ([]string, error) {
	const form = `a mount is written source:target, as data:/var/lib/app for a volume or /srv/conf:/etc/app:ro for a folder of the host the container may only read`

	mounted := []string{}
	for _, entry := range given {
		parts := strings.Split(strings.TrimSpace(entry), ":")
		switch {
		case len(parts) < 2 || len(parts) > 3:
			return nil, fmt.Errorf("volumes has %q: %s", entry, form)
		case !strings.HasPrefix(parts[0], "/") && !volumeName(parts[0]):
			return nil, fmt.Errorf("volumes has %q, whose source %q is neither a volume's name nor a folder of the host from its root: %s", entry, parts[0], form)
		case !strings.HasPrefix(parts[1], "/"):
			return nil, fmt.Errorf("volumes has %q, whose target %q is not a path from the container's root: %s", entry, parts[1], form)
		case len(parts) == 3 && parts[2] != "ro" && parts[2] != "rw":
			return nil, fmt.Errorf("volumes has %q, which ends in %q: it is ro, rw or left off", entry, parts[2])
		default:
		}
		mounted = append(mounted, strings.Join(parts, ":"))
	}

	return mounted, nil
}

// restartOf is a restart policy as it is given, on-failure:3 among them, taken apart: the policy, and how many times a failure is tried again.
func restartOf(given string) (policy string, retries int, err error) {
	policy, count, counted := strings.Cut(strings.ToLower(strings.TrimSpace(given)), ":")
	switch {
	case policy == "" && !counted:
		return "", 0, nil
	case !slices.Contains(restartPolicies, policy) || (counted && policy != restartOnFailure):
		return "", 0, fmt.Errorf("restart is %q: it is no, always, unless-stopped or on-failure, and on-failure may say how many times to try, as on-failure:3", given)
	case counted:
		if retries, err = strconv.Atoi(count); err != nil || retries < 1 {
			return "", 0, fmt.Errorf("restart is %q: what follows on-failure is how many times to try, a number above 0", given)
		}
	default:
	}

	return policy, retries, nil
}

// variablesOf is variables as Docker takes them, NAME=value and in order of name, or the name that is none.
func variablesOf(vars map[string]string) ([]string, error) {
	listed := make([]string, 0, len(vars))
	for _, name := range slices.Sorted(maps.Keys(vars)) {
		if !variableNamed.MatchString(name) {
			return nil, fmt.Errorf("env_vars names %q: a variable's name is letters, digits and underscores, and does not start with a digit", name)
		}
		listed = append(listed, name+"="+vars[name])
	}

	return listed, nil
}

// labelled is labels as Dockhand takes them.
func labelled(labels map[string]string) map[string]any {
	out := make(map[string]any, len(labels))
	for name, value := range labels {
		out[name] = value
	}

	return out
}

// dockhandsOwn is why a container that is Dockhand itself, or one of its agents, is not made again through Dockhand; nothing for any other.
func dockhandsOwn(c *dockhand.GetContainersAnswer) error {
	if c.SystemContainer == "" {
		return nil
	}

	return fmt.Errorf("%s is Dockhand's own (%s): making it again through Dockhand stops the very thing doing the work part way, so it is left alone here and is updated where it was installed", c.Name, c.SystemContainer)
}

// ofStack is what is said of changing a container compose made by hand.
func ofStack(c *dockhand.GetContainersAnswer) string {
	stack := c.Labels[labelStack]
	if stack == "" {
		return ""
	}

	return fmt.Sprintf("it belongs to the stack %s, whose file still says what it was: the stack's next deploy makes the container again from the file", stack)
}

// containerNamed is the container a host has under a name, or nil when it has none.
func (r *registry) containerNamed(ctx context.Context, env environment, name string) (*dockhand.GetContainersAnswer, error) {
	listed, err := r.containersOf(ctx, env)
	if err != nil {
		return nil, err
	}
	if i := slices.IndexFunc(listed, func(c dockhand.GetContainersAnswer) bool { return c.Name == name }); i >= 0 {
		return &listed[i], nil
	}

	return nil, nil //nolint:nilnil // no container of the name is an answer, and nil says it
}

// remade is how a container that was to be made, or made again, stands afterwards, read from the host: its id and state, or why they could not be
// read.
func (r *registry) remade(ctx context.Context, env environment, name string) (id, state, note string) {
	is, err := r.containerNamed(ctx, env, name)
	switch {
	case err != nil:
		return "", "", "how it stands could not be read back: " + err.Error()
	case is == nil:
		return "", "", "no container of that name is listed on " + env.Name + " now"
	default:
		return shortened(is.Id), string(is.State), ""
	}
}

//nolint:gocyclo,maintidx // five tools registered in one function, each a handler of its own: the count is theirs added up
func registerContainerMakeTools(r *registry) {
	type containerCreateIn struct {
		envArg
		dryRunArg
		waitArg

		Name       string            `json:"name"                 jsonschema:"what to call it: letters, digits, dots, hyphens and underscores, starting with a letter or a digit"`
		Image      string            `json:"image"                jsonschema:"the image to make it from, by name and tag, such as nginx:1.27; one the host has not got is pulled first"`
		Command    []string          `json:"command,omitempty"    jsonschema:"what to run in place of the image's own command, one word to an entry: [\"sleep\", \"3600\"]"`
		Entrypoint []string          `json:"entrypoint,omitempty" jsonschema:"what to run the command with in place of the image's own entrypoint"`
		EnvVars    map[string]string `json:"env_vars,omitempty"   jsonschema:"the variables to give it, by name"`
		Ports      []string          `json:"ports,omitempty"      jsonschema:"the ports to publish, each as host:container: 8080:80, 53:53/udp, 127.0.0.1:8080:80 to publish on one address, or 80 alone for a port of the host's choosing"`
		Volumes    []string          `json:"volumes,omitempty"    jsonschema:"what to mount, each as source:target: data:/var/lib/app for a named volume, made when the host has none of the name, or /srv/conf:/etc/app:ro for a folder of the host it may only read"`
		Networks   []string          `json:"networks,omitempty"   jsonschema:"the networks to put it on, by name, the first being the one it is made on; left out, the host's default bridge. host and none are Docker's own two"`
		Restart    string            `json:"restart,omitempty"    jsonschema:"when Docker starts it again by itself: no, which is the default, always, unless-stopped, or on-failure, which may say how many times as on-failure:3"`
		Labels     map[string]string `json:"labels,omitempty"`
		Workdir    string            `json:"workdir,omitempty"    jsonschema:"the directory its command runs in, in place of the image's own"`
		Start      *bool             `json:"start,omitempty"      jsonschema:"start it once it is made, which is the default; false makes it and leaves it not started"`
	}
	type containerCreateOut struct {
		jobState

		Env     string   `json:"env"               jsonschema:"the environment (Docker host) it was made on"`
		Name    string   `json:"name"`
		Image   string   `json:"image"`
		ID      string   `json:"id,omitempty"      jsonschema:"the new container's id, shortened"`
		State   string   `json:"state"             jsonschema:"how it stands, read back from the host: running, created for one made and not started, exited for one that started and ended; empty when it was not made"`
		Changes []string `json:"changes"           jsonschema:"what was done, in words"`
		DryRun  bool     `json:"dry_run,omitempty" jsonschema:"nothing was done: changes says what would be"`
		Note    string   `json:"note,omitempty"`
	}
	add(r, writeTool, &mcp.Tool{
		Name:        "container_create",
		Description: "Make one new container on a Docker host (environment) and start it, as docker run does: from an image, with the command, variables, published ports, mounts, networks, restart policy and labels given. It names one environment. A name another container on the host has is refused, and so is a network the host has none of. An image the host has not got is pulled first, in the background on Dockhand: this waits up to wait seconds for the pull and, when it is still going then, answers with running true and the job's id and has made nothing yet, so the call is made again once job_get says the pull is done. A container that is made and cannot be started, because a port is taken for one, is left as it was made and the error says so. For more than one container that belong together, a compose stack is the better tool. Read back from the host.",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in containerCreateIn) (*mcp.CallToolResult, containerCreateOut, error) {
		name, image := strings.TrimPrefix(strings.TrimSpace(in.Name), "/"), strings.TrimSpace(in.Image)
		switch {
		case !containerNamed.MatchString(name):
			return nil, containerCreateOut{}, fmt.Errorf("name is %q: a container's name is letters, digits, dots, hyphens and underscores, two or more, starting with a letter or a digit", in.Name)
		case image == "" || strings.ContainsAny(image, " \t\n"):
			return nil, containerCreateOut{}, fmt.Errorf("image is %q: it is the name and tag to make the container from, such as nginx:1.27 or ghcr.io/org/app:v2", in.Image)
		default:
		}
		variables, err := variablesOf(in.EnvVars)
		if err != nil {
			return nil, containerCreateOut{}, err
		}
		published, err := publishedOf(in.Ports)
		if err != nil {
			return nil, containerCreateOut{}, err
		}
		mounted, err := mountedOf(in.Volumes)
		if err != nil {
			return nil, containerCreateOut{}, err
		}
		policy, retries, err := restartOf(in.Restart)
		if err != nil {
			return nil, containerCreateOut{}, err
		}
		patience, err := in.patience()
		if err != nil {
			return nil, containerCreateOut{}, err
		}
		start := in.Start == nil || *in.Start

		env, err := r.one(ctx, in.envArg)
		if err != nil {
			return nil, containerCreateOut{}, err
		}
		out := containerCreateOut{Env: env.Name, Name: name, Image: image, Changes: []string{}, DryRun: in.DryRun}

		taken, err := r.containerNamed(ctx, env, name)
		if err != nil {
			return nil, out, fmt.Errorf("the containers of %s could not be listed, so nothing was made: %w", env.Name, err)
		}
		if taken != nil {
			return nil, out, fmt.Errorf("%s has a container named %s already, which is %s: choose another name, or remove that one first", env.Name, name, taken.State)
		}

		networks := []string{}
		for _, network := range in.Networks {
			if network = strings.TrimSpace(network); network != "" && !slices.Contains(networks, network) {
				networks = append(networks, network)
			}
		}
		if len(networks) > 0 {
			has, failed := r.networksOf(ctx, env)
			if failed != nil {
				return nil, out, fmt.Errorf("the networks of %s could not be listed, so nothing was made: %w", env.Name, failed)
			}
			for _, network := range networks {
				if !slices.ContainsFunc(has.Networks, func(n dockhand.GetNetworksAnswer) bool { return n.Name == network }) {
					return nil, out, fmt.Errorf("%s has no network named %s: make it first, or name one it has", env.Name, network)
				}
			}
		}

		there, err := r.imageNamed(ctx, env, image)
		if err != nil {
			return nil, out, fmt.Errorf("the images of %s could not be listed, so nothing was made: %w", env.Name, err)
		}
		would, did := "would make "+name+" on "+env.Name+" from "+image, "made "+name+" on "+env.Name+" from "+image
		if start {
			would, did = would+" and start it", did+" and started it"
		}
		if in.DryRun {
			if there == "" {
				out.Changes = append(out.Changes, "would pull "+image+" to "+env.Name+", which has not got it")
			}
			out.Changes = append(out.Changes, would)

			return nil, out, nil
		}

		if there == "" {
			state, failed := r.pull(ctx, env, image, patience)
			out.jobState = state
			if failed != nil {
				return nil, out, fmt.Errorf("%s has not got %s and it could not be pulled, so nothing was made: %w", env.Name, image, failed)
			}
			if state.Running {
				out.Note = fmt.Sprintf("%s has not got %s and the pull was still running after %s: nothing is made yet. job_get with job %s says when the pull is done, and this call made again then makes the container", env.Name, image, seconds(patience), state.Job)

				return nil, out, nil
			}
			out.Changes = append(out.Changes, "pulled "+image+" to "+env.Name)
		}

		request := dockhand.PostContainersRequest{Name: name, Image: image, Cmd: in.Command, Entrypoint: in.Entrypoint, Env: variables, VolumeBinds: mounted, RestartPolicy: policy, RestartMaxRetries: retries, WorkingDir: strings.TrimSpace(in.Workdir), StartAfterCreate: new(start)}
		if len(published) > 0 {
			request.Ports = published
		}
		if len(in.Labels) > 0 {
			request.Labels = labelled(in.Labels)
		}
		if len(networks) > 0 {
			request.NetworkMode, request.AdditionalNetworks = networks[0], networks[1:]
		}

		_, err = r.dock.PostContainers(ctx, request, dockhand.PostContainersOptions{Env: env.ID})
		var note string
		out.ID, out.State, note = r.remade(ctx, env, name)
		if err != nil {
			// Dockhand makes the container and then starts it, and one it could not start is left as it was made
			if out.ID != "" {
				return nil, out, fmt.Errorf("%s was made on %s and could not be started: %w. It is left as it was made, %s: put right what stopped it and start it, or remove it", name, env.Name, refusal(err), out.State)
			}

			return nil, out, fmt.Errorf("%s was not made on %s: %w", name, env.Name, refusal(err))
		}
		out.Changes = append(out.Changes, did)

		switch {
		case note != "":
			out.Note = note
		case start && out.State != stateRunning:
			out.Note = fmt.Sprintf("it was started and is %s, which is what a command that ends at once or fails leaves: its logs say why", out.State)
		default:
		}

		return nil, out, nil
	})

	type containerEditIn struct {
		containerArg
		dryRunArg

		EnvVars       map[string]string `json:"env_vars,omitempty"        jsonschema:"variables to set or change, by name; the ones not named are kept"`
		RemoveEnvVars []string          `json:"remove_env_vars,omitempty" jsonschema:"variables to take away, by name"`
		Labels        map[string]string `json:"labels,omitempty"          jsonschema:"labels to set or change, by name; the ones not named are kept"`
		RemoveLabels  []string          `json:"remove_labels,omitempty"   jsonschema:"labels to take away, by name"`
		Ports         []string          `json:"ports,omitempty"           jsonschema:"every port it is to publish, in place of the ones it publishes now, each as host:container: 8080:80, 53:53/udp, 127.0.0.1:8080:80. An empty list publishes none; left out, its ports are kept"`
		Volumes       []string          `json:"volumes,omitempty"         jsonschema:"everything it is to mount, in place of what it mounts now, each as source:target: data:/var/lib/app, /srv/conf:/etc/app:ro. A mount left off the list is no longer mounted, though what it held stays where it was; left out, its mounts are kept"`
		Command       []string          `json:"command,omitempty"         jsonschema:"what it is to run, in place of what it runs now, one word to an entry"`
	}
	type containerEditOut struct {
		Env     string   `json:"env"               jsonschema:"the environment (Docker host) the container is on"`
		Name    string   `json:"name"`
		ID      string   `json:"id,omitempty"      jsonschema:"its id, shortened: a new one once it was made again"`
		Was     string   `json:"was"               jsonschema:"its state before"`
		Is      string   `json:"is"                jsonschema:"its state after, read back from the host; with dry_run, its state as it stands"`
		Changes []string `json:"changes"           jsonschema:"what the container made again has that the one before had not, in words, variables and labels by name and never by value, and last what was done to make it so"`
		DryRun  bool     `json:"dry_run,omitempty" jsonschema:"nothing was done: changes says what would be"`
		Note    string   `json:"note,omitempty"`
	}
	add(r, writeTool, &mcp.Tool{
		Name:        "container_edit",
		Description: "Change what one container was made with: its variables, labels, published ports, mounts or command. Docker cannot change these on a container that exists, so Dockhand stops it, removes it and makes it again under the same name from the same image with the change, keeping everything not named here: its networks, restart policy and limits among them. It is started again when it was running, and what it wrote inside itself, outside its volumes, is gone, as with any container made again. Variables and labels are changed one by one; ports, mounts and the command are each given whole. A container Dockhand or its agent runs as is refused. Memory, processors and the restart policy are changed without making it again, by container_limits. Read back from the host.",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in containerEditIn) (*mcp.CallToolResult, containerEditOut, error) {
		setVariables, err := variablesOf(in.EnvVars)
		if err != nil {
			return nil, containerEditOut{}, err
		}
		for _, name := range slices.Concat(in.RemoveLabels, slices.Collect(maps.Keys(in.Labels))) {
			if strings.HasPrefix(name, "com.docker.compose.") {
				return nil, containerEditOut{}, fmt.Errorf("the label %s is one compose keeps to know its own containers by: it is not changed here", name)
			}
		}
		var published map[string]any
		if in.Ports != nil {
			if published, err = publishedOf(in.Ports); err != nil {
				return nil, containerEditOut{}, err
			}
		}
		var mounted []string
		if in.Volumes != nil {
			if mounted, err = mountedOf(in.Volumes); err != nil {
				return nil, containerEditOut{}, err
			}
		}
		if len(setVariables)+len(in.RemoveEnvVars)+len(in.Labels)+len(in.RemoveLabels)+len(in.Command) == 0 && in.Ports == nil && in.Volumes == nil {
			return nil, containerEditOut{}, errors.New("nothing to change was given: env_vars, remove_env_vars, labels, remove_labels, ports, volumes or command")
		}

		at, err := r.find(ctx, in.containerArg)
		if err != nil {
			return nil, containerEditOut{}, err
		}
		c := &at.Container
		out := containerEditOut{Env: at.Env.Name, Name: c.Name, ID: shortened(c.Id), Was: string(c.State), Is: string(c.State), Changes: []string{}, DryRun: in.DryRun}
		if err := dockhandsOwn(c); err != nil {
			return nil, out, err
		}
		doc, err := r.inspect(ctx, &at)
		if err != nil {
			return nil, out, fmt.Errorf("%w, so nothing was changed: what it has now has to be read to be kept", err)
		}

		request := dockhand.PostContainersByIdUpdateRequest{StartAfterUpdate: new(running(string(c.State)))}

		// variables: the ones it has, in their order, with the named ones changed or gone and the new ones after
		if len(in.EnvVars)+len(in.RemoveEnvVars) > 0 {
			variables, seen := []string{}, map[string]bool{}
			for _, held := range doc.Config.Env {
				name, value, _ := strings.Cut(held, "=")
				seen[name] = true
				to, set := in.EnvVars[name]
				switch {
				case slices.Contains(in.RemoveEnvVars, name):
					out.Changes = append(out.Changes, "the variable "+name+" is taken away")

					continue
				case set && to != value:
					out.Changes = append(out.Changes, "the variable "+name+" is changed")
					held = name + "=" + to
				default:
				}
				variables = append(variables, held)
			}
			for _, given := range setVariables {
				if name, _, _ := strings.Cut(given, "="); !seen[name] {
					out.Changes = append(out.Changes, "the variable "+name+" is set")
					variables = append(variables, given)
				}
			}
			if len(out.Changes) > 0 {
				request.Env = variables
			}
		}

		if len(in.Labels)+len(in.RemoveLabels) > 0 {
			labels, before := maps.Clone(doc.Config.Labels), len(out.Changes)
			if labels == nil {
				labels = map[string]string{}
			}
			for _, name := range slices.Sorted(maps.Keys(in.Labels)) {
				if value, has := labels[name]; !has || value != in.Labels[name] {
					out.Changes = append(out.Changes, "the label "+name+" is set")
					labels[name] = in.Labels[name]
				}
			}
			for _, name := range in.RemoveLabels {
				if _, has := labels[name]; has {
					out.Changes = append(out.Changes, "the label "+name+" is taken away")
					delete(labels, name)
				}
			}
			if len(out.Changes) > before {
				request.Labels = labelled(labels)
			}
		}

		if in.Ports != nil {
			request.Ports = published
			out.Changes = append(out.Changes, fmt.Sprintf("its published ports, which were %s, are %s", doc.publishes(), in.Ports))
		}
		if in.Volumes != nil {
			request.VolumeBinds = mounted
			out.Changes = append(out.Changes, fmt.Sprintf("its mounts, which were %s, are %s", orNone(doc.HostConfig.Binds), mounted))
		}
		if len(in.Command) > 0 {
			request.Cmd = in.Command
			out.Changes = append(out.Changes, "its command is "+strings.Join(in.Command, " "))
		}

		out.Note = ofStack(c)
		if len(out.Changes) == 0 {
			out.Note = noted("nothing was done: it is so already", out.Note)

			return nil, out, nil
		}
		if in.DryRun {
			out.Changes = append(out.Changes, fmt.Sprintf("would stop %s, remove it and make it again on %s with these", c.Name, out.Env))

			return nil, out, nil
		}

		_, err = r.dock.PostContainersByIdUpdate(ctx, c.Id, request, dockhand.PostContainersByIdUpdateOptions{Env: at.Env.ID})
		var note string
		out.ID, out.Is, note = r.remade(ctx, at.Env, c.Name)
		if err != nil {
			out.Changes = []string{}

			return nil, out, fmt.Errorf("%s on %s was not made again with the change: %w. %s", c.Name, out.Env, refusal(err), cmp.Or(note, "It is "+out.Is+" now"))
		}
		out.Changes = append(out.Changes, fmt.Sprintf("stopped %s, removed it and made it again on %s with these", c.Name, out.Env))
		out.Note = noted(out.Note, note)
		if note == "" && running(out.Was) && !running(out.Is) {
			out.Note = noted(out.Note, fmt.Sprintf("it was running and is %s now: its logs say why it did not stay up with the change", out.Is))
		}

		return nil, out, nil
	})

	type containerUpdateIn struct {
		containerArg
		dryRunArg
		waitArg

		Image    string `json:"image,omitempty"    jsonschema:"another image or tag to move it to, such as nginx:1.28; left out, the one it has, fetched afresh"`
		Recreate bool   `json:"recreate,omitempty" jsonschema:"make it again though the registry has nothing newer than the image it runs"`
	}
	type containerUpdateOut struct {
		jobState

		Env     string   `json:"env"               jsonschema:"the environment (Docker host) the container is on"`
		Name    string   `json:"name"`
		Image   string   `json:"image"             jsonschema:"the image it is to run, by name"`
		Was     string   `json:"was"               jsonschema:"the id of the image it ran before, shortened"`
		Is      string   `json:"is"                jsonschema:"the id of the image it runs now; the same as was when nothing newer was to be had"`
		Updated bool     `json:"updated"           jsonschema:"whether it was made again from another image than it ran"`
		State   string   `json:"state"             jsonschema:"how it stands, read back from the host"`
		Changes []string `json:"changes"           jsonschema:"what was done, in words"`
		DryRun  bool     `json:"dry_run,omitempty" jsonschema:"nothing was done: changes says what would be"`
		Note    string   `json:"note,omitempty"`
	}
	add(r, writeTool, &mcp.Tool{
		Name:        "container_update",
		Description: "Bring one container up to a newer image: pull the image it was made from, or another given as image, and when the registry had something newer, stop the container, remove it and make it again from that, with everything it was made with kept, its name, variables, ports, mounts, networks, restart policy and limits. It is started again when it was running. When the pull brings nothing newer, nothing is touched, unless recreate asks for it. The pull runs in the background on Dockhand: this waits up to wait seconds for it and, when it is still going then, answers with running true and the job's id and has changed nothing yet, so the call is made again once the job is done. What the container wrote inside itself, outside its volumes, is gone, and the image it ran stays on the host until images are pruned. A container Dockhand or its agent runs as is refused. A container of a compose stack is made again from the newer image too, and stays in its stack.",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in containerUpdateIn) (*mcp.CallToolResult, containerUpdateOut, error) {
		to := strings.TrimSpace(in.Image)
		if strings.ContainsAny(to, " \t\n") {
			return nil, containerUpdateOut{}, fmt.Errorf("image is %q: it is the name and tag to move the container to, such as nginx:1.28", in.Image)
		}
		patience, err := in.patience()
		if err != nil {
			return nil, containerUpdateOut{}, err
		}
		at, err := r.find(ctx, in.containerArg)
		if err != nil {
			return nil, containerUpdateOut{}, err
		}
		c := &at.Container
		image := cmp.Or(to, c.Image)
		out := containerUpdateOut{Env: at.Env.Name, Name: c.Name, Image: image, Was: shortImageID(c.ImageId), Is: shortImageID(c.ImageId), State: string(c.State), Changes: []string{}, DryRun: in.DryRun}
		if err := dockhandsOwn(c); err != nil {
			return nil, out, err
		}
		if unnamed(image) {
			return nil, out, fmt.Errorf("%s was made from an image the host no longer has a name for (%s), so there is nothing to ask a registry for: give image, the name and tag to make it again from", c.Name, shortImageID(c.ImageId))
		}
		out.Note = ofStack(c)
		if in.DryRun {
			out.Changes = append(out.Changes, "would pull "+image+" to "+out.Env, fmt.Sprintf("would stop %s, remove it and make it again from that, when it is another image than the one it runs", c.Name))

			return nil, out, nil
		}

		state, err := r.pull(ctx, at.Env, image, patience)
		out.jobState = state
		if err != nil {
			return nil, out, fmt.Errorf("%s was not pulled to %s, so %s is as it was: %w", image, out.Env, c.Name, err)
		}
		if state.Running {
			out.Note = noted(fmt.Sprintf("the pull of %s was still running after %s: %s is as it was. job_get with job %s says when the pull is done, and this call made again then makes the container again", image, seconds(patience), c.Name, state.Job), out.Note)

			return nil, out, nil
		}
		out.Changes = append(out.Changes, "pulled "+image+" to "+out.Env)

		newest, err := r.imageNamed(ctx, at.Env, image)
		if err != nil {
			return nil, out, fmt.Errorf("%s was pulled, and the images of %s could not be listed to see whether it is newer, so %s is as it was: %w", image, out.Env, c.Name, err)
		}
		if newest == c.ImageId && !in.Recreate {
			out.Note = noted(fmt.Sprintf("nothing more was done: the registry has nothing newer under %s than the image %s runs", image, c.Name), out.Note)

			return nil, out, nil
		}

		_, err = r.dock.PostContainersByIdUpdate(ctx, c.Id, dockhand.PostContainersByIdUpdateRequest{Image: image, StartAfterUpdate: new(running(string(c.State)))}, dockhand.PostContainersByIdUpdateOptions{Env: at.Env.ID})
		is, failed := r.containerNamed(ctx, at.Env, c.Name)
		if err != nil {
			how := "how it stands now could not be read back"
			if failed == nil && is != nil {
				how = "it is " + string(is.State) + " now, on the image " + shortImageID(is.ImageId)
			}

			return nil, out, fmt.Errorf("%s on %s was not made again from %s: %w; %s", c.Name, out.Env, image, refusal(err), how)
		}
		out.Changes = append(out.Changes, fmt.Sprintf("stopped %s, removed it and made it again from %s", c.Name, image))

		// what Dockhand noted of a newer image is kept under the id of the container that is gone, and is cleared with it
		if _, cleared := r.dock.DeleteContainersPendingUpdates(ctx, dockhand.DeleteContainersPendingUpdatesOptions{Env: at.Env.ID, ContainerId: c.Id}); cleared != nil {
			out.Note = noted(out.Note, "Dockhand's note that an update was waiting could not be cleared: "+refusal(cleared).Error())
		}

		switch {
		case failed != nil:
			out.Is, out.State = "", ""
			out.Note = noted(out.Note, "how it stands could not be read back: "+failed.Error())
		case is == nil:
			out.Is, out.State = "", ""
			out.Note = noted(out.Note, "Dockhand said it made the container again and none of that name is listed now")
		default:
			out.Is, out.State = shortImageID(is.ImageId), string(is.State)
			out.Updated = is.ImageId != c.ImageId
			if running(string(c.State)) && !running(string(is.State)) {
				out.Note = noted(out.Note, fmt.Sprintf("it was running and is %s now: its logs say why it did not stay up on the newer image", is.State))
			}
		}

		return nil, out, nil
	})

	type containerLimitsIn struct {
		containerArg
		dryRunArg

		MemoryMB int     `json:"memory_mb,omitempty" jsonschema:"the most memory it may hold, in megabytes of a million bytes, as the other tools report it; swap is held to the same, so it cannot swap its way past it"`
		CPUs     float64 `json:"cpus,omitempty"      jsonschema:"the most processor it may use, in processors: 1.5 is one and a half"`
		Restart  string  `json:"restart,omitempty"   jsonschema:"when Docker starts it again by itself: no, always, unless-stopped, or on-failure, which may say how many times as on-failure:3"`
	}
	type containerLimitsOut struct {
		Env      string   `json:"env"                jsonschema:"the environment (Docker host) the container is on"`
		Name     string   `json:"name"`
		MemoryMB float64  `json:"memory_mb"          jsonschema:"the most memory it may hold, read back from the host; 0 for no limit"`
		CPUs     float64  `json:"cpus"               jsonschema:"the most processor it may use, in processors; 0 for no limit"`
		Restart  string   `json:"restart"            jsonschema:"when Docker starts it again by itself"`
		Changes  []string `json:"changes"            jsonschema:"what was changed, in words"`
		Warnings []string `json:"warnings,omitempty" jsonschema:"what Docker warned of as it took the change"`
		DryRun   bool     `json:"dry_run,omitempty"  jsonschema:"nothing was done: changes says what would be"`
		Note     string   `json:"note,omitempty"`
	}
	add(r, writeTool, &mcp.Tool{
		Name:        "container_limits",
		Description: "Set the most memory and processor one container may use, and when Docker starts it again by itself, as docker update does: on the container as it stands, running or not, without stopping it or making it again. Any of the three may be given, and what is not given is kept. A limit can be set, raised and lowered here and not taken off: Docker only removes one when the container is made again. Memory below what the container holds now is refused by the kernel. A container of a compose stack takes the limit, and loses it at the stack's next deploy, which goes by the stack's file. Read back from the host.",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in containerLimitsIn) (*mcp.CallToolResult, containerLimitsOut, error) {
		policy, retries, err := restartOf(in.Restart)
		switch {
		case err != nil:
			return nil, containerLimitsOut{}, err
		case in.MemoryMB == 0 && in.CPUs == 0 && policy == "":
			return nil, containerLimitsOut{}, errors.New("nothing to set was given: memory_mb, cpus or restart")
		case in.MemoryMB != 0 && in.MemoryMB < minMemoryMB:
			return nil, containerLimitsOut{}, fmt.Errorf("memory_mb is %d: Docker limits a container to %d megabytes or more, and a limit cannot be taken off here", in.MemoryMB, minMemoryMB)
		case in.CPUs < 0 || math.IsNaN(in.CPUs) || math.IsInf(in.CPUs, 0):
			return nil, containerLimitsOut{}, fmt.Errorf("cpus is %v: it is how many processors the container may use, a number above 0", in.CPUs)
		default:
		}

		at, err := r.find(ctx, in.containerArg)
		if err != nil {
			return nil, containerLimitsOut{}, err
		}
		c := &at.Container
		out := containerLimitsOut{Env: at.Env.Name, Name: c.Name, Changes: []string{}, DryRun: in.DryRun}
		before, err := r.inspect(ctx, &at)
		if err != nil {
			return nil, out, err
		}
		out.MemoryMB, out.CPUs, out.Restart = megabytes(int(before.HostConfig.Memory)), float64(before.HostConfig.NanoCpus)/1e9, before.HostConfig.RestartPolicy.Name

		request := dockhand.PostContainersByIdUpdateRuntimeRequest{}
		limit := func(amount float64) string {
			if amount == 0 {
				return "no limit"
			}

			return strconv.FormatFloat(amount, 'f', -1, 64)
		}
		if in.MemoryMB != 0 && megabytes(in.MemoryMB*1e6) != out.MemoryMB {
			request.Memory, request.MemorySwap = in.MemoryMB*1e6, in.MemoryMB*1e6
			out.Changes = append(out.Changes, fmt.Sprintf("memory: %s to %d megabytes", limit(out.MemoryMB), in.MemoryMB))
		}
		if in.CPUs != 0 && in.CPUs != out.CPUs {
			request.NanoCpus = int(math.Round(in.CPUs * 1e9))
			out.Changes = append(out.Changes, fmt.Sprintf("processors: %s to %s", limit(out.CPUs), limit(in.CPUs)))
		}
		if policy != "" && (policy != out.Restart || retries > 0) {
			request.RestartPolicy = map[string]any{"Name": policy}
			if retries > 0 {
				request.RestartPolicy["MaximumRetryCount"] = retries
			}
			out.Changes = append(out.Changes, fmt.Sprintf("restart: %s to %s", cmp.Or(out.Restart, "no"), strings.ToLower(strings.TrimSpace(in.Restart))))
		}

		if stack := c.Labels[labelStack]; stack != "" {
			out.Note = fmt.Sprintf("it belongs to the stack %s, whose next deploy makes it again by the stack's file and without this", stack)
		}
		if len(out.Changes) == 0 {
			out.Note = noted("nothing was done: it is so already", out.Note)

			return nil, out, nil
		}
		if in.DryRun {
			for i := range out.Changes {
				out.Changes[i] = "would set " + out.Changes[i]
			}

			return nil, out, nil
		}

		set, err := r.dock.PostContainersByIdUpdateRuntime(ctx, c.Id, request, dockhand.PostContainersByIdUpdateRuntimeOptions{Env: at.Env.ID})
		if err != nil {
			out.Changes = []string{}

			return nil, out, fmt.Errorf("the limits of %s on %s were not changed: %w", c.Name, out.Env, refusal(err))
		}
		out.Warnings = set.Answer.Warnings
		for i := range out.Changes {
			out.Changes[i] = "set " + out.Changes[i]
		}

		after, err := r.inspect(ctx, &at)
		if err != nil {
			out.Note = noted(out.Note, "what it has now could not be read back: "+err.Error())

			return nil, out, nil //nolint:nilerr // the limits were set: that they could not be read back is said in the answer
		}
		out.MemoryMB, out.CPUs, out.Restart = megabytes(int(after.HostConfig.Memory)), float64(after.HostConfig.NanoCpus)/1e9, after.HostConfig.RestartPolicy.Name

		return nil, out, nil
	})

	type containerComposeIn struct {
		containerArg
		revealArg
	}
	type containerComposeOut struct {
		Env       string `json:"env"              jsonschema:"the environment (Docker host) the container is on"`
		Container string `json:"container"`
		Compose   string `json:"compose"          jsonschema:"a compose file that would make a container like this one: its image, command, variables, labels, ports, mounts and restart policy"`
		Service   string `json:"service"          jsonschema:"what the file calls the service"`
		Stack     string `json:"stack,omitempty"  jsonschema:"the compose stack the container belongs to already, when it belongs to one"`
		Hidden    int    `json:"hidden,omitempty" jsonschema:"how many secrets in the file are hidden"`
		Note      string `json:"note,omitempty"`
	}
	add(r, readTool, &mcp.Tool{
		Name:        "container_compose",
		Description: "Write one container out as a compose file: what Dockhand makes of how it was made, its image, command, variables, labels, published ports, mounts and restart policy, as a service that would make one like it. It is how a container someone started by hand is turned into a stack, or carried to another host. It is Dockhand's reading of the container and not a file anyone wrote: a container that came from a stack has a file already, which is the one to go by. Passwords, tokens and keys are hidden where a name beside them says what they are, unless reveal_secrets asks for them.",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in containerComposeIn) (*mcp.CallToolResult, containerComposeOut, error) {
		at, err := r.find(ctx, in.containerArg)
		if err != nil {
			return nil, containerComposeOut{}, err
		}
		out := containerComposeOut{Env: at.Env.Name, Container: at.Container.Name}

		written, err := r.dock.GetContainersByIdCompose(ctx, at.Container.Id, dockhand.GetContainersByIdComposeOptions{Env: at.Env.ID})
		if err != nil {
			return nil, out, fmt.Errorf("%s on %s could not be written out as compose: %w", out.Container, out.Env, refusal(err))
		}
		out.Compose, out.Service, out.Stack = written.Answer.Compose, written.Answer.ServiceName, written.Answer.StackProject
		if !in.RevealSecrets {
			out.Compose, out.Hidden = maskText(out.Compose)
		}
		if out.Hidden > 0 {
			out.Note = hiddenNote(out.Hidden, "told by the names beside them")
		}
		if out.Stack != "" {
			out.Note = noted(out.Note, fmt.Sprintf("it belongs to the stack %s already, whose own file is the one to go by", out.Stack))
		}

		return nil, out, nil
	})
}
