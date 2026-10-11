//go:build integration

package acceptance

import (
	"context"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/katbyte/dockhand-mcp/lib/testenv"
	"github.com/katbyte/go-kt/mcp/acctest"
)

// behindImage is a tag the suite's own image is given on a host, so that a container made from it runs another image than the registry has under the
// name: a container that is behind its tag.
const behindImage = "mirror.gcr.io/library/busybox:1.35"

// stopsAtOnce is a command that runs until it is told to stop and then does, where a plain sleep keeps Docker waiting ten seconds before it kills it.
var stopsAtOnce = []any{"sh", "-c", "trap 'exit 0' TERM; while :; do sleep 1; done"}

// asDockerHasIt is something of a container read from the host itself, by a docker inspect format, and not through Dockhand.
func asDockerHasIt(t *testing.T, host, container, format string) string {
	t.Helper()

	out, err := world.Docker(context.WithoutCancel(t.Context()), host, "inspect", "--format", format, container)
	if err != nil {
		t.Fatalf("inspecting %s on %s: %v", container, host, err)
	}

	return strings.TrimSpace(out)
}

// without takes containers, volumes and image names off a host from outside Dockhand, now and when the test ends: what a run that was cut short may
// have left, and what this one makes.
func without(t *testing.T, host string, containers, volumes, images []string) {
	t.Helper()

	if world == nil {
		t.Skip(testenv.NotConfigured)
	}
	ctx := context.WithoutCancel(t.Context())
	clean := func() {
		_, _ = world.Docker(ctx, host, append([]string{"rm", "-f"}, containers...)...)
		if len(volumes) > 0 {
			_, _ = world.Docker(ctx, host, append([]string{"volume", "rm", "-f"}, volumes...)...)
		}
		if len(images) > 0 {
			_, _ = world.Docker(ctx, host, append([]string{"rmi", "-f"}, images...)...)
		}
	}
	clean()
	t.Cleanup(clean)
}

func TestContainerSizes(t *testing.T) {
	out := call(t, "container_sizes", nil)
	rows := named(acctest.Rows(t, out["containers"], "containers"), "name")
	if out["failed"] != nil || acctest.Num(t, out["total"], "total") < 5 || rows[webFront] == nil || rows[solo] == nil || rows[crashed] == nil {
		t.Fatalf("what the containers take = %v", out)
	}
	// running or not, each takes at least its image
	if front, stopped := rows[webFront], rows[crashed]; front["env"] != testenv.Alpha || acctest.Decimal(t, front["total_mb"], "total_mb") <= 0 || stopped["state"] != "exited" || acctest.Decimal(t, stopped["total_mb"], "total_mb") <= 0 || rows[solo]["env"] != testenv.Beta {
		t.Errorf("the sizes = %v and %v", front, stopped)
	}
	if one := call(t, "container_sizes", map[string]any{"env": testenv.Beta, "limit": 1}); len(acctest.Rows(t, one["containers"], "containers")) != 1 {
		t.Errorf("one host's sizes, limited to one = %v", one)
	}
}

// A container is made with everything it can be given, written out as compose, limited, changed and removed, each read back from the host itself: on
// the host Dockhand reaches by its socket and on the one behind an agent.
func TestJourneyAContainerMadeLimitedChangedAndRemoved(t *testing.T) {
	for _, host := range []string{testenv.Alpha, testenv.Beta} {
		name, spare, volume := "acc-made-"+host, "acc-made-spare-"+host, "acc-made-data-"+host
		without(t, host, []string{name, spare}, []string{volume}, nil)
		on := func(more map[string]any) map[string]any {
			args := map[string]any{"container": name, "env": host}
			maps.Copy(args, more)

			return args
		}
		spec := map[string]any{"env": host, "name": name, "image": world.Image, "command": stopsAtOnce, "env_vars": map[string]any{"MODE": "demo", "API_KEY": "not-a-real-key"}, "ports": []any{"127.0.0.1:18190:80", "18191:81/udp"}, "volumes": []any{volume + ":/data"}, "restart": "on-failure:3", "labels": map[string]any{"acc.made": "yes"}, "workdir": "/data"}

		// said first, and nothing made
		dry := maps.Clone(spec)
		dry["dry_run"] = true
		if said := call(t, "container_create", dry); !acctest.IsBool(said["dry_run"], true) || len(acctest.Strs(t, said["changes"], "changes")) != 1 || containerOn(t, host, name) {
			t.Fatalf("%s: a dry run of making a container = %v", host, said)
		}

		// made, started, and as the host has it
		made := call(t, "container_create", spec)
		if made["state"] != "running" || made["env"] != host || !strings.HasPrefix(asDockerHasIt(t, host, name, "{{.Id}}"), acctest.Str(made["id"])) || made["note"] != nil {
			t.Fatalf("%s: making a container = %v", host, made)
		}
		if got := asDockerHasIt(t, host, name, `{{.State.Status}}|{{json .HostConfig.PortBindings}}|{{json .HostConfig.Binds}}|{{json .HostConfig.RestartPolicy}}|{{index .Config.Labels "acc.made"}}|{{.Config.WorkingDir}}`); got != `running|{"80/tcp":[{"HostIp":"127.0.0.1","HostPort":"18190"}],"81/udp":[{"HostIp":"","HostPort":"18191"}]}|["`+volume+`:/data"]|{"Name":"on-failure","MaximumRetryCount":3}|yes|/data` {
			t.Errorf("%s: the container as the host has it = %s", host, got)
		}
		if given := givenTo(t, host, name); !slices.Contains(given, "MODE=demo") || !slices.Contains(given, "API_KEY=not-a-real-key") {
			t.Errorf("%s: what the container was given = %v", host, given)
		}
		// a name that is taken and a network the host has not are refused, with nothing made
		if msg := refused(t, "container_create", spec); !strings.Contains(msg, "has a container named "+name+" already") {
			t.Errorf("%s: making it twice = %q", host, msg)
		}
		if msg := refused(t, "container_create", map[string]any{"env": host, "name": spare, "image": world.Image, "networks": []any{"acc-no-such-network"}}); !strings.Contains(msg, "has no network named acc-no-such-network") || containerOn(t, host, spare) {
			t.Errorf("%s: making one on a network there is none of = %q", host, msg)
		}
		// one whose port is taken is made and cannot be started, which is said, and it is left as it was made
		if msg := refused(t, "container_create", map[string]any{"env": host, "name": spare, "image": world.Image, "command": stopsAtOnce, "ports": []any{"127.0.0.1:18190:80"}}); !strings.Contains(msg, spare+" was made on "+host+" and could not be started") || stateOf(t, host, spare) != "created" {
			t.Errorf("%s: making one whose port is taken = %q, and it is %q", host, msg, stateOf(t, host, spare))
		}
		if gone := call(t, "container_delete", map[string]any{"container": spare, "env": host, "confirm": true}); !acctest.BoolOf(gone["removed"]) {
			t.Fatalf("%s: removing the one that could not be started = %v", host, gone)
		}
		// made and not started
		if idle := call(t, "container_create", map[string]any{"env": host, "name": spare, "image": world.Image, "command": stopsAtOnce, "start": false}); idle["state"] != "created" || asDockerHasIt(t, host, spare, "{{.State.Status}}") != "created" {
			t.Errorf("%s: making one and not starting it = %v", host, idle)
		}

		// written out as compose, with its secret hidden unless asked for
		written := call(t, "container_compose", on(nil))
		if text := acctest.Str(written["compose"]); written["service"] != name || !strings.Contains(text, "image: "+world.Image) || !strings.Contains(text, "MODE=demo") || !strings.Contains(text, "API_KEY=***") || strings.Contains(flat(written), "not-a-real-key") || !strings.Contains(text, "on-failure:3") || !strings.Contains(text, volume+":/data") || acctest.Num(t, written["hidden"], "hidden") != 1 {
			t.Errorf("%s: the container as compose = %v", host, written)
		}
		if shown := call(t, "container_compose", on(map[string]any{"reveal_secrets": true})); !strings.Contains(acctest.Str(shown["compose"]), "API_KEY=not-a-real-key") {
			t.Errorf("%s: the container as compose, secrets and all = %v", host, shown)
		}

		// limited: said first, then set on the running container, and a second go changes nothing
		if said := call(t, "container_limits", on(map[string]any{"memory_mb": 128, "cpus": 0.5, "restart": "unless-stopped", "dry_run": true})); len(acctest.Strs(t, said["changes"], "changes")) != 3 || asDockerHasIt(t, host, name, "{{.HostConfig.Memory}}") != "0" {
			t.Fatalf("%s: a dry run of limiting = %v", host, said)
		}
		limited := call(t, "container_limits", on(map[string]any{"memory_mb": 128, "cpus": 0.5, "restart": "unless-stopped"}))
		if acctest.Decimal(t, limited["memory_mb"], "memory_mb") != 128 || acctest.Decimal(t, limited["cpus"], "cpus") != 0.5 || limited["restart"] != "unless-stopped" || asDockerHasIt(t, host, name, "{{.HostConfig.Memory}} {{.HostConfig.MemorySwap}} {{.HostConfig.NanoCpus}} {{.HostConfig.RestartPolicy.Name}} {{.State.Status}}") != "128000000 128000000 500000000 unless-stopped running" {
			t.Fatalf("%s: limiting = %v", host, limited)
		}
		if again := call(t, "container_limits", on(map[string]any{"memory_mb": 128, "cpus": 0.5, "restart": "unless-stopped"})); len(acctest.Strs(t, again["changes"], "changes")) != 0 {
			t.Errorf("%s: limiting it the same again = %v", host, again)
		}
		if msg := refused(t, "container_limits", on(map[string]any{"memory_mb": 2})); !strings.Contains(msg, "memory_mb is 2") {
			t.Errorf("%s: a limit below what Docker takes = %q", host, msg)
		}

		// changed: said first, then made again with the change, with everything not named kept
		change := map[string]any{"env_vars": map[string]any{"MODE": "live", "ADDED": "1"}, "remove_env_vars": []any{"API_KEY"}, "labels": map[string]any{"acc.edited": "yes"}, "ports": []any{"127.0.0.1:18192:80"}}
		before := asDockerHasIt(t, host, name, "{{.Id}}")
		dry = on(change)
		dry["dry_run"] = true
		if said := call(t, "container_edit", dry); len(acctest.Strs(t, said["changes"], "changes")) != 6 || asDockerHasIt(t, host, name, "{{.Id}}") != before {
			t.Fatalf("%s: a dry run of changing = %v", host, said)
		}
		edited := call(t, "container_edit", on(change))
		after := asDockerHasIt(t, host, name, "{{.Id}}")
		if edited["was"] != "running" || edited["is"] != "running" || after == before || !strings.HasPrefix(after, acctest.Str(edited["id"])) || strings.Contains(flat(edited), "not-a-real-key") {
			t.Fatalf("%s: changing = %v", host, edited)
		}
		if given := givenTo(t, host, name); !slices.Contains(given, "MODE=live") || !slices.Contains(given, "ADDED=1") || slices.ContainsFunc(given, func(v string) bool { return strings.HasPrefix(v, "API_KEY=") }) {
			t.Errorf("%s: what the changed container was given = %v", host, given)
		}
		// its mount, limits, restart policy and the label it had are what it was not asked to change
		if got := asDockerHasIt(t, host, name, `{{json .HostConfig.PortBindings}}|{{json .HostConfig.Binds}}|{{.HostConfig.Memory}}|{{.HostConfig.RestartPolicy.Name}}|{{index .Config.Labels "acc.made"}}{{index .Config.Labels "acc.edited"}}`); got != `{"80/tcp":[{"HostIp":"127.0.0.1","HostPort":"18192"}]}|["`+volume+`:/data"]|128000000|unless-stopped|yesyes` {
			t.Errorf("%s: the changed container as the host has it = %s", host, got)
		}
		if again := call(t, "container_edit", on(map[string]any{"env_vars": map[string]any{"MODE": "live"}, "labels": map[string]any{"acc.edited": "yes"}})); len(acctest.Strs(t, again["changes"], "changes")) != 0 || asDockerHasIt(t, host, name, "{{.Id}}") != after {
			t.Errorf("%s: changing it to what it is = %v", host, again)
		}
		if msg := refused(t, "container_edit", on(nil)); !strings.Contains(msg, "nothing to change was given") {
			t.Errorf("%s: a change of nothing = %q", host, msg)
		}

		for _, container := range []string{name, spare} {
			if gone := call(t, "container_delete", map[string]any{"container": container, "env": host, "confirm": true, "force": true}); !acctest.BoolOf(gone["removed"]) || containerOn(t, host, container) {
				t.Fatalf("%s: removing %s = %v", host, container, gone)
			}
		}
	}
}

// A container that runs an older image than the registry has under its tag is found by a check, has the note of it cleared and found again, is
// updated, and is noted no more; and Dockhand is set to update it by itself, and told to stop. On the host Dockhand reaches by its socket and on the
// one behind an agent.
func TestJourneyAContainerBehindItsTagFoundUpdatedAndScheduled(t *testing.T) {
	for _, host := range []string{testenv.Alpha, testenv.Beta} {
		name := "acc-behind-" + host
		without(t, host, []string{name}, nil, []string{behindImage})
		on := func(more map[string]any) map[string]any {
			args := map[string]any{"container": name, "env": host}
			maps.Copy(args, more)

			return args
		}
		waiting := func(check bool) map[string]any {
			return named(acctest.Rows(t, call(t, "container_updates", map[string]any{"env": host, "check": check})["updates"], "updates"), "container")[name]
		}

		// the suite's own image under another tag's name: what the registry has under that name is another image
		if _, err := world.Docker(context.WithoutCancel(t.Context()), host, "tag", world.Image, behindImage); err != nil {
			t.Fatal(err)
		}
		old := asDockerHasIt(t, host, behindImage, "{{.Id}}")
		if made := call(t, "container_create", map[string]any{"env": host, "name": name, "image": behindImage, "command": stopsAtOnce, "env_vars": map[string]any{"MODE": "demo"}, "labels": map[string]any{"acc.behind": "yes"}, "restart": "unless-stopped"}); made["state"] != "running" {
			t.Fatalf("%s: making the container = %v", host, made)
		}

		// nothing is noted until the registries are asked, and then it is
		if noted := waiting(false); noted != nil {
			t.Fatalf("%s: noted before any check = %v", host, noted)
		}
		found := waiting(true)
		if found == nil || !acctest.IsBool(found["newer_image"], true) || found["image"] != behindImage || found["env"] != host || found["checked_at"] == "" {
			t.Fatalf("%s: what a check found = %v", host, found)
		}

		// the note forgotten: said first, then done, and a fresh check finds it again
		if dry := call(t, "container_updates_clear", map[string]any{"env": host, "container": name, "dry_run": true}); !slices.Contains(acctest.Strs(t, dry["cleared"], "cleared"), name) || waiting(false) == nil {
			t.Fatalf("%s: a dry run of clearing = %v", host, dry)
		}
		if cleared := call(t, "container_updates_clear", map[string]any{"env": host, "container": name}); !slices.Contains(acctest.Strs(t, cleared["cleared"], "cleared"), name) || waiting(false) != nil {
			t.Fatalf("%s: clearing = %v", host, cleared)
		}
		if again := call(t, "container_updates_clear", map[string]any{"env": host, "container": name}); len(acctest.Strs(t, again["cleared"], "cleared")) != 0 {
			t.Errorf("%s: clearing it twice = %v", host, again)
		}
		if waiting(true) == nil {
			t.Fatalf("%s: a second check did not find it again", host)
		}

		// updated: said first, then pulled and made again from the newer image, with what it was made with kept
		before := asDockerHasIt(t, host, name, "{{.Id}}")
		if dry := call(t, "container_update", on(map[string]any{"dry_run": true})); len(acctest.Strs(t, dry["changes"], "changes")) != 2 || asDockerHasIt(t, host, name, "{{.Id}}") != before {
			t.Fatalf("%s: a dry run of updating = %v", host, dry)
		}
		updated := call(t, "container_update", on(map[string]any{"wait": 120}))
		newer := asDockerHasIt(t, host, behindImage, "{{.Id}}")
		if !acctest.IsBool(updated["updated"], true) || !acctest.IsBool(updated["running"], false) || updated["state"] != "running" || updated["was"] == updated["is"] || newer == old || !strings.HasPrefix(newer, "sha256:"+acctest.Str(updated["is"])) || asDockerHasIt(t, host, name, "{{.Image}}") != newer {
			t.Fatalf("%s: updating = %v (the image under the tag was %s and is %s)", host, updated, old, newer)
		}
		if got := asDockerHasIt(t, host, name, `{{.State.Status}}|{{.HostConfig.RestartPolicy.Name}}|{{index .Config.Labels "acc.behind"}}`); got != "running|unless-stopped|yes" || !slices.Contains(givenTo(t, host, name), "MODE=demo") || asDockerHasIt(t, host, name, "{{.Id}}") == before {
			t.Errorf("%s: the updated container as the host has it = %s", host, got)
		}
		// what was noted went with the container that is gone, and a fresh check has nothing to say of the new one
		if noted := waiting(false); noted != nil {
			t.Errorf("%s: still noted after the update = %v", host, noted)
		}
		if noted := waiting(true); noted != nil {
			t.Errorf("%s: found behind again after the update = %v", host, noted)
		}

		// with nothing newer nothing is touched, unless it is asked for
		before = asDockerHasIt(t, host, name, "{{.Id}}")
		if same := call(t, "container_update", on(map[string]any{"wait": 120})); !acctest.IsBool(same["updated"], false) || !strings.Contains(acctest.Str(same["note"]), "the registry has nothing newer") || asDockerHasIt(t, host, name, "{{.Id}}") != before {
			t.Errorf("%s: updating with nothing newer = %v", host, same)
		}
		if remade := call(t, "container_update", on(map[string]any{"wait": 120, "recreate": true})); !acctest.IsBool(remade["updated"], false) || remade["state"] != "running" || asDockerHasIt(t, host, name, "{{.Id}}") == before {
			t.Errorf("%s: making it again all the same = %v", host, remade)
		}

		// Dockhand set to update it by itself: said first, set, listed, changed, and stopped
		schedule := func() map[string]any {
			return named(acctest.Rows(t, call(t, "container_autoupdate", map[string]any{"env": host})["schedules"], "schedules"), "container")[name]
		}
		if dry := call(t, "container_autoupdate_set", on(map[string]any{"enabled": true, "dry_run": true})); !acctest.IsBool(dry["enabled"], false) || schedule() != nil {
			t.Fatalf("%s: a dry run of scheduling = %v", host, dry)
		}
		set := call(t, "container_autoupdate_set", on(map[string]any{"enabled": true}))
		if !acctest.IsBool(set["enabled"], true) || set["cron"] != "0 3 * * *" || set["kind"] != "daily" || set["hold_back"] != "never" {
			t.Fatalf("%s: scheduling = %v", host, set)
		}
		if listed := schedule(); listed == nil || listed["cron"] != "0 3 * * *" || listed["env"] != host || listed["gone"] != nil {
			t.Fatalf("%s: the schedule as listed = %v", host, listed)
		}
		if again := call(t, "container_autoupdate_set", on(map[string]any{"enabled": true})); len(acctest.Strs(t, again["changes"], "changes")) != 0 {
			t.Errorf("%s: scheduling it the same again = %v", host, again)
		}
		changed := call(t, "container_autoupdate_set", on(map[string]any{"enabled": true, "cron": "30 4 * * 0", "hold_back": "critical_high"}))
		if changed["cron"] != "30 4 * * 0" || changed["kind"] != "weekly" || changed["hold_back"] != "critical_high" {
			t.Errorf("%s: a changed schedule = %v", host, changed)
		}
		if one := acctest.Rows(t, call(t, "container_autoupdate", on(nil))["schedules"], "schedules"); len(one) != 1 || one[0]["cron"] != "30 4 * * 0" || one[0]["hold_back"] != "critical_high" {
			t.Errorf("%s: the one container's schedule = %v", host, one)
		}
		if msg := refused(t, "container_autoupdate_set", on(map[string]any{"enabled": true, "cron": "whenever"})); !strings.Contains(msg, "it is five fields") {
			t.Errorf("%s: a schedule that is no cron = %q", host, msg)
		}
		if off := call(t, "container_autoupdate_set", on(map[string]any{"enabled": false})); !acctest.IsBool(off["enabled"], false) || schedule() != nil {
			t.Fatalf("%s: stopping the schedule = %v", host, off)
		}
		if again := call(t, "container_autoupdate_set", on(map[string]any{"enabled": false})); len(acctest.Strs(t, again["changes"], "changes")) != 0 {
			t.Errorf("%s: stopping it twice = %v", host, again)
		}

		if gone := call(t, "container_delete", on(map[string]any{"confirm": true, "force": true})); !acctest.BoolOf(gone["removed"]) {
			t.Fatalf("%s: removing it = %v", host, gone)
		}
	}
}

// Files are written into a container, sent in, given an owner, moved, fetched out and removed, each read back from the container itself; and a
// stopped container is mended by a file written into it. On the host Dockhand reaches by its socket and on the one behind an agent.
func TestJourneyFilesWrittenSentMovedFetchedAndRemoved(t *testing.T) {
	here := t.TempDir()
	one, two := filepath.Join(here, "one.txt"), filepath.Join(here, "two.bin")
	for file, holds := range map[string]string{one: "one\n", two: "\x00\x01two"} {
		if err := os.WriteFile(file, []byte(holds), 0o600); err != nil {
			t.Fatal(err)
		}
	}

	for _, host := range []string{testenv.Alpha, testenv.Beta} {
		name := ran(t, host, "acc-files-"+host)
		inside := func(script string) string {
			out, err := world.Docker(context.WithoutCancel(t.Context()), host, "exec", name, "sh", "-c", script)
			if err != nil {
				t.Fatalf("%s: running %q in %s: %v", host, script, name, err)
			}

			return out
		}
		on := func(more map[string]any) map[string]any {
			args := map[string]any{"container": name, "env": host}
			maps.Copy(args, more)

			return args
		}

		// a directory made, and made again to no change
		if dry := call(t, "container_file_mkdir", on(map[string]any{"path": "/srv/acc/conf", "dry_run": true})); !acctest.IsBool(dry["created"], false) || strings.TrimSpace(inside("ls -d /srv/acc 2>/dev/null || echo none")) != "none" {
			t.Fatalf("%s: a dry run of making a directory = %v", host, dry)
		}
		if made := call(t, "container_file_mkdir", on(map[string]any{"path": "/srv/acc/conf"})); !acctest.IsBool(made["created"], true) || strings.TrimSpace(inside("ls -d /srv/acc/conf")) != "/srv/acc/conf" {
			t.Fatalf("%s: making a directory = %v", host, made)
		}
		if again := call(t, "container_file_mkdir", on(map[string]any{"path": "/srv/acc/conf"})); !acctest.IsBool(again["created"], false) || !strings.Contains(acctest.Str(again["note"]), "there already") {
			t.Errorf("%s: making it twice = %v", host, again)
		}

		// a file written: said first, then there; not over itself unasked, and never over a directory
		const file, text = "/srv/acc/conf/app.conf", "mode = demo\npassword = not-a-real-password\n"
		if dry := call(t, "container_file_write", on(map[string]any{"path": file, "content": text, "dry_run": true})); !acctest.IsBool(dry["created"], false) || strings.TrimSpace(inside("ls "+file+" 2>/dev/null || echo none")) != "none" {
			t.Fatalf("%s: a dry run of writing = %v", host, dry)
		}
		if wrote := call(t, "container_file_write", on(map[string]any{"path": file, "content": text})); !acctest.IsBool(wrote["created"], true) || wrote["note"] != nil || inside("cat "+file) != text {
			t.Fatalf("%s: writing a file = %v", host, wrote)
		}
		if msg := refused(t, "container_file_write", on(map[string]any{"path": file, "content": "other"})); !strings.Contains(msg, "already: overwrite replaces it") || inside("cat "+file) != text {
			t.Errorf("%s: writing over a file unasked = %q", host, msg)
		}
		if msg := refused(t, "container_file_write", on(map[string]any{"path": "/srv/acc/conf", "content": "x", "overwrite": true})); !strings.Contains(msg, "is a directory") || inside("cat "+file) != text {
			t.Errorf("%s: writing over a directory = %q", host, msg)
		}
		if msg := refused(t, "container_file_write", on(map[string]any{"path": "/srv/acc/absent/x.conf", "content": "x"})); !strings.Contains(msg, "has no directory /srv/acc/absent") {
			t.Errorf("%s: writing where no directory is = %q", host, msg)
		}

		// given an owner and a mode, which a file written over loses again
		owned := call(t, "container_file_perms", on(map[string]any{"path": file, "mode": "640", "owner": "nobody"}))
		if owned["mode"] != "rw-r-----" || !strings.HasPrefix(acctest.Str(owned["owner"]), "nobody:") || strings.Fields(inside("ls -l " + file))[2] != "nobody" {
			t.Fatalf("%s: giving the file an owner and a mode = %v", host, owned)
		}
		if msg := refused(t, "container_file_perms", on(map[string]any{"path": file, "owner": "acc-no-such-user"})); !strings.Contains(msg, "was not given to acc-no-such-user") {
			t.Errorf("%s: an owner the container has not = %q", host, msg)
		}
		over := call(t, "container_file_write", on(map[string]any{"path": file, "content": "mode = live\n", "overwrite": true}))
		if !acctest.IsBool(over["created"], false) || !strings.Contains(acctest.Str(over["note"]), "root's and readable by everyone now") || inside("cat "+file) != "mode = live\n" || strings.Fields(inside("ls -l " + file))[2] != "root" {
			t.Fatalf("%s: writing over the file = %v, and it is %s", host, over, inside("ls -l "+file))
		}

		// moved: not onto a directory, and then under its new name alone
		if msg := refused(t, "container_file_move", on(map[string]any{"from": file, "to": "/srv/acc"})); !strings.Contains(msg, "/srv/acc in "+name+" is a directory") {
			t.Errorf("%s: moving onto a directory = %q", host, msg)
		}
		if moved := call(t, "container_file_move", on(map[string]any{"from": file, "to": "/srv/acc/conf/main.conf"})); !acctest.IsBool(moved["moved"], true) || strings.TrimSpace(inside("ls /srv/acc/conf")) != "main.conf" {
			t.Fatalf("%s: moving the file = %v", host, moved)
		}

		// files sent in from this machine, one of them not text, and not over what is there unasked
		sent := call(t, "container_file_upload", on(map[string]any{"files": []any{one, two}, "to": "/srv/acc/conf", "owner": "nobody"}))
		if got := acctest.Strs(t, sent["uploaded"], "uploaded"); !slices.Equal(acctest.Sorted(got), []string{"one.txt", "two.bin"}) || acctest.Num(t, sent["bytes"], "bytes") != 9 || sent["errors"] != nil || inside("cat /srv/acc/conf/one.txt") != "one\n" || inside("cat /srv/acc/conf/two.bin") != "\x00\x01two" || strings.Fields(inside("ls -l /srv/acc/conf/one.txt"))[2] != "nobody" {
			t.Fatalf("%s: sending files in = %v", host, sent)
		}
		if msg := refused(t, "container_file_upload", on(map[string]any{"files": []any{one}, "to": "/srv/acc/conf"})); !strings.Contains(msg, "already: overwrite replaces it") {
			t.Errorf("%s: sending over a file unasked = %q", host, msg)
		}
		if listed := named2(t, call(t, "container_files", on(map[string]any{"path": "/srv/acc/conf"}))); len(listed) != 3 || acctest.Num(t, listed["two.bin"]["size"], "size") != 5 {
			t.Errorf("%s: the directory as listed = %v", host, listed)
		}

		// fetched out: a file as itself, a directory as an archive, and never over what is here
		to := filepath.Join(here, host)
		if err := os.Mkdir(to, 0o700); err != nil {
			t.Fatal(err)
		}
		got := call(t, "container_file_download", on(map[string]any{"path": "/srv/acc/conf/two.bin", "to": to}))
		if fetched, err := os.ReadFile(filepath.Join(to, "two.bin")); err != nil || string(fetched) != "\x00\x01two" || got["kind"] != "file" || got["saved"] != filepath.Join(to, "two.bin") || acctest.Num(t, got["bytes"], "bytes") != 5 { //nolint:gosec // G304: the path is the test's own, under its own temporary folder
			t.Fatalf("%s: fetching a file = %v, read back as %q, %v", host, got, fetched, err)
		}
		if msg := refused(t, "container_file_download", on(map[string]any{"path": "/srv/acc/conf/two.bin", "to": to})); !strings.Contains(msg, "it could not be written here") {
			t.Errorf("%s: fetching over a file that is here = %q", host, msg)
		}
		whole := call(t, "container_file_download", on(map[string]any{"path": "/srv/acc", "to": filepath.Join(to, "acc.tar.gz")}))
		if info, err := os.Stat(filepath.Join(to, "acc.tar.gz")); err != nil || info.Size() == 0 || whole["kind"] != "directory" || acctest.Num(t, whole["bytes"], "bytes") != int(info.Size()) {
			t.Errorf("%s: fetching a directory = %v, %v", host, whole, err)
		}

		// removed: what would go said first, the root's own directories refused, then gone
		preview := call(t, "container_file_delete", on(map[string]any{"path": "/srv/acc/conf"}))
		if acctest.BoolOf(preview["removed"]) || preview["kind"] != "directory" || acctest.Num(t, preview["entries"], "entries") != 3 || strings.TrimSpace(inside("ls /srv/acc")) != "conf" {
			t.Fatalf("%s: what removing would take = %v", host, preview)
		}
		if msg := refused(t, "container_file_delete", on(map[string]any{"path": "/srv", "confirm": true})); !strings.Contains(msg, "/srv is a directory at the root of "+name) || strings.TrimSpace(inside("ls /srv")) != "acc" {
			t.Errorf("%s: removing a directory at the root = %q", host, msg)
		}
		if gone := call(t, "container_file_delete", on(map[string]any{"path": "/srv/acc/conf", "confirm": true})); !acctest.BoolOf(gone["removed"]) || strings.TrimSpace(inside("ls /srv/acc")) != "" {
			t.Fatalf("%s: removing = %v", host, gone)
		}
		if msg := refused(t, "container_file_delete", on(map[string]any{"path": "/srv/acc/conf", "confirm": true})); !strings.Contains(msg, "has nothing at /srv/acc/conf") {
			t.Errorf("%s: removing it twice = %q", host, msg)
		}

		// stopped, a container still takes a file and gives one up, which is how one that will not start is mended
		if _, err := world.Docker(context.WithoutCancel(t.Context()), host, "kill", name); err != nil {
			t.Fatal(err)
		}
		if mended := call(t, "container_file_write", on(map[string]any{"path": "/etc/acc-fix.conf", "content": "fixed\n"})); !acctest.IsBool(mended["created"], true) || mended["note"] != nil {
			t.Fatalf("%s: writing into a stopped container = %v", host, mended)
		}
		if out := call(t, "container_file_download", on(map[string]any{"path": "/etc/acc-fix.conf", "to": to})); acctest.Num(t, out["bytes"], "bytes") != 6 {
			t.Errorf("%s: fetching from a stopped container = %v", host, out)
		}
		if msg := refused(t, "container_file_mkdir", on(map[string]any{"path": "/srv/acc/more"})); !strings.Contains(msg, "only of a container that is running") {
			t.Errorf("%s: making a directory in a stopped container = %q", host, msg)
		}
		if _, err := world.Docker(context.WithoutCancel(t.Context()), host, "start", name); err != nil {
			t.Fatal(err)
		}
		if inside("cat /etc/acc-fix.conf") != "fixed\n" {
			t.Errorf("%s: the file written while it was stopped is not there once it runs", host)
		}
	}
}

// What a container did is kept and found again once the container is gone: it is made, killed and removed, its death is read with its exit code and
// its kill with its signal, and then the whole record is cleared. On the host Dockhand reaches by its socket and on the one behind an agent.
func TestJourneyWhatAContainerDidIsKeptAndCleared(t *testing.T) {
	for _, host := range []string{testenv.Alpha, testenv.Beta} {
		name := ran(t, host, "acc-events-"+host)
		for _, did := range [][]string{{"kill", name}, {"rm", name}} {
			if _, err := world.Docker(context.WithoutCancel(t.Context()), host, did...); err != nil {
				t.Fatal(err)
			}
		}

		// Dockhand hears of each from the host a moment after it happens
		var events []map[string]any
		heard := acctest.Eventually(func() bool {
			events = acctest.Rows(t, call(t, "container_events", map[string]any{"env": host, "container": name})["events"], "events")

			return slices.ContainsFunc(events, func(e map[string]any) bool { return e["action"] == "destroy" })
		})
		did := map[string]map[string]any{}
		for _, e := range events {
			did[acctest.Str(e["action"])] = e
		}
		if !heard || did["create"] == nil || did["start"] == nil || did["die"] == nil || acctest.Num(t, did["die"]["exit_code"], "exit_code") != 137 || did["kill"] == nil || did["kill"]["signal"] != "9" || did["die"]["env"] != host || did["die"]["container"] != name || did["die"]["image"] != world.Image || events[0]["action"] != "destroy" {
			t.Fatalf("%s: what %s did = %v", host, name, events)
		}

		// narrowed to its death alone, and to a time before it was made; and the log counted
		only := call(t, "container_events", map[string]any{"env": host, "container": name, "actions": []any{"die", "oom"}, "since": "10m", "counts": true})
		if rows := acctest.Rows(t, only["events"], "events"); len(rows) != 1 || rows[0]["action"] != "die" || acctest.Num(t, only["total"], "total") != 1 || acctest.Num(t, acctest.Object(t, only["by_action"], "by_action")["die"], "die") < 1 {
			t.Errorf("%s: its death alone = %v", host, only)
		}
		if none := call(t, "container_events", map[string]any{"env": host, "container": name, "until": "24h"}); acctest.Num(t, none["total"], "total") != 0 || !strings.Contains(acctest.Str(none["note"]), "nothing matched") {
			t.Errorf("%s: what it did before it was made = %v", host, none)
		}
		if msg := refused(t, "container_events", map[string]any{"actions": []any{"explode"}}); !strings.Contains(msg, "an action is one of") {
			t.Errorf("%s: an action there is none of = %q", host, msg)
		}
	}

	// the whole record cleared: how much would go said first, then gone for every host at once
	preview := call(t, "container_events_clear", nil)
	if acctest.BoolOf(preview["cleared"]) || acctest.Num(t, preview["events"], "events") < 8 || acctest.Num(t, call(t, "container_events", map[string]any{"container": "acc-events-"})["total"], "total") < 8 {
		t.Fatalf("what clearing would take = %v", preview)
	}
	if cleared := call(t, "container_events_clear", map[string]any{"confirm": true}); !acctest.BoolOf(cleared["cleared"]) {
		t.Fatalf("clearing = %v", cleared)
	}
	if after := call(t, "container_events", map[string]any{"container": "acc-events-"}); acctest.Num(t, after["total"], "total") != 0 {
		t.Errorf("what the removed containers did, once cleared = %v", after)
	}
}
