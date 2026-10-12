//go:build acceptance

package tools_test

import (
	"context"
	"strings"
	"testing"

	"github.com/katbyte/dockhand-mcp/lib/testenv"
	"github.com/katbyte/go-kt/mcp/acctest"
)

// audits is every audit in the order audit_all runs them.
var audits = []string{"audit_health", "audit_hosts", "audit_stacks", "audit_healthcheck", "audit_restart_policy", "audit_updates", "audit_vulnerabilities", "audit_security", "audit_image_tags", "audit_limits", "audit_stopped", "audit_unused"}

// audited is what an audit found, by name/check, and its whole answer.
func audited(t *testing.T, tool string, args map[string]any) (found map[string]map[string]any, out map[string]any) {
	t.Helper()

	out = call(t, tool, args)
	found = map[string]map[string]any{}
	for _, row := range acctest.Rows(t, out["findings"], "findings") {
		found[acctest.Str(row["name"])+"/"+acctest.Str(row["check"])] = row
	}
	if acctest.Num(t, out["total"], "total") < len(found) || out["failed"] != nil {
		t.Errorf("%s: total %v for %d findings, failed %v", tool, out["total"], len(found), out["failed"])
	}

	return found, out
}

// The fixtures are a small host with most of what the audits look for: one container unhealthy, one that fell over, one that keeps restarting, and
// one run with nothing said about how it is to be kept.
func TestAuditsOfWhatIsBrokenAndWhatWillBreak(t *testing.T) {
	alpha := map[string]any{"env": testenv.Alpha}

	// what is broken now: the health check's own words, the exit code, and the one that never stays up, in whichever state it is caught
	var broken map[string]map[string]any
	if !acctest.Eventually(func() bool {
		broken, _ = audited(t, "audit_health", alpha)

		return broken[webBack+"/unhealthy"] != nil && broken[crashed+"/exited_with_error"] != nil
	}) {
		t.Fatalf("the unhealthy container and the one that exited were never both found: %v", broken)
	}
	if sick := broken[webBack+"/unhealthy"]; sick["kind"] != "container" || sick["env"] != testenv.Alpha || sick["stack"] != webStack || !strings.Contains(acctest.Str(sick["detail"]), "the latest printed: not ready") {
		t.Errorf("the unhealthy one = %v", sick)
	}
	if got := acctest.Str(broken[crashed+"/exited_with_error"]["detail"]); !strings.HasPrefix(got, "exited with code 3, at ") {
		t.Errorf("the one that exited = %q", got)
	}
	if broken[looping+"/restarting"] == nil && broken[looping+"/restart_loop"] == nil && broken[looping+"/exited_with_error"] == nil {
		t.Errorf("the one that keeps restarting was not found: %v", broken)
	}
	if broken[webFront+"/unhealthy"] != nil || broken[loose+"/unhealthy"] != nil {
		t.Errorf("a container with nothing wrong was found: %v", broken)
	}
	// narrowed to the stack, only its own are looked at
	if narrowed, out := audited(t, "audit_health", map[string]any{"stack": webStack}); narrowed[webBack+"/unhealthy"] == nil || narrowed[crashed+"/exited_with_error"] != nil || acctest.Num(t, out["checked"], "checked") != 2 {
		t.Errorf("the stack alone = %v, checked %v", narrowed, out["checked"])
	}

	// what nothing checks: the one run on its own, and neither of the stack's, which were each given a check
	unchecked, _ := audited(t, "audit_healthcheck", alpha)
	if unchecked[loose+"/no_health_check"] == nil || unchecked[webFront+"/no_health_check"] != nil || unchecked[webBack+"/no_health_check"] != nil {
		t.Errorf("what nothing checks = %v", unchecked)
	}

	// what would stay down: the one run with no policy, not the stack's front, which has one
	down, _ := audited(t, "audit_restart_policy", alpha)
	if down[loose+"/no_restart_policy"] == nil || down[webFront+"/no_restart_policy"] != nil {
		t.Errorf("what would stay down = %v", down)
	}

	// what may use everything: the one run with no limit, not the stack's front, which has one
	unlimited, _ := audited(t, "audit_limits", alpha)
	if unlimited[loose+"/no_memory_limit"] == nil || unlimited[webFront+"/no_memory_limit"] != nil {
		t.Errorf("what may use everything = %v", unlimited)
	}

	// what stopped minutes ago is not left over, and it was looked at
	if stale, out := audited(t, "audit_stopped", alpha); stale[crashed+"/stopped_long"] != nil || acctest.Num(t, out["checked"], "checked") < 1 {
		t.Errorf("what was left lying = %v, checked %v", stale, out["checked"])
	}

	// both hosts are reached, so neither is found
	if offline, out := audited(t, "audit_hosts", nil); len(offline) != 0 || acctest.Num(t, out["checked"], "checked") != len(world.Environments) {
		t.Errorf("the hosts that cannot be reached = %v, checked %v", offline, out["checked"])
	}

	// nothing was ever noted of a newer image for these, and every container was looked at
	if _, out := audited(t, "audit_updates", alpha); acctest.Num(t, out["checked"], "checked") < 5 {
		t.Errorf("the containers looked at for updates = %v", out["checked"])
	}

	// whatever the scans have or have not found by now, each finding is an image's or a host's
	grave, _ := audited(t, "audit_vulnerabilities", nil)
	for key, finding := range grave {
		if kind := acctest.Str(finding["kind"]); (kind != "image" && kind != "host") || acctest.Str(finding["detail"]) == "" {
			t.Errorf("%s = %v", key, finding)
		}
	}
}

// A container given the run of its host is found for each thing it was given, and one made from an image by its id for that.
func TestAuditsOfWhatAContainerWasGiven(t *testing.T) {
	const wide, nameless = "acc-audit-wide", "acc-audit-nameless"
	if world == nil {
		t.Skip(testenv.NotConfigured)
	}
	ctx := context.WithoutCancel(t.Context())
	clean := func() { _, _ = world.Docker(ctx, testenv.Alpha, "rm", "-f", wide, nameless) }
	clean()
	t.Cleanup(clean)

	if _, err := world.Docker(ctx, testenv.Alpha, "run", "-d", "--name", wide, "--privileged", "--network", "host", "--pid", "host", "--cap-add", "SYS_ADMIN", "--cap-add", "NET_RAW", "-v", "/var/run/docker.sock:/var/run/docker.sock:ro", "-v", "/:/host:ro", "--memory", "32m", "--restart", "unless-stopped", "--health-cmd", "true", world.Image, "sleep", "100000"); err != nil {
		t.Fatal(err)
	}
	id, err := world.Docker(ctx, testenv.Alpha, "image", "inspect", "-f", "{{.Id}}", world.Image)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := world.Docker(ctx, testenv.Alpha, "run", "-d", "--name", nameless, strings.TrimSpace(id), "sleep", "100000"); err != nil {
		t.Fatal(err)
	}
	alpha := map[string]any{"env": testenv.Alpha}

	given, _ := audited(t, "audit_security", alpha)
	for _, check := range []string{"privileged", "host_network", "host_pid", "docker_socket", "host_root", "added_capabilities"} {
		if given[wide+"/"+check] == nil {
			t.Errorf("%s was not found %s: %v", wide, check, given)
		}
	}
	if got := acctest.Str(given[wide+"/added_capabilities"]["detail"]); !strings.HasSuffix(got, ": SYS_ADMIN") {
		t.Errorf("its capabilities = %q, want the one that reaches outside it", got)
	}
	if got := acctest.Str(given[wide+"/docker_socket"]["detail"]); !strings.Contains(got, "read-only") {
		t.Errorf("its socket = %q", got)
	}
	for key := range given {
		if strings.HasPrefix(key, loose+"/") || strings.HasPrefix(key, webFront+"/") {
			t.Errorf("a container given nothing was found: %s", key)
		}
	}

	// and it was given a limit, a policy and a check, so the audits of those pass it over
	for tool, check := range map[string]string{"audit_limits": "no_memory_limit", "audit_restart_policy": "no_restart_policy", "audit_healthcheck": "no_health_check"} {
		if found, _ := audited(t, tool, alpha); found[wide+"/"+check] != nil || found[loose+"/"+check] == nil {
			t.Errorf("%s = %v", tool, found)
		}
	}

	tags, _ := audited(t, "audit_image_tags", alpha)
	if tags[nameless+"/image_without_name"] == nil {
		t.Errorf("the one made from an image's id was not found: %v", tags)
	}
	// the fixtures' own image is found or not by how this run names it
	last := world.Image[strings.LastIndex(world.Image, "/")+1:]
	wantLoose := ""
	switch _, tag, tagged := strings.Cut(last, ":"); {
	case strings.Contains(world.Image, "@sha256:"):
	case !tagged:
		wantLoose = "no_tag"
	case tag == "latest":
		wantLoose = "latest_tag"
	default:
	}
	if (tags[loose+"/latest_tag"] != nil || tags[loose+"/no_tag"] != nil) != (wantLoose != "") || (wantLoose != "" && tags[loose+"/"+wantLoose] == nil) {
		t.Errorf("%s runs %s and was found %v, want %q", loose, world.Image, tags, wantLoose)
	}
}

// A stack with one container down is found partly up, and is whole again once the container is started; with both down it is found not running, and
// is up again once the stack is started. What an audit found, a tool put right, and the audit finds no more.
func TestJourneyAStackFoundDownStartedAndFoundUp(t *testing.T) {
	const stack = "acc-audit-stack"
	if world == nil {
		t.Skip(testenv.NotConfigured)
	}
	ctx := context.WithoutCancel(t.Context())
	t.Cleanup(func() {
		_, _ = world.Docker(ctx, testenv.Alpha, "rm", "-f", stack+"-one-1", stack+"-two-1")
		_, _ = world.Docker(ctx, testenv.Alpha, "network", "rm", "-f", stack+"_default")
	})

	compose := "name: " + stack + "\nservices:\n  one:\n    image: " + world.Image + "\n    command: [\"sleep\", \"100000\"]\n  two:\n    image: " + world.Image + "\n    command: [\"sleep\", \"100000\"]\n"
	if err := world.Compose(ctx, testenv.Alpha, stack, compose); err != nil {
		t.Fatal(err)
	}
	only := map[string]any{"stack": stack}
	var found map[string]map[string]any
	whole := func() bool {
		var out map[string]any
		found, out = audited(t, "audit_stacks", only)

		return len(found) == 0 && acctest.Num(t, out["checked"], "checked") == 1
	}

	if !whole() {
		t.Fatalf("%s as brought up = %v", stack, found)
	}

	// one container down: found, with which; started by the name the finding gave, and found no more
	if _, err := world.Docker(ctx, testenv.Alpha, "kill", stack+"-two-1"); err != nil {
		t.Fatal(err)
	}
	if !acctest.Eventually(func() bool {
		found, _ = audited(t, "audit_stacks", only)

		return found[stack+"/partly_running"] != nil
	}) {
		t.Fatalf("%s with one container down was never found partly up: %v", stack, found)
	}
	part := found[stack+"/partly_running"]
	if part["kind"] != "stack" || part["env"] != testenv.Alpha || !strings.HasPrefix(acctest.Str(part["detail"]), "1 of its 2 containers are not running: "+stack+"-two-1 (exited)") {
		t.Errorf("partly up = %v", part)
	}
	// the same container is what the audit of what is broken finds, killed
	if broken, _ := audited(t, "audit_health", only); broken[stack+"-two-1/exited_with_error"] == nil || !strings.Contains(acctest.Str(broken[stack+"-two-1/exited_with_error"]["detail"]), "exited with code 137") {
		t.Errorf("the container that was killed = %v", broken)
	}
	if back := call(t, "container_start", map[string]any{"container": stack + "-two-1", "env": acctest.Str(part["env"])}); back["is"] != "running" {
		t.Fatalf("starting the container the finding named = %v", back)
	}
	if !acctest.Eventually(whole) {
		t.Fatalf("%s with its container started again is still found: %v", stack, found)
	}
	if broken, _ := audited(t, "audit_health", only); len(broken) != 0 {
		t.Errorf("with everything running again, broken = %v", broken)
	}

	// both down: found not running; the stack started as one, and found no more
	if _, err := world.Docker(ctx, testenv.Alpha, "kill", stack+"-one-1", stack+"-two-1"); err != nil {
		t.Fatal(err)
	}
	if !acctest.Eventually(func() bool {
		found, _ = audited(t, "audit_stacks", only)

		return found[stack+"/not_running"] != nil
	}) {
		t.Fatalf("%s with both containers down was never found not running: %v", stack, found)
	}
	if up := call(t, "stack_start", map[string]any{"stack": stack, "env": testenv.Alpha}); len(acctest.Strs(t, up["changes"], "changes")) == 0 {
		t.Fatalf("starting the stack = %v", up)
	}
	if !acctest.Eventually(whole) {
		t.Fatalf("%s started again is still found: %v", stack, found)
	}
}

// A volume and a network nothing uses are found, removed by the names the findings gave, and found no more; what is in use, and Docker's own
// networks, never are.
func TestJourneyWhatNothingUsesFoundAndRemoved(t *testing.T) {
	const volume, network = "acc-audit-volume", "acc-audit-network"
	if world == nil {
		t.Skip(testenv.NotConfigured)
	}
	ctx := context.WithoutCancel(t.Context())
	t.Cleanup(func() {
		_, _ = world.Docker(ctx, testenv.Alpha, "network", "rm", "-f", network)
		_, _ = world.Docker(ctx, testenv.Alpha, "volume", "rm", "-f", volume)
	})
	alpha := map[string]any{"env": testenv.Alpha, "limit": 1000}

	before, _ := audited(t, "audit_unused", alpha)
	if before[volume+"/unused_volume"] != nil || before[network+"/unused_network"] != nil {
		t.Fatalf("found before either was made: %v", before)
	}
	if _, err := world.Docker(ctx, testenv.Alpha, "volume", "create", volume); err != nil {
		t.Fatal(err)
	}
	if _, err := world.Docker(ctx, testenv.Alpha, "network", "create", network); err != nil {
		t.Fatal(err)
	}

	unused, out := audited(t, "audit_unused", alpha)
	spare, loop := unused[volume+"/unused_volume"], unused[network+"/unused_network"]
	if spare == nil || loop == nil || spare["kind"] != "volume" || loop["kind"] != "network" || acctest.Num(t, out["total"], "total") != len(before)+2 {
		t.Fatalf("the volume and the network nothing uses = %v and %v, of %v where there were %d", spare, loop, out["total"], len(before))
	}
	// a stopped container still holds its network, and a stack its volume
	if unused["bridge/unused_network"] != nil || unused["host/unused_network"] != nil || unused[webStack+"_default/unused_network"] != nil || unused[webStack+"_data/unused_volume"] != nil {
		t.Errorf("something in use, or Docker's own, was found unused: %v", unused)
	}

	call(t, "volume_delete", map[string]any{"env": acctest.Str(spare["env"]), "volume": acctest.Str(spare["name"]), "confirm": true})
	call(t, "network_delete", map[string]any{"env": acctest.Str(loop["env"]), "network": acctest.Str(loop["name"]), "confirm": true})

	after, _ := audited(t, "audit_unused", alpha)
	if after[volume+"/unused_volume"] != nil || after[network+"/unused_network"] != nil || len(after) != len(before) {
		t.Errorf("after both were removed = %v, where there were %d findings to begin with", after, len(before))
	}
}

// A container run with nothing said of how it is to be kept is found by the audits of that, and counted by the audit of everything; given a limit
// and a policy through the tools, it is found no more, and the counts are each one fewer.
func TestJourneyAContainerFoundWantingPutRightAndFoundNoMore(t *testing.T) {
	const name = "acc-audit-wanting"
	// on the host with nothing on it that comes and goes, so the counts are this container's doing alone
	beta := map[string]any{"env": testenv.Beta}
	started(t, testenv.Beta, name)

	counts := func() (limits, policies int) {
		rows := named(acctest.Rows(t, call(t, "audit_all", beta)["audits"], "audits"), "audit")

		return acctest.Num(t, rows["audit_limits"]["findings"], "findings"), acctest.Num(t, rows["audit_restart_policy"]["findings"], "findings")
	}
	wanting := func() (limit, policy bool) {
		unlimited, _ := audited(t, "audit_limits", beta)
		down, _ := audited(t, "audit_restart_policy", beta)

		return unlimited[name+"/no_memory_limit"] != nil, down[name+"/no_restart_policy"] != nil
	}

	limits, policies := counts()
	if limit, policy := wanting(); !limit || !policy || limits < 2 || policies < 2 {
		t.Fatalf("as run: found without a limit %v and without a policy %v, of %d and %d", limit, policy, limits, policies)
	}

	// the limit alone: one audit is satisfied and the other is not
	if set := call(t, "container_limits", map[string]any{"container": name, "env": testenv.Beta, "memory_mb": 64}); len(acctest.Strs(t, set["changes"], "changes")) != 1 {
		t.Fatalf("giving it a limit = %v", set)
	}
	if limit, policy := wanting(); limit || !policy {
		t.Errorf("with a limit: found without a limit %v and without a policy %v", limit, policy)
	}

	if set := call(t, "container_limits", map[string]any{"container": name, "env": testenv.Beta, "restart": "unless-stopped"}); len(acctest.Strs(t, set["changes"], "changes")) != 1 {
		t.Fatalf("giving it a policy = %v", set)
	}
	if limit, policy := wanting(); limit || policy {
		t.Errorf("with both: found without a limit %v and without a policy %v", limit, policy)
	}
	if nowLimits, nowPolicies := counts(); nowLimits != limits-1 || nowPolicies != policies-1 {
		t.Errorf("the counts of everything = %d and %d, want one fewer than %d and %d", nowLimits, nowPolicies, limits, policies)
	}
}

// One call says where to look: every audit with its count, the counts adding up, and each count the one the audit itself gives.
func TestJourneyFromOneAuditOfEverythingToTheFinding(t *testing.T) {
	out := call(t, "audit_all", nil)

	rows := acctest.Rows(t, out["audits"], "audits")
	order, total := make([]string, 0, len(rows)), 0
	for _, row := range rows {
		order = append(order, acctest.Str(row["audit"]))
		total += acctest.Num(t, row["findings"], "findings")
	}
	if strings.Join(order, " ") != strings.Join(audits, " ") {
		t.Errorf("the audits = %v", order)
	}
	if acctest.Num(t, out["total"], "total") != total || out["failed"] != nil {
		t.Errorf("total %v, want the rows' %d; failed %v", out["total"], total, out["failed"])
	}

	// the fixtures alone are several things broken, and the row leads to the audit that names them
	health := named(rows, "audit")["audit_health"]
	if acctest.Num(t, health["findings"], "findings") < 2 || acctest.Num(t, health["checked"], "checked") < 5 || acctest.Num(t, acctest.Object(t, health["by_check"], "by_check")["unhealthy"], "unhealthy") < 1 {
		t.Fatalf("the health row = %v", health)
	}
	found, _ := audited(t, acctest.Str(health["audit"]), nil)
	if found[webBack+"/unhealthy"] == nil {
		t.Fatalf("the audit the row names did not find %s: %v", webBack, found)
	}

	// and the finding leads to the container, by the name it gave
	sick := found[webBack+"/unhealthy"]
	back := call(t, "container_get", map[string]any{"container": acctest.Str(sick["name"]), "env": acctest.Str(sick["env"])})
	if check := acctest.Object(t, back["health"], "health"); check["status"] != "unhealthy" || check["last_output"] != "not ready" {
		t.Errorf("the container the finding names = %v", check)
	}

	// one host alone is its own counts
	one := call(t, "audit_all", map[string]any{"env": testenv.Beta})
	if hosts := named(acctest.Rows(t, one["audits"], "audits"), "audit")["audit_hosts"]; acctest.Num(t, hosts["checked"], "checked") != 1 {
		t.Errorf("one host's row = %v", hosts)
	}
}
