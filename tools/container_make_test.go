package tools

import (
	"maps"
	"net/http"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/katbyte/dockhand-mcp/sdk/dockhandtest"
	"github.com/katbyte/go-kt/mcp/acctest"
)

const (
	madeID = "9999999999991111111111111111111111111111111111111111111111111111"
	// what the canned alpha lists of a container made by a test, in the state given
	madeRow = `{"id":"` + madeID + `","name":"app","image":"registry.example/tool:latest","imageId":"sha256:cafe","command":"sleep 5","created":1791615600,"state":"%s","status":"","health":null,"restartCount":0,"labels":{},"mounts":[],"networks":{},"ports":[],"systemContainer":null}`

	// loose as Docker's inspect has it: what it was made with, which an edit has to read to keep
	looseInspect = `{"Id":"` + looseID + `","Name":"/loose","State":{"Status":"running","Running":true},"Config":{"Image":"registry.example/tool:latest","Env":["PATH=/usr/bin","MODE=demo","API_KEY=abc"],"Labels":{"team":"ops"}},"HostConfig":{"RestartPolicy":{"Name":"no"},"Memory":0,"NanoCpus":0,"NetworkMode":"bridge","Binds":["data:/data"],"PortBindings":{"80/tcp":[{"HostIp":"","HostPort":"8080"}],"53/udp":[{"HostIp":"127.0.0.1","HostPort":"5353"}]}},"Mounts":[],"NetworkSettings":{"Ports":{},"Networks":{}}}`
)

// makeable is the two canned hosts with their images and networks, which making a container looks at first.
func makeable(t *testing.T) *dockhandtest.Server {
	t.Helper()

	dock := withImages(t)
	dock.AnswerWhen("GET /api/networks", "env", "1", alphaNetworks)
	dock.AnswerWhen("GET /api/networks", "env", "2", betaNetworks)

	return dock
}

// lists has the canned alpha list its containers changed from when a call is made: how a container made, or made again, comes to be read back.
func lists(dock *dockhandtest.Server, call, containers string) {
	dock.Then(call, func(dockhandtest.Request) {
		dock.AnswerWhen("GET /api/containers", "env", "1", containers)
	})
}

// with is alpha's containers and one more.
func with(row string) string {
	return strings.TrimSuffix(strings.TrimSpace(alphaContainers), "]") + "," + row + "]"
}

// dockhands is alpha's containers with the one named steady being Dockhand itself.
func dockhands() string {
	return strings.Replace(alphaContainers, `"ipAddress":"172.17.0.6"}},"ports":[],"systemContainer":null`, `"ipAddress":"172.17.0.6"}},"ports":[],"systemContainer":"dockhand"`, 1)
}

func TestContainerCreate(t *testing.T) {
	t.Parallel()

	const call = "POST /api/containers"
	args := func(more map[string]any) map[string]any {
		given := map[string]any{"env": "alpha", "name": "app", "image": "registry.example/tool:latest"}
		maps.Copy(given, more)

		return given
	}

	if shortened("abc") != "abc" || shortened(madeID) != madeID[:12] {
		t.Errorf("an id shortened = %q and %q", shortened("abc"), shortened(madeID))
	}

	// said first, and nothing made
	dock := makeable(t)
	suite := session(t, dock, quick)
	dry := suite.Call(t, "container_create", args(map[string]any{"dry_run": true}))
	if got := acctest.Strs(t, dry["changes"], "changes"); len(got) != 1 || got[0] != "would make app on alpha from registry.example/tool:latest and start it" || dry["state"] != "" || len(dock.Requests(call)) != 0 {
		t.Errorf("a dry run = %v", dry)
	}
	dry = suite.Call(t, "container_create", args(map[string]any{"image": "registry.example/new:1", "start": false, "dry_run": true}))
	if got := acctest.Strs(t, dry["changes"], "changes"); len(got) != 2 || got[0] != "would pull registry.example/new:1 to alpha, which has not got it" || got[1] != "would make app on alpha from registry.example/new:1" {
		t.Errorf("a dry run with an image the host has not = %v", dry)
	}

	// made with everything it can be given, started, and read back
	dock.Answer(call, `{"success":true,"id":"`+madeID+`"}`)
	lists(dock, call, with(strings.Replace(madeRow, "%s", "running", 1)))
	out := suite.Call(t, "container_create", args(map[string]any{"name": " /app ", "command": []any{"sleep", "5"}, "entrypoint": []any{"/bin/sh", "-c"}, "env_vars": map[string]any{"MODE": "demo", "A": "1"}, "ports": []any{"8080:80", "127.0.0.1:8082:81/UDP", "9000"}, "volumes": []any{"data:/var/lib/app", " /srv/conf:/etc/app:ro "}, "networks": []any{"spare", "web_default", "spare", " "}, "restart": "On-Failure:3", "labels": map[string]any{"team": "ops"}, "workdir": " /srv "}))
	if got := acctest.Strs(t, out["changes"], "changes"); out["env"] != "alpha" || out["name"] != "app" || out["id"] != madeID[:12] || out["state"] != "running" || out["note"] != nil || len(got) != 1 || got[0] != "made app on alpha from registry.example/tool:latest and started it" {
		t.Errorf("making a container = %v", out)
	}
	want := map[string]any{
		"name": "app", "image": "registry.example/tool:latest", "cmd": []any{"sleep", "5"}, "entrypoint": []any{"/bin/sh", "-c"}, "env": []any{"A=1", "MODE=demo"},
		"ports":       map[string]any{"80/tcp": map[string]any{"HostPort": "8080"}, "81/udp": map[string]any{"HostPort": "8082", "HostIp": "127.0.0.1"}, "9000/tcp": map[string]any{"HostPort": ""}},
		"volumeBinds": []any{"data:/var/lib/app", "/srv/conf:/etc/app:ro"}, "networkMode": "spare", "additionalNetworks": []any{"web_default"}, "restartPolicy": "on-failure", "restartMaxRetries": float64(3),
		"labels": map[string]any{"team": "ops"}, "workingDir": "/srv", "startAfterCreate": true,
	}
	if got := sent(t, dock.Last(call)); !reflect.DeepEqual(got, want) || dock.Last(call).Query.Get("env") != "1" {
		t.Errorf("making a container sent %v to environment %q, want %v", got, dock.Last(call).Query.Get("env"), want)
	}

	// made and not started; and started, and ended at once
	dock = makeable(t)
	dock.Answer(call, `{"success":true,"id":"`+madeID+`"}`)
	lists(dock, call, with(strings.Replace(madeRow, "%s", "created", 1)))
	out = session(t, dock, quick).Call(t, "container_create", args(map[string]any{"start": false}))
	if got := acctest.Strs(t, out["changes"], "changes"); out["state"] != "created" || out["note"] != nil || got[0] != "made app on alpha from registry.example/tool:latest" || !acctest.IsBool(sent(t, dock.Last(call))["startAfterCreate"], false) {
		t.Errorf("making a container and not starting it = %v", out)
	}
	dock = makeable(t)
	dock.Answer(call, `{"success":true,"id":"`+madeID+`"}`)
	lists(dock, call, with(strings.Replace(madeRow, "%s", "exited", 1)))
	if out = session(t, dock, quick).Call(t, "container_create", args(nil)); out["state"] != "exited" || !strings.Contains(acctest.Str(out["note"]), "it was started and is exited") {
		t.Errorf("a container that ended at once = %v", out)
	}

	// what the host says afterwards cannot be read, or does not have it
	dock = makeable(t)
	dock.Answer(call, `{"success":true,"id":"`+madeID+`"}`)
	if out = session(t, dock, quick).Call(t, "container_create", args(nil)); out["state"] != "" || !strings.Contains(acctest.Str(out["note"]), "no container of that name is listed on alpha now") {
		t.Errorf("a container not listed once made = %v", out)
	}
	dock.Then(call, func(dockhandtest.Request) {
		dock.FailWhen("GET /api/containers", "env", "1", http.StatusInternalServerError, "Failed to list containers")
	})
	if out = session(t, dock, quick).Call(t, "container_create", args(nil)); !strings.Contains(acctest.Str(out["note"]), "how it stands could not be read back") {
		t.Errorf("a container that could not be read back = %v", out)
	}
}

// An image the host has not got is pulled first, as a job: waited for, left running with nothing made, or failed with nothing made.
func TestContainerCreatePullsFirst(t *testing.T) {
	t.Parallel()

	const (
		call = "POST /api/containers"
		pull = "POST /api/images/pull"
	)
	args := map[string]any{"env": "alpha", "name": "app", "image": "registry.example/new:1"}

	dock := makeable(t)
	dock.Answer(pull, `{"jobId":"j1"}`)
	dock.Answer("GET /api/jobs/j1", pullDone)
	dock.Answer(call, `{"success":true,"id":"`+madeID+`","imagePulled":false}`)
	lists(dock, call, with(strings.Replace(madeRow, "%s", "running", 1)))
	out := session(t, dock, quick).Call(t, "container_create", args)
	if got := acctest.Strs(t, out["changes"], "changes"); len(got) != 2 || got[0] != "pulled registry.example/new:1 to alpha" || out["state"] != "running" || !acctest.IsBool(out["running"], false) || dock.Last(pull).Body != `{"image":"registry.example/new:1"}` {
		t.Errorf("making a container from an image that was pulled first = %v", out)
	}

	dock = makeable(t)
	dock.Answer(pull, `{"jobId":"j1"}`)
	dock.Answer("GET /api/jobs/j1", pullRunning)
	out = session(t, dock, quick).Call(t, "container_create", map[string]any{"env": "alpha", "name": "app", "image": "registry.example/new:1", "wait": 0})
	if !acctest.IsBool(out["running"], true) || out["job"] != "j1" || out["state"] != "" || !strings.Contains(acctest.Str(out["note"]), "nothing is made yet") || len(dock.Requests(call)) != 0 {
		t.Errorf("a pull left running = %v", out)
	}

	dock = makeable(t)
	dock.Answer(pull, `{"jobId":"j1"}`)
	dock.Answer("GET /api/jobs/j1", pullFailed)
	if msg := session(t, dock, quick).CallErr(t, "container_create", args); !strings.Contains(msg, "alpha has not got registry.example/new:1 and it could not be pulled, so nothing was made") || len(dock.Requests(call)) != 0 {
		t.Errorf("a pull that failed = %q", msg)
	}
}

func TestContainerCreateRefusals(t *testing.T) {
	t.Parallel()

	const call = "POST /api/containers"
	dock := makeable(t)
	suite := session(t, dock, quick)

	for _, tc := range []struct {
		why  string
		args map[string]any
		want string
	}{
		{"a name Docker does not take", map[string]any{"name": "my app"}, `name is "my app"`},
		{"no image", map[string]any{"image": " "}, `image is " "`},
		{"an image with a space in it", map[string]any{"image": "a b"}, `image is "a b"`},
		{"a variable with no name", map[string]any{"env_vars": map[string]any{"1BAD": "x"}}, `env_vars names "1BAD"`},
		{"a protocol there is none of", map[string]any{"ports": []any{"80:80/icmp"}}, `whose protocol is "icmp"`},
		{"a port with too much to it", map[string]any{"ports": []any{"::1:80:80"}}, `ports has "::1:80:80": a port is written host:container`},
		{"a port on no address", map[string]any{"ports": []any{"localhost:80:80"}}, `whose address "localhost" is no IP address`},
		{"a port of the container that is none", map[string]any{"ports": []any{"80:http"}}, `"http" is no port of the container`},
		{"a port of the host that is none", map[string]any{"ports": []any{"70000:80"}}, `"70000" is no port of the host`},
		{"a port published twice", map[string]any{"ports": []any{"8080:80", "8081:80"}}, "ports publishes 80/tcp twice"},
		{"a mount with no target", map[string]any{"volumes": []any{"data"}}, `volumes has "data": a mount is written source:target`},
		{"a mount from nowhere", map[string]any{"volumes": []any{"../conf:/etc/app"}}, "is neither a volume's name nor a folder of the host"},
		{"a mount to nowhere", map[string]any{"volumes": []any{"data:etc/app"}}, "is not a path from the container's root"},
		{"a mount in a mode there is none of", map[string]any{"volumes": []any{"data:/data:rx"}}, `which ends in "rx"`},
		{"a restart policy there is none of", map[string]any{"restart": "sometimes"}, `restart is "sometimes"`},
		{"a count on a policy that takes none", map[string]any{"restart": "always:3"}, `restart is "always:3"`},
		{"a count that is none", map[string]any{"restart": "on-failure:often"}, "what follows on-failure is how many times to try"},
		{"a wait too long", map[string]any{"wait": 9000}, "wait is 9000"},
		{"a name that is taken", map[string]any{"name": "loose"}, "alpha has a container named loose already, which is running"},
		{"a network the host has not", map[string]any{"networks": []any{"nope"}}, "alpha has no network named nope"},
	} {
		given := map[string]any{"env": "alpha", "name": "app", "image": "registry.example/tool:latest"}
		maps.Copy(given, tc.args)
		if msg := suite.CallErr(t, "container_create", given); !strings.Contains(msg, tc.want) {
			t.Errorf("%s = %q, want it to say %q", tc.why, msg, tc.want)
		}
	}
	if msg := suite.CallErr(t, "container_create", map[string]any{"name": "app", "image": "registry.example/tool:latest"}); !strings.Contains(msg, "env is required") {
		t.Errorf("no environment named = %q", msg)
	}
	if len(dock.Requests(call)) != 0 {
		t.Errorf("a refused call made a container: %v", dock.Calls())
	}

	// what has to be read first cannot be
	args := map[string]any{"env": "alpha", "name": "app", "image": "registry.example/tool:latest", "networks": []any{"spare"}}
	dock.FailWhen("GET /api/images", "env", "1", http.StatusInternalServerError, "Failed to list images")
	if msg := suite.CallErr(t, "container_create", args); !strings.Contains(msg, "the images of alpha could not be listed, so nothing was made") {
		t.Errorf("images that could not be listed = %q", msg)
	}
	dock.FailWhen("GET /api/networks", "env", "1", http.StatusInternalServerError, "Failed to list networks")
	if msg := suite.CallErr(t, "container_create", args); !strings.Contains(msg, "the networks of alpha could not be listed, so nothing was made") {
		t.Errorf("networks that could not be listed = %q", msg)
	}
	dock.FailWhen("GET /api/containers", "env", "1", http.StatusInternalServerError, "Failed to list containers")
	if msg := suite.CallErr(t, "container_create", args); !strings.Contains(msg, "the containers of alpha could not be listed, so nothing was made") {
		t.Errorf("containers that could not be listed = %q", msg)
	}

	// refused by Docker with nothing made, and made and then not started
	dock = makeable(t)
	dock.Fail(call, http.StatusInternalServerError, "Failed to pull image")
	if msg := session(t, dock, quick).CallErr(t, "container_create", map[string]any{"env": "alpha", "name": "app", "image": "registry.example/tool:latest"}); !strings.Contains(msg, "app was not made on alpha") || !strings.Contains(msg, "Failed to pull image") {
		t.Errorf("a container Docker would not make = %q", msg)
	}
	lists(dock, call, with(strings.Replace(madeRow, "%s", "created", 1)))
	if msg := session(t, dock, quick).CallErr(t, "container_create", map[string]any{"env": "alpha", "name": "app", "image": "registry.example/tool:latest"}); !strings.Contains(msg, "app was made on alpha and could not be started") || !strings.Contains(msg, "It is left as it was made, created") {
		t.Errorf("a container made and not started = %q", msg)
	}
}

func TestContainerEdit(t *testing.T) {
	t.Parallel()

	const call = "POST /api/containers/" + looseID + "/update"
	change := map[string]any{"container": "loose", "env_vars": map[string]any{"MODE": "live", "NEW": "1", "PATH": "/usr/bin"}, "remove_env_vars": []any{"API_KEY", "ABSENT"}, "labels": map[string]any{"edited": "yes", "team": "ops"}, "remove_labels": []any{"absent"}, "ports": []any{"8081:80"}, "volumes": []any{}, "command": []any{"sleep", "5"}}
	said := []string{"the variable MODE is changed", "the variable API_KEY is taken away", "the variable NEW is set", "the label edited is set", "its published ports, which were [127.0.0.1:5353:53/udp 8080:80], are [8081:80]", "its mounts, which were [data:/data], are []", "its command is sleep 5"}

	dock := inspectable(t)
	dock.Answer("GET /api/containers/"+looseID+"/inspect", looseInspect)
	suite := session(t, dock, everything)

	// said first: what the container made again would have, and that it would be made again
	dry := suite.Call(t, "container_edit", map[string]any{"container": "loose", "env_vars": change["env_vars"], "remove_env_vars": change["remove_env_vars"], "labels": change["labels"], "remove_labels": change["remove_labels"], "ports": change["ports"], "volumes": change["volumes"], "command": change["command"], "dry_run": true})
	if got := acctest.Strs(t, dry["changes"], "changes"); !reflect.DeepEqual(got, append(slices.Clone(said), "would stop loose, remove it and make it again on alpha with these")) || dry["is"] != "running" || len(dock.Requests(call)) != 0 {
		t.Errorf("a dry run = %v", dry)
	}

	// made again with the change, and what was not named kept; read back under its new id
	dock.Answer(call, `{"success":true,"id":"`+madeID+`"}`)
	lists(dock, call, strings.Replace(alphaContainers, looseID, madeID, 1))
	out := suite.Call(t, "container_edit", change)
	if got := acctest.Strs(t, out["changes"], "changes"); !reflect.DeepEqual(got, append(slices.Clone(said), "stopped loose, removed it and made it again on alpha with these")) || out["id"] != madeID[:12] || out["was"] != "running" || out["is"] != "running" || out["note"] != nil {
		t.Errorf("an edit = %v", out)
	}
	want := map[string]any{"env": []any{"PATH=/usr/bin", "MODE=live", "NEW=1"}, "labels": map[string]any{"team": "ops", "edited": "yes"}, "ports": map[string]any{"80/tcp": map[string]any{"HostPort": "8081"}}, "volumeBinds": []any{}, "cmd": []any{"sleep", "5"}, "startAfterUpdate": true}
	if got := sent(t, dock.Last(call)); !reflect.DeepEqual(got, want) || dock.Last(call).Query.Get("env") != "1" {
		t.Errorf("an edit sent %v, want %v", got, want)
	}

	// what is so already is not a change, and nothing is made again for it
	dock = inspectable(t)
	dock.Answer("GET /api/containers/"+looseID+"/inspect", looseInspect)
	suite = session(t, dock, everything)
	if out = suite.Call(t, "container_edit", map[string]any{"container": "loose", "env_vars": map[string]any{"MODE": "demo"}, "remove_env_vars": []any{"ABSENT"}, "labels": map[string]any{"team": "ops"}, "remove_labels": []any{"absent"}}); len(acctest.Strs(t, out["changes"], "changes")) != 0 || out["note"] != "nothing was done: it is so already" || len(dock.Requests(call)) != 0 {
		t.Errorf("an edit to what it is already = %v", out)
	}
	// one thing alone: only that is sent, and a label taken away
	dock.Answer(call, `{"success":true,"id":"`+madeID+`"}`)
	if out = suite.Call(t, "container_edit", map[string]any{"container": "loose", "remove_labels": []any{"team"}}); !reflect.DeepEqual(sent(t, dock.Last(call)), map[string]any{"labels": map[string]any{}, "startAfterUpdate": true}) {
		t.Errorf("a label taken away sent %v: %v", sent(t, dock.Last(call)), out)
	}

	// one of a stack's, and one that is stopped and has nothing yet: said, and not started
	const stopped = "POST /api/containers/" + crashedID + "/update"
	dock.Answer(stopped, `{"success":true,"id":"`+madeID+`"}`)
	out = suite.Call(t, "container_edit", map[string]any{"container": "crashed", "labels": map[string]any{"team": "ops"}, "env_vars": map[string]any{"MODE": "demo"}})
	if got := sent(t, dock.Last(stopped)); !reflect.DeepEqual(got, map[string]any{"env": []any{"MODE=demo"}, "labels": map[string]any{"team": "ops"}, "startAfterUpdate": false}) || out["was"] != "exited" {
		t.Errorf("an edit of a stopped container sent %v: %v", got, out)
	}
	if out = suite.Call(t, "container_edit", map[string]any{"container": "web-front-1", "command": []any{"serve"}, "dry_run": true}); !strings.Contains(acctest.Str(out["note"]), "it belongs to the stack web") {
		t.Errorf("an edit of a stack's container = %v", out)
	}
}

func TestContainerEditThatGoesWrong(t *testing.T) {
	t.Parallel()

	const call = "POST /api/containers/" + looseID + "/update"
	dock := inspectable(t)
	dock.Answer("GET /api/containers/"+looseID+"/inspect", looseInspect)
	suite := session(t, dock, everything)

	for _, tc := range []struct {
		why  string
		args map[string]any
		want string
	}{
		{"nothing to change", map[string]any{}, "nothing to change was given"},
		{"a variable with no name", map[string]any{"env_vars": map[string]any{"a b": "x"}}, `env_vars names "a b"`},
		{"a label compose keeps", map[string]any{"labels": map[string]any{"com.docker.compose.project": "other"}}, "is one compose keeps to know its own containers by"},
		{"a label compose keeps, taken away", map[string]any{"remove_labels": []any{"com.docker.compose.service"}}, "is one compose keeps"},
		{"a port that is none", map[string]any{"ports": []any{"x"}}, `ports has "x"`},
		{"a mount that is none", map[string]any{"volumes": []any{"x"}}, `volumes has "x"`},
	} {
		given := map[string]any{"container": "loose"}
		maps.Copy(given, tc.args)
		if msg := suite.CallErr(t, "container_edit", given); !strings.Contains(msg, tc.want) {
			t.Errorf("%s = %q, want it to say %q", tc.why, msg, tc.want)
		}
	}
	if msg := suite.CallErr(t, "container_edit", map[string]any{"container": "nope", "command": []any{"x"}}); !strings.Contains(msg, "nope") {
		t.Errorf("a container there is none of = %q", msg)
	}

	// Dockhand itself is not made again through Dockhand
	dock.AnswerWhen("GET /api/containers", "env", "1", dockhands())
	if msg := suite.CallErr(t, "container_edit", map[string]any{"container": "steady", "command": []any{"x"}}); !strings.Contains(msg, "steady is Dockhand's own (dockhand)") {
		t.Errorf("an edit of Dockhand's own container = %q", msg)
	}
	dock.AnswerWhen("GET /api/containers", "env", "1", alphaContainers)

	// what it has cannot be read, so there is nothing to keep
	dock.Fail("GET /api/containers/"+looseID+"/inspect", http.StatusInternalServerError, "Failed to inspect container")
	if msg := suite.CallErr(t, "container_edit", map[string]any{"container": "loose", "command": []any{"x"}}); !strings.Contains(msg, "so nothing was changed: what it has now has to be read to be kept") {
		t.Errorf("an edit of what could not be inspected = %q", msg)
	}
	dock.Answer("GET /api/containers/"+looseID+"/inspect", looseInspect)

	// refused by Docker: how it stands now is said
	dock.Fail(call, http.StatusInternalServerError, "Failed to update container")
	if msg := suite.CallErr(t, "container_edit", map[string]any{"container": "loose", "command": []any{"x"}}); !strings.Contains(msg, "loose on alpha was not made again with the change") || !strings.Contains(msg, "It is running now") {
		t.Errorf("an edit Docker refused = %q", msg)
	}
	// made again, and not up afterwards
	dock.Answer(call, `{"success":true,"id":"`+madeID+`"}`)
	becomes(dock, call, "loose", "running", "exited")
	if out := suite.Call(t, "container_edit", map[string]any{"container": "loose", "command": []any{"x"}}); out["is"] != "exited" || !strings.Contains(acctest.Str(out["note"]), "it was running and is exited now") {
		t.Errorf("an edit it did not stay up with = %v", out)
	}
}

func TestContainerUpdate(t *testing.T) {
	t.Parallel()

	const (
		call    = "POST /api/containers/" + looseID + "/update"
		pull    = "POST /api/images/pull"
		pending = "DELETE /api/containers/pending-updates"
	)
	ready := func() *dockhandtest.Server {
		dock := withImages(t)
		dock.Answer(pull, `{"jobId":"j1"}`)
		dock.Answer("GET /api/jobs/j1", pullDone)
		dock.Answer(call, `{"success":true,"id":"`+madeID+`"}`)
		dock.Answer(pending, `{"success":true}`)

		return dock
	}

	// said first
	dock := ready()
	dry := session(t, dock, quick).Call(t, "container_update", map[string]any{"container": "web-front-1", "dry_run": true})
	if got := acctest.Strs(t, dry["changes"], "changes"); len(got) != 2 || got[0] != "would pull registry.example/front:1.2 to alpha" || !strings.Contains(acctest.Str(dry["note"]), "it belongs to the stack web") || len(dock.Requests(pull)) != 0 {
		t.Errorf("a dry run = %v", dry)
	}

	// the pull brought a newer image: made again from it, read back, and what Dockhand had noted for the old one cleared
	pulled(dock)
	lists(dock, call, strings.Replace(strings.Replace(alphaContainers, looseID, madeID, 1), `"name":"loose","image":"registry.example/tool:latest","imageId":"sha256:cafe"`, `"name":"loose","image":"registry.example/tool:latest","imageId":"sha256:fresh"`, 1))
	out := session(t, dock, quick).Call(t, "container_update", map[string]any{"container": "loose"})
	if got := acctest.Strs(t, out["changes"], "changes"); len(got) != 2 || got[0] != "pulled registry.example/tool:latest to alpha" || got[1] != "stopped loose, removed it and made it again from registry.example/tool:latest" || out["was"] != "cafe" || out["is"] != "fresh" || !acctest.IsBool(out["updated"], true) || out["state"] != "running" || out["note"] != nil {
		t.Errorf("an update = %v", out)
	}
	if got := sent(t, dock.Last(call)); !reflect.DeepEqual(got, map[string]any{"image": "registry.example/tool:latest", "startAfterUpdate": true}) || dock.Last(pending).Query.Get("containerId") != looseID || dock.Last(pending).Query.Get("env") != "1" {
		t.Errorf("an update sent %v and cleared %v", got, dock.Last(pending).Query)
	}

	// nothing newer: nothing touched, unless it is asked for
	dock = ready()
	suite := session(t, dock, quick)
	if out = suite.Call(t, "container_update", map[string]any{"container": "loose"}); !acctest.IsBool(out["updated"], false) || out["is"] != "cafe" || !strings.Contains(acctest.Str(out["note"]), "the registry has nothing newer under registry.example/tool:latest than the image loose runs") || len(dock.Requests(call)) != 0 {
		t.Errorf("an update with nothing newer = %v", out)
	}
	if out = suite.Call(t, "container_update", map[string]any{"container": "loose", "recreate": true}); !acctest.IsBool(out["updated"], false) || len(dock.Requests(call)) != 1 || len(acctest.Strs(t, out["changes"], "changes")) != 2 {
		t.Errorf("an update made again all the same = %v", out)
	}
	// moved to another image, which the host has under another id
	if out = suite.Call(t, "container_update", map[string]any{"container": "loose", "image": " registry.example/job:1 "}); out["image"] != "registry.example/job:1" || sent(t, dock.Last(call))["image"] != "registry.example/job:1" || dock.Last(pull).Body != `{"image":"registry.example/job:1"}` {
		t.Errorf("an update to another image = %v", out)
	}

	// still pulling when the wait is over: nothing changed yet
	dock = ready()
	dock.Answer("GET /api/jobs/j1", pullRunning)
	if out = session(t, dock, quick).Call(t, "container_update", map[string]any{"container": "loose", "wait": 0}); !acctest.IsBool(out["running"], true) || out["job"] != "j1" || !strings.Contains(acctest.Str(out["note"]), "loose is as it was") || len(dock.Requests(call)) != 0 {
		t.Errorf("an update whose pull was left running = %v", out)
	}
}

func TestContainerUpdateThatGoesWrong(t *testing.T) {
	t.Parallel()

	const (
		call    = "POST /api/containers/" + looseID + "/update"
		pull    = "POST /api/images/pull"
		pending = "DELETE /api/containers/pending-updates"
	)
	ready := func() *dockhandtest.Server {
		dock := withImages(t)
		dock.Answer(pull, `{"jobId":"j1"}`)
		dock.Answer("GET /api/jobs/j1", pullDone)
		dock.Answer(call, `{"success":true,"id":"`+madeID+`"}`)
		dock.Answer(pending, `{"success":true}`)
		pulled(dock)

		return dock
	}

	dock := ready()
	suite := session(t, dock, quick)
	if msg := suite.CallErr(t, "container_update", map[string]any{"container": "loose", "image": "a b"}); !strings.Contains(msg, `image is "a b"`) {
		t.Errorf("an image with a space in it = %q", msg)
	}
	if msg := suite.CallErr(t, "container_update", map[string]any{"container": "loose", "wait": -1}); !strings.Contains(msg, "wait is -1") {
		t.Errorf("a wait below nothing = %q", msg)
	}
	if msg := suite.CallErr(t, "container_update", map[string]any{"container": "nope"}); !strings.Contains(msg, "nope") {
		t.Errorf("a container there is none of = %q", msg)
	}
	// Dockhand's own, and one whose image has no name left to ask a registry for
	dock.AnswerWhen("GET /api/containers", "env", "1", dockhands())
	if msg := suite.CallErr(t, "container_update", map[string]any{"container": "steady"}); !strings.Contains(msg, "steady is Dockhand's own") {
		t.Errorf("an update of Dockhand's own container = %q", msg)
	}
	for _, id := range []string{"sha256:d00d", "0123456789abcdef"} {
		dock.AnswerWhen("GET /api/containers", "env", "1", strings.Replace(alphaContainers, `"name":"done","image":"registry.example/job:1"`, `"name":"done","image":"`+id+`"`, 1))
		if msg := suite.CallErr(t, "container_update", map[string]any{"container": "done"}); !strings.Contains(msg, "done was made from an image the host no longer has a name for (d00d)") {
			t.Errorf("an update of a container whose image is %s = %q", id, msg)
		}
	}
	if len(dock.Requests(pull)) != 0 {
		t.Errorf("a refused update pulled: %v", dock.Calls())
	}

	// the pull fails, and the images cannot be listed after it
	dock = ready()
	dock.Answer("GET /api/jobs/j1", pullFailed)
	if msg := session(t, dock, quick).CallErr(t, "container_update", map[string]any{"container": "loose"}); !strings.Contains(msg, "registry.example/tool:latest was not pulled to alpha, so loose is as it was") {
		t.Errorf("an update whose pull failed = %q", msg)
	}
	dock = ready()
	dock.Then(pull, func(dockhandtest.Request) {
		dock.FailWhen("GET /api/images", "env", "1", http.StatusInternalServerError, "Failed to list images")
	})
	if msg := session(t, dock, quick).CallErr(t, "container_update", map[string]any{"container": "loose"}); !strings.Contains(msg, "the images of alpha could not be listed to see whether it is newer, so loose is as it was") {
		t.Errorf("an update that could not see what was pulled = %q", msg)
	}

	// Docker refuses to make it again: how it stands is said, or that it cannot be
	dock = ready()
	dock.Fail(call, http.StatusNotFound, "No such image: registry.example/tool:latest")
	if msg := session(t, dock, quick).CallErr(t, "container_update", map[string]any{"container": "loose"}); !strings.Contains(msg, "loose on alpha was not made again from registry.example/tool:latest") || !strings.Contains(msg, "it is running now, on the image cafe") {
		t.Errorf("an update Docker refused = %q", msg)
	}
	dock.Then(call, func(dockhandtest.Request) {
		dock.FailWhen("GET /api/containers", "env", "1", http.StatusInternalServerError, "Failed to list containers")
	})
	if msg := session(t, dock, quick).CallErr(t, "container_update", map[string]any{"container": "loose", "env": "alpha"}); !strings.Contains(msg, "how it stands now could not be read back") {
		t.Errorf("an update Docker refused, unreadable after = %q", msg)
	}

	// made again, and then: not readable, not there, not up, and Dockhand's note not cleared
	dock = ready()
	dock.Then(call, func(dockhandtest.Request) {
		dock.FailWhen("GET /api/containers", "env", "1", http.StatusInternalServerError, "Failed to list containers")
	})
	if out := session(t, dock, quick).Call(t, "container_update", map[string]any{"container": "loose", "env": "alpha"}); out["state"] != "" || !strings.Contains(acctest.Str(out["note"]), "how it stands could not be read back") {
		t.Errorf("an update that could not be read back = %v", out)
	}
	dock = ready()
	lists(dock, call, strings.Replace(alphaContainers, `"name":"loose"`, `"name":"gone"`, 1))
	if out := session(t, dock, quick).Call(t, "container_update", map[string]any{"container": "loose"}); !strings.Contains(acctest.Str(out["note"]), "Dockhand said it made the container again and none of that name is listed now") {
		t.Errorf("an update that left no container = %v", out)
	}
	dock = ready()
	dock.Fail(pending, http.StatusInternalServerError, "Failed to clear")
	becomes(dock, call, "loose", "running", "exited")
	if out := session(t, dock, quick).Call(t, "container_update", map[string]any{"container": "loose"}); out["state"] != "exited" || !strings.Contains(acctest.Str(out["note"]), "Dockhand's note that an update was waiting could not be cleared") || !strings.Contains(acctest.Str(out["note"]), "it was running and is exited now") {
		t.Errorf("an update it did not stay up on = %v", out)
	}
}

func TestContainerLimits(t *testing.T) {
	t.Parallel()

	const (
		call    = "POST /api/containers/" + backID + "/update-runtime"
		inspect = "GET /api/containers/" + backID + "/inspect"
	)
	limited := strings.Replace(backInspect, `"RestartPolicy":{"Name":"no"},"Memory":0,"NanoCpus":0`, `"RestartPolicy":{"Name":"on-failure","MaximumRetryCount":3},"Memory":128000000,"NanoCpus":500000000`, 1)
	args := map[string]any{"container": "web-back-1", "memory_mb": 128, "cpus": 0.5, "restart": "on-failure:3"}

	dock := inspectable(t)
	suite := session(t, dock, everything)

	// said first
	dry := suite.Call(t, "container_limits", map[string]any{"container": "web-back-1", "memory_mb": 128, "cpus": 0.5, "restart": "on-failure:3", "dry_run": true})
	if got := acctest.Strs(t, dry["changes"], "changes"); !reflect.DeepEqual(got, []string{"would set memory: no limit to 128 megabytes", "would set processors: no limit to 0.5", "would set restart: no to on-failure:3"}) || acctest.Num(t, dry["memory_mb"], "memory_mb") != 0 || len(dock.Requests(call)) != 0 {
		t.Errorf("a dry run = %v", dry)
	}

	// set, with swap held to the same, and read back with what Docker warned of
	dock.Answer(call, `{"success":true,"warnings":["Your kernel does not support swap limit capabilities"]}`)
	dock.Then(call, func(dockhandtest.Request) { dock.Answer(inspect, limited) })
	out := suite.Call(t, "container_limits", args)
	if got := acctest.Strs(t, out["changes"], "changes"); len(got) != 3 || got[0] != "set memory: no limit to 128 megabytes" || acctest.Num(t, out["memory_mb"], "memory_mb") != 128 || acctest.Decimal(t, out["cpus"], "cpus") != 0.5 || out["restart"] != "on-failure" || len(acctest.Strs(t, out["warnings"], "warnings")) != 1 || !strings.Contains(acctest.Str(out["note"]), "it belongs to the stack web") {
		t.Errorf("setting limits = %v", out)
	}
	want := map[string]any{"Memory": float64(128000000), "MemorySwap": float64(128000000), "NanoCpus": float64(500000000), "RestartPolicy": map[string]any{"Name": "on-failure", "MaximumRetryCount": float64(3)}}
	if got := sent(t, dock.Last(call)); !reflect.DeepEqual(got, want) || dock.Last(call).Query.Get("env") != "1" {
		t.Errorf("setting limits sent %v, want %v", got, want)
	}

	// what it has already is left, and one thing alone is sent alone
	if out = suite.Call(t, "container_limits", map[string]any{"container": "web-back-1", "memory_mb": 128, "cpus": 0.5, "restart": "on-failure"}); len(acctest.Strs(t, out["changes"], "changes")) != 0 || !strings.Contains(acctest.Str(out["note"]), "nothing was done: it is so already") || len(dock.Requests(call)) != 1 {
		t.Errorf("setting the limits it has = %v", out)
	}
	if out = suite.Call(t, "container_limits", map[string]any{"container": "web-back-1", "restart": "always"}); !reflect.DeepEqual(sent(t, dock.Last(call)), map[string]any{"RestartPolicy": map[string]any{"Name": "always"}}) {
		t.Errorf("setting the restart policy alone sent %v: %v", sent(t, dock.Last(call)), out)
	}

	// refused before anything is asked
	for _, tc := range []struct {
		why  string
		args map[string]any
		want string
	}{
		{"nothing to set", map[string]any{}, "nothing to set was given"},
		{"memory below what Docker takes", map[string]any{"memory_mb": 2}, "memory_mb is 2: Docker limits a container to 6 megabytes or more"},
		{"less than no processor", map[string]any{"cpus": -1}, "cpus is -1"},
		{"a restart policy there is none of", map[string]any{"restart": "maybe"}, `restart is "maybe"`},
		{"a container there is none of", map[string]any{"container": "nope", "cpus": 1}, "nope"},
	} {
		given := map[string]any{"container": "web-back-1"}
		maps.Copy(given, tc.args)
		if msg := suite.CallErr(t, "container_limits", given); !strings.Contains(msg, tc.want) {
			t.Errorf("%s = %q, want it to say %q", tc.why, msg, tc.want)
		}
	}

	// refused by Docker, and set but not readable afterwards
	dock.Then(call, nil)
	dock.Fail(call, http.StatusInternalServerError, "Failed to update container")
	if msg := suite.CallErr(t, "container_limits", map[string]any{"container": "web-back-1", "cpus": 2}); !strings.Contains(msg, "the limits of web-back-1 on alpha were not changed") {
		t.Errorf("limits Docker refused = %q", msg)
	}
	dock.Answer(call, `{"success":true,"warnings":[]}`)
	dock.Then(call, func(dockhandtest.Request) {
		dock.Fail(inspect, http.StatusInternalServerError, "Failed to inspect container")
	})
	if out = suite.Call(t, "container_limits", map[string]any{"container": "web-back-1", "cpus": 2}); !strings.Contains(acctest.Str(out["note"]), "what it has now could not be read back") {
		t.Errorf("limits that could not be read back = %v", out)
	}
	if msg := suite.CallErr(t, "container_limits", map[string]any{"container": "web-back-1", "cpus": 2}); !strings.Contains(msg, "could not be inspected") {
		t.Errorf("limits of what could not be inspected = %q", msg)
	}
}

func TestContainerCompose(t *testing.T) {
	t.Parallel()

	const call = "GET /api/containers/" + looseID + "/compose"
	dock := twoHosts(t)
	dock.Answer(call, `{"compose":"services:\n  loose:\n    image: registry.example/tool:latest\n    environment:\n      - MODE=demo\n      - API_KEY=abc123\n","composeFullEnv":"services: {}\n","serviceName":"loose","stackProject":null}`)
	suite := session(t, dock, Options{})

	out := suite.Call(t, "container_compose", map[string]any{"container": "loose"})
	if text := acctest.Str(out["compose"]); out["env"] != "alpha" || out["container"] != "loose" || out["service"] != "loose" || out["stack"] != nil || !strings.Contains(text, "- MODE=demo") || !strings.Contains(text, "- API_KEY=***") || acctest.Num(t, out["hidden"], "hidden") != 1 || !strings.Contains(acctest.Str(out["note"]), "1 secret is hidden") || dock.Last(call).Query.Get("env") != "1" {
		t.Errorf("a container as compose = %v", out)
	}
	if out = suite.Call(t, "container_compose", map[string]any{"container": "loose", "reveal_secrets": true}); !strings.Contains(acctest.Str(out["compose"]), "API_KEY=abc123") || out["hidden"] != nil || out["note"] != nil {
		t.Errorf("a container as compose, secrets and all = %v", out)
	}

	// one a stack made has a file already, and that is said
	dock.Answer("GET /api/containers/"+frontID+"/compose", `{"compose":"services:\n  front:\n    image: registry.example/front:1.2\n","serviceName":"front","stackProject":"web"}`)
	if out = suite.Call(t, "container_compose", map[string]any{"container": "web-front-1"}); out["stack"] != "web" || !strings.Contains(acctest.Str(out["note"]), "it belongs to the stack web already") {
		t.Errorf("a stack's container as compose = %v", out)
	}

	dock.Fail(call, http.StatusInternalServerError, "Failed to generate compose")
	if msg := suite.CallErr(t, "container_compose", map[string]any{"container": "loose"}); !strings.Contains(msg, "loose on alpha could not be written out as compose") {
		t.Errorf("a container that could not be written out = %q", msg)
	}
	if msg := suite.CallErr(t, "container_compose", map[string]any{"container": "nope"}); !strings.Contains(msg, "nope") {
		t.Errorf("a container there is none of = %q", msg)
	}
}
