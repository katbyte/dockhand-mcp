package tools

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"

	"github.com/katbyte/dockhand-mcp/sdk/dockhand"
)

// minIDPrefix is the least of an id that names a container: fewer characters than this match too many to be meant.
const minIDPrefix = 4

// containerArg is the arguments of a tool about one container: which, and where to look for it.
type containerArg struct {
	envArg

	Container string `json:"container" jsonschema:"the container, by its name or by the start of its id, as container_list gives them"`
}

// located is one container and the environment it is on.
type located struct {
	Env       environment
	Container dockhand.GetContainersResponse
}

// where is a container's own row, with the environment it is on.
func (l *located) row() containerRow {
	return containerRowOf(l.Env.Name, &l.Container)
}

// find is the one container a call names, looked for on the environment given or on every one: by its name, and failing that by the start of its id.
// One that is on several environments under one name is not guessed at: the call is refused, naming them, until it says which.
func (r *registry) find(ctx context.Context, in containerArg) (located, error) {
	want := strings.TrimPrefix(strings.TrimSpace(in.Container), "/")
	if want == "" {
		return located{}, errors.New("container is required: the container's name, or the start of its id, as container_list gives them")
	}

	envs, err := r.pick(ctx, in.envArg)
	if err != nil {
		return located{}, err
	}

	var named, byID []located
	var failed []string
	for _, answer := range ask(ctx, envs, r.containersOf) {
		if answer.Err != nil {
			failed = append(failed, answer.Env.Name+": "+answer.Err.Error())

			continue
		}
		for i := range answer.Value {
			c := answer.Value[i]
			switch {
			case c.Name == want:
				named = append(named, located{Env: answer.Env, Container: c})
			case len(want) >= minIDPrefix && strings.HasPrefix(c.Id, want):
				byID = append(byID, located{Env: answer.Env, Container: c})
			default:
			}
		}
	}

	matches := named
	if len(matches) == 0 {
		matches = byID
	}
	switch {
	case len(matches) == 1:
		return matches[0], nil
	case len(matches) > 1:
		at := make([]string, 0, len(matches))
		for i := range matches {
			at = append(at, matches[i].Container.Name+" on "+matches[i].Env.Name)
		}
		slices.Sort(at)

		return located{}, fmt.Errorf("%q is more than one container (%s): say which with env, or with more of its id", want, strings.Join(at, ", "))
	case len(failed) > 0:
		return located{}, fmt.Errorf("no container named %q was found, and not every environment could be asked: %s", want, strings.Join(failed, "; "))
	default:
		names := make([]string, 0, len(envs))
		for _, e := range envs {
			names = append(names, e.Name)
		}

		return located{}, fmt.Errorf("no container is named %q, or has an id that starts with it, on %s: container_list gives the ones there are", want, strings.Join(names, ", "))
	}
}
