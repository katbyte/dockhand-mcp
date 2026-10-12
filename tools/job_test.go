package tools

import (
	"net/http"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/katbyte/dockhand-mcp/sdk/dockhand"
	"github.com/katbyte/dockhand-mcp/sdk/dockhandtest"
	"github.com/katbyte/go-kt/mcp/acctest"
)

// A job as Dockhand answers one: a pull under way, the same pull done, one that failed, and work that reports text of its own.
const (
	pullRunning = `{"id":"j1","status":"running","lines":[{"event":"progress","data":{"status":"Pulling from library/tool","id":"latest"}},{"event":"progress","data":{"status":"Downloading","progressDetail":{"current":1048576,"total":8388608},"id":"0a1b2c3d4e5f"}},{"event":"progress","data":{"status":"Downloading","progressDetail":{"current":4194304,"total":8388608},"id":"0a1b2c3d4e5f"}}],"result":null}`
	pullDone    = `{"id":"j1","status":"done","lines":[{"event":"progress","data":{"status":"Pulling from library/tool","id":"latest"}},{"event":"progress","data":{"status":"Pull complete","progressDetail":{},"id":"0a1b2c3d4e5f"}},{"event":"progress","data":{"status":"Status: Downloaded newer image for registry.example/tool:latest"}},{"event":"result","data":{"status":"complete"}}],"result":{"status":"complete"}}`
	pullFailed  = `{"id":"j1","status":"error","lines":[{"event":"progress","data":{"status":"error","error":"Error: Failed to pull image: {\"message\":\"failed to resolve reference \\\"registry.example/tool:latest\\\": not found\"}\n"}},{"event":"result","data":{"status":"error","error":"Error: Failed to pull image: {\"message\":\"failed to resolve reference \\\"registry.example/tool:latest\\\": not found\"}\n"}}],"result":{"success":false,"error":"Failed to pull image: {\"message\":\"failed to resolve reference \\\"registry.example/tool:latest\\\": not found\"}\n"}}`
)

// quick registers every tool with a job asked how it goes every millisecond, so that a test that waits on one does not.
var quick = Options{EnableWrite: true, EnableDelete: true, poll: time.Millisecond}

func TestJobGet(t *testing.T) {
	t.Parallel()

	dock := dockhandtest.New(t)
	suite := session(t, dock, Options{})

	// under way: its id to ask again with, and the last of what it said, a layer's download said once
	dock.Answer("GET /api/jobs/j1", pullRunning)
	out := suite.Call(t, "job_get", map[string]any{"job": " j1 "})
	if out["id"] != "j1" || out["job"] != "j1" || out["status"] != "running" || !acctest.IsBool(out["running"], true) || out["result"] != nil || out["error"] != nil || !strings.Contains(acctest.Str(out["note"]), "still running") {
		t.Errorf("a job under way = %v", out)
	}
	if said := strings.Join(acctest.Strs(t, out["progress"], "progress"), "|"); said != "latest: Pulling from library/tool|0a1b2c3d4e5f: Downloading" {
		t.Errorf("its progress = %q", said)
	}
	if len(dock.Requests("GET /api/jobs/j1")) != 1 {
		t.Errorf("asked with no wait, the job was asked %d times, want once", len(dock.Requests("GET /api/jobs/j1")))
	}

	// done: what it answered, and no id to follow, since there is nothing left to follow
	dock.Answer("GET /api/jobs/j1", pullDone)
	out = suite.Call(t, "job_get", map[string]any{"job": "j1"})
	if out["status"] != "done" || !acctest.IsBool(out["running"], false) || out["job"] != nil || out["error"] != nil || out["note"] != nil || acctest.Object(t, out["result"], "result")["status"] != "complete" {
		t.Errorf("a job that is done = %v", out)
	}
	if said := acctest.Strs(t, out["progress"], "progress"); len(said) != 3 || said[2] != "Status: Downloaded newer image for registry.example/tool:latest" {
		t.Errorf("its progress = %q", said)
	}

	// ended in an error: why, in Docker's words out of the JSON they came in
	dock.Answer("GET /api/jobs/j1", pullFailed)
	out = suite.Call(t, "job_get", map[string]any{"job": "j1"})
	if out["status"] != "error" || !acctest.IsBool(out["running"], false) || out["error"] != `Failed to pull image: failed to resolve reference "registry.example/tool:latest": not found` {
		t.Errorf("a job that failed = %v", out)
	}

	// done, and its result says it did not do what it was for: an error all the same
	dock.Answer("GET /api/jobs/j1", `{"id":"j1","status":"done","lines":[],"result":{"success":false,"error":"No containers found for stack \"web\""}}`)
	if out = suite.Call(t, "job_get", map[string]any{"job": "j1"}); out["status"] != "error" || out["error"] != `No containers found for stack "web"` {
		t.Errorf("a job done without success = %v", out)
	}
	// and one that failed and said nothing of why
	dock.Answer("GET /api/jobs/j1", `{"id":"j1","status":"error","lines":[],"result":null}`)
	if out = suite.Call(t, "job_get", map[string]any{"job": "j1"}); out["error"] != "Dockhand gave no reason" {
		t.Errorf("a job that failed without a word = %v", out)
	}
}

// Told to wait, job_get holds its answer until the job ends, asking as it goes; and gives up when the wait is over with the job still running.
func TestJobGetWaits(t *testing.T) {
	t.Parallel()

	dock := dockhandtest.New(t)
	suite := session(t, dock, quick)

	asked := 0
	dock.Answer("GET /api/jobs/j1", pullRunning)
	dock.Then("GET /api/jobs/j1", func(dockhandtest.Request) {
		asked++
		if asked == 3 {
			dock.Answer("GET /api/jobs/j1", pullDone)
		}
	})
	if out := suite.Call(t, "job_get", map[string]any{"job": "j1", "wait": 30}); out["status"] != "done" || !acctest.IsBool(out["running"], false) || asked != 3 {
		t.Errorf("waited on, the job = %v after %d asks, want it done on the third", out, asked)
	}

	// a job that outlasts the wait is asked again and again until the wait is over, and then answered as it stands
	dock.Then("GET /api/jobs/j1", nil)
	dock.Answer("GET /api/jobs/j1", pullRunning)
	before := len(dock.Requests("GET /api/jobs/j1"))
	out := suite.Call(t, "job_get", map[string]any{"job": "j1", "wait": 1})
	if out["status"] != "running" || !acctest.IsBool(out["running"], true) || out["job"] != "j1" {
		t.Errorf("a job that outlasts the wait = %v", out)
	}
	if asks := len(dock.Requests("GET /api/jobs/j1")) - before; asks < 10 {
		t.Errorf("in a wait of one second the job was asked %d times, want it asked over and over", asks)
	}
}

func TestJobGetRefusals(t *testing.T) {
	t.Parallel()

	dock := dockhandtest.New(t)
	suite := session(t, dock, Options{})

	for name, tc := range map[string]struct {
		args map[string]any
		want string
	}{
		"no job":            {map[string]any{"job": "  "}, "job is required"},
		"a wait below none": {map[string]any{"job": "j1", "wait": -1}, "wait is -1: it is the seconds to wait, from 0 to 300"},
		"a wait too long":   {map[string]any{"job": "j1", "wait": 301}, "wait is 301"},
	} {
		if msg := suite.CallErr(t, "job_get", tc.args); !strings.Contains(msg, tc.want) {
			t.Errorf("%s = %q, want %q", name, msg, tc.want)
		}
	}
	if calls := dock.Calls(); len(calls) != 0 {
		t.Errorf("a call that was refused for its arguments still asked Dockhand: %v", calls)
	}

	// one Dockhand has forgotten, or that is another user's
	dock.Fail("GET /api/jobs/gone", http.StatusNotFound, "Job not found")
	if msg := suite.CallErr(t, "job_get", map[string]any{"job": "gone"}); !strings.Contains(msg, "no job gone is known to Dockhand") || !strings.Contains(msg, "only to whoever started it") {
		t.Errorf("a job that is not there = %q", msg)
	}
	dock.Fail("GET /api/jobs/j1", http.StatusForbidden, "Permission denied")
	if msg := suite.CallErr(t, "job_get", map[string]any{"job": "j1"}); !strings.Contains(msg, "job j1 could not be asked how it goes") || !strings.Contains(msg, "the role of the user") {
		t.Errorf("a job that may not be read = %q", msg)
	}
	dock.Answer("GET /api/jobs/j1", `null`)
	if msg := suite.CallErr(t, "job_get", map[string]any{"job": "j1"}); !strings.Contains(msg, "answered nothing that says") {
		t.Errorf("a job answered with nothing = %q", msg)
	}
}

func TestJobCancel(t *testing.T) {
	t.Parallel()

	dock := dockhandtest.New(t)
	suite := session(t, dock, everything)

	// asked to stop and stopped: read back as ended
	dock.Answer("GET /api/jobs/j1", pullRunning)
	dock.Answer("DELETE /api/jobs/j1", `{"cancelled":true}`)
	dock.Then("DELETE /api/jobs/j1", func(dockhandtest.Request) {
		dock.Answer("GET /api/jobs/j1", `{"id":"j1","status":"error","lines":[],"result":{"success":false,"error":"Cancelled"}}`)
	})
	if out := suite.Call(t, "job_cancel", map[string]any{"job": "j1"}); out["id"] != "j1" || out["was"] != "running" || out["is"] != "error" || !acctest.IsBool(out["cancelled"], true) || out["note"] != nil {
		t.Errorf("a job that was stopped = %v", out)
	}

	// asked to stop and running on, as a pull does: said, rather than called stopped
	dock.Then("DELETE /api/jobs/j1", nil)
	dock.Answer("GET /api/jobs/j1", pullRunning)
	if out := suite.Call(t, "job_cancel", map[string]any{"job": "j1"}); out["was"] != "running" || out["is"] != "running" || !acctest.IsBool(out["cancelled"], true) || !strings.Contains(acctest.Str(out["note"]), "asked to stop and still running") {
		t.Errorf("a job that runs on = %v", out)
	}

	// one that has ended is left alone: nothing is asked of Dockhand but how it stands
	asked := len(dock.Requests("DELETE /api/jobs/j1"))
	dock.Answer("GET /api/jobs/j1", pullDone)
	if out := suite.Call(t, "job_cancel", map[string]any{"job": "j1"}); out["was"] != "done" || out["is"] != "done" || !acctest.IsBool(out["cancelled"], false) || !strings.Contains(acctest.Str(out["note"]), "already ended") {
		t.Errorf("a job that had ended = %v", out)
	}
	if len(dock.Requests("DELETE /api/jobs/j1")) != asked {
		t.Error("a job that had ended was asked to stop all the same")
	}

	if msg := suite.CallErr(t, "job_cancel", map[string]any{"job": ""}); !strings.Contains(msg, "job is required") {
		t.Errorf("no job = %q", msg)
	}
	dock.Fail("GET /api/jobs/gone", http.StatusNotFound, "Job not found")
	if msg := suite.CallErr(t, "job_cancel", map[string]any{"job": "gone"}); !strings.Contains(msg, "no job gone is known to Dockhand") {
		t.Errorf("a job that is not there = %q", msg)
	}
	dock.Answer("GET /api/jobs/j1", pullRunning)
	dock.Fail("DELETE /api/jobs/j1", http.StatusForbidden, "Permission denied")
	if msg := suite.CallErr(t, "job_cancel", map[string]any{"job": "j1"}); !strings.Contains(msg, "job j1 could not be asked to stop") || !strings.Contains(msg, "Permission denied") {
		t.Errorf("a job that may not be stopped = %q", msg)
	}

	// stopped, and gone before it could be read back
	dock.Answer("DELETE /api/jobs/j1", `{"cancelled":true}`)
	dock.Then("DELETE /api/jobs/j1", func(dockhandtest.Request) {
		dock.Fail("GET /api/jobs/j1", http.StatusNotFound, "Job not found")
	})
	if msg := suite.CallErr(t, "job_cancel", map[string]any{"job": "j1"}); !strings.Contains(msg, "no job j1 is known to Dockhand") {
		t.Errorf("a job gone before it was read back = %q", msg)
	}
}

// job_cancel changes something, so a session that only reads is not given it; job_get is, since it only asks.
func TestJobToolsByKind(t *testing.T) {
	t.Parallel()

	reads := register(t, Options{})
	if !slices.Contains(reads, "job_get") || slices.Contains(reads, "job_cancel") {
		t.Errorf("a session that only reads has %v", reads)
	}
	if writes := register(t, Options{EnableWrite: true, Toolsets: []string{"manage"}}); !slices.Contains(writes, "job_cancel") {
		t.Errorf("a session that may change things has %v", writes)
	}
}

func TestProgressOf(t *testing.T) {
	t.Parallel()

	line := func(event string, data map[string]any) dockhand.GetJobsByIdAnswerLines {
		return dockhand.GetJobsByIdAnswerLines{Event: event, Data: data}
	}

	// only the last few, the result left out, a repeat said once, and a line with nothing to say left out
	lines := []dockhand.GetJobsByIdAnswerLines{
		line("progress", map[string]any{"status": "one"}),
		line("progress", map[string]any{"status": "two"}),
		line("progress", map[string]any{"status": "two"}),
		line("progress", map[string]any{"progressDetail": map[string]any{"current": 1}}),
		line("progress", map[string]any{"message": "three"}),
		line("progress", map[string]any{"output": " four\n"}),
		line("progress", map[string]any{"status": "error", "error": "five"}),
		line("progress", map[string]any{"id": "abc", "status": "six"}),
		line("progress", map[string]any{"status": 7}),
		line("result", map[string]any{"status": "complete"}),
	}
	if got := strings.Join(progressOf(lines), "|"); got != "two|three|four|error: five|abc: six" {
		t.Errorf("progressOf = %q", got)
	}
	if got := progressOf(nil); len(got) != 0 {
		t.Errorf("progressOf(nothing) = %q", got)
	}
}

func TestPlainly(t *testing.T) {
	t.Parallel()

	for in, want := range map[string]string{
		`Failed to pull image: {"message":"failed to resolve reference \"a/b:1\": not found"}` + "\n": `Failed to pull image: failed to resolve reference "a/b:1": not found`,
		`Error: Failed to pull image: {"message":"pull access denied"}`:                               "Failed to pull image: pull access denied",
		`{"message":"no such image"}`:      "no such image",
		"Error: it broke":                  "it broke",
		"  it broke  ":                     "it broke",
		`Failed: {"message":"unterminated`: `Failed: {"message":"unterminated`,
		"":                                 "",
	} {
		if got := plainly(in); got != want {
			t.Errorf("plainly(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestSeconds(t *testing.T) {
	t.Parallel()

	for wait, want := range map[time.Duration]string{0: "0 seconds", time.Second: "1 second", 50 * time.Second: "50 seconds"} {
		if got := seconds(wait); got != want {
			t.Errorf("seconds(%s) = %q, want %q", wait, got, want)
		}
	}
}
