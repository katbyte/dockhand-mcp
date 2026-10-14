package tools

import (
	"cmp"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strconv"
	"strings"

	"github.com/katbyte/dockhand-mcp/sdk/dockhand"
	"github.com/katbyte/pandorest/client"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// The two kinds of thing Dockhand backs up, in its own words.
const (
	backupOfContainer = "container"
	backupOfStack     = "stack"
)

// ownPolicy is what Dockhand writes for a backup that runs by itself when it is given nothing to keep, and for one given a policy of nothing.
const ownPolicy = "7 daily, 4 weekly and 6 monthly"

// errBackupsOff is what every backup tool answers on a Dockhand whose backups are switched off, which is how one comes.
var errBackupsOff = errors.New("this Dockhand has its backups switched off, as one is until it is started with them on: they are a beta its container has only with FEAT_BACKUPS_ENABLED=true in its environment")

// backupArg names one backup the Dockhand keeps.
type backupArg struct {
	Backup any `json:"backup,omitempty" jsonschema:"the backup, by its number as backup_list gives them, or by the name of the container or stack it is of when only one backup is of that name"`
}

// backupKeep is how many snapshots a backup keeps: the newest of them, and one for each of the latest days, weeks, months and years.
type backupKeep struct {
	Last    int `json:"last,omitempty"    jsonschema:"the newest this many, whenever they were taken"`
	Daily   int `json:"daily,omitempty"   jsonschema:"the latest of each of this many days"`
	Weekly  int `json:"weekly,omitempty"  jsonschema:"the latest of each of this many weeks"`
	Monthly int `json:"monthly,omitempty" jsonschema:"the latest of each of this many months"`
	Yearly  int `json:"yearly,omitempty"  jsonschema:"the latest of each of this many years"`
}

// none reports whether nothing is said of what to keep, which keeps every snapshot.
func (k *backupKeep) none() bool { return k == nil || *k == backupKeep{} }

// said is what is kept, in words.
func (k *backupKeep) said() string {
	if k.none() {
		return "every snapshot"
	}
	var parts []string
	for _, part := range []struct {
		n    int
		what string
	}{{k.Last, "newest"}, {k.Daily, "daily"}, {k.Weekly, "weekly"}, {k.Monthly, "monthly"}, {k.Yearly, "yearly"}} {
		if part.n > 0 {
			parts = append(parts, strconv.Itoa(part.n)+" "+part.what)
		}
	}

	return strings.Join(parts, ", ")
}

// backupRow is one backup as the tools give it: what is backed up, where to, when, and how its last run went.
type backupRow struct {
	ID          int            `json:"id"`
	Of          string         `json:"of"                    jsonschema:"the container or stack that is backed up, by name"`
	Type        string         `json:"type"                  jsonschema:"container or stack"`
	Env         string         `json:"env,omitempty"         jsonschema:"the environment (Docker host) it is on; absent for one saved with none, which cannot run"`
	Destination string         `json:"destination"           jsonschema:"where its snapshots are kept, by the destination's name"`
	Schedule    string         `json:"schedule,omitempty"    jsonschema:"when it runs by itself, as cron; absent for one that runs only when backup_run asks"`
	Enabled     bool           `json:"enabled"               jsonschema:"whether its schedule is switched on. One switched off still runs when asked"`
	Keep        *backupKeep    `json:"keep,omitempty"        jsonschema:"how many snapshots are kept, the older ones being removed after each run; absent when every one is kept"`
	Volumes     []string       `json:"volumes,omitempty"     jsonschema:"the volumes that are backed up, when not every one the container or stack has"`
	StopFirst   bool           `json:"stop_first,omitempty"  jsonschema:"the container or stack is stopped while it is backed up and started again after"`
	Tags        []string       `json:"tags,omitempty"`
	Options     map[string]any `json:"options,omitempty"     jsonschema:"more of how it is backed up, as Dockhand's web interface saved it"`
	LastRun     string         `json:"last_run,omitempty"`
	LastStatus  string         `json:"last_status,omitempty" jsonschema:"how its last run went: success, warning, skipped or failed"`
	Created     string         `json:"created,omitempty"`
	Changed     string         `json:"changed,omitempty"`
}

// listedText is a list Dockhand keeps as JSON written out as text, read back as the list: nothing for none, and for text that is no list.
func listedText(kept string) []string {
	var list []string
	if strings.TrimSpace(kept) == "" || json.Unmarshal([]byte(kept), &list) != nil {
		return nil
	}

	return list
}

// keepOf is what a backup keeps, read out of the JSON Dockhand holds it as: nil for one that keeps every snapshot.
func keepOf(kept string) *backupKeep {
	var policy struct {
		KeepLast, KeepDaily, KeepWeekly, KeepMonthly, KeepYearly int
	}
	if strings.TrimSpace(kept) == "" || json.Unmarshal([]byte(kept), &policy) != nil {
		return nil
	}
	keep := backupKeep{Last: policy.KeepLast, Daily: policy.KeepDaily, Weekly: policy.KeepWeekly, Monthly: policy.KeepMonthly, Yearly: policy.KeepYearly}
	if keep.none() {
		return nil
	}

	return &keep
}

// backupConfigs is every backup the Dockhand keeps.
func (r *registry) backupConfigs(ctx context.Context) ([]dockhand.GetBackupConfigsAnswer, error) {
	listed, err := r.dock.GetBackupConfigs(ctx)
	switch {
	case client.IsNotFound(err):
		return nil, errBackupsOff
	case err != nil:
		return nil, fmt.Errorf("the backups could not be listed: %w", refusal(err))
	default:
		return listed.Answer, nil
	}
}

// backupRowOf is a backup as the tools give it, with its environment and its destination by their names.
func backupRowOf(cfg *dockhand.GetBackupConfigsAnswer, envs []environment, destinations []dockhand.GetBackupDestinationsAnswer) backupRow {
	row := backupRow{ID: int(cfg.Id), Of: cfg.TargetName, Type: cfg.Type, Schedule: cfg.Schedule, Enabled: cfg.Enabled, Keep: keepOf(cfg.Retention), StopFirst: cfg.StopBeforeBackup, Tags: listedText(cfg.Tags), LastRun: when(cfg.LastBackupAt), LastStatus: cfg.LastBackupStatus, Created: when(cfg.CreatedAt), Changed: when(cfg.UpdatedAt)}
	if cfg.EnvironmentId != 0 {
		i := slices.IndexFunc(envs, func(e environment) bool { return e.ID == cfg.EnvironmentId })
		row.Env = nameOr(i, int(cfg.EnvironmentId), func(i int) string { return envs[i].Name })
	}
	d := slices.IndexFunc(destinations, func(d dockhand.GetBackupDestinationsAnswer) bool { return d.Id == cfg.DestinationId })
	row.Destination = nameOr(d, int(cfg.DestinationId), func(i int) string { return destinations[i].Name })
	if !cfg.AllVolumes {
		row.Volumes = listedText(cfg.SelectedVolumes)
	}
	if strings.TrimSpace(cfg.Options) != "" {
		_ = json.Unmarshal([]byte(cfg.Options), &row.Options)
	}

	return row
}

// backupIn is the backup of a list a call names: by its number, or by the name of what it backs up when one backup alone is of that name.
func backupIn(listed []dockhand.GetBackupConfigsAnswer, given any) (*dockhand.GetBackupConfigsAnswer, error) {
	wanted, err := named(given, "backup")
	if err != nil {
		return nil, err
	}
	if wanted == "" {
		return nil, errors.New("backup is required: the backup's number as backup_list gives them, or the name of the container or stack it is of")
	}
	var of []*dockhand.GetBackupConfigsAnswer
	for i := range listed {
		if strconv.Itoa(int(listed[i].Id)) == wanted {
			return &listed[i], nil
		}
		if listed[i].TargetName == wanted {
			of = append(of, &listed[i])
		}
	}
	switch len(of) {
	case 0:
		return nil, fmt.Errorf("no backup is numbered %s or is of a container or stack of that name: backup_list gives the ones there are", wanted)
	case 1:
		return of[0], nil
	default:
		numbers := make([]string, 0, len(of))
		for _, cfg := range of {
			numbers = append(numbers, strconv.Itoa(int(cfg.Id)))
		}

		return nil, fmt.Errorf("%d backups are of %s: give the number of one, %s", len(of), wanted, strings.Join(numbers, " or "))
	}
}

// retentionOf is what to keep as the call that makes or changes a backup takes it: nil for nothing said.
func retentionOf(keep *backupKeep) *dockhand.PutBackupConfigsByIdRequestRetention {
	if keep.none() {
		return nil
	}

	return &dockhand.PutBackupConfigsByIdRequestRetention{KeepLast: keep.Last, KeepDaily: keep.Daily, KeepWeekly: keep.Weekly, KeepMonthly: keep.Monthly, KeepYearly: keep.Yearly}
}

// backedUp is which of a container and a stack a name is on a host, with the volumes it has by the names Dockhand backs them up under, or the error that there is no such container or
// stack on the host: Dockhand saves a backup of a name nothing has, and each run of it then fails.
func (r *registry) backedUp(ctx context.Context, env environment, name, kind string) (found string, volumes []string, err error) {
	containers, err := r.containersOf(ctx, env)
	if err != nil {
		return "", nil, fmt.Errorf("the containers of %s could not be listed: %w", env.Name, err)
	}
	stacks, err := r.stacksOf(ctx, env)
	if err != nil {
		return "", nil, fmt.Errorf("the stacks of %s could not be listed: %w", env.Name, err)
	}
	isContainer := slices.ContainsFunc(containers, func(c dockhand.GetContainersAnswer) bool { return c.Name == name })
	stack := slices.IndexFunc(stacks, func(s dockhand.GetStacksAnswer) bool { return s.Name == name })

	switch {
	case kind == backupOfContainer && !isContainer:
		return "", nil, fmt.Errorf("%s has no container named %s: container_list gives the ones it has", env.Name, name)
	case kind == backupOfStack && stack < 0:
		return "", nil, fmt.Errorf("%s has no stack named %s: stack_list gives the ones it has", env.Name, name)
	case kind == "" && isContainer && stack >= 0:
		return "", nil, fmt.Errorf("%s has both a container and a stack named %s: type says which, container or stack", env.Name, name)
	case kind == "" && !isContainer && stack < 0:
		return "", nil, fmt.Errorf("%s has no container and no stack named %s", env.Name, name)
	default:
	}
	found = cmp.Or(kind, backupOfContainer)
	if kind == "" && !isContainer {
		found = backupOfStack
	}

	for i := range containers {
		mine := containers[i].Name == name
		if found == backupOfStack {
			mine = slices.ContainsFunc(stacks[stack].ContainerDetails, func(d dockhand.GetStacksAnswerContainerDetails) bool { return d.Name == containers[i].Name })
		}
		if !mine {
			continue
		}
		for _, mount := range containers[i].Mounts {
			if volume := cmp.Or(mount.Name, mount.Source); volume != "" && !slices.Contains(volumes, volume) {
				volumes = append(volumes, volume)
			}
		}
	}
	slices.Sort(volumes)

	return found, volumes, nil
}

// backupChangeOut is what making or changing a backup answers.
type backupChangeOut struct {
	Backup   *backupRow `json:"backup,omitempty"    jsonschema:"the backup as it is now, read back; absent for a dry run of making one"`
	Changes  []string   `json:"changes"             jsonschema:"what was done, in words; empty when nothing needed doing"`
	NotTaken []string   `json:"not_taken,omitempty" jsonschema:"what was asked for and Dockhand saved otherwise, each with what it saved"`
	DryRun   bool       `json:"dry_run,omitempty"   jsonschema:"nothing was done: changes says what would be"`
	Note     string     `json:"note,omitempty"`
}

// backupRead reads one backup back as it stands, into what a change answers.
func (r *registry) backupRead(ctx context.Context, out *backupChangeOut, id dockhand.BackupConfigID) *backupRow {
	note := func(err error) *backupRow {
		out.Note = noted(out.Note, "it was done, and could not be read back: "+err.Error())

		return nil
	}
	got, err := r.dock.GetBackupConfigsById(ctx, id)
	if err != nil {
		return note(refusal(err))
	}
	envs, err := r.environments(ctx)
	if err != nil {
		return note(err)
	}
	destinations, err := r.backupDestinations(ctx)
	if err != nil {
		return note(err)
	}
	row := backupRowOf(new(dockhand.GetBackupConfigsAnswer(*got.Answer)), envs, destinations)
	out.Backup = &row

	return &row
}

// notTaken is what a backup was asked to be and is not, each with what Dockhand saved in its place.
func notTaken(row *backupRow, schedule string, enabled bool, keep *backupKeep) []string {
	var not []string
	if row.Schedule != schedule {
		not = append(not, fmt.Sprintf("schedule: asked for %s, and Dockhand has %s", cmp.Or(schedule, "none"), cmp.Or(row.Schedule, "none")))
	}
	if row.Enabled != enabled {
		not = append(not, fmt.Sprintf("enabled: asked for %t, and Dockhand has %t", enabled, row.Enabled))
	}
	if row.Keep.said() != keep.said() {
		why := ""
		if keep.none() && row.Schedule != "" {
			why = ", which it writes for any backup that runs by itself and is given nothing to keep"
		}
		not = append(not, fmt.Sprintf("keep: asked for %s, and Dockhand has %s%s", keep.said(), row.Keep.said(), why))
	}

	return not
}

//nolint:gocyclo,maintidx // the backup tools registered in one function, each a handler of its own: the count is theirs added up
func registerBackupTools(r *registry) {
	type backupListIn struct {
		envArg
		backupArg

		Destination any    `json:"destination,omitempty" jsonschema:"only the backups kept in this destination, by its name or number"`
		Of          string `json:"of,omitempty"          jsonschema:"only the backups of the container or stack of this name"`
	}
	type backupListOut struct {
		Backups []backupRow `json:"backups"`
		Total   int         `json:"total"`
		Note    string      `json:"note,omitempty"`
	}
	add(r, readTool, &mcp.Tool{
		Name:        "backup_list",
		Description: "List the backups the Dockhand keeps, or one: each a container or a stack whose volumes are copied to a backup destination, with the Docker host it is on, when it runs by itself, how many snapshots it keeps, and when its last run was and how it went. Narrow it by env, by destination or by the name of what is backed up. A backup with no schedule runs only when backup_run asks. Dockhand's backups are a beta that is off unless it was started with them on, which this says when it is so.",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in backupListIn) (*mcp.CallToolResult, backupListOut, error) {
		listed, err := r.backupConfigs(ctx)
		if err != nil {
			return nil, backupListOut{}, err
		}
		if in.Backup != nil {
			one, unknown := backupIn(listed, in.Backup)
			if unknown != nil {
				return nil, backupListOut{}, unknown
			}
			listed = []dockhand.GetBackupConfigsAnswer{*one}
		}
		out := backupListOut{Backups: make([]backupRow, 0, len(listed))}
		destinations, err := r.backupDestinations(ctx)
		if err != nil {
			return nil, out, err
		}
		envs, err := r.environments(ctx)
		if err != nil {
			return nil, out, err
		}
		only, err := in.named()
		if err != nil {
			return nil, out, err
		}
		if only != "" {
			picked, unknown := r.pick(ctx, in.envArg)
			if unknown != nil {
				return nil, out, unknown
			}
			only = picked[0].Name
		}
		kept := ""
		if in.Destination != nil {
			destination, unknown := backupDestinationIn(destinations, in.Destination)
			if unknown != nil {
				return nil, out, unknown
			}
			kept = destination.Name
		}
		of := strings.TrimSpace(in.Of)
		for i := range listed {
			row := backupRowOf(&listed[i], envs, destinations)
			if (only != "" && row.Env != only) || (kept != "" && row.Destination != kept) || (of != "" && row.Of != of) {
				continue
			}
			out.Backups = append(out.Backups, row)
		}
		slices.SortFunc(out.Backups, func(a, b backupRow) int {
			return cmp.Or(cmp.Compare(a.Env, b.Env), cmp.Compare(a.Of, b.Of), cmp.Compare(a.ID, b.ID))
		})
		out.Total = len(out.Backups)
		switch {
		case len(listed) == 0 && len(destinations) == 0:
			out.Note = "nothing is backed up, and there is nowhere to back up to: backup_destination_create saves a destination, and backup_create a backup to it"
		case len(listed) == 0:
			out.Note = "nothing is backed up: backup_create makes a backup of a container or a stack"
		case out.Total == 0:
			out.Note = "no backup is as asked for; the Dockhand keeps " + several(len(listed), "backup")
		default:
		}

		return nil, out, nil
	})

	type backupCreateIn struct {
		envArg
		dryRunArg

		Of          string      `json:"of"                   jsonschema:"the container or stack to back up, by its name on that host"`
		Type        string      `json:"type,omitempty"       jsonschema:"container or stack; left out, whichever of that name the host has"`
		Destination any         `json:"destination"          jsonschema:"where to keep its snapshots: a backup destination, by its name or number as backup_destination_list gives them"`
		Volumes     []string    `json:"volumes,omitempty"    jsonschema:"back up only these of its volumes, each by its name, or for a folder of the host mounted into it by that folder's path; left out, every one it has"`
		StopFirst   bool        `json:"stop_first,omitempty" jsonschema:"stop the container or stack while it is backed up and start it again after, for data that is only whole at rest, as a database's files"`
		Schedule    string      `json:"schedule,omitempty"   jsonschema:"when to run by itself, as cron: 0 3 * * * is every night at three. Left out, it runs only when backup_run asks"`
		Keep        *backupKeep `json:"keep,omitempty"       jsonschema:"how many snapshots to keep, older ones being removed after each run. Left out, every snapshot is kept, but for a backup that runs by itself, which Dockhand then gives its own: 7 daily, 4 weekly and 6 monthly"`
		Disabled    bool        `json:"disabled,omitempty"   jsonschema:"save it with its schedule switched off"`
		Tags        []string    `json:"tags,omitempty"       jsonschema:"words of your own to mark its snapshots with"`
	}
	add(r, writeTool, &mcp.Tool{
		Name:        "backup_create",
		Description: "Have the Dockhand back up one container or one stack on a Docker host (environment) to a backup destination: the data in its volumes and, for a stack, its compose file and what lies beside it, so that backup_restore can put it back. It is checked first, since Dockhand saves a backup of a name nothing has, or of a volume there is none of, and every run of it then fails or passes the volume over without a word: the container or stack must be there, and each volume named must be one it has. Nothing is backed up until its schedule comes round or backup_run asks. A stack needs its folder on the Docker host, which is said when it cannot be found there.",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in backupCreateIn) (*mcp.CallToolResult, backupChangeOut, error) {
		name, kind, schedule := strings.TrimSpace(in.Of), strings.TrimSpace(in.Type), strings.TrimSpace(in.Schedule)
		switch {
		case name == "":
			return nil, backupChangeOut{}, errors.New("of is required: the container or stack to back up, by its name")
		case kind != "" && kind != backupOfContainer && kind != backupOfStack:
			return nil, backupChangeOut{}, fmt.Errorf("type is %q: it is container or stack", in.Type)
		default:
		}
		listed, err := r.backupConfigs(ctx)
		if err != nil {
			return nil, backupChangeOut{}, err
		}
		env, err := r.one(ctx, in.envArg)
		if err != nil {
			return nil, backupChangeOut{}, err
		}
		destinations, err := r.backupDestinations(ctx)
		if err != nil {
			return nil, backupChangeOut{}, err
		}
		destination, err := backupDestinationIn(destinations, in.Destination)
		if err != nil {
			return nil, backupChangeOut{}, err
		}
		kind, has, err := r.backedUp(ctx, env, name, kind)
		if err != nil {
			return nil, backupChangeOut{}, err
		}
		for _, volume := range in.Volumes {
			if !slices.Contains(has, volume) {
				return nil, backupChangeOut{}, fmt.Errorf("the %s %s has no volume %s, and Dockhand would save it and pass it over at every run: it has %s", kind, name, volume, cmp.Or(strings.Join(has, ", "), "none"))
			}
		}
		same := func(c dockhand.GetBackupConfigsAnswer) bool {
			return c.TargetName == name && c.Type == kind && c.EnvironmentId == env.ID && c.DestinationId == destination.Id
		}
		if i := slices.IndexFunc(listed, same); i >= 0 {
			return nil, backupChangeOut{}, fmt.Errorf("the %s %s on %s is already backed up to %s, as backup %d: backup_edit changes it", kind, name, env.Name, destination.Name, listed[i].Id)
		}
		out := backupChangeOut{Changes: []string{}, DryRun: in.DryRun}
		if kind == backupOfStack {
			folder, unfound := r.dock.GetBackupStackDirListing(ctx, dockhand.GetBackupStackDirListingOptions{Target: name, Env: env.ID})
			if unfound != nil {
				return nil, out, fmt.Errorf("the folder of the stack %s could not be looked for on %s: %w", name, env.Name, refusal(unfound))
			}
			if folder.Answer.Kind == "unknown" || folder.Answer.Kind == "helper-failed" {
				return nil, out, fmt.Errorf("the files of the stack %s could not be found on %s, and a backup of it would fail at every run: %s", name, env.Name, cmp.Or(folder.Answer.Reason, "Dockhand gives no reason"))
			}
		}

		what := fmt.Sprintf("the %s %s on %s to %s", kind, name, env.Name, destination.Name)
		if in.DryRun {
			out.Changes = append(out.Changes, "would back up "+what+", "+backupSaid(schedule, !in.Disabled, in.Keep))

			return nil, out, nil
		}

		request := dockhand.PostBackupConfigsRequest{DestinationId: destination.Id, TargetName: name, Type: kind, EnvironmentId: env.ID, Enabled: new(!in.Disabled), AllVolumes: new(len(in.Volumes) == 0), SelectedVolumes: in.Volumes, StopBeforeBackup: new(in.StopFirst), Schedule: schedule, Tags: in.Tags}
		if keep := retentionOf(in.Keep); keep != nil {
			request.Retention = new(dockhand.PostBackupConfigsRequestRetention(*keep))
		}
		made, err := r.dock.PostBackupConfigs(ctx, request)
		if err != nil {
			return nil, out, fmt.Errorf("the backup of %s was not saved: %w", what, refusal(err))
		}
		out.Changes = append(out.Changes, "saved a backup of "+what+", "+backupSaid(schedule, !in.Disabled, in.Keep))
		if row := r.backupRead(ctx, &out, made.Answer.Id); row != nil {
			out.NotTaken = notTaken(row, schedule, !in.Disabled, in.Keep)
		}
		if schedule == "" {
			out.Note = noted(out.Note, "nothing is backed up until backup_run asks: it has no schedule")
		}

		return nil, out, nil
	})

	type backupEditIn struct {
		backupArg
		dryRunArg

		Destination any         `json:"destination,omitempty" jsonschema:"keep its snapshots in this destination from now on, by its name or number. The ones already taken stay where they are"`
		Volumes     *[]string   `json:"volumes,omitempty"     jsonschema:"back up only these of its volumes; an empty list for every one it has"`
		StopFirst   *bool       `json:"stop_first,omitempty"  jsonschema:"whether the container or stack is stopped while it is backed up"`
		Schedule    *string     `json:"schedule,omitempty"    jsonschema:"when to run by itself, as cron; empty to run only when backup_run asks"`
		Keep        *backupKeep `json:"keep,omitempty"        jsonschema:"how many snapshots to keep; with every number 0 or left out, every snapshot, which Dockhand takes only for a backup with no schedule"`
		Enabled     *bool       `json:"enabled,omitempty"     jsonschema:"switch its schedule on or off"`
		Tags        *[]string   `json:"tags,omitempty"        jsonschema:"the words its snapshots are marked with, in place of the ones it has"`
	}
	add(r, writeTool, &mcp.Tool{
		Name:        "backup_edit",
		Description: "Change a backup the Dockhand keeps: where its snapshots go, which volumes are taken, whether it is stopped first, when it runs by itself, how many snapshots it keeps, and whether its schedule is on. What is not named stays as it was, and the answer reads the backup back. Dockhand changes more than it is asked to here, so the whole of it is sent each time and not_taken says what Dockhand saved otherwise: given a schedule it writes its own keep where there is none, " + ownPolicy + ", and switches on a backup that had no schedule. What it backs up, and on which host, is fixed when it is made.",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in backupEditIn) (*mcp.CallToolResult, backupChangeOut, error) {
		listed, err := r.backupConfigs(ctx)
		if err != nil {
			return nil, backupChangeOut{}, err
		}
		cfg, err := backupIn(listed, in.Backup)
		if err != nil {
			return nil, backupChangeOut{}, err
		}
		envs, err := r.environments(ctx)
		if err != nil {
			return nil, backupChangeOut{}, err
		}
		destinations, err := r.backupDestinations(ctx)
		if err != nil {
			return nil, backupChangeOut{}, err
		}
		was := backupRowOf(cfg, envs, destinations)
		out := backupChangeOut{Changes: []string{}, DryRun: in.DryRun, Backup: &was}
		done := "set"
		if in.DryRun {
			done = "would set"
		}

		// what it is to be: what it was, with what was named put over it
		request := dockhand.PutBackupConfigsByIdRequest{Enabled: new(cfg.Enabled), AllVolumes: new(cfg.AllVolumes), SelectedVolumes: listedText(cfg.SelectedVolumes), StopBeforeBackup: new(cfg.StopBeforeBackup)}
		schedule, enabled, keep := was.Schedule, was.Enabled, was.Keep
		if in.Destination != nil {
			destination, unknown := backupDestinationIn(destinations, in.Destination)
			if unknown != nil {
				return nil, out, unknown
			}
			if destination.Id != cfg.DestinationId {
				request.DestinationId = destination.Id
				out.Changes = append(out.Changes, fmt.Sprintf("%s its destination to %s, in place of %s", done, destination.Name, was.Destination))
			}
		}
		if in.Volumes != nil && !slices.Equal(*in.Volumes, was.Volumes) {
			if len(*in.Volumes) > 0 && cfg.EnvironmentId != 0 {
				i := slices.IndexFunc(envs, func(e environment) bool { return e.ID == cfg.EnvironmentId })
				if i < 0 {
					return nil, out, fmt.Errorf("the backup %d is of an environment that is gone, so its volumes cannot be checked", cfg.Id)
				}
				_, has, err := r.backedUp(ctx, envs[i], cfg.TargetName, cfg.Type)
				if err != nil {
					return nil, out, err
				}
				for _, volume := range *in.Volumes {
					if !slices.Contains(has, volume) {
						return nil, out, fmt.Errorf("the %s %s has no volume %s, and Dockhand would save it and pass it over at every run: it has %s", cfg.Type, cfg.TargetName, volume, cmp.Or(strings.Join(has, ", "), "none"))
					}
				}
			}
			request.AllVolumes, request.SelectedVolumes = new(len(*in.Volumes) == 0), append([]string{}, *in.Volumes...)
			out.Changes = append(out.Changes, fmt.Sprintf("%s the volumes it takes to %s", done, cmp.Or(strings.Join(*in.Volumes, ", "), "every one it has")))
		}
		if in.StopFirst != nil && *in.StopFirst != cfg.StopBeforeBackup {
			request.StopBeforeBackup = in.StopFirst
			out.Changes = append(out.Changes, fmt.Sprintf("%s stop_first to %t", done, *in.StopFirst))
		}
		if in.Tags != nil && !slices.Equal(*in.Tags, was.Tags) {
			request.Tags = append([]string{}, *in.Tags...)
			out.Changes = append(out.Changes, fmt.Sprintf("%s its tags to %s", done, cmp.Or(strings.Join(*in.Tags, ", "), "none")))
		}
		if in.Schedule != nil && strings.TrimSpace(*in.Schedule) != schedule {
			schedule = strings.TrimSpace(*in.Schedule)
			out.Changes = append(out.Changes, fmt.Sprintf("%s its schedule to %s", done, cmp.Or(schedule, "none: it runs only when asked")))
		}
		if in.Enabled != nil && *in.Enabled != enabled {
			enabled = *in.Enabled
			out.Changes = append(out.Changes, fmt.Sprintf("%s enabled to %t", done, enabled))
		}
		if in.Keep != nil && in.Keep.said() != keep.said() {
			keep = in.Keep
			out.Changes = append(out.Changes, fmt.Sprintf("%s what it keeps to %s", done, keep.said()))
		}
		if len(out.Changes) == 0 {
			out.Note = "it is already as asked for, so nothing was changed"

			return nil, out, nil
		}
		if in.DryRun {
			return nil, out, nil
		}

		// the whole of it goes each time: a schedule sent with nothing to keep has Dockhand write its own, and one sent to a backup that had none
		// switches it on whatever is said, which the second sending puts right
		request.Enabled, request.Schedule, request.Retention = new(enabled), schedule, retentionOf(keep)
		for range 2 {
			if _, err := r.dock.PutBackupConfigsById(ctx, cfg.Id, request); err != nil {
				return nil, out, fmt.Errorf("the backup %d was not changed: %w", cfg.Id, refusal(err))
			}
			if schedule == "" && (was.Schedule != "" || (keep.none() && !was.Keep.none())) {
				if _, err := r.dock.ForgetBackupConfigSchedule(ctx, cfg.Id, retentionOf(keep)); err != nil {
					return nil, out, fmt.Errorf("the backup %d was changed, and its schedule could not be cleared: %w", cfg.Id, refusal(err))
				}
			}
			row := r.backupRead(ctx, &out, cfg.Id)
			if row == nil {
				return nil, out, nil
			}
			if out.NotTaken = notTaken(row, schedule, enabled, keep); len(out.NotTaken) == 0 || row.Enabled == enabled {
				break
			}
		}

		return nil, out, nil
	})

	type backupRunIn struct {
		backupArg
		waitArg
	}
	type backupRunOut struct {
		jobState

		Backup      int    `json:"backup"`
		Of          string `json:"of"`
		Env         string `json:"env,omitempty"`
		Destination string `json:"destination"`
		Status      string `json:"status,omitempty"      jsonschema:"how it went, once it has ended: success, warning, or skipped for one that made no snapshot, as one that was stopped"`
		Snapshot    string `json:"snapshot,omitempty"    jsonschema:"the snapshot it made, by its id: what backup_restore puts back"`
		Files       int    `json:"files,omitempty"       jsonschema:"how many files the snapshot holds"`
		Changed     int    `json:"changed,omitempty"     jsonschema:"how many of them were new or changed since the snapshot before"`
		AddedBytes  int    `json:"added_bytes,omitempty" jsonschema:"how much the destination grew by, which is only what changed"`
		Pruned      bool   `json:"pruned,omitempty"      jsonschema:"older snapshots were looked through and the ones it no longer keeps removed"`
		Run         int    `json:"run,omitempty"         jsonschema:"the number of this run among the runs Dockhand keeps of what it does by itself, where what it logged is kept"`
		Note        string `json:"note,omitempty"`
	}
	add(r, writeTool, &mcp.Tool{
		Name:        "backup_run",
		Description: "Run a backup the Dockhand keeps now, whenever its schedule would: a snapshot of the container's or the stack's volumes is made in its destination, and older snapshots it no longer keeps are removed. Dockhand runs it in a helper container on the Docker host, which pulls the helper's image the first time, so the destination has to be one that host can reach. It is waited for as long as wait says and then answered as a job for job_get to follow. One that failed is an error that says why. A backup marked stop_first stops what it backs up for as long as it takes.",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in backupRunIn) (*mcp.CallToolResult, backupRunOut, error) {
		patience, err := in.patience()
		if err != nil {
			return nil, backupRunOut{}, err
		}
		listed, err := r.backupConfigs(ctx)
		if err != nil {
			return nil, backupRunOut{}, err
		}
		cfg, err := backupIn(listed, in.Backup)
		if err != nil {
			return nil, backupRunOut{}, err
		}
		envs, err := r.environments(ctx)
		if err != nil {
			return nil, backupRunOut{}, err
		}
		destinations, err := r.backupDestinations(ctx)
		if err != nil {
			return nil, backupRunOut{}, err
		}
		row := backupRowOf(cfg, envs, destinations)
		out := backupRunOut{Backup: row.ID, Of: row.Of, Env: row.Env, Destination: row.Destination}
		if cfg.EnvironmentId == 0 {
			return nil, out, fmt.Errorf("the backup %d was saved with no environment, so there is no host to run it on: backup_delete removes it, and backup_create makes one that names its host", cfg.Id)
		}

		started, err := r.dock.PostBackupConfigsByIdRun(ctx, cfg.Id, dockhand.PostBackupConfigsByIdRunOptions{Accept: dockhand.AcceptJob})
		if err != nil {
			return nil, out, fmt.Errorf("the backup of %s could not be started: %w", row.Of, refusal(err))
		}
		outcome, state, err := settle(ctx, r, started.Answer.JobId, started.Answer, patience)
		out.jobState = state
		if err != nil {
			return nil, out, fmt.Errorf("the backup of %s on %s failed: %w", row.Of, row.Env, err)
		}
		if state.Running {
			out.Note = "still running after " + seconds(patience) + ": job_get says how it ends, and backup_stop stops it"

			return nil, out, nil
		}
		if outcome.Status == jobFailed || outcome.Error != "" {
			return nil, out, fmt.Errorf("the backup of %s on %s failed: %s", row.Of, row.Env, cmp.Or(plainly(outcome.Error), "Dockhand gave no reason"))
		}
		out.Status, out.Snapshot, out.Run, out.Pruned = outcome.Status, shortSnapshot(outcome.SnapshotId), outcome.ExecutionId, outcome.Retention == "applied"
		if outcome.Reason != "" {
			out.Note = "no snapshot was made: " + outcome.Reason
		}
		if sum := outcome.Summary; sum != nil {
			out.Files, out.Changed, out.AddedBytes = sum.TotalFilesProcessed, sum.FilesNew+sum.FilesChanged, sum.DataAdded
		}

		return nil, out, nil
	})

	type backupStopIn struct {
		backupArg

		Restore  bool   `json:"restore,omitempty"  jsonschema:"stop a restore that is running, in place of a backup: every one, or with snapshot the one of that snapshot"`
		Snapshot string `json:"snapshot,omitempty" jsonschema:"with restore: the snapshot whose restore is to be stopped"`
	}
	type backupStopOut struct {
		Stopped bool   `json:"stopped"        jsonschema:"whether anything was running to stop"`
		Note    string `json:"note,omitempty"`
	}
	add(r, writeTool, &mcp.Tool{
		Name:        "backup_stop",
		Description: "Stop a backup that is running, by the backup it is a run of, or with restore a restore that is running. Dockhand ends the helper container that does the work: a backup that is stopped leaves no snapshot and is recorded as failed, and a restore that is stopped leaves what it was writing part written. Nothing running is no error: the answer says nothing was stopped.",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in backupStopIn) (*mcp.CallToolResult, backupStopOut, error) {
		snapshot := strings.TrimSpace(in.Snapshot)
		if in.Restore {
			if in.Backup != nil {
				return nil, backupStopOut{}, errors.New("backup and restore are two things to stop: give one")
			}
			stopped, err := r.dock.PostBackupRestoreStop(ctx, dockhand.PostBackupRestoreStopRequest{SnapshotId: snapshot})
			switch {
			case client.IsNotFound(err):
				return nil, backupStopOut{}, errBackupsOff
			case err != nil:
				return nil, backupStopOut{}, fmt.Errorf("the restore could not be stopped: %w", refusal(err))
			default:
			}
			out := backupStopOut{Stopped: stopped.Answer.Stopped}
			if !out.Stopped {
				out.Note = "no restore was running, so there was nothing to stop"
			}

			return nil, out, nil
		}
		if snapshot != "" {
			return nil, backupStopOut{}, errors.New("snapshot is for stopping a restore: give restore with it")
		}
		listed, err := r.backupConfigs(ctx)
		if err != nil {
			return nil, backupStopOut{}, err
		}
		cfg, err := backupIn(listed, in.Backup)
		if err != nil {
			return nil, backupStopOut{}, err
		}
		stopped, err := r.dock.PostBackupConfigsByIdStop(ctx, cfg.Id)
		if err != nil {
			return nil, backupStopOut{}, fmt.Errorf("the backup of %s could not be stopped: %w", cfg.TargetName, refusal(err))
		}
		out := backupStopOut{Stopped: stopped.Answer.Stopped}
		if !out.Stopped {
			out.Note = "the backup of " + cfg.TargetName + " was not running, so there was nothing to stop"
		}

		return nil, out, nil
	})

	type backupDeleteIn struct {
		backupArg

		Confirm       bool `json:"confirm,omitempty"        jsonschema:"remove it: without this the answer says what would go and nothing is removed"`
		WithSnapshots bool `json:"with_snapshots,omitempty" jsonschema:"remove every snapshot it took as well. Left out, they stay in the destination and can still be restored"`
	}
	type backupDeleteOut struct {
		Backup    int      `json:"backup"`
		Of        string   `json:"of"`
		WouldGo   []string `json:"would_go,omitempty"  jsonschema:"what removing it takes away; absent once it is removed"`
		Removed   bool     `json:"removed"`
		Snapshots int      `json:"snapshots,omitempty" jsonschema:"how many of its snapshots were removed with it"`
		Note      string   `json:"note,omitempty"`
	}
	add(r, deleteTool, &mcp.Tool{
		Name:        "backup_delete",
		Description: "Remove a backup the Dockhand keeps: it is no longer run, by its schedule or when asked. The snapshots it took stay in the destination, where backup_snapshot_list still finds them and backup_restore can put one back, unless with_snapshots removes them too, which cannot be undone. Without confirm the answer says what would go and removes nothing. One that is running is refused until it has ended or backup_stop has stopped it.",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in backupDeleteIn) (*mcp.CallToolResult, backupDeleteOut, error) {
		listed, err := r.backupConfigs(ctx)
		if err != nil {
			return nil, backupDeleteOut{}, err
		}
		cfg, err := backupIn(listed, in.Backup)
		if err != nil {
			return nil, backupDeleteOut{}, err
		}
		out := backupDeleteOut{Backup: int(cfg.Id), Of: cfg.TargetName}
		out.WouldGo = append(out.WouldGo, fmt.Sprintf("the backup of the %s %s, with its schedule", cfg.Type, cfg.TargetName))
		if in.WithSnapshots {
			taken, unlisted := r.dock.GetBackupSnapshots(ctx, dockhand.GetBackupSnapshotsOptions{ConfigId: cfg.Id})
			if unlisted != nil {
				return nil, out, fmt.Errorf("the snapshots of the backup %d could not be listed, so nothing was removed: %w", cfg.Id, refusal(unlisted))
			}
			out.WouldGo = append(out.WouldGo, several(len(taken.Answer), "snapshot")+" it took, which cannot be had back")
		}
		if !in.Confirm {
			out.Note = "nothing was removed: confirm removes it"

			return nil, out, nil
		}

		removed, err := r.dock.DeleteBackupConfigsById(ctx, cfg.Id, dockhand.DeleteBackupConfigsByIdOptions{DeleteSnapshots: new(in.WithSnapshots)})
		if err != nil {
			return nil, out, fmt.Errorf("the backup %d was not removed: %w", cfg.Id, refusal(err))
		}
		after, err := r.backupConfigs(ctx)
		switch {
		case err != nil:
			out.Note = "it was removed, and the backups could not be listed afterwards: " + err.Error()
		case slices.ContainsFunc(after, func(c dockhand.GetBackupConfigsAnswer) bool { return c.Id == cfg.Id }):
			return nil, out, fmt.Errorf("Dockhand answered that the backup %d was removed, and still lists it", cfg.Id) //nolint:staticcheck // ST1005: the sentence begins with the server's name
		default:
		}
		out.Removed, out.WouldGo = true, nil
		if kept := removed.Answer.Snapshots; kept != nil {
			out.Snapshots = kept.Deleted
			if kept.Error != "" || kept.Skipped > 0 {
				out.Note = noted(out.Note, fmt.Sprintf("%s could not be removed and are still in the destination: %s", several(kept.Skipped, "snapshot"), cmp.Or(kept.Error, "Dockhand gives no reason")))
			}
		}
		if !in.WithSnapshots {
			out.Note = noted(out.Note, "the snapshots it took are still in the destination: backup_snapshot_list finds them")
		}

		return nil, out, nil
	})
}

// backupSaid is when a backup runs and what it keeps, in words.
func backupSaid(schedule string, enabled bool, keep *backupKeep) string {
	when := "run only when asked"
	switch {
	case schedule != "" && enabled:
		when = "run at " + schedule
	case schedule != "":
		when = "with the schedule " + schedule + " switched off"
	default:
	}

	return when + ", keeping " + keep.said()
}

// shortSnapshot is a snapshot's id as restic shortens it, which every call takes in place of the whole.
func shortSnapshot(id string) string {
	if len(id) > 8 {
		return id[:8]
	}

	return id
}
