package tools

import (
	"net/http"
	"strings"
	"testing"

	"github.com/katbyte/dockhand-mcp/sdk/dockhandtest"
	"github.com/katbyte/go-kt/mcp/acctest"
)

// renamed has the canned alpha list a container under another name from when a call is made: how a rename comes to be read back.
func renamed(dock *dockhandtest.Server, call, was, is string) {
	dock.Then(call, func(dockhandtest.Request) {
		dock.AnswerWhen("GET /api/containers", "env", "1", strings.Replace(alphaContainers, `"name":"`+was+`"`, `"name":"`+is+`"`, 1))
	})
}

func TestContainerPauseAndUnpause(t *testing.T) {
	t.Parallel()

	const (
		pause   = "POST /api/containers/" + looseID + "/pause"
		unpause = "POST /api/containers/" + looseID + "/unpause"
	)

	dock := twoHosts(t)
	dock.Answer(pause, `{"success":true}`)
	dock.Answer(unpause, `{"success":true}`)
	suite := session(t, dock, everything)

	// said first, then held still and read back as paused
	dry := suite.Call(t, "container_pause", map[string]any{"container": "loose", "dry_run": true})
	if got := acctest.Strs(t, dry["changes"], "changes"); len(got) != 1 || got[0] != "would pause loose on alpha" || dry["is"] != "running" || len(dock.Requests(pause)) != 0 {
		t.Errorf("a dry run of pausing = %v", dry)
	}
	becomes(dock, pause, "loose", "running", "paused")
	out := suite.Call(t, "container_pause", map[string]any{"container": "loose"})
	if out["env"] != "alpha" || out["name"] != "loose" || out["was"] != "running" || out["is"] != "paused" || out["note"] != nil || dock.Last(pause).Query.Get("env") != "1" {
		t.Errorf("pausing = %v", out)
	}
	// held already, it is left as it is; and a container that is not running cannot be held
	if again := suite.Call(t, "container_pause", map[string]any{"container": "loose"}); len(acctest.Strs(t, again["changes"], "changes")) != 0 || !strings.Contains(acctest.Str(again["note"]), "loose is paused, and only a container that is running can be paused") || len(dock.Requests(pause)) != 1 {
		t.Errorf("pausing what is paused = %v", again)
	}
	if not := suite.Call(t, "container_pause", map[string]any{"container": "crashed"}); !strings.Contains(acctest.Str(not["note"]), "crashed is exited") {
		t.Errorf("pausing what is not running = %v", not)
	}

	// let go on, and read back as running
	becomes(dock, unpause, "loose", "paused", "running")
	out = suite.Call(t, "container_unpause", map[string]any{"container": "loose"})
	if got := acctest.Strs(t, out["changes"], "changes"); out["was"] != "paused" || out["is"] != "running" || len(got) != 1 || got[0] != "let go on loose on alpha" {
		t.Errorf("unpausing = %v", out)
	}
	if again := suite.Call(t, "container_unpause", map[string]any{"container": "loose"}); !strings.Contains(acctest.Str(again["note"]), "only a container that is paused can be let go on") || len(dock.Requests(unpause)) != 1 {
		t.Errorf("unpausing what is not paused = %v", again)
	}

	// refused by Docker, not as asked afterwards, and not readable afterwards
	dock.Then(pause, nil)
	dock.AnswerWhen("GET /api/containers", "env", "1", alphaContainers)
	dock.Fail(pause, http.StatusInternalServerError, "Failed to pause container")
	if msg := suite.CallErr(t, "container_pause", map[string]any{"container": "loose"}); !strings.Contains(msg, "loose on alpha was not paused") {
		t.Errorf("a pause Docker refused = %q", msg)
	}
	dock.Answer(pause, `{"success":true}`)
	if out = suite.Call(t, "container_pause", map[string]any{"container": "loose"}); out["is"] != "running" || !strings.Contains(acctest.Str(out["note"]), "it was paused and is running") {
		t.Errorf("a pause that did not take = %v", out)
	}
	dock.Then(pause, func(dockhandtest.Request) {
		dock.FailWhen("GET /api/containers", "env", "1", http.StatusInternalServerError, "Failed to list containers")
	})
	if out = suite.Call(t, "container_pause", map[string]any{"container": "loose", "env": "alpha"}); out["is"] != "" || !strings.Contains(acctest.Str(out["note"]), "its state after could not be read back") {
		t.Errorf("a pause that could not be read back = %v", out)
	}
}

func TestContainerRename(t *testing.T) {
	t.Parallel()

	const call = "POST /api/containers/" + looseID + "/rename"

	dock := twoHosts(t)
	dock.Answer(call, `{"success":true}`)
	suite := session(t, dock, everything)

	dry := suite.Call(t, "container_rename", map[string]any{"container": "loose", "name": "kept", "dry_run": true})
	if got := acctest.Strs(t, dry["changes"], "changes"); len(got) != 1 || got[0] != "would rename loose on alpha to kept" || dry["is"] != "loose" || len(dock.Requests(call)) != 0 {
		t.Errorf("a dry run = %v", dry)
	}
	renamed(dock, call, "loose", "kept")
	out := suite.Call(t, "container_rename", map[string]any{"container": "loose", "name": " /kept "})
	if out["env"] != "alpha" || out["was"] != "loose" || out["is"] != "kept" || out["note"] != nil || dock.Last(call).Body != `{"name":"kept"}` || dock.Last(call).Query.Get("env") != "1" {
		t.Errorf("a rename = %v, sent %s", out, dock.Last(call).Body)
	}

	// a container of a stack is said to be one, since the stack's next deploy will not know it
	dock = twoHosts(t)
	dock.Answer("POST /api/containers/"+frontID+"/rename", `{"success":true}`)
	suite = session(t, dock, everything)
	if out = suite.Call(t, "container_rename", map[string]any{"container": "web-front-1", "name": "front"}); !strings.Contains(acctest.Str(out["note"]), "it belongs to the stack web") || !strings.Contains(acctest.Str(out["note"]), "Dockhand said it renamed the container and it is named web-front-1") {
		t.Errorf("a stack's container renamed, and not read back as renamed = %v", out)
	}

	for _, tc := range []struct {
		args map[string]any
		want string
	}{
		{map[string]any{"container": "loose", "name": "no spaces"}, `name is "no spaces"`},
		{map[string]any{"container": "loose", "name": "x"}, `name is "x"`},
		{map[string]any{"container": "loose", "name": "crashed"}, "another container on alpha is named crashed already"},
		{map[string]any{"container": "nope", "name": "kept"}, "nope"},
	} {
		if msg := suite.CallErr(t, "container_rename", tc.args); !strings.Contains(msg, tc.want) {
			t.Errorf("container_rename %v = %q, want %q", tc.args, msg, tc.want)
		}
	}
	if same := suite.Call(t, "container_rename", map[string]any{"container": "loose", "name": "loose"}); same["note"] != "nothing was done: that is its name already" {
		t.Errorf("a rename to the name it has = %v", same)
	}

	dock.Fail("POST /api/containers/"+looseID+"/rename", http.StatusInternalServerError, "Failed to rename container")
	if msg := suite.CallErr(t, "container_rename", map[string]any{"container": "loose", "name": "kept"}); !strings.Contains(msg, "loose on alpha was not renamed") {
		t.Errorf("a rename Docker refused = %q", msg)
	}
	dock.Answer("POST /api/containers/"+looseID+"/rename", `{"success":true}`)
	dock.Then("POST /api/containers/"+looseID+"/rename", func(dockhandtest.Request) {
		dock.FailWhen("GET /api/containers", "env", "1", http.StatusInternalServerError, "Failed to list containers")
	})
	if out = suite.Call(t, "container_rename", map[string]any{"container": "loose", "name": "kept", "env": "alpha"}); out["is"] != "" || !strings.Contains(acctest.Str(out["note"]), "its name after could not be read back") {
		t.Errorf("a rename that could not be read back = %v", out)
	}
}

func TestContainerExec(t *testing.T) {
	t.Parallel()

	const call = "POST /api/containers/" + looseID + "/exec/run"

	dock := twoHosts(t)
	dock.Answer(call, `{"stdout":"MODE=demo\nAPI_KEY=hunter2\n","stderr":"","exitCode":0}`)
	suite := session(t, dock, everything)

	out := suite.Call(t, "container_exec", map[string]any{"container": "loose", "command": []any{"env"}, "user": " app ", "workdir": "/srv"})
	if out["env"] != "alpha" || out["container"] != "loose" || acctest.Num(t, out["exit_code"], "exit_code") != 0 || out["stdout"] != "MODE=demo\nAPI_KEY=***\n" || out["stderr"] != "" || acctest.Num(t, out["hidden"], "hidden") != 1 || !strings.Contains(acctest.Str(out["note"]), "1 secret is hidden") {
		t.Errorf("a command run = %v", out)
	}
	// the host is named envId on this one call, where every other names it env
	if ran := dock.Last(call); ran.Body != `{"cmd":["env"],"user":"app","workingDir":"/srv"}` || ran.Query.Get("envId") != "1" {
		t.Errorf("the command was sent as %s with %v", ran.Body, ran.Query)
	}
	if shown := suite.Call(t, "container_exec", map[string]any{"container": "loose", "command": []any{"env"}, "reveal_secrets": true}); shown["stdout"] != "MODE=demo\nAPI_KEY=hunter2\n" || shown["hidden"] != nil || shown["note"] != nil {
		t.Errorf("with reveal_secrets = %v", shown)
	}

	// a shell the container has not: Dockhand is asked which it has, and when it cannot say nothing is added
	const shells = "GET /api/containers/" + looseID + "/shells"
	dock.Answer(call, `{"stdout":"exec: \"bash\": executable file not found in $PATH","stderr":"","exitCode":127}`)
	dock.Answer(shells, `{"shells":["/bin/sh","/bin/ash"],"defaultShell":"/bin/sh","allShells":[{"path":"/bin/sh","label":"sh","available":true}]}`)
	if missing := suite.Call(t, "container_exec", map[string]any{"container": "loose", "command": []any{"/bin/bash", "-c", "true"}}); !strings.Contains(acctest.Str(missing["note"]), "the command exited with 127") || !strings.Contains(acctest.Str(missing["note"]), "the shells it has are /bin/sh and /bin/ash") || dock.Last(shells).Query.Get("env") != "1" {
		t.Errorf("a shell the container has not = %v", missing)
	}
	dock.Fail(shells, http.StatusInternalServerError, "Failed to detect shells")
	if missing := suite.Call(t, "container_exec", map[string]any{"container": "loose", "command": []any{"bash"}}); strings.Contains(acctest.Str(missing["note"]), "the shells it has") {
		t.Errorf("a shell the container has not, and no saying which it has = %v", missing)
	}
	if missing := suite.Call(t, "container_exec", map[string]any{"container": "loose", "command": []any{"curl"}}); strings.Contains(acctest.Str(missing["note"]), "shells") || len(dock.Requests(shells)) != 2 {
		t.Errorf("a command the container has not, which is no shell = %v", missing)
	}

	// a command that fails is an answer, with what it exited with and the end of what it printed
	dock.Answer(call, `{"stdout":"0123456789","stderr":"abcdefghij token=abc123","exitCode":3}`)
	out = suite.Call(t, "container_exec", map[string]any{"container": "loose", "command": []any{"sh", "-c", "exit 3"}, "max_chars": 4})
	note := acctest.Str(out["note"])
	if acctest.Num(t, out["exit_code"], "exit_code") != 3 || out["stdout"] != "6789" || out["stderr"] != "=***" || !acctest.IsBool(out["truncated"], true) || !strings.Contains(note, "it printed 10 characters and the last 4 are given") || !strings.Contains(note, "of errors and the last 4 are given") || !strings.Contains(note, "the command exited with 3") {
		t.Errorf("a command that failed = %v", out)
	}

	for _, tc := range []struct {
		args map[string]any
		want string
	}{
		{map[string]any{"container": "loose", "command": []any{}}, "command is required"},
		{map[string]any{"container": "loose", "command": []any{" "}}, "command is required"},
		{map[string]any{"container": "loose", "command": []any{"ls"}, "max_chars": -1}, "max_chars is -1"},
		{map[string]any{"container": "crashed", "command": []any{"ls"}}, "a command can be run only of a container that is running"},
	} {
		if msg := suite.CallErr(t, "container_exec", tc.args); !strings.Contains(msg, tc.want) {
			t.Errorf("container_exec %v = %q, want %q", tc.args, msg, tc.want)
		}
	}
	dock.Fail(call, http.StatusForbidden, "Permission denied")
	if msg := suite.CallErr(t, "container_exec", map[string]any{"container": "loose", "command": []any{"ls"}}); !strings.Contains(msg, "the command was not run in loose") {
		t.Errorf("a command Dockhand refused = %q", msg)
	}
}

func TestContainerDelete(t *testing.T) {
	t.Parallel()

	const (
		stopped = "DELETE /api/containers/" + crashedID
		live    = "DELETE /api/containers/" + frontID
	)

	dock := twoHosts(t)
	dock.Answer(stopped, `{"success":true}`)
	dock.Answer(live, `{"success":true}`)
	suite := session(t, dock, everything)

	// without confirm it says what would go and removes nothing
	out := suite.Call(t, "container_delete", map[string]any{"container": "crashed"})
	if out["env"] != "alpha" || out["name"] != "crashed" || out["state"] != "exited" || out["image"] != "registry.example/tool:latest" || acctest.BoolOf(out["removed"]) || out["note"] != "nothing was removed: call again with confirm to remove it" || len(dock.Requests(stopped)) != 0 {
		t.Errorf("a delete not confirmed = %v", out)
	}
	// confirmed, and still listed afterwards: Dockhand is not taken at its word
	if out = suite.Call(t, "container_delete", map[string]any{"container": "crashed", "confirm": true}); acctest.BoolOf(out["removed"]) || out["note"] != "Dockhand said it removed the container and it is still listed" || dock.Last(stopped).Query.Has("force") || dock.Last(stopped).Query.Get("env") != "1" {
		t.Errorf("a container that is still there = %v", out)
	}
	dock.Then(stopped, func(dockhandtest.Request) {
		dock.AnswerWhen("GET /api/containers", "env", "1", strings.Replace(alphaContainers, `"name":"crashed"`, `"name":"gone"`, 1))
		dock.AnswerWhen("GET /api/containers", "env", "1", strings.Replace(alphaContainers, crashedID, strings.Repeat("0", 64), 1))
	})
	if out = suite.Call(t, "container_delete", map[string]any{"container": "crashed", "confirm": true}); !acctest.BoolOf(out["removed"]) || out["note"] != nil {
		t.Errorf("a container removed = %v", out)
	}

	// a running one, of a stack, with a volume: what stays is said, and it takes force
	dock = twoHosts(t)
	dock.Answer(live, `{"success":true}`)
	suite = session(t, dock, everything)
	out = suite.Call(t, "container_delete", map[string]any{"container": "web-front-1"})
	if out["stack"] != "web" || out["state"] != "running" || strings.Join(acctest.Strs(t, out["volumes"], "volumes"), " ") != "web_data" || !strings.Contains(acctest.Str(out["note"]), "it belongs to the stack web, whose next deploy makes it again") || !strings.Contains(acctest.Str(out["note"]), "removing it takes force as well as confirm") {
		t.Errorf("what removing a running container would take = %v", out)
	}
	if msg := suite.CallErr(t, "container_delete", map[string]any{"container": "web-front-1", "confirm": true}); !strings.Contains(msg, "web-front-1 on alpha is running: stop it first, or give force") || len(dock.Requests(live)) != 0 {
		t.Errorf("a running container removed unforced = %q", msg)
	}
	if out = suite.Call(t, "container_delete", map[string]any{"container": "web-front-1", "force": true}); acctest.BoolOf(out["removed"]) || !strings.Contains(acctest.Str(out["note"]), "call again with confirm") || len(dock.Requests(live)) != 0 {
		t.Errorf("force without confirm = %v", out)
	}
	suite.Call(t, "container_delete", map[string]any{"container": "web-front-1", "confirm": true, "force": true})
	if dock.Last(live).Query.Get("force") != "true" {
		t.Errorf("a forced delete was asked with %v", dock.Last(live).Query)
	}

	dock.Fail(live, http.StatusInternalServerError, "Failed to remove container")
	if msg := suite.CallErr(t, "container_delete", map[string]any{"container": "web-front-1", "confirm": true, "force": true}); !strings.Contains(msg, "web-front-1 on alpha was not removed") {
		t.Errorf("a delete Docker refused = %q", msg)
	}
	dock.Answer(live, `{"success":true}`)
	dock.Then(live, func(dockhandtest.Request) {
		dock.FailWhen("GET /api/containers", "env", "1", http.StatusInternalServerError, "Failed to list containers")
	})
	if out = suite.Call(t, "container_delete", map[string]any{"container": "web-front-1", "env": "alpha", "confirm": true, "force": true}); acctest.BoolOf(out["removed"]) || !strings.Contains(acctest.Str(out["note"]), "that it is gone could not be read back") {
		t.Errorf("a delete that could not be read back = %v", out)
	}
}

func TestContainerPrune(t *testing.T) {
	t.Parallel()

	const call = "POST /api/prune/containers"

	dock := twoHosts(t)
	dock.Answer(call, `{"success":true,"result":{"ContainersDeleted":["`+crashedID+`","ffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffff"],"SpaceReclaimed":3145728}}`)
	suite := session(t, dock, everything)

	// without confirm it lists what is not running, and removes nothing
	out := suite.Call(t, "container_prune", map[string]any{"env": "alpha"})
	would := acctest.Strs(t, out["would_go"], "would_go")
	if out["env"] != "alpha" || len(would) != 3 || would[0] != "crashed (exited)" || would[1] != "done (exited)" || would[2] != "stopped (exited)" || len(acctest.Strs(t, out["removed"], "removed")) != 0 || !strings.Contains(acctest.Str(out["note"]), "call again with confirm") || len(dock.Requests(call)) != 0 {
		t.Errorf("what a prune would take = %v", out)
	}
	// confirmed: what Docker says it removed, by name where the name is known
	out = suite.Call(t, "container_prune", map[string]any{"env": "alpha", "confirm": true})
	if got := strings.Join(acctest.Strs(t, out["removed"], "removed"), " "); got != "crashed ffffffffffff" || acctest.Decimal(t, out["freed_mb"], "freed_mb") != 3.1 || out["would_go"] != nil || out["note"] != nil || dock.Last(call).Query.Get("env") != "1" {
		t.Errorf("a prune = %v", out)
	}

	// a host with nothing stopped has nothing to prune
	if none := suite.Call(t, "container_prune", map[string]any{"env": "beta", "confirm": true}); !strings.Contains(acctest.Str(none["note"]), "nothing to remove: every container on beta is running") || len(dock.Requests(call)) != 1 {
		t.Errorf("a prune of a host with nothing stopped = %v", none)
	}

	dock.Answer(call, `{"success":false}`)
	if msg := suite.CallErr(t, "container_prune", map[string]any{"env": "alpha", "confirm": true}); !strings.Contains(msg, "the stopped containers of alpha were not removed, and Dockhand gives no reason") {
		t.Errorf("a prune that did not succeed = %q", msg)
	}
	dock.Fail(call, http.StatusForbidden, "Permission denied")
	if msg := suite.CallErr(t, "container_prune", map[string]any{"env": "alpha", "confirm": true}); !strings.Contains(msg, "the stopped containers of alpha were not removed") || !strings.Contains(msg, "Permission denied") {
		t.Errorf("a prune Dockhand refused = %q", msg)
	}
	if msg := suite.CallErr(t, "container_prune", map[string]any{"confirm": true}); !strings.Contains(msg, "env is required") {
		t.Errorf("a prune of no host = %q", msg)
	}
	dock.FailWhen("GET /api/containers", "env", "1", http.StatusInternalServerError, "Failed to list containers")
	if msg := suite.CallErr(t, "container_prune", map[string]any{"env": "alpha"}); !strings.Contains(msg, "the containers of alpha could not be listed, so nothing was removed") {
		t.Errorf("containers that cannot be listed = %q", msg)
	}
}
