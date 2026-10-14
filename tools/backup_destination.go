package tools

import (
	"bytes"
	"cmp"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/katbyte/dockhand-mcp/sdk/dockhand"
	"github.com/katbyte/pandorest/client"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// How Dockhand says the last try of a destination went.
const destinationUsable = "success"

// The things Dockhand has restic do to a destination, by whether they only look.
var (
	backupChecks = []string{"stats", "check", "verify"}
	backupTasks  = []string{"unlock", "prune", "repair-index", "repair-snapshots", "init"}
)

// unreachableOrNew is what a try that finds no repository means, which Dockhand says the same way for two different things.
const unreachableOrNew = "nothing answered as a backup repository there: either none has been made at that address yet, or the address cannot be reached, which Dockhand does not tell apart"

// backupDestinationArg names one place the Dockhand keeps backups.
type backupDestinationArg struct {
	Destination any `json:"destination,omitempty" jsonschema:"the backup destination, by its name or its number as backup_destination_list gives them"`
}

// backupUpkeep is how Dockhand looks after a destination by itself.
type backupUpkeep struct {
	Prune          *bool   `json:"prune,omitempty"           jsonschema:"whether data no snapshot uses any more is cleared out on a schedule"`
	PruneSchedule  *string `json:"prune_schedule,omitempty"  jsonschema:"when, as cron"`
	Check          *bool   `json:"check,omitempty"           jsonschema:"whether the repository is checked for damage on a schedule"`
	CheckSchedule  *string `json:"check_schedule,omitempty"  jsonschema:"when, as cron"`
	Verify         *bool   `json:"verify,omitempty"          jsonschema:"whether a part of the data itself is read back and checked on a schedule, which a check alone does not do"`
	VerifySchedule *string `json:"verify_schedule,omitempty" jsonschema:"when, as cron"`
	VerifySubset   *string `json:"verify_subset,omitempty"   jsonschema:"how much of the data each verify reads, as 5% or 1G"`
	AutoUnlock     *bool   `json:"auto_unlock,omitempty"     jsonschema:"whether a lock left behind by a run that was cut short is cleared before the next"`
}

// byName is what is said of a destination's upkeep, by the names Dockhand keeps each setting under: only what was said.
func (u *backupUpkeep) byName() map[string]any {
	named := map[string]any{}
	if u == nil {
		return named
	}
	for name, flag := range map[string]*bool{"pruneEnabled": u.Prune, "checkEnabled": u.Check, "verifyEnabled": u.Verify, "autoUnlock": u.AutoUnlock} {
		if flag != nil {
			named[name] = *flag
		}
	}
	for name, text := range map[string]*string{"pruneSchedule": u.PruneSchedule, "checkSchedule": u.CheckSchedule, "verifySchedule": u.VerifySchedule, "verifyDataSubset": u.VerifySubset} {
		if text != nil {
			named[name] = strings.TrimSpace(*text)
		}
	}

	return named
}

// upkeepOf is a destination's upkeep, read out of the JSON Dockhand holds it as: nil for text that is none.
func upkeepOf(policies string) *backupUpkeep {
	var kept struct {
		PruneEnabled, CheckEnabled, VerifyEnabled, AutoUnlock          *bool
		PruneSchedule, CheckSchedule, VerifySchedule, VerifyDataSubset *string
	}
	if strings.TrimSpace(policies) == "" || json.Unmarshal([]byte(policies), &kept) != nil {
		return nil
	}

	return &backupUpkeep{Prune: kept.PruneEnabled, PruneSchedule: kept.PruneSchedule, Check: kept.CheckEnabled, CheckSchedule: kept.CheckSchedule, Verify: kept.VerifyEnabled, VerifySchedule: kept.VerifySchedule, VerifySubset: kept.VerifyDataSubset, AutoUnlock: kept.AutoUnlock}
}

// backupDestinationRow is a place backups are kept, as the tools give it: never with its password, which Dockhand does not give back.
type backupDestinationRow struct {
	ID           int               `json:"id"`
	Name         string            `json:"name"`
	Repository   string            `json:"repository"              jsonschema:"where the backups are kept, as restic names it: a folder's path on the Docker host, or an address that opens with the kind of store it is, as rest: or s3:. A password written into the address is hidden"`
	Variables    map[string]string `json:"variables,omitempty"     jsonschema:"what the store is signed in to with, by the names restic reads them under, as AWS_ACCESS_KEY_ID. A value whose name says it is a secret is hidden"`
	BackupFlags  string            `json:"backup_flags,omitempty"  jsonschema:"more that restic is given for a backup to it"`
	RestoreFlags string            `json:"restore_flags,omitempty" jsonschema:"more that restic is given for a restore from it"`
	HostPath     string            `json:"host_path,omitempty"`
	Upkeep       *backupUpkeep     `json:"upkeep,omitempty"        jsonschema:"how Dockhand looks after it by itself, each a schedule of its own among what Dockhand does unasked: repo-prune, repo-check and repo-verify"`
	Usable       bool              `json:"usable"                  jsonschema:"whether Dockhand could open it the last time it tried"`
	Tried        string            `json:"tried,omitempty"         jsonschema:"when Dockhand last tried it"`
	Error        string            `json:"error,omitempty"         jsonschema:"why it could not be opened then"`
	Backups      []string          `json:"backups,omitempty"       jsonschema:"the backups whose snapshots are kept in it, each as its number and what it is of"`
	Created      string            `json:"created,omitempty"`
	Changed      string            `json:"changed,omitempty"`
}

// backupDestinations is every place the Dockhand keeps backups.
func (r *registry) backupDestinations(ctx context.Context) ([]dockhand.GetBackupDestinationsAnswer, error) {
	listed, err := r.dock.GetBackupDestinations(ctx)
	switch {
	case client.IsNotFound(err):
		return nil, errBackupsOff
	case err != nil:
		return nil, fmt.Errorf("the backup destinations could not be listed: %w", refusal(err))
	default:
		return listed.Answer, nil
	}
}

// backupDestinationIn is the destination of a list a call names, by its number or by its name, letter for letter.
func backupDestinationIn(listed []dockhand.GetBackupDestinationsAnswer, given any) (*dockhand.GetBackupDestinationsAnswer, error) {
	wanted, err := named(given, "destination")
	if err != nil {
		return nil, err
	}
	if wanted == "" {
		return nil, errors.New("destination is required: the backup destination's name or number, as backup_destination_list gives them")
	}
	names := make([]string, 0, len(listed))
	for i := range listed {
		if strconv.Itoa(int(listed[i].Id)) == wanted || listed[i].Name == wanted {
			return &listed[i], nil
		}
		names = append(names, listed[i].Name)
	}

	return nil, fmt.Errorf("no backup destination is named or numbered %q: the Dockhand keeps %s", wanted, cmp.Or(strings.Join(names, ", "), "none"))
}

// usesOf is the backups kept in a destination, each as its number and what it is of: 3 (web, container).
func usesOf(configs []dockhand.GetBackupConfigsAnswer, id dockhand.BackupDestinationID) []string {
	var uses []string
	for i := range configs {
		if configs[i].DestinationId == id {
			uses = append(uses, fmt.Sprintf("%d (%s, %s)", configs[i].Id, configs[i].TargetName, configs[i].Type))
		}
	}

	return uses
}

// backupDestinationRowOf reads one destination in full, with what it is signed in to hidden unless asked for and the backups that are kept in it.
func (r *registry) backupDestinationRowOf(ctx context.Context, id dockhand.BackupDestinationID, configs []dockhand.GetBackupConfigsAnswer, reveal bool) (backupDestinationRow, int, error) {
	got, err := r.dock.GetBackupDestinationsById(ctx, id)
	if err != nil {
		return backupDestinationRow{}, 0, refusal(err)
	}
	d := got.Answer
	row := backupDestinationRow{ID: int(d.Id), Name: d.Name, Repository: d.Repository, BackupFlags: d.BackupFlags, RestoreFlags: d.RestoreFlags, HostPath: d.HostPath, Upkeep: upkeepOf(d.Policies), Usable: d.LastTestStatus == destinationUsable, Tried: when(d.LastTestAt), Error: d.LastTestError, Backups: usesOf(configs, d.Id), Created: when(d.CreatedAt), Changed: when(d.UpdatedAt)}
	hidden := 0
	if !reveal {
		row.Repository, hidden = maskText(row.Repository)
	}
	variables, more := maskedLabels(d.EnvVars, reveal)
	if len(variables) > 0 {
		row.Variables = variables
	}

	return row, hidden + more, nil
}

// backupDestinationOut is what making or changing a destination answers.
type backupDestinationOut struct {
	Destination *backupDestinationRow `json:"destination,omitempty" jsonschema:"the destination as it is now, read back; absent for a dry run of making one"`
	Changes     []string              `json:"changes"               jsonschema:"what was done, in words; empty when nothing needed doing"`
	DryRun      bool                  `json:"dry_run,omitempty"     jsonschema:"nothing was done: changes says what would be"`
	Note        string                `json:"note,omitempty"`
}

// destinationRead reads one destination back as it stands, into what a change answers.
func (r *registry) destinationRead(ctx context.Context, out *backupDestinationOut, id dockhand.BackupDestinationID) *backupDestinationRow {
	configs, err := r.backupConfigs(ctx)
	if err == nil {
		var row backupDestinationRow
		if row, _, err = r.backupDestinationRowOf(ctx, id, configs, false); err == nil {
			out.Destination = &row

			return &row
		}
	}
	out.Note = noted(out.Note, "it was done, and could not be read back: "+err.Error())

	return nil
}

// storeVariables is what a store is signed in to with, as the calls that send it take it.
func storeVariables(given map[string]string) map[string]any {
	if given == nil {
		return nil
	}
	sent := make(map[string]any, len(given))
	for name, value := range given {
		sent[name] = value
	}

	return sent
}

// lastWords is the last few lines of what restic printed, which is where it says how a task came out.
func lastWords(printed string) []string {
	return lastLines(strings.TrimSpace(printed), progressKept)
}

//nolint:gocyclo,maintidx // the destination tools registered in one function, each a handler of its own: the count is theirs added up
func registerBackupDestinationTools(r *registry) {
	type destinationListIn struct {
		backupDestinationArg
		revealArg
	}
	type destinationListOut struct {
		Destinations []backupDestinationRow `json:"destinations"`
		Total        int                    `json:"total"`
		Hidden       int                    `json:"hidden,omitempty" jsonschema:"how many values are hidden for being a secret"`
		Note         string                 `json:"note,omitempty"`
	}
	add(r, readTool, &mcp.Tool{
		Name:        "backup_destination_list",
		Description: "List the places the Dockhand keeps backups, or one: each a restic repository, a folder or a store such as S3, with whether Dockhand could open it the last time it tried, how it is looked after by itself, and the backups whose snapshots are kept in it. Its password is never given: Dockhand keeps it and does not give it back. What it is signed in to with is hidden unless reveal_secrets asks. Dockhand's backups are a beta that is off unless it was started with them on, which this says when it is so.",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in destinationListIn) (*mcp.CallToolResult, destinationListOut, error) {
		listed, err := r.backupDestinations(ctx)
		if err != nil {
			return nil, destinationListOut{}, err
		}
		if in.Destination != nil {
			one, unknown := backupDestinationIn(listed, in.Destination)
			if unknown != nil {
				return nil, destinationListOut{}, unknown
			}
			listed = []dockhand.GetBackupDestinationsAnswer{*one}
		}
		out := destinationListOut{Destinations: make([]backupDestinationRow, 0, len(listed))}
		if len(listed) == 0 {
			out.Note = "the Dockhand keeps no backup destination, so nothing can be backed up yet: backup_destination_create saves one"

			return nil, out, nil
		}
		configs, err := r.backupConfigs(ctx)
		if err != nil {
			return nil, out, err
		}
		for i := range listed {
			row, hidden, err := r.backupDestinationRowOf(ctx, listed[i].Id, configs, in.RevealSecrets)
			if err != nil {
				return nil, out, fmt.Errorf("the backup destination %s could not be read: %w", listed[i].Name, err)
			}
			out.Destinations, out.Hidden = append(out.Destinations, row), out.Hidden+hidden
		}
		slices.SortFunc(out.Destinations, func(a, b backupDestinationRow) int {
			return cmp.Or(cmp.Compare(strings.ToLower(a.Name), strings.ToLower(b.Name)), cmp.Compare(a.ID, b.ID))
		})
		out.Total = len(out.Destinations)
		if out.Hidden > 0 {
			out.Note = hiddenNote(out.Hidden, "told by the names beside them")
		}

		return nil, out, nil
	})

	type destinationTestIn struct {
		backupDestinationArg

		Repository string            `json:"repository,omitempty" jsonschema:"to try a destination that is not saved: where it is, a folder's path or an address that opens with rest:, s3:, b2:, azure: or gs:"`
		Password   string            `json:"password,omitempty"   jsonschema:"with repository: the repository's password. It is sent to Dockhand to try and is not kept"`
		Variables  map[string]string `json:"variables,omitempty"  jsonschema:"with repository: what the store is signed in to with, by the names restic reads them under"`
	}
	type destinationTestOut struct {
		Destination string `json:"destination,omitempty" jsonschema:"the destination that was tried; absent for one that is not saved"`
		Repository  string `json:"repository"`
		Usable      bool   `json:"usable"                jsonschema:"Dockhand opened the repository with the password it has, or was given"`
		NeedsInit   bool   `json:"needs_init,omitempty"  jsonschema:"nothing answered as a repository there: one that has not been made yet, and one that cannot be reached, are told alike"`
		Error       string `json:"error,omitempty"       jsonschema:"what Dockhand says went wrong, when it could not"`
		Note        string `json:"note,omitempty"`
	}
	add(r, readTool, &mcp.Tool{
		Name:        "backup_destination_test",
		Description: "Have the Dockhand try a backup destination and say whether it could open it: one it keeps, by destination, or one that is not saved yet, by repository and password. Nothing is saved, and no repository is made. Dockhand makes the call itself, so this says that Dockhand can reach it, and not that a Docker host can: a backup runs on the host, which has to reach the destination as well. Dockhand cannot tell a repository that has not been made yet from an address nothing answers at, and says needs_init of both.",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in destinationTestIn) (*mcp.CallToolResult, destinationTestOut, error) {
		repository := strings.TrimSpace(in.Repository)
		if in.Destination == nil {
			switch {
			case repository == "":
				return nil, destinationTestOut{}, errors.New("destination or repository is required: a destination the Dockhand keeps, or where one that is not saved is, with its password")
			case in.Password == "":
				return nil, destinationTestOut{}, errors.New("password is required with repository: the repository's password")
			default:
			}
			tried, err := r.dock.PostBackupDestinationsTest(ctx, dockhand.PostBackupDestinationsTestRequest{Repository: repository, Password: in.Password, EnvVars: storeVariables(in.Variables)})
			switch {
			case client.IsNotFound(err):
				return nil, destinationTestOut{}, errBackupsOff
			case err != nil:
				return nil, destinationTestOut{}, fmt.Errorf("the repository could not be tried: %w", refusal(err))
			default:
			}
			shown, _ := maskText(repository)
			out := destinationTestOut{Repository: shown, Usable: tried.Answer.Success, NeedsInit: tried.Answer.NeedsInit, Error: plainly(tried.Answer.Error)}
			if out.NeedsInit {
				out.Note = unreachableOrNew + ". Saving it has Dockhand make one there"
			}

			return nil, out, nil
		}
		if repository != "" || in.Password != "" || len(in.Variables) > 0 {
			return nil, destinationTestOut{}, errors.New("repository, password and variables are for a destination that is not saved: one the Dockhand keeps is tried as it is kept")
		}
		listed, err := r.backupDestinations(ctx)
		if err != nil {
			return nil, destinationTestOut{}, err
		}
		destination, err := backupDestinationIn(listed, in.Destination)
		if err != nil {
			return nil, destinationTestOut{}, err
		}
		tried, err := r.dock.PostBackupDestinationsByIdTest(ctx, destination.Id)
		if err != nil {
			return nil, destinationTestOut{}, fmt.Errorf("the destination %s could not be tried: %w", destination.Name, refusal(err))
		}
		shown, _ := maskText(destination.Repository)
		out := destinationTestOut{Destination: destination.Name, Repository: shown, Usable: tried.Answer.Success, NeedsInit: tried.Answer.Status == "needs_init", Error: plainly(tried.Answer.Error)}
		if out.NeedsInit {
			out.Note = unreachableOrNew + ". backup_destination_task with init makes one there"
		}

		return nil, out, nil
	})

	type destinationCheckIn struct {
		backupDestinationArg
		waitArg

		Check  string `json:"check,omitempty"  jsonschema:"what to look at: stats (how much it holds, the quickest and what is done when this is left out), check (whether the repository's own records hang together) or verify (read a part of the data itself back, which is the only one that finds a damaged file)"`
		Subset string `json:"subset,omitempty" jsonschema:"with verify: how much of the data to read, as 10% or 2G; 5% unless said"`
	}
	type destinationCheckOut struct {
		jobState

		Destination string   `json:"destination"`
		Check       string   `json:"check"`
		Sound       bool     `json:"sound"               jsonschema:"restic found nothing wrong, once the check has ended; for stats, that the repository could be read"`
		Bytes       int      `json:"bytes,omitempty"     jsonschema:"for stats: how much the repository holds"`
		Files       int      `json:"files,omitempty"     jsonschema:"for stats: how many files its snapshots hold between them"`
		Snapshots   int      `json:"snapshots,omitempty" jsonschema:"for stats: how many snapshots it holds"`
		Printed     []string `json:"printed,omitempty"   jsonschema:"the last few lines of what restic printed"`
		Error       string   `json:"error,omitempty"     jsonschema:"what restic says is wrong, when something is"`
		Note        string   `json:"note,omitempty"`
	}
	add(r, readTool, &mcp.Tool{
		Name:        "backup_destination_check",
		Description: "Look at what a backup destination holds and whether it is sound, changing nothing in it: stats says how much it holds and how many snapshots, check has restic go through the repository's own records, and verify has it read a part of the data back, which is the one way to know a backup can be restored short of restoring it. A check and a verify of a large repository take long, so each is waited for as long as wait says and then answered as a job for job_get to follow. Something found wrong is said in error, with sound false, and is no error of this tool's.",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in destinationCheckIn) (*mcp.CallToolResult, destinationCheckOut, error) {
		check := cmp.Or(strings.TrimSpace(in.Check), backupChecks[0])
		subset := strings.TrimSpace(in.Subset)
		switch {
		case !slices.Contains(backupChecks, check):
			return nil, destinationCheckOut{}, fmt.Errorf("check is %q: it is %s", in.Check, strings.Join(backupChecks, ", "))
		case subset != "" && check != "verify":
			return nil, destinationCheckOut{}, errors.New("subset is how much a verify reads: give check verify with it")
		default:
		}
		patience, err := in.patience()
		if err != nil {
			return nil, destinationCheckOut{}, err
		}
		listed, err := r.backupDestinations(ctx)
		if err != nil {
			return nil, destinationCheckOut{}, err
		}
		destination, err := backupDestinationIn(listed, in.Destination)
		if err != nil {
			return nil, destinationCheckOut{}, err
		}
		out := destinationCheckOut{Destination: destination.Name, Check: check}

		looked, state, err := r.upkeep(ctx, destination, check, subset, patience)
		out.jobState = state
		if err != nil {
			return nil, out, err
		}
		if state.Running {
			out.Note = "still looking after " + seconds(patience) + ": job_get says what it finds"

			return nil, out, nil
		}
		out.Sound, out.Printed, out.Error = looked.Success, lastWords(looked.Output), plainly(looked.Error)
		if stats := looked.Stats; stats != nil {
			out.Bytes, out.Files, out.Snapshots = stats.TotalSize, stats.TotalFiles, stats.Snapshots
			out.Printed = nil
		}

		return nil, out, nil
	})

	type destinationCreateIn struct {
		dryRunArg

		Name         string            `json:"name"                    jsonschema:"what to call the destination: the name the tools then take, which no other destination has"`
		Repository   string            `json:"repository"              jsonschema:"where to keep the backups, as restic names it: a folder's path, or an address that opens with rest:, s3:, b2:, azure: or gs:. An SFTP server (sftp:), which Dockhand takes from 1.0.52 on, wants an SSH key this tool cannot give yet. It has to be one the Docker hosts can reach as well as Dockhand, since a backup runs on the host"`
		Password     string            `json:"password"                jsonschema:"the repository's password, which every snapshot in it is encrypted with: one of at least 8 characters for a new repository, or the one an existing repository has. It is sent to Dockhand and kept there, and is never given back. Without it nothing in the repository can be read, by anyone"`
		Variables    map[string]string `json:"variables,omitempty"     jsonschema:"what the store is signed in to with, by the names restic reads them under: for S3, AWS_ACCESS_KEY_ID and AWS_SECRET_ACCESS_KEY"`
		BackupFlags  string            `json:"backup_flags,omitempty"  jsonschema:"more for restic to be given at each backup, from the flags Dockhand allows: --limit-upload 500, say"`
		RestoreFlags string            `json:"restore_flags,omitempty" jsonschema:"more for restic to be given at each restore"`
		Upkeep       *backupUpkeep     `json:"upkeep,omitempty"        jsonschema:"how Dockhand is to look after it by itself. Left out, Dockhand's own: pruned and checked on the first of each month, not verified"`
		SaveUnusable bool              `json:"save_unusable,omitempty" jsonschema:"keep it though Dockhand could not open or make a repository there. Without this such a destination is not kept"`
	}
	add(r, writeTool, &mcp.Tool{
		Name:        "backup_destination_create",
		Description: "Save a place for the Dockhand to keep backups: a restic repository in a folder or in a store such as S3, with the password its snapshots are encrypted with. Dockhand makes the repository when there is none there yet. It is tried, and one Dockhand cannot open is not kept unless save_unusable says so: Dockhand left to itself saves a destination with a wrong password or an address nothing answers at, and every backup to it then fails. Nothing is backed up until backup_create makes a backup to it. Keep the password somewhere else too: Dockhand never gives it back, and nothing can be restored without it.",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in destinationCreateIn) (*mcp.CallToolResult, backupDestinationOut, error) {
		name, repository := strings.TrimSpace(in.Name), strings.TrimSpace(in.Repository)
		switch {
		case name == "":
			return nil, backupDestinationOut{}, errors.New("name is required: what to call the destination")
		case repository == "":
			return nil, backupDestinationOut{}, errors.New("repository is required: where to keep the backups, as a folder's path or as an address, which opens with rest:, s3:, b2:, azure: or gs: and then says where")
		case in.Password == "":
			return nil, backupDestinationOut{}, errors.New("password is required: the password the repository's snapshots are encrypted with")
		default:
		}
		listed, err := r.backupDestinations(ctx)
		if err != nil {
			return nil, backupDestinationOut{}, err
		}
		if slices.ContainsFunc(listed, func(d dockhand.GetBackupDestinationsAnswer) bool { return d.Name == name }) {
			return nil, backupDestinationOut{}, fmt.Errorf("a backup destination is already named %s: backup_destination_edit changes it, and a new one takes a name of its own", name)
		}
		out := backupDestinationOut{Changes: []string{}, DryRun: in.DryRun}
		shown, _ := maskText(repository)

		tried, err := r.dock.PostBackupDestinationsTest(ctx, dockhand.PostBackupDestinationsTestRequest{Repository: repository, Password: in.Password, EnvVars: storeVariables(in.Variables)})
		if err != nil {
			return nil, out, fmt.Errorf("the repository could not be tried, so nothing was saved: %w", refusal(err))
		}
		unusable := !tried.Answer.Success && !tried.Answer.NeedsInit
		if unusable && !in.SaveUnusable {
			return nil, out, fmt.Errorf("Dockhand could not open the repository, so nothing was saved: %s. save_unusable saves it all the same", reasonOf(plainly(tried.Answer.Error))) //nolint:staticcheck // ST1005: the sentence begins with the server's name
		}
		if in.DryRun {
			out.Changes = append(out.Changes, fmt.Sprintf("would save the backup destination %s at %s", name, shown))
			switch {
			case unusable:
				out.Note = "it would be saved as save_unusable asks though Dockhand cannot open it: " + reasonOf(plainly(tried.Answer.Error))
			case tried.Answer.NeedsInit:
				out.Note = unreachableOrNew + ". Saving it has Dockhand make one there, and it is not kept when that fails"
			default:
			}

			return nil, out, nil
		}

		request := dockhand.PostBackupDestinationsRequest{Name: name, Repository: repository, Password: in.Password, EnvVars: storeVariables(in.Variables), BackupFlags: strings.TrimSpace(in.BackupFlags), RestoreFlags: strings.TrimSpace(in.RestoreFlags)}
		if policies := in.Upkeep.byName(); len(policies) > 0 {
			written, unwritten := json.Marshal(policies)
			if unwritten != nil {
				return nil, out, fmt.Errorf("upkeep could not be written out: %w", unwritten)
			}
			request.Policies = string(written)
		}
		made, err := r.dock.PostBackupDestinations(ctx, request)
		if err != nil {
			return nil, out, fmt.Errorf("the backup destination %s was not saved: %w", name, refusal(err))
		}
		out.Changes = append(out.Changes, fmt.Sprintf("saved the backup destination %s at %s", name, shown))

		// Dockhand saves it whatever became of the repository, and says how that went only in what it keeps of its last try
		row := r.destinationRead(ctx, &out, made.Answer.Id)
		if row == nil || row.Usable {
			return nil, out, nil
		}
		if in.SaveUnusable {
			out.Note = noted(out.Note, "it was kept as save_unusable asked though Dockhand cannot open it: "+reasonOf(row.Error))

			return nil, out, nil
		}
		if _, err := r.dock.DeleteBackupDestinationsById(ctx, made.Answer.Id); err != nil {
			return nil, out, fmt.Errorf("Dockhand saved the destination %s and then could not open or make a repository there (%s), and it could not be removed again: %w", name, reasonOf(row.Error), refusal(err)) //nolint:staticcheck // ST1005: the sentence begins with the server's name
		}
		out.Destination, out.Changes = nil, []string{}

		return nil, out, fmt.Errorf("Dockhand could not open or make a repository at %s, so the destination was not kept: %s. save_unusable keeps it all the same", shown, reasonOf(row.Error)) //nolint:staticcheck // ST1005: the sentence begins with the server's name
	})

	type destinationEditIn struct {
		backupDestinationArg
		dryRunArg

		Name         string             `json:"name,omitempty"          jsonschema:"a new name for it"`
		Repository   string             `json:"repository,omitempty"    jsonschema:"where it is from now on, which is somewhere else that already holds a repository: password is wanted with it, and the two are tried before anything is saved"`
		Password     string             `json:"password,omitempty"      jsonschema:"the password of the repository as it is, for Dockhand to keep in place of the one it has. It changes nothing in the repository: backup_destination_rotate_key does that"`
		Variables    *map[string]string `json:"variables,omitempty"     jsonschema:"what the store is signed in to with, the whole of it, in place of what is kept"`
		BackupFlags  *string            `json:"backup_flags,omitempty"  jsonschema:"more for restic to be given at each backup, in place of what is kept; empty for none"`
		RestoreFlags *string            `json:"restore_flags,omitempty" jsonschema:"more for restic to be given at each restore; empty for none"`
		Upkeep       *backupUpkeep      `json:"upkeep,omitempty"        jsonschema:"how Dockhand looks after it by itself: what is named here is changed and the rest stays, though Dockhand itself replaces the whole of it with whatever it is sent"`
		SaveUnusable bool               `json:"save_unusable,omitempty" jsonschema:"save a repository, a password or variables that Dockhand cannot open the repository with. Without this they are not saved"`
	}
	add(r, writeTool, &mcp.Tool{
		Name:        "backup_destination_edit",
		Description: "Change a backup destination the Dockhand keeps: its name, where it is, the password Dockhand has for it, what it is signed in to with, the flags restic is given, and how it is looked after by itself. What is not named stays as it was. A new address or password is tried before it is saved, and new variables straight after, being put back when the repository can no longer be opened: Dockhand left to itself saves any of them without a word, and every backup then fails. One a backup is running to is refused until the backup has ended.",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in destinationEditIn) (*mcp.CallToolResult, backupDestinationOut, error) {
		listed, err := r.backupDestinations(ctx)
		if err != nil {
			return nil, backupDestinationOut{}, err
		}
		destination, err := backupDestinationIn(listed, in.Destination)
		if err != nil {
			return nil, backupDestinationOut{}, err
		}
		had, err := r.dock.GetBackupDestinationsById(ctx, destination.Id)
		if err != nil {
			return nil, backupDestinationOut{}, fmt.Errorf("the destination %s could not be read, so nothing was changed: %w", destination.Name, refusal(err))
		}
		out := backupDestinationOut{Changes: []string{}, DryRun: in.DryRun}
		done := "set"
		if in.DryRun {
			done = "would set"
		}
		name, repository := strings.TrimSpace(in.Name), strings.TrimSpace(in.Repository)

		var request dockhand.PutBackupDestinationsByIdRequest
		forget := dockhand.BackupDestinationFlags{}
		if name != "" && name != destination.Name {
			if slices.ContainsFunc(listed, func(d dockhand.GetBackupDestinationsAnswer) bool { return d.Name == name }) {
				return nil, out, fmt.Errorf("a backup destination is already named %s", name)
			}
			request.Name = name
			out.Changes = append(out.Changes, fmt.Sprintf("%s its name to %s", done, name))
		}
		if repository != "" && repository != had.Answer.Repository {
			if in.Password == "" {
				return nil, out, errors.New("password is required with repository: Dockhand would go on using the password it keeps at the new address without trying it, so the repository's password is given again and the two are tried first")
			}
			shown, _ := maskText(repository)
			request.Repository = repository
			out.Changes = append(out.Changes, fmt.Sprintf("%s where it is to %s", done, shown))
		}
		if in.Password != "" {
			request.Password = in.Password
			out.Changes = append(out.Changes, done+" the password Dockhand keeps for it")
		}
		if in.Variables != nil && !maps.Equal(*in.Variables, had.Answer.EnvVars) {
			request.EnvVars = storeVariables(*in.Variables)
			if request.EnvVars == nil {
				request.EnvVars = map[string]any{}
			}
			names := slices.Sorted(maps.Keys(*in.Variables))
			out.Changes = append(out.Changes, fmt.Sprintf("%s what it is signed in to with to %s", done, cmp.Or(strings.Join(names, ", "), "nothing")))
		}
		for _, flags := range []struct {
			given     *string
			had, what string
			set       *string
			forget    *bool
		}{{in.BackupFlags, had.Answer.BackupFlags, "backup_flags", &request.BackupFlags, &forget.Backup}, {in.RestoreFlags, had.Answer.RestoreFlags, "restore_flags", &request.RestoreFlags, &forget.Restore}} {
			if flags.given == nil || strings.TrimSpace(*flags.given) == flags.had {
				continue
			}
			*flags.set, *flags.forget = strings.TrimSpace(*flags.given), strings.TrimSpace(*flags.given) == ""
			out.Changes = append(out.Changes, fmt.Sprintf("%s %s to %s", done, flags.what, cmp.Or(*flags.set, "none")))
		}
		if changed := in.Upkeep.byName(); len(changed) > 0 {
			policies := map[string]any{}
			if strings.TrimSpace(had.Answer.Policies) != "" {
				if err := json.Unmarshal([]byte(had.Answer.Policies), &policies); err != nil {
					return nil, out, fmt.Errorf("the upkeep Dockhand keeps for %s could not be read, so nothing was changed: %w", destination.Name, err)
				}
			}
			before, _ := json.Marshal(policies)
			maps.Copy(policies, changed)
			after, err := json.Marshal(policies)
			if err != nil {
				return nil, out, fmt.Errorf("upkeep could not be written out: %w", err)
			}
			if !bytes.Equal(after, before) {
				request.Policies = string(after)
				out.Changes = append(out.Changes, fmt.Sprintf("%s its upkeep: %s", done, strings.Join(slices.Sorted(maps.Keys(changed)), ", ")))
			}
		}
		if len(out.Changes) == 0 {
			out.Note = "it is already as asked for, so nothing was changed"
			r.destinationRead(ctx, &out, destination.Id)

			return nil, out, nil
		}

		// a new address or password is tried with what would be saved, before anything is
		if request.Repository != "" || request.Password != "" {
			variables := had.Answer.EnvVars
			if in.Variables != nil {
				variables = *in.Variables
			}
			tried, err := r.dock.PostBackupDestinationsTest(ctx, dockhand.PostBackupDestinationsTestRequest{Repository: cmp.Or(request.Repository, had.Answer.Repository), Password: in.Password, EnvVars: storeVariables(variables)})
			if err != nil {
				return nil, out, fmt.Errorf("the repository could not be tried, so nothing was changed: %w", refusal(err))
			}
			if !tried.Answer.Success && !in.SaveUnusable {
				why := reasonOf(plainly(tried.Answer.Error))
				if tried.Answer.NeedsInit {
					why = unreachableOrNew
				}

				return nil, out, fmt.Errorf("Dockhand could not open the repository with that, so nothing was changed: %s. save_unusable saves it all the same", why) //nolint:staticcheck // ST1005: the sentence begins with the server's name
			}
		}
		if in.DryRun {
			return nil, out, nil
		}

		if request.Name != "" || request.Repository != "" || request.Password != "" || request.EnvVars != nil || request.BackupFlags != "" || request.RestoreFlags != "" || request.Policies != "" {
			if _, err := r.dock.PutBackupDestinationsById(ctx, destination.Id, request); err != nil {
				return nil, out, fmt.Errorf("the destination %s was not changed: %w", destination.Name, refusal(err))
			}
		}
		if forget != (dockhand.BackupDestinationFlags{}) {
			if _, err := r.dock.ForgetBackupDestinationFlags(ctx, destination.Id, forget); err != nil {
				return nil, out, fmt.Errorf("the destination %s was changed, and its flags could not be cleared: %w", destination.Name, refusal(err))
			}
		}

		// Dockhand saves what it is signed in to with untried: it is tried now, and put back when the repository no longer opens
		if request.EnvVars != nil && request.Repository == "" && request.Password == "" {
			tried, err := r.dock.PostBackupDestinationsByIdTest(ctx, destination.Id)
			if err == nil && !tried.Answer.Success && !in.SaveUnusable {
				back := storeVariables(had.Answer.EnvVars)
				if back == nil {
					back = map[string]any{}
				}
				if _, err := r.dock.PutBackupDestinationsById(ctx, destination.Id, dockhand.PutBackupDestinationsByIdRequest{EnvVars: back}); err != nil {
					return nil, out, fmt.Errorf("Dockhand cannot open %s with what it is now signed in to with (%s), and what it had could not be put back: %w", destination.Name, reasonOf(plainly(tried.Answer.Error)), refusal(err)) //nolint:staticcheck // ST1005: the sentence begins with the server's name
				}
				r.destinationRead(ctx, &out, destination.Id)

				return nil, out, fmt.Errorf("Dockhand could not open %s with that, so what it was signed in to with before was put back: %s. save_unusable saves it all the same", destination.Name, reasonOf(plainly(tried.Answer.Error))) //nolint:staticcheck // ST1005: the sentence begins with the server's name
			}
		}
		if row := r.destinationRead(ctx, &out, destination.Id); row != nil && !row.Usable && (request.Repository != "" || request.Password != "") {
			out.Note = noted(out.Note, "it was saved as save_unusable asked though Dockhand cannot open it")
		}

		return nil, out, nil
	})

	type destinationTaskIn struct {
		backupDestinationArg
		dryRunArg
		waitArg

		Task string `json:"task" jsonschema:"what to do: unlock (clear a lock a run that was cut short left behind), prune (clear out data no snapshot uses any more), repair-index or repair-snapshots (mend what a check found wrong), or init (make a repository where there is none)"`
	}
	type destinationTaskOut struct {
		jobState

		Destination string   `json:"destination"`
		Task        string   `json:"task"`
		Done        bool     `json:"done"              jsonschema:"the task ran to its end and restic reported no failure"`
		Printed     []string `json:"printed,omitempty" jsonschema:"the last few lines of what restic printed"`
		DryRun      bool     `json:"dry_run,omitempty" jsonschema:"nothing was done"`
		Note        string   `json:"note,omitempty"`
	}
	add(r, writeTool, &mcp.Tool{
		Name:        "backup_destination_task",
		Description: "Have restic do one piece of upkeep on a backup destination now: unlock, prune, repair-index, repair-snapshots or init. A prune and a repair take long on a large repository and lock it while they run, so no backup to it runs meanwhile; each is waited for as long as wait says and then answered as a job for job_get to follow. Prune removes no snapshot: only data that no snapshot uses any more, which backup_snapshot_delete and a backup's own keep leave behind. A repair is for what backup_destination_check found wrong, and changes the repository's records. One that failed is an error that says why.",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in destinationTaskIn) (*mcp.CallToolResult, destinationTaskOut, error) {
		task := strings.TrimSpace(in.Task)
		switch {
		case task == "":
			return nil, destinationTaskOut{}, errors.New("task is required: " + strings.Join(backupTasks, ", "))
		case slices.Contains(backupChecks, task):
			return nil, destinationTaskOut{}, fmt.Errorf("%s only looks, and is backup_destination_check's", task)
		case !slices.Contains(backupTasks, task):
			return nil, destinationTaskOut{}, fmt.Errorf("task is %q: it is %s", in.Task, strings.Join(backupTasks, ", "))
		default:
		}
		patience, err := in.patience()
		if err != nil {
			return nil, destinationTaskOut{}, err
		}
		listed, err := r.backupDestinations(ctx)
		if err != nil {
			return nil, destinationTaskOut{}, err
		}
		destination, err := backupDestinationIn(listed, in.Destination)
		if err != nil {
			return nil, destinationTaskOut{}, err
		}
		out := destinationTaskOut{Destination: destination.Name, Task: task, DryRun: in.DryRun}
		if in.DryRun {
			out.Note = "would have restic " + task + " the repository of " + destination.Name

			return nil, out, nil
		}

		if task == "init" {
			made, unmade := r.dock.PostBackupDestinationsByIdInit(ctx, destination.Id)
			if unmade != nil {
				return nil, out, fmt.Errorf("no repository could be made for %s: %w", destination.Name, refusal(unmade))
			}
			if !made.Answer.Success {
				return nil, out, fmt.Errorf("no repository could be made for %s: %s", destination.Name, reasonOf(plainly(made.Answer.Error)))
			}
			out.Done, out.Printed = true, lastWords(made.Answer.Message)

			return nil, out, nil
		}
		did, state, err := r.upkeep(ctx, destination, task, "", patience)
		out.jobState = state
		if err != nil {
			return nil, out, err
		}
		if state.Running {
			out.Note = "still running after " + seconds(patience) + ": job_get says how it ends"

			return nil, out, nil
		}
		out.Printed = lastWords(did.Output)
		if !did.Success {
			return nil, out, fmt.Errorf("restic could not %s the repository of %s: %s", task, destination.Name, reasonOf(plainly(did.Error)))
		}
		out.Done = true

		return nil, out, nil
	})

	type destinationRotateIn struct {
		backupDestinationArg
		dryRunArg

		CurrentPassword string `json:"current_password" jsonschema:"the password the repository has now"`
		NewPassword     string `json:"new_password"     jsonschema:"the password it is to have: at least 8 characters, and not the one it has. Keep it somewhere else too, since Dockhand never gives it back"`
	}
	type destinationRotateOut struct {
		Destination string `json:"destination"`
		Rotated     bool   `json:"rotated"           jsonschema:"the repository now opens with the new password and no longer with the old"`
		Usable      bool   `json:"usable"            jsonschema:"Dockhand opened the repository with the password it now keeps, tried after the change"`
		DryRun      bool   `json:"dry_run,omitempty" jsonschema:"nothing was changed"`
		Note        string `json:"note,omitempty"`
	}
	add(r, writeTool, &mcp.Tool{
		Name:        "backup_destination_rotate_key",
		Description: "Change the password of a backup destination's repository itself, and have the Dockhand keep the new one: every snapshot in it is read with the new password from then on, and the old no longer opens it. It wants the password the repository has now, which Dockhand checks against the one it keeps. The repository is tried afterwards with what Dockhand kept. Anything else that reads this repository, another Dockhand or restic by hand, has to be given the new password too.",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in destinationRotateIn) (*mcp.CallToolResult, destinationRotateOut, error) {
		switch {
		case in.CurrentPassword == "" || in.NewPassword == "":
			return nil, destinationRotateOut{}, errors.New("current_password and new_password are required: the password the repository has, and the one it is to have")
		case len(in.NewPassword) < 8:
			return nil, destinationRotateOut{}, errors.New("new_password is shorter than 8 characters, which Dockhand refuses")
		case in.NewPassword == in.CurrentPassword:
			return nil, destinationRotateOut{}, errors.New("new_password is the password it has: there is nothing to change")
		default:
		}
		listed, err := r.backupDestinations(ctx)
		if err != nil {
			return nil, destinationRotateOut{}, err
		}
		destination, err := backupDestinationIn(listed, in.Destination)
		if err != nil {
			return nil, destinationRotateOut{}, err
		}
		out := destinationRotateOut{Destination: destination.Name, DryRun: in.DryRun}
		if in.DryRun {
			out.Note = "would change the password of the repository of " + destination.Name + ", which Dockhand checks the current one against only when it is done"

			return nil, out, nil
		}

		if _, err := r.dock.PostBackupDestinationsByIdRotateKey(ctx, destination.Id, dockhand.PostBackupDestinationsByIdRotateKeyRequest{CurrentPassword: in.CurrentPassword, NewPassword: in.NewPassword}); err != nil {
			return nil, out, fmt.Errorf("the password of %s was not changed: %w", destination.Name, refusal(err))
		}
		out.Rotated = true
		tried, err := r.dock.PostBackupDestinationsByIdTest(ctx, destination.Id)
		switch {
		case err != nil:
			out.Note = "it was changed, and the repository could not be tried afterwards: " + refusal(err).Error()
		case !tried.Answer.Success:
			out.Note = "it was changed, and Dockhand cannot open the repository with what it kept: " + reasonOf(plainly(tried.Answer.Error))
		default:
			out.Usable = true
		}

		return nil, out, nil
	})

	type destinationDeleteIn struct {
		backupDestinationArg

		Confirm  bool `json:"confirm,omitempty"   jsonschema:"remove it: without this the answer says what would go and nothing is removed"`
		EvenUsed bool `json:"even_used,omitempty" jsonschema:"remove it though backups are kept in it, each of which Dockhand removes with it"`
	}
	type destinationDeleteOut struct {
		Destination string   `json:"destination"`
		Backups     []string `json:"backups,omitempty"  jsonschema:"the backups kept in it, each of which goes with it"`
		WouldGo     []string `json:"would_go,omitempty" jsonschema:"what removing it takes away; absent once it is removed"`
		Removed     bool     `json:"removed"`
		Note        string   `json:"note,omitempty"`
	}
	add(r, deleteTool, &mcp.Tool{
		Name:        "backup_destination_delete",
		Description: "Have the Dockhand forget a backup destination, with the password it keeps for it. Dockhand removes every backup kept in it at the same moment, schedule and all, without a word, so one with backups is refused unless even_used is given, and the answer names them first. The repository itself is not touched: its snapshots stay where they are, and saving the destination again with the same password finds them. Without confirm the answer says what would go and removes nothing.",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in destinationDeleteIn) (*mcp.CallToolResult, destinationDeleteOut, error) {
		listed, err := r.backupDestinations(ctx)
		if err != nil {
			return nil, destinationDeleteOut{}, err
		}
		destination, err := backupDestinationIn(listed, in.Destination)
		if err != nil {
			return nil, destinationDeleteOut{}, err
		}
		configs, err := r.backupConfigs(ctx)
		if err != nil {
			return nil, destinationDeleteOut{}, fmt.Errorf("the backups could not be listed, so nothing was removed: %w", err)
		}
		out := destinationDeleteOut{Destination: destination.Name, Backups: usesOf(configs, destination.Id)}
		out.WouldGo = append(out.WouldGo, fmt.Sprintf("the backup destination %s, with the password Dockhand keeps for it", destination.Name))
		if len(out.Backups) > 0 {
			out.WouldGo = append(out.WouldGo, fmt.Sprintf("%s kept in it, with their schedules: %s", several(len(out.Backups), "backup"), strings.Join(out.Backups, ", ")))
		}
		if !in.Confirm {
			out.Note = "nothing was removed: confirm removes it"
			if len(out.Backups) > 0 {
				out.Note = "nothing was removed: confirm with even_used removes it"
			}

			return nil, out, nil
		}
		if len(out.Backups) > 0 && !in.EvenUsed {
			return nil, out, fmt.Errorf("the destination %s holds %s: %s. Dockhand would remove each with it without a word: give each another destination with backup_edit first, or even_used removes them all the same", destination.Name, several(len(out.Backups), "backup"), strings.Join(out.Backups, ", "))
		}

		if _, err := r.dock.DeleteBackupDestinationsById(ctx, destination.Id); err != nil {
			return nil, out, fmt.Errorf("the destination %s was not removed: %w", destination.Name, refusal(err))
		}
		after, err := r.backupDestinations(ctx)
		switch {
		case err != nil:
			out.Note = "it was removed, and the destinations could not be listed afterwards: " + err.Error()
		case slices.ContainsFunc(after, func(d dockhand.GetBackupDestinationsAnswer) bool { return d.Id == destination.Id }):
			return nil, out, fmt.Errorf("Dockhand answered that %s was removed, and still lists it", destination.Name) //nolint:staticcheck // ST1005: the sentence begins with the server's name
		default:
		}
		out.Removed, out.WouldGo = true, nil
		out.Note = noted(out.Note, "the repository itself was not touched: its snapshots are where they were")

		return nil, out, nil
	})
}

// upkeep has restic do one thing to a destination's repository, as a job on Dockhand, and waits for it as long as the patience given. A state that
// is running means the wait ran out first; what restic found wrong is in the answer, and an error is the task not being run at all.
func (r *registry) upkeep(ctx context.Context, destination *dockhand.GetBackupDestinationsAnswer, task, subset string, patience time.Duration) (*dockhand.PostBackupDestinationsByIdTaskAnswer, jobState, error) {
	if task == "verify" {
		started, err := r.dock.PostBackupDestinationsByIdVerify(ctx, destination.Id, dockhand.PostBackupDestinationsByIdVerifyRequest{DataSubset: subset}, dockhand.PostBackupDestinationsByIdVerifyOptions{Accept: dockhand.AcceptJob})
		if err != nil {
			return nil, jobState{}, fmt.Errorf("the repository of %s could not be verified: %w", destination.Name, refusal(err))
		}
		verified, state, err := finished(ctx, r, started.Answer.JobId, started.Answer, patience)
		if err != nil || state.Running {
			return nil, state, err
		}

		return &dockhand.PostBackupDestinationsByIdTaskAnswer{Success: verified.Success, Output: verified.Output, Error: verified.Error}, state, nil
	}

	started, err := r.dock.PostBackupDestinationsByIdTask(ctx, destination.Id, dockhand.PostBackupDestinationsByIdTaskRequest{Task: task}, dockhand.PostBackupDestinationsByIdTaskOptions{Accept: dockhand.AcceptJob})
	if err != nil {
		return nil, jobState{}, fmt.Errorf("restic could not be asked to %s the repository of %s: %w", task, destination.Name, refusal(err))
	}

	return finished(ctx, r, started.Answer.JobId, started.Answer, patience)
}

// finished is settle for work whose own answer says how it went: a job that has ended is read as the call's answer whatever that answer says, so
// that what restic found wrong in a repository reaches the caller as what was found, and not as the looking having failed.
func finished[T any](ctx context.Context, r *registry, jobID string, answered *T, patience time.Duration) (*T, jobState, error) {
	if jobID == "" {
		return answered, jobState{}, nil
	}

	j, err := r.await(ctx, jobID, patience)
	if err != nil {
		return nil, jobState{}, err
	}
	state := stateOfJob(j)
	if state.Running {
		return nil, state, nil
	}
	if len(j.Result) == 0 {
		return nil, state, errors.New(failureOf(j))
	}

	var outcome T
	raw, err := json.Marshal(j.Result)
	if err == nil {
		err = json.Unmarshal(raw, &outcome)
	}
	if err != nil {
		return nil, state, fmt.Errorf("job %s ended, and its result could not be read: %w", jobID, err)
	}

	return &outcome, state, nil
}
