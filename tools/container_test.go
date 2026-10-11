package tools

import (
	"net/http"
	"strings"
	"testing"

	"github.com/katbyte/dockhand-mcp/sdk/dockhandtest"
	"github.com/katbyte/go-kt/mcp/acctest"
)

// What a 1.0.51 server lists for two hosts, with invented names: on alpha a stack of two, one healthy and publishing ports and one unhealthy, a
// container on its own, one that exited with an error, one that was stopped, one that ran and finished, one Docker keeps starting again, caught
// between runs and caught during one, and one it started again days ago; on beta one container.
const (
	alphaContainers = `[
		{"id":"aaaaaaaaaaaa1111111111111111111111111111111111111111111111111111","name":"web-front-1","image":"registry.example/front:1.2","imageId":"sha256:f00d","command":"serve","created":1791615491,"state":"running","status":"Up 2 hours (healthy)","health":"healthy","restartCount":0,"labels":{"com.docker.compose.project":"web","com.docker.compose.service":"front"},"mounts":[{"type":"volume","name":"web_data","source":"/var/lib/docker/volumes/web_data/_data","destination":"/data","mode":"rw","rw":true}],"networks":{"web_default":{"ipAddress":"172.18.0.2"}},"ports":[{"IP":"0.0.0.0","PrivatePort":80,"PublicPort":8081,"Type":"tcp"},{"IP":"::","PrivatePort":80,"PublicPort":8081,"Type":"tcp"},{"IP":"127.0.0.1","PrivatePort":81,"PublicPort":8082,"Type":"udp"},{"PrivatePort":9000,"Type":"tcp"}],"systemContainer":null},
		{"id":"bbbbbbbbbbbb2222222222222222222222222222222222222222222222222222","name":"web-back-1","image":"registry.example/back:1.2","imageId":"sha256:beef","command":"work","created":1791615491,"state":"running","status":"Up 2 hours (unhealthy)","health":"unhealthy","restartCount":0,"labels":{"com.docker.compose.project":"web","com.docker.compose.service":"back"},"mounts":[],"networks":{"web_default":{"ipAddress":"172.18.0.3"}},"ports":[],"systemContainer":null},
		{"id":"cccccccccccc3333333333333333333333333333333333333333333333333333","name":"loose","image":"registry.example/tool:latest","imageId":"sha256:cafe","command":"sleep 100000","created":1791615492,"state":"running","status":"Up 12 seconds","health":null,"restartCount":0,"labels":{},"mounts":[],"networks":{"bridge":{"ipAddress":"172.17.0.2"}},"ports":[],"systemContainer":null},
		{"id":"dddddddddddd4444444444444444444444444444444444444444444444444444","name":"crashed","image":"registry.example/tool:latest","imageId":"sha256:cafe","command":"sh -c 'exit 3'","created":1791615492,"state":"exited","status":"Exited (3) 11 seconds ago","health":null,"restartCount":0,"labels":{},"mounts":[],"networks":{"bridge":{"ipAddress":""}},"ports":[],"systemContainer":null},
		{"id":"eeeeeeeeeeee5555555555555555555555555555555555555555555555555555","name":"stopped","image":"registry.example/tool:latest","imageId":"sha256:cafe","command":"sleep 100000","created":1791615492,"state":"exited","status":"Exited (137) 10 seconds ago","health":null,"restartCount":0,"labels":{},"mounts":[],"networks":{"bridge":{"ipAddress":""}},"ports":[],"systemContainer":null},
		{"id":"ffffffffffff6666666666666666666666666666666666666666666666666666","name":"done","image":"registry.example/job:1","imageId":"sha256:d00d","command":"run-once","created":1791615492,"state":"exited","status":"Exited (0) 3 hours ago","health":null,"restartCount":0,"labels":{},"mounts":[],"networks":{"bridge":{"ipAddress":""}},"ports":[],"systemContainer":null},
		{"id":"2222222222229999999999999999999999999999999999999999999999999999","name":"flapping","image":"registry.example/tool:latest","imageId":"sha256:cafe","command":"sh -c 'sleep 1; exit 1'","created":1791615492,"state":"running","status":"Up 2 seconds","health":null,"restartCount":4,"labels":{},"mounts":[],"networks":{"bridge":{"ipAddress":"172.17.0.5"}},"ports":[],"systemContainer":null},
		{"id":"333333333333aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa","name":"steady","image":"registry.example/tool:latest","imageId":"sha256:cafe","command":"serve","created":1791615492,"state":"running","status":"Up 3 days","health":null,"restartCount":2,"labels":{},"mounts":[],"networks":{"bridge":{"ipAddress":"172.17.0.6"}},"ports":[],"systemContainer":null},
		{"id":"0000000000007777777777777777777777777777777777777777777777777777","name":"looping","image":"registry.example/tool:latest","imageId":"sha256:cafe","command":"sh -c 'exit 1'","created":1791615492,"state":"restarting","status":"Restarting (1) 3 seconds ago","health":null,"restartCount":7,"labels":{},"mounts":[],"networks":{"bridge":{"ipAddress":""}},"ports":[],"systemContainer":null}
	]`
	betaContainers = `[{"id":"1111111111118888888888888888888888888888888888888888888888888888","name":"solo","image":"registry.example/tool:latest","imageId":"sha256:cafe","command":"sleep 100000","created":1791615500,"state":"running","status":"Up 5 minutes","health":null,"restartCount":0,"labels":{},"mounts":[],"networks":{"bridge":{"ipAddress":"172.17.0.2"}},"ports":[],"systemContainer":null}]`
)

// twoHosts is a canned Dockhand managing alpha and beta, with what runs on each.
func twoHosts(t *testing.T) *dockhandtest.Server {
	t.Helper()

	dock := dockhandtest.New(t)
	dock.Answer("GET /api/environments", twoEnvironments)
	dock.AnswerWhen("GET /api/containers", "env", "1", alphaContainers)
	dock.AnswerWhen("GET /api/containers", "env", "2", betaContainers)
	dock.AnswerWhen("GET /api/stacks", "env", "1", alphaStacks)
	dock.AnswerWhen("GET /api/stacks", "env", "2", `[]`)

	return dock
}

// rowsNamed is the rows of a list by the name each carries under a key.
func rowsNamed(rows []map[string]any, key string) map[string]map[string]any {
	out := map[string]map[string]any{}
	for _, row := range rows {
		out[acctest.Str(row[key])] = row
	}

	return out
}

func TestContainerList(t *testing.T) {
	t.Parallel()

	dock := twoHosts(t)
	out := session(t, dock, Options{}).Call(t, "container_list", nil)

	rows := acctest.Rows(t, out["containers"], "containers")
	if len(rows) != 10 || acctest.Num(t, out["total"], "total") != 10 || out["failed"] != nil || out["note"] != nil {
		t.Fatalf("every container of both hosts = %d rows, %v", len(rows), out)
	}
	// by environment as Dockhand lists them, then by name
	order := make([]string, 0, len(rows))
	for _, row := range rows {
		order = append(order, acctest.Str(row["env"])+"/"+acctest.Str(row["name"]))
	}
	if got := strings.Join(order, " "); got != "alpha/crashed alpha/done alpha/flapping alpha/looping alpha/loose alpha/steady alpha/stopped alpha/web-back-1 alpha/web-front-1 beta/solo" {
		t.Errorf("the order = %s", got)
	}
	// every container is asked for, running or not, of each host by its number
	for _, env := range []string{"1", "2"} {
		made := dock.Requests("GET /api/containers")
		asked := false
		for _, r := range made {
			asked = asked || (r.Query.Get("env") == env && r.Query.Get("all") == "true")
		}
		if !asked {
			t.Errorf("environment %s was not asked for all its containers: %v", env, made)
		}
	}

	byName := rowsNamed(rows, "name")
	front := byName["web-front-1"]
	if front["env"] != "alpha" || front["id"] != "aaaaaaaaaaaa" || front["image"] != "registry.example/front:1.2" || front["state"] != "running" || front["status"] != "Up 2 hours (healthy)" || front["health"] != "healthy" || front["stack"] != "web" || front["service"] != "front" || front["created"] != "2026-10-10T06:58:11Z" || front["problem"] != nil {
		t.Errorf("web-front-1 = %v", front)
	}
	// a port published on every address is listed once, and one that is only exposed without its host side
	if got := strings.Join(acctest.Strs(t, front["ports"], "ports"), " "); got != "8081->80/tcp 8082->81/udp 9000/tcp" {
		t.Errorf("its ports = %s", got)
	}

	for name, want := range map[string]string{
		"web-back-1": "unhealthy: its health check is failing",
		"crashed":    "exited with code 3",
		"stopped":    "exited with code 137: it was killed",
		"looping":    "restarting: Docker has started it again 7 times",
		"flapping":   "started again by Docker 4 times, the last only seconds ago",
	} {
		if got := acctest.Str(byName[name]["problem"]); !strings.Contains(got, want) {
			t.Errorf("%s: problem = %q, want %q", name, got, want)
		}
	}
	// one that ran and finished, one with no health check, and one Docker started again days ago have nothing wrong with them
	for _, name := range []string{"done", "loose", "solo", "steady"} {
		if byName[name]["problem"] != nil || byName[name]["health"] != nil || byName[name]["stack"] != nil {
			t.Errorf("%s = %v: no problem, no health and no stack", name, byName[name])
		}
	}
	if acctest.Num(t, byName["looping"]["restarts"], "restarts") != 7 {
		t.Errorf("looping = %v", byName["looping"])
	}

	states := acctest.Object(t, out["by_state"], "by_state")
	if acctest.Num(t, states["running"], "running") != 6 || acctest.Num(t, states["exited"], "exited") != 3 || acctest.Num(t, states["restarting"], "restarting") != 1 || acctest.Num(t, out["problems"], "problems") != 5 {
		t.Errorf("the counts = %v, problems %v", states, out["problems"])
	}
}

func TestContainerListNarrowed(t *testing.T) {
	t.Parallel()

	suite := session(t, twoHosts(t), Options{})
	names := func(args map[string]any) string {
		rows := acctest.Rows(t, suite.Call(t, "container_list", args)["containers"], "containers")
		got := make([]string, 0, len(rows))
		for _, row := range rows {
			got = append(got, acctest.Str(row["name"]))
		}

		return strings.Join(got, " ")
	}

	for what, tc := range map[string]struct {
		args map[string]any
		want string
	}{
		"one environment by name":       {map[string]any{"env": "beta"}, "solo"},
		"one environment by number":     {map[string]any{"env": 2}, "solo"},
		"its number in quotes":          {map[string]any{"env": " 2 "}, "solo"},
		"a name whatever its case":      {map[string]any{"env": "ALPHA", "name": "WEB"}, "web-back-1 web-front-1"},
		"a stack":                       {map[string]any{"stack": "web"}, "web-back-1 web-front-1"},
		"a state":                       {map[string]any{"state": "exited"}, "crashed done stopped"},
		"a state whatever its case":     {map[string]any{"state": "Restarting"}, "looping"},
		"problems alone":                {map[string]any{"problems": true}, "crashed flapping looping stopped web-back-1"},
		"problems of one stack":         {map[string]any{"problems": true, "stack": "web"}, "web-back-1"},
		"nothing that matches":          {map[string]any{"name": "nothing-by-this-name"}, ""},
		"a stack that is no stack here": {map[string]any{"stack": "Web"}, ""},
	} {
		if got := names(tc.args); got != tc.want {
			t.Errorf("%s = %q, want %q", what, got, tc.want)
		}
	}

	// cut at the limit, the counts are still of all that matched, and the cut is said
	out := suite.Call(t, "container_list", map[string]any{"limit": 3})
	if rows := acctest.Rows(t, out["containers"], "containers"); len(rows) != 3 || acctest.Num(t, out["total"], "total") != 10 || acctest.Num(t, out["problems"], "problems") != 5 || !strings.Contains(acctest.Str(out["note"]), "10 containers matched and the first 3 are listed") {
		t.Errorf("cut at 3 = %d rows, %v", len(rows), out)
	}
}

func TestContainerListRefusals(t *testing.T) {
	t.Parallel()

	suite := session(t, twoHosts(t), Options{})

	if msg := suite.CallErr(t, "container_list", map[string]any{"env": "gamma"}); !strings.Contains(msg, `no environment is named or numbered "gamma"`) || !strings.Contains(msg, "alpha (1), beta (2)") {
		t.Errorf("an environment that is none = %q", msg)
	}
	if msg := suite.CallErr(t, "container_list", map[string]any{"env": "7"}); !strings.Contains(msg, `"7"`) {
		t.Errorf("a number that is no environment's = %q", msg)
	}
	if msg := suite.CallErr(t, "container_list", map[string]any{"env": 1.5}); !strings.Contains(msg, "a whole one") {
		t.Errorf("an environment numbered in part = %q", msg)
	}
	if msg := suite.CallErr(t, "container_list", map[string]any{"env": []string{"alpha"}}); !strings.Contains(msg, "an environment's name or its number") {
		t.Errorf("an environment that is neither a name nor a number = %q", msg)
	}
	if msg := suite.CallErr(t, "container_list", map[string]any{"limit": -1}); !strings.Contains(msg, "limit is -1") {
		t.Errorf("a limit below nothing = %q", msg)
	}

	empty := dockhandtest.New(t)
	empty.Answer("GET /api/environments", `[]`)
	if msg := session(t, empty, Options{}).CallErr(t, "container_list", nil); !strings.Contains(msg, "manages no Docker host yet") {
		t.Errorf("a Dockhand with no hosts = %q", msg)
	}
}

// A host that cannot be asked is named with why, beside what the others answered; when none can, that is an error.
func TestContainerListWithAHostDown(t *testing.T) {
	t.Parallel()

	dock := twoHosts(t)
	dock.FailWhen("GET /api/containers", "env", "2", http.StatusInternalServerError, "Failed to connect to the agent")

	out := session(t, dock, Options{}).Call(t, "container_list", nil)
	failed := acctest.Rows(t, out["failed"], "failed")
	if len(failed) != 1 || failed[0]["env"] != "beta" || !strings.Contains(acctest.Str(failed[0]["error"]), "Failed to connect to the agent") {
		t.Errorf("failed = %v", failed)
	}
	if acctest.Num(t, out["total"], "total") != 9 || !strings.Contains(acctest.Str(out["note"]), "1 of the 2 environments could not be asked") {
		t.Errorf("alpha's containers are still given: %v", out)
	}

	dock.FailWhen("GET /api/containers", "env", "1", http.StatusInternalServerError, "Failed to list containers")
	if msg := session(t, dock, Options{}).CallErr(t, "container_list", nil); !strings.Contains(msg, "no environment could be asked for its containers") || !strings.Contains(msg, "Failed to list containers") {
		t.Errorf("with both down = %q", msg)
	}

	// asked for the one that is down, it is that host's failure that is said
	if msg := session(t, dock, Options{}).CallErr(t, "container_list", map[string]any{"env": "beta"}); !strings.Contains(msg, "beta") || !strings.Contains(msg, "Failed to connect to the agent") {
		t.Errorf("the one host asked for, down = %q", msg)
	}
}
