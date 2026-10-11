package tools

import (
	"net/http"
	"strings"
	"testing"

	"github.com/katbyte/dockhand-mcp/sdk/dockhandtest"
	"github.com/katbyte/go-kt/mcp/acctest"
)

// What a 1.0.51 server keeps for the stacks of stack_test.go: mail is one it made and keeps the files of, with a password written into its compose
// file, two variables in its .env file and one kept as a secret; archive is one it deploys from a repository; and web is one it only found running,
// which it has no record of at all.
const (
	alphaSources = `{"mail":{"sourceType":"internal","composePath":"/app/data/stacks/alpha/mail/compose.yaml","repository":null,"secretProviderId":null,"icon":null,"envVarCount":3},"archive":{"sourceType":"git","composePath":"/app/data/git/archive/compose.yaml","repository":null,"secretProviderId":null,"icon":null,"envVarCount":0}}`

	mailFile    = "services:\n  smtp:\n    image: registry.example/smtp:${TAG}\n    environment:\n      RELAY_PASSWORD: hunter2\n      TZ: ${TZ}\n  spool:\n    image: registry.example/spool:3\n"
	mailCompose = `{"content":"services:\n  smtp:\n    image: registry.example/smtp:${TAG}\n    environment:\n      RELAY_PASSWORD: hunter2\n      TZ: ${TZ}\n  spool:\n    image: registry.example/spool:3\n","stackDir":"/app/data/stacks/alpha/mail","composePath":"/app/data/stacks/alpha/mail/compose.yaml","envPath":"/app/data/stacks/alpha/mail/.env","suggestedEnvPath":"/app/data/stacks/alpha/mail/.env"}`
	mailEnv     = `{"variables":[{"key":"TZ","value":"UTC","isSecret":false},{"key":"API_TOKEN","value":"abc123def","isSecret":false},{"key":"DB_PASSWORD","value":"***","isSecret":true}],"injectedSecretKeys":[],"secretProvider":null}`

	// the runs as a server lists them are not relied on to be in order: the failed one is in the middle here
	mailDeploys = `{"runs":[
		{"id":2,"environmentId":1,"triggeredBy":"manual","triggeredAt":"2026-10-08T10:03:18.457Z","startedAt":"2026-10-08T10:03:18.457Z","completedAt":"2026-10-08T10:03:22.920Z","duration":4463,"status":"success","errorMessage":null,"details":{"options":{"pull":false,"build":false,"forceRecreate":false},"summary":{"containersCreated":2,"containersRecreated":0,"containersStarted":2,"containerNames":["mail-smtp-1","mail-spool-1"],"imagesBuilt":[],"imagesPulled":[],"buildSteps":0,"buildStepsCached":0},"exitCode":0,"composeHash":"93541c6f","envHash":"e3b0c442","logFile":"2.log"}},
		{"id":4,"environmentId":1,"triggeredBy":"webhook","triggeredAt":"2026-10-09T08:22:58.097Z","startedAt":"2026-10-09T08:22:58.099Z","completedAt":"2026-10-09T08:22:59.022Z","duration":923,"status":"failed","errorMessage":" Image registry.example/spool:9 Pulling \n Image registry.example/spool:9 Error failed to resolve reference\nError response from daemon: failed to resolve reference \"registry.example/spool:9\": not found\n","details":{"options":{"pull":true,"build":true,"forceRecreate":true},"summary":{"containersCreated":0,"containersRecreated":0,"containersStarted":0,"containerNames":[],"imagesBuilt":[],"imagesPulled":[],"buildSteps":0,"buildStepsCached":0},"exitCode":1,"composeHash":"aa","envHash":"bb","logFile":"4.log"}},
		{"id":3,"environmentId":1,"triggeredBy":"manual","triggeredAt":"2026-10-08T12:13:37.805Z","startedAt":"2026-10-08T12:13:37.806Z","completedAt":"2026-10-08T12:13:39.324Z","duration":1518,"status":"success","errorMessage":null,"details":null}
	]}`

	webHints = `{"stackName":"web","workingDir":"/srv/compose/web","configFiles":["/srv/compose/web/compose.yaml","/srv/compose/web/compose.override.yaml"]}`
)

// withStackFiles is the two canned hosts with what Dockhand keeps for each of alpha's stacks as well.
func withStackFiles(t *testing.T) *dockhandtest.Server {
	t.Helper()

	dock := twoHosts(t)
	dock.AnswerWhen("GET /api/stacks/sources", "env", "1", alphaSources)
	dock.Answer("GET /api/stacks/mail/compose", mailCompose)
	dock.Answer("GET /api/stacks/mail/env", mailEnv)
	dock.Answer("GET /api/stacks/mail/deploys", mailDeploys)
	dock.Fail("GET /api/stacks/web/compose", http.StatusNotFound, "Stack compose file location not configured")
	dock.Answer("GET /api/stacks/web/deploys", `{"runs":[]}`)
	dock.Answer("GET /api/stacks/path-hints", webHints)
	dock.Answer("GET /api/stacks/default-path", `{"stackDir":"/app/data/stacks/alpha/blog","composePath":"/app/data/stacks/alpha/blog/compose.yaml","envPath":"/app/data/stacks/alpha/blog/.env","source":"default"}`)

	return dock
}

func TestStackGet(t *testing.T) {
	t.Parallel()

	dock := withStackFiles(t)
	suite := session(t, dock, Options{})

	// a stack Dockhand keeps: the file, where it is, the variables and the last deploy, with what is secret hidden
	out := suite.Call(t, "stack_get", map[string]any{"stack": "mail"})
	if out["env"] != "alpha" || out["stack"] != "mail" || out["status"] != "partial" || len(acctest.Rows(t, out["services"], "services")) != 2 || acctest.Num(t, out["problems"], "problems") != 1 {
		t.Errorf("the stack as stack_list gives it = %v", out)
	}
	if out["kept"] != "internal" || !acctest.BoolOf(out["deployable"]) || out["compose_path"] != "/app/data/stacks/alpha/mail/compose.yaml" || out["env_path"] != "/app/data/stacks/alpha/mail/.env" || out["working_dir"] != nil || out["config_files"] != nil {
		t.Errorf("how it is kept = %v", out)
	}
	if got, want := acctest.Str(out["compose"]), strings.Replace(mailFile, "hunter2", "***", 1); got != want {
		t.Errorf("the compose file with its password hidden = %q, want %q", got, want)
	}
	vars := acctest.Rows(t, out["variables"], "variables")
	if len(vars) != 3 || vars[0]["name"] != "API_TOKEN" || vars[0]["value"] != "***" || vars[0]["secret"] != nil || vars[1]["name"] != "DB_PASSWORD" || vars[1]["value"] != "***" || !acctest.BoolOf(vars[1]["secret"]) || vars[2]["name"] != "TZ" || vars[2]["value"] != "UTC" {
		t.Errorf("the variables, by name, the secret ones hidden = %v", vars)
	}
	// the password in the file and the token among the variables: the one Dockhand keeps as a secret was never sent, so it is not one this tool hid
	if acctest.Num(t, out["hidden"], "hidden") != 2 || !strings.Contains(acctest.Str(out["note"]), "2 secrets are hidden") {
		t.Errorf("what is said of what was hidden = %v, %v", out["hidden"], out["note"])
	}
	// the last deploy is the one with the highest number, wherever the server lists it
	last := acctest.Object(t, out["last_deploy"], "last_deploy")
	if acctest.Num(t, last["run"], "run") != 4 || last["status"] != "failed" || last["by"] != "webhook" || last["when"] != "2026-10-09T08:22:58Z" || last["error"] != `Error response from daemon: failed to resolve reference "registry.example/spool:9": not found` {
		t.Errorf("the last deploy = %v", last)
	}
	for _, call := range []string{"GET /api/stacks/sources", "GET /api/stacks/mail/compose", "GET /api/stacks/mail/env", "GET /api/stacks/mail/deploys"} {
		if got := dock.Last(call).Query.Get("env"); got != "1" {
			t.Errorf("%s was asked on environment %q", call, got)
		}
	}

	// asked to, it shows what it hid; what Dockhand keeps as a secret it cannot
	out = suite.Call(t, "stack_get", map[string]any{"stack": "mail", "reveal_secrets": true})
	vars = acctest.Rows(t, out["variables"], "variables")
	if out["compose"] != mailFile || vars[0]["value"] != "abc123def" || vars[1]["value"] != "***" || out["hidden"] != nil || out["note"] != nil {
		t.Errorf("with reveal_secrets = %v", out)
	}

	// a stack Dockhand only found: no file, no variables asked for, and where its containers say it was brought up from
	out = suite.Call(t, "stack_get", map[string]any{"stack": "web", "env": "alpha"})
	if out["kept"] != "found" || acctest.BoolOf(out["deployable"]) || out["compose"] != nil || out["variables"] != nil || out["last_deploy"] != nil || out["working_dir"] != "/srv/compose/web" || strings.Join(acctest.Strs(t, out["config_files"], "config_files"), " ") != "/srv/compose/web/compose.yaml /srv/compose/web/compose.override.yaml" {
		t.Errorf("a stack Dockhand only found = %v", out)
	}
	if !strings.Contains(acctest.Str(out["note"]), "Dockhand has no compose file for this stack") || len(dock.Requests("GET /api/stacks/web/env")) != 0 {
		t.Errorf("what is said of it = %v", out["note"])
	}
	if hints := dock.Last("GET /api/stacks/path-hints"); hints.Query.Get("name") != "web" || hints.Query.Get("env") != "1" {
		t.Errorf("its path hints were asked with %v", hints.Query)
	}

	if msg := suite.CallErr(t, "stack_get", map[string]any{"stack": "nope"}); !strings.Contains(msg, `no stack is named "nope"`) {
		t.Errorf("a stack that is not there = %s", msg)
	}
	if msg := suite.CallErr(t, "stack_get", map[string]any{"stack": " "}); !strings.Contains(msg, "stack is required") {
		t.Errorf("no stack named = %s", msg)
	}
}

// A stack Dockhand deploys from a repository says so, and a stack is still described when part of what Dockhand keeps for it cannot be read: each
// part that failed is said, and the rest is given.
func TestStackGetInPart(t *testing.T) {
	t.Parallel()

	dock := withStackFiles(t)
	dock.Answer("GET /api/stacks/archive/compose", `{"content":"services:\n  job:\n    image: registry.example/job:1\n","stackDir":"/app/data/git/archive","composePath":"/app/data/git/archive/compose.yaml","envPath":null,"suggestedEnvPath":"/app/data/git/archive/.env"}`)
	dock.Answer("GET /api/stacks/archive/env", `{"variables":[{"key":"MIRROR","value":"https://reader:s3cret@mirror.example/repo","isSecret":false}],"injectedSecretKeys":[],"secretProvider":null}`)
	dock.Answer("GET /api/stacks/archive/deploys", `{"runs":[]}`)

	out := session(t, dock, Options{}).Call(t, "stack_get", map[string]any{"stack": "archive"})
	vars := acctest.Rows(t, out["variables"], "variables")
	if out["kept"] != "git" || !acctest.BoolOf(out["deployable"]) || out["env_path"] != nil || out["last_deploy"] != nil || len(vars) != 1 || vars[0]["value"] != "https://reader:***@mirror.example/repo" || acctest.Num(t, out["hidden"], "hidden") != 1 { //nolint:gosec // G101: the address is the fixture's, with its password already hidden
		t.Errorf("a stack deployed from a repository = %v", out)
	}

	dock.FailWhen("GET /api/stacks/sources", "env", "1", http.StatusInternalServerError, "Failed to get stack sources")
	dock.Fail("GET /api/stacks/mail/env", http.StatusForbidden, "Permission denied")
	dock.Fail("GET /api/stacks/mail/deploys", http.StatusInternalServerError, "Failed to list deploys")
	out = session(t, dock, Options{}).Call(t, "stack_get", map[string]any{"stack": "mail"})
	note := acctest.Str(out["note"])
	if out["kept"] != "found" || !acctest.BoolOf(out["deployable"]) || out["variables"] != nil || out["last_deploy"] != nil || !strings.Contains(acctest.Str(out["compose"]), "RELAY_PASSWORD: ***") {
		t.Errorf("a stack read in part = %v", out)
	}
	for _, want := range []string{"how Dockhand keeps it could not be read: ", "its variables could not be read: ", "Permission denied", "its deploys could not be read: ", "1 secret is hidden"} {
		if !strings.Contains(note, want) {
			t.Errorf("the note %q does not say %q", note, want)
		}
	}

	// a file that cannot be read is not the same as one Dockhand does not have
	dock.Fail("GET /api/stacks/mail/compose", http.StatusInternalServerError, "Failed to read compose file")
	out = session(t, dock, Options{}).Call(t, "stack_get", map[string]any{"stack": "mail"})
	if note := acctest.Str(out["note"]); !strings.Contains(note, "its compose file could not be read: ") || strings.Contains(note, "Dockhand has no compose file") || acctest.BoolOf(out["deployable"]) || out["working_dir"] != nil {
		t.Errorf("a compose file that could not be read = %v", out)
	}
}

func TestStackValidate(t *testing.T) {
	t.Parallel()

	const (
		findings = `{"findings":[
			{"ruleId":"MISSING_HEALTHCHECK","severity":"info","service":"spool","line":7,"message":"\"spool\" has no healthcheck","hint":"Add a healthcheck."},
			{"ruleId":"SECRET_IN_ENVIRONMENT","severity":"warn","service":"smtp","line":5,"message":"\"smtp\" hard-codes a secret value for RELAY_PASSWORD in environment:","hint":"Store it as a Dockhand secret."},
			{"ruleId":"CONTAINER_NAME_COLLISION","severity":"error","service":"smtp","line":4,"message":"container_name \"front\" is already used by a container on this environment"},
			{"ruleId":"LATEST_TAG","severity":"warn","service":"smtp","line":3,"message":"\"smtp\" uses an untagged image","hint":"Pin a version tag."},
			{"ruleId":"SOMETHING_NEW","severity":"notice","message":"a kind of finding this server did not have"}
		],"counts":{"error":1,"warn":2,"info":2}}`
		variables = `{"valid":false,"required":["TAG","TZ"],"optional":[],"defined":["TZ","API_TOKEN","DB_PASSWORD"],"missing":["TAG"],"unused":["API_TOKEN","DB_PASSWORD"]}`
		loose     = "services:\n  a:\n    image: registry.example/a:${V:-1}\n"
	)

	dock := withStackFiles(t)
	dock.Answer("POST /api/stacks/mail/validate", findings)
	dock.Answer("POST /api/stacks/mail/env/validate", variables)
	suite := session(t, dock, Options{})

	// a stack's own file: read, checked as that stack's, and held against the variables the stack gives a value
	out := suite.Call(t, "stack_validate", map[string]any{"stack": "mail"})
	if out["env"] != "alpha" || out["stack"] != "mail" || acctest.BoolOf(out["usable"]) || acctest.Num(t, out["errors"], "errors") != 1 || acctest.Num(t, out["warnings"], "warnings") != 2 || acctest.Num(t, out["info"], "info") != 2 {
		t.Errorf("the counts = %v", out)
	}
	rows := acctest.Rows(t, out["findings"], "findings")
	order := make([]string, 0, len(rows))
	for _, row := range rows {
		order = append(order, acctest.Str(row["rule"]))
	}
	// the most serious first, then by line; a severity this tool does not know is with what is only worth knowing
	if got := strings.Join(order, " "); got != "CONTAINER_NAME_COLLISION LATEST_TAG SECRET_IN_ENVIRONMENT SOMETHING_NEW MISSING_HEALTHCHECK" {
		t.Errorf("the findings, in order = %s", got)
	}
	if rows[1]["severity"] != "warn" || rows[1]["service"] != "smtp" || acctest.Num(t, rows[1]["line"], "line") != 3 || rows[1]["hint"] != "Pin a version tag." || rows[0]["hint"] != nil || rows[3]["line"] != nil {
		t.Errorf("a finding = %v, %v", rows[1], rows[3])
	}
	vars := acctest.Object(t, out["variables"], "variables")
	if strings.Join(acctest.Strs(t, vars["required"], "required"), " ") != "TAG TZ" || strings.Join(acctest.Strs(t, vars["missing"], "missing"), " ") != "TAG" || strings.Join(acctest.Strs(t, vars["unused"], "unused"), " ") != "API_TOKEN DB_PASSWORD" || len(acctest.Strs(t, vars["optional"], "optional")) != 0 {
		t.Errorf("the variables = %v", vars)
	}
	if note := acctest.Str(out["note"]); !strings.Contains(note, "1 of the variables the file needs are given no value by the stack") || !strings.HasSuffix(note, ": TAG") {
		t.Errorf("the note = %q", note)
	}
	check, held := dock.Last("POST /api/stacks/mail/validate"), dock.Last("POST /api/stacks/mail/env/validate")
	if body := sent(t, check); check.Query.Get("env") != "1" || body["compose"] != mailFile || !acctest.BoolOf(body["existing"]) {
		t.Errorf("the check was sent %v with %v", body, check.Query)
	}
	// Dockhand's own check knows only the variables it keeps itself, so it is told the ones in the stack's .env file as well
	if body := sent(t, held); held.Query.Get("env") != "1" || body["compose"] != mailFile || strings.Join(acctest.Strs(t, body["variables"], "variables"), " ") != "TZ API_TOKEN DB_PASSWORD" {
		t.Errorf("the variables were checked with %v", body)
	}

	// a change to a stack's file before it is saved: the text given is what is checked, and the file Dockhand keeps is not read
	before := len(dock.Requests("GET /api/stacks/mail/compose"))
	suite.Call(t, "stack_validate", map[string]any{"stack": "mail", "env": 1, "compose": loose})
	if body := sent(t, dock.Last("POST /api/stacks/mail/validate")); body["compose"] != loose || !acctest.BoolOf(body["existing"]) || len(dock.Requests("GET /api/stacks/mail/compose")) != before {
		t.Errorf("a change to a stack's file was checked with %v", body)
	}

	// text that is no stack's: checked under a name that is nobody's, against no host, and with no variable given a value
	dock.Answer("POST /api/stacks/unsaved/validate", `{"findings":[],"counts":{"error":0,"warn":0,"info":0}}`)
	dock.Answer("POST /api/stacks/unsaved/env/validate", `{"valid":true,"required":[],"optional":["V"],"defined":[],"missing":[],"unused":[]}`)
	out = suite.Call(t, "stack_validate", map[string]any{"compose": loose})
	vars = acctest.Object(t, out["variables"], "variables")
	if out["env"] != nil || out["stack"] != nil || !acctest.BoolOf(out["usable"]) || len(acctest.Rows(t, out["findings"], "findings")) != 0 || out["note"] != nil || strings.Join(acctest.Strs(t, vars["optional"], "optional"), " ") != "V" {
		t.Errorf("text that is no stack's = %v", out)
	}
	check, held = dock.Last("POST /api/stacks/unsaved/validate"), dock.Last("POST /api/stacks/unsaved/env/validate")
	if body := sent(t, check); check.Query.Has("env") || body["compose"] != loose || body["existing"] != nil {
		t.Errorf("loose text was checked with %v and %v", body, check.Query)
	}
	if body := sent(t, held); held.Query.Has("env") || !strings.Contains(held.Body, `"variables":[]`) || body["compose"] != loose {
		t.Errorf("loose text's variables were checked with %s", held.Body)
	}

	// with a host named, the names in the text are held against what is on it
	out = suite.Call(t, "stack_validate", map[string]any{"compose": loose, "env": "beta"})
	if got := dock.Last("POST /api/stacks/unsaved/validate").Query.Get("env"); out["env"] != "beta" || got != "2" {
		t.Errorf("loose text against a host = %v, asked on environment %q", out["env"], got)
	}
}

func TestStackValidateRefusals(t *testing.T) {
	t.Parallel()

	dock := withStackFiles(t)
	suite := session(t, dock, Options{})

	for _, tc := range []struct {
		args map[string]any
		want string
	}{
		{map[string]any{}, "stack or compose is required"},
		{map[string]any{"compose": " \n"}, "stack or compose is required"},
		{map[string]any{"stack": "nope"}, `no stack is named "nope"`},
		{map[string]any{"compose": "services: {}", "env": "gamma"}, "gamma"},
		// Dockhand has no file for a stack it only found, and says to give one
		{map[string]any{"stack": "web"}, "Dockhand has no compose file for web"},
	} {
		if msg := suite.CallErr(t, "stack_validate", tc.args); !strings.Contains(msg, tc.want) {
			t.Errorf("stack_validate %v = %s, want %s", tc.args, msg, tc.want)
		}
	}

	dock.Fail("POST /api/stacks/mail/validate", http.StatusBadRequest, "compose content is required")
	if msg := suite.CallErr(t, "stack_validate", map[string]any{"stack": "mail"}); !strings.Contains(msg, "the compose file could not be checked") || !strings.Contains(msg, "compose content is required") {
		t.Errorf("a check Dockhand refused = %s", msg)
	}

	// the variables failing to be read or listed is said, and what the checks found is still given
	dock.Answer("POST /api/stacks/mail/validate", `{"findings":[],"counts":{"error":0,"warn":0,"info":0}}`)
	dock.Fail("GET /api/stacks/mail/env", http.StatusInternalServerError, "Failed to read variables")
	dock.Fail("POST /api/stacks/mail/env/validate", http.StatusInternalServerError, "Failed to validate")
	out := suite.Call(t, "stack_validate", map[string]any{"stack": "mail"})
	vars := acctest.Object(t, out["variables"], "variables")
	if note := acctest.Str(out["note"]); !acctest.BoolOf(out["usable"]) || !strings.Contains(note, "the stack's variables could not be read") || !strings.Contains(note, "the variables the file uses could not be listed") || len(acctest.Strs(t, vars["missing"], "missing")) != 0 {
		t.Errorf("a check whose variables failed = %v", out)
	}

	dock.Fail("GET /api/stacks/mail/compose", http.StatusInternalServerError, "Failed to read compose file")
	if msg := suite.CallErr(t, "stack_validate", map[string]any{"stack": "mail"}); !strings.Contains(msg, "the compose file of mail could not be read") {
		t.Errorf("a file that could not be read = %s", msg)
	}
}

func TestStackDeploys(t *testing.T) {
	t.Parallel()

	dock := withStackFiles(t)
	suite := session(t, dock, Options{})

	out := suite.Call(t, "stack_deploys", map[string]any{"stack": "mail"})
	rows := acctest.Rows(t, out["deploys"], "deploys")
	if out["env"] != "alpha" || out["stack"] != "mail" || acctest.Num(t, out["total"], "total") != 3 || len(rows) != 3 || out["note"] != nil || out["log"] != nil {
		t.Fatalf("a stack's deploys = %v", out)
	}
	// the newest first, by number
	if acctest.Num(t, rows[0]["run"], "run") != 4 || acctest.Num(t, rows[1]["run"], "run") != 3 || acctest.Num(t, rows[2]["run"], "run") != 2 {
		t.Errorf("the order = %v, %v, %v", rows[0]["run"], rows[1]["run"], rows[2]["run"])
	}
	failed, bare, first := rows[0], rows[1], rows[2]
	if failed["status"] != "failed" || !acctest.BoolOf(failed["pulled"]) || !acctest.BoolOf(failed["built"]) || !acctest.BoolOf(failed["recreated"]) || acctest.Decimal(t, failed["seconds"], "seconds") != 0.923 || failed["containers"] != nil || !strings.HasPrefix(acctest.Str(failed["error"]), "Error response from daemon: ") {
		t.Errorf("the deploy that failed = %v", failed)
	}
	if first["status"] != "success" || first["error"] != nil || first["pulled"] != nil || first["by"] != "manual" || strings.Join(acctest.Strs(t, first["containers"], "containers"), " ") != "mail-smtp-1 mail-spool-1" || first["when"] != "2026-10-08T10:03:18Z" {
		t.Errorf("the first deploy = %v", first)
	}
	// a run recorded with no details is still a run
	if bare["status"] != "success" || bare["pulled"] != nil || bare["containers"] != nil {
		t.Errorf("a deploy with no details = %v", bare)
	}

	out = suite.Call(t, "stack_deploys", map[string]any{"stack": "mail", "limit": 1})
	if one := acctest.Rows(t, out["deploys"], "deploys"); len(one) != 1 || acctest.Num(t, out["total"], "total") != 3 || !strings.Contains(acctest.Str(out["note"]), "the newest 1 of 3 are listed") {
		t.Errorf("one deploy asked for = %v", out)
	}

	// one run, with what compose printed during it: the end of it, a line each, with what is secret hidden
	dock.AnswerText("GET /api/stacks/mail/deploys/4/log", "text/plain", " Image registry.example/spool:9 Pulling \r\n login with REGISTRY_TOKEN=abcdef123456 \n Image registry.example/spool:9 Error failed to resolve reference\nError response from daemon: not found\n\n\n")
	out = suite.Call(t, "stack_deploys", map[string]any{"stack": "mail", "run": 4, "tail": 3})
	rows = acctest.Rows(t, out["deploys"], "deploys")
	lines := acctest.Strs(t, out["log"], "log")
	if len(rows) != 1 || acctest.Num(t, rows[0]["run"], "run") != 4 || acctest.Num(t, out["lines"], "lines") != 4 || acctest.Num(t, out["hidden"], "hidden") != 1 {
		t.Fatalf("one deploy with its log = %v", out)
	}
	if strings.Join(lines, "|") != " login with REGISTRY_TOKEN=***| Image registry.example/spool:9 Error failed to resolve reference|Error response from daemon: not found" {
		t.Errorf("the lines = %q", lines)
	}
	if note := acctest.Str(out["note"]); !strings.Contains(note, "the last 3 of 4 lines are given") || !strings.Contains(note, "1 secret is hidden") {
		t.Errorf("the note = %q", note)
	}

	out = suite.Call(t, "stack_deploys", map[string]any{"stack": "mail", "run": 4, "reveal_secrets": true})
	if lines := acctest.Strs(t, out["log"], "log"); len(lines) != 4 || lines[0] != " Image registry.example/spool:9 Pulling" || lines[1] != " login with REGISTRY_TOKEN=abcdef123456" || out["hidden"] != nil || out["note"] != nil {
		t.Errorf("the whole log, shown = %v", out)
	}

	// a deploy that printed nothing
	dock.AnswerText("GET /api/stacks/mail/deploys/3/log", "text/plain", "")
	out = suite.Call(t, "stack_deploys", map[string]any{"stack": "mail", "run": 3})
	if out["log"] != nil || out["lines"] != nil || len(acctest.Rows(t, out["deploys"], "deploys")) != 1 {
		t.Errorf("a deploy that printed nothing = %v", out)
	}

	// the run is still described when its log is gone
	dock.Fail("GET /api/stacks/mail/deploys/2/log", http.StatusNotFound, "Deploy log not found")
	out = suite.Call(t, "stack_deploys", map[string]any{"stack": "mail", "run": 2})
	if rows := acctest.Rows(t, out["deploys"], "deploys"); len(rows) != 1 || out["log"] != nil || !strings.Contains(acctest.Str(out["note"]), "what it printed could not be read") {
		t.Errorf("a deploy whose log is gone = %v", out)
	}

	// a stack Dockhand never deployed
	out = suite.Call(t, "stack_deploys", map[string]any{"stack": "web"})
	if len(acctest.Rows(t, out["deploys"], "deploys")) != 0 || acctest.Num(t, out["total"], "total") != 0 || !strings.Contains(acctest.Str(out["note"]), "Dockhand has never deployed this stack") {
		t.Errorf("a stack never deployed = %v", out)
	}

	for _, tc := range []struct {
		args map[string]any
		want string
	}{
		{map[string]any{"stack": "mail", "run": 9}, "mail has no deploy numbered 9"},
		{map[string]any{"stack": "mail", "limit": -1}, "limit is -1"},
		{map[string]any{"stack": "mail", "tail": -2}, "tail -2"},
		{map[string]any{"stack": "nope"}, `no stack is named "nope"`},
	} {
		if msg := suite.CallErr(t, "stack_deploys", tc.args); !strings.Contains(msg, tc.want) {
			t.Errorf("stack_deploys %v = %s, want %s", tc.args, msg, tc.want)
		}
	}

	dock.Fail("GET /api/stacks/mail/deploys", http.StatusInternalServerError, "Failed to list deploys")
	if msg := suite.CallErr(t, "stack_deploys", map[string]any{"stack": "mail"}); !strings.Contains(msg, "the deploys of mail could not be listed") {
		t.Errorf("deploys that cannot be listed = %s", msg)
	}
}
