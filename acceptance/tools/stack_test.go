//go:build acceptance

package tools_test

import (
	"context"
	"maps"
	"slices"
	"strings"
	"testing"

	"github.com/katbyte/dockhand-mcp/lib/testenv"
	"github.com/katbyte/dockhand-mcp/sdk/dockhand"
	"github.com/katbyte/go-kt/mcp/acctest"
)

// laidUnder is where the tests put compose files inside the Dockhand's own container, as a folder mounted into it would hold them.
const laidUnder = "/srv/acc-stacks"

// containerOn reports whether a host's own Docker has a container by a name, running or not.
func containerOn(t *testing.T, host, name string) bool {
	t.Helper()

	_, err := world.Docker(context.WithoutCancel(t.Context()), host, "inspect", "--type", "container", name)

	return err == nil
}

// givenTo is what a host's own Docker says a container was given to run with, each as NAME=value.
func givenTo(t *testing.T, host, container string) []string {
	t.Helper()

	out, err := world.Docker(context.WithoutCancel(t.Context()), host, "inspect", "--format", "{{range .Config.Env}}{{println .}}{{end}}", container)
	if err != nil {
		t.Fatalf("asking %s what %s was given: %v", host, container, err)
	}

	return strings.Fields(out)
}

// startedAt is when a host's own Docker says a container last started: it changes when the container is made again.
func startedAt(t *testing.T, host, container string) string {
	t.Helper()

	out, err := world.Docker(context.WithoutCancel(t.Context()), host, "inspect", "--format", "{{.Id}} {{.State.StartedAt}}", container)
	if err != nil {
		t.Fatalf("asking %s when %s started: %v", host, container, err)
	}

	return strings.TrimSpace(out)
}

// forgotten has a stack a test makes taken away when the test ends, however it ends, and before it starts in case a run cut short left it: what
// Dockhand keeps for it, and its containers, network and volumes on the host.
func forgotten(t *testing.T, host, stack string, volumes ...string) {
	t.Helper()

	clean := func(ctx context.Context) {
		env := world.Env(host)
		// removing a stack Dockhand does not have is no failure of this
		_, _ = world.Dock.DeleteStacksByName(ctx, stack, dockhand.DeleteStacksByNameOperationOptions{Env: &env, Force: new(true), Volumes: new(true)})
		if ids, err := world.Docker(ctx, host, "ps", "-aq", "--filter", "label=com.docker.compose.project="+stack); err == nil && strings.TrimSpace(ids) != "" {
			_, _ = world.Docker(ctx, host, append([]string{"rm", "-f", "-v"}, strings.Fields(ids)...)...)
		}
		_, _ = world.Docker(ctx, host, "network", "rm", "-f", stack+"_default")
		for _, volume := range volumes {
			_, _ = world.Docker(ctx, host, "volume", "rm", "-f", volume)
		}
	}
	ctx := context.WithoutCancel(t.Context())
	clean(ctx)
	t.Cleanup(func() { clean(ctx) })
}

// stackNamed is one stack as stack_list gives it, or nil when it lists none by the name.
func stackNamed(t *testing.T, name string) map[string]any {
	t.Helper()

	return named(acctest.Rows(t, call(t, "stack_list", map[string]any{"name": name})["stacks"], "stacks"), "stack")[name]
}

// rulesOf is the checks that found something, in the order the findings came.
func rulesOf(t *testing.T, out map[string]any) []string {
	t.Helper()

	findings := acctest.Rows(t, out["findings"], "findings")
	rules := make([]string, 0, len(findings))
	for _, finding := range findings {
		rules = append(rules, acctest.Str(finding["rule"]))
	}

	return rules
}

func TestStackGet(t *testing.T) {
	// the stack someone brought up at the host: Dockhand has no file for it, and says where the host says it came from
	out := call(t, "stack_get", map[string]any{"stack": webStack})
	if out["env"] != testenv.Alpha || out["stack"] != webStack || out["kept"] != "found" || acctest.BoolOf(out["deployable"]) || out["compose"] != nil || out["variables"] != nil || out["last_deploy"] != nil {
		t.Errorf("%s = %v", webStack, out)
	}
	if out["working_dir"] != "/stacks/"+webStack || strings.Join(acctest.Strs(t, out["config_files"], "config_files"), " ") != "/stacks/"+webStack+"/compose.yaml" || !strings.Contains(acctest.Str(out["note"]), "Dockhand has no compose file for this stack") {
		t.Errorf("where %s came from = %v, %v, %v", webStack, out["working_dir"], out["config_files"], out["note"])
	}
	if services := named(acctest.Rows(t, out["services"], "services"), "service"); len(services) != 2 || services["front"]["container"] != webFront || services["back"]["container"] != webBack {
		t.Errorf("its services = %v", out["services"])
	}

	if msg := refused(t, "stack_get", map[string]any{"stack": "nothing-by-this-name"}); !strings.Contains(msg, "nothing-by-this-name") {
		t.Errorf("a stack that is not there = %q", msg)
	}
	if msg := refused(t, "stack_get", map[string]any{"stack": webStack, "env": testenv.Beta}); !strings.Contains(msg, webStack) {
		t.Errorf("a stack asked for on the host it is not on = %q", msg)
	}
}

func TestStackValidate(t *testing.T) {
	if world == nil {
		t.Skip(testenv.NotConfigured)
	}

	// text that is no stack's: what is risky in it is found with its line, and the variables it uses are listed
	risky := "services:\n  app:\n    image: " + strings.Split(world.Image, ":")[0] + "\n    privileged: true\n    environment:\n      DB_PASSWORD: not-a-real-password\n      MODE: ${MODE}\n      LEVEL: ${LEVEL:-info}\n"
	out := call(t, "stack_validate", map[string]any{"compose": risky})
	rules := rulesOf(t, out)
	for _, want := range []string{"LATEST_TAG", "PRIVILEGED_CONTAINER", "SECRET_IN_ENVIRONMENT"} {
		if !slices.Contains(rules, want) {
			t.Errorf("the checks found %v, and not %s", rules, want)
		}
	}
	if !acctest.BoolOf(out["usable"]) || acctest.Num(t, out["errors"], "errors") != 0 || acctest.Num(t, out["warnings"], "warnings") < 3 || out["env"] != nil || out["stack"] != nil {
		t.Errorf("a risky file = %v", out)
	}
	for _, finding := range acctest.Rows(t, out["findings"], "findings") {
		if finding["rule"] == "PRIVILEGED_CONTAINER" && (finding["service"] != "app" || acctest.Num(t, finding["line"], "line") != 4 || acctest.Str(finding["hint"]) == "" || finding["severity"] != "warn") {
			t.Errorf("the finding about privileged = %v", finding)
		}
	}
	vars := acctest.Object(t, out["variables"], "variables")
	if strings.Join(acctest.Strs(t, vars["required"], "required"), " ") != "MODE" || strings.Join(acctest.Strs(t, vars["optional"], "optional"), " ") != "LEVEL" || strings.Join(acctest.Strs(t, vars["missing"], "missing"), " ") != "MODE" {
		t.Errorf("the variables it uses = %v", vars)
	}

	// a file compose would not take is said not to be usable, the errors first
	broken := call(t, "stack_validate", map[string]any{"compose": "services:\n  app: [\n"})
	if acctest.BoolOf(broken["usable"]) || acctest.Num(t, broken["errors"], "errors") == 0 || acctest.Rows(t, broken["findings"], "findings")[0]["severity"] != "error" {
		t.Errorf("a broken file = %v", broken)
	}

	// held against a host, a container name another container has there is an error; as the stack whose container it is, it is none
	taking := "services:\n  front:\n    image: " + world.Image + "\n    container_name: " + webFront + "\n"
	clash := call(t, "stack_validate", map[string]any{"compose": taking, "env": testenv.Alpha})
	if clash["env"] != testenv.Alpha || acctest.BoolOf(clash["usable"]) || !slices.Contains(rulesOf(t, clash), "CONTAINER_NAME_COLLISION") {
		t.Errorf("a name that is taken on the host = %v", clash)
	}
	if elsewhere := call(t, "stack_validate", map[string]any{"compose": taking, "env": testenv.Beta}); !acctest.BoolOf(elsewhere["usable"]) {
		t.Errorf("the same name on a host that has no such container = %v", elsewhere)
	}
	own := call(t, "stack_validate", map[string]any{"compose": taking, "stack": webStack})
	if own["env"] != testenv.Alpha || own["stack"] != webStack || !acctest.BoolOf(own["usable"]) {
		t.Errorf("a stack's own container name = %v", own)
	}

	// a stack Dockhand has no file for has nothing to check until the text is given
	if msg := refused(t, "stack_validate", map[string]any{"stack": webStack}); !strings.Contains(msg, "Dockhand has no compose file for "+webStack) {
		t.Errorf("a stack with no file = %q", msg)
	}
	if msg := refused(t, "stack_validate", map[string]any{}); !strings.Contains(msg, "stack or compose is required") {
		t.Errorf("nothing to check = %q", msg)
	}
}

func TestStackDeploys(t *testing.T) {
	// a stack brought up at the host has no deploys of Dockhand's
	out := call(t, "stack_deploys", map[string]any{"stack": webStack})
	if out["env"] != testenv.Alpha || out["stack"] != webStack || acctest.Num(t, out["total"], "total") != 0 || len(acctest.Rows(t, out["deploys"], "deploys")) != 0 || !strings.Contains(acctest.Str(out["note"]), "Dockhand has never deployed this stack") {
		t.Errorf("the deploys of %s = %v", webStack, out)
	}
	if msg := refused(t, "stack_deploys", map[string]any{"stack": webStack, "run": 1}); !strings.Contains(msg, "has no deploy numbered 1") {
		t.Errorf("a deploy that is not there = %q", msg)
	}
	if msg := refused(t, "stack_deploys", map[string]any{"stack": "nothing-by-this-name"}); !strings.Contains(msg, "nothing-by-this-name") {
		t.Errorf("a stack that is not there = %q", msg)
	}
}

func TestStackScan(t *testing.T) {
	if world == nil {
		t.Skip(testenv.NotConfigured)
	}
	const dir = laidUnder + "/scan"
	ctx := context.WithoutCancel(t.Context())
	t.Cleanup(func() { _ = world.Unlay(ctx, dir) })

	file := "services:\n  one:\n    image: " + world.Image + "\n  two:\n    image: " + world.Image + "\n"
	for path, content := range map[string]string{
		dir + "/acc-scan-plain/compose.yaml":       file,
		dir + "/acc-scan-given/docker-compose.yml": file,
		dir + "/acc-scan-given/.env":               "MODE=demo\n",
		dir + "/acc-scan-nothing/readme.txt":       "no compose file here\n",
		dir + "/" + webStack + "/compose.yaml":     file,
		laidUnder + "/empty/placeholder/note.txt":  "nothing\n",
	} {
		if err := world.Lay(ctx, path, content); err != nil {
			t.Fatal(err)
		}
	}
	t.Cleanup(func() { _ = world.Unlay(ctx, laidUnder+"/empty") })

	out := call(t, "stack_scan", map[string]any{"path": dir})
	found := named(acctest.Rows(t, out["found"], "found"), "name")
	if out["path"] != dir || len(found) != 3 || out["errors"] != nil || found["acc-scan-nothing"] != nil {
		t.Fatalf("what was found under %s = %v", dir, out)
	}
	plain, given, web := found["acc-scan-plain"], found["acc-scan-given"], found[webStack]
	if plain["compose_path"] != dir+"/acc-scan-plain/compose.yaml" || plain["env_path"] != nil || acctest.Num(t, plain["services"], "services") != 2 || plain["running_on"] != nil || plain["adopted"] != nil {
		t.Errorf("a folder with a compose file = %v", plain)
	}
	if given["compose_path"] != dir+"/acc-scan-given/docker-compose.yml" || given["env_path"] != dir+"/acc-scan-given/.env" {
		t.Errorf("a folder with a .env beside its file = %v", given)
	}
	// a folder named as a stack that runs on a host is told to be that stack's
	if got := acctest.Strs(t, web["running_on"], "running_on"); len(got) != 1 || got[0] != testenv.Alpha+" (2 containers)" {
		t.Errorf("a folder named as a running stack = %v", web)
	}

	if none := call(t, "stack_scan", map[string]any{"path": laidUnder + "/empty"}); len(acctest.Rows(t, none["found"], "found")) != 0 || none["note"] != "no folder in that directory holds a compose file" {
		t.Errorf("a directory with no compose file in it = %v", none)
	}
	// the path is one inside Dockhand's own container: a host's is not seen
	if msg := refused(t, "stack_scan", map[string]any{"path": "/stacks"}); !strings.Contains(msg, "/stacks cannot be looked through") || !strings.Contains(msg, "inside Dockhand's own container") {
		t.Errorf("a path only a host has = %q", msg)
	}
}

// A stack is made from the text of a compose file with its variables and a secret, read back, checked, changed and brought up with the change, has a
// deploy fail and leave it running, is taken down and brought back, loses its volume, and is removed: on the host Dockhand reaches by its socket and
// on the one behind an agent, and twice over on each.
func TestJourneyAStackMadeChangedTakenDownAndRemoved(t *testing.T) {
	if world == nil {
		t.Skip(testenv.NotConfigured)
	}
	const (
		password = "not-a-real-password"
		key      = "not-a-real-key"
	)
	// what a service says, is given and keeps: a variable from the .env file, one kept as a secret, and one that has a value to fall back on
	file := func(says string) string {
		return "services:\n  app:\n    image: " + world.Image + "\n    command: [\"sh\", \"-c\", \"echo " + says + "; sleep 100000\"]\n    stop_grace_period: 1s\n    restart: unless-stopped\n    environment:\n      GREETING: ${GREETING}\n      DB_PASSWORD: ${DB_PASSWORD}\n      API_KEY: ${API_KEY:-none}\n    volumes:\n      - kept:/kept\nvolumes:\n  kept: {}\n"
	}

	for _, host := range []string{testenv.Alpha, testenv.Beta} {
		stack := "acc-made-" + host
		container, volume := stack+"-app-1", stack+"_kept"
		forgotten(t, host, stack, volume)
		on := func(more map[string]any) map[string]any {
			args := map[string]any{"stack": stack, "env": host}
			maps.Copy(args, more)

			return args
		}

		for round := range 2 {
			// said first, then made and brought up, and a second by its name refused
			making := map[string]any{"env": host, "name": stack, "compose": file("first"), "env_file": "GREETING=hello\n", "secrets": map[string]any{"DB_PASSWORD": password}}
			making["dry_run"] = true
			dry := call(t, "stack_create", making)
			if !acctest.IsBool(dry["dry_run"], true) || len(acctest.Strs(t, dry["changes"], "changes")) != 2 || stackNamed(t, stack) != nil || !strings.HasSuffix(acctest.Str(dry["compose_path"]), "/"+stack+"/compose.yaml") {
				t.Fatalf("round %d on %s: a dry run of making it = %v", round, host, dry)
			}
			delete(making, "dry_run")
			made := call(t, "stack_create", making)
			if made["compose_path"] != dry["compose_path"] {
				t.Errorf("round %d on %s: the file was to be kept at %v and is at %v", round, host, dry["compose_path"], made["compose_path"])
			}
			if made["env"] != host || made["stack"] != stack || made["status"] != "running" || acctest.Num(t, made["run"], "run") == 0 || !acctest.IsBool(made["running"], false) || acctest.Str(made["compose_path"]) == "" || len(acctest.Strs(t, made["changes"], "changes")) != 2 {
				t.Fatalf("round %d on %s: making it = %v", round, host, made)
			}
			if given := givenTo(t, host, container); !slices.Contains(given, "GREETING=hello") || !slices.Contains(given, "DB_PASSWORD="+password) || !slices.Contains(given, "API_KEY=none") {
				t.Fatalf("round %d on %s: its container was given %v", round, host, given)
			}
			if msg := refused(t, "stack_create", making); !strings.Contains(msg, "a stack named "+stack+" is already on "+host) {
				t.Errorf("round %d on %s: making it again = %q", round, host, msg)
			}

			// read back: the file as given, the variable from the .env file, and the secret hidden even when secrets are asked for
			got := call(t, "stack_get", on(map[string]any{"reveal_secrets": true}))
			vars := named(acctest.Rows(t, got["variables"], "variables"), "name")
			if got["kept"] != "internal" || !acctest.BoolOf(got["deployable"]) || got["compose"] != file("first") || got["compose_path"] != made["compose_path"] || got["status"] != "running" || vars["GREETING"]["value"] != "hello" || vars["DB_PASSWORD"]["value"] != "***" || !acctest.BoolOf(vars["DB_PASSWORD"]["secret"]) || strings.Contains(flat(got), password) {
				t.Fatalf("round %d on %s: the stack as kept = %v", round, host, got)
			}
			if last := acctest.Object(t, got["last_deploy"], "last_deploy"); last["status"] != "success" || acctest.Num(t, last["run"], "run") != acctest.Num(t, made["run"], "run") {
				t.Errorf("round %d on %s: its last deploy = %v, made as run %v", round, host, last, made["run"])
			}

			// its own file is sound and gives every variable it needs a value; a change that needs another is said to before it is saved
			sound := call(t, "stack_validate", on(nil))
			needs := acctest.Object(t, sound["variables"], "variables")
			if !acctest.BoolOf(sound["usable"]) || strings.Join(acctest.Strs(t, needs["required"], "required"), " ") != "DB_PASSWORD GREETING" || len(acctest.Strs(t, needs["missing"], "missing")) != 0 || strings.Join(acctest.Strs(t, needs["optional"], "optional"), " ") != "API_KEY" {
				t.Errorf("round %d on %s: its own file checked = %v", round, host, sound)
			}
			wanting := call(t, "stack_validate", on(map[string]any{"compose": strings.Replace(file("first"), "${GREETING}", "${GREETING} ${NOT_GIVEN}", 1)}))
			if got := acctest.Strs(t, acctest.Object(t, wanting["variables"], "variables")["missing"], "missing"); len(got) != 1 || got[0] != "NOT_GIVEN" || !strings.Contains(acctest.Str(wanting["note"]), "NOT_GIVEN") {
				t.Errorf("round %d on %s: a change that needs a variable the stack does not give = %v", round, host, wanting)
			}

			// changed: said first, then saved, and nothing that runs changes until it is deployed
			changing := on(map[string]any{"compose": file("second"), "env_file": "GREETING=goodbye\n", "secrets": map[string]any{"API_KEY": key}, "dry_run": true})
			if dry := call(t, "stack_edit", changing); len(acctest.Strs(t, dry["changes"], "changes")) != 3 || call(t, "stack_get", on(map[string]any{"reveal_secrets": true}))["compose"] != file("first") {
				t.Fatalf("round %d on %s: a dry run of changing it = %v", round, host, dry)
			}
			delete(changing, "dry_run")
			was := startedAt(t, host, container)
			edited := call(t, "stack_edit", changing)
			if len(acctest.Strs(t, edited["changes"], "changes")) != 3 || !strings.Contains(acctest.Str(edited["note"]), "the stack runs as it did until it is deployed") || startedAt(t, host, container) != was {
				t.Fatalf("round %d on %s: changing it = %v", round, host, edited)
			}
			// a variable to be filled in is shown as it is written, and only the fallback written into one is hidden
			got = call(t, "stack_get", on(nil))
			vars = named(acctest.Rows(t, got["variables"], "variables"), "name")
			if got["compose"] != strings.Replace(file("second"), "${API_KEY:-none}", "${API_KEY:-***}", 1) || vars["GREETING"]["value"] != "goodbye" || !acctest.BoolOf(vars["API_KEY"]["secret"]) || !acctest.BoolOf(vars["DB_PASSWORD"]["secret"]) || len(vars) != 3 {
				t.Errorf("round %d on %s: the stack as changed = %v", round, host, got)
			}
			// the file and the .env given again as they now are change nothing
			if again := call(t, "stack_edit", on(map[string]any{"compose": file("second"), "env_file": "GREETING=goodbye\n"})); len(acctest.Strs(t, again["changes"], "changes")) != 0 {
				t.Errorf("round %d on %s: changing it to what it is = %v", round, host, again)
			}

			// brought up with the change: the container is made again with what was saved, and the secret that was not given again kept its value
			if dry := call(t, "stack_deploy", on(map[string]any{"dry_run": true})); !acctest.IsBool(dry["dry_run"], true) || startedAt(t, host, container) != was {
				t.Fatalf("round %d on %s: a dry run of deploying = %v", round, host, dry)
			}
			deployed := call(t, "stack_deploy", on(nil))
			if deployed["was"] != "running" || deployed["is"] != "running" || acctest.Num(t, deployed["run"], "run") <= acctest.Num(t, made["run"], "run") || len(acctest.Strs(t, deployed["printed"], "printed")) == 0 || startedAt(t, host, container) == was {
				t.Fatalf("round %d on %s: deploying = %v", round, host, deployed)
			}
			if given := givenTo(t, host, container); !slices.Contains(given, "GREETING=goodbye") || !slices.Contains(given, "DB_PASSWORD="+password) || !slices.Contains(given, "API_KEY="+key) {
				t.Fatalf("round %d on %s: after the deploy its container was given %v", round, host, given)
			}
			if log := call(t, "container_logs", map[string]any{"container": container, "env": host}); !strings.Contains(acctest.Str(log["log"]), "second") {
				t.Errorf("round %d on %s: what it wrote after the deploy = %v", round, host, log["log"])
			}

			// its deploys, the newest first, and what compose printed during the last
			runs := call(t, "stack_deploys", on(map[string]any{"limit": 2}))
			rows := acctest.Rows(t, runs["deploys"], "deploys")
			if len(rows) != 2 || acctest.Num(t, rows[0]["run"], "run") != acctest.Num(t, deployed["run"], "run") || acctest.Num(t, rows[1]["run"], "run") != acctest.Num(t, made["run"], "run") || rows[0]["status"] != "success" || acctest.Num(t, runs["total"], "total") < 2 {
				t.Fatalf("round %d on %s: its deploys = %v", round, host, runs)
			}
			one := call(t, "stack_deploys", on(map[string]any{"run": deployed["run"]}))
			if lines := acctest.Strs(t, one["log"], "log"); len(lines) == 0 || !strings.Contains(strings.Join(lines, "\n"), container) || acctest.Num(t, one["lines"], "lines") != len(lines) {
				t.Errorf("round %d on %s: what the deploy printed = %v", round, host, one)
			}

			// the record of the first deploy is removed, said first, and the others are left
			first := on(map[string]any{"run": made["run"]})
			if preview := call(t, "stack_deploys_delete", first); acctest.BoolOf(preview["deleted"]) || acctest.Num(t, acctest.Object(t, preview["deploy"], "deploy")["run"], "run") != acctest.Num(t, made["run"], "run") {
				t.Fatalf("round %d on %s: what removing a deploy's record would take = %v", round, host, preview)
			}
			first["confirm"] = true
			if gone := call(t, "stack_deploys_delete", first); !acctest.BoolOf(gone["deleted"]) {
				t.Fatalf("round %d on %s: removing a deploy's record = %v", round, host, gone)
			}
			if left := acctest.Rows(t, call(t, "stack_deploys", on(map[string]any{"limit": 2}))["deploys"], "deploys"); len(left) == 0 || acctest.Num(t, left[0]["run"], "run") != acctest.Num(t, deployed["run"], "run") || (len(left) > 1 && acctest.Num(t, left[1]["run"], "run") == acctest.Num(t, made["run"], "run")) {
				t.Errorf("round %d on %s: its deploys once one was removed = %v", round, host, left)
			}
			if msg := refused(t, "stack_deploys_delete", first); !strings.Contains(msg, "has no deploy numbered") {
				t.Errorf("round %d on %s: removing the record again = %q", round, host, msg)
			}

			// a deploy compose refuses is an error, leaves what ran running, and is recorded with why
			absent := strings.Replace(file("third"), world.Image, "acc-local/never-built:1\n    pull_policy: never", 1)
			call(t, "stack_edit", on(map[string]any{"compose": absent}))
			was = startedAt(t, host, container)
			msg := refused(t, "stack_deploy", on(nil))
			if !strings.Contains(msg, "was not deployed") || !strings.Contains(msg, "what was running is left as it was") || !strings.Contains(msg, "stack_deploys with run") || startedAt(t, host, container) != was {
				t.Fatalf("round %d on %s: a deploy that cannot work = %q", round, host, msg)
			}
			failed := acctest.Object(t, call(t, "stack_get", on(nil))["last_deploy"], "last_deploy")
			if failed["status"] != "failed" || acctest.Str(failed["error"]) == "" || acctest.Num(t, failed["run"], "run") <= acctest.Num(t, deployed["run"], "run") {
				t.Errorf("round %d on %s: the deploy that failed, as recorded = %v", round, host, failed)
			}
			if why := call(t, "stack_deploys", on(map[string]any{"run": failed["run"]})); len(acctest.Strs(t, why["log"], "log")) == 0 {
				t.Errorf("round %d on %s: what the failed deploy printed = %v", round, host, why)
			}
			call(t, "stack_edit", on(map[string]any{"compose": file("second")}))

			// taken down: said first, then its container is gone from the host and its volume kept, and Dockhand still has its file
			if dry := call(t, "stack_down", on(map[string]any{"dry_run": true})); strings.Join(acctest.Strs(t, dry["removed"], "removed"), " ") != container || !containerOn(t, host, container) {
				t.Fatalf("round %d on %s: a dry run of taking it down = %v", round, host, dry)
			}
			down := call(t, "stack_down", on(nil))
			if down["was"] != "running" || down["is"] != "created" || strings.Join(acctest.Strs(t, down["removed"], "removed"), " ") != container || containerOn(t, host, container) || !volumeOn(t, host, volume) {
				t.Fatalf("round %d on %s: taking it down = %v", round, host, down)
			}
			if listed := stackNamed(t, stack); listed == nil || listed["status"] != "created" || len(acctest.Rows(t, listed["services"], "services")) != 0 {
				t.Errorf("round %d on %s: the stack while down = %v", round, host, listed)
			}
			if again := call(t, "stack_down", on(nil)); len(acctest.Strs(t, again["removed"], "removed")) != 0 || again["is"] != "created" {
				t.Errorf("round %d on %s: taking it down again = %v", round, host, again)
			}

			// brought back from the file, and taken down with its volume
			if back := call(t, "stack_deploy", on(nil)); back["was"] != "created" || back["is"] != "running" || !containerOn(t, host, container) {
				t.Fatalf("round %d on %s: bringing it back = %v", round, host, back)
			}
			emptied := call(t, "stack_down", on(map[string]any{"remove_volumes": true}))
			if strings.Join(acctest.Strs(t, emptied["volumes"], "volumes"), " ") != volume || volumeOn(t, host, volume) || containerOn(t, host, container) {
				t.Fatalf("round %d on %s: taking it down with its volume = %v", round, host, emptied)
			}

			// removed: said first with what would go, then gone from Dockhand and from the host
			call(t, "stack_deploy", on(nil))
			preview := call(t, "stack_delete", on(nil))
			if acctest.BoolOf(preview["deleted"]) || preview["kept"] != "internal" || strings.Join(acctest.Strs(t, preview["containers"], "containers"), " ") != container+" (running)" || strings.Join(acctest.Strs(t, preview["volumes"], "volumes"), " ") != volume || acctest.Str(preview["files"]) == "" || !containerOn(t, host, container) {
				t.Fatalf("round %d on %s: what removing it would take = %v", round, host, preview)
			}
			removed := call(t, "stack_delete", on(map[string]any{"confirm": true, "volumes": true}))
			if !acctest.BoolOf(removed["deleted"]) || removed["note"] != nil || stackNamed(t, stack) != nil || containerOn(t, host, container) || volumeOn(t, host, volume) || networkOn(t, host, stack+"_default") {
				t.Fatalf("round %d on %s: removing it = %v", round, host, removed)
			}
			if msg := refused(t, "stack_get", on(nil)); !strings.Contains(msg, stack) {
				t.Errorf("round %d on %s: reading it once removed = %q", round, host, msg)
			}
		}
	}

	// a file compose would not take is never made into a stack
	if msg := refused(t, "stack_create", map[string]any{"env": testenv.Alpha, "name": "acc-never-made", "compose": "services:\n  app: [\n"}); !strings.Contains(msg, "compose would not take the file, so nothing was made") || stackNamed(t, "acc-never-made") != nil {
		t.Errorf("a broken file made into a stack = %q", msg)
	}
	// and a name a stack someone else brought up already has is not taken over
	if msg := refused(t, "stack_create", map[string]any{"env": testenv.Alpha, "name": webStack, "compose": file("taken")}); !strings.Contains(msg, "a stack named "+webStack+" is already on "+testenv.Alpha) || call(t, "stack_get", map[string]any{"stack": webStack})["kept"] != "found" {
		t.Errorf("a name that is taken = %q", msg)
	}
}

// A stack someone brought up at a host by hand, which Dockhand can only start and stop, is given to Dockhand by showing it the compose file where the
// file lies: nothing running is touched, and from then on the stack is read, changed and deployed like one Dockhand made. Removed, its file stays.
func TestJourneyAStackFoundAdoptedDeployedAndRemoved(t *testing.T) {
	if world == nil {
		t.Skip(testenv.NotConfigured)
	}
	const (
		host      = testenv.Alpha
		stack     = "acc-adopted"
		container = stack + "-app-1"
		dir       = laidUnder + "/adopt"
		path      = dir + "/" + stack + "/compose.yaml"
	)
	file := func(says string) string {
		return "name: " + stack + "\nservices:\n  app:\n    image: " + world.Image + "\n    command: [\"sh\", \"-c\", \"echo " + says + "; sleep 100000\"]\n    stop_grace_period: 1s\n"
	}
	ctx := context.WithoutCancel(t.Context())
	forgotten(t, host, stack)
	t.Cleanup(func() { _ = world.Unlay(ctx, dir) })

	// brought up at the host, and the same file put where Dockhand's own container sees it
	if err := world.Compose(ctx, host, stack, file("by hand")); err != nil {
		t.Fatal(err)
	}
	if err := world.Lay(ctx, path, file("by hand")); err != nil {
		t.Fatal(err)
	}
	on := func(more map[string]any) map[string]any {
		args := map[string]any{"stack": stack}
		maps.Copy(args, more)

		return args
	}

	// as found: no file, so nothing to deploy, change or check
	found := call(t, "stack_get", on(nil))
	if found["kept"] != "found" || acctest.BoolOf(found["deployable"]) || found["working_dir"] != "/stacks/"+stack || found["status"] != "running" {
		t.Fatalf("%s as found = %v", stack, found)
	}
	for tool, args := range map[string]map[string]any{"stack_deploy": on(nil), "stack_edit": on(map[string]any{"env_file": "A=1\n"})} {
		if msg := refused(t, tool, args); !strings.Contains(msg, "Dockhand has no compose file for "+stack) || !strings.Contains(msg, "stack_adopt") {
			t.Errorf("%s on a stack with no file = %q", tool, msg)
		}
	}

	// found by looking, and told to be the stack that runs
	scanned := named(acctest.Rows(t, call(t, "stack_scan", map[string]any{"path": dir})["found"], "found"), "name")[stack]
	if scanned == nil || scanned["compose_path"] != path || scanned["adopted"] != nil || strings.Join(acctest.Strs(t, scanned["running_on"], "running_on"), " ") != host+" (1 containers)" {
		t.Fatalf("%s as scanned = %v", stack, scanned)
	}

	// adopted: said first, then done, and the container is the one that was running
	was := startedAt(t, host, container)
	adopting := map[string]any{"env": host, "name": stack, "compose_path": path, "dry_run": true}
	if dry := call(t, "stack_adopt", adopting); !acctest.IsBool(dry["dry_run"], true) || dry["status"] != "running" || call(t, "stack_get", on(nil))["kept"] != "found" {
		t.Fatalf("a dry run of adopting = %v", dry)
	}
	delete(adopting, "dry_run")
	adopted := call(t, "stack_adopt", adopting)
	if adopted["env"] != host || adopted["stack"] != stack || adopted["compose_path"] != path || adopted["status"] != "running" || len(acctest.Strs(t, adopted["changes"], "changes")) != 1 || startedAt(t, host, container) != was {
		t.Fatalf("adopting = %v", adopted)
	}
	kept := call(t, "stack_get", on(nil))
	if kept["kept"] != "internal" || !acctest.BoolOf(kept["deployable"]) || kept["compose_path"] != path || kept["compose"] != file("by hand") || kept["working_dir"] != nil {
		t.Fatalf("%s once adopted = %v", stack, kept)
	}
	if msg := refused(t, "stack_adopt", adopting); !strings.Contains(msg, "Dockhand already has a compose file for "+stack) {
		t.Errorf("adopting it again = %q", msg)
	}
	if again := named(acctest.Rows(t, call(t, "stack_scan", map[string]any{"path": dir})["found"], "found"), "name")[stack]; !acctest.BoolOf(again["adopted"]) {
		t.Errorf("%s scanned once adopted = %v", stack, again)
	}
	// a file Dockhand's container cannot see is refused in Dockhand's words
	if msg := refused(t, "stack_adopt", map[string]any{"env": host, "name": "acc-not-there", "compose_path": dir + "/acc-not-there/compose.yaml"}); !strings.Contains(msg, "acc-not-there was not adopted") {
		t.Errorf("adopting a file that is not there = %q", msg)
	}

	// changed: the file is written where it lies, and deploying brings the stack up from it
	if edited := call(t, "stack_edit", on(map[string]any{"compose": file("by dockhand")})); len(acctest.Strs(t, edited["changes"], "changes")) != 1 {
		t.Fatalf("changing the adopted file = %v", edited)
	}
	if laid, err := world.Laid(ctx, path); err != nil || laid != file("by dockhand") {
		t.Fatalf("the file where it lies = %q, %v", laid, err)
	}
	deployed := call(t, "stack_deploy", on(nil))
	if deployed["is"] != "running" || startedAt(t, host, container) == was {
		t.Fatalf("deploying the adopted stack = %v", deployed)
	}
	if log := call(t, "container_logs", map[string]any{"container": container}); !strings.Contains(acctest.Str(log["log"]), "by dockhand") {
		t.Errorf("what it wrote after the deploy = %v", log["log"])
	}

	// removed: its containers go and Dockhand forgets it, and the file stays where it lies
	preview := call(t, "stack_delete", on(nil))
	if preview["files"] != nil || !strings.Contains(acctest.Str(preview["note"]), "are not deleted") || !containerOn(t, host, container) {
		t.Fatalf("what removing it would take = %v", preview)
	}
	if removed := call(t, "stack_delete", on(map[string]any{"confirm": true})); !acctest.BoolOf(removed["deleted"]) || containerOn(t, host, container) || stackNamed(t, stack) != nil {
		t.Fatalf("removing it = %v", removed)
	}
	if laid, err := world.Laid(ctx, path); err != nil || laid != file("by dockhand") {
		t.Errorf("the file after the stack was removed = %q, %v", laid, err)
	}
}

// A stack Dockhand made and keeps in its own data directory is moved out of it, to a folder that could be kept elsewhere: its files go, the stack
// runs on untouched and is deployed from where they now are, a folder that already holds something is not moved into unasked, and removing the stack
// then leaves the files.
func TestJourneyAStackMovedOutOfDockhandsKeeping(t *testing.T) {
	if world == nil {
		t.Skip(testenv.NotConfigured)
	}
	const (
		host      = testenv.Alpha
		stack     = "acc-moved"
		container = stack + "-app-1"
		dir       = laidUnder + "/moved"
		to        = dir + "/" + stack + "/compose.yaml"
		taken     = dir + "/taken"
	)
	ctx := context.WithoutCancel(t.Context())
	forgotten(t, host, stack)
	_ = world.Unlay(ctx, dir)
	t.Cleanup(func() { _ = world.Unlay(ctx, dir) })

	file := "services:\n  app:\n    image: " + world.Image + "\n    command: [\"sh\", \"-c\", \"echo $$MODE; sleep 100000\"]\n    stop_grace_period: 1s\n    environment:\n      MODE: ${MODE}\n"
	made := call(t, "stack_create", map[string]any{"env": host, "name": stack, "compose": file, "env_file": "MODE=demo\n"})
	from := acctest.Str(made["compose_path"])
	if made["status"] != "running" || from == "" {
		t.Fatalf("making it = %v", made)
	}
	was := startedAt(t, host, container)
	on := func(more map[string]any) map[string]any {
		args := map[string]any{"stack": stack}
		maps.Copy(args, more)

		return args
	}

	// a folder that holds something is not moved into, since Dockhand would write over it
	if err := world.Lay(ctx, taken+"/compose.yaml", "someone else's\n"); err != nil {
		t.Fatal(err)
	}
	if msg := refused(t, "stack_move", on(map[string]any{"compose_path": taken + "/compose.yaml"})); !strings.Contains(msg, taken+" is there already") {
		t.Errorf("a move into a folder that holds something = %q", msg)
	}
	if kept, err := world.Laid(ctx, taken+"/compose.yaml"); err != nil || kept != "someone else's\n" {
		t.Fatalf("what the folder held after the move was refused = %q, %v", kept, err)
	}

	// said first, then moved: the files are where they were sent and nothing running was touched
	if dry := call(t, "stack_move", on(map[string]any{"compose_path": to, "dry_run": true})); !acctest.IsBool(dry["dry_run"], true) || dry["is"] != from || len(acctest.Strs(t, dry["changes"], "changes")) != 1 {
		t.Fatalf("a dry run of moving = %v", dry)
	}
	moved := call(t, "stack_move", on(map[string]any{"compose_path": to}))
	if moved["was"] != from || moved["is"] != to || strings.Join(acctest.Strs(t, moved["moved"], "moved"), " ") != ".env compose.yaml" || startedAt(t, host, container) != was {
		t.Fatalf("moving = %v", moved)
	}
	if laid, err := world.Laid(ctx, to); err != nil || laid != file {
		t.Fatalf("the compose file where it was moved to = %q, %v", laid, err)
	}
	if gone, err := world.Laid(ctx, from); err == nil {
		t.Errorf("the compose file is still where it was: %q", gone)
	}
	got := call(t, "stack_get", on(nil))
	if vars := acctest.Rows(t, got["variables"], "variables"); got["compose_path"] != to || got["compose"] != file || len(vars) != 1 || vars[0]["value"] != "demo" || got["status"] != "running" {
		t.Errorf("the stack once moved = %v", got)
	}
	if again := call(t, "stack_move", on(map[string]any{"compose_path": to})); len(acctest.Strs(t, again["changes"], "changes")) != 0 {
		t.Errorf("moving it to where it is = %v", again)
	}

	// changed and deployed from where it now is
	call(t, "stack_edit", on(map[string]any{"env_file": "MODE=moved\n"}))
	if deployed := call(t, "stack_deploy", on(nil)); deployed["is"] != "running" || !slices.Contains(givenTo(t, host, container), "MODE=moved") {
		t.Fatalf("deploying from the new folder = %v, given %v", deployed, givenTo(t, host, container))
	}

	// removed: the files are no longer Dockhand's to delete, and stay
	preview := call(t, "stack_delete", on(nil))
	if preview["files"] != nil || !strings.Contains(acctest.Str(preview["note"]), "are not deleted") {
		t.Errorf("what removing it would take = %v", preview)
	}
	if removed := call(t, "stack_delete", on(map[string]any{"confirm": true})); !acctest.BoolOf(removed["deleted"]) || containerOn(t, host, container) {
		t.Fatalf("removing it = %v", removed)
	}
	if laid, err := world.Laid(ctx, to); err != nil || laid != file {
		t.Errorf("the compose file after the stack was removed = %q, %v", laid, err)
	}
}
