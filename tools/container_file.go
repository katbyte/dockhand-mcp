package tools

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"errors"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"slices"
	"strings"

	"github.com/katbyte/dockhand-mcp/sdk/dockhand"
	"github.com/katbyte/pandorest/client"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// What there is at a path inside a container.
const (
	kindFile      = "file"
	kindDirectory = "directory"
	kindOther     = "link"
)

const (
	// maxWriteBytes is the most Dockhand writes to a file in one call
	maxWriteBytes = 1 << 20
	// maxUploadBytes is the most container_file_upload sends in one call, every file together: it is held in memory on its way
	maxUploadBytes = 64 << 20
)

var (
	// fileMode is what chmod takes: three or four octal digits, or the letters way, u+x and go-w
	fileMode = regexp.MustCompile(`^([0-7]{3,4}|[ugoa]*[-+=][rwxXst]*(,[ugoa]*[-+=][rwxXst]*)*)$`)
	// fileOwner is what chown takes: a user, by name or number, and after a colon a group
	fileOwner = regexp.MustCompile(`^[A-Za-z0-9_][A-Za-z0-9_.-]*(:[A-Za-z0-9_][A-Za-z0-9_.-]*)?$`)
)

// inPath is a path inside a container as it is given, made whole: from the container's root, with nothing doubled or going back up.
func inPath(given, what string) (string, error) {
	if strings.TrimSpace(given) == "" {
		return "", fmt.Errorf("%s is required: a path from the container's root, as container_files lists them", what)
	}

	return path.Clean("/" + strings.TrimSpace(given)), nil
}

// gzipped is the form every archive is asked for in. Dockhand cuts a plain tar short when the host is behind an agent, to the length the agent sent
// it compressed, and sends a gzipped one whole from any host.
const gzipped = "tar.gz"

// archiveOf is the archive Docker makes of a path inside a container, which it does of one that is running or not, as Dockhand sends it gzipped. The
// caller closes it. A path that is not there is no archive and no error.
func (r *registry) archiveOf(ctx context.Context, at *located, where string) (io.ReadCloser, error) {
	got, err := r.dock.GetContainersByIdFilesDownload(ctx, at.Container.Id, dockhand.GetContainersByIdFilesDownloadOperationOptions{Env: &at.Env.ID, Path: where, Format: gzipped})
	if err != nil {
		if got.HttpResponse != nil && got.HttpResponse.Body != nil {
			_ = got.HttpResponse.Body.Close()
		}
		// Dockhand answers 500 for a path that is not there, where its document says 404
		if status := client.StatusCode(err); status == http.StatusInternalServerError || status == http.StatusNotFound {
			return nil, nil //nolint:nilnil // nothing at the path is an answer, and nil says it
		}

		return nil, refusal(err)
	}

	return got.HttpResponse.Body, nil
}

// kindAt is what there is at a path inside a container: nothing, a file, a directory, or something else such as a link. It is read from the start of
// the path's archive, and the rest of the archive is not waited for.
func (r *registry) kindAt(ctx context.Context, at *located, where string) (string, error) {
	archive, err := r.archiveOf(ctx, at, where)
	if err != nil || archive == nil {
		return "", err
	}
	defer func() { _ = archive.Close() }()

	first, err := firstOf(archive)
	if err != nil {
		return "", fmt.Errorf("what Dockhand answered for %s is no archive: %w", where, err)
	}
	switch first.Typeflag {
	case tar.TypeDir:
		return kindDirectory, nil
	case tar.TypeReg:
		return kindFile, nil
	default:
		return kindOther, nil
	}
}

// firstOf is the first entry of a gzipped archive, with the archive left where that entry's content starts.
func firstOf(archive io.Reader) (*tar.Header, error) {
	_, first, err := entryOf(archive)

	return first, err
}

// entryOf unpacks a gzipped archive as far as its first entry: the reader is that entry's content, for a file the file itself.
func entryOf(archive io.Reader) (io.Reader, *tar.Header, error) {
	plain, err := gzip.NewReader(archive)
	if err != nil {
		return nil, nil, err
	}
	entries := tar.NewReader(plain)
	first, err := entries.Next()

	return entries, first, err
}

// placeFor is whether something can be put at a path inside a container, and what is there now. A path that is a directory is always refused: written
// to, Dockhand puts a file in its place and everything in it is gone, and moved to, mv puts the thing inside it under a name nobody gave. Anything
// else that is there is replaced only when the call says it may be, and a path whose directory is not there is refused before Docker refuses it in
// its own words.
func (r *registry) placeFor(ctx context.Context, at *located, where string, overwrite bool) (was string, err error) {
	name := at.Container.Name
	if where == "/" {
		return "", errors.New("the container's root is no place for a file: give the whole path, the file's name included")
	}

	if was, err = r.kindAt(ctx, at, where); err != nil {
		return "", fmt.Errorf("what is at %s in %s could not be seen, so nothing was changed: %w", where, name, err)
	}
	switch {
	case was == kindDirectory:
		return was, fmt.Errorf("%s in %s is a directory: give the whole path the file is to have, its name included", where, name)
	case was != "" && !overwrite:
		return was, fmt.Errorf("%s has a %s at %s already: overwrite replaces it", name, was, where)
	case was != "":
		return was, nil
	default:
	}

	dir := path.Dir(where)
	in, err := r.kindAt(ctx, at, dir)
	if err != nil {
		return "", fmt.Errorf("whether %s has the directory %s could not be seen, so nothing was changed: %w", name, dir, err)
	}
	if in != kindDirectory {
		return "", fmt.Errorf("%s has no directory %s to put it in: container_file_mkdir makes one", name, dir)
	}

	return "", nil
}

// mountAt is the mount of a container a path is in, or is: the volume's name, or the host's folder for a bind, and whether the path is the very place
// it is mounted.
func mountAt(c *dockhand.GetContainersResponse, where string) (what string, itself bool) {
	for i := range c.Mounts {
		m := &c.Mounts[i]
		target := path.Clean("/" + m.Destination)
		if where != target && !strings.HasPrefix(where, strings.TrimSuffix(target, "/")+"/") {
			continue
		}
		what = "the host's folder " + m.Source
		if m.Name != "" {
			what = "the volume " + m.Name
		}

		return what, where == target
	}

	return "", false
}

//nolint:gocyclo,maintidx // seven tools registered in one function, each a handler of its own: the count is theirs added up
func registerContainerFileTools(r *registry) {
	type fileWriteIn struct {
		containerArg
		dryRunArg

		Path      string `json:"path"                jsonschema:"the file to write, from the container's root, its name included"`
		Content   string `json:"content"             jsonschema:"the whole text the file is to hold, up to 1 MB; empty for an empty file"`
		Overwrite bool   `json:"overwrite,omitempty" jsonschema:"replace a file that is there already: without this one that is there is left alone and the call is refused"`
	}
	type fileWriteOut struct {
		Env       string   `json:"env"               jsonschema:"the environment (Docker host) the container is on"`
		Container string   `json:"container"`
		Path      string   `json:"path"`
		Created   bool     `json:"created"           jsonschema:"true when there was no file there before; false when one was replaced, or with dry_run"`
		Bytes     int      `json:"bytes"             jsonschema:"how much was written"`
		Changes   []string `json:"changes"           jsonschema:"what was done, in words"`
		DryRun    bool     `json:"dry_run,omitempty" jsonschema:"nothing was done: changes says what would be"`
		Note      string   `json:"note,omitempty"`
	}
	add(r, writeTool, &mcp.Tool{
		Name:        "container_file_write",
		Description: "Write one text file inside a container, new or over one that is there: a configuration put right, a script dropped in. The whole file is given, up to 1 MB, and it works on a container that is stopped too, which is how one that will not start for a bad file is mended. A file that is there is only replaced with overwrite. The file written is root's and readable by everyone, whatever was there before: container_file_perms sets its owner and mode after. A path that is a directory is refused, and so is one whose directory is not there. What is written outside a volume lasts until the container is made again, and what is written under a mount is written to the volume or the host's folder itself.",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in fileWriteIn) (*mcp.CallToolResult, fileWriteOut, error) {
		where, err := inPath(in.Path, "path")
		switch {
		case err != nil:
			return nil, fileWriteOut{}, err
		case len(in.Content) > maxWriteBytes:
			return nil, fileWriteOut{}, fmt.Errorf("content is %d bytes: Dockhand writes a file of up to %d in one call, and container_file_upload sends larger ones", len(in.Content), maxWriteBytes)
		default:
		}
		at, err := r.find(ctx, in.containerArg)
		if err != nil {
			return nil, fileWriteOut{}, err
		}
		out := fileWriteOut{Env: at.Env.Name, Container: at.Container.Name, Path: where, Bytes: len(in.Content), Changes: []string{}, DryRun: in.DryRun}

		was, err := r.placeFor(ctx, &at, where, in.Overwrite)
		if err != nil {
			return nil, out, err
		}
		would, did := fmt.Sprintf("would write %s in %s, %d bytes", where, out.Container, out.Bytes), fmt.Sprintf("wrote %s in %s, %d bytes", where, out.Container, out.Bytes)
		if was != "" {
			would, did = fmt.Sprintf("would replace the %s at %s in %s with %d bytes", was, where, out.Container, out.Bytes), fmt.Sprintf("replaced the %s at %s in %s with %d bytes", was, where, out.Container, out.Bytes)
		}
		if volume, _ := mountAt(&at.Container, where); volume != "" {
			out.Note = "it is under a mount, so it is written to " + volume
		}
		if in.DryRun {
			out.Changes = append(out.Changes, would)

			return nil, out, nil
		}

		if _, err := r.dock.PutContainersByIdFilesContent(ctx, at.Container.Id, dockhand.PutContainersByIdFilesContentRequest{Content: in.Content}, dockhand.PutContainersByIdFilesContentOperationOptions{Env: &at.Env.ID, Path: where}); err != nil {
			return nil, out, fmt.Errorf("%s in %s was not written: %w", where, out.Container, refusal(err))
		}
		out.Changes, out.Created = append(out.Changes, did), was == ""
		if was != "" {
			out.Note = noted(out.Note, "the file is root's and readable by everyone now, whatever the one it replaced was")
		}

		// a running container can be asked for the file back, and one that is stopped only whether it is there
		if running(at.Container.State) {
			if back, failed := r.dock.GetContainersByIdFilesContent(ctx, at.Container.Id, dockhand.GetContainersByIdFilesContentOperationOptions{Env: &at.Env.ID, Path: where}); failed != nil {
				out.Note = noted(out.Note, "it could not be read back: "+refusal(failed).Error())
			} else if back.Model.Content != in.Content {
				out.Note = noted(out.Note, "Dockhand said it wrote the file, and read back it does not hold what was given")
			}
		} else if is, failed := r.kindAt(ctx, &at, where); failed != nil || is != kindFile {
			out.Note = noted(out.Note, "Dockhand said it wrote the file, and it could not be seen there afterwards")
		}

		return nil, out, nil
	})

	type fileMkdirIn struct {
		containerArg
		dryRunArg

		Path string `json:"path" jsonschema:"the directory to make, from the container's root; the directories above it are made too when they are not there"`
	}
	type fileMkdirOut struct {
		Env       string   `json:"env"               jsonschema:"the environment (Docker host) the container is on"`
		Container string   `json:"container"`
		Path      string   `json:"path"`
		Created   bool     `json:"created"           jsonschema:"true when it was made; false when it was there already, or with dry_run"`
		Changes   []string `json:"changes"           jsonschema:"what was done, in words"`
		DryRun    bool     `json:"dry_run,omitempty" jsonschema:"nothing was done: changes says what would be"`
		Note      string   `json:"note,omitempty"`
	}
	add(r, writeTool, &mcp.Tool{
		Name:        "container_file_mkdir",
		Description: "Make a directory inside one running container, and the directories above it that are not there. One that is there already is left as it is, and a path that is a file is refused. Dockhand makes it by running mkdir in the container, as the user the container runs as, so the container must be running.",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in fileMkdirIn) (*mcp.CallToolResult, fileMkdirOut, error) {
		where, err := inPath(in.Path, "path")
		if err != nil {
			return nil, fileMkdirOut{}, err
		}
		at, err := r.inside(ctx, in.containerArg, "a directory can be made")
		if err != nil {
			return nil, fileMkdirOut{}, err
		}
		out := fileMkdirOut{Env: at.Env.Name, Container: at.Container.Name, Path: where, Changes: []string{}, DryRun: in.DryRun}

		was, err := r.kindAt(ctx, &at, where)
		switch {
		case err != nil:
			return nil, out, fmt.Errorf("what is at %s in %s could not be seen, so nothing was made: %w", where, out.Container, err)
		case was == kindDirectory:
			out.Note = "nothing was done: the directory is there already"

			return nil, out, nil
		case was != "":
			return nil, out, fmt.Errorf("%s has a %s at %s, which is no directory and is in the way", out.Container, was, where)
		case in.DryRun:
			out.Changes = append(out.Changes, fmt.Sprintf("would make the directory %s in %s", where, out.Container))

			return nil, out, nil
		default:
		}

		if _, err := r.dock.PostContainersByIdFilesCreate(ctx, at.Container.Id, dockhand.PostContainersByIdFilesCreateRequest{Path: where, Type: kindDirectory}, dockhand.PostContainersByIdFilesCreateOperationOptions{Env: &at.Env.ID}); err != nil {
			return nil, out, fmt.Errorf("the directory %s was not made in %s: %w", where, out.Container, refusal(err))
		}
		out.Changes = append(out.Changes, fmt.Sprintf("made the directory %s in %s", where, out.Container))

		is, err := r.kindAt(ctx, &at, where)
		if out.Created = err == nil && is == kindDirectory; !out.Created {
			out.Note = "Dockhand said it made the directory, and it could not be seen there afterwards"
		}

		return nil, out, nil
	})

	type fileMoveIn struct {
		containerArg
		dryRunArg

		From      string `json:"from"                jsonschema:"the file or directory to move, from the container's root"`
		To        string `json:"to"                  jsonschema:"the whole path it is to have, its name included: the same directory with another name renames it"`
		Overwrite bool   `json:"overwrite,omitempty" jsonschema:"replace a file that is at the new path already: without this the call is refused"`
	}
	type fileMoveOut struct {
		Env       string   `json:"env"               jsonschema:"the environment (Docker host) the container is on"`
		Container string   `json:"container"`
		From      string   `json:"from"`
		To        string   `json:"to"`
		Moved     bool     `json:"moved"             jsonschema:"whether it is at the new path and gone from the old, read back"`
		Changes   []string `json:"changes"           jsonschema:"what was done, in words"`
		DryRun    bool     `json:"dry_run,omitempty" jsonschema:"nothing was done: changes says what would be"`
		Note      string   `json:"note,omitempty"`
	}
	add(r, writeTool, &mcp.Tool{
		Name:        "container_file_move",
		Description: "Move or rename a file or a directory inside one running container, as mv does. The new path is given whole, the name included. A file that is there already is only replaced with overwrite, and a new path that is a directory is refused, where mv would quietly put the thing inside it. Dockhand runs mv in the container, so the container must be running.",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in fileMoveIn) (*mcp.CallToolResult, fileMoveOut, error) {
		from, err := inPath(in.From, "from")
		if err != nil {
			return nil, fileMoveOut{}, err
		}
		to, err := inPath(in.To, "to")
		switch {
		case err != nil:
			return nil, fileMoveOut{}, err
		case from == "/":
			return nil, fileMoveOut{}, errors.New("from is the container's root, which is not moved")
		case to == from:
			return nil, fileMoveOut{}, fmt.Errorf("from and to are both %s: nothing to move", from)
		case strings.HasPrefix(to, from+"/"):
			return nil, fileMoveOut{}, fmt.Errorf("to, %s, is inside from, %s: a thing is not moved into itself", to, from)
		default:
		}
		at, err := r.inside(ctx, in.containerArg, "a file can be moved")
		if err != nil {
			return nil, fileMoveOut{}, err
		}
		out := fileMoveOut{Env: at.Env.Name, Container: at.Container.Name, From: from, To: to, Changes: []string{}, DryRun: in.DryRun}

		what, err := r.kindAt(ctx, &at, from)
		switch {
		case err != nil:
			return nil, out, fmt.Errorf("what is at %s in %s could not be seen, so nothing was moved: %w", from, out.Container, err)
		case what == "":
			return nil, out, fmt.Errorf("%s has nothing at %s: container_files lists what it has", out.Container, from)
		default:
		}
		was, err := r.placeFor(ctx, &at, to, in.Overwrite)
		if err != nil {
			return nil, out, err
		}
		would, did := fmt.Sprintf("would move the %s %s to %s in %s", what, from, to, out.Container), fmt.Sprintf("moved the %s %s to %s in %s", what, from, to, out.Container)
		if was != "" {
			would, did = would+", replacing the "+was+" that is there", did+", replacing the "+was+" that was there"
		}
		if in.DryRun {
			out.Changes = append(out.Changes, would)

			return nil, out, nil
		}

		if _, err := r.dock.PostContainersByIdFilesRename(ctx, at.Container.Id, dockhand.PostContainersByIdFilesRenameRequest{OldPath: from, NewPath: to}, dockhand.PostContainersByIdFilesRenameOperationOptions{Env: &at.Env.ID}); err != nil {
			return nil, out, fmt.Errorf("%s was not moved to %s in %s: %w", from, to, out.Container, refusal(err))
		}
		out.Changes = append(out.Changes, did)

		left, leftErr := r.kindAt(ctx, &at, from)
		is, isErr := r.kindAt(ctx, &at, to)
		if out.Moved = leftErr == nil && isErr == nil && left == "" && is != ""; !out.Moved {
			out.Note = "Dockhand said it moved it, and read back it is not gone from the old path and there at the new"
		}

		return nil, out, nil
	})

	type filePermsIn struct {
		containerArg
		dryRunArg

		Path      string `json:"path"                jsonschema:"the file or directory, from the container's root"`
		Mode      string `json:"mode,omitempty"      jsonschema:"the permissions to give it, as chmod takes them: 644, 0755, or u+x and go-w"`
		Owner     string `json:"owner,omitempty"     jsonschema:"who it is to belong to, as chown takes it: a user, or user:group, by the names the container knows or by number"`
		Recursive bool   `json:"recursive,omitempty" jsonschema:"for a directory, everything in it as well"`
	}
	type filePermsOut struct {
		Env       string   `json:"env"               jsonschema:"the environment (Docker host) the container is on"`
		Container string   `json:"container"`
		Path      string   `json:"path"`
		Mode      string   `json:"mode,omitempty"    jsonschema:"its permissions as ls writes them, read back: rwxr-xr-x"`
		Owner     string   `json:"owner,omitempty"   jsonschema:"who it belongs to, read back, as user:group"`
		Changes   []string `json:"changes"           jsonschema:"what was done, in words"`
		DryRun    bool     `json:"dry_run,omitempty" jsonschema:"nothing was done: changes says what would be"`
		Note      string   `json:"note,omitempty"`
	}
	add(r, writeTool, &mcp.Tool{
		Name:        "container_file_perms",
		Description: "Set the permissions, the owner, or both, of a file or directory inside one running container, as chmod and chown do, and with recursive of everything in a directory. It is what puts a file right after it was written or sent in, which leaves it root's: a process that runs as another user cannot read its own configuration until it is given back. Dockhand runs chmod and chown in the container, so the container must be running, and a user or group is one the container knows or a number. Read back from the container.",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in filePermsIn) (*mcp.CallToolResult, filePermsOut, error) {
		mode, owner := strings.TrimSpace(in.Mode), strings.TrimSpace(in.Owner)
		where, err := inPath(in.Path, "path")
		switch {
		case err != nil:
			return nil, filePermsOut{}, err
		case mode == "" && owner == "":
			return nil, filePermsOut{}, errors.New("nothing to set was given: mode, owner or both")
		case mode != "" && !fileMode.MatchString(mode):
			return nil, filePermsOut{}, fmt.Errorf("mode is %q: it is permissions as chmod takes them, 644 or 0755, or u+x and go-w", in.Mode)
		case owner != "" && !fileOwner.MatchString(owner):
			return nil, filePermsOut{}, fmt.Errorf("owner is %q: it is a user, or user:group, by name or by number", in.Owner)
		default:
		}
		at, err := r.inside(ctx, in.containerArg, "permissions can be set")
		if err != nil {
			return nil, filePermsOut{}, err
		}
		out := filePermsOut{Env: at.Env.Name, Container: at.Container.Name, Path: where, Changes: []string{}, DryRun: in.DryRun}

		// how it stands, as the directory it is in lists it: what is not listed there is not there
		stands := func() (string, string, bool) {
			listed, failed := r.dock.GetContainersByIdFiles(ctx, at.Container.Id, dockhand.GetContainersByIdFilesOperationOptions{Env: &at.Env.ID, Path: path.Dir(where)})
			if failed != nil {
				return "", "", false
			}
			i := slices.IndexFunc(listed.Model.Entries, func(e dockhand.GetContainersByIdFilesResponseEntries) bool { return e.Name == path.Base(where) })
			if i < 0 {
				return "", "", false
			}

			return listed.Model.Entries[i].Permissions, listed.Model.Entries[i].Owner + ":" + listed.Model.Entries[i].Group, true
		}
		var there bool
		if out.Mode, out.Owner, there = stands(); !there && where != "/" {
			return nil, out, fmt.Errorf("%s has nothing at %s: container_files lists what it has", out.Container, where)
		}

		all := ""
		if in.Recursive {
			all = " and everything in it"
		}
		if in.DryRun {
			if mode != "" {
				out.Changes = append(out.Changes, fmt.Sprintf("would set the permissions of %s%s to %s", where, all, mode))
			}
			if owner != "" {
				out.Changes = append(out.Changes, fmt.Sprintf("would give %s%s to %s", where, all, owner))
			}

			return nil, out, nil
		}

		if mode != "" {
			if _, err := r.dock.PostContainersByIdFilesChmod(ctx, at.Container.Id, dockhand.PostContainersByIdFilesChmodRequest{Path: where, Mode: mode, Recursive: new(in.Recursive)}, dockhand.PostContainersByIdFilesChmodOperationOptions{Env: &at.Env.ID}); err != nil {
				return nil, out, fmt.Errorf("the permissions of %s in %s were not set: %w", where, out.Container, refusal(err))
			}
			out.Changes = append(out.Changes, fmt.Sprintf("set the permissions of %s%s to %s", where, all, mode))
		}
		if owner != "" {
			if _, err := r.dock.PostContainersByIdFilesChown(ctx, at.Container.Id, dockhand.PostContainersByIdFilesChownRequest{Path: where, Owner: owner, Recursive: new(in.Recursive)}, dockhand.PostContainersByIdFilesChownOperationOptions{Env: &at.Env.ID}); err != nil {
				if mode != "" {
					return nil, out, fmt.Errorf("the permissions of %s in %s were set, and it was not given to %s: %w", where, out.Container, owner, refusal(err))
				}

				return nil, out, fmt.Errorf("%s in %s was not given to %s: %w", where, out.Container, owner, refusal(err))
			}
			out.Changes = append(out.Changes, fmt.Sprintf("gave %s%s to %s", where, all, owner))
		}

		if out.Mode, out.Owner, there = stands(); !there && where != "/" {
			out.Note = "how it stands now could not be read back"
		}

		return nil, out, nil
	})

	type fileDeleteIn struct {
		containerArg

		Path    string `json:"path"              jsonschema:"the file or directory to remove, from the container's root"`
		Confirm bool   `json:"confirm,omitempty" jsonschema:"remove it: without this the answer says what would go and nothing is removed"`
	}
	type fileDeleteOut struct {
		Env       string `json:"env"               jsonschema:"the environment (Docker host) the container is on"`
		Container string `json:"container"`
		Path      string `json:"path"`
		Kind      string `json:"kind"              jsonschema:"what is there: a file, a directory or a link"`
		Entries   int    `json:"entries,omitempty" jsonschema:"for a directory, how many things are directly in it, all of which go with it and everything under them"`
		Mount     string `json:"mount,omitempty"   jsonschema:"the volume or folder of the host the path is in, when it is under a mount: what is removed is gone from there"`
		Removed   bool   `json:"removed"           jsonschema:"whether it was removed, read back as gone; false with confirm left out"`
		Note      string `json:"note,omitempty"`
	}
	add(r, deleteTool, &mcp.Tool{
		Name:        "container_file_delete",
		Description: "Remove a file or a directory inside one running container, as rm -rf does: a directory goes with everything in it, and nothing is kept anywhere to bring it back. Without confirm the answer says what is there and how much a directory holds, and removes nothing. Under a mount it removes from the volume or the host's folder itself, for every container that mounts it. The container's root, a directory directly under it and the place a volume is mounted are refused: removing those is taking the container apart. Dockhand runs rm in the container, so the container must be running.",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in fileDeleteIn) (*mcp.CallToolResult, fileDeleteOut, error) {
		where, err := inPath(in.Path, "path")
		switch {
		case err != nil:
			return nil, fileDeleteOut{}, err
		case where == "/":
			return nil, fileDeleteOut{}, errors.New("path is the container's root: that is not removed")
		default:
		}
		at, err := r.inside(ctx, in.containerArg, "a file can be removed")
		if err != nil {
			return nil, fileDeleteOut{}, err
		}
		out := fileDeleteOut{Env: at.Env.Name, Container: at.Container.Name, Path: where}

		if out.Kind, err = r.kindAt(ctx, &at, where); err != nil {
			return nil, out, fmt.Errorf("what is at %s in %s could not be seen, so nothing was removed: %w", where, out.Container, err)
		}
		var itself bool
		out.Mount, itself = mountAt(&at.Container, where)
		switch {
		case out.Kind == "":
			return nil, out, fmt.Errorf("%s has nothing at %s: container_files lists what it has", out.Container, where)
		case itself:
			return nil, out, fmt.Errorf("%s is where %s is mounted in %s: removing it would empty that, so it is not done. Remove what is in it by name", where, out.Mount, out.Container)
		case out.Kind == kindDirectory && path.Dir(where) == "/":
			return nil, out, fmt.Errorf("%s is a directory at the root of %s, which would go with everything in it: that is taking the container apart, so it is not done. Remove what is in it by name", where, out.Container)
		default:
		}
		if out.Kind == kindDirectory {
			if listed, failed := r.dock.GetContainersByIdFiles(ctx, at.Container.Id, dockhand.GetContainersByIdFilesOperationOptions{Env: &at.Env.ID, Path: where}); failed == nil {
				out.Entries = len(listed.Model.Entries)
			}
		}
		if out.Mount != "" {
			out.Note = "it is under a mount: what is removed is gone from " + out.Mount
		}
		if !in.Confirm {
			out.Note = noted("nothing was removed: call again with confirm to remove it", out.Note)

			return nil, out, nil
		}

		if _, err := r.dock.DeleteContainersByIdFilesDelete(ctx, at.Container.Id, dockhand.DeleteContainersByIdFilesDeleteOperationOptions{Env: &at.Env.ID, Path: where}); err != nil {
			return nil, out, fmt.Errorf("%s in %s was not removed: %w", where, out.Container, refusal(err))
		}

		is, err := r.kindAt(ctx, &at, where)
		if out.Removed = err == nil && is == ""; !out.Removed {
			out.Note = noted(out.Note, "Dockhand said it removed it, and read back it is still there")
		}

		return nil, out, nil
	})

	type fileUploadIn struct {
		containerArg
		dryRunArg

		Files     []string `json:"files"               jsonschema:"the files to send, each as a full path on the machine this tool runs on; each keeps its own name in the container"`
		To        string   `json:"to"                  jsonschema:"the directory inside the container to put them in, from its root; it must be there"`
		Owner     string   `json:"owner,omitempty"     jsonschema:"who they are to belong to in the container, as user or user:group, by name or number; left out, root"`
		Overwrite bool     `json:"overwrite,omitempty" jsonschema:"replace files of the same names that are there already: without this the call is refused when any is"`
	}
	type fileUploadOut struct {
		Env       string   `json:"env"               jsonschema:"the environment (Docker host) the container is on"`
		Container string   `json:"container"`
		To        string   `json:"to"`
		Uploaded  []string `json:"uploaded"          jsonschema:"the files that are in the container now, by name; with dry_run, the ones that would be sent"`
		Errors    []string `json:"errors,omitempty"  jsonschema:"the files that could not be put there, and why"`
		Bytes     int      `json:"bytes"             jsonschema:"how much was sent, every file together"`
		DryRun    bool     `json:"dry_run,omitempty" jsonschema:"nothing was sent"`
		Note      string   `json:"note,omitempty"`
	}
	add(r, writeTool, &mcp.Tool{
		Name:        "container_file_upload",
		Description: "Send files from the machine this tool runs on into a directory inside a container, each under its own name: a certificate, a dump to restore, a binary, anything too large or not text enough for container_file_write. Up to 64 MB a call, every file together. It works on a container that is stopped too. Files of the same names that are there already are only replaced with overwrite, and the directory must be there. They arrive as root's unless owner says whose. Under a mount they are written to the volume or the host's folder itself.",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in fileUploadIn) (*mcp.CallToolResult, fileUploadOut, error) {
		owner := strings.TrimSpace(in.Owner)
		to, err := inPath(in.To, "to")
		switch {
		case err != nil:
			return nil, fileUploadOut{}, err
		case len(in.Files) == 0:
			return nil, fileUploadOut{}, errors.New("files is required: the files to send, each as a full path on the machine this tool runs on")
		case owner != "" && !fileOwner.MatchString(owner):
			return nil, fileUploadOut{}, fmt.Errorf("owner is %q: it is a user, or user:group, by name or by number", in.Owner)
		default:
		}

		// every file is looked at before anything is asked of the container: one that cannot be sent stops the lot
		names, total := []string{}, int64(0)
		for _, file := range in.Files {
			info, failed := os.Stat(file)
			switch {
			case !filepath.IsAbs(file):
				return nil, fileUploadOut{}, fmt.Errorf("files has %q: a file is given as a full path from the top, on the machine this tool runs on", file)
			case failed != nil:
				return nil, fileUploadOut{}, fmt.Errorf("files has %q, which cannot be read here: %w", file, failed)
			case !info.Mode().IsRegular():
				return nil, fileUploadOut{}, fmt.Errorf("files has %q, which is no file: a directory is sent file by file", file)
			case slices.Contains(names, filepath.Base(file)):
				return nil, fileUploadOut{}, fmt.Errorf("files has two named %s: each keeps its name in the container, so one would replace the other", filepath.Base(file))
			default:
			}
			names, total = append(names, filepath.Base(file)), total+info.Size()
		}
		if total > maxUploadBytes {
			return nil, fileUploadOut{}, fmt.Errorf("the files are %d bytes together: one call sends up to %d, so send them in several", total, maxUploadBytes)
		}

		at, err := r.find(ctx, in.containerArg)
		if err != nil {
			return nil, fileUploadOut{}, err
		}
		out := fileUploadOut{Env: at.Env.Name, Container: at.Container.Name, To: to, Uploaded: []string{}, DryRun: in.DryRun}

		into, err := r.kindAt(ctx, &at, to)
		switch {
		case err != nil:
			return nil, out, fmt.Errorf("what is at %s in %s could not be seen, so nothing was sent: %w", to, out.Container, err)
		case into != kindDirectory:
			return nil, out, fmt.Errorf("%s has no directory %s to put the files in: container_file_mkdir makes one", out.Container, to)
		default:
		}
		for _, name := range names {
			if _, err := r.placeFor(ctx, &at, path.Join(to, name), in.Overwrite); err != nil {
				return nil, out, err
			}
		}
		if volume, _ := mountAt(&at.Container, path.Join(to, names[0])); volume != "" {
			out.Note = "the directory is under a mount, so they are written to " + volume
		}
		if in.DryRun {
			out.Uploaded, out.Bytes = names, int(total)

			return nil, out, nil
		}

		var body bytes.Buffer
		form := multipart.NewWriter(&body)
		for _, file := range in.Files {
			part, failed := form.CreateFormFile("files", filepath.Base(file))
			if failed != nil {
				return nil, out, fmt.Errorf("%s could not be made ready to send: %w", file, failed)
			}
			content, failed := os.ReadFile(file) //nolint:gosec // G304: the path is a file the operator asked to be sent
			if failed != nil {
				return nil, out, fmt.Errorf("%s could not be read, so nothing was sent: %w", file, failed)
			}
			if _, failed = part.Write(content); failed != nil {
				return nil, out, fmt.Errorf("%s could not be made ready to send: %w", file, failed)
			}
			out.Bytes += len(content)
		}
		if owner != "" {
			if err := form.WriteField("owner", owner); err != nil {
				return nil, out, fmt.Errorf("the files could not be made ready to send: %w", err)
			}
		}
		if err := form.Close(); err != nil {
			return nil, out, fmt.Errorf("the files could not be made ready to send: %w", err)
		}

		sent, err := r.dock.PostContainersByIdFilesUpload(ctx, at.Container.Id, &body, form.FormDataContentType(), dockhand.PostContainersByIdFilesUploadOperationOptions{Env: &at.Env.ID, Path: to})
		if err != nil {
			out.Bytes = 0

			return nil, out, fmt.Errorf("the files were not put in %s in %s: %w", to, out.Container, refusal(err))
		}
		out.Uploaded, out.Errors = orNone(sent.Model.Uploaded), sent.Model.Errors
		if len(out.Errors) > 0 {
			out.Note = noted(out.Note, fmt.Sprintf("%d of the %d files could not be put there: errors says why", len(out.Errors), len(names)))
		}

		return nil, out, nil
	})

	type fileDownloadIn struct {
		containerArg

		Path string `json:"path" jsonschema:"the file or directory inside the container to fetch, from its root"`
		To   string `json:"to"   jsonschema:"where to write it, as a full path on the machine this tool runs on: a file's name, or a folder that is there to put it in under its own name. Always a new file: a path anything is at already is refused. A directory is written as a tar archive, gzipped when the name ends .tar.gz or .tgz"`
	}
	type fileDownloadOut struct {
		Env       string `json:"env"            jsonschema:"the environment (Docker host) the container is on"`
		Container string `json:"container"`
		Path      string `json:"path"`
		Kind      string `json:"kind"           jsonschema:"what was fetched: a file, or a directory"`
		Saved     string `json:"saved"          jsonschema:"the file written here, as a full path"`
		Bytes     int64  `json:"bytes"          jsonschema:"its size"`
		Note      string `json:"note,omitempty"`
	}
	add(r, readTool, &mcp.Tool{
		Name:        "container_file_download",
		Description: "Fetch a file or a whole directory out of a container and write it to the machine this tool runs on: a file as itself, of any size and kind, and a directory as a tar archive of everything in it. It is how to take a copy of a container's data or configuration before changing it, and how to get at a file that is not text or is larger than container_read gives. It works on a container that is stopped too. Nothing in the container is changed. The file written here is new and its owner's alone to read: a path anything is at already is refused.",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in fileDownloadIn) (*mcp.CallToolResult, fileDownloadOut, error) {
		where, err := inPath(in.Path, "path")
		to := strings.TrimSpace(in.To)
		switch {
		case err != nil:
			return nil, fileDownloadOut{}, err
		case !filepath.IsAbs(to):
			return nil, fileDownloadOut{}, fmt.Errorf("to is %q: where to write it is a full path from the top, on the machine this tool runs on", in.To)
		default:
		}
		at, err := r.find(ctx, in.containerArg)
		if err != nil {
			return nil, fileDownloadOut{}, err
		}
		out := fileDownloadOut{Env: at.Env.Name, Container: at.Container.Name, Path: where}

		if out.Kind, err = r.kindAt(ctx, &at, where); err != nil {
			return nil, out, fmt.Errorf("what is at %s in %s could not be seen: %w", where, out.Container, err)
		}
		switch out.Kind {
		case "":
			return nil, out, fmt.Errorf("%s has nothing at %s: container_files lists what it has", out.Container, where)
		case kindOther:
			return nil, out, fmt.Errorf("%s in %s is a link or a device and no file: fetch what it points to, which container_files names", where, out.Container)
		default:
		}

		// a folder that is there takes the thing under its own name, and a directory comes as an archive
		if info, statErr := os.Stat(to); statErr == nil && info.IsDir() {
			name := path.Base(where)
			if where == "/" {
				name = out.Container
			}
			if out.Kind == kindDirectory {
				name += ".tar"
			}
			to = filepath.Join(to, name)
		}

		archive, err := r.archiveOf(ctx, &at, where)
		switch {
		case err != nil:
			return nil, out, fmt.Errorf("%s in %s could not be fetched: %w", where, out.Container, err)
		case archive == nil:
			return nil, out, fmt.Errorf("%s in %s was there a moment ago and could not be fetched", where, out.Container)
		default:
		}
		defer func() { _ = archive.Close() }()

		// a file comes wrapped in an archive of one and is written as itself; a directory is written as the archive it comes as, gzipped when its
		// name says so and unpacked to a plain tar when not
		var from io.Reader = archive
		switch lower := strings.ToLower(to); {
		case out.Kind != kindDirectory:
			if from, _, err = entryOf(archive); err != nil {
				return nil, out, fmt.Errorf("what Dockhand answered for %s is no archive: %w", where, err)
			}
		case !strings.HasSuffix(lower, ".tar.gz") && !strings.HasSuffix(lower, ".tgz"):
			if from, err = gzip.NewReader(archive); err != nil {
				return nil, out, fmt.Errorf("what Dockhand answered for %s is no archive: %w", where, err)
			}
		default:
		}

		// never over a file that is there, and for its owner's eyes only: what a container holds is as often secret as not
		file, err := os.OpenFile(to, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600) //nolint:gosec // G304: the path is where the operator asked for it to be written
		if err != nil {
			return nil, out, fmt.Errorf("it could not be written here: %w", err)
		}
		out.Bytes, err = io.Copy(file, from) //nolint:gosec // G110: it is the operator's own container's file, as large as it is, written to disk and never held in memory
		if closed := file.Close(); err == nil {
			err = closed
		}
		if err != nil {
			// what was written is a part of the thing and passes for the whole: it is this call's own file, made a moment ago, and is taken away
			// again
			_ = os.Remove(to)
			out.Bytes = 0

			return nil, out, fmt.Errorf("%s in %s was not all fetched, and the part written to %s was removed: %w", where, out.Container, to, err)
		}
		out.Saved = to
		if out.Kind == kindDirectory {
			out.Note = "a directory comes as a tar archive of everything in it: tar -xf unpacks it"
		}

		return nil, out, nil
	})
}
