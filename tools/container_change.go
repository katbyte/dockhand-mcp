package tools

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"path"
	"regexp"
	"slices"
	"strings"
	"unicode/utf8"

	"github.com/katbyte/dockhand-mcp/sdk/dockhand"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// statePaused is the state of a container whose processes are held still.
const statePaused = "paused"

// The two things done to a container that is running to hold it still and let it go on.
const (
	actPause   = "pause"
	actUnpause = "unpause"
)

// defaultOutputChars is how much of what a command printed container_exec gives unless told, from its end.
const defaultOutputChars = 20000

// noSuchCommand is what a command exits with when the container has none of its name.
const noSuchCommand = 127

// shellNames is the shells Dockhand looks for in a container.
var shellNames = []string{"sh", "bash", "ash", "zsh", "dash"}

// containerNamed is what Docker takes for a container's name.
var containerNamed = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9_.-]+$`)

// tailOf is the end of what a command printed, with its secrets hidden unless they are asked for: the text, how long the whole was, and how many
// secrets were hidden.
func tailOf(printed string, limit int, reveal bool) (text string, chars, hidden int) {
	if !reveal {
		printed, hidden = maskText(printed)
	}
	chars = utf8.RuneCountInString(printed)
	if chars > limit {
		printed = string([]rune(printed)[chars-limit:])
	}

	return printed, chars, hidden
}

//nolint:gocyclo,maintidx // six tools registered in one function, each a handler of its own: the count is theirs added up
func registerContainerChangeTools(r *registry) {
	type containerActIn struct {
		containerArg
		dryRunArg
	}
	for _, tool := range []struct {
		action, description string
	}{
		{actPause, "Hold one running container still: every process in it is frozen where it stands, keeping its memory and its place, and uses no processor until container_unpause lets it go on. Nothing is stopped or lost, and nothing it serves is answered while it is held. One that is not running, or is held already, is left as it is. Read back as paused."},
		{actUnpause, "Let one paused container go on from where it was held. One that is not paused is left as it is. Read back as running."},
	} {
		add(r, writeTool, &mcp.Tool{
			Name:        "container_" + tool.action,
			Description: tool.description,
		}, func(ctx context.Context, _ *mcp.CallToolRequest, in containerActIn) (*mcp.CallToolResult, actionOut, error) {
			at, err := r.find(ctx, in.containerArg)
			if err != nil {
				return nil, actionOut{}, err
			}
			name, env, was := at.Container.Name, at.Env.Name, at.Container.State
			out := actionOut{Env: env, Name: name, Was: string(was), Is: string(was), Changes: []string{}, DryRun: in.DryRun}

			from, to, done := stateRunning, statePaused, "paused"
			if tool.action == actUnpause {
				from, to, done = statePaused, stateRunning, "let go on"
			}
			switch {
			case string(was) != from:
				out.Note = fmt.Sprintf("%s is %s, and only a container that is %s can be %s: nothing was done", name, was, from, done)

				return nil, out, nil
			case in.DryRun:
				out.Changes = append(out.Changes, fmt.Sprintf("would %s %s on %s", tool.action, name, env))

				return nil, out, nil
			default:
			}

			if tool.action == actPause {
				_, err = r.dock.PostContainersByIdPause(ctx, at.Container.Id, dockhand.PostContainersByIdPauseOptions{Env: at.Env.ID})
			} else {
				_, err = r.dock.PostContainersByIdUnpause(ctx, at.Container.Id, dockhand.PostContainersByIdUnpauseOptions{Env: at.Env.ID})
			}
			if err != nil {
				return nil, out, fmt.Errorf("%s on %s was not %s: %w", name, env, done, refusal(err))
			}
			out.Changes = append(out.Changes, fmt.Sprintf("%s %s on %s", done, name, env))

			if is, err := r.stateOf(ctx, &at); err != nil {
				out.Is, out.Note = "", "its state after could not be read back: "+err.Error()
			} else if out.Is = is; is != to {
				out.Note = fmt.Sprintf("it was %s and is %s: container_get says how it stands", done, is)
			}

			return nil, out, nil
		})
	}

	type containerRenameIn struct {
		containerArg
		dryRunArg

		Name string `json:"name" jsonschema:"the name it is to have: letters, digits, dots, hyphens and underscores, starting with a letter or a digit"`
	}
	type containerRenameOut struct {
		Env     string   `json:"env"               jsonschema:"the environment (Docker host) the container is on"`
		Was     string   `json:"was"               jsonschema:"the name it had"`
		Is      string   `json:"is"                jsonschema:"the name it has, read back from the host; with dry_run, the name it still has"`
		Changes []string `json:"changes"           jsonschema:"what was done, in words"`
		DryRun  bool     `json:"dry_run,omitempty" jsonschema:"nothing was done: changes says what would be"`
		Note    string   `json:"note,omitempty"`
	}
	add(r, writeTool, &mcp.Tool{
		Name:        "container_rename",
		Description: "Give one container another name. It goes on running, untouched: only what it is called changes, and Dockhand carries what it keeps for the container over to the new name. A name another container on the host has is refused. A container that compose made is known to its stack by its labels and not its name, so it stays in the stack, but the next deploy of the stack makes a container under the name compose expects.",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in containerRenameIn) (*mcp.CallToolResult, containerRenameOut, error) {
		to := strings.TrimPrefix(strings.TrimSpace(in.Name), "/")
		if !containerNamed.MatchString(to) {
			return nil, containerRenameOut{}, fmt.Errorf("name is %q: a container's name is letters, digits, dots, hyphens and underscores, two or more, starting with a letter or a digit", in.Name)
		}
		at, err := r.find(ctx, in.containerArg)
		if err != nil {
			return nil, containerRenameOut{}, err
		}
		out := containerRenameOut{Env: at.Env.Name, Was: at.Container.Name, Is: at.Container.Name, Changes: []string{}, DryRun: in.DryRun}
		if to == out.Was {
			out.Note = "nothing was done: that is its name already"

			return nil, out, nil
		}

		others, err := r.containersOf(ctx, at.Env)
		if err != nil {
			return nil, out, fmt.Errorf("the containers of %s could not be listed, so nothing was renamed: %w", out.Env, err)
		}
		if slices.ContainsFunc(others, func(c dockhand.GetContainersAnswer) bool { return c.Name == to }) {
			return nil, out, fmt.Errorf("another container on %s is named %s already", out.Env, to)
		}
		if stack := at.Container.Labels[labelStack]; stack != "" {
			out.Note = fmt.Sprintf("it belongs to the stack %s, whose next deploy will make a container under the name compose expects beside this one", stack)
		}
		if in.DryRun {
			out.Changes = append(out.Changes, fmt.Sprintf("would rename %s on %s to %s", out.Was, out.Env, to))

			return nil, out, nil
		}

		if _, err := r.dock.PostContainersByIdRename(ctx, at.Container.Id, dockhand.PostContainersByIdRenameRequest{Name: to}, dockhand.PostContainersByIdRenameOptions{Env: at.Env.ID}); err != nil {
			return nil, out, fmt.Errorf("%s on %s was not renamed: %w", out.Was, out.Env, refusal(err))
		}
		out.Changes = append(out.Changes, fmt.Sprintf("renamed %s on %s to %s", out.Was, out.Env, to))

		after, err := r.containersOf(ctx, at.Env)
		if err != nil {
			out.Is, out.Note = "", noted(out.Note, "its name after could not be read back: "+err.Error())

			return nil, out, nil //nolint:nilerr // it was renamed: that it could not be read back is said in the answer
		}
		if i := slices.IndexFunc(after, func(c dockhand.GetContainersAnswer) bool { return c.Id == at.Container.Id }); i >= 0 {
			out.Is = after[i].Name
		}
		if out.Is != to {
			out.Note = noted(out.Note, "Dockhand said it renamed the container and it is named "+out.Is)
		}

		return nil, out, nil
	})

	type containerExecIn struct {
		containerArg
		revealArg

		Command  []string `json:"command"             jsonschema:"the command and its arguments, one to an entry: [\"ls\", \"-la\", \"/data\"]. No shell reads it unless it is one: [\"sh\", \"-c\", \"ls /data | wc -l\"]"`
		User     string   `json:"user,omitempty"      jsonschema:"the user to run it as, by name or number; left out, the user the container runs as"`
		Workdir  string   `json:"workdir,omitempty"   jsonschema:"the directory to run it in; left out, the container's own"`
		MaxChars int      `json:"max_chars,omitempty" jsonschema:"how much of what it printed to give, from its end: 20000 characters unless given, 200000 at most"`
	}
	type containerExecOut struct {
		Env       string `json:"env"                 jsonschema:"the environment (Docker host) the container is on"`
		Container string `json:"container"`
		ExitCode  int    `json:"exit_code"           jsonschema:"what the command exited with: 0 when it says it succeeded, 127 when the container has no such command"`
		Stdout    string `json:"stdout"              jsonschema:"what it printed, from the end when it is long"`
		Stderr    string `json:"stderr"              jsonschema:"what it printed as errors"`
		Truncated bool   `json:"truncated,omitempty" jsonschema:"it printed more than is given"`
		Hidden    int    `json:"hidden,omitempty"    jsonschema:"how many secrets in what it printed are hidden"`
		Note      string `json:"note,omitempty"`
	}
	add(r, writeTool, &mcp.Tool{
		Name:        "container_exec",
		Description: "Run one command inside a running container, as docker exec does, wait for it to end and give what it printed and what it exited with. It runs with the container's own files, network and variables, so it can look at anything the container can and change anything it can: it is as much a way to break a container as to look into one. The command is one that ends by itself; one that waits for input or runs on is not for this. A command that fails is an answer with its exit code, not an error. Passwords, tokens and keys in what it printed are hidden where a name beside them says what they are, unless reveal_secrets asks for them.",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in containerExecIn) (*mcp.CallToolResult, containerExecOut, error) {
		switch {
		case len(in.Command) == 0 || strings.TrimSpace(in.Command[0]) == "":
			return nil, containerExecOut{}, errors.New(`command is required: the command and its arguments, one to an entry, such as ["ls", "-la", "/data"]`)
		case in.MaxChars < 0 || in.MaxChars > maxFileChars:
			return nil, containerExecOut{}, fmt.Errorf("max_chars is %d: it is how much of what was printed to give, from 1 to %d, or left out for %d", in.MaxChars, maxFileChars, defaultOutputChars)
		default:
		}
		at, err := r.inside(ctx, in.containerArg, "a command can be run")
		if err != nil {
			return nil, containerExecOut{}, err
		}
		out := containerExecOut{Env: at.Env.Name, Container: at.Container.Name}

		ran, err := r.dock.PostContainersByIdExecRun(ctx, at.Container.Id, dockhand.PostContainersByIdExecRunRequest{Cmd: in.Command, User: strings.TrimSpace(in.User), WorkingDir: strings.TrimSpace(in.Workdir)}, dockhand.PostContainersByIdExecRunOptions{EnvId: at.Env.ID})
		if err != nil {
			return nil, out, fmt.Errorf("the command was not run in %s: %w", out.Container, refusal(err))
		}

		limit := defaultOutputChars
		if in.MaxChars > 0 {
			limit = in.MaxChars
		}
		var chars, hidden int
		out.ExitCode = ran.Answer.ExitCode
		out.Stdout, chars, out.Hidden = tailOf(ran.Answer.Stdout, limit, in.RevealSecrets)
		if chars > limit {
			out.Truncated = true
			out.Note = fmt.Sprintf("it printed %d characters and the last %d are given: raise max_chars for more", chars, limit)
		}
		out.Stderr, chars, hidden = tailOf(ran.Answer.Stderr, limit, in.RevealSecrets)
		if out.Hidden += hidden; chars > limit {
			out.Truncated = true
			out.Note = noted(out.Note, fmt.Sprintf("it printed %d characters of errors and the last %d are given", chars, limit))
		}
		if out.ExitCode != 0 {
			out.Note = noted(out.Note, fmt.Sprintf("the command exited with %d, which is its way of saying it failed", out.ExitCode))
		}
		// a shell the container has not is the commonest thing to be missing, and Dockhand can say which it has
		if out.ExitCode == noSuchCommand && slices.Contains(shellNames, path.Base(in.Command[0])) {
			if has, failed := r.dock.GetContainersByIdShells(ctx, at.Container.Id, dockhand.GetContainersByIdShellsOptions{Env: at.Env.ID}); failed == nil && len(has.Answer.Shells) > 0 {
				out.Note = noted(out.Note, "the shells it has are "+strings.Join(has.Answer.Shells, " and "))
			}
		}
		if out.Hidden > 0 {
			out.Note = noted(out.Note, hiddenNote(out.Hidden, "told by the names beside them"))
		}

		return nil, out, nil
	})

	type containerDeleteIn struct {
		containerArg

		Confirm bool `json:"confirm,omitempty" jsonschema:"remove it: without this the answer says what would go and nothing is removed"`
		Force   bool `json:"force,omitempty"   jsonschema:"remove it though it is running: it is killed first, with no time to finish what it is doing"`
	}
	type containerDeleteOut struct {
		Env     string   `json:"env"               jsonschema:"the environment (Docker host) the container is on"`
		Name    string   `json:"name"`
		State   string   `json:"state"             jsonschema:"how it stood before: running, exited, paused and so on"`
		Image   string   `json:"image"             jsonschema:"the image it was made from, which stays on the host"`
		Stack   string   `json:"stack,omitempty"   jsonschema:"the compose stack it belongs to, when it belongs to one"`
		Volumes []string `json:"volumes,omitempty" jsonschema:"the named volumes it mounts, which stay"`
		Removed bool     `json:"removed"           jsonschema:"whether it was removed, read back as gone; false with confirm left out"`
		Note    string   `json:"note,omitempty"`
	}
	add(r, deleteTool, &mcp.Tool{
		Name:        "container_delete",
		Description: "Remove one container from a Docker host. Everything it wrote inside itself goes with it and cannot be got back; its image stays, and so do the named volumes it mounts and what they hold. Without confirm the answer says what would go and removes nothing. A running container is refused unless force is given, which kills it first. A container that belongs to a compose stack is made again by the stack's next deploy, so taking one out for good is a change to the stack's file. Read back as gone.",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in containerDeleteIn) (*mcp.CallToolResult, containerDeleteOut, error) {
		at, err := r.find(ctx, in.containerArg)
		if err != nil {
			return nil, containerDeleteOut{}, err
		}
		c := &at.Container
		out := containerDeleteOut{Env: at.Env.Name, Name: c.Name, State: string(c.State), Image: c.Image, Stack: c.Labels[labelStack]}
		for i := range c.Mounts {
			if c.Mounts[i].Type == "volume" && c.Mounts[i].Name != "" {
				out.Volumes = append(out.Volumes, c.Mounts[i].Name)
			}
		}
		slices.Sort(out.Volumes)
		if out.Stack != "" {
			out.Note = fmt.Sprintf("it belongs to the stack %s, whose next deploy makes it again", out.Stack)
		}

		alive := running(string(c.State))
		switch {
		case !in.Confirm && alive && !in.Force:
			out.Note = noted(out.Note, fmt.Sprintf("nothing was removed: it is %s, so removing it takes force as well as confirm, or stopping it first", c.State))

			return nil, out, nil
		case !in.Confirm:
			out.Note = noted(out.Note, "nothing was removed: call again with confirm to remove it")

			return nil, out, nil
		case alive && !in.Force:
			return nil, out, fmt.Errorf("%s on %s is %s: stop it first, or give force to have it killed and removed", c.Name, out.Env, c.State)
		default:
		}

		options := dockhand.DeleteContainersByIdOptions{Env: at.Env.ID}
		if alive {
			options.Force = new(true)
		}
		if _, err := r.dock.DeleteContainersById(ctx, c.Id, options); err != nil {
			return nil, out, fmt.Errorf("%s on %s was not removed: %w", c.Name, out.Env, refusal(err))
		}

		// Dockhand answers success for a container that is not there, so that it is gone is read from the host
		after, err := r.containersOf(ctx, at.Env)
		if err != nil {
			out.Note = noted(out.Note, "that it is gone could not be read back: "+err.Error())

			return nil, out, nil //nolint:nilerr // Dockhand said it removed the container: that it could not be read back is said in the answer
		}
		out.Removed = !slices.ContainsFunc(after, func(other dockhand.GetContainersAnswer) bool { return other.Id == c.Id })
		if !out.Removed {
			out.Note = noted(out.Note, "Dockhand said it removed the container and it is still listed")
		}

		return nil, out, nil
	})

	type containerPruneIn struct {
		envArg

		Confirm bool `json:"confirm,omitempty" jsonschema:"remove them: without this the answer lists what would go and nothing is removed"`
	}
	type containerPruneOut struct {
		Env     string   `json:"env"                jsonschema:"the environment (Docker host) that was pruned"`
		Removed []string `json:"removed"            jsonschema:"the containers that were removed, by name; with confirm left out, empty"`
		WouldGo []string `json:"would_go,omitempty" jsonschema:"the containers that would be removed, each with its state and the stack it belongs to, said before anything is"`
		FreedMB float64  `json:"freed_mb"           jsonschema:"the disk given back, in megabytes, as Docker reports it"`
		Note    string   `json:"note,omitempty"`
	}
	add(r, deleteTool, &mcp.Tool{
		Name:        "container_prune",
		Description: "Remove every container on one Docker host that is not running, as docker container prune does: the ones that exited, whatever with, and the ones that were made and never started. It names one environment. What each wrote inside itself goes with it; images and named volumes stay. Without confirm the answer lists what would go and removes nothing, and the list is worth reading: a stopped container is as often one someone stopped on purpose and means to start again, a job that runs now and then, or a stack that is down, as it is something left over. A container of a compose stack is made again by the stack's next deploy.",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in containerPruneIn) (*mcp.CallToolResult, containerPruneOut, error) {
		env, err := r.one(ctx, in.envArg)
		if err != nil {
			return nil, containerPruneOut{}, err
		}
		out := containerPruneOut{Env: env.Name, Removed: []string{}}

		listed, err := r.containersOf(ctx, env)
		if err != nil {
			return nil, out, fmt.Errorf("the containers of %s could not be listed, so nothing was removed: %w", env.Name, err)
		}
		names := map[string]string{}
		for i := range listed {
			c := &listed[i]
			names[c.Id] = c.Name
			// Docker prunes what is not running, restarting or paused
			if running(string(c.State)) {
				continue
			}
			would := fmt.Sprintf("%s (%s)", c.Name, c.State)
			if stack := c.Labels[labelStack]; stack != "" {
				would = fmt.Sprintf("%s (%s, of the stack %s)", c.Name, c.State, stack)
			}
			out.WouldGo = append(out.WouldGo, would)
		}
		slices.Sort(out.WouldGo)
		switch {
		case len(out.WouldGo) == 0:
			out.Note = "nothing to remove: every container on " + env.Name + " is running"

			return nil, out, nil
		case !in.Confirm:
			out.Note = "nothing was removed: call again with confirm to remove them"

			return nil, out, nil
		default:
		}

		pruned, err := r.dock.PostPruneContainers(ctx, dockhand.PostPruneContainersOptions{Env: env.ID})
		if err != nil {
			return nil, out, fmt.Errorf("the stopped containers of %s were not removed: %w", env.Name, refusal(err))
		}
		if !pruned.Answer.Success {
			return nil, out, fmt.Errorf("the stopped containers of %s were not removed, and Dockhand gives no reason", env.Name)
		}
		out.WouldGo = nil
		if report := pruned.Answer.Result; report != nil {
			for _, id := range report.ContainersDeleted {
				out.Removed = append(out.Removed, cmpName(names[id], id))
			}
			out.FreedMB = megabytes(report.SpaceReclaimed)
		}
		slices.Sort(out.Removed)

		return nil, out, nil
	})
}

// cmpName is a container's name, or the start of its id for one whose name is not known.
func cmpName(name, id string) string {
	return cmp.Or(name, shortened(id))
}
