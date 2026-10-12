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

// ran starts a container on a host for the length of a test and returns its name, as started does, for a test that may rename or remove it: taking it
// away at the end is then no failure when it is already gone.
func ran(t *testing.T, host, name string, other ...string) string {
	t.Helper()

	if world == nil {
		t.Skip(testenv.NotConfigured)
	}
	ctx := context.WithoutCancel(t.Context())
	names := append([]string{name}, other...)
	clean := func() { _, _ = world.Docker(ctx, host, append([]string{"rm", "-f"}, names...)...) }
	clean()
	t.Cleanup(clean)
	if _, err := world.Docker(ctx, host, "run", "-d", "--name", name, "-e", "API_KEY=not-a-real-key", world.Image, "sleep", "100000"); err != nil {
		t.Fatalf("starting %s on %s: %v", name, host, err)
	}

	return name
}

func TestContainerStats(t *testing.T) {
	// every host's running containers, each measured as the call is made
	out := call(t, "container_stats", nil)
	rows := acctest.Rows(t, out["containers"], "containers")
	byName := named(rows, "name")
	if out["failed"] != nil || acctest.Num(t, out["total"], "total") != len(rows) || byName[webFront] == nil || byName[solo] == nil || byName[crashed] != nil {
		t.Fatalf("what is running = %v", out)
	}
	front := byName[webFront]
	if front["env"] != testenv.Alpha || acctest.Decimal(t, front["memory_mb"], "memory_mb") <= 0 || acctest.Decimal(t, front["memory_limit_mb"], "memory_limit_mb") != 67.1 || acctest.Decimal(t, front["memory_percent"], "memory_percent") <= 0 || byName[solo]["env"] != testenv.Beta {
		t.Errorf("what the web stack's front is using = %v", front)
	}
	// the heaviest first
	for i := 1; i < len(rows); i++ {
		if acctest.Decimal(t, rows[i-1]["memory_mb"], "memory_mb") < acctest.Decimal(t, rows[i]["memory_mb"], "memory_mb") {
			t.Errorf("%v is listed before %v, which holds more memory", rows[i-1]["name"], rows[i]["name"])
		}
	}

	if one := call(t, "container_stats", map[string]any{"container": solo, "sort": "cpu"}); acctest.Num(t, one["total"], "total") != 1 || acctest.Rows(t, one["containers"], "containers")[0]["env"] != testenv.Beta {
		t.Errorf("one container measured = %v", one)
	}
	// a container that is not running uses nothing, and is said not to be measured
	if none := call(t, "container_stats", map[string]any{"container": crashed}); acctest.Num(t, none["total"], "total") != 0 || !strings.Contains(acctest.Str(none["note"]), "no running container is named "+crashed) {
		t.Errorf("a container that is not running = %v", none)
	}
}

func TestContainerTop(t *testing.T) {
	out := call(t, "container_top", map[string]any{"container": loose})
	processes := acctest.Rows(t, out["processes"], "processes")
	if out["env"] != testenv.Alpha || out["container"] != loose || !slices.Contains(acctest.Strs(t, out["columns"], "columns"), "PID") || len(processes) == 0 {
		t.Fatalf("the processes of %s = %v", loose, out)
	}
	if !slices.ContainsFunc(processes, func(p map[string]any) bool { return strings.Contains(flat(p), "sleep 100000") && p["PID"] == "1" }) {
		t.Errorf("no process of %s is the sleep it was started with: %v", loose, processes)
	}
	// through the agent as well
	if far := call(t, "container_top", map[string]any{"container": solo}); far["env"] != testenv.Beta || len(acctest.Rows(t, far["processes"], "processes")) == 0 {
		t.Errorf("the processes of %s = %v", solo, far)
	}
	if msg := refused(t, "container_top", map[string]any{"container": crashed}); !strings.Contains(msg, "only of a container that is running") {
		t.Errorf("the processes of a container that is not running = %q", msg)
	}
}

func TestContainerFilesAndRead(t *testing.T) {
	for host, container := range map[string]string{testenv.Alpha: webFront, testenv.Beta: solo} {
		root := call(t, "container_files", map[string]any{"container": container})
		entries := named(acctest.Rows(t, root["entries"], "entries"), "name")
		if root["env"] != host || root["path"] != "/" || entries["etc"] == nil || entries["etc"]["type"] != "directory" || entries["bin"] == nil || acctest.Num(t, root["total"], "total") != len(entries) {
			t.Fatalf("the root of %s = %v", container, root)
		}
		if etc := entries["etc"]; acctest.Str(etc["mode"]) == "" || !strings.Contains(acctest.Str(etc["owner"]), ":") || acctest.Str(etc["modified"]) == "" {
			t.Errorf("an entry of %s = %v", container, etc)
		}
		// directories come first
		if first := acctest.Rows(t, root["entries"], "entries")[0]; first["type"] != "directory" {
			t.Errorf("the first entry of %s is %v", container, first)
		}

		// a file read: the container's own name for itself, which is the start of its id
		name := call(t, "container_read", map[string]any{"container": container, "path": "etc/hostname"})
		if name["path"] != "/etc/hostname" || len(strings.TrimSpace(acctest.Str(name["content"]))) != 12 || acctest.Num(t, name["chars"], "chars") != 13 {
			t.Errorf("the hostname file of %s = %v", container, name)
		}
		for path, want := range map[string]string{"/bin/busybox": "larger than the 1 MB", "/etc": "is a directory", "/nothing-by-this-name": "has no file"} {
			if msg := refused(t, "container_read", map[string]any{"container": container, "path": path}); !strings.Contains(msg, want) {
				t.Errorf("reading %s in %s = %q, want %q", path, container, msg, want)
			}
		}
		if msg := refused(t, "container_files", map[string]any{"container": container, "path": "/nothing-by-this-name"}); !strings.Contains(msg, "could not be listed") {
			t.Errorf("listing a path %s has not = %q", container, msg)
		}
	}
	if msg := refused(t, "container_files", map[string]any{"container": crashed}); !strings.Contains(msg, "only of a container that is running") {
		t.Errorf("the files of a container that is not running = %q", msg)
	}
}

// A container is held still and let go on, given another name, has a command run in it whose mark is then found in its files, and is removed: on the
// host Dockhand reaches by its socket and on the one behind an agent.
func TestJourneyAContainerHeldRenamedRunInAndRemoved(t *testing.T) {
	for _, host := range []string{testenv.Alpha, testenv.Beta} {
		was, is := "acc-held-"+host, "acc-renamed-"+host
		name := ran(t, host, was, is)
		on := func(container string, more map[string]any) map[string]any {
			args := map[string]any{"container": container, "env": host}
			maps.Copy(args, more)

			return args
		}

		// held still: said first, then done, and a second go changes nothing
		if dry := call(t, "container_pause", on(name, map[string]any{"dry_run": true})); !acctest.IsBool(dry["dry_run"], true) || stateOf(t, host, name) != "running" {
			t.Fatalf("%s: a dry run of pausing = %v", host, dry)
		}
		paused := call(t, "container_pause", on(name, nil))
		if paused["was"] != "running" || paused["is"] != "paused" || stateOf(t, host, name) != "paused" {
			t.Fatalf("%s: pausing = %v, and it is %s", host, paused, stateOf(t, host, name))
		}
		if again := call(t, "container_pause", on(name, nil)); len(acctest.Strs(t, again["changes"], "changes")) != 0 || !strings.Contains(acctest.Str(again["note"]), "only a container that is running can be paused") {
			t.Errorf("%s: pausing it again = %v", host, again)
		}
		// held, it still counts as running: nothing can be run in it, and Docker says so
		if msg := refused(t, "container_exec", on(name, map[string]any{"command": []any{"true"}})); msg == "" {
			t.Errorf("%s: a command run in a held container was not refused", host)
		}
		let := call(t, "container_unpause", on(name, nil))
		if let["was"] != "paused" || let["is"] != "running" || stateOf(t, host, name) != "running" {
			t.Fatalf("%s: unpausing = %v", host, let)
		}
		if again := call(t, "container_unpause", on(name, nil)); len(acctest.Strs(t, again["changes"], "changes")) != 0 {
			t.Errorf("%s: unpausing it again = %v", host, again)
		}

		// renamed: said first, then done, a taken name refused, and found under the new name alone
		taken := map[string]string{testenv.Alpha: loose, testenv.Beta: solo}[host]
		if msg := refused(t, "container_rename", on(name, map[string]any{"name": taken})); !strings.Contains(msg, "is named "+taken+" already") {
			t.Errorf("%s: renaming to a name that is taken = %q", host, msg)
		}
		if dry := call(t, "container_rename", on(name, map[string]any{"name": is, "dry_run": true})); dry["is"] != was || stateOf(t, host, was) != "running" {
			t.Fatalf("%s: a dry run of renaming = %v", host, dry)
		}
		named := call(t, "container_rename", on(name, map[string]any{"name": is}))
		if named["was"] != was || named["is"] != is || stateOf(t, host, is) != "running" || stateOf(t, host, was) != "" {
			t.Fatalf("%s: renaming = %v", host, named)
		}
		name = is

		// a command run in it: what it printed with the secret hidden, and what it wrote found among its files
		ran := call(t, "container_exec", on(name, map[string]any{"command": []any{"sh", "-c", "echo marked > /tmp/acc-mark.txt; env | grep API_KEY; echo to-errors >&2"}}))
		if acctest.Num(t, ran["exit_code"], "exit_code") != 0 || ran["stdout"] != "API_KEY=***\n" || ran["stderr"] != "to-errors\n" || acctest.Num(t, ran["hidden"], "hidden") != 1 || strings.Contains(flat(ran), "not-a-real-key") {
			t.Fatalf("%s: a command run = %v", host, ran)
		}
		if shown := call(t, "container_exec", on(name, map[string]any{"command": []any{"sh", "-c", "echo $API_KEY"}, "reveal_secrets": true})); shown["stdout"] != "not-a-real-key\n" {
			t.Errorf("%s: a command run with its secrets shown = %v", host, shown)
		}
		if tmp := named2(t, call(t, "container_files", on(name, map[string]any{"path": "/tmp"}))); tmp["acc-mark.txt"] == nil || acctest.Num(t, tmp["acc-mark.txt"]["size"], "size") != 7 {
			t.Errorf("%s: /tmp after the command = %v", host, tmp)
		}
		if mark := call(t, "container_read", on(name, map[string]any{"path": "/tmp/acc-mark.txt"})); mark["content"] != "marked\n" {
			t.Errorf("%s: the file the command wrote = %v", host, mark)
		}
		// a command that fails is an answer with what it exited with; one the container has not is 127
		if failed := call(t, "container_exec", on(name, map[string]any{"command": []any{"sh", "-c", "exit 7"}, "user": "nobody", "workdir": "/tmp"})); acctest.Num(t, failed["exit_code"], "exit_code") != 7 || !strings.Contains(acctest.Str(failed["note"]), "exited with 7") {
			t.Errorf("%s: a command that failed = %v", host, failed)
		}
		if absent := call(t, "container_exec", on(name, map[string]any{"command": []any{"nothing-by-this-name"}})); acctest.Num(t, absent["exit_code"], "exit_code") != 127 {
			t.Errorf("%s: a command the container has not = %v", host, absent)
		}

		// removed: what would go said first, a running one refused unforced, then gone
		preview := call(t, "container_delete", on(name, nil))
		if acctest.BoolOf(preview["removed"]) || preview["state"] != "running" || preview["image"] != world.Image || !strings.Contains(acctest.Str(preview["note"]), "takes force as well as confirm") || stateOf(t, host, name) != "running" {
			t.Fatalf("%s: what removing it would take = %v", host, preview)
		}
		if msg := refused(t, "container_delete", on(name, map[string]any{"confirm": true})); !strings.Contains(msg, "stop it first, or give force") || stateOf(t, host, name) != "running" {
			t.Errorf("%s: removing a running container unforced = %q", host, msg)
		}
		if gone := call(t, "container_delete", on(name, map[string]any{"confirm": true, "force": true})); !acctest.BoolOf(gone["removed"]) || stateOf(t, host, name) != "" || containerOn(t, host, name) {
			t.Fatalf("%s: removing it = %v", host, gone)
		}
		if msg := refused(t, "container_delete", on(name, map[string]any{"confirm": true})); !strings.Contains(msg, name) {
			t.Errorf("%s: removing it twice = %q", host, msg)
		}
	}
}

// named2 is the entries of a directory listing by name.
func named2(t *testing.T, listing map[string]any) map[string]map[string]any {
	t.Helper()

	return named(acctest.Rows(t, listing["entries"], "entries"), "name")
}

// The containers of a host that are not running are listed and then removed together, and the ones that run are left. It is the second host's that
// are pruned: the first has a container that exited on purpose, which other tests read.
func TestJourneyStoppedContainersListedAndPruned(t *testing.T) {
	if world == nil {
		t.Skip(testenv.NotConfigured)
	}
	const (
		host    = testenv.Beta
		exited  = "acc-pruned-exited"
		created = "acc-pruned-created"
	)
	ctx := context.WithoutCancel(t.Context())
	clean := func() { _, _ = world.Docker(ctx, host, "rm", "-f", exited, created) }
	clean()
	t.Cleanup(clean)

	for round := range 2 {
		if _, err := world.Docker(ctx, host, "run", "--name", exited, world.Image, "true"); err != nil {
			t.Fatal(err)
		}
		if _, err := world.Docker(ctx, host, "create", "--name", created, world.Image, "true"); err != nil {
			t.Fatal(err)
		}

		// what would go: the one that ran and ended and the one never started, and not the one that runs
		preview := call(t, "container_prune", map[string]any{"env": host})
		would := strings.Join(acctest.Strs(t, preview["would_go"], "would_go"), "|")
		if would != created+" (created)|"+exited+" (exited)" || len(acctest.Strs(t, preview["removed"], "removed")) != 0 || !containerOn(t, host, exited) || !containerOn(t, host, created) {
			t.Fatalf("round %d: what a prune would take = %v", round, preview)
		}
		pruned := call(t, "container_prune", map[string]any{"env": host, "confirm": true})
		if got := strings.Join(acctest.Strs(t, pruned["removed"], "removed"), "|"); got != created+"|"+exited || pruned["would_go"] != nil || containerOn(t, host, exited) || containerOn(t, host, created) || stateOf(t, host, solo) != "running" {
			t.Fatalf("round %d: a prune = %v", round, pruned)
		}
		if again := call(t, "container_prune", map[string]any{"env": host, "confirm": true}); !strings.Contains(acctest.Str(again["note"]), "nothing to remove") {
			t.Errorf("round %d: pruning again = %v", round, again)
		}
	}

	if msg := refused(t, "container_prune", map[string]any{"confirm": true}); !strings.Contains(msg, "env is required") {
		t.Errorf("a prune of no host = %q", msg)
	}
}
