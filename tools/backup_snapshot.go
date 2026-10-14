package tools

import (
	"cmp"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"os"
	"path"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/katbyte/dockhand-mcp/sdk/dockhand"
	"github.com/katbyte/pandorest/client"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

const (
	// snapshotVolumes is where a snapshot holds the data of the volumes it was taken of, each in a folder of its own
	snapshotVolumes = "/volumes"
	// snapshotRecord is where it holds what Dockhand wrote down of the container or stack, which is read only through the call that hides its secrets
	snapshotRecord = "/metadata"
	// maxSnapshotRead is the most of one file a tool reads out of a snapshot as text, which is what the other tools read of a file
	maxSnapshotRead = 1 << 20
	// changesShown is how many paths of each kind a comparison of two snapshots lists
	changesShown = 100

	// What Dockhand does with a container or stack once its data is back, in its own words.
	afterNothing  = "none"
	afterStart    = "start"
	afterRecreate = "recreate"

	// The two ways a restore goes, in Dockhand's words: over what is there, and to somewhere else.
	restoreInPlace = "in-place"
	restoreAway    = "new-location"
)

// snapshotArg names one snapshot, which is in one destination.
type snapshotArg struct {
	Destination any    `json:"destination,omitempty" jsonschema:"the backup destination the snapshot is in, by its name or number; or give backup in its place"`
	Backup      any    `json:"backup,omitempty"      jsonschema:"in place of destination: a backup, by its number or the name of what it is of, whose destination is meant"`
	Snapshot    string `json:"snapshot"              jsonschema:"the snapshot, by its id as backup_snapshot_list gives it: the eight characters, or the whole"`
}

// snapshotRow is one snapshot as the tools give it.
type snapshotRow struct {
	ID     string `json:"id"               jsonschema:"what the snapshot is named by wherever one is asked for"`
	Taken  string `json:"taken"`
	Of     string `json:"of,omitempty"     jsonschema:"the container or stack it is of, by name"`
	Type   string `json:"type,omitempty"   jsonschema:"container or stack"`
	Env    string `json:"env,omitempty"    jsonschema:"the environment (Docker host) it was taken on"`
	Backup int    `json:"backup,omitempty" jsonschema:"the backup that took it, by its number; one that backup_list no longer has was removed since"`
	Ours   bool   `json:"ours"             jsonschema:"whether this Dockhand took it. One another Dockhand left in the same repository is listed and cannot be removed or restored from here"`
}

// taken is when restic says a snapshot was taken, as every tool gives a date: UTC, to the second, and as it was written when it is no date.
func taken(written string) string {
	at, err := time.Parse(time.RFC3339Nano, written)
	if err != nil {
		return written
	}

	return at.UTC().Format(time.RFC3339)
}

// snapshotRowOf is a snapshot as the tools give it, read out of the tags Dockhand marks each with.
func snapshotRowOf(s *dockhand.GetBackupSnapshotsAnswer, instance string, envs []environment) snapshotRow {
	row := snapshotRow{ID: cmp.Or(s.ShortId, shortSnapshot(s.Id)), Taken: taken(s.Time)}
	for _, tag := range s.Tags {
		name, value, _ := strings.Cut(strings.TrimPrefix(tag, "dockhand:"), "=")
		switch name {
		case "instance":
			row.Ours = value == instance
		case "configid":
			row.Backup, _ = strconv.Atoi(value)
		case "envid":
			id, _ := strconv.Atoi(value)
			i := slices.IndexFunc(envs, func(e environment) bool { return int(e.ID) == id })
			row.Env = nameOr(i, id, func(i int) string { return envs[i].Name })
		case "type":
			row.Type = value
		case "name":
			row.Of = value
		default:
		}
	}

	return row
}

// snapshotsIn is every snapshot in a destination, the newest first.
func (r *registry) snapshotsIn(ctx context.Context, destination *dockhand.GetBackupDestinationsAnswer) ([]dockhand.GetBackupSnapshotsAnswer, error) {
	listed, err := r.dock.GetBackupSnapshots(ctx, dockhand.GetBackupSnapshotsOptions{DestinationId: destination.Id})
	if err != nil {
		return nil, fmt.Errorf("the snapshots in %s could not be listed, which is how a destination that cannot be opened answers: %w", destination.Name, refusal(err))
	}

	return listed.Answer, nil
}

// destinationOf is the destination a call about a snapshot means: the one it names, or the one a backup it names keeps its snapshots in.
func (r *registry) destinationOf(ctx context.Context, destination, backup any) (*dockhand.GetBackupDestinationsAnswer, error) {
	destinations, err := r.backupDestinations(ctx)
	if err != nil {
		return nil, err
	}
	switch {
	case destination != nil && backup != nil:
		return nil, errors.New("destination and backup both say where the snapshot is: give one")
	case destination != nil:
		return backupDestinationIn(destinations, destination)
	case backup == nil:
		return nil, errors.New("destination or backup is required: where the snapshot is kept, or the backup that took it")
	default:
	}
	configs, err := r.backupConfigs(ctx)
	if err != nil {
		return nil, err
	}
	cfg, err := backupIn(configs, backup)
	if err != nil {
		return nil, err
	}
	i := slices.IndexFunc(destinations, func(d dockhand.GetBackupDestinationsAnswer) bool { return d.Id == cfg.DestinationId })
	if i < 0 {
		return nil, fmt.Errorf("the destination of the backup %d is gone", cfg.Id)
	}

	return &destinations[i], nil
}

// snapshotAt is the destination a snapshot is in and the id it is asked for by, or the error that says what is missing.
func (r *registry) snapshotAt(ctx context.Context, in snapshotArg) (*dockhand.GetBackupDestinationsAnswer, string, error) {
	id := strings.TrimSpace(in.Snapshot)
	if id == "" {
		return nil, "", errors.New("snapshot is required: its id, as backup_snapshot_list gives it")
	}
	destination, err := r.destinationOf(ctx, in.Destination, in.Backup)
	if err != nil {
		return nil, "", err
	}

	return destination, id, nil
}

// snapshotPath is a path inside a snapshot as it is asked for: rooted, and with no way out of the snapshot written into it.
func snapshotPath(given string) (string, error) {
	cleaned := path.Clean("/" + strings.TrimSpace(given))
	if strings.Contains(given, "..") {
		return "", fmt.Errorf("path is %q: it is a path inside the snapshot, with no .. in it", given)
	}

	return cleaned, nil
}

// restoreTarget is where one volume of a snapshot would be put by a restore.
type restoreTarget struct {
	Volume string `json:"volume"          jsonschema:"the volume as the snapshot names it"`
	To     string `json:"to"              jsonschema:"the volume, or the path on the Docker host, its data would be written to"`
	Kind   string `json:"kind"            jsonschema:"volume or bind, a bind being a folder of the host"`
	There  string `json:"there,omitempty" jsonschema:"what is at the target now: has-data, which the restore replaces; empty; or missing, which the restore makes"`
}

//nolint:gocyclo,maintidx // the snapshot tools registered in one function, each a handler of its own: the count is theirs added up
func registerBackupSnapshotTools(r *registry) {
	type snapshotListIn struct {
		Destination any    `json:"destination,omitempty" jsonschema:"every snapshot in this backup destination, by its name or number"`
		Backup      any    `json:"backup,omitempty"      jsonschema:"in place of destination: only the snapshots this backup took, by its number or the name of what it is of"`
		Of          string `json:"of,omitempty"          jsonschema:"only the snapshots of the container or stack of this name"`
		Limit       int    `json:"limit,omitempty"       jsonschema:"how many to give, the newest first; 50 unless said"`
	}
	type snapshotListOut struct {
		Destination string        `json:"destination"`
		Snapshots   []snapshotRow `json:"snapshots"`
		Total       int           `json:"total"          jsonschema:"how many there are, of which snapshots holds the newest"`
		Note        string        `json:"note,omitempty"`
	}
	add(r, readTool, &mcp.Tool{
		Name:        "backup_snapshot_list",
		Description: "List the snapshots in a backup destination, the newest first: each one copy of a container's or a stack's volumes as they were at one moment, with when it was taken, what of, on which Docker host, and the backup that took it. Name the destination for every snapshot in it, those of backups since removed among them, or a backup for its own. A repository can hold what another Dockhand put there, which is listed with ours false and is not this Dockhand's to remove or restore. It reads the repository itself, so a destination that cannot be opened is an error that says so.",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in snapshotListIn) (*mcp.CallToolResult, snapshotListOut, error) {
		if in.Limit < 0 {
			return nil, snapshotListOut{}, fmt.Errorf("limit is %d: it is how many to give", in.Limit)
		}
		destination, err := r.destinationOf(ctx, in.Destination, in.Backup)
		if err != nil {
			return nil, snapshotListOut{}, err
		}
		out := snapshotListOut{Destination: destination.Name, Snapshots: []snapshotRow{}}
		only := 0
		if in.Backup != nil {
			configs, unlisted := r.backupConfigs(ctx)
			if unlisted != nil {
				return nil, out, unlisted
			}
			cfg, unknown := backupIn(configs, in.Backup)
			if unknown != nil {
				return nil, out, unknown
			}
			only = int(cfg.Id)
		}
		listed, err := r.snapshotsIn(ctx, destination)
		if err != nil {
			return nil, out, err
		}
		envs, err := r.environments(ctx)
		if err != nil {
			return nil, out, err
		}
		instance, err := r.dock.GetBackupInstance(ctx)
		if err != nil {
			return nil, out, fmt.Errorf("which snapshots are this Dockhand's own could not be told: %w", refusal(err))
		}
		of := strings.TrimSpace(in.Of)
		for i := range listed {
			row := snapshotRowOf(&listed[i], instance.Answer.InstanceId, envs)
			if (only != 0 && (row.Backup != only || !row.Ours)) || (of != "" && row.Of != of) {
				continue
			}
			out.Snapshots = append(out.Snapshots, row)
		}
		slices.SortStableFunc(out.Snapshots, func(a, b snapshotRow) int { return cmp.Compare(b.Taken, a.Taken) })
		out.Total = len(out.Snapshots)
		if limit := cmp.Or(in.Limit, 50); out.Total > limit {
			out.Snapshots = out.Snapshots[:limit]
			out.Note = fmt.Sprintf("the newest %d of %d: limit gives more", limit, out.Total)
		}
		if out.Total == 0 {
			out.Note = "no snapshot is as asked for; the destination holds " + several(len(listed), "snapshot")
		}

		return nil, out, nil
	})

	type snapshotVolume struct {
		Name string `json:"name"           jsonschema:"what a restore names it by"`
		Kind string `json:"kind"           jsonschema:"volume, or bind for a folder of the host that was mounted in"`
		From string `json:"from,omitempty" jsonschema:"the volume or the host's folder its data was read from"`
	}
	type snapshotFile struct {
		Path  string `json:"path"`
		Bytes int    `json:"bytes"`
	}
	type snapshotGetOut struct {
		Destination string           `json:"destination"`
		Snapshot    string           `json:"snapshot"`
		Taken       string           `json:"taken,omitempty"`
		Of          string           `json:"of"`
		Type        string           `json:"type"`
		Env         string           `json:"env,omitempty"          jsonschema:"the environment (Docker host) it was taken on"`
		Volumes     []snapshotVolume `json:"volumes"                jsonschema:"the volumes whose data it holds, each under /volumes and its name"`
		Image       string           `json:"image,omitempty"        jsonschema:"for a container: the image it ran"`
		StackFiles  []snapshotFile   `json:"stack_files,omitempty"  jsonschema:"for a stack: the compose file and what lay beside it, which it holds as well"`
		SecretNames []string         `json:"secret_names,omitempty" jsonschema:"for a stack: the names of its secrets, whose values are in the snapshot and are never given"`
		Note        string           `json:"note,omitempty"`
	}
	add(r, readTool, &mcp.Tool{
		Name:        "backup_snapshot_get",
		Description: "Describe one snapshot: what it is of, when and on which Docker host it was taken, the volumes whose data it holds, and for a stack the files that were taken with it. It reads what Dockhand wrote down of the container or stack at the time, with its variables, labels and secrets left out. backup_snapshot_files looks at the data itself, and backup_restore with dry_run says what putting it back would write.",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in snapshotArg) (*mcp.CallToolResult, snapshotGetOut, error) {
		destination, id, err := r.snapshotAt(ctx, in)
		if err != nil {
			return nil, snapshotGetOut{}, err
		}
		out := snapshotGetOut{Destination: destination.Name, Snapshot: shortSnapshot(id), Volumes: []snapshotVolume{}}
		got, err := r.dock.GetBackupSnapshotsByIdMetadata(ctx, id, dockhand.GetBackupSnapshotsByIdMetadataOptions{DestinationId: destination.Id})
		if err != nil {
			return nil, out, fmt.Errorf("the snapshot %s could not be read: %w", id, refusal(err))
		}
		kept := got.Answer
		if kept.Error != "" {
			return nil, out, fmt.Errorf("the snapshot %s could not be read from %s: %s", id, destination.Name, plainly(kept.Error))
		}
		envs, err := r.environments(ctx)
		if err != nil {
			return nil, out, err
		}
		out.Of, out.Type, out.Taken = kept.TargetName, kept.Type, when(kept.BackupTime)
		if kept.EnvironmentId != 0 {
			i := slices.IndexFunc(envs, func(e environment) bool { return e.ID == kept.EnvironmentId })
			out.Env = nameOr(i, int(kept.EnvironmentId), func(i int) string { return envs[i].Name })
		}
		for _, volume := range kept.Volumes {
			out.Volumes = append(out.Volumes, snapshotVolume{Name: volume.Key, Kind: volume.Type, From: volume.Source})
		}
		if config, isRecord := kept.Container["Config"].(map[string]any); isRecord {
			out.Image = textOf(config["Image"])
		}
		if stack := kept.Stack; stack != nil {
			for _, file := range stack.FileList {
				out.StackFiles = append(out.StackFiles, snapshotFile{Path: file.Path, Bytes: file.Bytes})
			}
			out.SecretNames = stack.SecretKeys
		}

		return nil, out, nil
	})

	type snapshotFilesIn struct {
		snapshotArg

		Path string `json:"path,omitempty" jsonschema:"the folder inside the snapshot to list; left out, its top, which holds volumes, with a folder for each volume, and metadata"`
	}
	type snapshotEntry struct {
		Name        string `json:"name"`
		Type        string `json:"type"                  jsonschema:"dir or file"`
		Bytes       int    `json:"bytes,omitempty"`
		Permissions string `json:"permissions,omitempty"`
		Modified    string `json:"modified,omitempty"`
	}
	type snapshotFilesOut struct {
		Destination string          `json:"destination"`
		Snapshot    string          `json:"snapshot"`
		Path        string          `json:"path"`
		Entries     []snapshotEntry `json:"entries"`
		Total       int             `json:"total"`
		Note        string          `json:"note,omitempty"`
	}
	add(r, readTool, &mcp.Tool{
		Name:        "backup_snapshot_files",
		Description: "List one folder inside a snapshot, entry by entry: how to see what a backup really holds before it is needed. The data of each volume is under /volumes and the volume's name; a stack's own files are under /volumes/__dockhand_stackdir__. A path the snapshot does not have is answered as an empty folder, which Dockhand does not tell from a folder with nothing in it.",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in snapshotFilesIn) (*mcp.CallToolResult, snapshotFilesOut, error) {
		where, err := snapshotPath(in.Path)
		if err != nil {
			return nil, snapshotFilesOut{}, err
		}
		destination, id, err := r.snapshotAt(ctx, in.snapshotArg)
		if err != nil {
			return nil, snapshotFilesOut{}, err
		}
		out := snapshotFilesOut{Destination: destination.Name, Snapshot: shortSnapshot(id), Path: where, Entries: []snapshotEntry{}}
		listed, err := r.dock.GetBackupSnapshotsByIdBrowse(ctx, id, dockhand.GetBackupSnapshotsByIdBrowseOptions{DestinationId: destination.Id, Path: where})
		if err != nil {
			return nil, out, fmt.Errorf("the snapshot %s could not be looked into: %w", id, refusal(err))
		}
		if listed.Answer.Error != "" {
			return nil, out, fmt.Errorf("the snapshot %s could not be looked into in %s: %s", id, destination.Name, plainly(listed.Answer.Error))
		}
		for _, entry := range listed.Answer.Entries {
			out.Entries = append(out.Entries, snapshotEntry{Name: entry.Name, Type: entry.Type, Bytes: entry.Size, Permissions: entry.Permissions, Modified: when(entry.Mtime)})
		}
		slices.SortFunc(out.Entries, func(a, b snapshotEntry) int { return cmp.Or(cmp.Compare(a.Type, b.Type), cmp.Compare(a.Name, b.Name)) })
		if out.Total = len(out.Entries); out.Total == 0 {
			out.Note = "nothing is there: an empty folder, or a path the snapshot does not have, which read alike"
		}

		return nil, out, nil
	})

	type snapshotReadIn struct {
		snapshotArg
		revealArg

		Path string `json:"path" jsonschema:"the file inside the snapshot, as backup_snapshot_files gives it: /volumes, the volume's name, and the path in it"`
	}
	type snapshotReadOut struct {
		Destination string `json:"destination"`
		Snapshot    string `json:"snapshot"`
		Path        string `json:"path"`
		Content     string `json:"content"`
		Bytes       int    `json:"bytes"`
		Hidden      int    `json:"hidden,omitempty" jsonschema:"how many secrets in the text are hidden"`
		Note        string `json:"note,omitempty"`
	}
	add(r, readTool, &mcp.Tool{
		Name:        "backup_snapshot_read",
		Description: "Read one text file out of a snapshot, as it was when the snapshot was taken: a configuration file as it stood last week, say, without restoring anything. Up to 1 MB is read. Passwords, tokens and keys in the text are hidden, told by the names beside them, unless reveal_secrets asks. A folder is not read: backup_snapshot_files lists one and backup_snapshot_download fetches one.",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in snapshotReadIn) (*mcp.CallToolResult, snapshotReadOut, error) {
		if strings.TrimSpace(in.Path) == "" {
			return nil, snapshotReadOut{}, errors.New("path is required: the file inside the snapshot")
		}
		where, err := snapshotPath(in.Path)
		if err != nil {
			return nil, snapshotReadOut{}, err
		}
		destination, id, err := r.snapshotAt(ctx, in.snapshotArg)
		if err != nil {
			return nil, snapshotReadOut{}, err
		}
		out := snapshotReadOut{Destination: destination.Name, Snapshot: shortSnapshot(id), Path: where}
		entry, err := r.snapshotEntry(ctx, destination, id, where)
		if err != nil {
			return nil, out, err
		}
		switch {
		case entry.Type != "file":
			return nil, out, fmt.Errorf("%s is a folder in the snapshot %s: backup_snapshot_files lists it", where, id)
		case entry.Size > maxSnapshotRead:
			return nil, out, fmt.Errorf("%s is %d bytes, more than the 1 MB that is read of a file: backup_snapshot_download fetches it whole", where, entry.Size)
		default:
		}

		// asked for with no download, Dockhand answers the file as text inside JSON, with what it wrote down of a container's secrets taken out
		got, err := r.dock.GetBackupSnapshotsByIdDump(ctx, id, dockhand.GetBackupSnapshotsByIdDumpOptions{DestinationId: destination.Id, Path: where})
		if err != nil {
			return nil, out, fmt.Errorf("%s could not be read out of the snapshot %s: %w", where, id, refusal(err))
		}
		raw, err := got.Bytes(4 * maxSnapshotRead)
		if err != nil {
			return nil, out, fmt.Errorf("%s could not be read out of the snapshot %s: %w", where, id, err)
		}
		var read struct{ Content, Error string }
		if err := json.Unmarshal(raw, &read); err != nil {
			return nil, out, fmt.Errorf("%s was answered in a form that could not be read: %w", where, err)
		}
		if read.Error != "" {
			return nil, out, fmt.Errorf("%s could not be read out of the snapshot %s: %s", where, id, plainly(read.Error))
		}
		out.Content, out.Bytes = read.Content, len(read.Content)
		if !in.RevealSecrets {
			if out.Content, out.Hidden = maskText(out.Content); out.Hidden > 0 {
				out.Note = hiddenNote(out.Hidden, "told by the names beside them")
			}
		}

		return nil, out, nil
	})

	type snapshotDiffIn struct {
		Destination any    `json:"destination,omitempty" jsonschema:"the backup destination both snapshots are in, by its name or number; or give backup in its place"`
		Backup      any    `json:"backup,omitempty"      jsonschema:"in place of destination: a backup whose destination is meant"`
		From        string `json:"from"                  jsonschema:"the earlier snapshot, by its id"`
		To          string `json:"to"                    jsonschema:"the later snapshot, by its id"`
	}
	type snapshotDiffOut struct {
		Destination string   `json:"destination"`
		From        string   `json:"from"`
		To          string   `json:"to"`
		Added       []string `json:"added"          jsonschema:"paths the later snapshot has and the earlier does not"`
		Removed     []string `json:"removed"        jsonschema:"paths the earlier has and the later does not"`
		Changed     []string `json:"changed"        jsonschema:"files whose content differs"`
		Total       int      `json:"total"          jsonschema:"how many paths differ in all"`
		Note        string   `json:"note,omitempty"`
	}
	add(r, readTool, &mcp.Tool{
		Name:        "backup_snapshot_diff",
		Description: "Compare two snapshots in one backup destination and list the paths that were added, removed or changed between them: what changed in a volume between two nights, or whether a snapshot holds what was thought lost. The first hundred paths of each kind are listed, with the count of all. Dockhand's own record under /metadata differs between any two snapshots, since it holds when each was taken.",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in snapshotDiffIn) (*mcp.CallToolResult, snapshotDiffOut, error) {
		from, to := strings.TrimSpace(in.From), strings.TrimSpace(in.To)
		if from == "" || to == "" {
			return nil, snapshotDiffOut{}, errors.New("from and to are required: the two snapshots to compare, the earlier and the later")
		}
		destination, err := r.destinationOf(ctx, in.Destination, in.Backup)
		if err != nil {
			return nil, snapshotDiffOut{}, err
		}
		out := snapshotDiffOut{Destination: destination.Name, From: shortSnapshot(from), To: shortSnapshot(to), Added: []string{}, Removed: []string{}, Changed: []string{}}
		compared, err := r.dock.GetBackupSnapshotsDiff(ctx, dockhand.GetBackupSnapshotsDiffOptions{DestinationId: destination.Id, SnapshotA: from, SnapshotB: to})
		if err != nil {
			return nil, out, fmt.Errorf("the snapshots %s and %s could not be compared: %w", from, to, refusal(err))
		}
		differs := compared.Answer
		if differs.Error != "" {
			return nil, out, fmt.Errorf("the snapshots %s and %s could not be compared in %s: %s", from, to, destination.Name, plainly(differs.Error))
		}
		out.Total = len(differs.Added) + len(differs.Removed) + len(differs.Modified)
		cut := false
		for _, kind := range []struct {
			found []string
			shown *[]string
		}{{differs.Added, &out.Added}, {differs.Removed, &out.Removed}, {differs.Modified, &out.Changed}} {
			*kind.shown = append(*kind.shown, kind.found[:min(len(kind.found), changesShown)]...)
			cut = cut || len(kind.found) > changesShown
		}
		if cut {
			out.Note = fmt.Sprintf("the first %d paths of a kind are listed, of %d that differ in all", changesShown, out.Total)
		}

		return nil, out, nil
	})

	type snapshotDownloadIn struct {
		snapshotArg

		Path string `json:"path" jsonschema:"the file or folder inside the snapshot to fetch, under /volumes: /volumes and a volume's name for the whole of one volume"`
		To   string `json:"to"   jsonschema:"where on this machine to write it: a file that is not there yet, or a folder that is, which takes it under its own name. A folder of the snapshot is written as a tar archive"`
	}
	type snapshotDownloadOut struct {
		Destination string `json:"destination"`
		Snapshot    string `json:"snapshot"`
		Path        string `json:"path"`
		Archive     bool   `json:"archive"        jsonschema:"what was written is a tar archive of a folder, and not one file"`
		Saved       string `json:"saved"          jsonschema:"where on this machine it was written"`
		Bytes       int64  `json:"bytes"`
		Note        string `json:"note,omitempty"`
	}
	add(r, readTool, &mcp.Tool{
		Name:        "backup_snapshot_download",
		Description: "Write a file or a folder out of a snapshot to a file on this machine, as it was when the snapshot was taken: one file as it is, a folder as a tar archive. It is how one thing is had back without restoring a whole volume over what is there. Only the data of the volumes is fetched, under /volumes: what Dockhand wrote down of the container or stack is read with backup_snapshot_get, which leaves its secrets out. Nothing on any host changes, and a file that is there already is never written over.",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in snapshotDownloadIn) (*mcp.CallToolResult, snapshotDownloadOut, error) {
		to := strings.TrimSpace(in.To)
		switch {
		case strings.TrimSpace(in.Path) == "":
			return nil, snapshotDownloadOut{}, errors.New("path is required: the file or folder inside the snapshot, under /volumes")
		case to == "":
			return nil, snapshotDownloadOut{}, errors.New("to is required: where on this machine to write it")
		default:
		}
		where, err := snapshotPath(in.Path)
		if err != nil {
			return nil, snapshotDownloadOut{}, err
		}
		if !strings.HasPrefix(where, snapshotVolumes+"/") {
			return nil, snapshotDownloadOut{}, fmt.Errorf("path is %s: only what is under %s, the data of a volume, is fetched. What is under %s is Dockhand's record of the container or stack, which backup_snapshot_get reads with its secrets left out", where, snapshotVolumes, snapshotRecord)
		}
		destination, id, err := r.snapshotAt(ctx, in.snapshotArg)
		if err != nil {
			return nil, snapshotDownloadOut{}, err
		}
		out := snapshotDownloadOut{Destination: destination.Name, Snapshot: shortSnapshot(id), Path: where}
		entry, err := r.snapshotEntry(ctx, destination, id, where)
		if err != nil {
			return nil, out, err
		}
		out.Archive = entry.Type != "file"

		// a folder that is there takes it under its own name
		if info, statErr := os.Stat(to); statErr == nil && info.IsDir() {
			name := fileNameOf(path.Base(where))
			if out.Archive {
				name += ".tar"
			}
			to = filepath.Join(to, name)
		}
		got, err := r.dock.GetBackupSnapshotsByIdDump(ctx, id, dockhand.GetBackupSnapshotsByIdDumpOptions{DestinationId: destination.Id, Path: where, Download: "1"})
		if got.HttpResponse != nil && got.HttpResponse.Body != nil {
			defer func() { _ = got.HttpResponse.Body.Close() }()
		}
		if err != nil {
			return nil, out, fmt.Errorf("%s could not be fetched out of the snapshot %s: %w", where, id, refusal(err))
		}
		if out.Bytes, err = savedHere(to, got.HttpResponse.Body); err != nil {
			return nil, out, fmt.Errorf("%s of the snapshot %s: %w", where, id, err)
		}
		out.Saved = to

		return nil, out, nil
	})

	type snapshotDeleteIn struct {
		Destination any      `json:"destination,omitempty" jsonschema:"the backup destination the snapshots are in, by its name or number; or give backup in its place"`
		Backup      any      `json:"backup,omitempty"      jsonschema:"in place of destination: a backup whose destination is meant"`
		Snapshots   []string `json:"snapshots"             jsonschema:"the snapshots to remove, each by its id as backup_snapshot_list gives it"`
		Confirm     bool     `json:"confirm,omitempty"     jsonschema:"remove them: without this the answer says what would go and nothing is removed"`
	}
	type snapshotDeleteOut struct {
		Destination string   `json:"destination"`
		WouldGo     []string `json:"would_go,omitempty" jsonschema:"each snapshot that would be removed, with when it was taken and what of; absent once they are removed"`
		Removed     []string `json:"removed"            jsonschema:"the snapshots that were removed, by id"`
		Skipped     []string `json:"skipped,omitempty"  jsonschema:"the ones Dockhand left: not this Dockhand's own, or no longer there"`
		Note        string   `json:"note,omitempty"`
	}
	add(r, deleteTool, &mcp.Tool{
		Name:        "backup_snapshot_delete",
		Description: "Remove snapshots from a backup destination for good: what each held of a volume at that moment cannot be had back, and the space it alone used is freed. Each is looked up first, and one the destination does not hold, or that another Dockhand took, is refused before anything is removed. Without confirm the answer says which would go, with when each was taken and what of, and removes nothing. A backup's own keep removes old snapshots after each run without this.",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in snapshotDeleteIn) (*mcp.CallToolResult, snapshotDeleteOut, error) {
		if len(in.Snapshots) == 0 {
			return nil, snapshotDeleteOut{}, errors.New("snapshots is required: the ids of the snapshots to remove")
		}
		destination, err := r.destinationOf(ctx, in.Destination, in.Backup)
		if err != nil {
			return nil, snapshotDeleteOut{}, err
		}
		out := snapshotDeleteOut{Destination: destination.Name, Removed: []string{}}
		listed, err := r.snapshotsIn(ctx, destination)
		if err != nil {
			return nil, out, err
		}
		envs, err := r.environments(ctx)
		if err != nil {
			return nil, out, err
		}
		instance, err := r.dock.GetBackupInstance(ctx)
		if err != nil {
			return nil, out, fmt.Errorf("which snapshots are this Dockhand's own could not be told, so nothing was removed: %w", refusal(err))
		}
		var going []string
		for _, given := range in.Snapshots {
			id := strings.TrimSpace(given)
			held := slices.IndexFunc(listed, func(s dockhand.GetBackupSnapshotsAnswer) bool { return id != "" && strings.HasPrefix(s.Id, id) })
			if held < 0 {
				return nil, out, fmt.Errorf("%s holds no snapshot %q, so nothing was removed: backup_snapshot_list gives the ones it holds", destination.Name, given)
			}
			row := snapshotRowOf(&listed[held], instance.Answer.InstanceId, envs)
			if !row.Ours {
				return nil, out, fmt.Errorf("the snapshot %s was taken by another Dockhand, which this one will not remove, so nothing was removed", row.ID)
			}
			if !slices.Contains(going, listed[held].Id) {
				going = append(going, listed[held].Id)
				out.WouldGo = append(out.WouldGo, fmt.Sprintf("%s, taken %s of the %s %s on %s", row.ID, row.Taken, row.Type, row.Of, row.Env))
			}
		}
		if !in.Confirm {
			out.Note = "nothing was removed: confirm removes " + several(len(going), "snapshot") + " for good"

			return nil, out, nil
		}

		removed, err := r.dock.PostBackupSnapshotsBatchDelete(ctx, dockhand.PostBackupSnapshotsBatchDeleteRequest{DestinationId: destination.Id, SnapshotIds: going})
		if err != nil {
			return nil, out, fmt.Errorf("the snapshots were not removed from %s: %w", destination.Name, refusal(err))
		}
		out.WouldGo = nil
		for _, id := range removed.Answer.Deleted {
			out.Removed = append(out.Removed, shortSnapshot(id))
		}
		for _, id := range removed.Answer.Skipped {
			out.Skipped = append(out.Skipped, shortSnapshot(id))
		}
		if len(out.Skipped) > 0 {
			out.Note = several(len(out.Skipped), "snapshot") + " Dockhand passed over and left where it was"
		}

		return nil, out, nil
	})

	type restoreIn struct {
		snapshotArg
		envArg
		dryRunArg
		waitArg

		InPlace   bool              `json:"in_place,omitempty"   jsonschema:"put the data back over what the volumes hold now, which is replaced whole: a file made since the snapshot is gone. It wants confirm, and --enable-delete on this server"`
		Confirm   bool              `json:"confirm,omitempty"    jsonschema:"with in_place: do it. Without this the answer says what would be written over and nothing is"`
		To        string            `json:"to,omitempty"         jsonschema:"in place of in_place: a folder on the Docker host to put the data in, each volume under volumes and its name. It is a path on the host's own disk, and is made when it is not there"`
		VolumesTo map[string]string `json:"volumes_to,omitempty" jsonschema:"in place of in_place and to: where each volume named goes, by the volume as the snapshot names it: the name of a volume there is none of yet, which is made, Dockhand refusing one that is there; or a path on the host that begins with /"`
		Volumes   []string          `json:"volumes,omitempty"    jsonschema:"only these of the snapshot's volumes; left out, every one, or with volumes_to the ones it names"`
		After     string            `json:"after,omitempty"      jsonschema:"what Dockhand does once the data is back: none (what is done when this is left out), start (start the container or stack the snapshot is of, in place), or recreate (make the container or stack again from what the snapshot recorded of it)"`
		Name      string            `json:"name,omitempty"       jsonschema:"with after: the container or stack to start or make; left out, the one the snapshot is of"`
	}
	type restoreOut struct {
		jobState

		Destination string          `json:"destination"`
		Snapshot    string          `json:"snapshot"`
		Of          string          `json:"of,omitempty"`
		Env         string          `json:"env,omitempty"         jsonschema:"the environment (Docker host) it is restored on"`
		Mode        string          `json:"mode"                  jsonschema:"in-place, over what is there, or new-location"`
		Targets     []restoreTarget `json:"targets,omitempty"     jsonschema:"where each volume is written, as Dockhand works it out before anything is"`
		StackFiles  string          `json:"stack_files,omitempty" jsonschema:"for a stack: the folder its compose file and what lay beside it are written to"`
		Unresolved  []string        `json:"unresolved,omitempty"  jsonschema:"the volumes Dockhand has nowhere to put, each with why"`
		Restored    []string        `json:"restored,omitempty"    jsonschema:"the volumes whose data was put back"`
		Status      string          `json:"status,omitempty"      jsonschema:"how it went, once it has ended: success, or warning when the data is back and what was to follow could not be done"`
		After       string          `json:"after,omitempty"       jsonschema:"what was done once the data was back, and why not when it could not be"`
		Run         int             `json:"run,omitempty"`
		DryRun      bool            `json:"dry_run,omitempty"     jsonschema:"nothing was restored: targets says what would be written"`
		Note        string          `json:"note,omitempty"`
	}
	add(r, writeTool, &mcp.Tool{
		Name:        "backup_restore",
		Description: "Put the data of a snapshot back. One of three ways is named: in_place writes it over the volumes it was taken from, which replaces everything in them and so wants confirm and --enable-delete; to writes it into a folder on the Docker host, beside what is there; volumes_to writes each volume into a volume or a folder named for it. With dry_run, and for in_place without confirm, nothing is written and the answer says where each volume would go and whether anything is there now. Dockhand restores in a helper container on the Docker host, and it is waited for as long as wait says and then answered as a job. Nothing is started or made afterwards unless after says so: left to itself Dockhand makes the container again after a restore to somewhere else, a second copy of it, which this never does unasked. A container that is running is not stopped first: stop it for data that must be whole.",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in restoreIn) (*mcp.CallToolResult, restoreOut, error) {
		to, after, target := strings.TrimSpace(in.To), cmp.Or(strings.TrimSpace(in.After), afterNothing), strings.TrimSpace(in.Name)
		ways := 0
		for _, named := range []bool{in.InPlace, to != "", len(in.VolumesTo) > 0} {
			if named {
				ways++
			}
		}
		switch {
		case ways != 1:
			return nil, restoreOut{}, errors.New("one of in_place, to and volumes_to is required, and only one: over what is there, into a folder on the host, or each volume to a place of its own")
		case to != "" && !strings.HasPrefix(to, "/"):
			return nil, restoreOut{}, fmt.Errorf("to is %q: it is a folder on the Docker host, by its whole path", in.To)
		case !slices.Contains([]string{afterNothing, afterStart, afterRecreate}, after):
			return nil, restoreOut{}, fmt.Errorf("after is %q: it is none, start or recreate, and any other word Dockhand reads as its own choice", in.After)
		case in.InPlace && in.Confirm && !in.DryRun && !r.opts.EnableDelete:
			return nil, restoreOut{}, errors.New("in_place replaces what the volumes hold, and nothing is removed without --enable-delete (DOCKHAND_ENABLE_DELETE=true) on this server: to and volumes_to restore beside what is there")
		default:
		}
		patience, err := in.patience()
		if err != nil {
			return nil, restoreOut{}, err
		}
		destination, id, err := r.snapshotAt(ctx, in.snapshotArg)
		if err != nil {
			return nil, restoreOut{}, err
		}
		out := restoreOut{Destination: destination.Name, Snapshot: shortSnapshot(id), Mode: restoreAway, DryRun: in.DryRun}
		if in.InPlace {
			out.Mode = restoreInPlace
		}

		// what the snapshot is of, and where it was taken, which is where it goes back unless another host is named
		kept, err := r.dock.GetBackupSnapshotsByIdMetadata(ctx, id, dockhand.GetBackupSnapshotsByIdMetadataOptions{DestinationId: destination.Id})
		if err != nil {
			return nil, out, fmt.Errorf("the snapshot %s could not be read, so nothing was restored: %w", id, refusal(err))
		}
		if kept.Answer.Error != "" {
			return nil, out, fmt.Errorf("the snapshot %s could not be read from %s, so nothing was restored: %s", id, destination.Name, plainly(kept.Answer.Error))
		}
		envs, err := r.environments(ctx)
		if err != nil {
			return nil, out, err
		}
		env := environment{ID: kept.Answer.EnvironmentId}
		if named, err := in.named(); err != nil {
			return nil, out, err
		} else if named != "" {
			if env, err = r.one(ctx, in.envArg); err != nil {
				return nil, out, err
			}
		}
		i := slices.IndexFunc(envs, func(e environment) bool { return e.ID == env.ID })
		if i < 0 {
			return nil, out, fmt.Errorf("the snapshot %s was taken on an environment that is gone: env names the Docker host to restore it on", id)
		}
		env = envs[i]
		out.Of, out.Env = kept.Answer.TargetName, env.Name
		target = cmp.Or(target, kept.Answer.TargetName)

		volumes := in.Volumes
		held := make([]string, 0, len(kept.Answer.Volumes))
		for _, volume := range kept.Answer.Volumes {
			held = append(held, volume.Key)
		}
		var where []dockhand.PostBackupRestoreRequestVolumeDestinations
		var asked []dockhand.PostBackupRestorePreviewRequestVolumeDestinations
		for _, volume := range slices.Sorted(maps.Keys(in.VolumesTo)) {
			put := strings.TrimSpace(in.VolumesTo[volume])
			if put == "" {
				return nil, out, fmt.Errorf("volumes_to names %s and nowhere to put it", volume)
			}
			kind := "volume"
			if strings.HasPrefix(put, "/") {
				kind = "path"
			}
			where = append(where, dockhand.PostBackupRestoreRequestVolumeDestinations{Volume: volume, Kind: kind, Target: put})
			asked = append(asked, dockhand.PostBackupRestorePreviewRequestVolumeDestinations{Volume: volume, Target: put})
			if len(in.Volumes) == 0 {
				volumes = append(volumes, volume)
			}
		}
		for _, volume := range volumes {
			if !slices.Contains(held, volume) {
				return nil, out, fmt.Errorf("the snapshot %s holds no volume %s: it holds %s", id, volume, cmp.Or(strings.Join(held, ", "), "none"))
			}
		}

		preview, err := r.dock.PostBackupRestorePreview(ctx, dockhand.PostBackupRestorePreviewRequest{DestinationId: destination.Id, SnapshotId: id, IncludeTargets: new(true), Mode: out.Mode, TargetType: kept.Answer.Type, TargetName: target, TargetPath: to, EnvironmentId: env.ID, TargetEnvId: env.ID, Volumes: volumes, VolumeDestinations: asked})
		if err != nil {
			return nil, out, fmt.Errorf("where the snapshot %s would be written could not be worked out, so nothing was restored: %w", id, refusal(err))
		}
		if preview.Answer.Error != "" {
			return nil, out, fmt.Errorf("where the snapshot %s would be written could not be worked out, so nothing was restored: %s", id, plainly(preview.Answer.Error))
		}
		if targets := preview.Answer.Targets; targets != nil {
			for _, volume := range targets.Volumes {
				out.Targets = append(out.Targets, restoreTarget{Volume: volume.Key, To: volume.Target, Kind: volume.Type, There: volume.HasData})
			}
			for _, volume := range targets.Unresolved {
				out.Unresolved = append(out.Unresolved, volume.Key+": "+volume.Reason)
			}
			if targets.StackFiles != nil && targets.StackFiles.WillWrite {
				out.StackFiles = targets.StackFiles.TargetDir
			}
		}
		if len(out.Unresolved) > 0 {
			return nil, out, fmt.Errorf("Dockhand has nowhere to put %s of the snapshot %s, so nothing was restored: %s", several(len(out.Unresolved), "volume"), id, strings.Join(out.Unresolved, "; ")) //nolint:staticcheck // ST1005: the sentence begins with the server's name
		}
		if in.DryRun || (in.InPlace && !in.Confirm) {
			out.DryRun = true
			out.Note = "nothing was restored: targets says what would be written"
			if in.InPlace && !in.DryRun {
				out.Note = "nothing was restored: confirm writes the snapshot over what targets says is there now, all of which is replaced"
			}

			return nil, out, nil
		}

		request := dockhand.PostBackupRestoreRequest{DestinationId: destination.Id, SnapshotId: id, Mode: out.Mode, TargetType: kept.Answer.Type, TargetName: target, TargetPath: to, EnvironmentId: env.ID, Volumes: volumes, VolumeDestinations: where, PostRestore: after}
		if in.InPlace {
			request.ConfirmOverwrite = new(true)
		}
		started, err := r.dock.PostBackupRestore(ctx, request, dockhand.PostBackupRestoreOptions{Accept: dockhand.AcceptJob})
		if err != nil {
			return nil, out, fmt.Errorf("the restore of the snapshot %s could not be started: %w", id, refusal(err))
		}
		outcome, state, err := settle(ctx, r, started.Answer.JobId, started.Answer, patience)
		out.jobState = state
		if err != nil {
			return nil, out, fmt.Errorf("the snapshot %s was not restored on %s: %w", id, env.Name, err)
		}
		if state.Running {
			out.Note = "still restoring after " + seconds(patience) + ": job_get says how it ends, and backup_stop with restore stops it"

			return nil, out, nil
		}
		if outcome.Status == jobFailed || outcome.Error != "" {
			return nil, out, fmt.Errorf("the snapshot %s was not restored on %s: %s", id, env.Name, cmp.Or(plainly(outcome.Error), "Dockhand gave no reason"))
		}
		out.Status, out.Restored, out.Run = outcome.Status, outcome.RestoredVolumes, outcome.ExecutionId
		if did := outcome.PostRestore; did != nil && did.Action != afterNothing {
			out.After = did.Action
			if !did.Ok {
				out.After = did.Action + " could not be done: " + cmp.Or(did.Error, "Dockhand gives no reason")
			}
		}

		return nil, out, nil
	})
}

// snapshotEntry is the file or folder a path of a snapshot is, found in the folder that holds it, or the error that nothing is there: Dockhand
// answers a path a snapshot does not have as an empty folder, and fetching one as a failure with restic's own words.
func (r *registry) snapshotEntry(ctx context.Context, destination *dockhand.GetBackupDestinationsAnswer, id, where string) (*dockhand.GetBackupSnapshotsByIdBrowseAnswerEntries, error) {
	listed, err := r.dock.GetBackupSnapshotsByIdBrowse(ctx, id, dockhand.GetBackupSnapshotsByIdBrowseOptions{DestinationId: destination.Id, Path: path.Dir(where)})
	switch {
	case client.IsNotFound(err):
		return nil, errBackupsOff
	case err != nil:
		return nil, fmt.Errorf("the snapshot %s could not be looked into: %w", id, refusal(err))
	case listed.Answer.Error != "":
		return nil, fmt.Errorf("the snapshot %s could not be looked into in %s: %s", id, destination.Name, plainly(listed.Answer.Error))
	default:
	}
	i := slices.IndexFunc(listed.Answer.Entries, func(e dockhand.GetBackupSnapshotsByIdBrowseAnswerEntries) bool { return e.Name == path.Base(where) })
	if i < 0 {
		return nil, fmt.Errorf("the snapshot %s has nothing at %s: backup_snapshot_files lists what it has", id, where)
	}

	return &listed.Answer.Entries[i], nil
}
