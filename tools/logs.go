package tools

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/katbyte/dockhand-mcp/sdk/dockhand"
	"github.com/katbyte/go-kt/parallel"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

const (
	// defaultTail is how many lines of a log are given unless told otherwise, and maxTail the most: a line is a few dozen tokens, and a log read
	// whole would be most of a conversation
	defaultTail = 100
	maxTail     = 2000
	// defaultStackTail is how many lines of each container are given for a whole stack
	defaultStackTail = 50
)

// logArgs is how much of a log a call asks for, which every tool that reads one takes the same way.
type logArgs struct {
	Tail     int    `json:"tail,omitempty"     jsonschema:"how many lines to give, counted from the end: 100 unless given, 2000 at most"`
	Since    string `json:"since,omitempty"    jsonschema:"only lines from this on: how long ago (10m, 2h, 1h30m) or a time (2026-10-10T07:00:00Z)"`
	Until    string `json:"until,omitempty"    jsonschema:"only lines up to this: how long ago or a time, as since"`
	Contains string `json:"contains,omitempty" jsonschema:"only lines containing this, whatever its case"`

	revealArg
}

// moment is a point in time as a caller gives one - how long ago, or a time - in seconds since 1970, which is the one form the API takes: it refuses
// a written time, and reads a length of time as something else. "" is no bound.
func moment(given string, now time.Time) (string, error) {
	given = strings.TrimSpace(given)
	if given == "" {
		return "", nil
	}
	if ago, err := time.ParseDuration(given); err == nil {
		if ago < 0 {
			return "", fmt.Errorf("%q is a length of time in the future: give how long ago, as 10m or 2h", given)
		}

		return strconv.FormatInt(now.Add(-ago).Unix(), 10), nil
	}
	if at, err := time.Parse(time.RFC3339, given); err == nil {
		return strconv.FormatInt(at.Unix(), 10), nil
	}

	return "", fmt.Errorf("%q is neither how long ago (10m, 2h, 1h30m) nor a time (2026-10-10T07:00:00Z)", given)
}

// logLine is one line of a container's log.
type logLine struct {
	// At is when Docker took the line, zero when the line carries no time
	At   time.Time
	Text string
}

// logLines splits what the API answers, one text with each line led by its time, into its lines.
func logLines(text string) []logLine {
	var out []logLine
	for line := range strings.SplitSeq(strings.TrimRight(text, "\n"), "\n") {
		if line == "" {
			continue
		}
		stamp, rest, cut := strings.Cut(line, " ")
		if at, err := time.Parse(time.RFC3339Nano, stamp); cut && err == nil {
			out = append(out, logLine{At: at, Text: rest})

			continue
		}
		out = append(out, logLine{Text: line})
	}

	return out
}

// stamped is a line as it is given: led by its time to the second, in UTC, when it has one.
func stamped(at time.Time, text string) string {
	if at.IsZero() {
		return text
	}

	return at.UTC().Format(time.RFC3339) + " " + text
}

// logOf reads the end of one container's log, within the bounds asked for.
func (r *registry) logOf(ctx context.Context, env environment, id string, tail int, since, until string) ([]logLine, error) {
	answer, err := r.dock.GetContainersByIdLogs(ctx, id, dockhand.GetContainersByIdLogsOperationOptions{Env: &env.ID, Tail: &tail, Since: since, Until: until})
	if err != nil {
		return nil, refusal(err)
	}

	return logLines(answer.Model.Logs), nil
}

// bounds checks how much of a log is asked for, and gives it as the API takes it.
func (a *logArgs) bounds(fallback int) (tail int, since, until string, err error) {
	if a.Tail < 0 || a.Tail > maxTail {
		return 0, "", "", fmt.Errorf("tail is %d: it is how many lines to give from the end, from 1 to %d, or left out for %d", a.Tail, maxTail, fallback)
	}
	now := time.Now()
	if since, err = moment(a.Since, now); err != nil {
		return 0, "", "", fmt.Errorf("since: %w", err)
	}
	if until, err = moment(a.Until, now); err != nil {
		return 0, "", "", fmt.Errorf("until: %w", err)
	}

	return cmp.Or(a.Tail, fallback), since, until, nil
}

// kept is a line's text as a call is given it: with its secrets hidden, unless the call asked for those too, and how many were. It reports false for
// a line that does not hold what the call asked for.
func (a *logArgs) kept(l *logLine) (text string, hidden int, ok bool) {
	if a.Contains != "" && !strings.Contains(strings.ToLower(l.Text), strings.ToLower(a.Contains)) {
		return "", 0, false
	}
	if a.RevealSecrets {
		return l.Text, 0, true
	}
	text, hidden = maskText(l.Text)

	return text, hidden, true
}

func registerLogTools(r *registry) {
	type containerLogsIn struct {
		containerArg
		logArgs
	}
	type containerLogsOut struct {
		Env       string `json:"env"`
		Container string `json:"container"`
		State     string `json:"state"          jsonschema:"the container's state now, since a log is read of one that has stopped as of one that runs"`
		Log       string `json:"log"            jsonschema:"the lines, oldest first, each led by when Docker took it, in UTC"`
		Lines     int    `json:"lines"          jsonschema:"how many lines are given"`
		Hidden    int    `json:"hidden"         jsonschema:"how many secrets are hidden in them; reveal_secrets shows them"`
		Note      string `json:"note,omitempty"`
	}
	add(r, readTool, &mcp.Tool{
		Name:        "container_logs",
		Description: "Read the end of one container's log, what it wrote to its output and its errors: the last 100 lines unless told otherwise, each with its time. Narrow it with since and until, each given as how long ago (10m, 2h) or as a time, and with contains for lines holding a word. Name the container as container_list does; env is only needed when the same name is on more than one host. It reads a stopped container's log as well as a running one's, which is where to see why it stopped. Passwords, tokens and keys in the lines are hidden, told by the names beside them, unless reveal_secrets asks for them. What is given is what Docker still holds on the host: a host that rotates or ships its logs keeps less here.",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in containerLogsIn) (*mcp.CallToolResult, containerLogsOut, error) {
		tail, since, until, err := in.bounds(defaultTail)
		if err != nil {
			return nil, containerLogsOut{}, err
		}
		at, err := r.find(ctx, in.containerArg)
		if err != nil {
			return nil, containerLogsOut{}, err
		}

		lines, err := r.logOf(ctx, at.Env, at.Container.Id, tail, since, until)
		if err != nil {
			return nil, containerLogsOut{}, fmt.Errorf("the log of %s on %s could not be read: %w", at.Container.Name, at.Env.Name, err)
		}

		out := containerLogsOut{Env: at.Env.Name, Container: at.Container.Name, State: at.Container.State}
		shown := make([]string, 0, len(lines))
		for i := range lines {
			if text, hidden, ok := in.kept(&lines[i]); ok {
				shown = append(shown, stamped(lines[i].At, text))
				out.Hidden += hidden
			}
		}
		out.Log, out.Lines = strings.Join(shown, "\n"), len(shown)
		switch {
		case len(lines) == 0:
			out.Note = "the container has written nothing in the stretch asked for"
		case len(shown) == 0:
			out.Note = fmt.Sprintf("none of the %d lines read holds %q", len(lines), in.Contains)
		case len(lines) == tail:
			out.Note = fmt.Sprintf("these are the last %d lines, and the log may hold more before them: raise tail, or bound it with since", tail)
		default:
		}
		if out.Hidden > 0 {
			out.Note = note(out.Note, hiddenNote(out.Hidden, "told by the names beside them"))
		}

		return nil, out, nil
	})

	type stackLogsIn struct {
		envArg

		Stack string `json:"stack" jsonschema:"the compose stack (project) whose containers' logs to read, by its name as stack_list gives it"`

		logArgs
	}
	type stackLogSource struct {
		Service   string `json:"service"`
		Container string `json:"container"`
		State     string `json:"state"`
		Lines     int    `json:"lines"           jsonschema:"how many of the lines given are this container's"`
		Error     string `json:"error,omitempty" jsonschema:"why this container's log could not be read; the others' are still given"`
	}
	type stackLogsOut struct {
		Env        string           `json:"env"`
		Stack      string           `json:"stack"`
		Log        string           `json:"log"            jsonschema:"the lines of every container, in the order they were written, each led by its time in UTC and the service that wrote it"`
		Lines      int              `json:"lines"          jsonschema:"how many lines are given"`
		Containers []stackLogSource `json:"containers"     jsonschema:"the stack's containers, each with how many of the lines are its own"`
		Hidden     int              `json:"hidden"         jsonschema:"how many secrets are hidden in the lines; reveal_secrets shows them"`
		Note       string           `json:"note,omitempty"`
	}
	add(r, readTool, &mcp.Tool{
		Name:        "stack_logs",
		Description: "Read the logs of every container of one compose stack together, as one log in the order the lines were written, each line led by its time and the service that wrote it: the last 50 lines of each container unless tail says otherwise. Narrow it with since, until and contains, as container_logs. It is how a stack is read as one thing: what one service logged just before another failed. env is only needed when a stack of that name is on more than one host. Secrets in the lines are hidden unless reveal_secrets asks for them.",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in stackLogsIn) (*mcp.CallToolResult, stackLogsOut, error) {
		if strings.TrimSpace(in.Stack) == "" {
			return nil, stackLogsOut{}, errors.New("stack is required: the stack's name, as stack_list gives it")
		}
		tail, since, until, err := in.bounds(defaultStackTail)
		if err != nil {
			return nil, stackLogsOut{}, err
		}
		envs, err := r.pick(ctx, in.envArg)
		if err != nil {
			return nil, stackLogsOut{}, err
		}

		// the stack's containers, wherever it is: on one environment, or the call is refused until it says which
		var members []located
		var on []string
		for _, answer := range ask(ctx, envs, r.containersOf) {
			if answer.Err != nil {
				return nil, stackLogsOut{}, fmt.Errorf("the containers of %s could not be listed: %w", answer.Env.Name, answer.Err)
			}
			found := false
			for i := range answer.Value {
				if answer.Value[i].Labels[labelStack] == in.Stack {
					members, found = append(members, located{Env: answer.Env, Container: answer.Value[i]}), true
				}
			}
			if found {
				on = append(on, answer.Env.Name)
			}
		}
		switch {
		case len(on) == 0:
			return nil, stackLogsOut{}, fmt.Errorf("no stack is named %q on the environments asked: stack_list gives the ones there are", in.Stack)
		case len(on) > 1:
			return nil, stackLogsOut{}, fmt.Errorf("a stack named %q is on %s: say which with env", in.Stack, strings.Join(on, " and "))
		default:
		}

		// each container's log is its own call, made a few at a time; one that fails is said beside the rest
		type one struct {
			lines []logLine
			err   error
		}
		read := make([]one, len(members))
		_ = parallel.Each(ctx, len(members), askAtOnce, func(ctx context.Context, i int) error {
			read[i].lines, read[i].err = r.logOf(ctx, members[i].Env, members[i].Container.Id, tail, since, until)

			return nil
		})

		out := stackLogsOut{Env: on[0], Stack: in.Stack, Containers: make([]stackLogSource, 0, len(members))}
		type line struct {
			at   time.Time
			text string
		}
		var all []line
		failed := 0
		for i := range members {
			service := cmp.Or(members[i].Container.Labels[labelService], members[i].Container.Name)
			source := stackLogSource{Service: service, Container: members[i].Container.Name, State: members[i].Container.State}
			if read[i].err != nil {
				source.Error = read[i].err.Error()
				failed++
			}
			for j := range read[i].lines {
				if text, hidden, ok := in.kept(&read[i].lines[j]); ok {
					all = append(all, line{at: read[i].lines[j].At, text: stamped(read[i].lines[j].At, service+" | "+text)})
					out.Hidden += hidden
					source.Lines++
				}
			}
			out.Containers = append(out.Containers, source)
		}
		if failed == len(members) {
			return nil, out, fmt.Errorf("the log of no container of %s could be read: %s: %s", in.Stack, out.Containers[0].Container, out.Containers[0].Error)
		}

		slices.SortStableFunc(all, func(a, b line) int { return a.at.Compare(b.at) })
		texts := make([]string, 0, len(all))
		for i := range all {
			texts = append(texts, all[i].text)
		}
		slices.SortFunc(out.Containers, func(a, b stackLogSource) int { return cmp.Compare(a.Service, b.Service) })
		out.Log, out.Lines = strings.Join(texts, "\n"), len(texts)

		if failed > 0 {
			out.Note = fmt.Sprintf("the log of %d of the %d containers could not be read, and is missing from this", failed, len(members))
		}
		if out.Hidden > 0 {
			out.Note = note(out.Note, hiddenNote(out.Hidden, "told by the names beside them"))
		}

		return nil, out, nil
	})
}
