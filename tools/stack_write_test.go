package tools

import (
	"encoding/json"
	"net/http"
	"strconv"
	"strings"
	"testing"

	"github.com/katbyte/dockhand-mcp/sdk/dockhandtest"
	"github.com/katbyte/go-kt/mcp/acctest"
)

// What a 1.0.51 server answers a caller that asked for a job and then asks after it: a deploy that went well, one that compose refused, one still
// going, and a stack taken down.
const (
	deployDone    = `{"id":"d1","status":"done","lines":[{"event":"progress","data":{"status":"Deploying stack..."}},{"event":"progress","data":{"type":"line","line":" Container mail-spool-1 Recreate "}},{"event":"progress","data":{"type":"line","line":" Container mail-spool-1 Started "}},{"event":"result","data":{"success":true}}],"result":{"success":true,"output":" Container mail-spool-1 Recreate \n Container mail-spool-1 Recreate \n Container mail-spool-1 Recreated \n login with REGISTRY_TOKEN=abcdef123456 \n Container mail-spool-1 Started \n"}}`
	deployFailed  = `{"id":"d1","status":"done","lines":[{"event":"progress","data":{"status":"Deploying stack..."}},{"event":"result","data":{"success":false}}],"result":{"success":false,"error":" Image registry.example/spool:9 Pulling \nError response from daemon: failed to resolve reference \"registry.example/spool:9\": not found\n"}}`
	deployRunning = `{"id":"d1","status":"running","lines":[{"event":"progress","data":{"status":"Deploying stack..."}},{"event":"progress","data":{"type":"line","line":" Image registry.example/spool:3 Pulling "}}],"result":null}`
	downDone      = `{"id":"d1","status":"done","lines":[{"event":"progress","data":{"type":"line","line":" Container mail-smtp-1 Removed "}},{"event":"progress","data":{"type":"line","line":" Network mail_default Removed "}},{"event":"result","data":{"success":true}}],"result":{"success":true,"output":" Container mail-smtp-1 Removed \n Network mail_default Removed \n"}}`

	startedJob = `{"jobId":"d1"}`
	aJob       = "GET /api/jobs/d1"

	noFindings = `{"findings":[],"counts":{"error":0,"warn":0,"info":0}}`
	twoRemarks = `{"findings":[{"ruleId":"MISSING_HEALTHCHECK","severity":"info","service":"app","line":2,"message":"\"app\" has no healthcheck"},{"ruleId":"LATEST_TAG","severity":"warn","service":"app","line":3,"message":"\"app\" uses an untagged image"}],"counts":{"error":0,"warn":1,"info":1}}`
	unusable   = `{"findings":[{"ruleId":"YAML_PARSE_ERROR","severity":"error","line":2,"message":"unexpected end of the stream (2:1) "},{"ruleId":"COMPOSE_SCHEMA_ERROR","severity":"error","message":"services must be a mapping"},{"ruleId":"LATEST_TAG","severity":"warn","line":3,"message":"untagged"}],"counts":{"error":2,"warn":1,"info":0}}`

	blogFile = "services:\n  app:\n    image: registry.example/blog:1\n"
)

// stacksAfter is alpha's canned stacks after a change. A stack named in the changes is listed with the status given there: without its containers
// when that is created, with every one exited when it is stopped, and not at all when it is gone. A name alpha has no stack of is added, with one
// container.
func stacksAfter(t *testing.T, changes map[string]string) string {
	t.Helper()

	var stacks, after []map[string]any
	if err := json.Unmarshal([]byte(alphaStacks), &stacks); err != nil {
		t.Fatal(err)
	}
	seen := map[string]bool{}
	for _, stack := range stacks {
		name := acctest.Str(stack["name"])
		seen[name] = true
		status, changed := changes[name]
		switch {
		case !changed:
		case status == stackGone:
			continue
		case status == "created":
			stack["status"], stack["containers"], stack["containerDetails"] = status, []any{}, []any{}
		default:
			stack["status"] = status
			for _, c := range acctest.Rows(t, stack["containerDetails"], "containerDetails") {
				if status == stackStopped {
					c["state"], c["status"] = "exited", "Exited (0) 1 second ago"
				} else {
					c["state"], c["status"] = "running", "Up 1 second"
				}
			}
		}
		after = append(after, stack)
	}
	for name, status := range changes {
		if seen[name] || status == stackGone {
			continue
		}
		stack := map[string]any{"name": name, "status": status, "containers": []any{}, "containerDetails": []any{}}
		if status != "created" {
			stack["containers"] = []any{"ffff"}
			stack["containerDetails"] = []any{map[string]any{"id": "ffff", "name": name + "-app-1", "service": "app", "image": "registry.example/" + name + ":1", "state": "running", "status": "Up 1 second", "restartCount": 0, "ports": []any{}}}
		}
		after = append(after, stack)
	}
	body, err := json.Marshal(after)
	if err != nil {
		t.Fatal(err)
	}

	return string(body)
}

// then has the canned server list alpha's stacks another way from when a call is made: how what a tool did comes to be read back.
func then(t *testing.T, dock *dockhandtest.Server, call string, changes map[string]string) {
	t.Helper()

	listed := stacksAfter(t, changes)
	dock.Then(call, func(dockhandtest.Request) { dock.AnswerWhen("GET /api/stacks", "env", "1", listed) })
}

// runsWith is a stack's deploys with a newer run than the canned ones.
func runsWith(run int) string {
	return `{"runs":[{"id":` + strconv.Itoa(run) + `,"environmentId":1,"triggeredBy":"manual","triggeredAt":"2026-10-10T10:00:00.000Z","duration":900,"status":"success","errorMessage":null,"details":null},{"id":4,"environmentId":1,"triggeredBy":"webhook","triggeredAt":"2026-10-09T08:22:58.097Z","duration":923,"status":"failed","errorMessage":"no","details":null}]}`
}

func TestStackDeploy(t *testing.T) {
	t.Parallel()

	const call = "POST /api/stacks/mail/deploy"

	// started as a job, waited on, and read back: how the stack stood and stands, the deploy's number, and the end of what compose printed
	dock := withStackFiles(t)
	dock.Answer(call, startedJob)
	dock.Answer(aJob, deployRunning)
	asked := 0
	dock.Then(aJob, func(dockhandtest.Request) {
		asked++
		if asked == 2 {
			dock.Answer(aJob, deployDone)
		}
	})
	// from when the deploy is asked for, the stack is wholly up and has one more run
	listed := stacksAfter(t, map[string]string{"mail": "running"})
	dock.Then(call, func(dockhandtest.Request) {
		dock.AnswerWhen("GET /api/stacks", "env", "1", listed)
		dock.Answer("GET /api/stacks/mail/deploys", runsWith(5))
	})

	out := session(t, dock, quick).Call(t, "stack_deploy", map[string]any{"stack": "mail", "pull": true, "recreate": true})
	if out["env"] != "alpha" || out["stack"] != "mail" || out["was"] != "partial" || out["is"] != "running" || acctest.Num(t, out["run"], "run") != 5 || !acctest.IsBool(out["running"], false) || out["job"] != nil || out["progress"] != nil || out["note"] != nil || out["dry_run"] != nil {
		t.Errorf("a deploy = %v", out)
	}
	// a line said twice running is said once, and a secret that was printed is hidden
	if got := strings.Join(acctest.Strs(t, out["printed"], "printed"), "|"); got != "Container mail-spool-1 Recreate|Container mail-spool-1 Recreated|login with REGISTRY_TOKEN=***|Container mail-spool-1 Started" {
		t.Errorf("what compose printed = %s", got)
	}
	if got := strings.Join(acctest.Strs(t, out["changes"], "changes"), "|"); got != "brought up mail on alpha from /app/data/stacks/alpha/mail/compose.yaml, pulling its images first and making every container again" {
		t.Errorf("the changes = %s", got)
	}
	made := dock.Last(call)
	if body := sent(t, made); made.Query.Get("env") != "1" || made.Accept != "*/*" || !acctest.BoolOf(body["pull"]) || !acctest.BoolOf(body["forceRecreate"]) || body["build"] != nil {
		t.Errorf("the deploy was asked on environment %q with %s, accepting %q: want it asked as a job", made.Query.Get("env"), made.Body, made.Accept)
	}

	// a Dockhand that does the work first and answers how it went is taken at its word; a stack that is not wholly up afterwards is said not to be
	dock = withStackFiles(t)
	dock.Answer(call, `{"success":true,"output":" Container mail-spool-1 Started \n"}`)
	out = session(t, dock, quick).Call(t, "stack_deploy", map[string]any{"stack": "mail", "build": true})
	if out["is"] != "partial" || out["run"] != nil || !strings.Contains(acctest.Str(out["note"]), "it was deployed and is partial") || !strings.Contains(acctest.Strs(t, out["changes"], "changes")[0], "building its images first") {
		t.Errorf("a deploy answered without a job = %v", out)
	}
	if body := sent(t, dock.Last(call)); !acctest.BoolOf(body["build"]) || body["pull"] != nil || body["forceRecreate"] != nil {
		t.Errorf("a deploy that builds was sent %v", body)
	}

	// with dry_run it says what it would do and asks nothing of the host
	out = session(t, dock, quick).Call(t, "stack_deploy", map[string]any{"stack": "mail", "dry_run": true})
	if got := acctest.Strs(t, out["changes"], "changes"); len(got) != 1 || got[0] != "would bring up mail on alpha from /app/data/stacks/alpha/mail/compose.yaml" || !acctest.BoolOf(out["dry_run"]) || out["is"] != "partial" || len(dock.Requests(call)) != 1 {
		t.Errorf("a dry run = %v", out)
	}

	// what cannot be read back is said, and the deploy is still told
	dock = withStackFiles(t)
	dock.Answer(call, `{"success":true,"output":""}`)
	dock.Then(call, func(dockhandtest.Request) {
		dock.FailWhen("GET /api/stacks", "env", "1", http.StatusInternalServerError, "Failed to list stacks")
	})
	if out = session(t, dock, quick).Call(t, "stack_deploy", map[string]any{"stack": "mail", "env": "alpha"}); out["is"] != nil || !strings.Contains(acctest.Str(out["note"]), "how it stands after could not be read back") || len(acctest.Strs(t, out["changes"], "changes")) != 1 {
		t.Errorf("a deploy that could not be read back = %v", out)
	}
}

func TestStackDeployThatGoesWrong(t *testing.T) {
	t.Parallel()

	const call = "POST /api/stacks/mail/deploy"

	// compose refusing is an error that gives its last word, says nothing running changed, and where the rest is
	dock := withStackFiles(t)
	dock.Answer(call, startedJob)
	dock.Answer(aJob, deployFailed)
	dock.Then(call, func(dockhandtest.Request) { dock.Answer("GET /api/stacks/mail/deploys", runsWith(5)) })
	msg := session(t, dock, quick).CallErr(t, "stack_deploy", map[string]any{"stack": "mail"})
	for _, want := range []string{`mail on alpha was not deployed: Error response from daemon: failed to resolve reference "registry.example/spool:9": not found`, "what was running is left as it was", "stack_deploys with run 5 gives everything compose printed"} {
		if !strings.Contains(msg, want) {
			t.Errorf("a deploy compose refused = %q, want it to say %q", msg, want)
		}
	}

	// the same from a Dockhand that answered it outright, with no run recorded
	dock = withStackFiles(t)
	dock.Answer(call, `{"success":false,"error":"Stack compose file location not configured"}`)
	if msg = session(t, dock, quick).CallErr(t, "stack_deploy", map[string]any{"stack": "mail"}); !strings.Contains(msg, "was not deployed: Stack compose file location not configured; what was running is left as it was") || strings.Contains(msg, "stack_deploys") {
		t.Errorf("a deploy refused outright = %q", msg)
	}
	dock.Answer(call, `{"success":false}`)
	if msg = session(t, dock, quick).CallErr(t, "stack_deploy", map[string]any{"stack": "mail"}); !strings.Contains(msg, "Dockhand says it could not, and gives no reason") {
		t.Errorf("a deploy refused with no reason = %q", msg)
	}
	dock.Fail(call, http.StatusForbidden, "Permission denied")
	if msg = session(t, dock, quick).CallErr(t, "stack_deploy", map[string]any{"stack": "mail"}); !strings.Contains(msg, "was not deployed") || !strings.Contains(msg, "Permission denied") {
		t.Errorf("a deploy Dockhand would not start = %q", msg)
	}

	// still going when the wait is over: left running and said to be, with the job to follow
	dock = withStackFiles(t)
	dock.Answer(call, startedJob)
	dock.Answer(aJob, deployRunning)
	out := session(t, dock, quick).Call(t, "stack_deploy", map[string]any{"stack": "mail", "wait": 0})
	if !acctest.IsBool(out["running"], true) || out["job"] != "d1" || out["is"] != nil || out["printed"] != nil || len(acctest.Strs(t, out["changes"], "changes")) != 0 || !strings.Contains(acctest.Str(out["note"]), "the deploy was still going after 0 seconds") {
		t.Errorf("a deploy left running = %v", out)
	}
	if said := acctest.Strs(t, out["progress"], "progress"); len(said) != 2 || said[1] != "Image registry.example/spool:3 Pulling" {
		t.Errorf("its progress = %q", said)
	}

	// a stack Dockhand has no file for cannot be deployed, and neither can one whose file cannot be read
	suite := session(t, dock, quick)
	if msg = suite.CallErr(t, "stack_deploy", map[string]any{"stack": "web"}); !strings.Contains(msg, "Dockhand has no compose file for web") || !strings.Contains(msg, "stack_adopt") {
		t.Errorf("a stack with no file = %q", msg)
	}
	dock.Fail("GET /api/stacks/mail/compose", http.StatusInternalServerError, "Failed to read compose file")
	if msg = suite.CallErr(t, "stack_deploy", map[string]any{"stack": "mail"}); !strings.Contains(msg, "the compose file of mail could not be read, so nothing was deployed") {
		t.Errorf("a file that cannot be read = %q", msg)
	}
	if msg = suite.CallErr(t, "stack_deploy", map[string]any{"stack": "mail", "wait": 301}); !strings.Contains(msg, "wait is 301") {
		t.Errorf("a wait too long = %q", msg)
	}
	if msg = suite.CallErr(t, "stack_deploy", map[string]any{"stack": "nope"}); !strings.Contains(msg, `no stack is named "nope"`) {
		t.Errorf("a stack that is not there = %q", msg)
	}
}

func TestStackDown(t *testing.T) {
	t.Parallel()

	const (
		call    = "POST /api/stacks/mail/down"
		preview = `{"stackName":"mail","sourceType":"internal","stackDir":"/app/data/stacks/alpha/mail","gitDir":null,"namedVolumes":["mail_spool","mail_shared"],"canDeleteFiles":true}`
	)

	// a stack Dockhand has the file of: its containers are removed, read back as gone, and it is left with none
	dock := withStackFiles(t)
	dock.Answer(call, startedJob)
	dock.Answer(aJob, downDone)
	then(t, dock, call, map[string]string{"mail": "created"})
	out := session(t, dock, quick).Call(t, "stack_down", map[string]any{"stack": "mail"})
	if out["env"] != "alpha" || out["stack"] != "mail" || out["was"] != "partial" || out["is"] != "created" || strings.Join(acctest.Strs(t, out["removed"], "removed"), " ") != "mail-smtp-1 mail-spool-1" || out["volumes"] != nil || out["note"] != nil || !acctest.IsBool(out["running"], false) {
		t.Errorf("a stack taken down = %v", out)
	}
	if made := dock.Last(call); made.Query.Get("env") != "1" || made.Accept != "*/*" || made.Body != `{}` {
		t.Errorf("the down was asked on environment %q with %s, accepting %q", made.Query.Get("env"), made.Body, made.Accept)
	}

	// with its volumes: the ones that are gone are said, and one that is still there is too
	dock = withStackFiles(t)
	dock.Answer(call, `{"success":true,"output":"done"}`)
	dock.Answer("GET /api/stacks/mail/delete-preview", preview)
	dock.AnswerWhen("GET /api/volumes", "env", "1", `[{"name":"mail_shared","driver":"local"},{"name":"other","driver":"local"}]`)
	then(t, dock, call, map[string]string{"mail": "created"})
	suite := session(t, dock, quick)
	dry := suite.Call(t, "stack_down", map[string]any{"stack": "mail", "remove_volumes": true, "dry_run": true})
	if strings.Join(acctest.Strs(t, dry["removed"], "removed"), " ") != "mail-smtp-1 mail-spool-1" || strings.Join(acctest.Strs(t, dry["volumes"], "volumes"), " ") != "mail_spool mail_shared" || dry["is"] != "partial" || len(dock.Requests(call)) != 0 {
		t.Errorf("a dry run with volumes = %v", dry)
	}
	out = suite.Call(t, "stack_down", map[string]any{"stack": "mail", "remove_volumes": true})
	if strings.Join(acctest.Strs(t, out["volumes"], "volumes"), " ") != "mail_spool" || !strings.Contains(acctest.Str(out["note"]), "1 of its volumes are still there, which something else may be using: mail_shared") {
		t.Errorf("a stack taken down with its volumes = %v", out)
	}
	if body := sent(t, dock.Last(call)); !acctest.BoolOf(body["removeVolumes"]) {
		t.Errorf("down with volumes was sent %v", body)
	}
	// deleting what a volume holds is a delete: a server that was not started to delete refuses it, and still takes a stack down without
	if msg := session(t, dock, Options{EnableWrite: true}).CallErr(t, "stack_down", map[string]any{"stack": "mail", "remove_volumes": true}); !strings.Contains(msg, "this server was started without --enable-delete") {
		t.Errorf("volumes removed without deletes enabled = %q", msg)
	}

	// a stack Dockhand only found: all Dockhand does is stop it, and that is said rather than called a down
	dock = withStackFiles(t)
	dock.Answer("POST /api/stacks/web/down", `{"success":true,"output":"stop completed for 2 container(s): web-front-1, web-back-1"}`)
	then(t, dock, "POST /api/stacks/web/down", map[string]string{"web": "stopped"})
	suite = session(t, dock, quick)
	if dry = suite.Call(t, "stack_down", map[string]any{"stack": "web", "dry_run": true}); len(acctest.Strs(t, dry["removed"], "removed")) != 0 || !strings.Contains(acctest.Str(dry["note"]), "all it does is stop the stack's containers") {
		t.Errorf("a dry run on a stack with no file = %v", dry)
	}
	out = suite.Call(t, "stack_down", map[string]any{"stack": "web"})
	if out["was"] != "running" || out["is"] != "stopped" || len(acctest.Strs(t, out["removed"], "removed")) != 0 || !strings.Contains(acctest.Str(out["note"]), "nothing is removed") {
		t.Errorf("a stack with no file taken down = %v", out)
	}
	if msg := suite.CallErr(t, "stack_down", map[string]any{"stack": "web", "remove_volumes": true}); !strings.Contains(msg, "it removes none of its volumes") {
		t.Errorf("volumes of a stack with no file = %q", msg)
	}
}

func TestStackDownThatGoesWrong(t *testing.T) {
	t.Parallel()

	const call = "POST /api/stacks/mail/down"

	dock := withStackFiles(t)
	suite := session(t, dock, quick)

	dock.Answer(call, `{"success":false,"error":"No containers found for stack \"mail\""}`)
	if msg := suite.CallErr(t, "stack_down", map[string]any{"stack": "mail"}); !strings.Contains(msg, `mail on alpha was not taken down: Dockhand says No containers found for stack "mail"`) {
		t.Errorf("a down Dockhand refused = %q", msg)
	}
	dock.Fail(call, http.StatusForbidden, "Permission denied")
	if msg := suite.CallErr(t, "stack_down", map[string]any{"stack": "mail"}); !strings.Contains(msg, "was not taken down") || !strings.Contains(msg, "Permission denied") {
		t.Errorf("a down Dockhand would not start = %q", msg)
	}
	dock.Answer(call, startedJob)
	dock.Answer(aJob, `{"id":"d1","status":"error","lines":[{"event":"progress","data":{"type":"line","line":" Container mail-smtp-1 Stopping "}}],"result":null}`)
	if msg := suite.CallErr(t, "stack_down", map[string]any{"stack": "mail"}); !strings.Contains(msg, "was not taken down: Container mail-smtp-1 Stopping") {
		t.Errorf("a down whose job failed = %q", msg)
	}

	// still going when the wait is over
	dock.Answer(aJob, `{"id":"d1","status":"running","lines":[],"result":null}`)
	out := suite.Call(t, "stack_down", map[string]any{"stack": "mail", "wait": 0})
	if !acctest.IsBool(out["running"], true) || out["job"] != "d1" || out["is"] != nil || !strings.Contains(acctest.Str(out["note"]), "it was still being taken down after 0 seconds") {
		t.Errorf("a down left running = %v", out)
	}

	// containers that are still there afterwards are said
	dock.Answer(call, `{"success":true,"output":"done"}`)
	if out = suite.Call(t, "stack_down", map[string]any{"stack": "mail"}); len(acctest.Strs(t, out["removed"], "removed")) != 0 || !strings.Contains(acctest.Str(out["note"]), "2 of its containers are still there: mail-smtp-1, mail-spool-1") {
		t.Errorf("a down that left its containers = %v", out)
	}

	// what cannot be read, before and after
	dock.Fail("GET /api/volumes", http.StatusInternalServerError, "Failed to list volumes")
	dock.Answer("GET /api/stacks/mail/delete-preview", `{"stackName":"mail","sourceType":"internal","stackDir":null,"gitDir":null,"namedVolumes":["mail_spool"],"canDeleteFiles":true}`)
	if out = suite.Call(t, "stack_down", map[string]any{"stack": "mail", "remove_volumes": true}); out["volumes"] != nil || !strings.Contains(acctest.Str(out["note"]), "whether its volumes are gone could not be read back") {
		t.Errorf("volumes that could not be read back = %v", out)
	}
	dock.Fail("GET /api/stacks/mail/delete-preview", http.StatusInternalServerError, "Failed to compute preview")
	if msg := suite.CallErr(t, "stack_down", map[string]any{"stack": "mail", "remove_volumes": true}); !strings.Contains(msg, "the volumes of mail could not be listed, so nothing was taken down") {
		t.Errorf("volumes that could not be listed = %q", msg)
	}
	dock.Fail("GET /api/stacks/mail/compose", http.StatusInternalServerError, "Failed to read compose file")
	if msg := suite.CallErr(t, "stack_down", map[string]any{"stack": "mail"}); !strings.Contains(msg, "the compose file of mail could not be read, so nothing was taken down") {
		t.Errorf("a file that cannot be read = %q", msg)
	}
	if msg := suite.CallErr(t, "stack_down", map[string]any{"stack": "mail", "wait": -1}); !strings.Contains(msg, "wait is -1") {
		t.Errorf("a wait below nothing = %q", msg)
	}

	dock = withStackFiles(t)
	dock.Answer(call, `{"success":true,"output":"done"}`)
	dock.Then(call, func(dockhandtest.Request) {
		dock.FailWhen("GET /api/stacks", "env", "1", http.StatusInternalServerError, "Failed to list stacks")
	})
	if out = session(t, dock, quick).Call(t, "stack_down", map[string]any{"stack": "mail", "env": "alpha"}); out["is"] != nil || !strings.Contains(acctest.Str(out["note"]), "how it stands after could not be read back") {
		t.Errorf("a down that could not be read back = %v", out)
	}
}

func TestStackCreate(t *testing.T) {
	t.Parallel()

	const (
		check  = "POST /api/stacks/blog/validate"
		create = "POST /api/stacks"
		deploy = "POST /api/stacks/blog/deploy"
	)

	// checked, made without being started, then brought up as a job and read back
	dock := withStackFiles(t)
	dock.Answer(check, twoRemarks)
	dock.Answer(create, `{"success":true,"started":false}`)
	dock.Answer("GET /api/stacks/blog/compose", `{"content":"services: {}\n","stackDir":"/app/data/stacks/alpha/blog","composePath":"/app/data/stacks/alpha/blog/compose.yaml","envPath":null,"suggestedEnvPath":"/app/data/stacks/alpha/blog/.env"}`)
	dock.Answer(deploy, startedJob)
	dock.Answer(aJob, deployDone)
	dock.Answer("GET /api/stacks/blog/deploys", `{"runs":[{"id":7,"environmentId":1,"triggeredBy":"manual","triggeredAt":"2026-10-10T10:00:00.000Z","duration":900,"status":"success","errorMessage":null,"details":null}]}`)
	then(t, dock, deploy, map[string]string{"blog": "running"})

	args := map[string]any{"env": "alpha", "name": "blog", "compose": blogFile, "env_file": "TZ=UTC\n", "secrets": map[string]any{"DB_PASSWORD": "hunter2", "API_KEY": "k-1"}, "pull": true}
	out := session(t, dock, quick).Call(t, "stack_create", args)
	if out["env"] != "alpha" || out["stack"] != "blog" || out["compose_path"] != "/app/data/stacks/alpha/blog/compose.yaml" || out["status"] != "running" || acctest.Num(t, out["run"], "run") != 7 || !acctest.IsBool(out["running"], false) || out["progress"] != nil || len(acctest.Strs(t, out["printed"], "printed")) != 4 {
		t.Errorf("a stack made and brought up = %v", out)
	}
	if got := strings.Join(acctest.Strs(t, out["changes"], "changes"), "|"); got != "made the stack blog on alpha|brought it up" {
		t.Errorf("the changes = %s", got)
	}
	if note := acctest.Str(out["note"]); !strings.Contains(note, "the checks have 2 things to say of the file") {
		t.Errorf("the note = %q", note)
	}
	if body := sent(t, dock.Last(check)); body["compose"] != blogFile || body["existing"] != nil || dock.Last(check).Query.Get("env") != "1" {
		t.Errorf("the file was checked with %v", body)
	}
	// made by a call that is answered at once, with the secrets as secrets and never started by it
	made := dock.Last(create)
	body := sent(t, made)
	vars := acctest.Rows(t, body["envVars"], "envVars")
	if made.Query.Get("env") != "1" || made.Accept == "*/*" || body["name"] != "blog" || body["compose"] != blogFile || body["rawEnvContent"] != "TZ=UTC\n" || !acctest.IsBool(body["start"], false) || len(vars) != 2 || vars[0]["key"] != "API_KEY" || vars[0]["value"] != "k-1" || !acctest.BoolOf(vars[0]["isSecret"]) || vars[1]["key"] != "DB_PASSWORD" {
		t.Errorf("the stack was made with %s on environment %q, accepting %q", made.Body, made.Query.Get("env"), made.Accept)
	}
	if up := dock.Last(deploy); up.Accept != "*/*" || !acctest.BoolOf(sent(t, up)["pull"]) {
		t.Errorf("it was brought up with %s, accepting %q", up.Body, up.Accept)
	}

	// made and left with no containers when start is false
	dock = withStackFiles(t)
	dock.Answer(check, noFindings)
	dock.Answer(create, `{"success":true,"started":false}`)
	dock.Fail("GET /api/stacks/blog/compose", http.StatusInternalServerError, "Failed to read compose file")
	then(t, dock, create, map[string]string{"blog": "created"})
	suite := session(t, dock, quick)
	dry := suite.Call(t, "stack_create", map[string]any{"env": 1, "name": "blog", "compose": blogFile, "dry_run": true})
	if got := strings.Join(acctest.Strs(t, dry["changes"], "changes"), "|"); got != "would make the stack blog on alpha|would bring it up" || len(dock.Requests(create)) != 0 || dry["note"] != nil || dry["compose_path"] != "/app/data/stacks/alpha/blog/compose.yaml" {
		t.Errorf("a dry run = %v", dry)
	}
	if asked := dock.Last("GET /api/stacks/default-path"); asked.Query.Get("name") != "blog" || asked.Query.Get("env") != "1" {
		t.Errorf("where the file would lie was asked with %v", asked.Query)
	}

	// Dockhand not saying where is said, and the rest of the dry run stands
	dock.Fail("GET /api/stacks/default-path", http.StatusInternalServerError, "Failed to compute path")
	if dry = suite.Call(t, "stack_create", map[string]any{"env": 1, "name": "blog", "compose": blogFile, "dry_run": true}); dry["compose_path"] != nil || !strings.Contains(acctest.Str(dry["note"]), "where Dockhand would keep its file could not be asked") || len(acctest.Strs(t, dry["changes"], "changes")) != 2 {
		t.Errorf("a dry run when Dockhand will not say where = %v", dry)
	}
	out = suite.Call(t, "stack_create", map[string]any{"env": 1, "name": "blog", "compose": blogFile, "start": false})
	if got := acctest.Strs(t, out["changes"], "changes"); len(got) != 1 || out["status"] != "created" || out["run"] != nil || out["compose_path"] != nil || out["note"] != nil || len(dock.Requests(deploy)) != 0 {
		t.Errorf("a stack made and not started = %v", out)
	}
	if body := sent(t, dock.Last(create)); body["envVars"] != nil || body["rawEnvContent"] != nil {
		t.Errorf("a stack with no variables was made with %v", body)
	}
}

func TestStackCreateThatGoesWrong(t *testing.T) {
	t.Parallel()

	const (
		check  = "POST /api/stacks/blog/validate"
		create = "POST /api/stacks"
		deploy = "POST /api/stacks/blog/deploy"
	)

	dock := withStackFiles(t)
	dock.Answer(check, noFindings)
	dock.Answer("POST /api/stacks/web/validate", noFindings)
	suite := session(t, dock, quick)

	for _, tc := range []struct {
		args map[string]any
		want string
	}{
		{map[string]any{"env": "alpha", "name": "Bad Name", "compose": blogFile}, `name is "Bad Name"`},
		{map[string]any{"env": "alpha", "name": "blog", "compose": " \n"}, "compose is required"},
		{map[string]any{"env": "alpha", "name": "blog", "compose": blogFile, "secrets": map[string]any{"1BAD": "x"}}, `"1BAD" is no name for a secret`},
		{map[string]any{"env": "alpha", "name": "blog", "compose": blogFile, "secrets": map[string]any{"EMPTY": ""}}, "the secret EMPTY is given no value"},
		{map[string]any{"env": "alpha", "name": "blog", "compose": blogFile, "secrets": map[string]any{"HIDDEN": "***"}}, "the mark of a value that is hidden"},
		{map[string]any{"env": "alpha", "name": "blog", "compose": blogFile, "wait": 999}, "wait is 999"},
		{map[string]any{"name": "blog", "compose": blogFile}, "env is required"},
		// a name a stack has already, whether Dockhand only found it or keeps it
		{map[string]any{"env": "alpha", "name": "web", "compose": blogFile}, "a stack named web is already on alpha"},
		{map[string]any{"env": "alpha", "name": "mail", "compose": blogFile}, "a stack named mail is already on alpha"},
	} {
		if msg := suite.CallErr(t, "stack_create", tc.args); !strings.Contains(msg, tc.want) {
			t.Errorf("stack_create %v = %q, want %q", tc.args, msg, tc.want)
		}
	}
	if made := dock.Requests(create); len(made) != 0 {
		t.Errorf("a stack was made %d times by calls that were refused", len(made))
	}
	args := map[string]any{"env": "alpha", "name": "blog", "compose": blogFile}

	// a file compose would not take is refused with what is wrong with it
	dock.Answer(check, unusable)
	if msg := suite.CallErr(t, "stack_create", args); !strings.Contains(msg, "compose would not take the file, so nothing was made: line 2: unexpected end of the stream (2:1); services must be a mapping") || strings.Contains(msg, "untagged") {
		t.Errorf("a file compose would not take = %q", msg)
	}
	dock.Fail(check, http.StatusInternalServerError, "Failed to validate")
	if msg := suite.CallErr(t, "stack_create", args); !strings.Contains(msg, "the compose file could not be checked, so nothing was made") {
		t.Errorf("a file that could not be checked = %q", msg)
	}
	dock.Answer(check, noFindings)

	dock.Answer(create, `{"success":false,"error":"disk full"}`)
	if msg := suite.CallErr(t, "stack_create", args); !strings.Contains(msg, "the stack blog was not made on alpha: Dockhand says disk full") {
		t.Errorf("a stack Dockhand could not make = %q", msg)
	}
	dock.Fail(create, http.StatusBadRequest, "Stack name must be lowercase")
	if msg := suite.CallErr(t, "stack_create", args); !strings.Contains(msg, "the stack blog was not made on alpha") || !strings.Contains(msg, "Stack name must be lowercase") {
		t.Errorf("a stack Dockhand refused = %q", msg)
	}

	// made, and then not brought up: it is kept, and the error says so
	dock.Answer(create, `{"success":true,"started":false}`)
	dock.Fail("GET /api/stacks/blog/compose", http.StatusNotFound, "not yet")
	dock.Answer(deploy, `{"success":false,"error":"pull access denied"}`)
	if msg := suite.CallErr(t, "stack_create", args); !strings.Contains(msg, "the stack blog was made on alpha and could not be brought up: pull access denied; it is kept") || !strings.Contains(msg, "stack_delete removes it") {
		t.Errorf("a stack made and not brought up = %q", msg)
	}

	// still being brought up when the wait is over
	dock.Answer(deploy, startedJob)
	dock.Answer(aJob, deployRunning)
	args["wait"] = 0
	out := suite.Call(t, "stack_create", args)
	if !acctest.IsBool(out["running"], true) || out["job"] != "d1" || out["status"] != nil || !strings.Contains(acctest.Str(out["note"]), "it was made and was still being brought up after 0 seconds") || len(acctest.Strs(t, out["changes"], "changes")) != 1 {
		t.Errorf("a stack left being brought up = %v", out)
	}

	// brought up and not wholly running, and how it stands not readable
	delete(args, "wait")
	dock.Answer(deploy, `{"success":true,"output":""}`)
	then(t, dock, deploy, map[string]string{"blog": "partial"})
	dock.Answer("GET /api/stacks/blog/deploys", `{"runs":[]}`)
	if out = suite.Call(t, "stack_create", args); out["status"] != "partial" || !strings.Contains(acctest.Str(out["note"]), "it was brought up and is partial") {
		t.Errorf("a stack brought up in part = %v", out)
	}
	dock.AnswerWhen("GET /api/stacks", "env", "1", alphaStacks)
	dock.Then(deploy, func(dockhandtest.Request) {
		dock.FailWhen("GET /api/stacks", "env", "1", http.StatusInternalServerError, "Failed to list stacks")
	})
	if out = suite.Call(t, "stack_create", args); out["status"] != nil || !strings.Contains(acctest.Str(out["note"]), "how it stands could not be read back") {
		t.Errorf("a stack that could not be read back = %v", out)
	}

	// what cannot be listed before anything is made
	if msg := suite.CallErr(t, "stack_create", args); !strings.Contains(msg, "the stacks of alpha could not be listed, so nothing was made") {
		t.Errorf("stacks that cannot be listed = %q", msg)
	}
	dock.Then(deploy, nil)
	dock.AnswerWhen("GET /api/stacks", "env", "1", alphaStacks)
	dock.FailWhen("GET /api/stacks/sources", "env", "1", http.StatusInternalServerError, "Failed to get stack sources")
	if msg := suite.CallErr(t, "stack_create", args); !strings.Contains(msg, "the stacks Dockhand keeps for alpha could not be listed, so nothing was made") {
		t.Errorf("kept stacks that cannot be listed = %q", msg)
	}
}

func TestStackEdit(t *testing.T) {
	t.Parallel()

	const (
		check   = "POST /api/stacks/mail/validate"
		compose = "PUT /api/stacks/mail/compose"
		envFile = "PUT /api/stacks/mail/env/raw"
		secrets = "PUT /api/stacks/mail/env" //nolint:gosec // G101: the call that saves secrets, and no secret itself
		twoKept = `{"variables":[{"key":"TZ","value":"UTC","isSecret":false},{"key":"DB_PASSWORD","value":"***","isSecret":true},{"key":"OLD_KEY","value":"***","isSecret":true}],"injectedSecretKeys":[],"secretProvider":null}`
	)

	dock := withStackFiles(t)
	dock.Answer(check, twoRemarks)
	dock.Answer("GET /api/stacks/mail/env/raw", `{"content":"TZ=UTC\n"}`)
	dock.Answer("GET /api/stacks/mail/env", twoKept)
	dock.Answer(compose, `{"success":true}`)
	dock.Answer(envFile, `{"success":true}`)
	dock.Answer(secrets, `{"success":true,"count":2}`)
	suite := session(t, dock, quick)

	args := map[string]any{"stack": "mail", "compose": blogFile, "env_file": "TZ=Europe/Paris\n", "secrets": map[string]any{"API_KEY": "k-1"}, "remove_secrets": []any{"OLD_KEY"}}

	// with dry_run it says what it would save and saves nothing
	args["dry_run"] = true
	dry := suite.Call(t, "stack_edit", args)
	if got := strings.Join(acctest.Strs(t, dry["changes"], "changes"), "|"); got != "would replace the compose file at /app/data/stacks/alpha/mail/compose.yaml: 3 lines where there were 8|would replace the .env file|would set the secrets API_KEY and would remove the secrets OLD_KEY" {
		t.Errorf("a dry run = %s", got)
	}
	if len(dock.Requests(compose))+len(dock.Requests(envFile))+len(dock.Requests(secrets)) != 0 || !acctest.BoolOf(dry["dry_run"]) || strings.Contains(acctest.Str(dry["note"]), "stack_deploy") {
		t.Errorf("a dry run saved something, or said it had: %v", dry)
	}
	delete(args, "dry_run")

	out := suite.Call(t, "stack_edit", args)
	if got := strings.Join(acctest.Strs(t, out["changes"], "changes"), "|"); out["env"] != "alpha" || out["stack"] != "mail" || got != "replaced the compose file at /app/data/stacks/alpha/mail/compose.yaml: 3 lines where there were 8|replaced the .env file|set the secrets API_KEY and removed the secrets OLD_KEY" {
		t.Errorf("an edit = %v", out)
	}
	if note := acctest.Str(out["note"]); !strings.Contains(note, "the checks have 2 things to say of the new file") || !strings.Contains(note, "the stack runs as it did until it is deployed") {
		t.Errorf("the note = %q", note)
	}
	// the new file is checked as this stack's, and saved without the stack being brought up again
	if body := sent(t, dock.Last(check)); body["compose"] != blogFile || !acctest.BoolOf(body["existing"]) {
		t.Errorf("the new file was checked with %v", body)
	}
	if saved := dock.Last(compose); saved.Body != `{"content":"services:\n  app:\n    image: registry.example/blog:1\n","restart":false}` || saved.Query.Get("env") != "1" || saved.Accept == "*/*" {
		t.Errorf("the compose file was saved with %s, accepting %q", saved.Body, saved.Accept)
	}
	if saved := dock.Last(envFile); saved.Body != `{"content":"TZ=Europe/Paris\n"}` || saved.Query.Get("env") != "1" {
		t.Errorf("the .env file was saved with %s", saved.Body)
	}
	// the secrets that stay are sent back hidden, which keeps their values; the one removed is left out; none that is no secret is sent
	if saved := dock.Last(secrets); saved.Body != `{"variables":[{"isSecret":true,"key":"DB_PASSWORD","value":"***"},{"isSecret":true,"key":"API_KEY","value":"k-1"}]}` {
		t.Errorf("the secrets were saved with %s", saved.Body)
	}

	// what is given as the stack has it already is not saved again
	before := len(dock.Requests(compose)) + len(dock.Requests(envFile))
	out = suite.Call(t, "stack_edit", map[string]any{"stack": "mail", "compose": mailFile, "env_file": "TZ=UTC\n"})
	if len(acctest.Strs(t, out["changes"], "changes")) != 0 || !strings.Contains(acctest.Str(out["note"]), "nothing was changed") || len(dock.Requests(compose))+len(dock.Requests(envFile)) != before {
		t.Errorf("an edit that changes nothing = %v", out)
	}

	// an empty .env text removes the file, and the last secrets removed leave none
	out = suite.Call(t, "stack_edit", map[string]any{"stack": "mail", "env_file": "", "remove_secrets": []any{"OLD_KEY", "DB_PASSWORD"}})
	if got := strings.Join(acctest.Strs(t, out["changes"], "changes"), "|"); got != "removed the .env file|removed the secrets DB_PASSWORD, OLD_KEY" {
		t.Errorf("an edit that removes = %s", got)
	}
	if dock.Last(envFile).Body != `{"content":""}` || dock.Last(secrets).Body != `{"variables":[]}` {
		t.Errorf("removing was sent %s and %s", dock.Last(envFile).Body, dock.Last(secrets).Body)
	}

	// a file compose would not take is refused, and saved when force says to with what is wrong said
	dock.Answer(check, unusable)
	if msg := suite.CallErr(t, "stack_edit", map[string]any{"stack": "mail", "compose": "services: ["}); !strings.Contains(msg, "compose would not take the new file, so nothing was changed: line 2: unexpected end of the stream (2:1); services must be a mapping") {
		t.Errorf("a file compose would not take = %q", msg)
	}
	out = suite.Call(t, "stack_edit", map[string]any{"stack": "mail", "compose": "services: [", "force": true})
	if len(acctest.Strs(t, out["changes"], "changes")) != 1 || !strings.Contains(acctest.Str(out["note"]), "the file was saved as force asked though compose would not take it") {
		t.Errorf("a file saved by force = %v", out)
	}
}

func TestStackEditThatGoesWrong(t *testing.T) {
	t.Parallel()

	const (
		check   = "POST /api/stacks/mail/validate"
		compose = "PUT /api/stacks/mail/compose"
		envFile = "PUT /api/stacks/mail/env/raw"
		secrets = "PUT /api/stacks/mail/env" //nolint:gosec // G101: the call that saves secrets, and no secret itself
	)

	dock := withStackFiles(t)
	dock.Answer(check, noFindings)
	dock.Answer("GET /api/stacks/mail/env/raw", `{"content":"TZ=UTC\n"}`)
	suite := session(t, dock, quick)

	for _, tc := range []struct {
		args map[string]any
		want string
	}{
		{map[string]any{"stack": "mail"}, "nothing to change was given"},
		{map[string]any{"stack": "mail", "compose": " "}, "compose is empty"},
		{map[string]any{"stack": "mail", "secrets": map[string]any{"A B": "x"}}, `"A B" is no name for a secret`},
		{map[string]any{"stack": "mail", "secrets": map[string]any{"API_KEY": "x"}, "remove_secrets": []any{"API_KEY"}}, "the secret API_KEY is both given a value and named to be removed"},
		{map[string]any{"stack": "mail", "remove_secrets": []any{"TZ"}}, "mail has no secret named TZ to remove"},
		{map[string]any{"stack": "web", "env_file": "A=1\n"}, "Dockhand has no compose file for web"},
		{map[string]any{"stack": "nope", "env_file": "A=1\n"}, `no stack is named "nope"`},
	} {
		if msg := suite.CallErr(t, "stack_edit", tc.args); !strings.Contains(msg, tc.want) {
			t.Errorf("stack_edit %v = %q, want %q", tc.args, msg, tc.want)
		}
	}

	// a step that fails after another was saved says what was saved before it
	dock.Answer(compose, `{"success":true}`)
	dock.Fail(envFile, http.StatusBadRequest, "Refusing to write a masked placeholder")
	msg := suite.CallErr(t, "stack_edit", map[string]any{"stack": "mail", "compose": blogFile, "env_file": "A=***\n", "secrets": map[string]any{"API_KEY": "k"}})
	if !strings.Contains(msg, "mail was changed in part: its .env file could not be saved") || !strings.Contains(msg, "Refusing to write a masked placeholder") || !strings.Contains(msg, "before that, this was saved: replaced the compose file") || len(dock.Requests(secrets)) != 0 {
		t.Errorf("an edit that failed part way = %q", msg)
	}
	// and one that fails first says nothing was
	dock.Answer(compose, `{"success":false,"error":"read-only file system"}`)
	if msg = suite.CallErr(t, "stack_edit", map[string]any{"stack": "mail", "compose": blogFile}); !strings.Contains(msg, "mail was not changed: its compose file could not be saved: Dockhand says read-only file system") {
		t.Errorf("a compose file Dockhand could not save = %q", msg)
	}
	dock.Fail(compose, http.StatusInternalServerError, "Failed to save")
	if msg = suite.CallErr(t, "stack_edit", map[string]any{"stack": "mail", "compose": blogFile}); !strings.Contains(msg, "mail was not changed: its compose file could not be saved") {
		t.Errorf("a compose file that failed to save = %q", msg)
	}
	dock.Fail(secrets, http.StatusForbidden, "Permission denied")
	if msg = suite.CallErr(t, "stack_edit", map[string]any{"stack": "mail", "secrets": map[string]any{"API_KEY": "k"}}); !strings.Contains(msg, "mail was not changed: its secrets could not be saved") {
		t.Errorf("secrets that failed to save = %q", msg)
	}

	// what cannot be read or checked before anything is saved
	dock.Fail(check, http.StatusInternalServerError, "Failed to validate")
	if msg = suite.CallErr(t, "stack_edit", map[string]any{"stack": "mail", "compose": blogFile}); !strings.Contains(msg, "the new compose file could not be checked, so nothing was changed") {
		t.Errorf("a file that could not be checked = %q", msg)
	}
	dock.Fail("GET /api/stacks/mail/env/raw", http.StatusInternalServerError, "Failed to read")
	if msg = suite.CallErr(t, "stack_edit", map[string]any{"stack": "mail", "env_file": "A=1\n"}); !strings.Contains(msg, "the .env file of mail could not be read, so nothing was changed") {
		t.Errorf("a .env that could not be read = %q", msg)
	}
	dock.Fail("GET /api/stacks/mail/env", http.StatusInternalServerError, "Failed to read")
	if msg = suite.CallErr(t, "stack_edit", map[string]any{"stack": "mail", "secrets": map[string]any{"API_KEY": "k"}}); !strings.Contains(msg, "the secrets of mail could not be read, so nothing was changed") {
		t.Errorf("secrets that could not be read = %q", msg)
	}
	dock.Fail("GET /api/stacks/mail/compose", http.StatusInternalServerError, "Failed to read compose file")
	if msg = suite.CallErr(t, "stack_edit", map[string]any{"stack": "mail", "env_file": "A=1\n"}); !strings.Contains(msg, "the compose file of mail could not be read, so nothing was changed") {
		t.Errorf("a compose file that could not be read = %q", msg)
	}
}

func TestStackDelete(t *testing.T) {
	t.Parallel()

	const (
		call    = "DELETE /api/stacks/mail"
		preview = "GET /api/stacks/mail/delete-preview"
		kept    = `{"stackName":"mail","sourceType":"internal","stackDir":"/app/data/stacks/alpha/mail","gitDir":null,"namedVolumes":["mail_spool","mail_shared"],"canDeleteFiles":true}`
	)

	dock := withStackFiles(t)
	dock.Answer(preview, kept)
	dock.Answer(call, `{"success":true}`)
	suite := session(t, dock, quick)

	// without confirm it says what would go and removes nothing
	out := suite.Call(t, "stack_delete", map[string]any{"stack": "mail"})
	if out["env"] != "alpha" || out["stack"] != "mail" || out["kept"] != "internal" || out["files"] != "/app/data/stacks/alpha/mail" || acctest.BoolOf(out["deleted"]) || strings.Join(acctest.Strs(t, out["containers"], "containers"), "|") != "mail-smtp-1 (running)|mail-spool-1 (exited)" || strings.Join(acctest.Strs(t, out["volumes"], "volumes"), " ") != "mail_spool mail_shared" {
		t.Errorf("what removing would take = %v", out)
	}
	if out["note"] != "nothing was removed: call again with confirm to remove it" || len(dock.Requests(call)) != 0 || dock.Last(preview).Query.Get("env") != "1" {
		t.Errorf("a delete not confirmed = %v, after %d deletes", out["note"], len(dock.Requests(call)))
	}

	// confirmed: removed with its files and without its volumes, and read back as gone
	then(t, dock, call, map[string]string{"mail": stackGone})
	out = suite.Call(t, "stack_delete", map[string]any{"stack": "mail", "confirm": true})
	if !acctest.BoolOf(out["deleted"]) || out["note"] != nil || out["files"] != "/app/data/stacks/alpha/mail" {
		t.Errorf("a stack removed = %v", out)
	}
	if q := dock.Last(call).Query; q.Get("env") != "1" || q.Get("force") != "true" || q.Get("files") != "true" || q.Has("volumes") {
		t.Errorf("the delete was asked with %v", q)
	}

	// with its volumes and without its files: a volume that is still there afterwards is said
	dock = withStackFiles(t)
	dock.Answer(preview, kept)
	dock.Answer(call, `{"success":true}`)
	dock.AnswerWhen("GET /api/volumes", "env", "1", `[{"name":"mail_shared","driver":"local"}]`)
	then(t, dock, call, map[string]string{"mail": stackGone})
	out = session(t, dock, quick).Call(t, "stack_delete", map[string]any{"stack": "mail", "confirm": true, "volumes": true, "keep_files": true})
	if !acctest.BoolOf(out["deleted"]) || out["files"] != nil || !strings.Contains(acctest.Str(out["note"]), "1 of its volumes are still there, which something else may be using: mail_shared") {
		t.Errorf("a stack removed with its volumes = %v", out)
	}
	if q := dock.Last(call).Query; q.Get("files") != "false" || q.Get("volumes") != "true" {
		t.Errorf("the delete was asked with %v", q)
	}

	// a stack that was adopted: its files are where they lay and stay there
	dock = withStackFiles(t)
	dock.Answer(preview, `{"stackName":"mail","sourceType":"internal","stackDir":null,"gitDir":null,"namedVolumes":[],"canDeleteFiles":false}`)
	if out = session(t, dock, quick).Call(t, "stack_delete", map[string]any{"stack": "mail"}); out["files"] != nil || out["volumes"] != nil || !strings.Contains(acctest.Str(out["note"]), "the stack's files are outside Dockhand's own data directory, where it was shown them or they were moved to, and are not deleted; nothing was removed") {
		t.Errorf("an adopted stack = %v", out)
	}

	// a stack Dockhand only found: its containers go and nothing else, and its volumes cannot be asked for
	dock = withStackFiles(t)
	dock.Answer("GET /api/stacks/web/delete-preview", `{"stackName":"web","sourceType":null,"stackDir":null,"gitDir":null,"namedVolumes":["web_data"],"canDeleteFiles":false}`)
	dock.Answer("DELETE /api/stacks/web", `{"success":true}`)
	suite = session(t, dock, quick)
	if out = suite.Call(t, "stack_delete", map[string]any{"stack": "web"}); out["kept"] != "found" || out["files"] != nil || !strings.Contains(acctest.Str(out["note"]), "its containers are removed and nothing else") {
		t.Errorf("a stack with no file = %v", out)
	}
	if msg := suite.CallErr(t, "stack_delete", map[string]any{"stack": "web", "volumes": true, "confirm": true}); !strings.Contains(msg, "its volumes are not removed with it") || len(dock.Requests("DELETE /api/stacks/web")) != 0 {
		t.Errorf("the volumes of a stack with no file = %q", msg)
	}
	// Dockhand saying it removed a stack that is still listed is not taken at its word
	if out = suite.Call(t, "stack_delete", map[string]any{"stack": "web", "confirm": true}); acctest.BoolOf(out["deleted"]) || !strings.Contains(acctest.Str(out["note"]), "Dockhand said it removed the stack and it is still listed, as running with 2 containers") {
		t.Errorf("a stack that is still there = %v", out)
	}
}

func TestStackDeleteThatGoesWrong(t *testing.T) {
	t.Parallel()

	const (
		call    = "DELETE /api/stacks/mail"
		preview = "GET /api/stacks/mail/delete-preview"
	)

	dock := withStackFiles(t)
	suite := session(t, dock, quick)
	args := map[string]any{"stack": "mail", "confirm": true}

	dock.Fail(preview, http.StatusInternalServerError, "Failed to compute preview")
	if msg := suite.CallErr(t, "stack_delete", args); !strings.Contains(msg, "what removing mail would take could not be read, so nothing was removed") {
		t.Errorf("a preview that failed = %q", msg)
	}
	dock.Answer(preview, `{"stackName":"mail","sourceType":"git","stackDir":"/app/data/git/mail","gitDir":"/app/data/git-repos/mail","namedVolumes":[],"canDeleteFiles":true}`)
	dock.Fail(call, http.StatusBadRequest, "failed to parse compose.yaml")
	if msg := suite.CallErr(t, "stack_delete", args); !strings.Contains(msg, "mail on alpha was not removed") || !strings.Contains(msg, "failed to parse compose.yaml") {
		t.Errorf("a delete Dockhand refused = %q", msg)
	}
	dock.Answer(call, `{"success":false}`)
	if msg := suite.CallErr(t, "stack_delete", args); !strings.Contains(msg, "mail on alpha was not removed, and Dockhand gives no reason") {
		t.Errorf("a delete that did not succeed = %q", msg)
	}
	if msg := suite.CallErr(t, "stack_delete", map[string]any{"stack": "nope", "confirm": true}); !strings.Contains(msg, `no stack is named "nope"`) {
		t.Errorf("a stack that is not there = %q", msg)
	}

	dock.Answer(call, `{"success":true}`)
	dock.Then(call, func(dockhandtest.Request) {
		dock.FailWhen("GET /api/stacks", "env", "1", http.StatusInternalServerError, "Failed to list stacks")
	})
	args["env"] = "alpha"
	if out := suite.Call(t, "stack_delete", args); out["kept"] != "git" || acctest.BoolOf(out["deleted"]) || !strings.Contains(acctest.Str(out["note"]), "that it is gone could not be read back") {
		t.Errorf("a delete that could not be read back = %v", out)
	}
}

func TestStackScan(t *testing.T) {
	t.Parallel()

	const (
		usable = "POST /api/stacks/validate-path"
		scan   = "POST /api/stacks/scan"
	)

	dock := withStackFiles(t)
	dock.Answer(usable, `{"valid":true,"resolvedPath":"/srv/found"}`)
	dock.Answer(scan, `{"discovered":[{"name":"web","composePath":"/srv/found/web/docker-compose.yml","envPath":null,"sourceDir":"/srv/found/web","serviceCount":2,"runningOn":[{"envId":1,"envName":"alpha","containerCount":2}],"unadoptable":false},{"name":"fresh","composePath":"/srv/found/fresh/compose.yaml","envPath":"/srv/found/fresh/.env","sourceDir":"/srv/found/fresh","serviceCount":1,"unadoptable":false}],"adopted":[],"skipped":[{"name":"mail","composePath":"/srv/found/mail/compose.yaml","envPath":null,"sourceDir":"/srv/found/mail","serviceCount":2}],"errors":[{"path":"/srv/found/broken","error":"permission denied"}]}`)
	suite := session(t, dock, Options{})

	out := suite.Call(t, "stack_scan", map[string]any{"path": " /srv/found "})
	rows := acctest.Rows(t, out["found"], "found")
	if out["path"] != "/srv/found" || len(rows) != 3 || rows[0]["name"] != "fresh" || rows[1]["name"] != "mail" || rows[2]["name"] != "web" || out["note"] != nil {
		t.Fatalf("what was found = %v", out)
	}
	if rows[0]["compose_path"] != "/srv/found/fresh/compose.yaml" || rows[0]["env_path"] != "/srv/found/fresh/.env" || acctest.Num(t, rows[0]["services"], "services") != 1 || rows[0]["running_on"] != nil || rows[0]["adopted"] != nil {
		t.Errorf("a folder that is nothing yet = %v", rows[0])
	}
	if !acctest.BoolOf(rows[1]["adopted"]) || rows[2]["env_path"] != nil || strings.Join(acctest.Strs(t, rows[2]["running_on"], "running_on"), "|") != "alpha (2 containers)" {
		t.Errorf("one Dockhand has, and one that is running = %v, %v", rows[1], rows[2])
	}
	if got := acctest.Strs(t, out["errors"], "errors"); len(got) != 1 || got[0] != "/srv/found/broken: permission denied" {
		t.Errorf("what could not be read = %q", got)
	}
	if dock.Last(usable).Body != `{"path":"/srv/found"}` || dock.Last(scan).Body != `{"path":"/srv/found"}` {
		t.Errorf("the path was sent as %s and %s", dock.Last(usable).Body, dock.Last(scan).Body)
	}

	dock.Answer(scan, `{"discovered":[],"adopted":[],"skipped":[],"errors":[]}`)
	if out = suite.Call(t, "stack_scan", map[string]any{"path": "/srv/empty"}); len(acctest.Rows(t, out["found"], "found")) != 0 || out["errors"] != nil || out["note"] != "no folder in that directory holds a compose file" {
		t.Errorf("a directory with nothing in it = %v", out)
	}

	// a path Dockhand's own container cannot see is refused with where it is looked for
	dock.Fail(scan, http.StatusInternalServerError, "Failed to scan")
	if msg := suite.CallErr(t, "stack_scan", map[string]any{"path": "/srv/found"}); !strings.Contains(msg, "/srv/found could not be looked through") {
		t.Errorf("a scan that failed = %q", msg)
	}
	dock.Answer(usable, `{"valid":false,"error":"Path does not exist"}`)
	if msg := suite.CallErr(t, "stack_scan", map[string]any{"path": "/host/only"}); !strings.Contains(msg, "/host/only cannot be looked through: Dockhand says Path does not exist") || !strings.Contains(msg, "inside Dockhand's own container") {
		t.Errorf("a path Dockhand cannot see = %q", msg)
	}
	dock.Fail(usable, http.StatusForbidden, "Permission denied")
	if msg := suite.CallErr(t, "stack_scan", map[string]any{"path": "/srv/found"}); !strings.Contains(msg, "/srv/found could not be looked for") {
		t.Errorf("a path that could not be checked = %q", msg)
	}
	if msg := suite.CallErr(t, "stack_scan", map[string]any{"path": " "}); !strings.Contains(msg, "path is required") {
		t.Errorf("no path = %q", msg)
	}
}

func TestStackAdopt(t *testing.T) {
	t.Parallel()

	const (
		call = "POST /api/stacks/adopt"
		file = "/srv/found/web/docker-compose.yml"
	)

	// a stack that is running: Dockhand is given its file, and nothing about it changes
	dock := withStackFiles(t)
	dock.Answer(call, `{"adopted":["web"],"failed":[]}`)
	dock.Then(call, func(dockhandtest.Request) {
		dock.Answer("GET /api/stacks/web/compose", `{"content":"services: {}\n","stackDir":"/srv/found/web","composePath":"/srv/found/web/docker-compose.yml","envPath":null,"suggestedEnvPath":"/srv/found/web/.env"}`)
	})
	suite := session(t, dock, quick)
	args := map[string]any{"env": "alpha", "name": "web", "compose_path": " " + file + " "}

	args["dry_run"] = true
	dry := suite.Call(t, "stack_adopt", args)
	if got := acctest.Strs(t, dry["changes"], "changes"); len(got) != 1 || got[0] != "would give Dockhand /srv/found/web/docker-compose.yml as the compose file of web on alpha, which is running there with 2 containers" || dry["status"] != "running" || len(dock.Requests(call)) != 0 || dry["note"] != nil {
		t.Errorf("a dry run = %v", dry)
	}
	delete(args, "dry_run")

	out := suite.Call(t, "stack_adopt", args)
	if got := acctest.Strs(t, out["changes"], "changes"); out["env"] != "alpha" || out["stack"] != "web" || out["compose_path"] != file || out["status"] != "running" || len(got) != 1 || !strings.HasPrefix(got[0], "gave Dockhand /srv/found/web/docker-compose.yml as the compose file of web on alpha") {
		t.Errorf("a running stack adopted = %v", out)
	}
	if note := acctest.Str(out["note"]); !strings.HasPrefix(note, "nothing running was touched") {
		t.Errorf("the note = %q", note)
	}
	if dock.Last(call).Body != `{"environmentId":1,"stacks":[{"composePath":"/srv/found/web/docker-compose.yml","name":"web"}]}` {
		t.Errorf("the adoption was sent %s", dock.Last(call).Body)
	}

	// one that is running nowhere yet, and whose file cannot be read back afterwards
	dock = withStackFiles(t)
	dock.Answer(call, `{"adopted":["fresh"],"failed":[]}`)
	dock.Fail("GET /api/stacks/fresh/compose", http.StatusNotFound, "not yet")
	then(t, dock, call, map[string]string{"fresh": "created"})
	out = session(t, dock, quick).Call(t, "stack_adopt", map[string]any{"env": 1, "name": "fresh", "compose_path": "/srv/found/fresh/compose.yaml"})
	if out["status"] != "created" || !strings.Contains(acctest.Strs(t, out["changes"], "changes")[0], "which has no containers there yet") || !strings.HasPrefix(acctest.Str(out["note"]), "that Dockhand now has the file could not be read back; nothing running was touched") {
		t.Errorf("a stack that runs nowhere adopted = %v", out)
	}
}

func TestStackAdoptThatGoesWrong(t *testing.T) {
	t.Parallel()

	const call = "POST /api/stacks/adopt"

	dock := withStackFiles(t)
	suite := session(t, dock, quick)

	for _, tc := range []struct {
		args map[string]any
		want string
	}{
		{map[string]any{"env": "alpha", "name": "Web", "compose_path": "/srv/x.yaml"}, `name is "Web"`},
		{map[string]any{"env": "alpha", "name": "web", "compose_path": " "}, "compose_path is required"},
		{map[string]any{"name": "web", "compose_path": "/srv/x.yaml"}, "env is required"},
		// Dockhand has a file for it already
		{map[string]any{"env": "alpha", "name": "mail", "compose_path": "/srv/x.yaml"}, "Dockhand already has a compose file for mail on alpha"},
	} {
		if msg := suite.CallErr(t, "stack_adopt", tc.args); !strings.Contains(msg, tc.want) {
			t.Errorf("stack_adopt %v = %q, want %q", tc.args, msg, tc.want)
		}
	}
	args := map[string]any{"env": "alpha", "name": "web", "compose_path": "/srv/ghost/compose.yaml"}

	dock.Answer(call, `{"adopted":[],"failed":[{"name":"web","error":"Compose file not found on Dockhand's filesystem: /srv/ghost/compose.yaml."}]}`)
	if msg := suite.CallErr(t, "stack_adopt", args); !strings.Contains(msg, "web was not adopted: Dockhand says Compose file not found on Dockhand's filesystem") {
		t.Errorf("a file Dockhand cannot see = %q", msg)
	}
	dock.Answer(call, `{"adopted":[],"failed":[]}`)
	if msg := suite.CallErr(t, "stack_adopt", args); !strings.Contains(msg, "web was not adopted, and Dockhand gives no reason") {
		t.Errorf("an adoption that did nothing = %q", msg)
	}
	dock.Fail(call, http.StatusForbidden, "Permission denied")
	if msg := suite.CallErr(t, "stack_adopt", args); !strings.Contains(msg, "web was not adopted") || !strings.Contains(msg, "Permission denied") {
		t.Errorf("an adoption Dockhand refused = %q", msg)
	}

	dock.FailWhen("GET /api/stacks", "env", "1", http.StatusInternalServerError, "Failed to list stacks")
	if msg := suite.CallErr(t, "stack_adopt", args); !strings.Contains(msg, "the stacks of alpha could not be listed, so nothing was adopted") {
		t.Errorf("stacks that cannot be listed = %q", msg)
	}
	dock.FailWhen("GET /api/stacks/sources", "env", "1", http.StatusInternalServerError, "Failed to get stack sources")
	if msg := suite.CallErr(t, "stack_adopt", args); !strings.Contains(msg, "the stacks Dockhand keeps for alpha could not be listed, so nothing was adopted") {
		t.Errorf("kept stacks that cannot be listed = %q", msg)
	}
}

func TestStackMove(t *testing.T) {
	t.Parallel()

	const (
		check  = "POST /api/stacks/mail/check-path-change"
		usable = "POST /api/stacks/validate-path"
		move   = "POST /api/stacks/mail/relocate"
		to     = "/stacks/mail/compose.yaml"
		change = `{"hasChanges":true,"oldDir":"/app/data/stacks/alpha/mail","newDir":"/stacks/mail","fileCount":2,"currentComposePath":"/app/data/stacks/alpha/mail/compose.yaml","persistenceWarning":null}`
	)

	dock := withStackFiles(t)
	dock.Answer(check, change)
	dock.Answer(usable, `{"valid":false,"error":"Path does not exist"}`)
	dock.Answer(move, `{"success":true,"movedFiles":["compose.yaml",".env"],"composeContent":"services: {}\n","rawEnvContent":"TZ=UTC\n","envVars":[]}`)
	dock.Then(move, func(dockhandtest.Request) {
		dock.Answer("GET /api/stacks/mail/compose", strings.ReplaceAll(mailCompose, "/app/data/stacks/alpha/mail", "/stacks/mail"))
	})
	suite := session(t, dock, quick)

	// said first, then moved, and where the file now is read back
	dry := suite.Call(t, "stack_move", map[string]any{"stack": "mail", "compose_path": to, "dry_run": true})
	if got := acctest.Strs(t, dry["changes"], "changes"); len(got) != 1 || got[0] != "would move the 2 files of mail from /app/data/stacks/alpha/mail to /stacks/mail" || dry["is"] != "/app/data/stacks/alpha/mail/compose.yaml" || len(dock.Requests(move)) != 0 || dry["note"] != nil {
		t.Errorf("a dry run = %v", dry)
	}
	out := suite.Call(t, "stack_move", map[string]any{"stack": "mail", "compose_path": " " + to + " "})
	if out["env"] != "alpha" || out["stack"] != "mail" || out["was"] != "/app/data/stacks/alpha/mail/compose.yaml" || out["is"] != to || strings.Join(acctest.Strs(t, out["moved"], "moved"), " ") != ".env compose.yaml" || !strings.HasPrefix(acctest.Str(out["note"]), "nothing running was touched") {
		t.Errorf("a stack moved = %v", out)
	}
	if got := acctest.Strs(t, out["changes"], "changes"); len(got) != 1 || got[0] != "moved the 2 files of mail from /app/data/stacks/alpha/mail to /stacks/mail" {
		t.Errorf("the changes = %v", got)
	}
	if dock.Last(check).Body != `{"newComposePath":"/stacks/mail/compose.yaml"}` || dock.Last(usable).Body != `{"path":"/stacks/mail"}` || dock.Last(move).Body != `{"newComposePath":"/stacks/mail/compose.yaml","oldDir":"/app/data/stacks/alpha/mail"}` || dock.Last(move).Query.Get("env") != "1" {
		t.Errorf("the move was asked with %s, %s and %s", dock.Last(check).Body, dock.Last(usable).Body, dock.Last(move).Body)
	}

	// moved to where it is: nothing to do, whether Dockhand or the path itself says so
	if out = suite.Call(t, "stack_move", map[string]any{"stack": "mail", "compose_path": to}); len(acctest.Strs(t, out["changes"], "changes")) != 0 || out["note"] != "nothing was moved: that is where the stack's files are already" || len(dock.Requests(move)) != 1 {
		t.Errorf("a stack moved to where it is = %v", out)
	}
	dock.Answer(check, strings.Replace(change, `"hasChanges":true`, `"hasChanges":false`, 1))
	if out = suite.Call(t, "stack_move", map[string]any{"stack": "mail", "compose_path": "/stacks/mail/other.yaml"}); len(acctest.Strs(t, out["changes"], "changes")) != 0 || len(dock.Requests(move)) != 1 {
		t.Errorf("a move Dockhand says changes nothing = %v", out)
	}
}

func TestStackMoveThatGoesWrong(t *testing.T) {
	t.Parallel()

	const (
		check  = "POST /api/stacks/mail/check-path-change"
		usable = "POST /api/stacks/validate-path"
		move   = "POST /api/stacks/mail/relocate"
		to     = "/stacks/mail/compose.yaml"
	)

	dock := withStackFiles(t)
	dock.Answer(check, `{"hasChanges":true,"oldDir":"/app/data/stacks/alpha/mail","newDir":"/stacks/mail","fileCount":2,"currentComposePath":"/app/data/stacks/alpha/mail/compose.yaml","persistenceWarning":"The new location is not on a volume that outlives Dockhand's container"}`)
	dock.Answer(usable, `{"valid":true,"resolvedPath":"/stacks/mail"}`)
	suite := session(t, dock, quick)
	args := map[string]any{"stack": "mail", "compose_path": to}

	for _, path := range []string{"compose.yaml", "/compose.yaml", "/stacks/mail/", " "} {
		if msg := suite.CallErr(t, "stack_move", map[string]any{"stack": "mail", "compose_path": path}); !strings.Contains(msg, "it is the whole path the compose file is to have") {
			t.Errorf("a move to %q = %q", path, msg)
		}
	}
	if msg := suite.CallErr(t, "stack_move", map[string]any{"stack": "web", "compose_path": to}); !strings.Contains(msg, "Dockhand has no compose file for web") {
		t.Errorf("a stack with no file = %q", msg)
	}

	// a folder that is there is refused, since Dockhand writes over what it holds, unless that is asked for; what Dockhand warns of is passed on
	if msg := suite.CallErr(t, "stack_move", args); !strings.Contains(msg, "/stacks/mail is there already, and Dockhand would write over any file in it") || len(dock.Requests(move)) != 0 {
		t.Errorf("a move into a folder that is there = %q", msg)
	}
	dock.Answer(move, `{"success":true,"movedFiles":["compose.yaml"]}`)
	out := suite.Call(t, "stack_move", map[string]any{"stack": "mail", "compose_path": to, "overwrite": true})
	if note := acctest.Str(out["note"]); !strings.HasPrefix(note, "Dockhand warns: The new location is not on a volume") || !strings.Contains(note, "Dockhand said it moved the files and has the compose file at /app/data/stacks/alpha/mail/compose.yaml") {
		t.Errorf("a move into a folder that is there, asked for = %v", out)
	}
	// a path that is a file, or that Dockhand will not have
	dock.Answer(usable, `{"valid":false,"error":"Path is not a directory"}`)
	if msg := suite.CallErr(t, "stack_move", args); !strings.Contains(msg, "the stack's files cannot be moved to /stacks/mail: Dockhand says Path is not a directory") {
		t.Errorf("a move to what is no folder = %q", msg)
	}
	dock.Answer(usable, `{"valid":false,"error":"Path does not exist"}`)

	dock.Answer(move, `{"success":false,"errors":["EXDEV: cross-device link not permitted","EACCES: .env"]}`)
	if msg := suite.CallErr(t, "stack_move", args); !strings.Contains(msg, "the files of mail were not moved: Dockhand says EXDEV: cross-device link not permitted; EACCES: .env") {
		t.Errorf("a move that failed = %q", msg)
	}
	dock.Fail(move, http.StatusBadRequest, "Source directory does not exist")
	if msg := suite.CallErr(t, "stack_move", args); !strings.Contains(msg, "the files of mail were not moved") || !strings.Contains(msg, "Source directory does not exist") {
		t.Errorf("a move Dockhand refused = %q", msg)
	}

	// moved, and where the file now is not readable
	dock.Answer(move, `{"success":true,"movedFiles":["compose.yaml"]}`)
	dock.Then(move, func(dockhandtest.Request) {
		dock.Fail("GET /api/stacks/mail/compose", http.StatusInternalServerError, "Failed to read compose file")
	})
	if out = suite.Call(t, "stack_move", args); out["is"] != "" || !strings.Contains(acctest.Str(out["note"]), "where the compose file now is could not be read back") {
		t.Errorf("a move that could not be read back = %v", out)
	}
	dock.Then(move, nil)
	dock.Answer("GET /api/stacks/mail/compose", mailCompose)

	// what cannot be read before anything is moved
	dock.Fail(usable, http.StatusForbidden, "Permission denied")
	if msg := suite.CallErr(t, "stack_move", args); !strings.Contains(msg, "whether /stacks/mail is there already could not be read, so nothing was moved") {
		t.Errorf("a folder that could not be looked for = %q", msg)
	}
	dock.Fail(check, http.StatusInternalServerError, "Failed to check")
	if msg := suite.CallErr(t, "stack_move", args); !strings.Contains(msg, "what moving mail would take could not be read, so nothing was moved") {
		t.Errorf("a move that could not be checked = %q", msg)
	}
	dock.Fail("GET /api/stacks/mail/compose", http.StatusInternalServerError, "Failed to read compose file")
	if msg := suite.CallErr(t, "stack_move", args); !strings.Contains(msg, "the compose file of mail could not be read, so nothing was moved") {
		t.Errorf("a file that could not be read = %q", msg)
	}
}

func TestStackDeploysDelete(t *testing.T) {
	t.Parallel()

	const call = "DELETE /api/stacks/mail/deploys/2"

	dock := withStackFiles(t)
	dock.Answer(call, `{"success":true}`)
	suite := session(t, dock, quick)

	// without confirm it says which deploy would go and removes nothing
	out := suite.Call(t, "stack_deploys_delete", map[string]any{"stack": "mail", "run": 2})
	deploy := acctest.Object(t, out["deploy"], "deploy")
	if out["env"] != "alpha" || out["stack"] != "mail" || acctest.Num(t, deploy["run"], "run") != 2 || deploy["status"] != "success" || acctest.BoolOf(out["deleted"]) || !strings.Contains(acctest.Str(out["note"]), "nothing was removed") || len(dock.Requests(call)) != 0 {
		t.Errorf("a delete not confirmed = %v", out)
	}

	// Dockhand saying it removed a record that is still listed is not taken at its word
	if out = suite.Call(t, "stack_deploys_delete", map[string]any{"stack": "mail", "run": 2, "confirm": true}); acctest.BoolOf(out["deleted"]) || out["note"] != "Dockhand said it removed the record and it is still listed" {
		t.Errorf("a record that is still there = %v", out)
	}
	// removed, and read back as gone
	dock.Then(call, func(dockhandtest.Request) { dock.Answer("GET /api/stacks/mail/deploys", runsWith(5)) })
	if out = suite.Call(t, "stack_deploys_delete", map[string]any{"stack": "mail", "run": 2, "confirm": true}); !acctest.BoolOf(out["deleted"]) || out["note"] != nil {
		t.Errorf("a record removed = %v", out)
	}

	for _, tc := range []struct {
		args map[string]any
		want string
	}{
		{map[string]any{"stack": "mail", "run": 0}, "run is 0"},
		{map[string]any{"stack": "mail", "run": 99, "confirm": true}, "mail has no deploy numbered 99"},
		{map[string]any{"stack": "nope", "run": 2}, `no stack is named "nope"`},
	} {
		if msg := suite.CallErr(t, "stack_deploys_delete", tc.args); !strings.Contains(msg, tc.want) {
			t.Errorf("stack_deploys_delete %v = %q, want %q", tc.args, msg, tc.want)
		}
	}

	dock = withStackFiles(t)
	suite = session(t, dock, quick)
	dock.Answer(call, `{"success":false}`)
	if msg := suite.CallErr(t, "stack_deploys_delete", map[string]any{"stack": "mail", "run": 2, "confirm": true}); !strings.Contains(msg, "the record of deploy 2 of mail was not removed, and Dockhand gives no reason") {
		t.Errorf("a record Dockhand did not remove = %q", msg)
	}
	dock.Fail(call, http.StatusNotFound, "Deploy run not found")
	if msg := suite.CallErr(t, "stack_deploys_delete", map[string]any{"stack": "mail", "run": 2, "confirm": true}); !strings.Contains(msg, "the record of deploy 2 of mail was not removed") || !strings.Contains(msg, "Deploy run not found") {
		t.Errorf("a record Dockhand refused to remove = %q", msg)
	}
	dock.Answer(call, `{"success":true}`)
	dock.Then(call, func(dockhandtest.Request) {
		dock.Fail("GET /api/stacks/mail/deploys", http.StatusInternalServerError, "Failed to list deploys")
	})
	if out = suite.Call(t, "stack_deploys_delete", map[string]any{"stack": "mail", "run": 2, "confirm": true}); acctest.BoolOf(out["deleted"]) || !strings.Contains(acctest.Str(out["note"]), "that it is gone could not be read back") {
		t.Errorf("a removal that could not be read back = %v", out)
	}
	if msg := suite.CallErr(t, "stack_deploys_delete", map[string]any{"stack": "mail", "run": 2}); !strings.Contains(msg, "the deploys of mail could not be listed, so nothing was removed") {
		t.Errorf("deploys that cannot be listed = %q", msg)
	}
}

// A stack Dockhand only found and keeps a record of, as it does once the stack is given an icon, is adopted by saving its file where it lies:
// Dockhand's own adoption would make a second stack of it. Removing one takes what removing any stack it only found takes.
func TestStackAdoptAndDeleteOfAStackDockhandOnlyRecords(t *testing.T) {
	t.Parallel()

	const (
		adopt   = "POST /api/stacks/adopt"
		save    = "PUT /api/stacks/web/compose"
		read    = "GET /api/system/files/content"
		file    = "/srv/found/web/docker-compose.yml"
		record  = `"web":{"sourceType":"external","composePath":null,"repository":null,"secretProviderId":null,"icon":"box","envVarCount":0}`
		preview = `{"stackName":"web","sourceType":"external","stackDir":null,"gitDir":null,"namedVolumes":[],"canDeleteFiles":false}`
	)
	dock := withStackFiles(t)
	dock.AnswerWhen("GET /api/stacks/sources", "env", "1", `{`+record+`,`+strings.TrimPrefix(alphaSources, `{`))
	dock.Answer(read, `{"path":"/srv/found/web/docker-compose.yml","content":"services: {}\n","size":13,"mtime":"2026-10-10T10:00:00.000Z"}`)
	dock.Answer(save, `{"success":true}`)
	dock.Then(save, func(dockhandtest.Request) {
		dock.Answer("GET /api/stacks/web/compose", `{"content":"services: {}\n","stackDir":"/srv/found/web","composePath":"/srv/found/web/docker-compose.yml","envPath":null,"suggestedEnvPath":"/srv/found/web/.env"}`)
	})
	suite := session(t, dock, quick)
	args := map[string]any{"env": "alpha", "name": "web", "compose_path": file}

	// how it is kept is said as Dockhand says it
	if got := suite.Call(t, "stack_get", map[string]any{"env": "alpha", "stack": "web"}); got["kept"] != "external" || !acctest.IsBool(got["deployable"], false) {
		t.Errorf("a stack Dockhand only records = %v", got)
	}

	// the file is read where it lies and saved back there as the stack's, and Dockhand's own adoption is never asked
	adopted := suite.Call(t, "stack_adopt", args)
	saved := dock.Last(save)
	if saved.Body != `{"composePath":"/srv/found/web/docker-compose.yml","content":"services: {}\n"}` || saved.Query.Get("env") != "1" || dock.Last(read).Query.Get("path") != file || len(dock.Requests(adopt)) != 0 || adopted["compose_path"] != file || adopted["status"] != "running" || len(acctest.Strs(t, adopted["changes"], "changes")) != 1 {
		t.Errorf("a recorded stack adopted = %v, saved %s", adopted, saved.Body)
	}

	dock.Answer(save, `{"success":false}`)
	if msg := suite.CallErr(t, "stack_adopt", args); !strings.Contains(msg, "web was not adopted: the answer did not say that it was") {
		t.Errorf("a file saved without its being said = %q", msg)
	}
	dock.Fail(save, http.StatusInternalServerError, "Failed to save compose file")
	if msg := suite.CallErr(t, "stack_adopt", args); !strings.Contains(msg, "web was not adopted") || !strings.Contains(msg, "Failed to save compose file") {
		t.Errorf("a file Dockhand refuses to save = %q", msg)
	}
	dock.Fail(read, http.StatusNotFound, "File not found")
	if msg := suite.CallErr(t, "stack_adopt", args); !strings.Contains(msg, "web was not adopted: the compose file at "+file+" could not be read") || len(dock.Requests(adopt)) != 0 {
		t.Errorf("a file that is not there = %q", msg)
	}

	// removed, it is what Dockhand only found: its containers go and nothing else
	dock.Answer("GET /api/stacks/web/delete-preview", preview)
	if would := suite.Call(t, "stack_delete", map[string]any{"env": "alpha", "stack": "web"}); would["kept"] != "external" || !strings.Contains(acctest.Str(would["note"]), "Dockhand only found this stack on the host: its containers are removed and nothing else") {
		t.Errorf("what removing a recorded stack would take = %v", would)
	}
	if msg := suite.CallErr(t, "stack_delete", map[string]any{"env": "alpha", "stack": "web", "volumes": true, "confirm": true}); !strings.Contains(msg, "Dockhand removes only the containers of web") {
		t.Errorf("the volumes of a recorded stack asked for = %q", msg)
	}
}
