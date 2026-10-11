package tools

import (
	"net/http"
	"strings"
	"testing"

	"github.com/katbyte/dockhand-mcp/sdk/dockhandtest"
	"github.com/katbyte/go-kt/mcp/acctest"
)

const (
	looseID   = "cccccccccccc3333333333333333333333333333333333333333333333333333"
	stoppedID = "eeeeeeeeeeee5555555555555555555555555555555555555555555555555555"
)

// becomes has the canned server list a container of alpha's in another state from when a call is made: how a change comes to be read back.
func becomes(dock *dockhandtest.Server, call, name, was, is string) {
	dock.Then(call, func(dockhandtest.Request) {
		dock.AnswerWhen("GET /api/containers", "env", "1", strings.Replace(alphaContainers, `"name":"`+name+`","image":"registry.example/tool:latest","imageId":"sha256:cafe","command":"sleep 100000","created":1791615492,"state":"`+was+`"`, `"name":"`+name+`","image":"registry.example/tool:latest","imageId":"sha256:cafe","command":"sleep 100000","created":1791615492,"state":"`+is+`"`, 1))
	})
}

func TestContainerStartStopRestart(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		tool, container, id, action, was, is, change string
	}{
		{"container_stop", "loose", looseID, "stop", "running", "exited", "stopped loose on alpha"},
		{"container_start", "stopped", stoppedID, "start", "exited", "running", "started stopped on alpha"},
		{"container_restart", "loose", looseID, "restart", "running", "running", "restarted loose on alpha"},
	} {
		dock := twoHosts(t)
		call := "POST /api/containers/" + tc.id + "/" + tc.action
		dock.Answer(call, `{"success":true}`)
		becomes(dock, call, tc.container, tc.was, tc.is)

		out := session(t, dock, everything).Call(t, tc.tool, map[string]any{"container": tc.container})
		if made := dock.Last(call); made.Query.Get("env") != "1" {
			t.Errorf("%s: asked on environment %q, want the one the container is on", tc.tool, made.Query.Get("env"))
		}
		if out["env"] != "alpha" || out["name"] != tc.container || out["was"] != tc.was || out["is"] != tc.is || strings.Join(acctest.Strs(t, out["changes"], "changes"), "|") != tc.change || out["dry_run"] != nil || out["note"] != nil {
			t.Errorf("%s = %v", tc.tool, out)
		}
	}
}

// What is already so is left so, and said to be: no call is made to start what runs or to stop what does not.
func TestContainerActionsThatNeedNothingDone(t *testing.T) {
	t.Parallel()

	dock := twoHosts(t)
	suite := session(t, dock, everything)

	out := suite.Call(t, "container_start", map[string]any{"container": "loose"})
	if len(acctest.Strs(t, out["changes"], "changes")) != 0 || out["was"] != "running" || out["is"] != "running" || !strings.Contains(acctest.Str(out["note"]), "loose is already running: nothing was done") {
		t.Errorf("starting what runs = %v", out)
	}
	// one Docker is starting again by itself is not started over it
	out = suite.Call(t, "container_start", map[string]any{"container": "looping"})
	if len(acctest.Strs(t, out["changes"], "changes")) != 0 || !strings.Contains(acctest.Str(out["note"]), "already restarting") {
		t.Errorf("starting what Docker is restarting = %v", out)
	}
	out = suite.Call(t, "container_stop", map[string]any{"container": "crashed"})
	if len(acctest.Strs(t, out["changes"], "changes")) != 0 || !strings.Contains(acctest.Str(out["note"]), "crashed is not running, it is exited: nothing was done") {
		t.Errorf("stopping what does not run = %v", out)
	}

	for _, call := range dock.Calls() {
		if strings.HasPrefix(call, "POST ") {
			t.Errorf("%s was made, for something that needed nothing done", call)
		}
	}
}

func TestContainerActionsDryRun(t *testing.T) {
	t.Parallel()

	dock := twoHosts(t)
	suite := session(t, dock, everything)

	for tool, want := range map[string]string{
		"container_stop":    "would stop loose on alpha, which is running",
		"container_restart": "would restart loose on alpha, which is running",
	} {
		out := suite.Call(t, tool, map[string]any{"container": "loose", "dry_run": true})
		if strings.Join(acctest.Strs(t, out["changes"], "changes"), "|") != want || !acctest.IsBool(out["dry_run"], true) || out["is"] != "running" {
			t.Errorf("%s as a dry run = %v", tool, out)
		}
	}
	out := suite.Call(t, "container_start", map[string]any{"container": "stopped", "dry_run": true})
	if strings.Join(acctest.Strs(t, out["changes"], "changes"), "|") != "would start stopped on alpha, which is exited" {
		t.Errorf("container_start as a dry run = %v", out)
	}

	for _, call := range dock.Calls() {
		if strings.HasPrefix(call, "POST ") {
			t.Errorf("%s was made by a dry run", call)
		}
	}
}

// An action Dockhand refuses, one it does not say succeeded, and one that did not take are each said for what they are.
func TestContainerActionsThatGoWrong(t *testing.T) {
	t.Parallel()

	dock := twoHosts(t)
	suite := session(t, dock, everything)

	dock.Fail("POST /api/containers/"+looseID+"/restart", http.StatusInternalServerError, "Failed to restart container")
	if msg := suite.CallErr(t, "container_restart", map[string]any{"container": "loose"}); !strings.Contains(msg, "loose on alpha was not restarted") || !strings.Contains(msg, "Failed to restart container") {
		t.Errorf("a restart Dockhand fails = %q", msg)
	}
	dock.Fail("POST /api/containers/"+looseID+"/stop", http.StatusForbidden, "Permission denied")
	if msg := suite.CallErr(t, "container_stop", map[string]any{"container": "loose"}); !strings.Contains(msg, "was not stopped") || !strings.Contains(msg, "Permission denied") || !strings.Contains(msg, "the role of the user") {
		t.Errorf("a stop the account may not make = %q", msg)
	}
	dock.Answer("POST /api/containers/"+stoppedID+"/start", `{"success":false}`)
	if msg := suite.CallErr(t, "container_start", map[string]any{"container": "stopped"}); !strings.Contains(msg, "without its being said to have succeeded") {
		t.Errorf("a start answered without success = %q", msg)
	}

	// started, and exited again at once: the call went through and the container is not running, which is said
	dock.Answer("POST /api/containers/"+stoppedID+"/start", `{"success":true}`)
	out := suite.Call(t, "container_start", map[string]any{"container": "stopped"})
	if out["is"] != "exited" || len(acctest.Strs(t, out["changes"], "changes")) != 1 || !strings.Contains(acctest.Str(out["note"]), "it was started and is exited all the same") {
		t.Errorf("a start that did not take = %v", out)
	}

	// done, and the host cannot be read afterwards: the change is still said, with that its state is not known
	dock.Answer("POST /api/containers/"+looseID+"/restart", `{"success":true}`)
	dock.Then("POST /api/containers/"+looseID+"/restart", func(dockhandtest.Request) {
		dock.FailWhen("GET /api/containers", "env", "1", http.StatusInternalServerError, "Failed to list containers")
	})
	out = suite.Call(t, "container_restart", map[string]any{"container": "loose"})
	if out["is"] != "" || len(acctest.Strs(t, out["changes"], "changes")) != 1 || !strings.Contains(acctest.Str(out["note"]), "its state after could not be read back") {
		t.Errorf("a restart whose result cannot be read = %v", out)
	}
}

// stackBecomes has the canned server list alpha's web stack in another state from when a call is made.
func stackBecomes(dock *dockhandtest.Server, call, is string) {
	dock.Then(call, func(dockhandtest.Request) {
		dock.AnswerWhen("GET /api/stacks", "env", "1", strings.Replace(alphaStacks, `{"name":"web","status":"running"`, `{"name":"web","status":"`+is+`"`, 1))
	})
}

func TestStackStartStopRestart(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		tool, stack, action, was, is, answer, change string
	}{
		{"stack_stop", "web", "stop", "running", "stopped", `{"success":true,"output":"stop completed for 2 container(s): web-front-1, web-back-1"}`, "stopped web on alpha: stop completed for 2 container(s): web-front-1, web-back-1"},
		{"stack_restart", "web", "restart", "running", "running", `{"success":true,"output":"restart completed for 2 container(s): web-front-1, web-back-1"}`, "restarted web on alpha: restart completed for 2 container(s): web-front-1, web-back-1"},
		{"stack_start", "mail", "start", "partial", "partial", `{"success":true,"output":"start completed for 2 container(s): mail-smtp-1, mail-spool-1"}`, "started mail on alpha: start completed for 2 container(s): mail-smtp-1, mail-spool-1"},
	} {
		dock := twoHosts(t)
		call := "POST /api/stacks/" + tc.stack + "/" + tc.action
		dock.Answer(call, tc.answer)
		if tc.stack == "web" {
			stackBecomes(dock, call, tc.is)
		}

		out := session(t, dock, everything).Call(t, tc.tool, map[string]any{"stack": tc.stack})
		if made := dock.Last(call); made.Query.Get("env") != "1" {
			t.Errorf("%s: asked on environment %q", tc.tool, made.Query.Get("env"))
		}
		if out["env"] != "alpha" || out["name"] != tc.stack || out["was"] != tc.was || out["is"] != tc.is || strings.Join(acctest.Strs(t, out["changes"], "changes"), "|") != tc.change {
			t.Errorf("%s = %v", tc.tool, out)
		}
		// a stack started that is still not wholly running says so, with where to look
		if tc.tool == "stack_start" && !strings.Contains(acctest.Str(out["note"]), "it was started and is partial") {
			t.Errorf("a start that left the stack partial = %v", out["note"])
		}
		if tc.tool != "stack_start" && out["note"] != nil {
			t.Errorf("%s said %q of an action that went as asked", tc.tool, out["note"])
		}
	}
}

func TestStackActionsThatNeedNothingDoneOrDoNothing(t *testing.T) {
	t.Parallel()

	dock := twoHosts(t)
	suite := session(t, dock, everything)

	out := suite.Call(t, "stack_start", map[string]any{"stack": "web"})
	if len(acctest.Strs(t, out["changes"], "changes")) != 0 || !strings.Contains(acctest.Str(out["note"]), "every container of web is already running") {
		t.Errorf("starting a stack that runs = %v", out)
	}
	out = suite.Call(t, "stack_stop", map[string]any{"stack": "archive"})
	if len(acctest.Strs(t, out["changes"], "changes")) != 0 || !strings.Contains(acctest.Str(out["note"]), "no container of archive is running") {
		t.Errorf("stopping a stack that is stopped = %v", out)
	}
	out = suite.Call(t, "stack_stop", map[string]any{"stack": "web", "dry_run": true})
	if strings.Join(acctest.Strs(t, out["changes"], "changes"), "|") != "would stop the 2 containers of web on alpha, which is running" || !acctest.IsBool(out["dry_run"], true) {
		t.Errorf("stopping a stack as a dry run = %v", out)
	}

	for _, call := range dock.Calls() {
		if strings.HasPrefix(call, "POST ") {
			t.Errorf("%s was made, for something that needed nothing done or was a dry run", call)
		}
	}
}

// Dockhand answers an action it could not do with HTTP 200 and success false, so that is read, and its reason handed on.
func TestStackActionsThatGoWrong(t *testing.T) {
	t.Parallel()

	dock := twoHosts(t)
	suite := session(t, dock, everything)

	dock.Answer("POST /api/stacks/web/restart", `{"success":false,"error":"No containers found for stack \"web\""}`)
	if msg := suite.CallErr(t, "stack_restart", map[string]any{"stack": "web"}); !strings.Contains(msg, "web on alpha was not restarted: Dockhand says") || !strings.Contains(msg, `No containers found for stack "web"`) {
		t.Errorf("a restart answered success false = %q", msg)
	}
	dock.Answer("POST /api/stacks/web/stop", `{"success":false}`)
	if msg := suite.CallErr(t, "stack_stop", map[string]any{"stack": "web"}); !strings.Contains(msg, "it could not, and gives no reason") {
		t.Errorf("a stop answered success false and no reason = %q", msg)
	}
	dock.Fail("POST /api/stacks/mail/start", http.StatusForbidden, "Permission denied")
	if msg := suite.CallErr(t, "stack_start", map[string]any{"stack": "mail"}); !strings.Contains(msg, "mail on alpha was not started") || !strings.Contains(msg, "Permission denied") {
		t.Errorf("a start the account may not make = %q", msg)
	}

	for what, tc := range map[string]struct {
		args map[string]any
		want string
	}{
		"no stack named":          {map[string]any{"stack": ""}, "stack is required"},
		"a stack that is none":    {map[string]any{"stack": "nothing-by-this-name"}, `no stack is named "nothing-by-this-name"`},
		"a stack on another host": {map[string]any{"stack": "web", "env": "beta"}, `no stack is named "web"`},
	} {
		if msg := suite.CallErr(t, "stack_restart", tc.args); !strings.Contains(msg, tc.want) {
			t.Errorf("%s = %q, want %q", what, msg, tc.want)
		}
	}

	// done, and the stacks cannot be listed afterwards
	dock.Answer("POST /api/stacks/web/restart", `{"success":true,"output":"restart completed for 2 container(s)"}`)
	dock.Then("POST /api/stacks/web/restart", func(dockhandtest.Request) {
		dock.FailWhen("GET /api/stacks", "env", "1", http.StatusInternalServerError, "Failed to list stacks")
	})
	out := suite.Call(t, "stack_restart", map[string]any{"stack": "web"})
	if out["is"] != "" || len(acctest.Strs(t, out["changes"], "changes")) != 1 || !strings.Contains(acctest.Str(out["note"]), "its state after could not be read back") {
		t.Errorf("a restart whose result cannot be read = %v", out)
	}
}

// The tools that change something are not there until they are asked for.
func TestActionsNeedWritesEnabled(t *testing.T) {
	t.Parallel()

	read := register(t, Options{})
	for _, name := range []string{"container_start", "container_stop", "container_restart", "stack_start", "stack_stop", "stack_restart"} {
		for _, registered := range read {
			if registered == name {
				t.Errorf("%s is registered with writes not enabled", name)
			}
		}
	}
	if msg := session(t, twoHosts(t), Options{}).CallErr(t, "container_restart", map[string]any{"container": "loose"}); !strings.Contains(msg, "container_restart") {
		t.Errorf("a write tool called in a session that only reads = %q", msg)
	}
}
