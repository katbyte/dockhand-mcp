package tools

import (
	"cmp"
	"context"
	"fmt"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/katbyte/dockhand-mcp/sdk/dockhand"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

const (
	// defaultEvents is how many events container_events gives unless told
	defaultEvents = 50
	// maxEvents is the most it gives in one call
	maxEvents = 500
)

// eventActions is what Docker calls the things a container does, which is what Dockhand keeps them under.
var eventActions = []string{"create", "start", "die", "kill", "stop", "restart", "destroy", "pause", "unpause", "rename", "update", "oom", "health_status: healthy", "health_status: unhealthy"}

// containerEvent is one thing a container did, as Dockhand kept it.
type containerEvent struct {
	At        string `json:"at"                  jsonschema:"when"`
	Env       string `json:"env"                 jsonschema:"the environment (Docker host) the container is or was on"`
	Container string `json:"container"`
	Action    string `json:"action"              jsonschema:"what it did, in Docker's word: create, start, die, kill, stop, restart, destroy, pause, unpause, rename, update, oom, health_status: healthy or health_status: unhealthy"`
	ExitCode  *int   `json:"exit_code,omitempty" jsonschema:"for a death, what the container exited with: 0 when it ended as it meant to, 137 when it was killed, as for using too much memory"`
	Signal    string `json:"signal,omitempty"    jsonschema:"for a kill, the signal it was sent: 15 asks it to stop, 9 does not ask"`
	Was       string `json:"was,omitempty"       jsonschema:"for a rename, the name it had"`
	Image     string `json:"image,omitempty"`
	Stack     string `json:"stack,omitempty"     jsonschema:"the compose stack the container belonged to, when it belonged to one"`
}

// written is a moment as a caller gives one, how long ago or a time, written the way the activity log takes it.
func written(given string, now time.Time) (string, error) {
	seconds, err := moment(given, now)
	if err != nil || seconds == "" {
		return "", err
	}
	at, err := strconv.ParseInt(seconds, 10, 64)
	if err != nil {
		return "", err
	}

	return time.Unix(at, 0).UTC().Format(time.RFC3339), nil
}

func registerContainerEventTools(r *registry) {
	type containerEventsIn struct {
		envArg

		Container string   `json:"container,omitempty" jsonschema:"only the containers whose name has this in it"`
		Actions   []string `json:"actions,omitempty"   jsonschema:"only these actions, in Docker's words: die and oom for containers that went down, kill, restart, start, health_status: unhealthy"`
		Since     string   `json:"since,omitempty"     jsonschema:"only events from this on: how long ago (10m, 2h, 36h) or a time (2026-10-10T07:00:00Z)"`
		Until     string   `json:"until,omitempty"     jsonschema:"only events up to this: how long ago or a time, as since"`
		Limit     int      `json:"limit,omitempty"     jsonschema:"the most events to give, 50 unless given and 500 at most; total counts every one that matched"`
		Counts    bool     `json:"counts,omitempty"    jsonschema:"also count the whole log by action, for the environment asked or for all: how many deaths, kills and restarts it holds"`
	}
	type containerEventsOut struct {
		Events   []containerEvent `json:"events"              jsonschema:"what the containers did, the latest first"`
		Total    int              `json:"total"               jsonschema:"how many events matched"`
		ByAction map[string]int   `json:"by_action,omitempty" jsonschema:"with counts: how many events of each action the log holds, whatever else was asked for"`
		Note     string           `json:"note,omitempty"`
	}
	add(r, readTool, &mcp.Tool{
		Name:        "container_events",
		Description: "List what the containers did and when, on every Docker host (environment) or on one, the latest first: each time one was made, started, died and with what exit code, was killed and by what signal, was stopped, restarted, renamed or removed, ran out of memory, or changed health. It is how to learn what happened to a container while nobody was looking, that it died at three in the morning with exit code 137, or has been started forty times since yesterday, and it knows of containers that are gone, which no list of containers does. Narrow it by a part of the container's name, by action and by time. Dockhand keeps this for the hosts it is set to collect activity from, for a month, from when it began watching the host: what happened before that is not here.",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in containerEventsIn) (*mcp.CallToolResult, containerEventsOut, error) {
		now := time.Now()
		since, err := written(in.Since, now)
		if err != nil {
			return nil, containerEventsOut{}, fmt.Errorf("since: %w", err)
		}
		until, err := written(in.Until, now)
		if err != nil {
			return nil, containerEventsOut{}, fmt.Errorf("until: %w", err)
		}
		actions := []string{}
		for _, action := range in.Actions {
			action = strings.ToLower(strings.TrimSpace(action))
			if !slices.Contains(eventActions, action) {
				return nil, containerEventsOut{}, fmt.Errorf("actions has %q: an action is one of %s", action, strings.Join(eventActions, ", "))
			}
			actions = append(actions, action)
		}
		if in.Limit < 0 || in.Limit > maxEvents {
			return nil, containerEventsOut{}, fmt.Errorf("limit is %d: it is how many events to give at most, from 1 to %d, or left out for %d", in.Limit, maxEvents, defaultEvents)
		}
		limit := cmp.Or(in.Limit, defaultEvents)

		// the log is one for every host, and is asked once: for one environment, or for all
		options := dockhand.GetActivityOperationOptions{ContainerName: strings.TrimPrefix(strings.TrimSpace(in.Container), "/"), Actions: strings.Join(actions, ","), FromDate: since, ToDate: until, Limit: &limit}
		counted := dockhand.GetActivityStatsOperationOptions{}
		named, err := in.named()
		if err != nil {
			return nil, containerEventsOut{}, err
		}
		if named != "" {
			env, failed := r.one(ctx, in.envArg)
			if failed != nil {
				return nil, containerEventsOut{}, failed
			}
			options.EnvironmentId, counted.EnvironmentId = &env.ID, &env.ID
		}

		kept, err := r.dock.GetActivity(ctx, options)
		if err != nil {
			return nil, containerEventsOut{}, fmt.Errorf("the activity log could not be read: %w", refusal(err))
		}
		out := containerEventsOut{Events: make([]containerEvent, 0, len(kept.Model.Events)), Total: kept.Model.Total}
		for i := range kept.Model.Events {
			e := &kept.Model.Events[i]
			event := containerEvent{At: e.Timestamp, Env: e.EnvironmentName, Container: e.ContainerName, Action: e.Action, Signal: e.ActorAttributes["signal"], Was: strings.TrimPrefix(e.ActorAttributes["oldName"], "/"), Image: e.Image, Stack: e.ActorAttributes[labelStack]}
			if code, failed := strconv.Atoi(e.ActorAttributes["exitCode"]); failed == nil && e.Action == "die" {
				event.ExitCode = &code
			}
			out.Events = append(out.Events, event)
		}

		switch {
		case out.Total > len(out.Events):
			out.Note = fmt.Sprintf("%d events matched and the latest %d are given: narrow it by container, actions or time, or raise limit", out.Total, len(out.Events))
		case out.Total == 0:
			out.Note = "nothing matched: Dockhand keeps what containers did only for hosts it collects activity from, and only since it began watching them"
		default:
		}
		if in.Counts {
			stats, failed := r.dock.GetActivityStats(ctx, counted)
			if failed != nil {
				out.Note = note(out.Note, "the log could not be counted by action: "+refusal(failed).Error())
			} else {
				out.ByAction = stats.Model.ByAction
			}
		}

		return nil, out, nil
	})

	type eventsClearIn struct {
		Confirm bool `json:"confirm,omitempty" jsonschema:"clear it: without this the answer says how much would go and nothing is cleared"`
	}
	type eventsClearOut struct {
		Events  int    `json:"events"         jsonschema:"how many events the log held before"`
		Cleared bool   `json:"cleared"        jsonschema:"whether the log was cleared, read back as empty; false with confirm left out"`
		Note    string `json:"note,omitempty"`
	}
	add(r, deleteTool, &mcp.Tool{
		Name:        "container_events_clear",
		Description: "Clear Dockhand's whole record of what containers did, for every Docker host at once: every start, death, kill and restart it has kept. Nothing on any host is touched, and nothing that happens afterwards is lost, but what happened before cannot be got back: it is the only place a container that is gone is remembered. Dockhand takes no part of it, one host's or one container's: it is all or nothing. Without confirm the answer says how many events would go and clears nothing.",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in eventsClearIn) (*mcp.CallToolResult, eventsClearOut, error) {
		held, err := r.dock.GetActivityStats(ctx, dockhand.GetActivityStatsOperationOptions{})
		if err != nil {
			return nil, eventsClearOut{}, fmt.Errorf("the activity log could not be read, so nothing was cleared: %w", refusal(err))
		}
		out := eventsClearOut{Events: held.Model.Total}
		switch {
		case out.Events == 0:
			out.Note = "nothing to clear: the log holds no events"

			return nil, out, nil
		case !in.Confirm:
			out.Note = "nothing was cleared: call again with confirm to clear the log of every host"

			return nil, out, nil
		default:
		}

		if _, err := r.dock.DeleteActivity(ctx); err != nil {
			return nil, out, fmt.Errorf("the activity log was not cleared: %w", refusal(err))
		}

		// what the hosts go on doing is kept from the moment it is cleared, so a log read back with a few events in it was cleared all the same
		left, err := r.dock.GetActivityStats(ctx, dockhand.GetActivityStatsOperationOptions{})
		switch {
		case err != nil:
			out.Note = "that it is cleared could not be read back: " + refusal(err).Error()
		case left.Model.Total >= out.Events:
			out.Note = fmt.Sprintf("Dockhand said it cleared the log, and it holds %d events", left.Model.Total)
		default:
			out.Cleared = true
		}

		return nil, out, nil
	})
}
