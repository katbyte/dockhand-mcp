package tools

import (
	"net/http"
	"strings"
	"testing"

	"github.com/katbyte/dockhand-mcp/sdk/dockhandtest"
	"github.com/katbyte/go-kt/mcp/acctest"
)

// Docker's inspect documents as a 1.0.51 server hands them on, with invented names, cut to what a container is, how it is doing and what it was
// given.
const (
	frontID      = "aaaaaaaaaaaa1111111111111111111111111111111111111111111111111111"
	backID       = "bbbbbbbbbbbb2222222222222222222222222222222222222222222222222222"
	crashedID    = "dddddddddddd4444444444444444444444444444444444444444444444444444"
	frontInspect = `{"Id":"` + frontID + `","Created":"2026-10-10T07:39:18.673021268Z","Path":"serve","Args":["--listen",":80","--db-password=hunter2"],"Name":"/web-front-1","RestartCount":2,
		"State":{"Status":"running","Running":true,"Paused":false,"Restarting":false,"OOMKilled":false,"Dead":false,"ExitCode":0,"Error":"","StartedAt":"2026-10-10T07:39:18.727783836Z","FinishedAt":"0001-01-01T00:00:00Z","Health":{"Status":"healthy","FailingStreak":0,"Log":[{"Start":"2026-10-10T07:43:12Z","End":"2026-10-10T07:43:12.5Z","ExitCode":0,"Output":""},{"Start":"2026-10-10T07:43:14Z","End":"2026-10-10T07:43:14.02Z","ExitCode":0,"Output":"ok\n"}]}},
		"Config":{"Image":"registry.example/front:1.2","User":"1000","Env":["PATH=/usr/bin","MODE=demo","API_KEY=hunter2-not-real","DATABASE_URL=postgres://app:hunter2@db:5432/app","EMPTY="],"Labels":{"com.docker.compose.project":"web","com.docker.compose.service":"front","proxy.basic-auth":"admin:$apr1$abc","maintainer":"someone"}},
		"HostConfig":{"RestartPolicy":{"Name":"unless-stopped","MaximumRetryCount":0},"Memory":67108864,"NanoCpus":1500000000,"Privileged":false,"NetworkMode":"web_default"},
		"Mounts":[{"Type":"volume","Name":"web_data","Source":"/var/lib/docker/volumes/web_data/_data","Destination":"/data","Mode":"rw","RW":true},{"Type":"bind","Source":"/srv/web/config","Destination":"/config","Mode":"ro","RW":false}],
		"NetworkSettings":{"Ports":{"80/tcp":[{"HostIp":"0.0.0.0","HostPort":"8081"},{"HostIp":"::","HostPort":"8081"}],"9000/tcp":null},"Networks":{"web_default":{"IPAddress":"172.18.0.2","Gateway":"172.18.0.1"}}},
		"divergence":{"env":[],"labels":[]}}`
	backInspect = `{"Id":"` + backID + `","Created":"2026-10-10T07:39:18Z","Path":"work","Args":[],"Name":"/web-back-1","RestartCount":0,
		"State":{"Status":"running","Running":true,"OOMKilled":false,"ExitCode":0,"Error":"","StartedAt":"2026-10-10T07:39:18Z","FinishedAt":"0001-01-01T00:00:00Z","Health":{"Status":"unhealthy","FailingStreak":117,"Log":[{"Start":"2026-10-10T07:43:18Z","End":"2026-10-10T07:43:18.1Z","ExitCode":1,"Output":"not ready: token=abc123\n"}]}},
		"Config":{"Image":"registry.example/back:1.2","Env":[],"Labels":{"com.docker.compose.project":"web","com.docker.compose.service":"back"}},
		"HostConfig":{"RestartPolicy":{"Name":"no"},"Memory":0,"NanoCpus":0,"NetworkMode":"web_default"},"Mounts":[],"NetworkSettings":{"Ports":{},"Networks":{}}}`
	crashedInspect = `{"Id":"` + crashedID + `","Created":"2026-10-10T07:39:19Z","Path":"sh","Args":["-c","exit 3"],"Name":"/crashed","RestartCount":0,
		"State":{"Status":"exited","Running":false,"OOMKilled":true,"ExitCode":3,"Error":"","StartedAt":"2026-10-10T07:39:19Z","FinishedAt":"2026-10-10T07:39:20.5Z"},
		"Config":{"Image":"registry.example/tool:latest","Env":null,"Labels":null},
		"HostConfig":{"RestartPolicy":{"Name":"no"},"NetworkMode":"bridge"},"Mounts":null,"NetworkSettings":{"Ports":null,"Networks":{"bridge":{"IPAddress":""}}}}`
)

// inspectable is the two hosts with the inspect documents of three of alpha's containers.
func inspectable(t *testing.T) *dockhandtest.Server {
	t.Helper()

	dock := twoHosts(t)
	dock.Answer("GET /api/containers/"+frontID+"/inspect", frontInspect)
	dock.Answer("GET /api/containers/"+backID+"/inspect", backInspect)
	dock.Answer("GET /api/containers/"+crashedID+"/inspect", crashedInspect)

	return dock
}

func TestContainerGet(t *testing.T) {
	t.Parallel()

	dock := inspectable(t)
	out := session(t, dock, Options{}).Call(t, "container_get", map[string]any{"container": "web-front-1"})

	if made := dock.Last("GET /api/containers/" + frontID + "/inspect"); made.Query.Get("env") != "1" {
		t.Errorf("the container was asked for on environment %q, want the one it was found on", made.Query.Get("env"))
	}
	if out["env"] != "alpha" || out["name"] != "web-front-1" || out["id"] != frontID || out["image"] != "registry.example/front:1.2" || out["created"] != "2026-10-10T07:39:18Z" || out["user"] != "1000" || out["stack"] != "web" || out["service"] != "front" {
		t.Errorf("what it is = %v", out)
	}
	if out["state"] != "running" || out["started"] != "2026-10-10T07:39:18Z" || out["finished"] != nil || out["exit_code"] != nil || !acctest.IsBool(out["oom_killed"], false) || acctest.Num(t, out["restarts"], "restarts") != 2 || out["problem"] != nil {
		t.Errorf("how it is doing = %v: a running container has neither finished nor exited", out)
	}
	if out["restart_policy"] != "unless-stopped" || acctest.Num(t, out["memory_limit_bytes"], "memory_limit_bytes") != 67108864 || acctest.Decimal(t, out["cpu_limit"], "cpu_limit") != 1.5 || out["network_mode"] != "web_default" || out["privileged"] != nil {
		t.Errorf("what it may use = %v", out)
	}

	// the latest check is the one that is told, not the first
	check := acctest.Object(t, out["health"], "health")
	if check["status"] != "healthy" || acctest.Num(t, check["failing_streak"], "failing_streak") != 0 || check["last_checked"] != "2026-10-10T07:43:14Z" || check["last_output"] != "ok" {
		t.Errorf("its health = %v", check)
	}

	// what it was given is listed by name, and a value whose name says it is a secret is hidden, here and on the command line
	variables := rowsNamed(acctest.Rows(t, out["environment"], "environment"), "name")
	if len(variables) != 5 || variables["MODE"]["value"] != "demo" || variables["MODE"]["hidden"] != nil || variables["API_KEY"]["value"] != "***" || !acctest.IsBool(variables["API_KEY"]["hidden"], true) || variables["DATABASE_URL"]["value"] != "postgres://app:***@db:5432/app" || !acctest.IsBool(variables["DATABASE_URL"]["hidden"], true) || variables["EMPTY"]["hidden"] != nil { //nolint:gosec // G101: the address is the fixture's, with its password already hidden
		t.Errorf("its variables = %v", variables)
	}
	if out["command"] != "serve --listen :80 --db-password=***" {
		t.Errorf("its command = %q", out["command"])
	}
	labels := acctest.Object(t, out["labels"], "labels")
	if labels["maintainer"] != "someone" || labels["proxy.basic-auth"] != "***" {
		t.Errorf("its labels = %v", labels)
	}
	if acctest.Num(t, out["hidden"], "hidden") != 4 || !strings.Contains(acctest.Str(out["note"]), "4 secrets are hidden") {
		t.Errorf("hidden = %v, %q: the key, the address's password, the command line's and the label's", out["hidden"], out["note"])
	}
	for _, secret := range []string{"hunter2", "$apr1$"} {
		if strings.Contains(strings.Join([]string{acctest.Str(out["command"]), acctest.Str(variables["API_KEY"]["value"]), acctest.Str(variables["DATABASE_URL"]["value"]), acctest.Str(labels["proxy.basic-auth"])}, " "), secret) {
			t.Errorf("%q is shown", secret)
		}
	}

	// a volume is named, a bind is a path, and what cannot be written to says so
	mounts := rowsNamed(acctest.Rows(t, out["mounts"], "mounts"), "destination")
	if mounts["/data"]["type"] != "volume" || mounts["/data"]["source"] != "web_data" || mounts["/data"]["read_only"] != nil || mounts["/config"]["type"] != "bind" || mounts["/config"]["source"] != "/srv/web/config" || !acctest.IsBool(mounts["/config"]["read_only"], true) {
		t.Errorf("its mounts = %v", mounts)
	}
	// a port published on every address is listed once, and one only exposed without a host side
	if got := strings.Join(acctest.Strs(t, out["ports"], "ports"), " "); got != "8081->80/tcp 9000/tcp" {
		t.Errorf("its ports = %q", got)
	}
	if networks := acctest.Object(t, out["networks"], "networks"); networks["web_default"] != "172.18.0.2" {
		t.Errorf("its networks = %v", networks)
	}
}

func TestContainerGetRevealed(t *testing.T) {
	t.Parallel()

	out := session(t, inspectable(t), Options{}).Call(t, "container_get", map[string]any{"container": "web-front-1", "reveal_secrets": true})

	variables := rowsNamed(acctest.Rows(t, out["environment"], "environment"), "name")
	if variables["API_KEY"]["value"] != "hunter2-not-real" || variables["API_KEY"]["hidden"] != nil || variables["DATABASE_URL"]["value"] != "postgres://app:hunter2@db:5432/app" || out["command"] != "serve --listen :80 --db-password=hunter2" { //nolint:gosec // G101: the fixture's own invented password, which this test asks to be shown
		t.Errorf("asked to show its secrets = %v, %q", variables, out["command"])
	}
	if acctest.Num(t, out["hidden"], "hidden") != 0 || out["note"] != nil {
		t.Errorf("hidden = %v, note %v: nothing is hidden and nothing is said to be", out["hidden"], out["note"])
	}
}

// What is wrong with a container is said as the list says it, with what only an inspect knows beside it: why its health check fails, and that the
// host killed it.
func TestContainerGetOfOnesThatAreNotRight(t *testing.T) {
	t.Parallel()

	suite := session(t, inspectable(t), Options{})

	back := suite.Call(t, "container_get", map[string]any{"container": "web-back-1"})
	check := acctest.Object(t, back["health"], "health")
	if check["status"] != "unhealthy" || acctest.Num(t, check["failing_streak"], "failing_streak") != 117 || acctest.Num(t, check["last_exit_code"], "last_exit_code") != 1 || check["last_output"] != "not ready: token=***" || !strings.Contains(acctest.Str(back["problem"]), "unhealthy") {
		t.Errorf("an unhealthy container = %v, health %v", back["problem"], check)
	}
	if acctest.Num(t, back["hidden"], "hidden") != 1 || back["memory_limit_bytes"] != nil || back["cpu_limit"] != nil || back["restart_policy"] != "no" {
		t.Errorf("a secret in what its check printed is hidden, and no limit is no limit: %v", back)
	}

	crashed := suite.Call(t, "container_get", map[string]any{"container": "crashed"})
	if crashed["state"] != "exited" || acctest.Num(t, crashed["exit_code"], "exit_code") != 3 || crashed["finished"] != "2026-10-10T07:39:20Z" || !acctest.IsBool(crashed["oom_killed"], true) || crashed["health"] != nil {
		t.Errorf("one that exited = %v", crashed)
	}
	if said := acctest.Str(crashed["problem"]); !strings.Contains(said, "exited with code 3") || !strings.Contains(said, "killed it for using more memory") {
		t.Errorf("its problem = %q, want how it ended and that the host killed it", said)
	}
	// a container given nothing lists nothing, rather than nothing at all
	if len(acctest.RowsOf(crashed["environment"])) != 0 || len(acctest.RowsOf(crashed["mounts"])) != 0 || crashed["environment"] == nil || crashed["ports"] == nil {
		t.Errorf("one given nothing = %v", crashed)
	}
}

func TestContainerGetRefused(t *testing.T) {
	t.Parallel()

	dock := inspectable(t)
	suite := session(t, dock, Options{})

	if msg := suite.CallErr(t, "container_get", map[string]any{"container": "nothing-by-this-name"}); !strings.Contains(msg, `no container is named "nothing-by-this-name"`) {
		t.Errorf("a container that is none = %q", msg)
	}

	dock.Fail("GET /api/containers/"+frontID+"/inspect", http.StatusInternalServerError, "Failed to inspect container")
	if msg := suite.CallErr(t, "container_get", map[string]any{"container": "web-front-1"}); !strings.Contains(msg, "web-front-1 on alpha could not be inspected") || !strings.Contains(msg, "Failed to inspect container") {
		t.Errorf("an inspect that fails = %q", msg)
	}

	dock.Answer("GET /api/containers/"+frontID+"/inspect", `["not", "a", "container"]`)
	if msg := suite.CallErr(t, "container_get", map[string]any{"container": "web-front-1"}); !strings.Contains(msg, "not Docker's inspect document") {
		t.Errorf("an answer that is no inspect document = %q", msg)
	}
}
