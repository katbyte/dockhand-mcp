package tools

import (
	"net/http"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/katbyte/dockhand-mcp/sdk/dockhandtest"
	"github.com/katbyte/go-kt/mcp/acctest"
)

// What a 1.0.51 server answers for a container's log: one text, each line led by when Docker took it.
const (
	frontLog = `{"logs":"2026-10-10T07:39:18.727783836Z front up\n2026-10-10T07:39:18.800000000Z connecting with password=hunter2\n2026-10-10T07:39:20.882410768Z tick 1\n2026-10-10T07:39:24.884237039Z tick 3\n"}`
	backLog  = `{"logs":"2026-10-10T07:39:18.100000000Z back up\n2026-10-10T07:39:22.000000000Z waiting for front\nno time on this line\n"}`
)

// logged is the two hosts with the logs of the two containers of alpha's stack.
func logged(t *testing.T) *dockhandtest.Server {
	t.Helper()

	dock := twoHosts(t)
	dock.Answer("GET /api/containers/"+frontID+"/logs", frontLog)
	dock.Answer("GET /api/containers/"+backID+"/logs", backLog)

	return dock
}

func TestMoment(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 10, 10, 8, 0, 0, 0, time.UTC)
	for given, want := range map[string]string{
		"":                          "",
		"  ":                        "",
		"10m":                       strconv.FormatInt(now.Add(-10*time.Minute).Unix(), 10),
		"1h30m":                     strconv.FormatInt(now.Add(-90*time.Minute).Unix(), 10),
		"2026-10-10T07:00:00Z":      strconv.FormatInt(now.Add(-time.Hour).Unix(), 10),
		"2026-10-10T09:00:00+02:00": strconv.FormatInt(now.Add(-time.Hour).Unix(), 10),
	} {
		if got, err := moment(given, now); err != nil || got != want {
			t.Errorf("moment(%q) = %q, %v; want %q", given, got, err, want)
		}
	}
	for _, given := range []string{"yesterday", "-10m", "10", "2026-10-10"} {
		if got, err := moment(given, now); err == nil {
			t.Errorf("moment(%q) = %q, want it refused", given, got)
		}
	}
}

func TestContainerLogs(t *testing.T) {
	t.Parallel()

	dock := logged(t)
	out := session(t, dock, Options{}).Call(t, "container_logs", map[string]any{"container": "web-front-1"})

	// the last hundred lines are asked for, of the host the container is on, with no bound in time
	made := dock.Last("GET /api/containers/" + frontID + "/logs")
	if made.Query.Get("env") != "1" || made.Query.Get("tail") != "100" || made.Query.Has("since") || made.Query.Has("until") {
		t.Errorf("asked for %v", made.Query)
	}
	// each line is led by its time to the second, and a secret beside a name that says so is hidden
	want := "2026-10-10T07:39:18Z front up\n2026-10-10T07:39:18Z connecting with password=***\n2026-10-10T07:39:20Z tick 1\n2026-10-10T07:39:24Z tick 3"
	if out["log"] != want || acctest.Num(t, out["lines"], "lines") != 4 || acctest.Num(t, out["hidden"], "hidden") != 1 {
		t.Errorf("the log = %q, %v lines, %v hidden", out["log"], out["lines"], out["hidden"])
	}
	if out["env"] != "alpha" || out["container"] != "web-front-1" || out["state"] != "running" || !strings.Contains(acctest.Str(out["note"]), "1 secret is hidden") {
		t.Errorf("whose it is = %v", out)
	}
}

func TestContainerLogsNarrowed(t *testing.T) {
	t.Parallel()

	dock := logged(t)
	suite := session(t, dock, Options{})

	// a length of time and a time are both sent as seconds since 1970, which is all the API reads
	beforeSeconds := time.Now().Add(-10 * time.Minute).Unix()
	suite.Call(t, "container_logs", map[string]any{"container": "web-front-1", "tail": 4, "since": "10m", "until": "2026-10-10T07:00:00Z"})
	made := dock.Last("GET /api/containers/" + frontID + "/logs")
	since, err := strconv.ParseInt(made.Query.Get("since"), 10, 64)
	if err != nil || since < beforeSeconds || since > beforeSeconds+5 || made.Query.Get("until") != "1791615600" || made.Query.Get("tail") != "4" {
		t.Errorf("asked for %v, want since ten minutes ago in seconds and until as seconds", made.Query)
	}

	// as many lines came as were asked for, so there may be more before them
	out := suite.Call(t, "container_logs", map[string]any{"container": "web-front-1", "tail": 4})
	if !strings.Contains(acctest.Str(out["note"]), "these are the last 4 lines") {
		t.Errorf("the note with the tail filled = %q", out["note"])
	}

	out = suite.Call(t, "container_logs", map[string]any{"container": "web-front-1", "contains": "TICK"})
	if out["log"] != "2026-10-10T07:39:20Z tick 1\n2026-10-10T07:39:24Z tick 3" || acctest.Num(t, out["lines"], "lines") != 2 || acctest.Num(t, out["hidden"], "hidden") != 0 {
		t.Errorf("lines holding a word = %v", out)
	}
	out = suite.Call(t, "container_logs", map[string]any{"container": "web-front-1", "contains": "nothing like this"})
	if acctest.Num(t, out["lines"], "lines") != 0 || !strings.Contains(acctest.Str(out["note"]), `none of the 4 lines read holds "nothing like this"`) {
		t.Errorf("a word no line holds = %v", out)
	}

	out = suite.Call(t, "container_logs", map[string]any{"container": "web-front-1", "reveal_secrets": true})
	if !strings.Contains(acctest.Str(out["log"]), "password=hunter2") || acctest.Num(t, out["hidden"], "hidden") != 0 {
		t.Errorf("asked to show its secrets = %v", out)
	}

	// a line with no time is given as it came, and a log with nothing in it says so
	out = suite.Call(t, "container_logs", map[string]any{"container": "web-back-1"})
	if !strings.HasSuffix(acctest.Str(out["log"]), "\nno time on this line") {
		t.Errorf("a line with no time = %q", out["log"])
	}
	dock.Answer("GET /api/containers/"+frontID+"/logs", `{"logs":""}`)
	out = suite.Call(t, "container_logs", map[string]any{"container": "web-front-1"})
	if out["log"] != "" || !strings.Contains(acctest.Str(out["note"]), "has written nothing") {
		t.Errorf("an empty log = %v", out)
	}
}

func TestContainerLogsRefused(t *testing.T) {
	t.Parallel()

	dock := logged(t)
	suite := session(t, dock, Options{})

	for what, tc := range map[string]struct {
		args map[string]any
		want string
	}{
		"more lines than are given": {map[string]any{"container": "web-front-1", "tail": 2001}, "tail is 2001"},
		"fewer than none":           {map[string]any{"container": "web-front-1", "tail": -1}, "tail is -1"},
		"a since that is no time":   {map[string]any{"container": "web-front-1", "since": "yesterday"}, `since: "yesterday" is neither how long ago`},
		"an until that is no time":  {map[string]any{"container": "web-front-1", "until": "soon"}, `until: "soon"`},
		"a container that is none":  {map[string]any{"container": "nothing-by-this-name"}, `no container is named "nothing-by-this-name"`},
	} {
		if msg := suite.CallErr(t, "container_logs", tc.args); !strings.Contains(msg, tc.want) {
			t.Errorf("%s = %q, want %q", what, msg, tc.want)
		}
	}

	dock.Fail("GET /api/containers/"+frontID+"/logs", http.StatusInternalServerError, "Failed to get container logs")
	if msg := suite.CallErr(t, "container_logs", map[string]any{"container": "web-front-1"}); !strings.Contains(msg, "the log of web-front-1 on alpha could not be read") || !strings.Contains(msg, "Failed to get container logs") {
		t.Errorf("a log that cannot be read = %q", msg)
	}
}

// A stack's logs are one log, in the order the lines were written, each led by its time and the service that wrote it.
func TestStackLogs(t *testing.T) {
	t.Parallel()

	dock := logged(t)
	out := session(t, dock, Options{}).Call(t, "stack_logs", map[string]any{"stack": "web"})

	want := strings.Join([]string{
		"back | no time on this line",
		"2026-10-10T07:39:18Z back | back up",
		"2026-10-10T07:39:18Z front | front up",
		"2026-10-10T07:39:18Z front | connecting with password=***",
		"2026-10-10T07:39:20Z front | tick 1",
		"2026-10-10T07:39:22Z back | waiting for front",
		"2026-10-10T07:39:24Z front | tick 3",
	}, "\n")
	if out["log"] != want || acctest.Num(t, out["lines"], "lines") != 7 || acctest.Num(t, out["hidden"], "hidden") != 1 || out["env"] != "alpha" || out["stack"] != "web" {
		t.Errorf("the stack's log =\n%s\nwant\n%s\n(%v lines, %v hidden)", out["log"], want, out["lines"], out["hidden"])
	}
	// fifty lines of each container are asked for unless told otherwise
	if made := dock.Last("GET /api/containers/" + backID + "/logs"); made.Query.Get("tail") != "50" || made.Query.Get("env") != "1" {
		t.Errorf("asked each container for %v", made.Query)
	}

	sources := rowsNamed(acctest.Rows(t, out["containers"], "containers"), "service")
	if len(sources) != 2 || sources["front"]["container"] != "web-front-1" || acctest.Num(t, sources["front"]["lines"], "lines") != 4 || acctest.Num(t, sources["back"]["lines"], "lines") != 3 || sources["back"]["state"] != "running" {
		t.Errorf("whose lines they are = %v", sources)
	}
}

func TestStackLogsNarrowedAndRefused(t *testing.T) {
	t.Parallel()

	dock := logged(t)
	suite := session(t, dock, Options{})

	out := suite.Call(t, "stack_logs", map[string]any{"stack": "web", "contains": "up", "tail": 5, "env": "alpha"})
	if out["log"] != "2026-10-10T07:39:18Z back | back up\n2026-10-10T07:39:18Z front | front up" || acctest.Num(t, out["lines"], "lines") != 2 {
		t.Errorf("lines holding a word = %v", out)
	}
	if made := dock.Last("GET /api/containers/" + frontID + "/logs"); made.Query.Get("tail") != "5" {
		t.Errorf("asked each container for %v", made.Query)
	}

	// one container's log that cannot be read is said beside the other's, and when none can it is an error
	dock.Fail("GET /api/containers/"+backID+"/logs", http.StatusInternalServerError, "Failed to get container logs")
	out = suite.Call(t, "stack_logs", map[string]any{"stack": "web"})
	sources := rowsNamed(acctest.Rows(t, out["containers"], "containers"), "service")
	if !strings.Contains(acctest.Str(sources["back"]["error"]), "Failed to get container logs") || acctest.Num(t, out["lines"], "lines") != 4 || !strings.Contains(acctest.Str(out["note"]), "the log of 1 of the 2 containers could not be read") {
		t.Errorf("with one log unreadable = %v", out)
	}
	dock.Fail("GET /api/containers/"+frontID+"/logs", http.StatusInternalServerError, "Failed to get container logs")
	if msg := suite.CallErr(t, "stack_logs", map[string]any{"stack": "web"}); !strings.Contains(msg, "the log of no container of web could be read") {
		t.Errorf("with no log readable = %q", msg)
	}

	for what, tc := range map[string]struct {
		args map[string]any
		want string
	}{
		"no stack named":          {map[string]any{"stack": " "}, "stack is required"},
		"a stack that is none":    {map[string]any{"stack": "nothing-by-this-name"}, `no stack is named "nothing-by-this-name"`},
		"a stack on another host": {map[string]any{"stack": "web", "env": "beta"}, `no stack is named "web" on the environments asked`},
		"too many lines":          {map[string]any{"stack": "web", "tail": 5000}, "tail is 5000"},
	} {
		if msg := suite.CallErr(t, "stack_logs", tc.args); !strings.Contains(msg, tc.want) {
			t.Errorf("%s = %q, want %q", what, msg, tc.want)
		}
	}

	// a stack of one name on two hosts is not guessed at
	both := logged(t)
	both.AnswerWhen("GET /api/containers", "env", "2", strings.ReplaceAll(betaContainers, `"labels":{}`, `"labels":{"com.docker.compose.project":"web","com.docker.compose.service":"solo"}`))
	if msg := session(t, both, Options{}).CallErr(t, "stack_logs", map[string]any{"stack": "web"}); !strings.Contains(msg, `a stack named "web" is on alpha and beta: say which with env`) {
		t.Errorf("a stack on two hosts = %q", msg)
	}

	// a host that cannot be asked for its containers is not passed over: the stack may be there
	down := logged(t)
	down.FailWhen("GET /api/containers", "env", "2", http.StatusInternalServerError, "Failed to connect to the agent")
	if msg := session(t, down, Options{}).CallErr(t, "stack_logs", map[string]any{"stack": "web"}); !strings.Contains(msg, "the containers of beta could not be listed") {
		t.Errorf("with a host down = %q", msg)
	}
}
