package tools

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/katbyte/dockhand-mcp/sdk/dockhand"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// The three things done to a container or a stack that is already there.
const (
	actStart   = "start"
	actStop    = "stop"
	actRestart = "restart"
)

// dryRunArg is the argument every tool that changes something takes to say what it would do and do nothing.
type dryRunArg struct {
	DryRun bool `json:"dry_run,omitempty" jsonschema:"say what would be done and do nothing"`
}

// actionOut is what a tool that starts, stops or restarts something answers.
type actionOut struct {
	Env     string   `json:"env"               jsonschema:"the environment (Docker host) it is on"`
	Name    string   `json:"name"              jsonschema:"what was acted on: the container, or the stack"`
	Was     string   `json:"was"               jsonschema:"its state before"`
	Is      string   `json:"is"                jsonschema:"its state after, read back from the host; with dry_run, its state as it stands"`
	Changes []string `json:"changes"           jsonschema:"what was done, in words; empty when nothing needed doing"`
	DryRun  bool     `json:"dry_run,omitempty" jsonschema:"nothing was done: changes says what would be"`
	Note    string   `json:"note,omitempty"`
}

// running reports whether a container in a state is one Docker is running or is starting again by itself.
func running(state string) bool {
	return state == stateRunning || state == stateRestarting || state == "paused"
}

// stateOf reads a container's state back from the host it is on.
func (r *registry) stateOf(ctx context.Context, at *located) (string, error) {
	listed, err := r.containersOf(ctx, at.Env)
	if err != nil {
		return "", err
	}
	for i := range listed {
		if listed[i].Id == at.Container.Id {
			return string(listed[i].State), nil
		}
	}

	return "", fmt.Errorf("%s is no longer on %s", at.Container.Name, at.Env.Name)
}

// actOnContainer starts, stops or restarts one container and reads back how it stands.
func (r *registry) actOnContainer(ctx context.Context, action string, in containerArg, dryRun bool) (actionOut, error) {
	at, err := r.find(ctx, in)
	if err != nil {
		return actionOut{}, err
	}
	name, env, id, was := at.Container.Name, at.Env.Name, at.Container.Id, at.Container.State
	out := actionOut{Env: env, Name: name, Was: string(was), Is: string(was), Changes: []string{}, DryRun: dryRun}

	// what is already so is left so: starting what runs and stopping what does not are no change
	switch {
	case action == actStart && running(string(was)):
		out.Note = fmt.Sprintf("%s is already %s: nothing was done", name, was)

		return out, nil
	case action == actStop && !running(string(was)):
		out.Note = fmt.Sprintf("%s is not running, it is %s: nothing was done", name, was)

		return out, nil
	case dryRun:
		out.Changes = append(out.Changes, fmt.Sprintf("would %s %s on %s, which is %s", action, name, env, was))

		return out, nil
	default:
	}

	ok, err := r.containerAction(ctx, action, id, int(at.Env.ID))
	if err != nil {
		return out, fmt.Errorf("%s on %s was not %s: %w", name, env, past(action), refusal(err))
	}
	if !ok {
		return out, fmt.Errorf("the %s of %s on %s was answered without its being said to have succeeded: container_get says how the container stands", action, name, env)
	}
	out.Changes = append(out.Changes, fmt.Sprintf("%s %s on %s", past(action), name, env))

	// the answer says the call went through, and the host says how the container stands
	if is, err := r.stateOf(ctx, &at); err != nil {
		out.Is, out.Note = "", "its state after could not be read back: "+err.Error()
	} else {
		out.Is = is
		if (action == actStop) == running(is) {
			out.Note = fmt.Sprintf("it was %s and is %s all the same: a container that exits as soon as it starts, or one Docker starts again by itself, does this, and container_get and container_logs say why", past(action), is)
		}
	}

	return out, nil
}

// containerAction makes the one call an action on a container is, and reports whether Dockhand said it succeeded.
func (r *registry) containerAction(ctx context.Context, action, id string, env int) (bool, error) {
	switch action {
	case actStart:
		answer, err := r.dock.PostContainersByIdStart(ctx, id, dockhand.PostContainersByIdStartOptions{Env: dockhand.EnvironmentID(env)})
		if err != nil {
			return false, err
		}

		return answer.Answer.Success, nil
	case actStop:
		answer, err := r.dock.PostContainersByIdStop(ctx, id, dockhand.PostContainersByIdStopOptions{Env: dockhand.EnvironmentID(env)})
		if err != nil {
			return false, err
		}

		return answer.Answer.Success, nil
	default:
		answer, err := r.dock.PostContainersByIdRestart(ctx, id, dockhand.PostContainersByIdRestartOptions{Env: dockhand.EnvironmentID(env)})
		if err != nil {
			return false, err
		}

		return answer.Answer.Success, nil
	}
}

// past is what was done, for the words a change is said in.
func past(action string) string {
	switch action {
	case actStart:
		return "started"
	case actStop:
		return "stopped"
	default:
		return "restarted"
	}
}

// stackArg is the arguments of a tool about one compose stack.
type stackArg struct {
	envArg

	Stack string `json:"stack" jsonschema:"the compose stack (project), by its name as stack_list gives it"`
}

// findStack is the one stack a call names, on the environment given or on every one. A name that is a stack's on several environments is refused
// until the call says which.
func (r *registry) findStack(ctx context.Context, in stackArg) (environment, dockhand.GetStacksAnswer, error) {
	want := strings.TrimSpace(in.Stack)
	if want == "" {
		return environment{}, dockhand.GetStacksAnswer{}, errors.New("stack is required: the stack's name, as stack_list gives it")
	}
	envs, err := r.pick(ctx, in.envArg)
	if err != nil {
		return environment{}, dockhand.GetStacksAnswer{}, err
	}

	var (
		on    []environment
		found []dockhand.GetStacksAnswer
	)
	for _, answer := range ask(ctx, envs, r.stacksOf) {
		if answer.Err != nil {
			return environment{}, dockhand.GetStacksAnswer{}, fmt.Errorf("the stacks of %s could not be listed: %w", answer.Env.Name, answer.Err)
		}
		for i := range answer.Value {
			if answer.Value[i].Name == want {
				on, found = append(on, answer.Env), append(found, answer.Value[i])
			}
		}
	}
	switch len(on) {
	case 0:
		return environment{}, dockhand.GetStacksAnswer{}, fmt.Errorf("no stack is named %q on the environments asked: stack_list gives the ones there are", want)
	case 1:
		return on[0], found[0], nil
	default:
		names := make([]string, 0, len(on))
		for _, e := range on {
			names = append(names, e.Name)
		}

		return environment{}, dockhand.GetStacksAnswer{}, fmt.Errorf("a stack named %q is on %s: say which with env", want, strings.Join(names, " and "))
	}
}

// actOnStack starts, stops or restarts every container of one stack and reads back how the stack stands.
func (r *registry) actOnStack(ctx context.Context, action string, in stackArg, dryRun bool) (actionOut, error) {
	env, stack, err := r.findStack(ctx, in)
	if err != nil {
		return actionOut{}, err
	}
	out := actionOut{Env: env.Name, Name: stack.Name, Was: string(stack.Status), Is: string(stack.Status), Changes: []string{}, DryRun: dryRun}

	switch {
	case action == actStart && stack.Status == stackRunning:
		out.Note = fmt.Sprintf("every container of %s is already running: nothing was done", stack.Name)

		return out, nil
	case action == actStop && stack.Status == stackStopped:
		out.Note = fmt.Sprintf("no container of %s is running: nothing was done", stack.Name)

		return out, nil
	case dryRun:
		out.Changes = append(out.Changes, fmt.Sprintf("would %s the %d containers of %s on %s, which is %s", action, len(stack.Containers), stack.Name, env.Name, stack.Status))

		return out, nil
	default:
	}

	// asked for JSON, as this client asks, Dockhand does the work before it answers and says how it went; a failure comes with HTTP 200 all the same
	ok, done, whyNot, err := r.stackAction(ctx, action, stack.Name, int(env.ID))
	if err != nil {
		return out, fmt.Errorf("%s on %s was not %s: %w", stack.Name, env.Name, past(action), refusal(err))
	}
	if !ok {
		return out, fmt.Errorf("%s on %s was not %s: Dockhand says %s", stack.Name, env.Name, past(action), cmp.Or(whyNot, "it could not, and gives no reason"))
	}
	out.Changes = append(out.Changes, fmt.Sprintf("%s %s on %s: %s", past(action), stack.Name, env.Name, done))

	if listed, err := r.stacksOf(ctx, env); err != nil {
		out.Is, out.Note = "", "its state after could not be read back: "+err.Error()
	} else {
		out.Is = stackGone
		for i := range listed {
			if listed[i].Name == stack.Name {
				out.Is = string(listed[i].Status)
			}
		}
		// a stack that was stopped is stopped, and one that was started or restarted is wholly running: anything else is said
		wanted := stackRunning
		if action == actStop {
			wanted = stackStopped
		}
		if out.Is != wanted {
			out.Note = fmt.Sprintf("it was %s and is %s: stack_list says which of its containers are not as asked", past(action), out.Is)
		}
	}

	return out, nil
}

// stackAction makes the one call an action on a stack is, and gives how Dockhand said it went: whether it succeeded, with what was done or why not.
func (r *registry) stackAction(ctx context.Context, action, name string, env int) (ok bool, done, whyNot string, err error) {
	switch action {
	case actStart:
		answer, err := r.dock.PostStacksByNameStart(ctx, name, dockhand.PostStacksByNameStartOptions{Env: dockhand.EnvironmentID(env)})
		if err != nil {
			return false, "", "", err
		}

		return answer.Answer.Success, answer.Answer.Output, answer.Answer.Error, nil
	case actStop:
		answer, err := r.dock.PostStacksByNameStop(ctx, name, dockhand.PostStacksByNameStopOptions{Env: dockhand.EnvironmentID(env)})
		if err != nil {
			return false, "", "", err
		}

		return answer.Answer.Success, answer.Answer.Output, answer.Answer.Error, nil
	default:
		answer, err := r.dock.PostStacksByNameRestart(ctx, name, dockhand.PostStacksByNameRestartOptions{Env: dockhand.EnvironmentID(env)})
		if err != nil {
			return false, "", "", err
		}

		return answer.Answer.Success, answer.Answer.Output, answer.Answer.Error, nil
	}
}

func registerActionTools(r *registry) {
	type containerActIn struct {
		containerArg
		dryRunArg
	}
	for _, tool := range []struct {
		action, description string
	}{
		{actStart, "Start one container that is not running, and read back that it is. One that is already running is left as it is. Name it as container_list does; env is only needed when the same name is on more than one host. A container that exits as soon as it starts is said to have, with where to look for why."},
		{actStop, "Stop one running container, and read back that it has stopped. Docker asks it to stop and kills it after ten seconds if it has not, so a stop can take that long. One that is not running is left as it is. A container whose restart policy is always is started again by Docker when the host's Docker restarts, though not by a stop alone."},
		{actRestart, "Restart one container: stop it and start it again, and read back how it stands. A stopped container is started. It is what makes a container pick up a change to something it reads only at start; it does not pull a newer image or apply a changed compose file, which is deploying the stack."},
	} {
		add(r, writeTool, &mcp.Tool{
			Name:        "container_" + tool.action,
			Description: tool.description,
		}, func(ctx context.Context, _ *mcp.CallToolRequest, in containerActIn) (*mcp.CallToolResult, actionOut, error) {
			out, err := r.actOnContainer(ctx, tool.action, in.containerArg, in.DryRun)

			return nil, out, err
		})
	}

	type stackActIn struct {
		stackArg
		dryRunArg
	}
	for _, tool := range []struct {
		action, description string
	}{
		{actStart, "Start every container of one compose stack that is not running, and read back how the stack stands. A stack that is wholly running is left as it is. It starts the containers that are there: it does not create ones that are missing or apply a changed compose file, which is deploying the stack."},
		{actStop, "Stop every container of one compose stack, and read back that the stack has stopped. The containers are kept, stopped, and stack_start brings them back. Each is given ten seconds to stop before it is killed, so a stack can take a while."},
		{actRestart, "Restart every container of one compose stack, and read back how the stack stands. It restarts the containers that are there: it does not pull newer images or apply a changed compose file, which is deploying the stack."},
	} {
		add(r, writeTool, &mcp.Tool{
			Name:        "stack_" + tool.action,
			Description: tool.description,
		}, func(ctx context.Context, _ *mcp.CallToolRequest, in stackActIn) (*mcp.CallToolResult, actionOut, error) {
			out, err := r.actOnStack(ctx, tool.action, in.stackArg, in.DryRun)

			return nil, out, err
		})
	}
}
