package tools

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"math"
	"net/http"
	"slices"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/katbyte/dockhand-mcp/sdk/dockhand"
	"github.com/katbyte/pandorest/client"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

const (
	// defaultRuns is how many runs schedule_history gives unless told
	defaultRuns = 20
	// maxRuns is the most it gives in one call
	maxRuns = 200
	// typeSystem is the type of the jobs Dockhand runs to keep itself tidy, and typeDeploy that of a stack brought up, which is kept as a run and
	// belongs to no schedule
	typeSystem = "system_cleanup"
	typeDeploy = "stack_deploy"
	// runQueued and runRunning are how a run stands before it has ended
	runQueued  = "queued"
	runRunning = "running"
)

var (
	// scheduleTypes is what Dockhand does by itself, in its own names for them
	scheduleTypes = []string{"container_update", "env_update_check", "image_prune", "git_stack_sync", "backup", "repo_prune", "repo_check", "repo_verify", typeSystem, "deploy_log_reconcile"}
	// runStatuses is how a run can stand
	runStatuses = []string{runQueued, runRunning, "success", "warning", "failed", "skipped"}
	// runTriggers is what can set a run off
	runTriggers = []string{"cron", "manual", "webhook"}
	// pausable is the system jobs Dockhand lets be switched off, by their numbers: the clearing of old runs, of old container events and of the
	// scanners' cache
	pausable = []int{1, 2, 4}
)

// typeShown is a type as the tools give it: Dockhand's own name for it with hyphens for its underscores, since written Dockhand's way three of them
// are the names of tools, container_update among them, and a type is no tool to call.
func typeShown(kind string) string { return strings.ReplaceAll(kind, "_", "-") }

// typeAsked is a type as Dockhand names it, from one given either way.
func typeAsked(given string) string {
	return strings.ReplaceAll(strings.ToLower(strings.TrimSpace(given)), "-", "_")
}

// typesShown is every type a schedule can be, as the tools give them.
func typesShown() string { return typeShown(strings.Join(scheduleTypes, ", ")) }

// scheduleRun is one run of something Dockhand does by itself.
type scheduleRun struct {
	Run     int            `json:"run"                    jsonschema:"the run's number"`
	Type    string         `json:"type"                   jsonschema:"what kind of thing ran: container-update, env-update-check, image-prune, git-stack-sync, backup, system-cleanup, or stack-deploy for a stack brought up"`
	ID      int            `json:"id,omitempty"           jsonschema:"the number of the schedule it was a run of, among those of its type; absent for a run that belongs to no schedule"`
	Name    string         `json:"name,omitempty"         jsonschema:"what it was a run of: a container, an environment, a stack or a system job, by name"`
	Env     string         `json:"env,omitempty"          jsonschema:"the environment it ran for; absent for Dockhand's own jobs"`
	Status  string         `json:"status"                 jsonschema:"queued, running, success, warning, failed or skipped"`
	By      string         `json:"triggered_by,omitempty" jsonschema:"what set it off: cron for its schedule, manual for someone, webhook"`
	At      string         `json:"at,omitempty"           jsonschema:"when it was set off"`
	Seconds float64        `json:"seconds,omitempty"      jsonschema:"how long it took"`
	Error   string         `json:"error,omitempty"        jsonschema:"why it failed, when it did"`
	Details map[string]any `json:"details,omitempty"      jsonschema:"what it did, by its kind: what a prune removed, what a check found, why an update was skipped"`
}

// envNamed is the name of the environment with a number, the number itself where no environment listed has it, and nothing for none.
func envNamed(envs []environment, id int) string {
	if id == 0 {
		return ""
	}
	if at := slices.IndexFunc(envs, func(e environment) bool { return int(e.ID) == id }); at >= 0 {
		return envs[at].Name
	}

	return "#" + strconv.Itoa(id)
}

// scheduleRunOf is a run as the tools answer with it.
func scheduleRunOf(e *dockhand.GetSchedulesExecutionsAnswerExecutions, envs []environment) scheduleRun {
	return scheduleRun{Run: e.Id, Type: typeShown(e.ScheduleType), ID: e.ScheduleId, Name: e.EntityName, Env: envNamed(envs, int(e.EnvironmentId)), Status: string(e.Status), By: e.TriggeredBy, At: when(cmp.Or(e.TriggeredAt, e.StartedAt)), Seconds: math.Round(float64(e.Duration)/100) / 10, Error: strings.TrimSpace(e.ErrorMessage), Details: e.Details}
}

// scheduleRow is one thing Dockhand does by itself.
type scheduleRow struct {
	Type    string       `json:"type"               jsonschema:"what kind of thing it is: container-update (a container kept on the newest image), env-update-check (an environment's containers checked for newer images), image-prune, git-stack-sync, backup, repo-prune, repo-check, repo-verify, system-cleanup or deploy-log-reconcile (Dockhand keeping itself tidy)"`
	ID      int          `json:"id"                 jsonschema:"its number among those of its type: with the type, what names it to the other schedule tools"`
	Name    string       `json:"name"`
	Does    string       `json:"does,omitempty"     jsonschema:"what it does, in Dockhand's words"`
	Env     string       `json:"env,omitempty"      jsonschema:"the environment it is for; absent for Dockhand's own jobs"`
	Enabled bool         `json:"enabled"            jsonschema:"whether it runs when its time comes"`
	Cron    string       `json:"cron,omitempty"     jsonschema:"when it runs, as a cron expression in its environment's timezone: the minute, the hour, the day of the month, the month and the day of the week"`
	NextRun string       `json:"next_run,omitempty" jsonschema:"when it next runs; absent for one that is switched off"`
	System  bool         `json:"system,omitempty"   jsonschema:"one of Dockhand's own jobs, which can be run but not removed"`
	LastRun *scheduleRun `json:"last_run,omitempty" jsonschema:"its latest run; absent for one that has never run"`
}

// schedules is everything Dockhand does by itself, as it lists them, with the environments to name each by.
func (r *registry) schedules(ctx context.Context) ([]dockhand.GetSchedulesAnswerSchedules, []environment, error) {
	envs, err := r.environments(ctx)
	if err != nil {
		return nil, nil, err
	}
	listed, err := r.dock.GetSchedules(ctx)
	if err != nil {
		return nil, nil, fmt.Errorf("the schedules could not be listed: %w", refusal(err))
	}

	return listed.Answer.Schedules, envs, nil
}

// scheduleArg names one schedule, as schedule_list gives them.
type scheduleArg struct {
	Type string `json:"type" jsonschema:"the schedule's type, as schedule_list gives it: container-update, env-update-check, image-prune, system-cleanup and so on"`
	ID   int    `json:"id"   jsonschema:"the schedule's number among those of its type, as schedule_list gives it"`
}

// checked is the type a schedule argument names, once it is one there is.
func (a scheduleArg) checked() (string, error) {
	kind := typeAsked(a.Type)
	if !slices.Contains(scheduleTypes, kind) {
		return "", fmt.Errorf("type is %q: a schedule's type is one of %s", a.Type, typesShown())
	}
	if a.ID < 1 {
		return "", errors.New("id is required: the schedule's number among those of its type, as schedule_list gives it")
	}

	return kind, nil
}

// scheduled is the one schedule an argument names, as Dockhand lists it now, or nil when it lists none: an environment's check for newer images is
// only listed while it is switched on.
func (r *registry) scheduled(ctx context.Context, a scheduleArg) (kind string, found *dockhand.GetSchedulesAnswerSchedules, envs []environment, err error) {
	if kind, err = a.checked(); err != nil {
		return "", nil, nil, err
	}
	listed, envs, err := r.schedules(ctx)
	if err != nil {
		return kind, nil, nil, err
	}
	if at := slices.IndexFunc(listed, func(s dockhand.GetSchedulesAnswerSchedules) bool { return string(s.Type) == kind && s.Id == a.ID }); at >= 0 {
		found = &listed[at]
	}

	return kind, found, envs, nil
}

// latestRun is the newest run of a schedule, or nil when it has none.
func (r *registry) latestRun(ctx context.Context, kind string, id int) (*dockhand.GetSchedulesExecutionsAnswerExecutions, error) {
	runs, err := r.dock.GetSchedulesExecutions(ctx, dockhand.GetSchedulesExecutionsOptions{ScheduleType: kind, ScheduleId: &id, Limit: new(1)})
	if err != nil {
		return nil, refusal(err)
	}
	if len(runs.Answer.Executions) == 0 {
		return nil, nil //nolint:nilnil // a schedule that has never run has no latest run, and that is no error
	}

	return &runs.Answer.Executions[0], nil
}

func registerScheduleTools(r *registry) {
	type scheduleListIn struct {
		envArg

		Type       string `json:"type,omitempty"        jsonschema:"only the schedules of this type: container-update, env-update-check, image-prune, git-stack-sync, backup, system-cleanup"`
		HideSystem bool   `json:"hide_system,omitempty" jsonschema:"leave out Dockhand's own jobs, the ones that keep it tidy"`
	}
	type scheduleListOut struct {
		Schedules []scheduleRow `json:"schedules"      jsonschema:"what Dockhand does by itself: the ones for containers and environments first, then its own jobs"`
		Total     int           `json:"total"`
		Note      string        `json:"note,omitempty"`
	}
	add(r, readTool, &mcp.Tool{
		Name:        "schedule_list",
		Description: "List everything the Dockhand does by itself on a schedule: containers it keeps on the newest image, environments whose containers it checks for newer images, images it prunes, git stacks it syncs, backups it takes, and its own jobs that keep it tidy. Each is given with whether it is switched on, when it runs and next runs, and how its latest run went. It is how to see what will change a host with nobody asking, and whether what was meant to happen overnight did. A schedule is named by its type and its number together. An environment's check for newer images is listed only while it is switched on.",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in scheduleListIn) (*mcp.CallToolResult, scheduleListOut, error) {
		kind := typeAsked(in.Type)
		if kind != "" && !slices.Contains(scheduleTypes, kind) {
			return nil, scheduleListOut{}, fmt.Errorf("type is %q: a schedule's type is one of %s", in.Type, typesShown())
		}
		named, err := in.named()
		if err != nil {
			return nil, scheduleListOut{}, err
		}
		only := 0
		if named != "" {
			env, failed := r.one(ctx, in.envArg)
			if failed != nil {
				return nil, scheduleListOut{}, failed
			}
			only = int(env.ID)
		}
		listed, envs, err := r.schedules(ctx)
		if err != nil {
			return nil, scheduleListOut{}, err
		}

		out := scheduleListOut{Schedules: []scheduleRow{}}
		for i := range listed {
			s := &listed[i]
			if (kind != "" && string(s.Type) != kind) || (only != 0 && int(s.EnvironmentId) != only) || (in.HideSystem && s.IsSystem) {
				continue
			}
			row := scheduleRow{Type: typeShown(string(s.Type)), ID: s.Id, Name: s.Name, Does: s.Description, Env: envNamed(envs, int(s.EnvironmentId)), Enabled: s.Enabled, Cron: s.CronExpression, NextRun: when(s.NextRun), System: s.IsSystem}
			if s.LastExecution != nil {
				last := scheduleRunOf(new(dockhand.GetSchedulesExecutionsAnswerExecutions(*s.LastExecution)), envs)
				row.LastRun = &last
			}
			out.Schedules = append(out.Schedules, row)
		}
		slices.SortStableFunc(out.Schedules, func(a, b scheduleRow) int {
			return cmp.Or(cmp.Compare(b2i(a.System), b2i(b.System)), cmp.Compare(a.Env, b.Env), cmp.Compare(a.Type, b.Type), cmp.Compare(a.Name, b.Name))
		})
		if out.Total = len(out.Schedules); out.Total == 0 {
			out.Note = "nothing matched: no schedule of that kind, or for that environment"
		}

		return nil, out, nil
	})

	type scheduleHistoryIn struct {
		envArg
		revealArg

		Run         int      `json:"run,omitempty"          jsonschema:"one run by its number, in full: with what it logged"`
		Type        string   `json:"type,omitempty"         jsonschema:"only runs of this type: a schedule's type, or stack-deploy for stacks brought up"`
		ID          int      `json:"id,omitempty"           jsonschema:"with type: only the runs of the schedule with this number"`
		Status      []string `json:"status,omitempty"       jsonschema:"only runs that stand like this: failed, warning, success, skipped, running, queued"`
		TriggeredBy string   `json:"triggered_by,omitempty" jsonschema:"only runs set off this way: cron, manual or webhook"`
		Since       string   `json:"since,omitempty"        jsonschema:"only runs from this on: how long ago (10m, 2h, 36h) or a time (2026-10-10T07:00:00Z)"`
		Until       string   `json:"until,omitempty"        jsonschema:"only runs up to this: how long ago or a time, as since"`
		Limit       int      `json:"limit,omitempty"        jsonschema:"the most runs to give, 20 unless given and 200 at most; total counts every one that matched"`
		Offset      int      `json:"offset,omitempty"       jsonschema:"how many of the latest to pass over, to read further back"`
		MaxChars    int      `json:"max_chars,omitempty"    jsonschema:"with run: how much of its log to give, from its end: 20000 characters unless given, 200000 at most"`
	}
	type scheduleHistoryOut struct {
		Runs      []scheduleRun `json:"runs"                jsonschema:"the runs, the latest first"`
		Total     int           `json:"total"               jsonschema:"how many runs matched"`
		Log       string        `json:"log,omitempty"       jsonschema:"with run: what the run logged, its end when it is long"`
		Truncated bool          `json:"truncated,omitempty" jsonschema:"the log is longer than what is given"`
		Hidden    int           `json:"hidden,omitempty"    jsonschema:"how many secrets in the log are hidden"`
		Note      string        `json:"note,omitempty"`
	}
	add(r, readTool, &mcp.Tool{
		Name:        "schedule_history",
		Description: "List the runs of what the Dockhand does by itself, the latest first: each with what it was a run of, whether it succeeded, failed or was skipped, what set it off, how long it took, why it failed, and what it did, such as what a prune removed or why an update was skipped. Narrow it by type, by one schedule, by environment, by how the run stands, by what set it off and by time: status failed is everything that went wrong unasked. Give run for one run in full, with what it logged line by line. A stack brought up is kept as a run as well, of type stack-deploy. Dockhand keeps runs for a month.",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in scheduleHistoryIn) (*mcp.CallToolResult, scheduleHistoryOut, error) {
		if in.MaxChars < 0 || in.MaxChars > maxFileChars {
			return nil, scheduleHistoryOut{}, fmt.Errorf("max_chars is %d: it is how much of a run's log to give, from 1 to %d, or left out for %d", in.MaxChars, maxFileChars, defaultFileChars)
		}
		envs, err := r.environments(ctx)
		if err != nil {
			return nil, scheduleHistoryOut{}, err
		}

		// one run, with what it logged
		if in.Run != 0 {
			read, failed := r.dock.GetSchedulesExecutionsById(ctx, in.Run)
			if client.StatusCode(failed) == http.StatusNotFound {
				return nil, scheduleHistoryOut{}, fmt.Errorf("there is no run numbered %d: Dockhand keeps runs for a month, and schedule_history without run lists the ones it has", in.Run)
			}
			if failed != nil {
				return nil, scheduleHistoryOut{}, fmt.Errorf("run %d could not be read: %w", in.Run, refusal(failed))
			}
			out := scheduleHistoryOut{Runs: []scheduleRun{scheduleRunOf(new(dockhand.GetSchedulesExecutionsAnswerExecutions(*read.Answer)), envs)}, Total: 1}
			log := strings.TrimSpace(read.Answer.Logs)
			if !in.RevealSecrets {
				log, out.Hidden = maskText(log)
			}
			if limit, chars := cmp.Or(in.MaxChars, defaultFileChars), utf8.RuneCountInString(log); chars > limit {
				log, out.Truncated = string([]rune(log)[chars-limit:]), true
				out.Note = fmt.Sprintf("the log is %d characters and its last %d are given: raise max_chars for more", chars, limit)
			}
			if out.Log = log; log == "" {
				out.Note = "the run logged nothing"
			}
			if out.Hidden > 0 {
				out.Note = noted(out.Note, hiddenNote(out.Hidden, "told by the names beside them"))
			}

			return nil, out, nil
		}

		kind := typeAsked(in.Type)
		if kind != "" && kind != typeDeploy && !slices.Contains(scheduleTypes, kind) {
			return nil, scheduleHistoryOut{}, fmt.Errorf("type is %q: a run's type is one of %s, or %s", in.Type, typesShown(), typeShown(typeDeploy))
		}
		if in.ID != 0 && kind == "" {
			return nil, scheduleHistoryOut{}, errors.New("id is a schedule's number among those of its type, so it is given with type")
		}
		statuses := make([]string, 0, len(in.Status))
		for _, status := range in.Status {
			status = strings.ToLower(strings.TrimSpace(status))
			if !slices.Contains(runStatuses, status) {
				return nil, scheduleHistoryOut{}, fmt.Errorf("status has %q: a run stands as one of %s", status, strings.Join(runStatuses, ", "))
			}
			statuses = append(statuses, status)
		}
		by := strings.ToLower(strings.TrimSpace(in.TriggeredBy))
		if by != "" && !slices.Contains(runTriggers, by) {
			return nil, scheduleHistoryOut{}, fmt.Errorf("triggered_by is %q: a run is set off by one of %s", in.TriggeredBy, strings.Join(runTriggers, ", "))
		}
		now := time.Now()
		since, err := written(in.Since, now)
		if err != nil {
			return nil, scheduleHistoryOut{}, fmt.Errorf("since: %w", err)
		}
		until, err := written(in.Until, now)
		if err != nil {
			return nil, scheduleHistoryOut{}, fmt.Errorf("until: %w", err)
		}
		if in.Limit < 0 || in.Limit > maxRuns || in.Offset < 0 {
			return nil, scheduleHistoryOut{}, fmt.Errorf("limit is %d and offset %d: limit is how many runs to give at most, from 1 to %d or left out for %d, and offset how many of the latest to pass over", in.Limit, in.Offset, maxRuns, defaultRuns)
		}
		limit := cmp.Or(in.Limit, defaultRuns)
		options := dockhand.GetSchedulesExecutionsOptions{ScheduleType: kind, Statuses: strings.Join(statuses, ","), TriggeredBy: by, FromDate: since, ToDate: until, Limit: &limit}
		if in.ID != 0 {
			options.ScheduleId = &in.ID
		}
		if in.Offset != 0 {
			options.Offset = &in.Offset
		}
		named, err := in.named()
		if err != nil {
			return nil, scheduleHistoryOut{}, err
		}
		if named != "" {
			env, failed := r.one(ctx, in.envArg)
			if failed != nil {
				return nil, scheduleHistoryOut{}, failed
			}
			options.EnvironmentId = env.ID
		}

		listed, err := r.dock.GetSchedulesExecutions(ctx, options)
		if err != nil {
			return nil, scheduleHistoryOut{}, fmt.Errorf("the runs could not be listed: %w", refusal(err))
		}
		out := scheduleHistoryOut{Runs: make([]scheduleRun, 0, len(listed.Answer.Executions)), Total: listed.Answer.Total}
		for i := range listed.Answer.Executions {
			out.Runs = append(out.Runs, scheduleRunOf(&listed.Answer.Executions[i], envs))
		}
		switch {
		case out.Total > in.Offset+len(out.Runs):
			out.Note = fmt.Sprintf("%d runs matched and %d are given: narrow it, raise limit, or pass over these with offset %d", out.Total, len(out.Runs), in.Offset+len(out.Runs))
		case out.Total == 0:
			out.Note = "nothing matched: no run of that kind in the month Dockhand keeps them"
		default:
		}

		return nil, out, nil
	})

	type scheduleRunIn struct {
		scheduleArg
		waitArg
		dryRunArg
	}
	type scheduleRunOut struct {
		Type    string       `json:"type"`
		ID      int          `json:"id"`
		Name    string       `json:"name,omitempty"`
		Run     *scheduleRun `json:"run,omitempty"     jsonschema:"the run that was set off, as it stood when this answered"`
		Running bool         `json:"running"           jsonschema:"whether the run is still going. True means this answer came before it ended: schedule_history with its number says how it went"`
		Changes []string     `json:"changes"           jsonschema:"what was done, in words"`
		DryRun  bool         `json:"dry_run,omitempty" jsonschema:"nothing was done: changes says what would be"`
		Note    string       `json:"note,omitempty"`
	}
	add(r, writeTool, &mcp.Tool{
		Name:        "schedule_run",
		Description: "Run now one thing the Dockhand does on a schedule, without waiting for its time, whether or not it is switched on: named by its type and number, as schedule_list gives them. It does at once what the schedule would do, and that can change a host: a container-update pulls the newer image and makes the container again when there is one, an env-update-check that is set to update does the same for every container of its environment, and an image-prune removes images. It waits for the run to end, 50 seconds unless wait says otherwise, and answers how it went and what it did; a run still going by then is answered as running, with its number.",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in scheduleRunIn) (*mcp.CallToolResult, scheduleRunOut, error) {
		patience, err := in.patience()
		if err != nil {
			return nil, scheduleRunOut{}, err
		}
		kind, found, envs, err := r.scheduled(ctx, in.scheduleArg)
		if err != nil {
			return nil, scheduleRunOut{}, err
		}
		out := scheduleRunOut{Type: typeShown(kind), ID: in.ID, Changes: []string{}, DryRun: in.DryRun}
		what := fmt.Sprintf("the %s numbered %d", typeShown(kind), in.ID)
		if found != nil {
			out.Name = found.Name
			what = fmt.Sprintf("%q (%s %d)", found.Name, typeShown(kind), in.ID)
		}
		if in.DryRun {
			out.Changes = append(out.Changes, "would run "+what+" now")
			if found == nil {
				out.Note = "no schedule of that type and number is listed, which is how an environment's check for newer images stands while it is switched off: Dockhand says whether there is one when it is run"
			}

			return nil, out, nil
		}

		// the run it sets off is the one newer than the newest there was
		before, err := r.latestRun(ctx, kind, in.ID)
		if err != nil {
			return nil, out, fmt.Errorf("the runs of %s could not be read, so it was not run: %w", what, err)
		}
		if _, err := r.dock.PostSchedulesByTypeByIdRun(ctx, kind, in.ID); err != nil {
			return nil, out, fmt.Errorf("%s was not run: %w", what, refusal(err))
		}
		out.Changes = append(out.Changes, "ran "+what)

		deadline := time.Now().Add(patience)
		for {
			latest, failed := r.latestRun(ctx, kind, in.ID)
			if failed != nil {
				out.Note = "it was set off, and its run could not be read: " + failed.Error()

				return nil, out, nil //nolint:nilerr // it was set off: that its run could not be read is said in the answer
			}
			if latest != nil && (before == nil || latest.Id != before.Id) {
				run := scheduleRunOf(latest, envs)
				out.Run, out.Running = &run, latest.Status == runQueued || latest.Status == runRunning
			}
			if (out.Run != nil && !out.Running) || !time.Now().Add(r.poll()).Before(deadline) {
				break
			}

			select {
			case <-ctx.Done():
				return nil, out, fmt.Errorf("%s was still running when this call was ended: %w", what, ctx.Err())
			case <-time.After(r.poll()):
			}
		}
		switch {
		case out.Run == nil:
			out.Running = true
			out.Note = "it was set off, and Dockhand has not yet listed its run: schedule_history lists it once it has"
		case out.Running:
			out.Note = fmt.Sprintf("the run is still going: schedule_history with run %d says how it went, and what it logged", out.Run.Run)
		default:
		}

		return nil, out, nil
	})

	type scheduleSetIn struct {
		scheduleArg
		dryRunArg

		Enabled bool `json:"enabled" jsonschema:"true to have it run when its time comes, false to stop it running until it is switched on again"`
	}
	type scheduleSetOut struct {
		Type    string   `json:"type"`
		ID      int      `json:"id"`
		Name    string   `json:"name,omitempty"`
		Enabled bool     `json:"enabled"            jsonschema:"whether it runs when its time comes, as Dockhand has it now"`
		NextRun string   `json:"next_run,omitempty" jsonschema:"when it next runs, once switched on"`
		Changes []string `json:"changes"            jsonschema:"what was done, in words; empty when it stood that way already"`
		DryRun  bool     `json:"dry_run,omitempty"  jsonschema:"nothing was done: changes says what would be"`
		Note    string   `json:"note,omitempty"`
	}
	add(r, writeTool, &mcp.Tool{
		Name:        "schedule_set",
		Description: "Switch one thing the Dockhand does on a schedule on or off, named by its type and number as schedule_list gives them. Switched off it keeps its time and everything else about it, and does not run until it is switched on again; one that stands that way already is left as it is. Of Dockhand's own jobs three can be switched off, the clearing of old runs, of old container events and of the scanners' cache, and the others cannot.",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in scheduleSetIn) (*mcp.CallToolResult, scheduleSetOut, error) {
		kind, found, _, err := r.scheduled(ctx, in.scheduleArg)
		if err != nil {
			return nil, scheduleSetOut{}, err
		}
		out := scheduleSetOut{Type: typeShown(kind), ID: in.ID, Changes: []string{}, DryRun: in.DryRun}
		system := kind == typeSystem || kind == "deploy_log_reconcile"
		if system && (kind != typeSystem || !slices.Contains(pausable, in.ID)) {
			return nil, out, fmt.Errorf("the %s numbered %d is one of Dockhand's own jobs that cannot be switched off: the three that can are the system-cleanup jobs numbered 1, 2 and 4", typeShown(kind), in.ID)
		}
		// an environment's check for newer images is not listed while it is off, and that is the one a schedule not listed can be
		if found == nil && (kind != "env_update_check" || !in.Enabled) {
			if kind == "env_update_check" {
				out.Note = "nothing to do: an environment's check for newer images that is not listed is switched off already"

				return nil, out, nil
			}

			return nil, out, fmt.Errorf("there is no %s numbered %d: schedule_list gives the ones there are", typeShown(kind), in.ID)
		}
		word := map[bool]string{true: "on", false: "off"}[in.Enabled]
		what := fmt.Sprintf("the %s numbered %d", typeShown(kind), in.ID)
		if found != nil {
			out.Name, out.Enabled, out.NextRun = found.Name, found.Enabled, when(found.NextRun)
			what = fmt.Sprintf("%q (%s %d)", found.Name, typeShown(kind), in.ID)
			if found.Enabled == in.Enabled {
				out.Note = "nothing to do: it is switched " + word + " already"

				return nil, out, nil
			}
		}
		if in.DryRun {
			out.Changes = append(out.Changes, "would switch "+what+" "+word)

			return nil, out, nil
		}

		// Dockhand has no call that sets it one way: the one it has turns it over, which is why how it stands was read first
		var now bool
		if system {
			toggled, failed := r.dock.PostSchedulesSystemByIdToggle(ctx, in.ID)
			if failed != nil {
				return nil, out, fmt.Errorf("%s was not switched %s: %w", what, word, refusal(failed))
			}
			now = toggled.Answer.Enabled
		} else {
			toggled, failed := r.dock.PostSchedulesByTypeByIdToggle(ctx, kind, in.ID)
			if failed != nil {
				return nil, out, fmt.Errorf("%s was not switched %s: %w", what, word, refusal(failed))
			}
			now = toggled.Answer.Enabled
		}
		if now != in.Enabled {
			return nil, out, fmt.Errorf("%s was turned over and now stands switched %s, which is not what was asked: something else switched it at the same moment, and schedule_list says how it stands", what, map[bool]string{true: "on", false: "off"}[now])
		}
		out.Changes = append(out.Changes, "switched "+what+" "+word)
		out.Enabled, out.NextRun = in.Enabled, ""
		if _, back, _, failed := r.scheduled(ctx, in.scheduleArg); failed == nil && back != nil {
			out.Name, out.Enabled, out.NextRun = back.Name, back.Enabled, when(back.NextRun)
		}

		return nil, out, nil
	})

	type scheduleDeleteIn struct {
		scheduleArg

		Confirm bool `json:"confirm,omitempty" jsonschema:"remove it: without this the answer says what would go and nothing is removed"`
	}
	type scheduleDeleteOut struct {
		Type    string   `json:"type"`
		ID      int      `json:"id"`
		Name    string   `json:"name,omitempty"`
		Removed bool     `json:"removed"            jsonschema:"the schedule was removed, and is read back as no longer listed"`
		WouldGo []string `json:"would_go,omitempty" jsonschema:"what removing it takes away, said before anything is removed"`
		Note    string   `json:"note,omitempty"`
	}
	add(r, deleteTool, &mcp.Tool{
		Name:        "schedule_delete",
		Description: "Remove one thing the Dockhand does on a schedule, named by its type and number as schedule_list gives them, so that it no longer happens by itself: a container is no longer kept on the newest image, an environment's containers are no longer checked, its images no longer pruned. What it acted on is not touched, and its past runs are kept. Dockhand forgets a container's schedule and an environment's pruning altogether, time and all, and only switches off an environment's check for newer images, keeping how it was set. Its own jobs cannot be removed. Without confirm the answer says what would go and removes nothing.",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in scheduleDeleteIn) (*mcp.CallToolResult, scheduleDeleteOut, error) {
		kind, found, _, err := r.scheduled(ctx, in.scheduleArg)
		if err != nil {
			return nil, scheduleDeleteOut{}, err
		}
		out := scheduleDeleteOut{Type: typeShown(kind), ID: in.ID}
		if kind == typeSystem || kind == "deploy_log_reconcile" {
			return nil, out, fmt.Errorf("the %s numbered %d is one of Dockhand's own jobs, which cannot be removed", typeShown(kind), in.ID)
		}
		// Dockhand answers that it removed a schedule whether or not there was one, so that there is one is looked for first
		if found == nil {
			return nil, out, fmt.Errorf("there is no %s numbered %d: schedule_list gives the ones there are", typeShown(kind), in.ID)
		}
		out.Name = found.Name
		if !in.Confirm {
			out.WouldGo = []string{fmt.Sprintf("%q (%s %d): it would no longer run by itself", found.Name, typeShown(kind), in.ID)}
			out.Note = "nothing was removed: call again with confirm to remove it. What it acts on is not touched either way, and its past runs are kept"

			return nil, out, nil
		}

		gone, err := r.dock.DeleteSchedulesByTypeById(ctx, kind, in.ID)
		if err != nil {
			return nil, out, fmt.Errorf("%q was not removed: %w", found.Name, refusal(err))
		}
		if !gone.Answer.Success {
			return nil, out, fmt.Errorf("the removal of %q was answered without its being said to have succeeded: schedule_list says whether it is still there", found.Name)
		}
		_, left, _, err := r.scheduled(ctx, in.scheduleArg)
		switch {
		case err != nil:
			out.Note = "it was removed, and the schedules could not be listed afterwards: " + err.Error()
		case left != nil && left.Enabled:
			return nil, out, fmt.Errorf("the removal of %q was answered as done, and it is still listed and switched on", found.Name)
		case left != nil:
			out.Removed = true
			out.Note = "Dockhand keeps it listed, switched off: it no longer runs by itself"
		default:
			out.Removed = true
		}

		return nil, out, nil
	})

	type historyDeleteIn struct {
		Run     int  `json:"run"               jsonschema:"the run's number, as schedule_history gives it"`
		Confirm bool `json:"confirm,omitempty" jsonschema:"remove it: without this the answer says what would go and nothing is removed"`
	}
	type historyDeleteOut struct {
		Run     *scheduleRun `json:"run,omitempty"  jsonschema:"the run whose record it is"`
		Removed bool         `json:"removed"        jsonschema:"the record was removed, and is read back as gone"`
		Note    string       `json:"note,omitempty"`
	}
	add(r, deleteTool, &mcp.Tool{
		Name:        "schedule_history_delete",
		Description: "Remove the record of one run of what the Dockhand does by itself, with what it logged, by the run's number. Nothing the run did is undone and its schedule is untouched: only the record goes, and it cannot be got back. Dockhand removes records older than a month by itself. Without confirm the answer says which run would go and removes nothing.",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in historyDeleteIn) (*mcp.CallToolResult, historyDeleteOut, error) {
		if in.Run < 1 {
			return nil, historyDeleteOut{}, errors.New("run is required: the run's number, as schedule_history gives it")
		}
		envs, err := r.environments(ctx)
		if err != nil {
			return nil, historyDeleteOut{}, err
		}
		read, err := r.dock.GetSchedulesExecutionsById(ctx, in.Run)
		if client.StatusCode(err) == http.StatusNotFound {
			return nil, historyDeleteOut{}, fmt.Errorf("there is no run numbered %d: schedule_history lists the ones Dockhand has", in.Run)
		}
		if err != nil {
			return nil, historyDeleteOut{}, fmt.Errorf("run %d could not be read, so nothing was removed: %w", in.Run, refusal(err))
		}
		run := scheduleRunOf(new(dockhand.GetSchedulesExecutionsAnswerExecutions(*read.Answer)), envs)
		out := historyDeleteOut{Run: &run}
		if !in.Confirm {
			out.Note = "nothing was removed: call again with confirm to remove the record of this run and what it logged"

			return nil, out, nil
		}

		if _, err := r.dock.DeleteSchedulesExecutionsById(ctx, in.Run); err != nil {
			return nil, out, fmt.Errorf("the record of run %d was not removed: %w", in.Run, refusal(err))
		}
		switch _, err := r.dock.GetSchedulesExecutionsById(ctx, in.Run); {
		case client.StatusCode(err) == http.StatusNotFound:
			out.Removed = true
		case err != nil:
			out.Note = "it was removed, and that it is gone could not be read back: " + refusal(err).Error()
		default:
			return nil, out, fmt.Errorf("the removal of the record of run %d was answered as done, and it is still there", in.Run)
		}

		return nil, out, nil
	})
}
