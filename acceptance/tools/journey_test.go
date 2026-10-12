//go:build acceptance

package tools_test

import (
	"context"
	"maps"
	"slices"
	"strings"
	"testing"

	"github.com/katbyte/dockhand-mcp/lib/testenv"
	"github.com/katbyte/go-kt/mcp/acctest"
)

// A journey is the tools chained as a session chains them: one tool's answer is what the next is asked about, every change is read back from the host
// itself rather than taken from the answer to it, and each is made twice, since a second go is where a tool that only worked on a clean start shows.

// stateOf is a container's state as the list gives it, read afresh.
func stateOf(t *testing.T, env, name string) string {
	t.Helper()

	rows := named(acctest.Rows(t, call(t, "container_list", map[string]any{"env": env, "name": name})["containers"], "containers"), "name")

	return acctest.Str(rows[name]["state"])
}

// A container is found, stopped, read while it is stopped, started and restarted, on each host: the one Dockhand reaches by its socket and the one
// behind an agent.
func TestJourneyContainerStoppedStartedAndRestarted(t *testing.T) {
	for _, host := range []string{testenv.Alpha, testenv.Beta} {
		name := started(t, host, "acc-journey-"+host)

		// found by its name with no host given, since only one host has it
		before := call(t, "container_get", map[string]any{"container": name})
		if before["env"] != host || before["state"] != "running" || acctest.Str(before["started"]) == "" {
			t.Fatalf("%s as found = %v", name, before)
		}

		for round := range 2 {
			// a dry run says what it would do and the container goes on running
			dry := call(t, "container_stop", map[string]any{"container": name, "dry_run": true})
			if !acctest.IsBool(dry["dry_run"], true) || len(acctest.Strs(t, dry["changes"], "changes")) != 1 || stateOf(t, host, name) != "running" {
				t.Fatalf("round %d on %s: a dry run of stopping = %v, and it is %s", round, host, dry, stateOf(t, host, name))
			}

			stopped := call(t, "container_stop", map[string]any{"container": name, "env": host})
			if stopped["was"] != "running" || stopped["is"] != "exited" || stateOf(t, host, name) != "exited" {
				t.Fatalf("round %d on %s: stopping = %v, and it is %s", round, host, stopped, stateOf(t, host, name))
			}
			// stopped, it is what the list calls a problem, it can still be read, and it says when it stopped
			while := call(t, "container_get", map[string]any{"container": name})
			if while["state"] != "exited" || acctest.Str(while["finished"]) == "" || !strings.Contains(acctest.Str(while["problem"]), "exited with code 137") {
				t.Errorf("round %d on %s: read while stopped = %v", round, host, while)
			}
			if log := call(t, "container_logs", map[string]any{"container": name, "tail": 5}); !strings.Contains(acctest.Str(log["log"]), "beat") || log["state"] != "exited" {
				t.Errorf("round %d on %s: its log while stopped = %v", round, host, log)
			}
			// stopping it again changes nothing, and says so
			again := call(t, "container_stop", map[string]any{"container": name})
			if len(acctest.Strs(t, again["changes"], "changes")) != 0 || !strings.Contains(acctest.Str(again["note"]), "not running") {
				t.Errorf("round %d on %s: stopping it again = %v", round, host, again)
			}

			startedAgain := call(t, "container_start", map[string]any{"container": name})
			if startedAgain["was"] != "exited" || startedAgain["is"] != "running" || stateOf(t, host, name) != "running" {
				t.Fatalf("round %d on %s: starting = %v, and it is %s", round, host, startedAgain, stateOf(t, host, name))
			}
			if again := call(t, "container_start", map[string]any{"container": name}); len(acctest.Strs(t, again["changes"], "changes")) != 0 || !strings.Contains(acctest.Str(again["note"]), "already running") {
				t.Errorf("round %d on %s: starting it again = %v", round, host, again)
			}

			// a restart is seen in when the container says it started
			was := acctest.Str(call(t, "container_get", map[string]any{"container": name})["started"])
			restarted := call(t, "container_restart", map[string]any{"container": name})
			now := call(t, "container_get", map[string]any{"container": name})
			if restarted["is"] != "running" || now["state"] != "running" || acctest.Str(now["started"]) <= was {
				t.Fatalf("round %d on %s: restarting = %v; it started %s before and %v after", round, host, restarted, was, now["started"])
			}
		}
	}
}

// A stack is stopped as one, read while it is stopped, started and restarted, and each is seen in the stack and in its containers.
func TestJourneyStackStoppedStartedAndRestarted(t *testing.T) {
	const stack = "acc-journey-stack"
	if world == nil {
		t.Skip(testenv.NotConfigured)
	}
	compose := "name: " + stack + "\nservices:\n  one:\n    image: " + world.Image + "\n    command: [\"sh\", \"-c\", \"echo one up; sleep 100000\"]\n  two:\n    image: " + world.Image + "\n    command: [\"sh\", \"-c\", \"echo two up; sleep 100000\"]\n"
	if err := world.Compose(t.Context(), testenv.Alpha, stack, compose); err != nil {
		t.Fatal(err)
	}
	// a test's own context is cancelled before its clean-ups run
	ctx := context.WithoutCancel(t.Context())
	t.Cleanup(func() {
		_, _ = world.Docker(ctx, testenv.Alpha, "rm", "-f", stack+"-one-1", stack+"-two-1")
		_, _ = world.Docker(ctx, testenv.Alpha, "network", "rm", "-f", stack+"_default")
	})

	status := func() (string, map[string]map[string]any) {
		rows := named(acctest.Rows(t, call(t, "stack_list", map[string]any{"name": stack})["stacks"], "stacks"), "stack")

		return acctest.Str(rows[stack]["status"]), named(acctest.RowsOf(rows[stack]["services"]), "service")
	}
	if is, services := status(); is != "running" || len(services) != 2 {
		t.Fatalf("%s as brought up = %s, %v", stack, is, services)
	}

	for round := range 2 {
		if dry := call(t, "stack_stop", map[string]any{"stack": stack, "dry_run": true}); !acctest.IsBool(dry["dry_run"], true) {
			t.Errorf("round %d: a dry run of stopping = %v", round, dry)
		}
		if is, _ := status(); is != "running" {
			t.Fatalf("round %d: after a dry run the stack is %s", round, is)
		}

		stopped := call(t, "stack_stop", map[string]any{"stack": stack})
		is, services := status()
		if stopped["was"] != "running" || stopped["is"] != "stopped" || is != "stopped" || services["one"]["state"] != "exited" || services["two"]["state"] != "exited" {
			t.Fatalf("round %d: stopping = %v; the stack is %s with %v", round, stopped, is, services)
		}
		if !strings.Contains(strings.Join(acctest.Strs(t, stopped["changes"], "changes"), " "), "stop completed for 2 container(s)") {
			t.Errorf("round %d: what stopping did = %v", round, stopped["changes"])
		}
		// its containers kept, their logs are still read as one
		if logs := call(t, "stack_logs", map[string]any{"stack": stack}); !strings.Contains(acctest.Str(logs["log"]), "one | one up") || !strings.Contains(acctest.Str(logs["log"]), "two | two up") {
			t.Errorf("round %d: the stack's logs while stopped = %v", round, logs)
		}
		if again := call(t, "stack_stop", map[string]any{"stack": stack}); len(acctest.Strs(t, again["changes"], "changes")) != 0 {
			t.Errorf("round %d: stopping it again = %v", round, again)
		}

		startedAgain := call(t, "stack_start", map[string]any{"stack": stack, "env": testenv.Alpha})
		if is, services = status(); startedAgain["is"] != "running" || is != "running" || services["one"]["state"] != "running" {
			t.Fatalf("round %d: starting = %v; the stack is %s with %v", round, startedAgain, is, services)
		}
		if again := call(t, "stack_start", map[string]any{"stack": stack}); len(acctest.Strs(t, again["changes"], "changes")) != 0 {
			t.Errorf("round %d: starting it again = %v", round, again)
		}

		restarted := call(t, "stack_restart", map[string]any{"stack": stack})
		if is, _ = status(); restarted["is"] != "running" || is != "running" || !strings.Contains(strings.Join(acctest.Strs(t, restarted["changes"], "changes"), " "), "restart completed for 2 container(s)") {
			t.Fatalf("round %d: restarting = %v; the stack is %s", round, restarted, is)
		}
	}

	// what is not there is refused, in words that say what is
	for tool, args := range map[string]map[string]any{
		"stack_restart":     {"stack": "nothing-by-this-name"},
		"container_restart": {"container": "nothing-by-this-name"},
	} {
		if msg := refused(t, tool, args); !strings.Contains(msg, "nothing-by-this-name") {
			t.Errorf("%s of what is not there = %q", tool, msg)
		}
	}
}

// What a container was given and what it wrote are read with their secrets hidden, and shown when they are asked for.
func TestJourneySecretsAreHiddenUntilAskedFor(t *testing.T) {
	const (
		key      = "not-a-real-key"
		password = "not-a-real-password"
	)

	got := call(t, "container_get", map[string]any{"container": webFront})
	variables := named(acctest.Rows(t, got["environment"], "environment"), "name")
	if variables["MODE"]["value"] != "demo" || variables["API_KEY"]["value"] != "***" || variables["DATABASE_URL"]["value"] != "postgres://app:***@db:5432/app" { //nolint:gosec // G101: a made-up value for the throwaway server, which is no credential of anything real
		t.Errorf("its variables = %v", variables)
	}
	if got["stack"] != webStack || got["service"] != "front" || got["restart_policy"] != "unless-stopped" || acctest.Num(t, got["memory_limit_bytes"], "memory_limit_bytes") != 64<<20 || acctest.Num(t, got["hidden"], "hidden") < 3 {
		t.Errorf("%s = %v", webFront, got)
	}

	logs := call(t, "container_logs", map[string]any{"container": webFront, "contains": "connecting", "tail": 2000})
	if logs["log"] == "" || !strings.Contains(acctest.Str(logs["log"]), "password=***") || acctest.Num(t, logs["hidden"], "hidden") != 1 {
		t.Errorf("its log = %v", logs)
	}
	together := call(t, "stack_logs", map[string]any{"stack": webStack, "contains": "connecting", "tail": 2000})

	// neither secret is anywhere in any of the three answers
	for _, answer := range []map[string]any{got, logs, together} {
		if text := flat(answer); strings.Contains(text, key) || strings.Contains(text, password) {
			t.Errorf("a secret is shown: %s", text)
		}
	}

	// asked for, they are shown, and nothing is said to be hidden
	shown := call(t, "container_get", map[string]any{"container": webFront, "reveal_secrets": true})
	if text := flat(shown); !strings.Contains(text, key) || !strings.Contains(text, password) || acctest.Num(t, shown["hidden"], "hidden") != 0 {
		t.Errorf("asked to show its secrets = %v", shown)
	}
	if shown := call(t, "container_logs", map[string]any{"container": webFront, "contains": "connecting", "tail": 2000, "reveal_secrets": true}); !strings.Contains(acctest.Str(shown["log"]), "password="+password) {
		t.Errorf("its log, asked to show its secrets = %v", shown)
	}
}

// What an unhealthy container's check printed is where to see why, and a container that exited says how.
func TestJourneyWhyAContainerIsNotRight(t *testing.T) {
	// the list says which are not right, and each is then asked about by the name the list gave
	settled := acctest.Eventually(func() bool {
		wrong := named(acctest.RowsOf(call(t, "container_list", map[string]any{"problems": true, "env": testenv.Alpha})["containers"]), "name")

		return wrong[webBack] != nil && wrong[crashed] != nil
	})
	if !settled {
		t.Fatal("the unhealthy container and the one that exited were never both listed as not right")
	}

	back := call(t, "container_get", map[string]any{"container": webBack})
	check := acctest.Object(t, back["health"], "health")
	if check["status"] != "unhealthy" || check["last_output"] != "not ready" || acctest.Num(t, check["last_exit_code"], "last_exit_code") != 1 || acctest.Num(t, check["failing_streak"], "failing_streak") < 1 {
		t.Errorf("why %s is unhealthy = %v", webBack, check)
	}

	gone := call(t, "container_get", map[string]any{"container": crashed})
	if gone["state"] != "exited" || acctest.Num(t, gone["exit_code"], "exit_code") != 3 || acctest.Str(gone["finished"]) == "" || !strings.Contains(acctest.Str(gone["problem"]), "exited with code 3") {
		t.Errorf("how %s ended = %v", crashed, gone)
	}
	if log := call(t, "container_logs", map[string]any{"container": crashed}); acctest.Str(log["log"]) == "" || !strings.HasSuffix(acctest.Str(log["log"]), "dying") || acctest.Num(t, log["lines"], "lines") != 1 {
		t.Errorf("what %s wrote before it ended = %v", crashed, log)
	}
}

// A Dockhand that managed nothing took on its two hosts through the tools, before anything else here ran: the one on its own machine by a socket, and
// the one elsewhere through the Hawser agent in front of it. On the second stage of a run they are there already, and are found so.
func TestJourneyAFreshDockhandTakesOnItsHosts(t *testing.T) {
	if world == nil {
		t.Skip(testenv.NotConfigured)
	}

	if world.Stage == testenv.StageOpen {
		for host, connection := range map[string]string{testenv.Alpha: testenv.ConnectionSocket, testenv.Beta: testenv.ConnectionAgent} {
			made := added[host]
			if made == nil {
				t.Fatalf("%s was not added through the tools on a fresh server", host)
			}
			row, found := acctest.Object(t, made["environment"], "environment"), acctest.Object(t, made["found"], "found")
			if row["env"] != host || row["connection"] != connection || !acctest.IsBool(row["online"], true) || !acctest.IsBool(found["reachable"], true) || acctest.Str(found["docker_version"]) == "" {
				t.Errorf("%s as it was added = %v", host, made)
			}
		}
		// the agent said who it was as it was added, and a socket has none
		if found := acctest.Object(t, added[testenv.Beta]["found"], "found"); found["agent"] != "agent-beta" || acctest.Str(found["agent_version"]) == "" || len(acctest.Strs(t, found["agent_can"], "agent_can")) == 0 {
			t.Errorf("the agent before beta, as it was added = %v", found)
		}
		if found := acctest.Object(t, added[testenv.Alpha]["found"], "found"); found["agent"] != nil {
			t.Errorf("alpha, reached by its socket, has an agent: %v", found)
		}
	} else if len(added) != 0 {
		t.Errorf("hosts were added again on a server that already managed them: %v", added)
	}

	// each answers when it is tried as saved, on either stage
	for _, host := range []string{testenv.Alpha, testenv.Beta} {
		tried := call(t, "env_test", map[string]any{"env": host})
		if tried["env"] != host || !acctest.IsBool(tried["reachable"], true) || (host == testenv.Beta) != (tried["agent"] == "agent-beta") {
			t.Errorf("%s tried = %v", host, tried)
		}
	}
}

// A host is tried, refused for a wrong token, added, read through, and forgotten again, and what runs on it is untouched throughout.
func TestJourneyAnAgentIsAddedAndForgotten(t *testing.T) {
	const name = "acc-journey-agent"
	if world == nil {
		t.Skip(testenv.NotConfigured)
	}
	agentHost, agentPort, err := testenv.HostPort(world.Agent)
	if err != nil {
		t.Fatal(err)
	}
	agent := func(token string, more map[string]any) map[string]any {
		args := map[string]any{"connection": testenv.ConnectionAgent, "host": agentHost, "port": agentPort, "agent_token": token}
		maps.Copy(args, more)

		return args
	}
	listed := func() map[string]map[string]any {
		return named(acctest.Rows(t, call(t, "env_list", nil)["environments"], "environments"), "env")
	}
	// a run that was cut short may have left it behind
	if listed()[name] != nil {
		call(t, "env_delete", map[string]any{"env": name, "confirm": true})
	}
	before := len(listed())

	for round := range 2 {
		// the wrong token is turned away by the agent, which is said, and nothing is saved by trying or by a refused add
		wrong := call(t, "env_test", agent("not-the-token", nil))
		if !acctest.IsBool(wrong["reachable"], false) || !strings.Contains(acctest.Str(wrong["error"]), "Invalid token") {
			t.Fatalf("round %d: the agent tried with another token = %v", round, wrong)
		}
		if msg := refused(t, "env_create", agent("not-the-token", map[string]any{"name": name})); !strings.Contains(msg, "was not saved") || !strings.Contains(msg, "Invalid token") {
			t.Errorf("round %d: adding it with another token = %q", round, msg)
		}
		if msg := refused(t, "env_create", map[string]any{"name": name, "connection": testenv.ConnectionDirect, "host": "nobody-home"}); !strings.Contains(msg, "was not saved") {
			t.Errorf("round %d: adding a host nobody is at = %q", round, msg)
		}
		if dry := call(t, "env_create", agent(world.AgentToken, map[string]any{"name": name, "dry_run": true})); !acctest.IsBool(dry["dry_run"], true) || dry["environment"] != nil {
			t.Errorf("round %d: a dry run of adding it = %v", round, dry)
		}
		if len(listed()) != before {
			t.Fatalf("round %d: %d environments after tries, refusals and a dry run, want the %d there were", round, len(listed()), before)
		}

		made := call(t, "env_create", agent(world.AgentToken, map[string]any{"name": name, "labels": []string{"journey"}}))
		row := acctest.Object(t, made["environment"], "environment")
		if row["env"] != name || row["connection"] != testenv.ConnectionAgent || row["address"] != world.Agent || !acctest.IsBool(row["online"], true) || strings.Contains(flat(made), world.AgentToken) {
			t.Fatalf("round %d: added = %v", round, made)
		}
		// a second by its name, whatever its case, is refused
		if msg := refused(t, "env_create", agent(world.AgentToken, map[string]any{"name": strings.ToUpper(name)})); !strings.Contains(msg, "already named") {
			t.Errorf("round %d: a second by the same name = %q", round, msg)
		}

		// it is listed, online, and what runs behind the agent is read through it as through beta
		if now := listed()[name]; now == nil || !acctest.IsBool(now["online"], true) || acctest.Num(t, acctest.Object(t, now["containers"], "containers")["total"], "total") != 1 {
			t.Errorf("round %d: as listed = %v", round, now)
		}
		through := named(acctest.Rows(t, call(t, "container_list", map[string]any{"env": name})["containers"], "containers"), "name")
		if through[solo] == nil || through[solo]["env"] != name {
			t.Errorf("round %d: what runs behind the agent, read through the new environment = %v", round, through)
		}
		// the one container now seen through two environments is not guessed at
		if msg := refused(t, "container_get", map[string]any{"container": solo}); !strings.Contains(msg, "more than one container") || !strings.Contains(msg, name) || !strings.Contains(msg, testenv.Beta) {
			t.Errorf("round %d: a container seen through two environments = %q", round, msg)
		}

		// described in full: how it is reached, the machine behind it, and what Dockhand does for it by itself, which for a new host is nothing yet
		got := call(t, "env_get", map[string]any{"env": name})
		machine := acctest.Object(t, got["machine"], "machine")
		if got["env"] != name || got["connection"] != testenv.ConnectionAgent || got["address"] != world.Agent || !acctest.IsBool(got["has_agent_token"], true) || !slices.Equal(acctest.Strs(t, got["labels"], "labels"), []string{"journey"}) || machine["docker_version"] == "" || machine["hostname"] == "" || acctest.Num(t, machine["cpus"], "cpus") < 1 || acctest.Num(t, machine["containers"], "containers") != 1 || got["timezone"] != "UTC" || acctest.BoolOf(acctest.Object(t, got["update_check"], "update_check")["enabled"]) || acctest.BoolOf(acctest.Object(t, got["prune_images"], "prune_images")["enabled"]) || got["note"] != nil || strings.Contains(flat(got), world.AgentToken) {
			t.Fatalf("round %d: the new environment in full = %v", round, got)
		}

		// what is kept for it changed: said first, then saved and read back, and a second go changes nothing
		edit := map[string]any{"env": name, "labels": []string{"journey", "edited"}, "public_ip": "192.0.2.10", "collect_metrics": false}
		if dry := call(t, "env_edit", map[string]any{"env": name, "labels": []string{"journey", "edited"}, "public_ip": "192.0.2.10", "collect_metrics": false, "dry_run": true}); len(acctest.Strs(t, dry["changes"], "changes")) != 3 || call(t, "env_get", map[string]any{"env": name})["public_ip"] != nil {
			t.Fatalf("round %d: a dry run of changing it = %v", round, dry)
		}
		if edited := call(t, "env_edit", edit); len(acctest.Strs(t, edited["changes"], "changes")) != 3 || edited["reachable"] != nil {
			t.Fatalf("round %d: changing it = %v", round, edited)
		}
		if now := call(t, "env_get", map[string]any{"env": name}); !slices.Equal(acctest.Strs(t, now["labels"], "labels"), []string{"journey", "edited"}) || now["public_ip"] != "192.0.2.10" || !acctest.IsBool(now["collect_metrics"], false) || !acctest.IsBool(now["collect_activity"], true) {
			t.Errorf("round %d: read back once changed = %v", round, now)
		}
		if again := call(t, "env_edit", edit); len(acctest.Strs(t, again["changes"], "changes")) != 0 {
			t.Errorf("round %d: changing it to what it is = %v", round, again)
		}
		// its public address forgotten again, which Dockhand only does when it is sent the address empty
		if forgot := call(t, "env_edit", map[string]any{"env": name, "public_ip": ""}); len(acctest.Strs(t, forgot["changes"], "changes")) != 1 || call(t, "env_get", map[string]any{"env": name})["public_ip"] != nil {
			t.Errorf("round %d: forgetting its public address = %v", round, forgot)
		}
		if msg := refused(t, "env_edit", map[string]any{"env": name, "name": strings.ToUpper(testenv.Alpha)}); !strings.Contains(msg, "another environment is named") {
			t.Errorf("round %d: renaming it to a name that is taken = %q", round, msg)
		}
		// reached on a port nobody answers: saved, tried, and said not to get through; then put right
		_, port, err := testenv.HostPort(world.Agent)
		if err != nil {
			t.Fatal(err)
		}
		if lost := call(t, "env_edit", map[string]any{"env": name, "port": port + 23}); !acctest.IsBool(lost["reachable"], false) || !strings.Contains(acctest.Str(lost["note"]), "Dockhand does not reach the host the new way") {
			t.Errorf("round %d: reaching it on a port nobody answers = %v", round, lost)
		}
		if back := call(t, "env_edit", map[string]any{"env": name, "port": port}); !acctest.IsBool(back["reachable"], true) || back["note"] != nil {
			t.Fatalf("round %d: reaching it on its own port again = %v", round, back)
		}

		// what Dockhand does for it by itself: said first, then set and read back, and a second go changes nothing
		schedules := map[string]any{"env": name, "timezone": "Europe/Berlin", "update_check": true, "update_check_cron": "0 5 * * *", "prune_images": true, "prune_images_mode": "all", "disk_warning_mode": "absolute", "disk_warning_gb": 20}
		if dry := call(t, "env_schedules", map[string]any{"env": name, "timezone": "Europe/Berlin", "update_check": true, "dry_run": true}); len(acctest.Strs(t, dry["changes"], "changes")) != 2 || dry["timezone"] != "UTC" {
			t.Fatalf("round %d: a dry run of setting its schedules = %v", round, dry)
		}
		set := call(t, "env_schedules", schedules)
		if len(acctest.Strs(t, set["changes"], "changes")) != 4 || set["timezone"] != "Europe/Berlin" || set["note"] != nil {
			t.Fatalf("round %d: setting its schedules = %v", round, set)
		}
		now := call(t, "env_get", map[string]any{"env": name})
		check, prune, warning := acctest.Object(t, now["update_check"], "update_check"), acctest.Object(t, now["prune_images"], "prune_images"), acctest.Object(t, now["disk_warning"], "disk_warning")
		if now["timezone"] != "Europe/Berlin" || !acctest.IsBool(check["enabled"], true) || check["cron"] != "0 5 * * *" || !acctest.IsBool(check["auto_update"], false) || !acctest.IsBool(prune["enabled"], true) || prune["mode"] != "all" || warning["mode"] != "absolute" || acctest.Num(t, warning["gb"], "gb") != 20 {
			t.Errorf("round %d: its schedules read back = %v", round, now)
		}
		if again := call(t, "env_schedules", schedules); len(acctest.Strs(t, again["changes"], "changes")) != 0 {
			t.Errorf("round %d: setting its schedules the same again = %v", round, again)
		}
		// a timezone there is none of is Dockhand's to refuse, and a time no scheduler reads is refused before it is asked
		if msg := refused(t, "env_schedules", map[string]any{"env": name, "timezone": "Mars/Olympus"}); !strings.Contains(msg, "Invalid timezone") {
			t.Errorf("round %d: a timezone there is none of = %q", round, msg)
		}
		if msg := refused(t, "env_schedules", map[string]any{"env": name, "update_check_cron": "every morning"}); !strings.Contains(msg, "update_check_cron: cron is") {
			t.Errorf("round %d: a check at no time = %q", round, msg)
		}

		// forgetting it says first what would go and what would not
		preview := call(t, "env_delete", map[string]any{"env": name})
		if !acctest.IsBool(preview["removed"], false) || len(acctest.Strs(t, preview["would_go"], "would_go")) != 1 || acctest.Num(t, preview["containers"], "containers") != 1 || listed()[name] == nil {
			t.Fatalf("round %d: a removal not confirmed = %v", round, preview)
		}
		gone := call(t, "env_delete", map[string]any{"env": name, "confirm": true})
		if !acctest.IsBool(gone["removed"], true) || listed()[name] != nil || len(listed()) != before {
			t.Fatalf("round %d: removed = %v, and %d environments are left", round, gone, len(listed()))
		}
		// what ran on the host still runs, and is one container again
		if got := call(t, "container_get", map[string]any{"container": solo}); got["env"] != testenv.Beta || got["state"] != "running" {
			t.Errorf("round %d: %s after its second environment was forgotten = %v", round, solo, got)
		}
		if msg := refused(t, "env_delete", map[string]any{"env": name, "confirm": true}); !strings.Contains(msg, "no environment is named") {
			t.Errorf("round %d: forgetting it again = %q", round, msg)
		}
	}
}
