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
	"unicode/utf8"

	"github.com/katbyte/dockhand-mcp/sdk/dockhand"
	"github.com/katbyte/pandorest/client"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// keptBack is what Dockhand's refusal to show a part of its own filesystem means, which its own words, "Access denied", do not say.
const keptBack = "Dockhand keeps parts of its own filesystem to itself, among them /etc, /proc, /root and its own database and key"

// serverPath is a path on Dockhand's own filesystem as a caller gives one, which is from the root and is said so rather than guessed at.
func serverPath(given, what string) (string, error) {
	given = strings.TrimSpace(given)
	if given == "" {
		return "", fmt.Errorf("path is required: %s, from the root of Dockhand's own filesystem", what)
	}
	if !strings.HasPrefix(given, "/") {
		return "", fmt.Errorf("path is %q: it is from the root of Dockhand's own filesystem, so it begins with a /", given)
	}

	return path.Clean(given), nil
}

func registerServerFileTools(r *registry) {
	type serverEntry struct {
		Name     string `json:"name"`
		Type     string `json:"type"               jsonschema:"file, directory or symlink"`
		Size     int    `json:"size"               jsonschema:"in bytes; for a directory, what its own entry takes and not what is in it"`
		Mode     string `json:"mode"               jsonschema:"its permissions as numbers: 755"`
		Modified string `json:"modified,omitempty" jsonschema:"when it last changed"`
	}
	type serverFilesIn struct {
		Path  string `json:"path,omitempty"  jsonschema:"the directory to list, from the root of Dockhand's own filesystem; left out, the root itself"`
		Limit int    `json:"limit,omitempty" jsonschema:"the most entries to list, 200 unless given; total counts every one"`
	}
	type serverFilesOut struct {
		Path    string        `json:"path"           jsonschema:"the directory that was listed"`
		Entries []serverEntry `json:"entries"        jsonschema:"what is in it, directories first and then by name; what Dockhand keeps to itself is left out"`
		Total   int           `json:"total"          jsonschema:"how many entries it has that Dockhand shows"`
		Note    string        `json:"note,omitempty"`
	}
	add(r, readTool, &mcp.Tool{
		Name:        "server_files",
		Description: "List a directory on the Dockhand's own filesystem: the machine, or the container, Dockhand itself runs in, which is no Docker host's. It is where Dockhand keeps the compose files of the stacks it made, each in a folder of its own, and where a folder of compose files mounted into Dockhand is found: so it is how to find the path that stack_scan, stack_adopt and stack_move take, and to see what a stack's folder holds. Each entry is given with whether it is a file or a directory, its size, permissions and when it changed. Dockhand keeps parts of its filesystem to itself, and those are neither listed nor read.",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in serverFilesIn) (*mcp.CallToolResult, serverFilesOut, error) {
		if in.Limit < 0 {
			return nil, serverFilesOut{}, fmt.Errorf("limit is %d: it is how many entries to list at most, a number above 0, or left out for %d", in.Limit, defaultEntries)
		}
		dir, err := serverPath(cmp.Or(strings.TrimSpace(in.Path), "/"), "the directory to list")
		if err != nil {
			return nil, serverFilesOut{}, err
		}
		out := serverFilesOut{Path: dir, Entries: []serverEntry{}}

		listed, err := r.dock.GetSystemFiles(ctx, dockhand.GetSystemFilesOperationOptions{Path: dir})
		switch status := client.StatusCode(err); {
		case status == http.StatusNotFound:
			return nil, out, fmt.Errorf("there is no %s on Dockhand's own filesystem", dir)
		case status == http.StatusBadRequest:
			return nil, out, fmt.Errorf("%s is a file and not a directory: server_read reads it", dir)
		case status == http.StatusForbidden:
			return nil, out, fmt.Errorf("%s is not shown: %s", dir, keptBack)
		case err != nil:
			return nil, out, fmt.Errorf("%s could not be listed: %w", dir, refusal(err))
		default:
		}
		for i := range listed.Model.Entries {
			e := &listed.Model.Entries[i]
			out.Entries = append(out.Entries, serverEntry{Name: e.Name, Type: e.Type, Size: e.Size, Mode: e.Mode, Modified: e.Mtime})
		}
		slices.SortStableFunc(out.Entries, func(a, b serverEntry) int {
			return cmp.Or(cmp.Compare(b2i(a.Type != "directory"), b2i(b.Type != "directory")), cmp.Compare(a.Name, b.Name))
		})
		out.Total = len(out.Entries)
		if limit := cmp.Or(in.Limit, defaultEntries); out.Total > limit {
			out.Entries = out.Entries[:limit]
			out.Note = fmt.Sprintf("the directory has %d entries and the first %d are listed: raise limit for more", out.Total, limit)
		}

		return nil, out, nil
	})

	type serverReadIn struct {
		revealArg

		Path     string `json:"path"                jsonschema:"the file to read, from the root of Dockhand's own filesystem, as server_files lists them"`
		MaxChars int    `json:"max_chars,omitempty" jsonschema:"how much of the file to give, from its start: 20000 characters unless given, 200000 at most"`
	}
	type serverReadOut struct {
		Path      string `json:"path"`
		Content   string `json:"content"             jsonschema:"the file's text, from its start"`
		Chars     int    `json:"chars"               jsonschema:"how long the whole file is, in characters"`
		Modified  string `json:"modified,omitempty"  jsonschema:"when it last changed"`
		Truncated bool   `json:"truncated,omitempty" jsonschema:"the file is longer than what is given"`
		Hidden    int    `json:"hidden,omitempty"    jsonschema:"how many secrets in it are hidden"`
		Note      string `json:"note,omitempty"`
	}
	add(r, readTool, &mcp.Tool{
		Name:        "server_read",
		Description: "Read one text file on the Dockhand's own filesystem, which is no Docker host's: a compose file or a .env file where it lies, before a stack is made of it or after Dockhand wrote it. Gives its start, 20000 characters unless max_chars says otherwise, and says how long the whole is. Dockhand reads files up to 10 MB, keeps parts of its filesystem to itself, and a file that is not text is refused rather than shown as noise. Passwords, tokens and keys are hidden where a name beside them says what they are, unless reveal_secrets asks for them.",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in serverReadIn) (*mcp.CallToolResult, serverReadOut, error) {
		if in.MaxChars < 0 || in.MaxChars > maxFileChars {
			return nil, serverReadOut{}, fmt.Errorf("max_chars is %d: it is how much of the file to give, from 1 to %d, or left out for %d", in.MaxChars, maxFileChars, defaultFileChars)
		}
		file, err := serverPath(in.Path, "the file to read")
		if err != nil {
			return nil, serverReadOut{}, err
		}
		out := serverReadOut{Path: file}

		read, err := r.dock.GetSystemFilesContent(ctx, dockhand.GetSystemFilesContentOperationOptions{Path: file})
		switch status := client.StatusCode(err); {
		case status == http.StatusNotFound:
			return nil, out, fmt.Errorf("there is no file %s on Dockhand's own filesystem: server_files lists what there is", file)
		case status == http.StatusBadRequest && strings.Contains(err.Error(), "too large"):
			return nil, out, fmt.Errorf("%s is larger than the 10 MB Dockhand reads of a file", file)
		case status == http.StatusBadRequest:
			return nil, out, fmt.Errorf("%s is a directory and not a file: server_files lists it", file)
		case status == http.StatusForbidden:
			return nil, out, fmt.Errorf("%s is not shown: %s", file, keptBack)
		case err != nil:
			return nil, out, fmt.Errorf("%s could not be read: %w", file, refusal(err))
		default:
		}

		text := read.Model.Content
		// Dockhand hands a file over as text whatever it is: what was not text shows as bytes that are none, or as the mark of ones that could not be
		// read
		if strings.ContainsRune(text, 0) || strings.ContainsRune(text, utf8.RuneError) {
			return nil, out, fmt.Errorf("%s is not a text file, so it is not shown", file)
		}
		if !in.RevealSecrets {
			text, out.Hidden = maskText(text)
		}
		out.Chars, out.Modified = utf8.RuneCountInString(text), read.Model.Mtime
		if limit := cmp.Or(in.MaxChars, defaultFileChars); out.Chars > limit {
			text, out.Truncated = string([]rune(text)[:limit]), true
			out.Note = fmt.Sprintf("the file is %d characters and its first %d are given: raise max_chars for more", out.Chars, limit)
		}
		out.Content = text
		if out.Hidden > 0 {
			out.Note = noted(out.Note, hiddenNote(out.Hidden, "told by the names beside them"))
		}

		return nil, out, nil
	})

	type serverMkdirIn struct {
		dryRunArg

		Path string `json:"path" jsonschema:"the directory to make, from the root of Dockhand's own filesystem; the ones above it are made as well"`
	}
	type serverMkdirOut struct {
		Path    string   `json:"path"`
		Changes []string `json:"changes"           jsonschema:"what was done, in words; empty when the directory was there already"`
		DryRun  bool     `json:"dry_run,omitempty" jsonschema:"nothing was done: changes says what would be"`
		Note    string   `json:"note,omitempty"`
	}
	add(r, writeTool, &mcp.Tool{
		Name:        "server_mkdir",
		Description: "Make a directory on the Dockhand's own filesystem, which is no Docker host's, with the directories above it: a folder for stacks to be kept in or moved to. One that is there already is left as it is. Dockhand makes none in the parts of its filesystem it keeps to itself, and a file in the way is refused. Nothing is ever removed by it, and Dockhand has no call that removes a directory again.",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in serverMkdirIn) (*mcp.CallToolResult, serverMkdirOut, error) {
		dir, err := serverPath(in.Path, "the directory to make")
		if err != nil {
			return nil, serverMkdirOut{}, err
		}
		out := serverMkdirOut{Path: dir, Changes: []string{}, DryRun: in.DryRun}

		// what is there is looked at first: a directory is nothing to do, and a file is in the way
		_, err = r.dock.GetSystemFiles(ctx, dockhand.GetSystemFilesOperationOptions{Path: dir})
		switch status := client.StatusCode(err); {
		case err == nil:
			out.Note = "nothing to do: the directory is there already"

			return nil, out, nil
		case status == http.StatusBadRequest:
			return nil, out, fmt.Errorf("%s is a file, so no directory can be made there", dir)
		case status == http.StatusForbidden:
			return nil, out, fmt.Errorf("%s is not made: %s", dir, keptBack)
		case status != http.StatusNotFound:
			return nil, out, fmt.Errorf("whether %s is there could not be seen, so nothing was made: %w", dir, refusal(err))
		default:
		}
		if in.DryRun {
			out.Changes = append(out.Changes, "would make the directory "+dir+" on Dockhand's own filesystem")

			return nil, out, nil
		}

		made, err := r.dock.PostSystemFiles(ctx, dockhand.PostSystemFilesRequest{Path: dir})
		switch status := client.StatusCode(err); {
		case status == http.StatusForbidden:
			return nil, out, fmt.Errorf("%s was not made: %s", dir, keptBack)
		case err != nil:
			return nil, out, fmt.Errorf("%s was not made: %w", dir, refusal(err))
		case !yes(made.Model.Success):
			return nil, out, errors.New("the making of " + dir + " was answered without its being said to have succeeded: server_files says whether it is there")
		default:
		}
		out.Changes = append(out.Changes, "made the directory "+dir+" on Dockhand's own filesystem")

		return nil, out, nil
	})
}
