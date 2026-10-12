package tools

import (
	"cmp"
	"context"
	"fmt"
	"maps"
	"regexp"
	"slices"
	"strings"
	"unicode/utf8"

	"github.com/katbyte/dockhand-mcp/sdk/dockhand"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

const (
	// defaultCron is when Dockhand updates a container on a schedule that was given no time of its own: three in the morning, every day
	defaultCron = "0 3 * * *"
	// holdNever is the criteria that holds no update back
	holdNever = "never"
	// notesFor is how many containers' release notes one call fetches: GitHub limits how often it may be asked
	notesFor = 5
	// noteChars is how much of one release's notes is given
	noteChars = 1500
	// updateOff is the value of the label that tells Dockhand to leave a container's image alone
	updateOff = "false"
)

// labelUpdate is the label a container carries to tell Dockhand not to look for a newer image for it, or to update it.
const labelUpdate = "dockhand.update"

// holdBacks is what Dockhand takes for when a scan of a newer image holds an update back, and what each means.
var holdBacks = []string{holdNever, "any", "critical_high", "critical", "more_than_current"}

// cronField is one of the five fields of a cron expression as Dockhand's scheduler reads them: numbers, with lists, ranges, steps and the star.
var cronField = regexp.MustCompile(`^(\*|\d+(-\d+)?)(/\d+)?(,(\*|\d+(-\d+)?)(/\d+)?)*$`)

// cronOf is a cron expression checked: five fields, each one the scheduler can read. Dockhand saves any text it is given and a schedule it cannot
// read never runs, so it is checked here.
func cronOf(given string) (string, error) {
	fields := strings.Fields(given)
	if len(fields) != 5 || slices.ContainsFunc(fields, func(field string) bool { return !cronField.MatchString(field) }) {
		return "", fmt.Errorf("cron is %q: it is five fields, minute, hour, day of the month, month and day of the week, as %q for three every morning or \"0 4 * * 0\" for four on Sundays", given, defaultCron)
	}

	return strings.Join(fields, " "), nil
}

// waitingUpdate is one container Dockhand has noted something newer for.
type waitingUpdate struct {
	Env        string        `json:"env"                 jsonschema:"the environment (Docker host) the container is on"`
	Container  string        `json:"container"`
	Image      string        `json:"image"               jsonschema:"the image it runs, by name and tag"`
	NewerImage bool          `json:"newer_image"         jsonschema:"the registry has another image under the same name and tag than the one it runs: container_update fetches it"`
	NewerTag   string        `json:"newer_tag,omitempty" jsonschema:"the newest version tag the registry has, when Dockhand is set to look for newer versions and found one: container_update with image moves to it"`
	Step       string        `json:"step,omitempty"      jsonschema:"how far a step the newer tag is: patch, minor or major"`
	Versions   []string      `json:"versions,omitempty"  jsonschema:"the version tags from the one after the tag in use to the newest"`
	CheckedAt  string        `json:"checked_at"          jsonschema:"when the registry was last asked"`
	Changelog  string        `json:"changelog,omitempty" jsonschema:"where the image's releases are written up, when notes were asked for and found"`
	Notes      []releaseNote `json:"notes,omitempty"     jsonschema:"what each newer version changed, when notes were asked for and found"`
}

// releaseNote is what one version of an image changed, as its makers wrote it up.
type releaseNote struct {
	Version   string `json:"version"`
	Name      string `json:"name,omitempty"`
	Published string `json:"published,omitempty"`
	URL       string `json:"url,omitempty"`
	Body      string `json:"body,omitempty"      jsonschema:"the release's notes, from their start"`
}

// uncheckedUpdate is a container a fresh check could not ask a registry about.
type uncheckedUpdate struct {
	Env       string `json:"env"`
	Container string `json:"container"`
	Image     string `json:"image"`
	Why       string `json:"why"       jsonschema:"why not: an image built on the host, a registry that needs credentials Dockhand has not got, a label that says to leave it alone"`
}

// hostUpdates is what one environment says of newer images: what it has noted, what a fresh check found when one was asked for, and the containers it
// has now.
type hostUpdates struct {
	Noted   []dockhand.GetContainersPendingUpdatesAnswerPendingUpdates
	Checked *dockhand.PostContainersCheckUpdatesAnswer
	Has     map[string]dockhand.GetContainersAnswer
}

// autoUpdate is one container's schedule for being updated by Dockhand itself.
type autoUpdate struct {
	Env         string `json:"env"                    jsonschema:"the environment (Docker host) the container is on"`
	Container   string `json:"container"`
	Cron        string `json:"cron"                   jsonschema:"when it is checked and updated, as five cron fields; empty for a schedule saved without one, which never runs"`
	Kind        string `json:"kind"                   jsonschema:"what Dockhand makes of the cron: daily, weekly or custom"`
	HoldBack    string `json:"hold_back"              jsonschema:"when a scan of the newer image holds the update back: never, any, critical_high, critical or more_than_current"`
	LastChecked string `json:"last_checked,omitempty" jsonschema:"when the schedule last looked, given when one container is asked for"`
	LastUpdated string `json:"last_updated,omitempty" jsonschema:"when it last updated the container"`
	Gone        bool   `json:"gone,omitempty"         jsonschema:"the host has no container of this name now: the schedule is left over"`
}

//nolint:gocyclo,maintidx // four tools registered in one function, each a handler of its own: the count is theirs added up
func registerContainerUpdateTools(r *registry) {
	type containerUpdatesIn struct {
		envArg

		Check bool `json:"check,omitempty" jsonschema:"ask the registries afresh before answering, which takes a few seconds a host and longer with many containers; left out, what the last check found is given"`
		Notes bool `json:"notes,omitempty" jsonschema:"fetch what each newer version changed, for the first few containers with a newer version tag, from the releases of the image's source on GitHub when the image says where that is"`
	}
	type containerUpdatesOut struct {
		Updates   []waitingUpdate   `json:"updates"             jsonschema:"the containers something newer is to be had for"`
		Total     int               `json:"total"`
		Checked   int               `json:"checked,omitempty"   jsonschema:"how many containers a fresh check asked about, when check was given"`
		Unchecked []uncheckedUpdate `json:"unchecked,omitempty" jsonschema:"the containers a fresh check could not ask a registry about, and why"`
		Stale     int               `json:"stale,omitempty"     jsonschema:"how many notes of an update are for containers that are gone: container_updates_clear forgets them"`
		Failed    []envFailure      `json:"failed,omitempty"    jsonschema:"the environments that could not be asked, and why; the others are still given"`
		Note      string            `json:"note,omitempty"`
	}
	add(r, readTool, &mcp.Tool{
		Name:        "container_updates",
		Description: "List the containers a newer image is to be had for, on every Docker host (environment) or on one. Dockhand notes two things: that the registry has another image under the very name and tag a container runs, which is what a moving tag such as latest does, and, when it is set to look for them, that the registry has a newer version tag, with how far a step it is and the versions between. Without check this is what the last check found, which may be old or may never have been made; with check the registries are asked now. Nothing on a host is changed either way: container_update is what fetches a newer image and makes the container again from it.",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in containerUpdatesIn) (*mcp.CallToolResult, containerUpdatesOut, error) {
		envs, err := r.pick(ctx, in.envArg)
		if err != nil {
			return nil, containerUpdatesOut{}, err
		}

		out := containerUpdatesOut{Updates: []waitingUpdate{}}
		var ids []located
		for _, answer := range ask(ctx, envs, func(ctx context.Context, env environment) (hostUpdates, error) {
			found := hostUpdates{Has: map[string]dockhand.GetContainersAnswer{}}
			if in.Check {
				checked, failed := r.dock.PostContainersCheckUpdates(ctx, dockhand.PostContainersCheckUpdatesOptions{Env: env.ID})
				if failed != nil {
					return found, fmt.Errorf("the registries could not be asked: %w", refusal(failed))
				}
				found.Checked = checked.Answer
			}
			noted, failed := r.dock.GetContainersPendingUpdates(ctx, dockhand.GetContainersPendingUpdatesOptions{Env: env.ID})
			if failed != nil {
				return found, refusal(failed)
			}
			found.Noted = noted.Answer.PendingUpdates

			containers, failed := r.containersOf(ctx, env)
			if failed != nil {
				return found, failed
			}
			for i := range containers {
				found.Has[containers[i].Id] = containers[i]
			}

			return found, nil
		}) {
			if answer.Err != nil {
				out.Failed = append(out.Failed, envFailure{Env: answer.Env.Name, Error: answer.Err.Error()})

				continue
			}
			for i := range answer.Value.Noted {
				n := &answer.Value.Noted[i]
				c, there := answer.Value.Has[n.ContainerId]
				if !there {
					out.Stale++

					continue
				}
				row := waitingUpdate{Env: answer.Env.Name, Container: n.ContainerName, Image: n.CurrentImage, NewerImage: n.HasImageUpdate, CheckedAt: when(n.CheckedAt)}
				if newer := n.NewerVersion; newer != nil {
					row.NewerTag, row.Step, row.Versions = newer.Tag, newer.Bump, newer.Skipped
				}
				out.Updates = append(out.Updates, row)
				ids = append(ids, located{Env: answer.Env, Container: c})
			}
			if checked := answer.Value.Checked; checked != nil {
				out.Checked += checked.Total
				for i := range checked.Results {
					result := &checked.Results[i]
					why := result.Error
					if result.UpdateDisabled {
						why = "it carries the label " + labelUpdate + "=" + updateOff + ", which tells Dockhand to leave its image alone"
					}
					if why != "" {
						out.Unchecked = append(out.Unchecked, uncheckedUpdate{Env: answer.Env.Name, Container: result.ContainerName, Image: result.ImageName, Why: why})
					}
				}
			}
		}
		if len(out.Failed) == len(envs) {
			return nil, out, fmt.Errorf("no environment could be asked what is newer: %s: %s", out.Failed[0].Env, out.Failed[0].Error)
		}
		out.Total = len(out.Updates)

		if in.Notes {
			asked, limited := 0, false
			for i := range out.Updates {
				row := &out.Updates[i]
				if len(row.Versions) == 0 || asked == notesFor {
					continue
				}
				asked++
				found, failed := r.dock.GetContainersByIdVersionNotes(ctx, ids[i].Container.Id, dockhand.GetContainersByIdVersionNotesOptions{Env: ids[i].Env.ID, Versions: strings.Join(row.Versions, ",")})
				if failed != nil {
					out.Note = note(out.Note, fmt.Sprintf("the release notes of %s could not be fetched: %s", row.Container, refusal(failed)))

					continue
				}
				limited = limited || found.Answer.RateLimited
				row.Changelog = found.Answer.ChangelogUrl
				for j := range found.Answer.Notes {
					n := &found.Answer.Notes[j]
					body := strings.TrimSpace(n.Body)
					if utf8.RuneCountInString(body) > noteChars {
						body = string([]rune(body)[:noteChars]) + " ..."
					}
					row.Notes = append(row.Notes, releaseNote{Version: n.Version, Name: n.Name, Published: when(n.PublishedAt), URL: n.Url, Body: body})
				}
			}
			if limited {
				out.Note = note(out.Note, "GitHub would answer no more for now, so some release notes are missing: it limits how often it is asked")
			}
		}

		slices.SortStableFunc(out.Updates, func(a, b waitingUpdate) int {
			return cmp.Or(cmp.Compare(a.Env, b.Env), cmp.Compare(a.Container, b.Container))
		})
		if !in.Check && out.Total == 0 {
			out.Note = note(out.Note, "Dockhand has nothing noted, which says only what its last check found: check asks the registries now")
		}
		if out.Stale > 0 {
			out.Note = note(out.Note, fmt.Sprintf("%d more notes are for containers that are gone, made again since under another id", out.Stale))
		}
		if len(out.Failed) > 0 {
			out.Note = note(out.Note, fmt.Sprintf("%d of the %d environments could not be asked, and their containers are missing from this", len(out.Failed), len(envs)))
		}

		return nil, out, nil
	})

	type updatesClearIn struct {
		envArg
		dryRunArg

		Container string `json:"container,omitempty" jsonschema:"only what is noted for the container of this name, or whose id starts with this; left out, everything noted for the environment"`
	}
	type updatesClearOut struct {
		Env     string   `json:"env"               jsonschema:"the environment (Docker host) whose notes were cleared"`
		Cleared []string `json:"cleared"           jsonschema:"the containers whose note was forgotten, by name; with dry_run, the ones that would be"`
		Left    int      `json:"left"              jsonschema:"how many notes Dockhand still has for the environment, read back"`
		DryRun  bool     `json:"dry_run,omitempty" jsonschema:"nothing was done"`
		Note    string   `json:"note,omitempty"`
	}
	add(r, writeTool, &mcp.Tool{
		Name:        "container_updates_clear",
		Description: "Make Dockhand forget what it has noted of newer images, for one container or for every container of one Docker host (environment): the marks container_updates lists and Dockhand's own pages show beside a container. Nothing on the host is touched, and no image or container changes. The next check notes again whatever is still so, so this is for clearing notes that are stale, of containers that are gone or were updated some other way, and for putting off a reminder.",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in updatesClearIn) (*mcp.CallToolResult, updatesClearOut, error) {
		env, err := r.one(ctx, in.envArg)
		if err != nil {
			return nil, updatesClearOut{}, err
		}
		out := updatesClearOut{Env: env.Name, Cleared: []string{}, DryRun: in.DryRun}

		noted, err := r.dock.GetContainersPendingUpdates(ctx, dockhand.GetContainersPendingUpdatesOptions{Env: env.ID})
		if err != nil {
			return nil, out, fmt.Errorf("what Dockhand has noted for %s could not be read, so nothing was cleared: %w", env.Name, refusal(err))
		}
		want := strings.TrimPrefix(strings.TrimSpace(in.Container), "/")
		var forget []dockhand.GetContainersPendingUpdatesAnswerPendingUpdates
		for i := range noted.Answer.PendingUpdates {
			n := noted.Answer.PendingUpdates[i]
			if want == "" || n.ContainerName == want || (len(want) >= minIDPrefix && strings.HasPrefix(n.ContainerId, want)) {
				forget = append(forget, n)
				out.Cleared = append(out.Cleared, n.ContainerName)
			}
		}
		slices.Sort(out.Cleared)
		out.Left = len(noted.Answer.PendingUpdates)
		switch {
		case len(forget) == 0 && want != "":
			out.Note = fmt.Sprintf("nothing was done: Dockhand has nothing noted for a container named %s on %s", want, env.Name)

			return nil, out, nil
		case len(forget) == 0:
			out.Note = "nothing was done: Dockhand has nothing noted for " + env.Name

			return nil, out, nil
		case in.DryRun:
			return nil, out, nil
		default:
		}

		if want == "" {
			_, err = r.dock.DeleteContainersPendingUpdates(ctx, dockhand.DeleteContainersPendingUpdatesOptions{Env: env.ID})
		}
		for i := 0; want != "" && err == nil && i < len(forget); i++ {
			_, err = r.dock.DeleteContainersPendingUpdates(ctx, dockhand.DeleteContainersPendingUpdatesOptions{Env: env.ID, ContainerId: forget[i].ContainerId})
		}
		if err != nil {
			out.Cleared = []string{}

			return nil, out, fmt.Errorf("what Dockhand has noted for %s was not all cleared: %w", env.Name, refusal(err))
		}

		after, err := r.dock.GetContainersPendingUpdates(ctx, dockhand.GetContainersPendingUpdatesOptions{Env: env.ID})
		if err != nil {
			out.Note = "what is left could not be read back: " + refusal(err).Error()

			return nil, out, nil
		}
		out.Left = len(after.Answer.PendingUpdates)

		return nil, out, nil
	})

	type autoUpdateIn struct {
		envArg

		Container string `json:"container,omitempty" jsonschema:"only the schedule of the container of this name, with when it last looked and last updated"`
	}
	type autoUpdateOut struct {
		Schedules []autoUpdate `json:"schedules"        jsonschema:"the containers Dockhand updates by itself, and when"`
		Total     int          `json:"total"`
		Failed    []envFailure `json:"failed,omitempty" jsonschema:"the environments that could not be asked, and why; the others are still given"`
		Note      string       `json:"note,omitempty"`
	}
	add(r, readTool, &mcp.Tool{
		Name:        "container_autoupdate",
		Description: "List the containers Dockhand updates by itself, on every Docker host (environment) or on one: each one's schedule as a cron expression, and whether a scan of the newer image may hold the update back. On its schedule Dockhand asks the registry, and when there is a newer image under the container's name and tag it pulls it and makes the container again, with nobody asked. A container with no schedule is only ever updated when someone does it. A schedule is kept by the container's name, so one whose container is gone is left over and says so.",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in autoUpdateIn) (*mcp.CallToolResult, autoUpdateOut, error) {
		envs, err := r.pick(ctx, in.envArg)
		if err != nil {
			return nil, autoUpdateOut{}, err
		}
		want := strings.TrimPrefix(strings.TrimSpace(in.Container), "/")

		type scheduled struct {
			Kept map[string]dockhand.GetAutoUpdateAnswer
			Has  []dockhand.GetContainersAnswer
		}
		out := autoUpdateOut{Schedules: []autoUpdate{}}
		for _, answer := range ask(ctx, envs, func(ctx context.Context, env environment) (scheduled, error) {
			kept, failed := r.dock.GetAutoUpdate(ctx, dockhand.GetAutoUpdateOptions{Env: env.ID})
			if failed != nil {
				return scheduled{}, refusal(failed)
			}
			has, failed := r.containersOf(ctx, env)

			return scheduled{Kept: kept.Answer, Has: has}, failed
		}) {
			if answer.Err != nil {
				out.Failed = append(out.Failed, envFailure{Env: answer.Env.Name, Error: answer.Err.Error()})

				continue
			}
			for _, name := range slices.Sorted(maps.Keys(answer.Value.Kept)) {
				if want != "" && name != want {
					continue
				}
				kept := answer.Value.Kept[name]
				row := autoUpdate{Env: answer.Env.Name, Container: name, Cron: kept.CronExpression, Kind: kept.ScheduleType, HoldBack: kept.VulnerabilityCriteria}
				row.Gone = !slices.ContainsFunc(answer.Value.Has, func(c dockhand.GetContainersAnswer) bool { return c.Name == name })
				if want != "" {
					// the list leaves out when the schedule last ran, which one container's own record has
					if one, failed := r.dock.GetAutoUpdateByContainerName(ctx, name, dockhand.GetAutoUpdateByContainerNameOptions{Env: answer.Env.ID}); failed == nil {
						row.LastChecked, row.LastUpdated = when(one.Answer.LastChecked), when(one.Answer.LastUpdated)
					}
				}
				out.Schedules = append(out.Schedules, row)
			}
		}
		if len(out.Failed) == len(envs) {
			return nil, out, fmt.Errorf("no environment could be asked what it updates by itself: %s: %s", out.Failed[0].Env, out.Failed[0].Error)
		}

		out.Total = len(out.Schedules)
		switch {
		case out.Total == 0 && want != "":
			out.Note = fmt.Sprintf("no container named %s is updated on a schedule on the environments asked: it is updated only when someone does it", want)
		case out.Total == 0:
			out.Note = "no container is updated on a schedule on the environments asked"
		default:
		}
		if len(out.Failed) > 0 {
			out.Note = note(out.Note, fmt.Sprintf("%d of the %d environments could not be asked, and their schedules are missing from this", len(out.Failed), len(envs)))
		}

		return nil, out, nil
	})

	type autoUpdateSetIn struct {
		containerArg
		dryRunArg

		Enabled  bool   `json:"enabled"             jsonschema:"true to have Dockhand update the container by itself on the schedule, false to stop it doing so and forget the schedule"`
		Cron     string `json:"cron,omitempty"      jsonschema:"when to check and update, as five cron fields: \"0 3 * * *\" for three every morning, \"0 4 * * 0\" for four on Sundays. Left out, the schedule it has, or three every morning for a new one"`
		HoldBack string `json:"hold_back,omitempty" jsonschema:"when a vulnerability scan of the newer image holds the update back: never, which is the default; any, for any finding; critical_high; critical; or more_than_current, for more findings than the image it runs. It only ever holds one back on an environment that scans images"`
	}
	type autoUpdateSetOut struct {
		Env       string   `json:"env"                 jsonschema:"the environment (Docker host) the container is on"`
		Container string   `json:"container"`
		Enabled   bool     `json:"enabled"             jsonschema:"whether Dockhand updates it by itself now, read back"`
		Cron      string   `json:"cron,omitempty"      jsonschema:"when, as five cron fields"`
		Kind      string   `json:"kind,omitempty"      jsonschema:"what Dockhand makes of the cron: daily, weekly or custom"`
		HoldBack  string   `json:"hold_back,omitempty"`
		Changes   []string `json:"changes"             jsonschema:"what was changed, in words"`
		DryRun    bool     `json:"dry_run,omitempty"   jsonschema:"nothing was done: changes says what would be"`
		Note      string   `json:"note,omitempty"`
	}
	add(r, writeTool, &mcp.Tool{
		Name:        "container_autoupdate_set",
		Description: "Have Dockhand update one container by itself on a schedule, change the schedule, or stop it. With a schedule, Dockhand asks the registry at the times the cron expression says and, when there is a newer image under the container's name and tag, pulls it and makes the container again without asking anyone: right for something that should follow its tag, and wrong for anything that must not restart unwatched or whose newer versions need a look first. hold_back has a scan of the newer image stop an update that would bring in vulnerabilities. The schedule is kept by the container's name. Read back from Dockhand.",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in autoUpdateSetIn) (*mcp.CallToolResult, autoUpdateSetOut, error) {
		hold := strings.ToLower(strings.TrimSpace(in.HoldBack))
		if hold != "" && !slices.Contains(holdBacks, hold) {
			return nil, autoUpdateSetOut{}, fmt.Errorf("hold_back is %q: it is one of %s", in.HoldBack, strings.Join(holdBacks, ", "))
		}
		cron := strings.TrimSpace(in.Cron)
		if cron != "" {
			checked, err := cronOf(cron)
			if err != nil {
				return nil, autoUpdateSetOut{}, err
			}
			cron = checked
		}

		at, err := r.find(ctx, in.containerArg)
		if err != nil {
			return nil, autoUpdateSetOut{}, err
		}
		name, env := at.Container.Name, at.Env
		out := autoUpdateSetOut{Env: env.Name, Container: name, Changes: []string{}, DryRun: in.DryRun}

		// a container with no schedule answers what one would be by default, with enabled false
		has, err := r.dock.GetAutoUpdateByContainerName(ctx, name, dockhand.GetAutoUpdateByContainerNameOptions{Env: env.ID})
		if err != nil {
			return nil, out, fmt.Errorf("the schedule of %s on %s could not be read, so nothing was changed: %w", name, env.Name, refusal(err))
		}
		was := has.Answer
		show := func(kept *dockhand.GetAutoUpdateByContainerNameAnswer) {
			if out.Enabled = kept.Enabled; out.Enabled {
				out.Cron, out.Kind, out.HoldBack = kept.CronExpression, kept.ScheduleType, kept.VulnerabilityCriteria
			}
		}
		show(was)

		if !in.Enabled {
			if !was.Enabled {
				out.Note = "nothing was done: Dockhand does not update it by itself"

				return nil, out, nil
			}
			if in.DryRun {
				out.Changes = append(out.Changes, "would stop Dockhand updating "+name+" by itself and forget its schedule, "+was.CronExpression)

				return nil, out, nil
			}
			if _, err := r.dock.DeleteAutoUpdateByContainerName(ctx, name, dockhand.DeleteAutoUpdateByContainerNameOptions{Env: env.ID}); err != nil {
				return nil, out, fmt.Errorf("the schedule of %s on %s was not removed: %w", name, env.Name, refusal(err))
			}
			out.Changes = append(out.Changes, "stopped Dockhand updating "+name+" by itself and forgot its schedule, "+was.CronExpression)
		} else {
			// what is not given is kept from the schedule it has, and a new one gets the defaults
			if cron == "" && was.Enabled {
				cron = was.CronExpression
			}
			cron = cmp.Or(cron, defaultCron)
			if hold == "" && was.Enabled {
				hold = was.VulnerabilityCriteria
			}
			hold = cmp.Or(hold, holdNever)

			var would, did string
			switch {
			case !was.Enabled:
				would, did = fmt.Sprintf("would set Dockhand to update %s by itself at %s, holding an update back: %s", name, cron, hold), fmt.Sprintf("set Dockhand to update %s by itself at %s, holding an update back: %s", name, cron, hold)
			case cron != was.CronExpression || hold != was.VulnerabilityCriteria:
				would, did = fmt.Sprintf("would change the schedule of %s from %s (holding back: %s) to %s (holding back: %s)", name, was.CronExpression, was.VulnerabilityCriteria, cron, hold), fmt.Sprintf("changed the schedule of %s from %s (holding back: %s) to %s (holding back: %s)", name, was.CronExpression, was.VulnerabilityCriteria, cron, hold)
			default:
				out.Note = "nothing was done: that is its schedule already"

				return nil, out, nil
			}
			if at.Container.Labels[labelUpdate] == updateOff {
				out.Note = "it carries the label " + labelUpdate + "=" + updateOff + ", which tells Dockhand to leave its image alone: the schedule runs and finds nothing to do until the label is gone"
			}
			if in.DryRun {
				out.Changes = append(out.Changes, would)

				return nil, out, nil
			}
			if _, err := r.dock.PostAutoUpdateByContainerName(ctx, name, dockhand.PostAutoUpdateByContainerNameRequest{Enabled: new(true), CronExpression: cron, VulnerabilityCriteria: hold}, dockhand.PostAutoUpdateByContainerNameOptions{Env: env.ID}); err != nil {
				return nil, out, fmt.Errorf("the schedule of %s on %s was not saved: %w", name, env.Name, refusal(err))
			}
			out.Changes = append(out.Changes, did)
		}

		after, err := r.dock.GetAutoUpdateByContainerName(ctx, name, dockhand.GetAutoUpdateByContainerNameOptions{Env: env.ID})
		if err != nil {
			out.Note = noted(out.Note, "the schedule as it is now could not be read back: "+refusal(err).Error())

			return nil, out, nil
		}
		out.Cron, out.Kind, out.HoldBack = "", "", ""
		show(after.Answer)
		if out.Enabled != in.Enabled {
			out.Note = noted(out.Note, "Dockhand took the change and the schedule read back is not as it was asked for")
		}

		return nil, out, nil
	})
}
