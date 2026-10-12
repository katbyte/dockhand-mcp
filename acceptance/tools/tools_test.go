//go:build acceptance

package tools_test

import (
	"slices"
	"strings"
	"testing"

	"github.com/katbyte/dockhand-mcp/lib/testenv"
	"github.com/katbyte/dockhand-mcp/sdk/dockhand"
	"github.com/katbyte/go-kt/mcp/acctest"
)

func TestServerInfo(t *testing.T) {
	out := call(t, "server_info", nil)

	if out["url"] != world.Server || acctest.Str(out["version"]) == "" || acctest.Str(out["released"]) == "" || out["sdk_api_version"] != dockhand.APIVersion {
		t.Errorf("the server = %v", out)
	}
	if !acctest.IsBool(out["login"], world.Stage == testenv.StageLogin) {
		t.Errorf("login = %v in the %s stage", out["login"], world.Stage)
	}
	if out["database"] != "sqlite" || !acctest.IsBool(out["database_healthy"], true) || acctest.Str(out["schema_version"]) == "" {
		t.Errorf("its database = %v", out)
	}
	if acctest.Num(t, out["environments"], "environments") != len(world.Environments) {
		t.Errorf("environments = %v, want the %d this run registered", out["environments"], len(world.Environments))
	}

	// a server anyone can use says so, and one with its login on does not
	said := acctest.Str(out["note"])
	if strings.Contains(said, "login is switched off") != (world.Stage == testenv.StageOpen) {
		t.Errorf("the note in the %s stage = %q", world.Stage, said)
	}
}

// The test server is given no Docker socket of its own, so it cannot look for a newer version of itself: that is told apart from "none is out".
func TestServerUpdateCheck(t *testing.T) {
	out := call(t, "server_update_check", nil)

	if acctest.BoolOf(out["checked"]) || acctest.BoolOf(out["update_available"]) || !strings.Contains(acctest.Str(out["note"]), "was not given it") || out["image"] != nil {
		t.Errorf("the update check of a Dockhand with no socket = %v", out)
	}
}

func TestServerAbout(t *testing.T) {
	// the licence, a part at a time, each part going on from the last
	first := call(t, "server_about", map[string]any{"limit": 500})
	whole := acctest.Num(t, first["characters"], "characters")
	if first["what"] != "licence" || !strings.HasPrefix(acctest.Str(first["text"]), "DOCKHAND LICENSE") || whole < 5000 || acctest.Num(t, first["next"], "next") != 500 {
		t.Fatalf("the start of the licence = %v", first)
	}
	rest := call(t, "server_about", map[string]any{"what": "licence", "offset": 500, "limit": 30000})
	if len([]rune(acctest.Str(rest["text"]))) != whole-500 || rest["next"] != nil || rest["note"] != nil {
		t.Errorf("the rest of the licence is %d characters of %v, next %v", len([]rune(acctest.Str(rest["text"]))), whole, rest["next"])
	}
	if policy := call(t, "server_about", map[string]any{"what": "privacy", "limit": 200}); !strings.Contains(acctest.Str(policy["text"]), "PRIVACY POLICY") || acctest.Num(t, policy["characters"], "characters") < 2000 {
		t.Errorf("the privacy policy = %v", policy)
	}

	// what it is built from: counted by licence, found by name, narrowed by licence
	built := call(t, "server_about", map[string]any{"what": "dependencies", "limit": 3})
	total := acctest.Num(t, built["total"], "total")
	licences := acctest.Rows(t, built["licences"], "licences")
	if total < 100 || len(acctest.Rows(t, built["dependencies"], "dependencies")) != 3 || len(licences) < 3 || licences[0]["licence"] != "MIT" || !strings.Contains(acctest.Str(built["note"]), "the first 3 of") {
		t.Fatalf("what it is built from = %v", built)
	}
	counted := 0
	for _, licence := range licences {
		counted += acctest.Num(t, licence["count"], "count")
	}
	if counted != total {
		t.Errorf("the licences count %v of %v", counted, total)
	}
	under := call(t, "server_about", map[string]any{"what": "dependencies", "licence": "mit", "limit": 500})
	if acctest.Num(t, under["matched"], "matched") != acctest.Num(t, licences[0]["count"], "count") || under["note"] != nil {
		t.Errorf("under MIT: %v matched, and the count says %v", under["matched"], licences[0]["count"])
	}
	if scanner := call(t, "server_about", map[string]any{"what": "dependencies", "find": "GRYPE"}); acctest.Num(t, scanner["matched"], "matched") < 1 || !strings.Contains(flat(scanner), "anchore/grype") {
		t.Errorf("found by name = %v", scanner)
	}

	if msg := refused(t, "server_about", map[string]any{"what": "terms"}); !strings.Contains(msg, "it is licence, privacy or dependencies") {
		t.Errorf("asked for something else = %q", msg)
	}
}

func TestEnvList(t *testing.T) {
	out := call(t, "env_list", nil)
	rows := named(acctest.Rows(t, out["environments"], "environments"), "env")

	alpha, beta := rows[testenv.Alpha], rows[testenv.Beta]
	if acctest.Num(t, alpha["id"], "id") != world.Env(testenv.Alpha) || alpha["connection"] != testenv.ConnectionSocket || alpha["address"] != nil || !acctest.IsBool(alpha["online"], true) {
		t.Errorf("alpha = %v: reached by its socket, with no address, and online", alpha)
	}
	if acctest.Num(t, beta["id"], "id") != world.Env(testenv.Beta) || beta["connection"] != testenv.ConnectionAgent || beta["address"] != world.Agent || !acctest.IsBool(beta["online"], true) {
		t.Errorf("beta = %v: reached through the agent at %s, and online", beta, world.Agent)
	}

	// the counts are of what the fixtures put there: five containers on alpha, one of them Docker is restarting, and one on beta
	alphaCounts, betaCounts := acctest.Object(t, alpha["containers"], "containers"), acctest.Object(t, beta["containers"], "containers")
	if acctest.Num(t, alphaCounts["total"], "total") != 5 || acctest.Num(t, alphaCounts["running"], "running") < 3 || acctest.Num(t, betaCounts["total"], "total") != 1 || acctest.Num(t, betaCounts["running"], "running") != 1 {
		t.Errorf("containers: alpha %v, beta %v", alphaCounts, betaCounts)
	}
	if stacks := acctest.Object(t, alpha["stacks"], "stacks"); acctest.Num(t, stacks["total"], "total") != 1 {
		t.Errorf("alpha's stacks = %v, want the one brought up there", stacks)
	}
}

func TestContainerList(t *testing.T) {
	out := call(t, "container_list", nil)
	rows := named(acctest.Rows(t, out["containers"], "containers"), "name")

	if acctest.Num(t, out["total"], "total") != 6 || len(rows) != 6 || out["failed"] != nil {
		t.Fatalf("the containers of both hosts = %v", out)
	}
	for name, env := range map[string]string{webFront: testenv.Alpha, webBack: testenv.Alpha, loose: testenv.Alpha, crashed: testenv.Alpha, looping: testenv.Alpha, solo: testenv.Beta} {
		row := rows[name]
		if row["env"] != env || len(acctest.Str(row["id"])) != 12 || row["image"] != world.Image || acctest.Str(row["created"]) == "" {
			t.Errorf("%s = %v, want it on %s", name, row, env)
		}
	}

	// a stack's containers say which stack and which service, and what they publish
	front := rows[webFront]
	if front["stack"] != webStack || front["service"] != "front" || front["state"] != "running" || !strings.HasPrefix(acctest.Str(front["status"]), "Up") {
		t.Errorf("%s = %v", webFront, front)
	}
	if got := strings.Join(acctest.Strs(t, front["ports"], "ports"), " "); got != "18081->80/tcp 18082->81/udp" {
		t.Errorf("%s publishes %q", webFront, got)
	}
	if rows[loose]["stack"] != nil || rows[loose]["health"] != nil || rows[loose]["problem"] != nil || rows[solo]["problem"] != nil {
		t.Errorf("containers on their own with nothing wrong: %v, %v", rows[loose], rows[solo])
	}

	// what is wrong is said of each: a health check takes a few seconds to fail for the first time
	healthSettled := acctest.Eventually(func() bool {
		found := named(acctest.RowsOf(call(t, "container_list", map[string]any{"stack": webStack})["containers"]), "name")

		return found[webBack]["health"] == "unhealthy" && found[webFront]["health"] == "healthy"
	})
	if !healthSettled {
		t.Fatalf("the stack's health checks never settled: %v", call(t, "container_list", map[string]any{"stack": webStack}))
	}
	// a container that keeps exiting is caught between runs or during one, and is said to be wrong either way
	var problems map[string]any
	var wrong map[string]map[string]any
	looped := acctest.Eventually(func() bool {
		problems = call(t, "container_list", map[string]any{"problems": true})
		wrong = named(acctest.RowsOf(problems["containers"]), "name")
		said := acctest.Str(wrong[looping]["problem"])

		return strings.Contains(said, "restarting") || strings.Contains(said, "started again by Docker")
	})
	if !looped {
		t.Fatalf("%s, which exits as soon as it starts, was never said to have anything wrong with it: %v", looping, problems)
	}
	for name, want := range map[string]string{webBack: "unhealthy", crashed: "exited with code 3"} {
		if got := acctest.Str(wrong[name]["problem"]); !strings.Contains(got, want) {
			t.Errorf("%s: problem = %q, want %q", name, got, want)
		}
	}
	if len(wrong) != 3 || acctest.Num(t, problems["problems"], "problems") != 3 || acctest.Num(t, wrong[looping]["restarts"], "restarts") < 1 {
		t.Errorf("problems alone = %v", problems)
	}
}

func TestContainerListNarrowed(t *testing.T) {
	names := func(args map[string]any) []string {
		rows := acctest.Rows(t, call(t, "container_list", args)["containers"], "containers")
		got := make([]string, 0, len(rows))
		for _, row := range rows {
			got = append(got, acctest.Str(row["name"]))
		}

		return got
	}

	for what, tc := range map[string]struct {
		args map[string]any
		want []string
	}{
		"the second host by name":     {map[string]any{"env": testenv.Beta}, []string{solo}},
		"the second host by number":   {map[string]any{"env": world.Env(testenv.Beta)}, []string{solo}},
		"a host whatever its case":    {map[string]any{"env": "BETA"}, []string{solo}},
		"a stack":                     {map[string]any{"stack": webStack}, []string{webBack, webFront}},
		"a part of a name":            {map[string]any{"name": "ACC-LO"}, []string{looping, loose}},
		"a state":                     {map[string]any{"state": "paused"}, []string{}},
		"a part of a name in a state": {map[string]any{"name": "crash", "state": "exited"}, []string{crashed}},
		"a stack on another host":     {map[string]any{"env": testenv.Beta, "stack": webStack}, []string{}},
	} {
		if got := names(tc.args); !slices.Equal(got, tc.want) {
			t.Errorf("%s = %v, want %v", what, got, tc.want)
		}
	}

	out := call(t, "container_list", map[string]any{"limit": 2})
	if rows := acctest.Rows(t, out["containers"], "containers"); len(rows) != 2 || acctest.Num(t, out["total"], "total") != 6 || !strings.Contains(acctest.Str(out["note"]), "6 containers matched and the first 2 are listed") {
		t.Errorf("cut at 2 = %v", out)
	}

	if msg := refused(t, "container_list", map[string]any{"env": "gamma"}); !strings.Contains(msg, `no environment is named or numbered "gamma"`) || !strings.Contains(msg, testenv.Alpha) || !strings.Contains(msg, testenv.Beta) {
		t.Errorf("a host that is none = %q", msg)
	}
	if msg := refused(t, "container_list", map[string]any{"limit": -1}); !strings.Contains(msg, "limit is -1") {
		t.Errorf("a limit below nothing = %q", msg)
	}
}

func TestStackList(t *testing.T) {
	// a stack someone brought up at the host is listed as any other
	var web map[string]any
	settled := acctest.Eventually(func() bool {
		stacks := named(acctest.RowsOf(call(t, "stack_list", nil)["stacks"]), "stack")
		web = stacks[webStack]

		return web != nil && acctest.NumOr0(web["problems"]) == 1
	})
	if !settled {
		t.Fatalf("%s with its one unhealthy service was never listed: %v", webStack, web)
	}

	services := named(acctest.Rows(t, web["services"], "services"), "service")
	if web["env"] != testenv.Alpha || web["status"] != "running" || len(services) != 2 {
		t.Fatalf("%s = %v", webStack, web)
	}
	front, back := services["front"], services["back"]
	if front["container"] != webFront || front["image"] != world.Image || front["state"] != "running" || front["health"] != "healthy" || front["problem"] != nil {
		t.Errorf("front = %v", front)
	}
	if got := strings.Join(acctest.Strs(t, front["ports"], "ports"), " "); got != "18081:80/tcp 18082:81/udp" {
		t.Errorf("front publishes %q", got)
	}
	if back["container"] != webBack || back["health"] != "unhealthy" || !strings.Contains(acctest.Str(back["problem"]), "unhealthy") {
		t.Errorf("back = %v", back)
	}

	// containers that belong to no stack make none, and the second host has no stack
	all := call(t, "stack_list", nil)
	if acctest.Num(t, all["total"], "total") != 1 || acctest.Num(t, acctest.Object(t, all["by_status"], "by_status")["running"], "running") != 1 {
		t.Errorf("every stack = %v", all)
	}
	if rows := acctest.RowsOf(call(t, "stack_list", map[string]any{"env": testenv.Beta})["stacks"]); len(rows) != 0 {
		t.Errorf("the second host's stacks = %v", rows)
	}
	if rows := acctest.RowsOf(call(t, "stack_list", map[string]any{"name": "ACC", "problems": true})["stacks"]); len(rows) != 1 {
		t.Errorf("stacks with something wrong, by a part of a name = %v", rows)
	}
	if rows := acctest.RowsOf(call(t, "stack_list", map[string]any{"name": "nothing-by-this-name"})["stacks"]); len(rows) != 0 {
		t.Errorf("stacks by a name none has = %v", rows)
	}

	if msg := refused(t, "stack_list", map[string]any{"env": "gamma"}); !strings.Contains(msg, `no environment is named or numbered "gamma"`) {
		t.Errorf("a host that is none = %q", msg)
	}
}
