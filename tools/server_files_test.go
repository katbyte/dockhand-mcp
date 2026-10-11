package tools

import (
	"net/http"
	"strings"
	"testing"

	"github.com/katbyte/dockhand-mcp/sdk/dockhandtest"
	"github.com/katbyte/go-kt/mcp/acctest"
)

// What a 1.0.51 server answers of its own filesystem, with invented names.
const (
	stacksDir = `{"path":"/srv/stacks","parent":"/srv","entries":[{"name":"readme.txt","path":"/srv/stacks/readme.txt","type":"file","size":120,"mtime":"2026-10-10T10:00:00.000Z","mode":"644"},{"name":"web","path":"/srv/stacks/web","type":"directory","size":4096,"mtime":"2026-10-10T11:00:00.000Z","mode":"755"},{"name":"api","path":"/srv/stacks/api","type":"directory","size":4096,"mtime":"2026-10-10T12:00:00.000Z","mode":"755"}]}`
	envFile   = `{"path":"/srv/stacks/web/.env","content":"TAG=1.2\nDB_PASSWORD=not-a-real-password\n","size":40,"mtime":"2026-10-10T11:00:00.000Z"}`

	browse = "GET /api/system/files"
	reads  = "GET /api/system/files/content"
	mkdir  = "POST /api/system/files"
)

func TestServerFiles(t *testing.T) {
	t.Parallel()

	dock := dockhandtest.New(t)
	dock.Answer(browse, stacksDir)
	suite := session(t, dock, Options{})

	// directories first and then by name, each with what it is
	out := suite.Call(t, "server_files", map[string]any{"path": " /srv/stacks/ "})
	entries := acctest.Rows(t, out["entries"], "entries")
	if out["path"] != "/srv/stacks" || acctest.Num(t, out["total"], "total") != 3 || len(entries) != 3 || entries[0]["name"] != "api" || entries[1]["name"] != "web" || entries[2]["name"] != "readme.txt" || out["note"] != nil || dock.Last(browse).Query.Get("path") != "/srv/stacks" {
		t.Fatalf("a directory = %v, asked with %v", out, dock.Last(browse).Query)
	}
	if file := entries[2]; file["type"] != "file" || acctest.Num(t, file["size"], "size") != 120 || file["mode"] != "644" || file["modified"] != "2026-10-10T10:00:00.000Z" {
		t.Errorf("a file in it = %v", file)
	}

	// the root unless a path is given, and no more entries than asked for
	if out = suite.Call(t, "server_files", map[string]any{"limit": 2}); dock.Last(browse).Query.Get("path") != "/" || len(acctest.Rows(t, out["entries"], "entries")) != 2 || acctest.Num(t, out["total"], "total") != 3 || !strings.Contains(acctest.Str(out["note"]), "the directory has 3 entries and the first 2 are listed") {
		t.Errorf("the root, two at most = %v, asked with %v", out, dock.Last(browse).Query)
	}

	for what, tc := range map[string]struct {
		status int
		want   string
	}{
		"one that is not there":        {http.StatusNotFound, "there is no /srv/stacks on Dockhand's own filesystem"},
		"a file":                       {http.StatusBadRequest, "is a file and not a directory: server_read reads it"},
		"a part Dockhand keeps back":   {http.StatusForbidden, "is not shown: Dockhand keeps parts of its own filesystem to itself"},
		"one that could not be listed": {http.StatusInternalServerError, "/srv/stacks could not be listed"},
	} {
		dock.Fail(browse, tc.status, "Access denied")
		if msg := suite.CallErr(t, "server_files", map[string]any{"path": "/srv/stacks"}); !strings.Contains(msg, tc.want) {
			t.Errorf("%s = %q, want %q", what, msg, tc.want)
		}
	}
	asked := len(dock.Requests(browse))
	if msg := suite.CallErr(t, "server_files", map[string]any{"path": "srv/stacks"}); !strings.Contains(msg, "it begins with a /") {
		t.Errorf("a path not from the root = %q", msg)
	}
	if msg := suite.CallErr(t, "server_files", map[string]any{"limit": -1}); !strings.Contains(msg, "limit is -1") || len(dock.Requests(browse)) != asked {
		t.Errorf("fewer entries than none = %q", msg)
	}
}

func TestServerRead(t *testing.T) {
	t.Parallel()

	dock := dockhandtest.New(t)
	dock.Answer(reads, envFile)
	suite := session(t, dock, Options{})

	// its text, with what is named a secret hidden unless asked for
	out := suite.Call(t, "server_read", map[string]any{"path": "/srv/stacks/web/../web/.env"})
	if out["path"] != "/srv/stacks/web/.env" || !strings.Contains(acctest.Str(out["content"]), "TAG=1.2") || strings.Contains(flat(out), "not-a-real-password") || acctest.Num(t, out["hidden"], "hidden") != 1 || out["modified"] != "2026-10-10T11:00:00.000Z" || !strings.Contains(acctest.Str(out["note"]), "hidden") || dock.Last(reads).Query.Get("path") != "/srv/stacks/web/.env" {
		t.Errorf("a file with a secret in it = %v", out)
	}
	if out = suite.Call(t, "server_read", map[string]any{"path": "/srv/stacks/web/.env", "reveal_secrets": true}); !strings.Contains(acctest.Str(out["content"]), "not-a-real-password") || out["hidden"] != nil || out["note"] != nil {
		t.Errorf("the same with its secrets asked for = %v", out)
	}
	if out = suite.Call(t, "server_read", map[string]any{"path": "/srv/stacks/web/.env", "max_chars": 7}); out["content"] != "TAG=1.2" || !acctest.IsBool(out["truncated"], true) || !strings.Contains(acctest.Str(out["note"]), "its first 7 are given") {
		t.Errorf("the start of it = %v", out)
	}

	// what is not text is not shown
	dock.Answer(reads, `{"path":"/srv/stacks/web/logo.png","content":"\u0000PNG\u0000","size":5,"mtime":"2026-10-10T11:00:00.000Z"}`)
	if msg := suite.CallErr(t, "server_read", map[string]any{"path": "/srv/stacks/web/logo.png"}); !strings.Contains(msg, "is not a text file") {
		t.Errorf("a picture = %q", msg)
	}
	for what, tc := range map[string]struct {
		status int
		says   string
		want   string
	}{
		"one that is not there":      {http.StatusNotFound, "File not found", "there is no file /srv/x on Dockhand's own filesystem"},
		"a directory":                {http.StatusBadRequest, "Cannot read directory as file: /srv/x", "/srv/x is a directory and not a file: server_files lists it"},
		"one too large":              {http.StatusBadRequest, "File too large (max 10MB)", "/srv/x is larger than the 10 MB Dockhand reads of a file"},
		"a part Dockhand keeps back": {http.StatusForbidden, "Access denied", "/srv/x is not shown: Dockhand keeps parts"},
		"one that could not be read": {http.StatusInternalServerError, "Failed to read file", "/srv/x could not be read"},
	} {
		dock.Fail(reads, tc.status, tc.says)
		if msg := suite.CallErr(t, "server_read", map[string]any{"path": "/srv/x"}); !strings.Contains(msg, tc.want) {
			t.Errorf("%s = %q, want %q", what, msg, tc.want)
		}
	}
	asked := len(dock.Requests(reads))
	for what, tc := range map[string]struct {
		args map[string]any
		want string
	}{
		"a blank path":             {map[string]any{"path": " "}, "path is required: the file to read"},
		"a path not from the root": {map[string]any{"path": "x"}, "it begins with a /"},
		"more than can be given":   {map[string]any{"path": "/x", "max_chars": 200001}, "max_chars is 200001"},
	} {
		if msg := suite.CallErr(t, "server_read", tc.args); !strings.Contains(msg, tc.want) {
			t.Errorf("%s = %q, want %q", what, msg, tc.want)
		}
	}
	if len(dock.Requests(reads)) != asked {
		t.Error("a file was asked for by a call that was refused")
	}
}

func TestServerMkdir(t *testing.T) {
	t.Parallel()

	dock := dockhandtest.New(t)
	dock.Fail(browse, http.StatusNotFound, "Path not found: /srv/stacks/new")
	dock.Answer(mkdir, `{"success":true,"path":"/srv/stacks/new"}`)
	suite := session(t, dock, everything)

	// a dry run looks and makes nothing
	out := suite.Call(t, "server_mkdir", map[string]any{"path": "/srv/stacks/new/", "dry_run": true})
	if changes := acctest.Strs(t, out["changes"], "changes"); len(changes) != 1 || changes[0] != "would make the directory /srv/stacks/new on Dockhand's own filesystem" || !acctest.IsBool(out["dry_run"], true) || len(dock.Requests(mkdir)) != 0 {
		t.Errorf("a dry run = %v, with %d made", out, len(dock.Requests(mkdir)))
	}
	out = suite.Call(t, "server_mkdir", map[string]any{"path": "/srv/stacks/new/"})
	if changes := acctest.Strs(t, out["changes"], "changes"); len(changes) != 1 || changes[0] != "made the directory /srv/stacks/new on Dockhand's own filesystem" || out["path"] != "/srv/stacks/new" || sent(t, dock.Last(mkdir))["path"] != "/srv/stacks/new" || out["note"] != nil {
		t.Errorf("a directory made = %v, sent %v", out, dock.Last(mkdir).Body)
	}

	// one that is there is left as it is
	made := len(dock.Requests(mkdir))
	dock.Answer(browse, `{"path":"/srv/stacks/new","parent":"/srv/stacks","entries":[]}`)
	if out = suite.Call(t, "server_mkdir", map[string]any{"path": "/srv/stacks/new"}); len(acctest.Strs(t, out["changes"], "changes")) != 0 || !strings.Contains(acctest.Str(out["note"]), "nothing to do") || len(dock.Requests(mkdir)) != made {
		t.Errorf("one that is there = %v", out)
	}

	for what, tc := range map[string]struct {
		status int
		want   string
	}{
		"a file in the way":              {http.StatusBadRequest, "is a file, so no directory can be made there"},
		"a part Dockhand keeps back":     {http.StatusForbidden, "/srv/stacks/new is not made: Dockhand keeps parts"},
		"a place that could not be seen": {http.StatusInternalServerError, "whether /srv/stacks/new is there could not be seen, so nothing was made"},
	} {
		dock.Fail(browse, tc.status, "Access denied")
		if msg := suite.CallErr(t, "server_mkdir", map[string]any{"path": "/srv/stacks/new"}); !strings.Contains(msg, tc.want) {
			t.Errorf("%s = %q, want %q", what, msg, tc.want)
		}
	}
	if msg := suite.CallErr(t, "server_mkdir", map[string]any{"path": " "}); !strings.Contains(msg, "path is required: the directory to make") || len(dock.Requests(mkdir)) != made {
		t.Errorf("a blank path = %q", msg)
	}

	// refused by Dockhand when it comes to making it, in three ways
	dock.Fail(browse, http.StatusNotFound, "Path not found")
	dock.Fail(mkdir, http.StatusForbidden, "Access denied")
	if msg := suite.CallErr(t, "server_mkdir", map[string]any{"path": "/srv/a/b"}); !strings.Contains(msg, "/srv/a/b was not made: Dockhand keeps parts") {
		t.Errorf("one made under a part Dockhand keeps back = %q", msg)
	}
	dock.Fail(mkdir, http.StatusInternalServerError, "Failed to create directory")
	if msg := suite.CallErr(t, "server_mkdir", map[string]any{"path": "/srv/a/b"}); !strings.Contains(msg, "/srv/a/b was not made") || !strings.Contains(msg, "Failed to create directory") {
		t.Errorf("one Dockhand could not make = %q", msg)
	}
	dock.Answer(mkdir, `{"success":false,"path":"/srv/a/b"}`)
	if msg := suite.CallErr(t, "server_mkdir", map[string]any{"path": "/srv/a/b"}); !strings.Contains(msg, "without its being said to have succeeded") {
		t.Errorf("one answered without success = %q", msg)
	}
}
