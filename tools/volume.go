package tools

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"net/http"
	"path"
	"slices"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/katbyte/dockhand-mcp/sdk/dockhand"
	"github.com/katbyte/pandorest/client"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// defaultVolumes is how many volumes volume_list lists unless told.
const defaultVolumes = 200

// volumeRow is one volume of a host as volume_list answers with it.
type volumeRow struct {
	Env     string   `json:"env"               jsonschema:"the environment (Docker host) the volume is on"`
	Name    string   `json:"name"`
	Driver  string   `json:"driver"            jsonschema:"what keeps the volume: local for one on the host's own disk"`
	Stack   string   `json:"stack,omitempty"   jsonschema:"the compose stack (project) that made the volume; absent for one made on its own"`
	Created string   `json:"created,omitempty" jsonschema:"when the volume was made, in UTC"`
	UsedBy  []string `json:"used_by"           jsonschema:"the containers that mount the volume, running or not; none for a volume nothing uses"`
}

// volumeRowOf is a volume of an environment as the tools give one.
func volumeRowOf(env string, v *dockhand.GetVolumesResponse) volumeRow {
	row := volumeRow{Env: env, Name: v.Name, Driver: v.Driver, Stack: v.Labels[labelStack], Created: v.Created, UsedBy: make([]string, 0, len(v.UsedBy))}
	for i := range v.UsedBy {
		row.UsedBy = append(row.UsedBy, v.UsedBy[i].ContainerName)
	}
	slices.Sort(row.UsedBy)

	return row
}

// volumesOf is every volume on an environment.
func (r *registry) volumesOf(ctx context.Context, env environment) ([]dockhand.GetVolumesResponse, error) {
	listed, err := r.dock.GetVolumes(ctx, dockhand.GetVolumesOperationOptions{Env: &env.ID})
	if err != nil {
		return nil, refusal(err)
	}

	return listed.Model, nil
}

// volumeArg is the arguments of a tool about one volume: which, and where to look for it.
type volumeArg struct {
	envArg

	Volume string `json:"volume" jsonschema:"the volume, by its name, as volume_list gives them"`
}

// locatedVolume is one volume and the environment it is on.
type locatedVolume struct {
	Env    environment
	Volume dockhand.GetVolumesResponse
}

// findVolume is the one volume a call names, looked for on the environment given or on every one, by its name in full: a volume holds data, and one
// is not acted on for having a name like the one asked for. The same name on several hosts is not guessed at: the call is refused, naming them, until
// it says which.
func (r *registry) findVolume(ctx context.Context, in volumeArg) (locatedVolume, error) {
	want := strings.TrimSpace(in.Volume)
	if want == "" {
		return locatedVolume{}, errors.New("volume is required: the volume's name, as volume_list gives them")
	}

	envs, err := r.pick(ctx, in.envArg)
	if err != nil {
		return locatedVolume{}, err
	}

	var matches []locatedVolume
	var failed []string
	for _, answer := range ask(ctx, envs, r.volumesOf) {
		if answer.Err != nil {
			failed = append(failed, answer.Env.Name+": "+answer.Err.Error())

			continue
		}
		for i := range answer.Value {
			if answer.Value[i].Name == want {
				matches = append(matches, locatedVolume{Env: answer.Env, Volume: answer.Value[i]})
			}
		}
	}

	names := make([]string, 0, len(envs))
	for _, e := range envs {
		names = append(names, e.Name)
	}
	switch {
	case len(matches) == 1:
		return matches[0], nil
	case len(matches) > 1:
		at := make([]string, 0, len(matches))
		for i := range matches {
			at = append(at, matches[i].Env.Name)
		}

		return locatedVolume{}, fmt.Errorf("there is a volume named %q on each of %s: say which with env", want, strings.Join(at, ", "))
	case len(failed) > 0:
		return locatedVolume{}, fmt.Errorf("no volume named %q was found, and not every environment could be asked: %s", want, strings.Join(failed, "; "))
	default:
		return locatedVolume{}, fmt.Errorf("no volume is named %q on %s: volume_list gives the ones there are", want, strings.Join(names, ", "))
	}
}

// mountersOf is the containers of an environment that mount a volume, by name, each with its state, which the volume's own record does not carry.
func (r *registry) mountersOf(ctx context.Context, at *locatedVolume) []string {
	states := map[string]string{}
	if containers, err := r.containersOf(ctx, at.Env); err == nil {
		for i := range containers {
			states[containers[i].Id] = containers[i].State
		}
	}

	users := make([]string, 0, len(at.Volume.UsedBy))
	for i := range at.Volume.UsedBy {
		user := at.Volume.UsedBy[i].ContainerName
		if state := states[at.Volume.UsedBy[i].ContainerId]; state != "" {
			user += " (" + state + ")"
		}
		users = append(users, user)
	}
	slices.Sort(users)

	return users
}

// maskedLabels is labels, or what a driver was told, as they are shown: a value under a name that is a secret's hidden whole, a secret inside a value
// (a share's password among its mount options) hidden where it is, and how many were. Asked to reveal, they are given as they are.
func maskedLabels(labels map[string]string, reveal bool) (shown map[string]string, hidden int) {
	if len(labels) == 0 || reveal {
		return labels, 0
	}

	shown = make(map[string]string, len(labels))
	for name, value := range labels {
		masked, wasHidden := maskValue(name, value)
		if wasHidden {
			hidden++
		} else {
			var inside int
			masked, inside = maskText(value)
			hidden += inside
		}
		shown[name] = masked
	}

	return shown, hidden
}

//nolint:gocyclo,maintidx // the volume tools registered in one function, each a handler of its own: the count is theirs added up
func registerVolumeTools(r *registry) {
	type volumeListIn struct {
		envArg

		Name   string `json:"name,omitempty"   jsonschema:"only volumes whose name contains this, whatever its case"`
		Stack  string `json:"stack,omitempty"  jsonschema:"only the volumes this compose stack (project) made, by its name"`
		Unused bool   `json:"unused,omitempty" jsonschema:"only volumes no container mounts: what volume_prune would remove"`
		Limit  int    `json:"limit,omitempty"  jsonschema:"the most volumes to list, 200 unless given; the counts are of every volume that matched"`
	}
	type volumeListOut struct {
		Volumes []volumeRow  `json:"volumes"          jsonschema:"the volumes that matched, by environment and then by name"`
		Total   int          `json:"total"            jsonschema:"how many volumes matched, on every environment asked; more than are listed when the list was cut at the limit"`
		Unused  int          `json:"unused"           jsonschema:"how many of those no container mounts"`
		Failed  []envFailure `json:"failed,omitempty" jsonschema:"the environments that could not be asked, each with why; their volumes are in no count"`
		Note    string       `json:"note,omitempty"`
	}
	add(r, readTool, &mcp.Tool{
		Name:        "volume_list",
		Description: "List the Docker volumes on every Docker host (environment) the Dockhand manages, or on one: each with its host, name, what keeps it, the compose stack that made it and the containers that mount it, running or not. Narrow it to one environment, a part of a name or one stack's volumes, or ask for unused alone, which is every volume no container mounts and so what a prune would remove. Docker does not say how much a volume holds, so no size is given. A host that cannot be asked is named with the reason beside what the others answered.",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in volumeListIn) (*mcp.CallToolResult, volumeListOut, error) {
		if in.Limit < 0 {
			return nil, volumeListOut{}, fmt.Errorf("limit is %d: it is how many volumes to list at most, a number above 0, or left out for %d", in.Limit, defaultVolumes)
		}
		limit := cmp.Or(in.Limit, defaultVolumes)

		envs, err := r.pick(ctx, in.envArg)
		if err != nil {
			return nil, volumeListOut{}, err
		}

		out := volumeListOut{Volumes: []volumeRow{}}
		for _, answer := range ask(ctx, envs, r.volumesOf) {
			if answer.Err != nil {
				out.Failed = append(out.Failed, envFailure{Env: answer.Env.Name, Error: answer.Err.Error()})

				continue
			}

			rows := make([]volumeRow, 0, len(answer.Value))
			for i := range answer.Value {
				row := volumeRowOf(answer.Env.Name, &answer.Value[i])
				if (in.Unused && len(row.UsedBy) > 0) || (in.Stack != "" && row.Stack != in.Stack) || (in.Name != "" && !strings.Contains(strings.ToLower(row.Name), strings.ToLower(in.Name))) {
					continue
				}

				out.Total++
				if len(row.UsedBy) == 0 {
					out.Unused++
				}
				rows = append(rows, row)
			}
			slices.SortFunc(rows, func(a, b volumeRow) int { return cmp.Compare(a.Name, b.Name) })

			out.Volumes = append(out.Volumes, rows[:min(len(rows), max(0, limit-len(out.Volumes)))]...)
		}

		if len(out.Failed) == len(envs) {
			return nil, out, fmt.Errorf("no environment could be asked for its volumes: %s: %s", out.Failed[0].Env, out.Failed[0].Error)
		}
		if len(out.Failed) > 0 {
			out.Note = fmt.Sprintf("%d of the %d environments could not be asked, and their volumes are missing from this", len(out.Failed), len(envs))
		}
		if out.Total > len(out.Volumes) {
			out.Note = note(out.Note, fmt.Sprintf("%d volumes matched and the first %d are listed: narrow it by env, stack or name, or raise limit", out.Total, len(out.Volumes)))
		}

		return nil, out, nil
	})

	type volumeGetIn struct {
		volumeArg
		revealArg
	}
	type volumeGetOut struct {
		volumeRow

		Mountpoint string            `json:"mountpoint,omitempty" jsonschema:"where the volume's files are on the host itself"`
		Scope      string            `json:"scope,omitempty"      jsonschema:"local for a volume of this host alone, global for one a swarm shares"`
		Labels     map[string]string `json:"labels,omitempty"     jsonschema:"the labels the volume was made with"`
		Options    map[string]string `json:"options,omitempty"    jsonschema:"what the volume's driver was told when it was made: for a local volume that is a mount of something else, what and how"`
		Note       string            `json:"note,omitempty"`
	}
	add(r, readTool, &mcp.Tool{
		Name:        "volume_get",
		Description: "Describe one Docker volume on a host in full: what keeps it and where its files are on the host, when it was made and by which compose stack, its labels, what its driver was told when it was made (for a volume that is a mount of a network share, the share), and the containers that mount it, each with its state. Name it by its name in full; say which host with env when more than one has a volume of that name. A password among its labels or among what its driver was told, a network share's above all, is hidden unless reveal_secrets asks for it.",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in volumeGetIn) (*mcp.CallToolResult, volumeGetOut, error) {
		at, err := r.findVolume(ctx, in.volumeArg)
		if err != nil {
			return nil, volumeGetOut{}, err
		}

		out := volumeGetOut{volumeRow: volumeRowOf(at.Env.Name, &at.Volume), Mountpoint: at.Volume.Mountpoint, Scope: at.Volume.Scope}
		out.UsedBy = r.mountersOf(ctx, &at)
		labels, hidden := maskedLabels(at.Volume.Labels, in.RevealSecrets)
		out.Labels = labels

		// Docker's own record of the volume has what the list leaves out: what its driver was told
		if one, err := r.dock.GetVolumesByNameInspect(ctx, at.Volume.Name, dockhand.GetVolumesByNameInspectOperationOptions{Env: &at.Env.ID}); err != nil {
			out.Note = "what its driver was told could not be read: " + refusal(err).Error()
		} else {
			options, more := maskedLabels(one.Model.Options, in.RevealSecrets)
			out.Options, hidden = options, hidden+more
		}
		if hidden > 0 {
			out.Note = note(out.Note, hiddenNote(hidden, "told by the names beside them"))
		}

		return nil, out, nil
	})

	type volumeCreateIn struct {
		envArg
		dryRunArg

		Name   string            `json:"name"             jsonschema:"the new volume's name: letters, digits, dot, dash and underscore, starting with a letter or a digit"`
		Driver string            `json:"driver,omitempty" jsonschema:"what is to keep the volume; local, the host's own disk, unless said"`
		Labels map[string]string `json:"labels,omitempty" jsonschema:"labels to make the volume with, by name"`
	}
	type volumeCreateOut struct {
		Env     string     `json:"env"`
		Created bool       `json:"created"           jsonschema:"the volume was made, and is read back from the host"`
		Volume  *volumeRow `json:"volume,omitempty"  jsonschema:"the volume as the host now lists it; absent with dry_run"`
		DryRun  bool       `json:"dry_run,omitempty" jsonschema:"nothing was made"`
		Note    string     `json:"note,omitempty"`
	}
	add(r, writeTool, &mcp.Tool{
		Name:        "volume_create",
		Description: "Make an empty Docker volume on one Docker host (environment), as docker volume create does. It names one environment. A name the host already has a volume under is refused rather than answered as made, since Docker would hand back the one that is there with whatever it holds. Read back from the host.",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in volumeCreateIn) (*mcp.CallToolResult, volumeCreateOut, error) {
		name := strings.TrimSpace(in.Name)
		if !volumeName(name) {
			return nil, volumeCreateOut{}, fmt.Errorf("name is %q: a volume's name is letters, digits, dot, dash and underscore, two or more, starting with a letter or a digit", in.Name)
		}
		env, err := r.one(ctx, in.envArg)
		if err != nil {
			return nil, volumeCreateOut{}, err
		}

		out := volumeCreateOut{Env: env.Name, DryRun: in.DryRun}
		before, err := r.volumesOf(ctx, env)
		if err != nil {
			return nil, out, fmt.Errorf("the volumes of %s could not be listed, so nothing was made: %w", env.Name, err)
		}
		if slices.ContainsFunc(before, func(v dockhand.GetVolumesResponse) bool { return v.Name == name }) {
			return nil, out, fmt.Errorf("%s already has a volume named %s, which was left as it is: volume_get describes it", env.Name, name)
		}
		if in.DryRun {
			out.Note = fmt.Sprintf("nothing was made: without dry_run an empty volume named %s is made on %s", name, env.Name)

			return nil, out, nil
		}

		body := dockhand.PostVolumesRequest{Name: name, Driver: strings.TrimSpace(in.Driver)}
		if len(in.Labels) > 0 {
			body.Labels = make(map[string]any, len(in.Labels))
			for label, value := range in.Labels {
				body.Labels[label] = value
			}
		}
		made, err := r.dock.PostVolumes(ctx, body, dockhand.PostVolumesOperationOptions{Env: &env.ID})
		if err != nil {
			return nil, out, fmt.Errorf("the volume %s was not made on %s: %w", name, env.Name, refusal(err))
		}
		if !yes(made.Model.Success) {
			return nil, out, fmt.Errorf("making the volume %s was answered without its being said to have succeeded: volume_list says whether it is there", name)
		}

		after, err := r.volumesOf(ctx, env)
		if err != nil {
			out.Created = true
			out.Note = "it was made, and the volumes could not be listed afterwards: " + err.Error()

			return nil, out, nil //nolint:nilerr // the volume was made: that it could not be read back is said in the answer
		}
		for i := range after {
			if after[i].Name == name {
				row := volumeRowOf(env.Name, &after[i])
				out.Created, out.Volume = true, &row
			}
		}
		if !out.Created {
			return nil, out, fmt.Errorf("making the volume %s was answered as done, and %s does not list it", name, env.Name)
		}

		return nil, out, nil
	})

	type volumeCloneIn struct {
		volumeArg
		dryRunArg

		As string `json:"as" jsonschema:"the name of the new volume to copy it into: one the host has no volume under"`
	}
	type volumeCloneOut struct {
		Env    string     `json:"env"`
		From   string     `json:"from"              jsonschema:"the volume that was copied"`
		Cloned bool       `json:"cloned"            jsonschema:"the copy was made, and is read back from the host"`
		Volume *volumeRow `json:"volume,omitempty"  jsonschema:"the new volume as the host now lists it; absent with dry_run"`
		DryRun bool       `json:"dry_run,omitempty" jsonschema:"nothing was copied"`
		Note   string     `json:"note,omitempty"`
	}
	add(r, writeTool, &mcp.Tool{
		Name:        "volume_clone",
		Description: "Copy a Docker volume, files and all, into a new volume on the same host, made as the first was: a copy to try something on, or to keep before a change. The volume copied is untouched. A name the host already has a volume under is refused, since Dockhand would copy into what is there. The files are copied as they are at that moment, by a helper container Dockhand starts for it, so a database that is running may be copied mid-write: stop what writes to the volume first when the copy has to be sound. The call waits for the copy, and a very large volume can outlast the two minutes it waits, in which case the copy goes on and volume_list shows it. Read back from the host.",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in volumeCloneIn) (*mcp.CallToolResult, volumeCloneOut, error) {
		as := strings.TrimSpace(in.As)
		if !volumeName(as) {
			return nil, volumeCloneOut{}, fmt.Errorf("as is %q: a volume's name is letters, digits, dot, dash and underscore, two or more, starting with a letter or a digit", in.As)
		}
		at, err := r.findVolume(ctx, in.volumeArg)
		if err != nil {
			return nil, volumeCloneOut{}, err
		}

		out := volumeCloneOut{Env: at.Env.Name, From: at.Volume.Name, DryRun: in.DryRun}
		before, err := r.volumesOf(ctx, at.Env)
		if err != nil {
			return nil, out, fmt.Errorf("the volumes of %s could not be listed, so nothing was copied: %w", at.Env.Name, err)
		}
		if slices.ContainsFunc(before, func(v dockhand.GetVolumesResponse) bool { return v.Name == as }) {
			return nil, out, fmt.Errorf("%s already has a volume named %s, which was left as it is: a copy is made into a new volume", at.Env.Name, as)
		}
		if in.DryRun {
			out.Note = fmt.Sprintf("nothing was copied: without dry_run %s is copied into a new volume named %s on %s", out.From, as, out.Env)

			return nil, out, nil
		}

		copied, err := r.dock.PostVolumesByNameClone(ctx, at.Volume.Name, dockhand.PostVolumesByNameCloneRequest{Name: as}, dockhand.PostVolumesByNameCloneOperationOptions{Env: &at.Env.ID})
		if err != nil {
			return nil, out, fmt.Errorf("%s was not copied into %s: %w", out.From, as, refusal(err))
		}
		if !yes(copied.Model.Success) {
			return nil, out, fmt.Errorf("copying %s into %s was answered without its being said to have succeeded: volume_list says whether it is there", out.From, as)
		}

		after, err := r.volumesOf(ctx, at.Env)
		if err != nil {
			out.Cloned = true
			out.Note = "it was copied, and the volumes could not be listed afterwards: " + err.Error()

			return nil, out, nil //nolint:nilerr // the copy was made: that it could not be read back is said in the answer
		}
		for i := range after {
			if after[i].Name == as {
				row := volumeRowOf(at.Env.Name, &after[i])
				out.Cloned, out.Volume = true, &row
			}
		}
		if !out.Cloned {
			return nil, out, fmt.Errorf("copying %s into %s was answered as done, and %s does not list it", out.From, as, out.Env)
		}

		return nil, out, nil
	})

	type volumeEntry struct {
		Name     string `json:"name"`
		Type     string `json:"type"               jsonschema:"file, directory, or symlink"`
		Size     int    `json:"size"               jsonschema:"bytes; for a directory, what its own entry takes and not what is in it"`
		Modified string `json:"modified,omitempty" jsonschema:"when it was last changed, in the host's own time"`
		Mode     string `json:"mode,omitempty"     jsonschema:"who may read, write and run it: rw-r--r--"`
		Owner    string `json:"owner,omitempty"    jsonschema:"the user and group it belongs to, as the helper sees them: root:root"`
	}
	type volumeFilesIn struct {
		volumeArg

		Path string `json:"path,omitempty" jsonschema:"the directory in the volume to list, from the volume's top: /config/sites. Left out, the top"`
	}
	type volumeFilesOut struct {
		Env     string        `json:"env"`
		Volume  string        `json:"volume"`
		Path    string        `json:"path"           jsonschema:"the directory that was listed"`
		Entries []volumeEntry `json:"entries"        jsonschema:"what the directory holds, directories first and then by name"`
		Note    string        `json:"note,omitempty"`
	}
	add(r, writeTool, &mcp.Tool{
		Name:        "volume_files",
		Description: "List what is in a Docker volume, one directory at a time: each file and directory with its size, when it was changed, its permissions and its owner. Docker has no way to look inside a volume, so Dockhand starts a small helper container on the host with the volume mounted, read-only when a container is using it, and takes it away again when this is done. That is why this needs --enable-write although it changes nothing in the volume: the first time on a host, it has the host pull the busybox image for the helper, which stays there. volume_read reads one file.",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in volumeFilesIn) (*mcp.CallToolResult, volumeFilesOut, error) {
		at, err := r.findVolume(ctx, in.volumeArg)
		if err != nil {
			return nil, volumeFilesOut{}, err
		}
		// from the volume's top and never above it: Dockhand would list the helper's own files for a path that climbs out
		dir := path.Clean("/" + strings.TrimSpace(in.Path))
		out := volumeFilesOut{Env: at.Env.Name, Volume: at.Volume.Name, Path: dir, Entries: []volumeEntry{}}

		listed, err := r.dock.GetVolumesByNameBrowse(ctx, at.Volume.Name, dockhand.GetVolumesByNameBrowseOperationOptions{Env: &at.Env.ID, Path: dir})
		// the helper is wanted for this one look and no longer, however the look went
		unreleased := r.release(ctx, &at)
		switch {
		case client.IsNotFound(err):
			return nil, out, leftBehind(fmt.Errorf("the volume %s has no directory %s: list the one above it", out.Volume, dir), unreleased)
		case err != nil:
			return nil, out, leftBehind(fmt.Errorf("the volume %s could not be looked into: %w", out.Volume, refusal(err)), unreleased)
		default:
		}
		out.Note = unreleased

		for i := range listed.Model.Entries {
			e := &listed.Model.Entries[i]
			entry := volumeEntry{Name: e.Name, Type: e.Type, Size: e.Size, Modified: e.Modified, Mode: e.Permissions}
			if e.Owner != "" || e.Group != "" {
				entry.Owner = e.Owner + ":" + e.Group
			}
			// asked for a file, Dockhand answers the file alone, by where the helper has it
			if len(listed.Model.Entries) == 1 && strings.HasPrefix(e.Name, "/") {
				entry.Name = path.Base(e.Name)
				out.Note = noted(fmt.Sprintf("%s is a %s, not a directory: volume_read reads it", dir, cmp.Or(e.Type, "file")), unreleased)
			}
			out.Entries = append(out.Entries, entry)
		}
		slices.SortFunc(out.Entries, func(a, b volumeEntry) int {
			return cmp.Or(cmp.Compare(b2i(b.Type == "directory"), b2i(a.Type == "directory")), cmp.Compare(a.Name, b.Name))
		})

		return nil, out, nil
	})

	type volumeReadIn struct {
		volumeArg
		revealArg

		Path     string `json:"path"                jsonschema:"the file to read, from the volume's top: /config/app.yaml"`
		MaxChars int    `json:"max_chars,omitempty" jsonschema:"the most of the file to give, from its start: 20000 characters unless said, 200000 at most"`
	}
	type volumeReadOut struct {
		Env       string `json:"env"`
		Volume    string `json:"volume"`
		Path      string `json:"path"`
		Content   string `json:"content"             jsonschema:"the file's text, from its start"`
		Chars     int    `json:"chars"               jsonschema:"how long the whole file is, in characters"`
		Truncated bool   `json:"truncated,omitempty" jsonschema:"the file is longer than what is given: content is its start"`
		Hidden    int    `json:"hidden,omitempty"    jsonschema:"how many secrets in it are hidden"`
		Note      string `json:"note,omitempty"`
	}
	add(r, writeTool, &mcp.Tool{
		Name:        "volume_read",
		Description: "Read one text file in a Docker volume: a configuration file, a script, a small log. Gives its start, 20000 characters unless max_chars says otherwise, and says how long the whole is. Dockhand reads files up to 1 MB and no larger, and a file that is not text is refused rather than shown as noise. Passwords, tokens and keys written beside their names, as in a .env file, are hidden unless reveal_secrets asks for them. As volume_files does, and for the same reason, it has Dockhand start a helper container on the host to reach the file and take it away after, so it needs --enable-write although it changes nothing in the volume.",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in volumeReadIn) (*mcp.CallToolResult, volumeReadOut, error) {
		if strings.TrimSpace(in.Path) == "" {
			return nil, volumeReadOut{}, errors.New("path is required: the file to read, from the volume's top, as volume_files lists them")
		}
		if in.MaxChars < 0 || in.MaxChars > maxFileChars {
			return nil, volumeReadOut{}, fmt.Errorf("max_chars is %d: it is how much of the file to give, from 1 to %d, or left out for %d", in.MaxChars, maxFileChars, defaultFileChars)
		}
		at, err := r.findVolume(ctx, in.volumeArg)
		if err != nil {
			return nil, volumeReadOut{}, err
		}
		file := path.Clean("/" + strings.TrimSpace(in.Path))
		out := volumeReadOut{Env: at.Env.Name, Volume: at.Volume.Name, Path: file}

		read, err := r.dock.GetVolumesByNameBrowseContent(ctx, at.Volume.Name, dockhand.GetVolumesByNameBrowseContentOperationOptions{Env: &at.Env.ID, Path: file})
		// the helper is wanted for this one read and no longer, however the read went
		unreleased := r.release(ctx, &at)
		switch status := client.StatusCode(err); {
		case status == http.StatusNotFound:
			return nil, out, leftBehind(fmt.Errorf("the volume %s has no file %s: volume_files lists what it has", out.Volume, file), unreleased)
		case status == http.StatusBadRequest:
			return nil, out, leftBehind(fmt.Errorf("%s in the volume %s is a directory: volume_files lists it", file, out.Volume), unreleased)
		case status == http.StatusRequestEntityTooLarge:
			return nil, out, leftBehind(fmt.Errorf("%s in the volume %s is larger than the 1 MB Dockhand reads of a file", file, out.Volume), unreleased)
		case err != nil:
			return nil, out, leftBehind(fmt.Errorf("%s in the volume %s could not be read: %w", file, out.Volume, refusal(err)), unreleased)
		default:
		}

		text := read.Model.Content
		// Dockhand hands a file over as text whatever it is: what was not text shows as bytes that are none, or as the mark of ones that could not be
		// read
		if strings.ContainsRune(text, 0) || strings.ContainsRune(text, utf8.RuneError) {
			return nil, out, leftBehind(fmt.Errorf("%s in the volume %s is not a text file, so it is not shown", file, out.Volume), unreleased)
		}
		out.Note = unreleased
		if !in.RevealSecrets {
			text, out.Hidden = maskText(text)
		}
		out.Chars = utf8.RuneCountInString(text)
		if limit := cmp.Or(in.MaxChars, defaultFileChars); out.Chars > limit {
			text, out.Truncated = string([]rune(text)[:limit]), true
			out.Note = noted(fmt.Sprintf("the file is %d characters and its first %d are given: raise max_chars for more", out.Chars, limit), out.Note)
		}
		out.Content = text
		if out.Hidden > 0 {
			out.Note = noted(out.Note, hiddenNote(out.Hidden, "told by the names beside them"))
		}

		return nil, out, nil
	})

	type volumeDeleteIn struct {
		volumeArg

		Confirm bool `json:"confirm,omitempty" jsonschema:"remove it: without this the answer says what would go and nothing is removed"`
	}
	type volumeDeleteOut struct {
		Env     string   `json:"env"`
		Name    string   `json:"name"`
		Removed bool     `json:"removed"            jsonschema:"the volume was removed, and is read back as gone"`
		WouldGo []string `json:"would_go,omitempty" jsonschema:"what removing it takes away, said before anything is removed"`
		UsedBy  []string `json:"used_by"            jsonschema:"the containers that mount it, each with its state"`
		Note    string   `json:"note,omitempty"`
	}
	add(r, deleteTool, &mcp.Tool{
		Name:        "volume_delete",
		Description: "Remove one Docker volume from a host, and with it everything it holds: a volume is where a container keeps what must outlast it, a database's files as often as not, and nothing here can bring that back. Without confirm the answer says what would go and which containers mount it, and removes nothing. A volume a container mounts, running or stopped, is refused: remove the container first. Read back as gone.",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in volumeDeleteIn) (*mcp.CallToolResult, volumeDeleteOut, error) {
		at, err := r.findVolume(ctx, in.volumeArg)
		if err != nil {
			return nil, volumeDeleteOut{}, err
		}

		out := volumeDeleteOut{Env: at.Env.Name, Name: at.Volume.Name, UsedBy: r.mountersOf(ctx, &at)}
		if len(out.UsedBy) > 0 {
			return nil, out, fmt.Errorf("the volume %s on %s is mounted by %d containers (%s): Docker does not remove a volume a container has, so remove them first", out.Name, out.Env, len(out.UsedBy), strings.Join(out.UsedBy, ", "))
		}
		if !in.Confirm {
			out.WouldGo = []string{fmt.Sprintf("the volume %s on %s, and every file in it", out.Name, out.Env)}
			out.Note = "nothing was removed: call again with confirm to remove it. What it holds cannot be got back"

			return nil, out, nil
		}

		gone, err := r.dock.DeleteVolumesByName(ctx, at.Volume.Name, dockhand.DeleteVolumesByNameOperationOptions{Env: &at.Env.ID})
		if err != nil {
			return nil, out, fmt.Errorf("the volume %s was not removed: %w", out.Name, refusal(err))
		}
		if !yes(gone.Model.Success) {
			return nil, out, fmt.Errorf("the removal of the volume %s was answered without its being said to have succeeded: volume_list says whether it is still there", out.Name)
		}

		left, err := r.volumesOf(ctx, at.Env)
		if err != nil {
			out.Note = "it was removed, and the volumes could not be listed afterwards: " + err.Error()

			return nil, out, nil //nolint:nilerr // the removal went through: that it could not be read back is said in the answer
		}
		if slices.ContainsFunc(left, func(v dockhand.GetVolumesResponse) bool { return v.Name == at.Volume.Name }) {
			return nil, out, fmt.Errorf("the removal of the volume %s was answered as done, and it is still listed", out.Name)
		}
		out.Removed = true

		return nil, out, nil
	})

	type volumePruneIn struct {
		envArg

		Confirm bool `json:"confirm,omitempty" jsonschema:"remove them: without this the answer lists what would go and nothing is removed"`
	}
	type volumePruneOut struct {
		Env     string   `json:"env"`
		Removed []string `json:"removed"            jsonschema:"the volumes that were removed, by name; with confirm left out, empty"`
		WouldGo []string `json:"would_go,omitempty" jsonschema:"the volumes that would be removed, said before anything is"`
		FreedMB float64  `json:"freed_mb"           jsonschema:"the disk given back, in megabytes, as Docker reports it; 0 before anything is removed, since Docker does not say what a volume holds until it is gone"`
		Note    string   `json:"note,omitempty"`
	}
	add(r, deleteTool, &mcp.Tool{
		Name:        "volume_prune",
		Description: "Remove every Docker volume on one host that no container mounts, named ones included, and everything they hold. It names one environment. This is wider than docker volume prune, which leaves named volumes alone: Dockhand removes them too, so a stack that has been taken down loses the data its volumes kept. Without confirm the answer lists what would go and removes nothing; read that list before confirming. A volume a container mounts, running or stopped, is never removed. Nothing here can bring a removed volume's files back.",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in volumePruneIn) (*mcp.CallToolResult, volumePruneOut, error) {
		env, err := r.one(ctx, in.envArg)
		if err != nil {
			return nil, volumePruneOut{}, err
		}

		out := volumePruneOut{Env: env.Name, Removed: []string{}}
		before, err := r.volumesOf(ctx, env)
		if err != nil {
			return nil, out, fmt.Errorf("the volumes of %s could not be listed, so nothing was removed: %w", env.Name, err)
		}
		for i := range before {
			if len(before[i].UsedBy) == 0 {
				out.WouldGo = append(out.WouldGo, before[i].Name)
			}
		}
		slices.Sort(out.WouldGo)

		if !in.Confirm || len(out.WouldGo) == 0 {
			out.Note = "nothing was removed: call again with confirm to remove them. What they hold cannot be got back"
			if len(out.WouldGo) == 0 {
				out.Note = "every volume on the host is mounted by a container, so there is nothing to remove"
			}

			return nil, out, nil
		}

		pruned, err := r.dock.PostPruneVolumes(ctx, dockhand.PostPruneVolumesOperationOptions{Env: &env.ID})
		if err != nil {
			return nil, out, fmt.Errorf("the volumes of %s were not pruned: %w", env.Name, refusal(err))
		}
		if !yes(pruned.Model.Success) || pruned.Model.Result == nil {
			return nil, out, fmt.Errorf("the pruning of %s was answered without its being said to have succeeded: volume_list says what is left", env.Name)
		}

		out.WouldGo = nil
		out.Removed = append(out.Removed, pruned.Model.Result.VolumesDeleted...)
		slices.Sort(out.Removed)
		out.FreedMB = megabytes(pruned.Model.Result.SpaceReclaimed)
		if len(out.Removed) == 0 {
			out.Note = "nothing was removed: a container came to mount what was unused, or it had gone already"
		}

		return nil, out, nil
	})
}

// How much of a file volume_read gives unless told, and at most.
const (
	defaultFileChars = 20000
	maxFileChars     = 200000
)

// b2i is a flag as a number, to sort by.
func b2i(flag bool) int {
	if flag {
		return 1
	}

	return 0
}

// A helper container Dockhand starts to look inside a volume is named for the volume, and is asked after this many times once Dockhand says it took
// it away.
const (
	helperPrefix = "dockhand-browse-"
	helperPolls  = 20
)

// release has Dockhand take away the helper container it started to look inside a volume, when a tool that looked is done, whether or not the call it
// was looking for is still wanted: a helper left behind is a container on the host that nobody asked to keep. It answers nothing when the helper is
// gone, and otherwise what there is to say of one that is not.
func (r *registry) release(ctx context.Context, at *locatedVolume) string {
	const helper = "the helper container Dockhand started on the host to look inside the volume was not "

	released, err := r.dock.PostVolumesByNameBrowseRelease(context.WithoutCancel(ctx), at.Volume.Name, dockhand.PostVolumesByNameBrowseReleaseOperationOptions{Env: &at.Env.ID})
	switch {
	case err != nil:
		return helper + "taken away: " + err.Error()
	case !yes(released.Model.Success):
		return helper + "said to have been taken away"
	default:
	}

	// Dockhand answers before the helper is gone, and a volume a helper still has is one Docker will not remove: it is waited for, a little
	for range helperPolls {
		listed, err := r.containersOf(context.WithoutCancel(ctx), at.Env)
		still := func(c dockhand.GetContainersResponse) bool {
			return strings.HasPrefix(c.Name, helperPrefix+at.Volume.Name+"-")
		}
		if err != nil || !slices.ContainsFunc(listed, still) {
			return ""
		}
		time.Sleep(r.poll())
	}

	return helper + "gone some seconds after Dockhand said it was taken away"
}

// noted is several things to say in one note, the ones that are nothing left out.
func noted(parts ...string) string {
	said := make([]string, 0, len(parts))
	for _, part := range parts {
		if part != "" {
			said = append(said, part)
		}
	}

	return strings.Join(said, "; ")
}

// leftBehind is a failure with what there is to say of a helper that was not taken away after it, when there is anything.
func leftBehind(err error, unreleased string) error {
	if unreleased == "" {
		return err
	}

	return fmt.Errorf("%w; %s", err, unreleased)
}

// volumeName reports whether a name is one Docker takes for a volume.
func volumeName(name string) bool {
	if len(name) < 2 {
		return false
	}
	for i, r := range name {
		letterOrDigit := (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9')
		if !letterOrDigit && (i == 0 || !strings.ContainsRune("_.-", r)) {
			return false
		}
	}

	return true
}
