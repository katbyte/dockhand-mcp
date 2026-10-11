package tools

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"maps"
	"net/http"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/katbyte/dockhand-mcp/sdk/dockhandtest"
	"github.com/katbyte/go-kt/mcp/acctest"
)

// The calls about the files of the canned alpha's container named loose, which is running.
const (
	looseDownload = "GET /api/containers/" + looseID + "/files/download"
	looseContent  = "GET /api/containers/" + looseID + "/files/content"
	looseFiles    = "GET /api/containers/" + looseID + "/files"
	gzipType      = "application/gzip"
)

// archived is the archive Dockhand answers for a path it is asked to hand over: its entries in order, each a name and what it holds. A name ending in
// a slash is a directory, and one that holds "->" and a target is a link.
func archived(t *testing.T, entries ...string) string {
	t.Helper()

	var packed bytes.Buffer
	w := tar.NewWriter(&packed)
	for i := 0; i+1 < len(entries); i += 2 {
		name, holds := entries[i], entries[i+1]
		header := &tar.Header{Name: name, Mode: 0o644, Size: int64(len(holds)), Typeflag: tar.TypeReg}
		switch {
		case strings.HasSuffix(name, "/"):
			header.Typeflag, header.Size, header.Mode, holds = tar.TypeDir, 0, 0o755, ""
		case strings.HasPrefix(holds, "->"):
			header.Typeflag, header.Size, header.Linkname, holds = tar.TypeSymlink, 0, strings.TrimPrefix(holds, "->"), ""
		default:
		}
		if err := w.WriteHeader(header); err != nil {
			t.Fatal(err)
		}
		if _, err := w.Write([]byte(holds)); err != nil {
			t.Fatal(err)
		}
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}

	return packed.String()
}

// zipped is something gzipped, which is how an archive is asked for and sent.
func zipped(t *testing.T, plain string) string {
	t.Helper()

	var packed bytes.Buffer
	w := gzip.NewWriter(&packed)
	if _, err := w.Write([]byte(plain)); err != nil {
		t.Fatal(err)
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}

	return packed.String()
}

// holds has a canned container hold something at a path, as the archive of the path tells it: a file, a directory with what is in it, or a link.
func holds(t *testing.T, dock *dockhandtest.Server, id, where string, entries ...string) {
	t.Helper()

	dock.AnswerTextWhen("GET /api/containers/"+id+"/files/download", "path", where, gzipType, zipped(t, archived(t, entries...)))
}

// lacks has the canned loose hold nothing at a path, which Dockhand answers as a failure of its own.
func lacks(dock *dockhandtest.Server, where string) {
	dock.FailWhen(looseDownload, "path", where, http.StatusInternalServerError, "Failed to download file")
}

// withFiles is the two canned hosts, with alpha's loose holding a directory, /etc/app, with one file in it, and nothing anywhere else.
func withFiles(t *testing.T) *dockhandtest.Server {
	t.Helper()

	dock := twoHosts(t)
	for _, id := range []string{looseID, frontID, crashedID} {
		dock.Fail("GET /api/containers/"+id+"/files/download", http.StatusInternalServerError, "Failed to download file")
	}
	holds(t, dock, looseID, "/etc/app", "app/", "", "app/app.conf", "mode = demo\n")
	holds(t, dock, looseID, "/etc/app/app.conf", "app.conf", "mode = demo\n")

	return dock
}

func TestContainerFileWrite(t *testing.T) {
	t.Parallel()

	const put = "PUT /api/containers/" + looseID + "/files/content"
	dock := withFiles(t)
	dock.Answer(put, `{"success":true,"path":"/etc/app/new.conf"}`)
	suite := session(t, dock, everything)

	// a new file: said first, then written and read back
	dry := suite.Call(t, "container_file_write", map[string]any{"container": "loose", "path": "etc/app//new.conf", "content": "live\n", "dry_run": true})
	if got := acctest.Strs(t, dry["changes"], "changes"); len(got) != 1 || got[0] != "would write /etc/app/new.conf in loose, 5 bytes" || dry["path"] != "/etc/app/new.conf" || !acctest.IsBool(dry["created"], false) || len(dock.Requests(put)) != 0 {
		t.Errorf("a dry run = %v", dry)
	}
	dock.Then(put, func(made dockhandtest.Request) {
		dock.AnswerWhen(looseContent, "path", made.Query.Get("path"), `{"content":"live\n","path":"/etc/app/new.conf"}`)
	})
	out := suite.Call(t, "container_file_write", map[string]any{"container": "loose", "path": "/etc/app/new.conf", "content": "live\n"})
	if got := acctest.Strs(t, out["changes"], "changes"); out["env"] != "alpha" || out["container"] != "loose" || !acctest.IsBool(out["created"], true) || acctest.Num(t, out["bytes"], "bytes") != 5 || out["note"] != nil || len(got) != 1 || got[0] != "wrote /etc/app/new.conf in loose, 5 bytes" {
		t.Errorf("writing a new file = %v", out)
	}
	if made := dock.Last(put); made.Body != `{"content":"live\n"}` || made.Query.Get("path") != "/etc/app/new.conf" || made.Query.Get("env") != "1" {
		t.Errorf("writing a file sent %s to %v", made.Body, made.Query)
	}

	// one that is there is left alone until it is said it may go, and then it is root's
	if msg := suite.CallErr(t, "container_file_write", map[string]any{"container": "loose", "path": "/etc/app/app.conf", "content": "live\n"}); !strings.Contains(msg, "loose has a file at /etc/app/app.conf already: overwrite replaces it") || len(dock.Requests(put)) != 1 {
		t.Errorf("writing over a file unasked = %q", msg)
	}
	if dry = suite.Call(t, "container_file_write", map[string]any{"container": "loose", "path": "/etc/app/app.conf", "content": "live\n", "overwrite": true, "dry_run": true}); acctest.Strs(t, dry["changes"], "changes")[0] != "would replace the file at /etc/app/app.conf in loose with 5 bytes" {
		t.Errorf("a dry run of writing over = %v", dry)
	}
	out = suite.Call(t, "container_file_write", map[string]any{"container": "loose", "path": "/etc/app/app.conf", "content": "live\n", "overwrite": true})
	if got := acctest.Strs(t, out["changes"], "changes"); got[0] != "replaced the file at /etc/app/app.conf in loose with 5 bytes" || !acctest.IsBool(out["created"], false) || out["note"] != "the file is root's and readable by everyone now, whatever the one it replaced was" {
		t.Errorf("writing over a file = %v", out)
	}

	// read back, it holds something else, or cannot be read
	dock.Then(put, func(made dockhandtest.Request) {
		dock.AnswerWhen(looseContent, "path", made.Query.Get("path"), `{"content":"something else","path":"/etc/app/other.conf"}`)
	})
	if out = suite.Call(t, "container_file_write", map[string]any{"container": "loose", "path": "/etc/app/other.conf", "content": "live\n"}); !strings.Contains(acctest.Str(out["note"]), "read back it does not hold what was given") {
		t.Errorf("a file that reads back as something else = %v", out)
	}
	dock.Then(put, nil)
	dock.Fail(looseContent, http.StatusInternalServerError, "Failed to read file")
	if out = suite.Call(t, "container_file_write", map[string]any{"container": "loose", "path": "/etc/app/third.conf", "content": ""}); !strings.Contains(acctest.Str(out["note"]), "it could not be read back") || dock.Last(put).Body != `{"content":""}` {
		t.Errorf("a file that could not be read back = %v", out)
	}

	// under a mount it is written to what is mounted, and that is said
	holds(t, dock, frontID, "/data", "data/", "")
	if dry = suite.Call(t, "container_file_write", map[string]any{"container": "web-front-1", "path": "/data/note.txt", "content": "kept", "dry_run": true}); dry["note"] != "it is under a mount, so it is written to the volume web_data" {
		t.Errorf("a file under a mount = %v", dry)
	}

	holds(t, dock, frontID, "/etc", "etc/", "")
	if dry = suite.Call(t, "container_file_write", map[string]any{"container": "web-front-1", "path": "/etc/note.txt", "content": "kept", "dry_run": true}); dry["note"] != nil {
		t.Errorf("a file beside a mount and not under it = %v", dry)
	}

	// a stopped container takes a file too, and is asked only whether it is there afterwards
	const stopped = "PUT /api/containers/" + crashedID + "/files/content"
	holds(t, dock, crashedID, "/etc", "etc/", "")
	dock.Answer(stopped, `{"success":true,"path":"/etc/fix.conf"}`)
	if out = suite.Call(t, "container_file_write", map[string]any{"container": "crashed", "path": "/etc/fix.conf", "content": "fixed"}); !acctest.IsBool(out["created"], true) || out["note"] != "Dockhand said it wrote the file, and it could not be seen there afterwards" {
		t.Errorf("a file written to a stopped container and not seen after = %v", out)
	}
	dock.Then(stopped, func(dockhandtest.Request) { holds(t, dock, crashedID, "/etc/fix.conf", "fix.conf", "fixed") })
	if out = suite.Call(t, "container_file_write", map[string]any{"container": "crashed", "path": "/etc/fix.conf", "content": "fixed"}); out["note"] != nil {
		t.Errorf("a file written to a stopped container = %v", out)
	}
}

func TestContainerFileWriteRefusals(t *testing.T) {
	t.Parallel()

	const put = "PUT /api/containers/" + looseID + "/files/content"
	dock := withFiles(t)
	holds(t, dock, looseID, "/etc/app/link", "link", "->app.conf")
	dock.AnswerTextWhen(looseDownload, "path", "/etc/odd", gzipType, "this is no archive")
	dock.FailWhen(looseDownload, "path", "/etc/shut", http.StatusForbidden, "Permission denied to access the path")
	dock.FailWhen(looseDownload, "path", "/etc/shut/in", http.StatusInternalServerError, "Failed to download file")
	suite := session(t, dock, everything)

	for _, tc := range []struct {
		why, path, want string
	}{
		{"no path", " ", "path is required"},
		{"the root", "/", "the container's root is no place for a file"},
		{"a directory", "/etc/app", "/etc/app in loose is a directory: give the whole path the file is to have"},
		{"a link, unasked", "/etc/app/link", "loose has a link at /etc/app/link already"},
		{"a directory that is not there", "/nope/x.conf", "loose has no directory /nope to put it in: container_file_mkdir makes one"},
		{"an answer that is no archive", "/etc/odd", "what Dockhand answered for /etc/odd is no archive"},
		{"a path that cannot be looked at", "/etc/shut", "what is at /etc/shut in loose could not be seen, so nothing was changed"},
		{"a directory that cannot be looked at", "/etc/shut/in", "whether loose has the directory /etc/shut could not be seen"},
	} {
		if msg := suite.CallErr(t, "container_file_write", map[string]any{"container": "loose", "path": tc.path, "content": "x"}); !strings.Contains(msg, tc.want) {
			t.Errorf("%s = %q, want it to say %q", tc.why, msg, tc.want)
		}
	}
	if msg := suite.CallErr(t, "container_file_write", map[string]any{"container": "loose", "path": "/etc/app/big", "content": strings.Repeat("x", maxWriteBytes+1)}); !strings.Contains(msg, "content is 1048577 bytes") {
		t.Errorf("more than Dockhand writes = %q", msg)
	}
	if msg := suite.CallErr(t, "container_file_write", map[string]any{"container": "nope", "path": "/x", "content": "x"}); !strings.Contains(msg, "nope") {
		t.Errorf("a container there is none of = %q", msg)
	}
	if len(dock.Requests(put)) != 0 {
		t.Errorf("a refused write wrote: %v", dock.Calls())
	}

	dock.Fail(put, http.StatusInternalServerError, "Failed to write file")
	if msg := suite.CallErr(t, "container_file_write", map[string]any{"container": "loose", "path": "/etc/app/new.conf", "content": "x"}); !strings.Contains(msg, "/etc/app/new.conf in loose was not written") {
		t.Errorf("a write Dockhand refused = %q", msg)
	}
}

func TestContainerFileMkdir(t *testing.T) {
	t.Parallel()

	const create = "POST /api/containers/" + looseID + "/files/create"
	dock := withFiles(t)
	dock.Answer(create, `{"success":true,"path":"/srv/new/deep","type":"directory","owner":null}`)
	suite := session(t, dock, everything)

	dry := suite.Call(t, "container_file_mkdir", map[string]any{"container": "loose", "path": "/srv/new/deep/", "dry_run": true})
	if got := acctest.Strs(t, dry["changes"], "changes"); len(got) != 1 || got[0] != "would make the directory /srv/new/deep in loose" || len(dock.Requests(create)) != 0 {
		t.Errorf("a dry run = %v", dry)
	}
	// made, and not seen afterwards; then made and seen
	if out := suite.Call(t, "container_file_mkdir", map[string]any{"container": "loose", "path": "/srv/new/deep"}); !acctest.IsBool(out["created"], false) || out["note"] != "Dockhand said it made the directory, and it could not be seen there afterwards" {
		t.Errorf("a directory not seen once made = %v", out)
	}
	dock.Then(create, func(dockhandtest.Request) { holds(t, dock, looseID, "/srv/new/deep", "deep/", "") })
	out := suite.Call(t, "container_file_mkdir", map[string]any{"container": "loose", "path": "/srv/new/deep"})
	if got := acctest.Strs(t, out["changes"], "changes"); !acctest.IsBool(out["created"], true) || out["note"] != nil || got[0] != "made the directory /srv/new/deep in loose" || !reflect.DeepEqual(sent(t, dock.Last(create)), map[string]any{"path": "/srv/new/deep", "type": "directory"}) || dock.Last(create).Query.Get("env") != "1" {
		t.Errorf("making a directory = %v, sent %s", out, dock.Last(create).Body)
	}
	// there already, it is left; a file is in the way
	if out = suite.Call(t, "container_file_mkdir", map[string]any{"container": "loose", "path": "/etc/app"}); out["note"] != "nothing was done: the directory is there already" || len(dock.Requests(create)) != 2 {
		t.Errorf("making a directory that is there = %v", out)
	}
	if msg := suite.CallErr(t, "container_file_mkdir", map[string]any{"container": "loose", "path": "/etc/app/app.conf"}); !strings.Contains(msg, "loose has a file at /etc/app/app.conf, which is no directory and is in the way") {
		t.Errorf("making a directory where a file is = %q", msg)
	}

	// what goes wrong
	if msg := suite.CallErr(t, "container_file_mkdir", map[string]any{"container": "loose", "path": ""}); !strings.Contains(msg, "path is required") {
		t.Errorf("no path = %q", msg)
	}
	if msg := suite.CallErr(t, "container_file_mkdir", map[string]any{"container": "crashed", "path": "/srv/x"}); !strings.Contains(msg, "crashed on alpha is exited, and a directory can be made only of a container that is running") {
		t.Errorf("a directory in a stopped container = %q", msg)
	}
	dock.FailWhen(looseDownload, "path", "/srv/shut", http.StatusForbidden, "Permission denied to access the path")
	if msg := suite.CallErr(t, "container_file_mkdir", map[string]any{"container": "loose", "path": "/srv/shut"}); !strings.Contains(msg, "what is at /srv/shut in loose could not be seen, so nothing was made") {
		t.Errorf("a path that cannot be looked at = %q", msg)
	}
	dock.Fail(create, http.StatusInternalServerError, "Failed to create")
	if msg := suite.CallErr(t, "container_file_mkdir", map[string]any{"container": "loose", "path": "/srv/other"}); !strings.Contains(msg, "the directory /srv/other was not made in loose") {
		t.Errorf("a directory Dockhand would not make = %q", msg)
	}
}

func TestContainerFileMove(t *testing.T) {
	t.Parallel()

	const rename = "POST /api/containers/" + looseID + "/files/rename"
	dock := withFiles(t)
	holds(t, dock, looseID, "/etc/app/old.conf", "old.conf", "old\n")
	dock.Answer(rename, `{"success":true,"oldPath":"/etc/app/app.conf","newPath":"/etc/app/main.conf"}`)
	suite := session(t, dock, everything)
	args := map[string]any{"container": "loose", "from": "/etc/app/app.conf", "to": "/etc/app/main.conf"}

	dry := suite.Call(t, "container_file_move", map[string]any{"container": "loose", "from": "/etc/app/app.conf", "to": "/etc/app/main.conf", "dry_run": true})
	if got := acctest.Strs(t, dry["changes"], "changes"); len(got) != 1 || got[0] != "would move the file /etc/app/app.conf to /etc/app/main.conf in loose" || !acctest.IsBool(dry["moved"], false) || len(dock.Requests(rename)) != 0 {
		t.Errorf("a dry run = %v", dry)
	}
	// Dockhand says it moved it, and it is where it was
	if out := suite.Call(t, "container_file_move", args); !acctest.IsBool(out["moved"], false) || !strings.Contains(acctest.Str(out["note"]), "read back it is not gone from the old path and there at the new") {
		t.Errorf("a move that did not take = %v", out)
	}
	// moved, and read back as gone from the one and there at the other
	dock.Then(rename, func(dockhandtest.Request) {
		lacks(dock, "/etc/app/app.conf")
		holds(t, dock, looseID, "/etc/app/main.conf", "main.conf", "mode = demo\n")
	})
	out := suite.Call(t, "container_file_move", args)
	if got := acctest.Strs(t, out["changes"], "changes"); !acctest.IsBool(out["moved"], true) || out["note"] != nil || got[0] != "moved the file /etc/app/app.conf to /etc/app/main.conf in loose" || !reflect.DeepEqual(sent(t, dock.Last(rename)), map[string]any{"oldPath": "/etc/app/app.conf", "newPath": "/etc/app/main.conf"}) || dock.Last(rename).Query.Get("env") != "1" {
		t.Errorf("a move = %v, sent %s", out, dock.Last(rename).Body)
	}

	// onto a file that is there only when asked, and never onto a directory
	dock.Then(rename, nil)
	if msg := suite.CallErr(t, "container_file_move", map[string]any{"container": "loose", "from": "/etc/app/main.conf", "to": "/etc/app/old.conf"}); !strings.Contains(msg, "loose has a file at /etc/app/old.conf already: overwrite replaces it") {
		t.Errorf("a move onto a file unasked = %q", msg)
	}
	if dry = suite.Call(t, "container_file_move", map[string]any{"container": "loose", "from": "/etc/app/main.conf", "to": "/etc/app/old.conf", "overwrite": true, "dry_run": true}); acctest.Strs(t, dry["changes"], "changes")[0] != "would move the file /etc/app/main.conf to /etc/app/old.conf in loose, replacing the file that is there" {
		t.Errorf("a dry run of a move onto a file = %v", dry)
	}
	if msg := suite.CallErr(t, "container_file_move", map[string]any{"container": "loose", "from": "/etc/app/main.conf", "to": "/etc/app", "overwrite": true}); !strings.Contains(msg, "/etc/app in loose is a directory") {
		t.Errorf("a move onto a directory = %q", msg)
	}

	for _, tc := range []struct {
		why, from, to, want string
	}{
		{"nothing to move", "", "/x", "from is required"},
		{"nowhere to move it", "/x", " ", "to is required"},
		{"the root", "/", "/x", "from is the container's root"},
		{"where it is", "/etc/app/main.conf", "/etc/app/./main.conf", "from and to are both /etc/app/main.conf"},
		{"into itself", "/etc/app", "/etc/app/inner", "a thing is not moved into itself"},
		{"what is not there", "/etc/app/absent", "/etc/app/x", "loose has nothing at /etc/app/absent"},
	} {
		if msg := suite.CallErr(t, "container_file_move", map[string]any{"container": "loose", "from": tc.from, "to": tc.to}); !strings.Contains(msg, tc.want) {
			t.Errorf("moving %s = %q, want it to say %q", tc.why, msg, tc.want)
		}
	}
	if msg := suite.CallErr(t, "container_file_move", map[string]any{"container": "crashed", "from": "/a", "to": "/b"}); !strings.Contains(msg, "a file can be moved only of a container that is running") {
		t.Errorf("a move in a stopped container = %q", msg)
	}
	dock.FailWhen(looseDownload, "path", "/etc/shut", http.StatusForbidden, "Permission denied to access the path")
	if msg := suite.CallErr(t, "container_file_move", map[string]any{"container": "loose", "from": "/etc/shut", "to": "/etc/x"}); !strings.Contains(msg, "what is at /etc/shut in loose could not be seen, so nothing was moved") {
		t.Errorf("a move of what cannot be looked at = %q", msg)
	}
	dock.Fail(rename, http.StatusInternalServerError, "Failed to rename")
	if msg := suite.CallErr(t, "container_file_move", map[string]any{"container": "loose", "from": "/etc/app/main.conf", "to": "/etc/app/last.conf"}); !strings.Contains(msg, "/etc/app/main.conf was not moved to /etc/app/last.conf in loose") {
		t.Errorf("a move Dockhand refused = %q", msg)
	}
}

func TestContainerFilePerms(t *testing.T) {
	t.Parallel()

	const (
		chmod = "POST /api/containers/" + looseID + "/files/chmod"
		chown = "POST /api/containers/" + looseID + "/files/chown"
		was   = `{"path":"/etc/app","entries":[{"name":"app.conf","type":"file","size":12,"permissions":"rw-r--r--","owner":"root","group":"root","modified":"2026-10-10T12:00:00"}]}`
		is    = `{"path":"/etc/app","entries":[{"name":"app.conf","type":"file","size":12,"permissions":"rw-r-----","owner":"app","group":"app","modified":"2026-10-10T12:00:00"}]}`
	)
	dock := withFiles(t)
	dock.Fail(looseFiles, http.StatusInternalServerError, "Failed to list")
	dock.AnswerWhen(looseFiles, "path", "/etc/app", was)
	dock.AnswerWhen(looseFiles, "path", "/", `{"path":"/","entries":[{"name":"etc","type":"directory","size":4096,"permissions":"rwxr-xr-x","owner":"root","group":"root"}]}`)
	dock.Answer(chmod, `{"success":true,"path":"/etc/app/app.conf","mode":"640","recursive":false}`)
	dock.Answer(chown, `{"success":true,"path":"/etc/app/app.conf","owner":"app:app","recursive":false}`)
	suite := session(t, dock, everything)
	args := map[string]any{"container": "loose", "path": "/etc/app/app.conf", "mode": " 640 ", "owner": "app:app"}

	dry := suite.Call(t, "container_file_perms", map[string]any{"container": "loose", "path": "/etc/app/app.conf", "mode": "640", "owner": "app:app", "recursive": true, "dry_run": true})
	if got := acctest.Strs(t, dry["changes"], "changes"); !reflect.DeepEqual(got, []string{"would set the permissions of /etc/app/app.conf and everything in it to 640", "would give /etc/app/app.conf and everything in it to app:app"}) || dry["mode"] != "rw-r--r--" || dry["owner"] != "root:root" || len(dock.Requests(chmod)) != 0 {
		t.Errorf("a dry run = %v", dry)
	}

	// both set, and read back as the directory lists it
	dock.Then(chown, func(dockhandtest.Request) { dock.AnswerWhen(looseFiles, "path", "/etc/app", is) })
	out := suite.Call(t, "container_file_perms", args)
	if got := acctest.Strs(t, out["changes"], "changes"); !reflect.DeepEqual(got, []string{"set the permissions of /etc/app/app.conf to 640", "gave /etc/app/app.conf to app:app"}) || out["mode"] != "rw-r-----" || out["owner"] != "app:app" || out["note"] != nil {
		t.Errorf("setting permissions and owner = %v", out)
	}
	if !reflect.DeepEqual(sent(t, dock.Last(chmod)), map[string]any{"path": "/etc/app/app.conf", "mode": "640", "recursive": false}) || !reflect.DeepEqual(sent(t, dock.Last(chown)), map[string]any{"path": "/etc/app/app.conf", "owner": "app:app", "recursive": false}) || dock.Last(chown).Query.Get("env") != "1" {
		t.Errorf("setting them sent %s and %s", dock.Last(chmod).Body, dock.Last(chown).Body)
	}
	// one alone, the letters way; and the root itself, which no directory lists
	if out = suite.Call(t, "container_file_perms", map[string]any{"container": "loose", "path": "/etc/app/app.conf", "mode": "u+x,go-w"}); len(acctest.Strs(t, out["changes"], "changes")) != 1 || len(dock.Requests(chown)) != 1 || sent(t, dock.Last(chmod))["mode"] != "u+x,go-w" {
		t.Errorf("setting permissions alone = %v", out)
	}
	if out = suite.Call(t, "container_file_perms", map[string]any{"container": "loose", "path": "/", "owner": "0"}); len(acctest.Strs(t, out["changes"], "changes")) != 1 || out["note"] != nil {
		t.Errorf("giving the root to someone = %v", out)
	}

	for _, tc := range []struct {
		why  string
		args map[string]any
		want string
	}{
		{"no path", map[string]any{"path": " ", "mode": "644"}, "path is required"},
		{"nothing to set", map[string]any{"path": "/etc/app/app.conf"}, "nothing to set was given"},
		{"a mode that is none", map[string]any{"path": "/etc/app/app.conf", "mode": "999"}, `mode is "999"`},
		{"an owner that is none", map[string]any{"path": "/etc/app/app.conf", "owner": "a b"}, `owner is "a b"`},
		{"what is not there", map[string]any{"path": "/etc/app/absent", "mode": "644"}, "loose has nothing at /etc/app/absent"},
		{"what is in no directory", map[string]any{"path": "/nope/x", "mode": "644"}, "loose has nothing at /nope/x"},
	} {
		given := map[string]any{"container": "loose"}
		maps.Copy(given, tc.args)
		if msg := suite.CallErr(t, "container_file_perms", given); !strings.Contains(msg, tc.want) {
			t.Errorf("%s = %q, want it to say %q", tc.why, msg, tc.want)
		}
	}
	if msg := suite.CallErr(t, "container_file_perms", map[string]any{"container": "crashed", "path": "/x", "mode": "644"}); !strings.Contains(msg, "permissions can be set only of a container that is running") {
		t.Errorf("permissions in a stopped container = %q", msg)
	}

	// the owner refused, alone and after the permissions were set; then the permissions refused
	dock.Then(chown, nil)
	dock.Fail(chown, http.StatusInternalServerError, "Failed to change ownership: chown: unknown user nouser")
	if msg := suite.CallErr(t, "container_file_perms", map[string]any{"container": "loose", "path": "/etc/app/app.conf", "owner": "nouser"}); !strings.HasPrefix(msg, "container_file_perms: /etc/app/app.conf in loose was not given to nouser") && !strings.Contains(msg, "/etc/app/app.conf in loose was not given to nouser") {
		t.Errorf("an owner Docker refused = %q", msg)
	}
	if msg := suite.CallErr(t, "container_file_perms", map[string]any{"container": "loose", "path": "/etc/app/app.conf", "mode": "600", "owner": "nouser"}); !strings.Contains(msg, "the permissions of /etc/app/app.conf in loose were set, and it was not given to nouser") {
		t.Errorf("an owner Docker refused after the permissions = %q", msg)
	}
	dock.Fail(chmod, http.StatusBadRequest, "Invalid chmod mode")
	if msg := suite.CallErr(t, "container_file_perms", map[string]any{"container": "loose", "path": "/etc/app/app.conf", "mode": "600"}); !strings.Contains(msg, "the permissions of /etc/app/app.conf in loose were not set") {
		t.Errorf("permissions Docker refused = %q", msg)
	}
	// set, and not readable afterwards
	dock.Answer(chmod, `{"success":true}`)
	dock.Then(chmod, func(dockhandtest.Request) {
		dock.FailWhen(looseFiles, "path", "/etc/app", http.StatusInternalServerError, "Failed to list")
	})
	if out = suite.Call(t, "container_file_perms", map[string]any{"container": "loose", "path": "/etc/app/app.conf", "mode": "600"}); out["note"] != "how it stands now could not be read back" {
		t.Errorf("permissions that could not be read back = %v", out)
	}
}

func TestContainerFileDelete(t *testing.T) {
	t.Parallel()

	const (
		remove = "DELETE /api/containers/" + looseID + "/files/delete"
		front  = "GET /api/containers/" + frontID + "/files"
	)
	dock := withFiles(t)
	holds(t, dock, looseID, "/srv", "srv/", "")
	holds(t, dock, frontID, "/data", "data/", "")
	holds(t, dock, frontID, "/data/cache", "cache/", "", "cache/a", "a")
	dock.AnswerWhen(looseFiles, "path", "/etc/app", `{"path":"/etc/app","entries":[{"name":"app.conf","type":"file","size":12},{"name":"extra","type":"directory","size":4096}]}`)
	dock.Fail(front, http.StatusInternalServerError, "Failed to list")
	dock.Answer(remove, `{"success":true,"path":"/etc/app"}`)
	suite := session(t, dock, everything)

	// said first: what is there and how much a directory holds
	out := suite.Call(t, "container_file_delete", map[string]any{"container": "loose", "path": "/etc/app/"})
	if out["env"] != "alpha" || out["container"] != "loose" || out["path"] != "/etc/app" || out["kind"] != "directory" || acctest.Num(t, out["entries"], "entries") != 2 || !acctest.IsBool(out["removed"], false) || out["note"] != "nothing was removed: call again with confirm to remove it" || len(dock.Requests(remove)) != 0 {
		t.Errorf("what would be removed = %v", out)
	}
	// under a mount it goes from what is mounted, which is said; a directory that cannot be listed is not counted
	out = suite.Call(t, "container_file_delete", map[string]any{"container": "web-front-1", "path": "/data/cache"})
	if out["mount"] != "the volume web_data" || out["entries"] != nil || out["note"] != "nothing was removed: call again with confirm to remove it; it is under a mount: what is removed is gone from the volume web_data" {
		t.Errorf("what would be removed from under a mount = %v", out)
	}

	// said to have gone and still there; then gone, read back
	out = suite.Call(t, "container_file_delete", map[string]any{"container": "loose", "path": "/etc/app/app.conf", "confirm": true})
	if out["kind"] != "file" || out["entries"] != nil || !acctest.IsBool(out["removed"], false) || out["note"] != "Dockhand said it removed it, and read back it is still there" {
		t.Errorf("a removal that did not take = %v", out)
	}
	dock.Then(remove, func(made dockhandtest.Request) { lacks(dock, made.Query.Get("path")) })
	out = suite.Call(t, "container_file_delete", map[string]any{"container": "loose", "path": "/etc/app", "confirm": true})
	if !acctest.IsBool(out["removed"], true) || out["note"] != nil || dock.Last(remove).Query.Get("path") != "/etc/app" || dock.Last(remove).Query.Get("env") != "1" {
		t.Errorf("a removal = %v", out)
	}

	for _, tc := range []struct {
		why, container, path, want string
	}{
		{"no path", "loose", "", "path is required"},
		{"the root", "loose", "/", "path is the container's root"},
		{"what is not there", "loose", "/etc/absent", "loose has nothing at /etc/absent"},
		{"a directory at the root", "loose", "/srv", "/srv is a directory at the root of loose"},
		{"where a volume is mounted", "web-front-1", "/data", "/data is where the volume web_data is mounted in web-front-1"},
		{"in a stopped container", "crashed", "/x", "a file can be removed only of a container that is running"},
	} {
		if msg := suite.CallErr(t, "container_file_delete", map[string]any{"container": tc.container, "path": tc.path, "confirm": true}); !strings.Contains(msg, tc.want) {
			t.Errorf("removing %s = %q, want it to say %q", tc.why, msg, tc.want)
		}
	}
	dock.FailWhen(looseDownload, "path", "/etc/shut", http.StatusForbidden, "Permission denied to access the path")
	if msg := suite.CallErr(t, "container_file_delete", map[string]any{"container": "loose", "path": "/etc/shut", "confirm": true}); !strings.Contains(msg, "what is at /etc/shut in loose could not be seen, so nothing was removed") {
		t.Errorf("removing what cannot be looked at = %q", msg)
	}
	holds(t, dock, looseID, "/etc/keep", "keep", "x")
	dock.Then(remove, nil)
	dock.Fail(remove, http.StatusBadRequest, "Cannot delete critical system path")
	if msg := suite.CallErr(t, "container_file_delete", map[string]any{"container": "loose", "path": "/etc/keep", "confirm": true}); !strings.Contains(msg, "/etc/keep in loose was not removed") {
		t.Errorf("a removal Dockhand refused = %q", msg)
	}
}

func TestContainerFileUpload(t *testing.T) {
	t.Parallel()

	const upload = "POST /api/containers/" + looseID + "/files/upload"
	here := t.TempDir()
	one, two := filepath.Join(here, "one.txt"), filepath.Join(here, "two.bin")
	for file, holds := range map[string]string{one: "one\n", two: "\x00\x01two"} {
		if err := os.WriteFile(file, []byte(holds), 0o600); err != nil {
			t.Fatal(err)
		}
	}

	dock := withFiles(t)
	dock.Answer(upload, `{"success":true,"uploaded":["one.txt","two.bin"]}`)
	suite := session(t, dock, everything)

	dry := suite.Call(t, "container_file_upload", map[string]any{"container": "loose", "files": []any{one, two}, "to": "/etc/app", "dry_run": true})
	if got := acctest.Strs(t, dry["uploaded"], "uploaded"); !reflect.DeepEqual(got, []string{"one.txt", "two.bin"}) || acctest.Num(t, dry["bytes"], "bytes") != 9 || len(dock.Requests(upload)) != 0 {
		t.Errorf("a dry run = %v", dry)
	}

	// sent as the parts of a form, each under its own name, with whose they are to be
	out := suite.Call(t, "container_file_upload", map[string]any{"container": "loose", "files": []any{one, two}, "to": "/etc/app/", "owner": "app:app"})
	if got := acctest.Strs(t, out["uploaded"], "uploaded"); out["env"] != "alpha" || out["container"] != "loose" || out["to"] != "/etc/app" || len(got) != 2 || acctest.Num(t, out["bytes"], "bytes") != 9 || out["errors"] != nil || out["note"] != nil {
		t.Errorf("sending files = %v", out)
	}
	made := dock.Last(upload)
	if made.Query.Get("path") != "/etc/app" || made.Query.Get("env") != "1" || !strings.Contains(made.Body, `name="files"; filename="one.txt"`) || !strings.Contains(made.Body, "one\n") || !strings.Contains(made.Body, `filename="two.bin"`) || !strings.Contains(made.Body, "\x00\x01two") || !strings.Contains(made.Body, "name=\"owner\"\r\n\r\napp:app") {
		t.Errorf("sending files sent %q to %v", made.Body, made.Query)
	}

	// some put there and some not; and under a mount
	dock.Answer(upload, `{"success":false,"uploaded":["one.txt"],"errors":["two.bin: no space left on device"]}`)
	if out = suite.Call(t, "container_file_upload", map[string]any{"container": "loose", "files": []any{one, two}, "to": "/etc/app"}); len(acctest.Strs(t, out["errors"], "errors")) != 1 || out["note"] != "1 of the 2 files could not be put there: errors says why" || strings.Contains(dock.Last(upload).Body, `name="owner"`) {
		t.Errorf("files sent in part = %v", out)
	}
	holds(t, dock, frontID, "/data", "data/", "")
	if dry = suite.Call(t, "container_file_upload", map[string]any{"container": "web-front-1", "files": []any{one}, "to": "/data", "dry_run": true}); dry["note"] != "the directory is under a mount, so they are written to the volume web_data" {
		t.Errorf("files sent under a mount = %v", dry)
	}

	// a file of the same name is replaced only when asked, and a directory of the name never
	holds(t, dock, looseID, "/etc/app/one.txt", "one.txt", "old")
	if msg := suite.CallErr(t, "container_file_upload", map[string]any{"container": "loose", "files": []any{one}, "to": "/etc/app"}); !strings.Contains(msg, "loose has a file at /etc/app/one.txt already: overwrite replaces it") {
		t.Errorf("sending over a file unasked = %q", msg)
	}
	dock.Answer(upload, `{"success":true,"uploaded":["one.txt"]}`)
	if out = suite.Call(t, "container_file_upload", map[string]any{"container": "loose", "files": []any{one}, "to": "/etc/app", "overwrite": true}); len(acctest.Strs(t, out["uploaded"], "uploaded")) != 1 {
		t.Errorf("sending over a file = %v", out)
	}
	holds(t, dock, looseID, "/etc/app/two.bin", "two.bin/", "")
	if msg := suite.CallErr(t, "container_file_upload", map[string]any{"container": "loose", "files": []any{two}, "to": "/etc/app", "overwrite": true}); !strings.Contains(msg, "/etc/app/two.bin in loose is a directory") {
		t.Errorf("sending over a directory = %q", msg)
	}

	// refused before the container is asked anything
	big := filepath.Join(here, "big.bin")
	if err := os.WriteFile(big, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Truncate(big, maxUploadBytes+1); err != nil {
		t.Fatal(err)
	}
	again := filepath.Join(here, "sub", "one.txt")
	if err := os.MkdirAll(filepath.Dir(again), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(again, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		why  string
		args map[string]any
		want string
	}{
		{"nowhere to put them", map[string]any{"files": []any{one}, "to": ""}, "to is required"},
		{"no files", map[string]any{"files": []any{}, "to": "/etc/app"}, "files is required"},
		{"an owner that is none", map[string]any{"files": []any{one}, "to": "/etc/app", "owner": "a b"}, `owner is "a b"`},
		{"a path that is not whole", map[string]any{"files": []any{"one.txt"}, "to": "/etc/app"}, `files has "one.txt": a file is given as a full path`},
		{"a file that is not here", map[string]any{"files": []any{filepath.Join(here, "absent")}, "to": "/etc/app"}, "which cannot be read here"},
		{"a directory", map[string]any{"files": []any{here}, "to": "/etc/app"}, "which is no file: a directory is sent file by file"},
		{"two of one name", map[string]any{"files": []any{one, again}, "to": "/etc/app"}, "files has two named one.txt"},
		{"more than a call sends", map[string]any{"files": []any{big}, "to": "/etc/app"}, "the files are 67108865 bytes together"},
		{"a container there is none of", map[string]any{"container": "nope", "files": []any{one}, "to": "/etc/app"}, "nope"},
		{"a directory that is not there", map[string]any{"files": []any{one}, "to": "/nope"}, "loose has no directory /nope to put the files in"},
	} {
		given := map[string]any{"container": "loose"}
		maps.Copy(given, tc.args)
		if msg := suite.CallErr(t, "container_file_upload", given); !strings.Contains(msg, tc.want) {
			t.Errorf("%s = %q, want it to say %q", tc.why, msg, tc.want)
		}
	}
	dock.FailWhen(looseDownload, "path", "/etc/shut", http.StatusForbidden, "Permission denied to access the path")
	if msg := suite.CallErr(t, "container_file_upload", map[string]any{"container": "loose", "files": []any{two}, "to": "/etc/shut"}); !strings.Contains(msg, "what is at /etc/shut in loose could not be seen, so nothing was sent") {
		t.Errorf("sending to what cannot be looked at = %q", msg)
	}

	lacks(dock, "/etc/app/two.bin")
	dock.Fail(upload, http.StatusInternalServerError, "Failed to upload files")
	if msg := suite.CallErr(t, "container_file_upload", map[string]any{"container": "loose", "files": []any{two}, "to": "/etc/app"}); !strings.Contains(msg, "the files were not put in /etc/app in loose") {
		t.Errorf("an upload Dockhand refused = %q", msg)
	}
}

func TestContainerFileDownload(t *testing.T) {
	t.Parallel()

	here := t.TempDir()
	dock := withFiles(t)
	holds(t, dock, looseID, "/etc/app/link", "link", "->app.conf")
	suite := session(t, dock, Options{})

	// a file comes as itself, into a folder under its own name, for its owner alone
	out := suite.Call(t, "container_file_download", map[string]any{"container": "loose", "path": "/etc/app/app.conf", "to": here})
	saved := filepath.Join(here, "app.conf")
	if out["env"] != "alpha" || out["container"] != "loose" || out["path"] != "/etc/app/app.conf" || out["kind"] != "file" || out["saved"] != saved || acctest.Num(t, out["bytes"], "bytes") != 12 || out["note"] != nil {
		t.Errorf("fetching a file = %v", out)
	}
	if got, err := os.ReadFile(saved); err != nil || string(got) != "mode = demo\n" { //nolint:gosec // G304: the path is the test's own, under its own temporary folder
		t.Errorf("the file written = %q, %v", got, err)
	}
	if info, err := os.Stat(saved); err != nil || info.Mode().Perm() != 0o600 {
		t.Errorf("the file written is %v, want it its owner's alone: %v", info.Mode(), err)
	}
	// asked for gzipped, whatever is written: a plain archive is cut short by a Dockhand whose host is behind an agent
	if made := dock.Last(looseDownload); made.Query.Get("path") != "/etc/app/app.conf" || made.Query.Get("env") != "1" || made.Query.Get("format") != "tar.gz" {
		t.Errorf("fetching a file asked %v", made.Query)
	}
	// never over what is there
	if msg := suite.CallErr(t, "container_file_download", map[string]any{"container": "loose", "path": "/etc/app/app.conf", "to": saved}); !strings.Contains(msg, "it could not be written here") {
		t.Errorf("fetching over a file that is there = %q", msg)
	}

	// a directory comes as an archive, named for it: unpacked to a plain tar, or left gzipped when its name says so
	out = suite.Call(t, "container_file_download", map[string]any{"container": "loose", "path": "/etc/app", "to": here})
	if out["kind"] != "directory" || out["saved"] != filepath.Join(here, "app.tar") || !strings.Contains(acctest.Str(out["note"]), "a directory comes as a tar archive") {
		t.Errorf("fetching a directory = %v", out)
	}
	//nolint:gosec // G304: the path is the test's own, under its own temporary folder
	if got, err := os.ReadFile(filepath.Join(here, "app.tar")); err != nil || string(got) != archived(t, "app/", "", "app/app.conf", "mode = demo\n") {
		t.Errorf("the archive written is %d bytes, %v: want what Dockhand sent, whole", len(got), err)
	}
	out = suite.Call(t, "container_file_download", map[string]any{"container": "loose", "path": "/etc/app", "to": filepath.Join(here, "app.TGZ")})
	//nolint:gosec // G304: the path is the test's own, under its own temporary folder
	if got, err := os.ReadFile(filepath.Join(here, "app.TGZ")); err != nil || string(got) != zipped(t, archived(t, "app/", "", "app/app.conf", "mode = demo\n")) || out["saved"] != filepath.Join(here, "app.TGZ") {
		t.Errorf("fetching a directory gzipped = %v, written as %d bytes, %v", out, len(got), err)
	}
	// the root is named for the container
	holds(t, dock, looseID, "/", "/", "")
	if out = suite.Call(t, "container_file_download", map[string]any{"container": "loose", "path": "/", "to": here}); out["saved"] != filepath.Join(here, "loose.tar") {
		t.Errorf("fetching the root = %v", out)
	}

	for _, tc := range []struct {
		why, container, path, to, want string
	}{
		{"no path", "loose", "", here, "path is required"},
		{"nowhere whole to write it", "loose", "/etc/app/app.conf", "app.conf", `to is "app.conf"`},
		{"a container there is none of", "nope", "/x", here, "nope"},
		{"what is not there", "loose", "/etc/absent", here, "loose has nothing at /etc/absent"},
		{"a link", "loose", "/etc/app/link", here, "/etc/app/link in loose is a link or a device and no file"},
	} {
		if msg := suite.CallErr(t, "container_file_download", map[string]any{"container": tc.container, "path": tc.path, "to": tc.to}); !strings.Contains(msg, tc.want) {
			t.Errorf("fetching %s = %q, want it to say %q", tc.why, msg, tc.want)
		}
	}
	dock.FailWhen(looseDownload, "path", "/etc/shut", http.StatusForbidden, "Permission denied to access the path")
	if msg := suite.CallErr(t, "container_file_download", map[string]any{"container": "loose", "path": "/etc/shut", "to": here}); !strings.Contains(msg, "what is at /etc/shut in loose could not be seen") {
		t.Errorf("fetching what cannot be looked at = %q", msg)
	}

	// seen to be there, and then: refused, gone, no archive, and cut short, with the part written taken away again
	second := func(then func()) {
		asked := 0
		dock.Then(looseDownload, func(dockhandtest.Request) {
			asked++
			if asked == 2 {
				then()
			}
		})
	}
	second(func() {
		dock.FailWhen(looseDownload, "path", "/etc/app", http.StatusForbidden, "Permission denied to access the path")
	})
	if msg := suite.CallErr(t, "container_file_download", map[string]any{"container": "loose", "path": "/etc/app", "to": filepath.Join(here, "refused.tar.gz")}); !strings.Contains(msg, "/etc/app in loose could not be fetched") {
		t.Errorf("a fetch Dockhand refused = %q", msg)
	}
	holds(t, dock, looseID, "/etc/app", "app/", "")
	second(func() { lacks(dock, "/etc/app") })
	if msg := suite.CallErr(t, "container_file_download", map[string]any{"container": "loose", "path": "/etc/app", "to": filepath.Join(here, "gone.tar")}); !strings.Contains(msg, "/etc/app in loose was there a moment ago and could not be fetched") {
		t.Errorf("a fetch of what went in between = %q", msg)
	}
	for _, kind := range []struct{ path, to string }{{"/etc/app/app.conf", "odd.conf"}, {"/etc/odd-dir", "odd.tar"}} {
		holds(t, dock, looseID, "/etc/odd-dir", "odd-dir/", "")
		second(func() { dock.AnswerTextWhen(looseDownload, "path", kind.path, gzipType, "this is no archive") })
		if msg := suite.CallErr(t, "container_file_download", map[string]any{"container": "loose", "path": kind.path, "to": filepath.Join(here, kind.to)}); !strings.Contains(msg, "what Dockhand answered for "+kind.path+" is no archive") {
			t.Errorf("a fetch of %s answered with no archive = %q", kind.path, msg)
		}
	}
	dock.Then(looseDownload, nil)
	dock.AnswerTextWhen(looseDownload, "path", "/etc/app/long.txt", gzipType, zipped(t, archived(t, "long.txt", strings.Repeat("x", 4000))[:1024]))
	short := filepath.Join(here, "long.txt")
	if msg := suite.CallErr(t, "container_file_download", map[string]any{"container": "loose", "path": "/etc/app/long.txt", "to": short}); !strings.Contains(msg, "/etc/app/long.txt in loose was not all fetched, and the part written to "+short+" was removed") {
		t.Errorf("a fetch cut short = %q", msg)
	}
	if _, err := os.Stat(short); !os.IsNotExist(err) {
		t.Errorf("the part of a fetch cut short was left behind: %v", err)
	}
}
