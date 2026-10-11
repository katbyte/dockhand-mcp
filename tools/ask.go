package tools

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"

	"github.com/katbyte/go-kt/parallel"
)

// askAtOnce is how many environments are asked at a time: each is another machine behind Dockhand, so they answer side by side, and Dockhand itself
// is one process that should not be handed every host at once.
const askAtOnce = 4

// envArg is the argument every tool about a Docker host takes. It is given as a name or as a number, and a number comes as often written as one as in
// quotes, so it is taken either way rather than refused for its type.
type envArg struct {
	Env any `json:"env,omitempty" jsonschema:"the environment (Docker host) to ask, by its name or its number as env_list gives them; left out, every environment is asked"`
}

// named is the environment an argument names, as text: a name as it came, a number as its digits, and nothing for an argument left out.
func (a envArg) named() (string, error) {
	switch env := a.Env.(type) {
	case nil:
		return "", nil
	case string:
		return strings.TrimSpace(env), nil
	case float64:
		if env != float64(int(env)) {
			return "", fmt.Errorf("env is %v: an environment's number is a whole one", env)
		}

		return strconv.Itoa(int(env)), nil
	default:
		return "", fmt.Errorf("env is %v: it is an environment's name or its number", env)
	}
}

// environment is one Docker host the Dockhand manages.
type environment struct {
	ID   int
	Name string
}

// envFailure is an environment that could not be asked, and why.
type envFailure struct {
	Env   string `json:"env"`
	Error string `json:"error" jsonschema:"why this environment could not be asked; what the others answered is still given"`
}

// environments is every Docker host the Dockhand manages, in the order it lists them.
func (r *registry) environments(ctx context.Context) ([]environment, error) {
	listed, err := r.dock.GetEnvironments(ctx)
	if err != nil {
		return nil, fmt.Errorf("the environments could not be listed: %w", refusal(err))
	}

	out := make([]environment, 0, len(listed.Model))
	for i := range listed.Model {
		out = append(out, environment{ID: listed.Model[i].Id, Name: listed.Model[i].Name})
	}

	return out, nil
}

// pick is the environments a call is about: the one named, by its name whatever its case or by its number, or every one when none is named. A name
// that is no environment's is refused with the ones there are.
func (r *registry) pick(ctx context.Context, in envArg) ([]environment, error) {
	arg, err := in.named()
	if err != nil {
		return nil, err
	}

	all, err := r.environments(ctx)
	if err != nil {
		return nil, err
	}
	if len(all) == 0 {
		return nil, errors.New("this Dockhand manages no Docker host yet: there is no environment to ask")
	}
	if arg == "" {
		return all, nil
	}

	number, isNumber := 0, false
	if n, err := strconv.Atoi(arg); err == nil {
		number, isNumber = n, true
	}
	for _, e := range all {
		if strings.EqualFold(e.Name, arg) || (isNumber && e.ID == number) {
			return []environment{e}, nil
		}
	}

	names := make([]string, 0, len(all))
	for _, e := range all {
		names = append(names, fmt.Sprintf("%s (%d)", e.Name, e.ID))
	}

	return nil, fmt.Errorf("no environment is named or numbered %q: this Dockhand has %s", arg, strings.Join(names, ", "))
}

// asked is what one environment answered, or why it did not.
type asked[T any] struct {
	Env   environment
	Value T
	Err   error
}

// ask puts one question to each environment, a few at a time, and gives the answers in the order the environments were given. One that fails is an
// answer with its error: the others are still asked, and still answer.
func ask[T any](ctx context.Context, envs []environment, question func(ctx context.Context, env environment) (T, error)) []asked[T] {
	answers := make([]asked[T], len(envs))
	// no job returns an error, so one environment failing ends nothing; what Each returns is then only the caller's own context ending, which every
	// answer already carries
	_ = parallel.Each(ctx, len(envs), askAtOnce, func(ctx context.Context, i int) error {
		value, err := question(ctx, envs[i])
		answers[i] = asked[T]{Env: envs[i], Value: value, Err: err}

		return nil
	})

	return answers
}
