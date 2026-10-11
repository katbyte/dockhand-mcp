package tools

import (
	"net/http"
	"strings"
	"testing"

	"github.com/katbyte/go-kt/mcp/acctest"
)

// What a 1.0.51 server lists for alpha's stacks, with invented names: one wholly running with an unhealthy service, one with a service stopped, and
// one stopped.
const alphaStacks = `[
	{"name":"web","status":"running","containers":["aaaa","bbbb"],"labelTags":[],"updatesAvailable":false,"updateCount":0,"newerVersionCount":0,"containerDetails":[
		{"id":"aaaa","name":"web-front-1","service":"front","image":"registry.example/front:1.2","state":"running","status":"Up 2 hours (healthy)","health":"healthy","restartCount":0,"created":1791615491,"volumeCount":1,"updateAvailable":false,"newerVersion":null,"labels":{},"networks":[{"name":"web_default","ipAddress":"172.18.0.2"}],"ports":[{"publicPort":8082,"privatePort":81,"type":"udp","display":"8082:81/udp"},{"publicPort":8081,"privatePort":80,"type":"tcp","display":"8081:80/tcp"},{"publicPort":8081,"privatePort":80,"type":"tcp","display":"8081:80/tcp"}]},
		{"id":"bbbb","name":"web-back-1","service":"back","image":"registry.example/back:1.2","state":"running","status":"Up 2 hours (unhealthy)","health":"unhealthy","restartCount":0,"created":1791615491,"volumeCount":0,"updateAvailable":false,"newerVersion":null,"labels":{},"networks":[{"name":"web_default","ipAddress":"172.18.0.3"}],"ports":[]}
	]},
	{"name":"mail","status":"partial","containers":["cccc","dddd"],"labelTags":[],"updatesAvailable":false,"updateCount":0,"newerVersionCount":0,"containerDetails":[
		{"id":"cccc","name":"mail-smtp-1","service":"smtp","image":"registry.example/smtp:3","state":"running","status":"Up 2 hours","health":null,"restartCount":0,"created":1791615491,"volumeCount":0,"updateAvailable":false,"newerVersion":null,"labels":{},"networks":[],"ports":[]},
		{"id":"dddd","name":"mail-spool-1","service":"spool","image":"registry.example/spool:3","state":"exited","status":"Exited (137) 10 seconds ago","health":null,"restartCount":2,"created":1791615491,"volumeCount":0,"updateAvailable":false,"newerVersion":null,"labels":{},"networks":[],"ports":[]}
	]},
	{"name":"archive","status":"stopped","containers":["eeee"],"labelTags":[],"updatesAvailable":false,"updateCount":0,"newerVersionCount":0,"containerDetails":[
		{"id":"eeee","name":"archive-job-1","service":"job","image":"registry.example/job:1","state":"exited","status":"Exited (0) 3 hours ago","health":null,"restartCount":0,"created":1791615491,"volumeCount":0,"updateAvailable":false,"newerVersion":null,"labels":{},"networks":[],"ports":[]}
	]}
]`

func TestStackList(t *testing.T) {
	t.Parallel()

	dock := twoHosts(t)
	out := session(t, dock, Options{}).Call(t, "stack_list", nil)

	rows := acctest.Rows(t, out["stacks"], "stacks")
	if len(rows) != 3 || acctest.Num(t, out["total"], "total") != 3 || out["failed"] != nil || out["note"] != nil {
		t.Fatalf("every stack of both hosts = %d rows, %v", len(rows), out)
	}
	// by name, on the one host that has any
	if rows[0]["stack"] != "archive" || rows[1]["stack"] != "mail" || rows[2]["stack"] != "web" || rows[2]["env"] != "alpha" {
		t.Errorf("the order = %v, %v, %v", rows[0]["stack"], rows[1]["stack"], rows[2]["stack"])
	}

	web := rows[2]
	services := acctest.Rows(t, web["services"], "services")
	if web["status"] != "running" || acctest.Num(t, web["problems"], "problems") != 1 || len(services) != 2 || services[0]["service"] != "back" || services[1]["service"] != "front" {
		t.Fatalf("web = %v", web)
	}
	back, front := services[0], services[1]
	if back["container"] != "web-back-1" || back["health"] != "unhealthy" || !strings.Contains(acctest.Str(back["problem"]), "unhealthy") || back["ports"] != nil {
		t.Errorf("web's back = %v", back)
	}
	if front["container"] != "web-front-1" || front["image"] != "registry.example/front:1.2" || front["state"] != "running" || front["health"] != "healthy" || front["problem"] != nil {
		t.Errorf("web's front = %v", front)
	}
	// a port published on every address is listed once
	if got := strings.Join(acctest.Strs(t, front["ports"], "ports"), " "); got != "8081:80/tcp 8082:81/udp" {
		t.Errorf("front's ports = %s", got)
	}

	// a service that was stopped is said to have been, and one that ran and finished has nothing wrong with it
	spool := acctest.Rows(t, rows[1]["services"], "services")[1]
	if rows[1]["status"] != "partial" || !strings.Contains(acctest.Str(spool["problem"]), "exited with code 137") || acctest.Num(t, spool["restarts"], "restarts") != 2 {
		t.Errorf("mail = %v", rows[1])
	}
	if rows[0]["status"] != "stopped" || acctest.Num(t, rows[0]["problems"], "problems") != 0 {
		t.Errorf("archive = %v", rows[0])
	}

	statuses := acctest.Object(t, out["by_status"], "by_status")
	if acctest.Num(t, statuses["running"], "running") != 1 || acctest.Num(t, statuses["partial"], "partial") != 1 || acctest.Num(t, statuses["stopped"], "stopped") != 1 {
		t.Errorf("by status = %v", statuses)
	}
}

func TestStackListNarrowed(t *testing.T) {
	t.Parallel()

	suite := session(t, twoHosts(t), Options{})
	names := func(args map[string]any) string {
		rows := acctest.Rows(t, suite.Call(t, "stack_list", args)["stacks"], "stacks")
		got := make([]string, 0, len(rows))
		for _, row := range rows {
			got = append(got, acctest.Str(row["stack"]))
		}

		return strings.Join(got, " ")
	}

	for what, tc := range map[string]struct {
		args map[string]any
		want string
	}{
		"one environment":             {map[string]any{"env": "alpha"}, "archive mail web"},
		"an environment with none":    {map[string]any{"env": "beta"}, ""},
		"a name whatever its case":    {map[string]any{"name": "MAI"}, "mail"},
		"problems alone":              {map[string]any{"problems": true}, "archive mail web"},
		"problems by a name":          {map[string]any{"problems": true, "name": "web"}, "web"},
		"nothing that matches a name": {map[string]any{"name": "nothing-by-this-name"}, ""},
	} {
		if got := names(tc.args); got != tc.want {
			t.Errorf("%s = %q, want %q", what, got, tc.want)
		}
	}

	if msg := suite.CallErr(t, "stack_list", map[string]any{"env": "gamma"}); !strings.Contains(msg, `no environment is named or numbered "gamma"`) {
		t.Errorf("an environment that is none = %q", msg)
	}
}

func TestStackListWithAHostDown(t *testing.T) {
	t.Parallel()

	dock := twoHosts(t)
	dock.FailWhen("GET /api/stacks", "env", "2", http.StatusInternalServerError, "Failed to connect to the agent")

	out := session(t, dock, Options{}).Call(t, "stack_list", nil)
	failed := acctest.Rows(t, out["failed"], "failed")
	if len(failed) != 1 || failed[0]["env"] != "beta" || acctest.Num(t, out["total"], "total") != 3 || !strings.Contains(acctest.Str(out["note"]), "1 of the 2 environments could not be asked") {
		t.Errorf("with beta down = %v", out)
	}

	dock.FailWhen("GET /api/stacks", "env", "1", http.StatusForbidden, "Permission denied")
	if msg := session(t, dock, Options{}).CallErr(t, "stack_list", nil); !strings.Contains(msg, "no environment could be asked for its stacks") || !strings.Contains(msg, "Permission denied") {
		t.Errorf("with both down = %q", msg)
	}
}
