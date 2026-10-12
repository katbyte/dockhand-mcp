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

// What the volume journey makes on a host and takes away again.
const (
	journeyVolume = "acc-journey-vol"
	journeyCopy   = "acc-journey-copy"
	journeyKeeper = "acc-journey-vol-user"
	// webVolume is the volume the web stack's front service keeps
	webVolume = webStack + "_data"
	// helperImage is what Dockhand runs to look inside a volume, as Docker Hub names it, and where the suites fetch it from instead
	helperImage  = "busybox:latest"
	helperMirror = "mirror.gcr.io/library/busybox:latest"
)

// volumeOn reports whether a host's own Docker has a volume by a name.
func volumeOn(t *testing.T, host, name string) bool {
	t.Helper()

	_, err := world.Docker(context.WithoutCancel(t.Context()), host, "volume", "inspect", name)

	return err == nil
}

// containersOn is how many containers a host's own Docker has, running or not: what must be the same after Dockhand has looked inside a volume as
// before.
func containersOn(t *testing.T, host string) int {
	t.Helper()

	out, err := world.Docker(context.WithoutCancel(t.Context()), host, "ps", "-aq")
	if err != nil {
		t.Fatalf("listing the containers of %s: %v", host, err)
	}

	return len(strings.Fields(out))
}

// helperThere gives a host the image Dockhand runs to look inside a volume, from the mirror, so that looking does not have the host fetch it from
// Docker Hub in the middle of a test.
func helperThere(t *testing.T, host string) {
	t.Helper()

	ctx := context.WithoutCancel(t.Context())
	if _, err := world.Docker(ctx, host, "image", "inspect", helperImage); err == nil {
		return
	}
	for _, args := range [][]string{{"pull", "-q", helperMirror}, {"tag", helperMirror, helperImage}, {"rmi", helperMirror}} {
		if out, err := world.Docker(ctx, host, args...); err != nil {
			t.Fatalf("giving %s the helper image: %v: %s", host, err, out)
		}
	}
}

func TestVolumeList(t *testing.T) {
	out := call(t, "volume_list", nil)
	rows := named(acctest.Rows(t, out["volumes"], "volumes"), "name")

	// the volume the web stack's front service keeps, on the host the stack is on
	web := rows[webVolume]
	if web == nil || web["env"] != testenv.Alpha || web["driver"] != "local" || web["stack"] != webStack || acctest.Str(web["created"]) == "" || strings.Join(acctest.Strs(t, web["used_by"], "used_by"), "|") != webFront {
		t.Errorf("the web stack's volume = %v", web)
	}

	unused := 0
	listed := acctest.Rows(t, out["volumes"], "volumes")
	for _, row := range listed {
		if len(acctest.Strs(t, row["used_by"], "used_by")) == 0 {
			unused++
		}
	}
	if acctest.Num(t, out["total"], "total") != len(listed) || acctest.Num(t, out["unused"], "unused") != unused || out["failed"] != nil {
		t.Errorf("the counts = total %v, unused %v, over %d rows, %d of them unused", out["total"], out["unused"], len(listed), unused)
	}

	// narrowed to one stack's, and to a host that has none of that stack's
	if only := acctest.Rows(t, call(t, "volume_list", map[string]any{"stack": webStack})["volumes"], "volumes"); len(only) != 1 || only[0]["name"] != webVolume {
		t.Errorf("the web stack's volumes = %v", only)
	}
	if none := call(t, "volume_list", map[string]any{"env": testenv.Beta, "name": "WEB"}); acctest.Num(t, none["total"], "total") != 0 {
		t.Errorf("volumes named web on beta = %v", none)
	}
	for _, row := range acctest.Rows(t, call(t, "volume_list", map[string]any{"unused": true})["volumes"], "volumes") {
		if len(acctest.Strs(t, row["used_by"], "used_by")) != 0 {
			t.Errorf("listed as unused, with a container that mounts it: %v", row)
		}
	}
}

func TestVolumeGet(t *testing.T) {
	out := call(t, "volume_get", map[string]any{"volume": webVolume})

	if out["env"] != testenv.Alpha || out["name"] != webVolume || !strings.Contains(acctest.Str(out["mountpoint"]), webVolume) || out["scope"] != "local" || out["stack"] != webStack || strings.Join(acctest.Strs(t, out["used_by"], "used_by"), "|") != webFront+" (running)" {
		t.Errorf("the web stack's volume = %v", out)
	}
	if labels := acctest.Object(t, out["labels"], "labels"); labels["com.docker.compose.project"] != webStack || labels["com.docker.compose.volume"] != "data" {
		t.Errorf("its labels = %v", labels)
	}
	// by its name in full and nothing less
	if msg := refused(t, "volume_get", map[string]any{"volume": webStack}); !strings.Contains(msg, "no volume is named") {
		t.Errorf("a volume by part of its name = %q", msg)
	}
}

// A volume is made, filled from outside, looked into and read through Dockhand's helper, copied, mounted and so kept from removal, and removed and
// pruned once nothing mounts it, twice over; and every step is held against what the host's own Docker says it has.
func TestJourneyAVolumeMadeReadCopiedAndRemoved(t *testing.T) {
	if world == nil {
		t.Skip(testenv.NotConfigured)
	}
	const host = testenv.Alpha
	ctx := context.WithoutCancel(t.Context())
	on := func(more map[string]any) map[string]any {
		args := map[string]any{"env": host}
		maps.Copy(args, more)

		return args
	}
	clean := func() {
		_, _ = world.Docker(ctx, host, "rm", "-f", journeyKeeper)
		_, _ = world.Docker(ctx, host, "volume", "rm", "-f", journeyVolume, journeyCopy)
	}
	// a run that was cut short may have left them behind
	clean()
	t.Cleanup(clean)
	helperThere(t, host)

	for round := range 2 {
		// said first, then made, and a second by its name refused rather than answered as made
		if dry := call(t, "volume_create", on(map[string]any{"name": journeyVolume, "dry_run": true})); !acctest.IsBool(dry["dry_run"], true) || volumeOn(t, host, journeyVolume) {
			t.Fatalf("round %d: a dry run of making it = %v", round, dry)
		}
		made := call(t, "volume_create", on(map[string]any{"name": journeyVolume, "labels": map[string]any{"made-by": "journey"}}))
		if row := acctest.Object(t, made["volume"], "volume"); !acctest.IsBool(made["created"], true) || row["name"] != journeyVolume || row["driver"] != "local" || len(acctest.Strs(t, row["used_by"], "used_by")) != 0 || !volumeOn(t, host, journeyVolume) {
			t.Fatalf("round %d: made = %v", round, made)
		}
		if msg := refused(t, "volume_create", on(map[string]any{"name": journeyVolume})); !strings.Contains(msg, "already has a volume named "+journeyVolume) {
			t.Errorf("round %d: made a second time = %q", round, msg)
		}

		// filled from outside, as a container that keeps its state there would
		if out, err := world.Docker(ctx, host, "run", "--rm", "-v", journeyVolume+":/d", world.Image, "sh", "-c", "mkdir /d/conf && echo mode=demo > /d/conf/app.conf && echo API_KEY=not-a-real-key > /d/.env"); err != nil {
			t.Fatalf("round %d: filling the volume: %v: %s", round, err, out)
		}

		// looked into: its top, then a directory in it, and the helper Dockhand started for the look is gone each time
		before := containersOn(t, host)
		top := named(acctest.Rows(t, call(t, "volume_files", on(map[string]any{"volume": journeyVolume}))["entries"], "entries"), "name")
		if len(top) != 2 || top["conf"]["type"] != "directory" || top[".env"]["type"] != "file" || acctest.Num(t, top[".env"]["size"], "size") != len("API_KEY=not-a-real-key\n") {
			t.Fatalf("round %d: the top of the volume = %v", round, top)
		}
		if inside := acctest.Rows(t, call(t, "volume_files", on(map[string]any{"volume": journeyVolume, "path": "conf"}))["entries"], "entries"); len(inside) != 1 || inside[0]["name"] != "app.conf" {
			t.Errorf("round %d: the directory in it = %v", round, inside)
		}
		if msg := refused(t, "volume_files", on(map[string]any{"volume": journeyVolume, "path": "/nope"})); !strings.Contains(msg, "has no directory /nope") {
			t.Errorf("round %d: a directory there is none of = %q", round, msg)
		}
		// a path that climbs out of the volume is taken from its top, and finds nothing of the helper's own
		if msg := refused(t, "volume_files", on(map[string]any{"volume": journeyVolume, "path": "/../../etc"})); !strings.Contains(msg, "has no directory /etc") {
			t.Errorf("round %d: a path above the volume = %q", round, msg)
		}

		// read: a file as it is, and one with a key in it hidden until it is asked for
		if conf := call(t, "volume_read", on(map[string]any{"volume": journeyVolume, "path": "conf/app.conf"})); conf["content"] != "mode=demo\n" || conf["path"] != "/conf/app.conf" || conf["hidden"] != nil {
			t.Errorf("round %d: a file read = %v", round, conf)
		}
		secret := call(t, "volume_read", on(map[string]any{"volume": journeyVolume, "path": "/.env"}))
		if secret["content"] != "API_KEY=***\n" || acctest.Num(t, secret["hidden"], "hidden") != 1 || strings.Contains(flat(secret), "not-a-real-key") {
			t.Errorf("round %d: a file with a key in it = %v", round, secret)
		}
		if shown := call(t, "volume_read", on(map[string]any{"volume": journeyVolume, "path": "/.env", "reveal_secrets": true})); shown["content"] != "API_KEY=not-a-real-key\n" {
			t.Errorf("round %d: the same, revealed = %v", round, shown)
		}
		if msg := refused(t, "volume_read", on(map[string]any{"volume": journeyVolume, "path": "/conf"})); !strings.Contains(msg, "is a directory") {
			t.Errorf("round %d: a directory read as a file = %q", round, msg)
		}
		if msg := refused(t, "volume_read", on(map[string]any{"volume": journeyVolume, "path": "/nope.txt"})); !strings.Contains(msg, "has no file /nope.txt") {
			t.Errorf("round %d: a file there is none of = %q", round, msg)
		}
		if after := containersOn(t, host); after != before {
			t.Errorf("round %d: the host had %d containers before the volume was looked into and has %d after: a helper was left behind", round, before, after)
		}

		// copied, files and all, into a new volume; and not into one that is there
		if dry := call(t, "volume_clone", on(map[string]any{"volume": journeyVolume, "as": journeyCopy, "dry_run": true})); !acctest.IsBool(dry["cloned"], false) || volumeOn(t, host, journeyCopy) {
			t.Fatalf("round %d: a dry run of the copy = %v", round, dry)
		}
		copied := call(t, "volume_clone", on(map[string]any{"volume": journeyVolume, "as": journeyCopy}))
		if !acctest.IsBool(copied["cloned"], true) || copied["from"] != journeyVolume || !volumeOn(t, host, journeyCopy) {
			t.Fatalf("round %d: copied = %v", round, copied)
		}
		if conf := call(t, "volume_read", on(map[string]any{"volume": journeyCopy, "path": "/conf/app.conf"})); conf["content"] != "mode=demo\n" {
			t.Errorf("round %d: the file in the copy = %v", round, conf)
		}
		if msg := refused(t, "volume_clone", on(map[string]any{"volume": journeyVolume, "as": journeyCopy})); !strings.Contains(msg, "already has a volume named "+journeyCopy) {
			t.Errorf("round %d: copied onto a volume that is there = %q", round, msg)
		}

		// mounted by a container, it is not removed and not pruned, and the copy nothing mounts is what a prune would take
		if out, err := world.Docker(ctx, host, "run", "-d", "--name", journeyKeeper, "-v", journeyVolume+":/d", world.Image, "sleep", "100000"); err != nil {
			t.Fatalf("round %d: mounting the volume: %v: %s", round, err, out)
		}
		if got := call(t, "volume_get", on(map[string]any{"volume": journeyVolume})); strings.Join(acctest.Strs(t, got["used_by"], "used_by"), "|") != journeyKeeper+" (running)" || acctest.Object(t, got["labels"], "labels")["made-by"] != "journey" {
			t.Errorf("round %d: the volume, mounted = %v", round, got)
		}
		if msg := refused(t, "volume_delete", on(map[string]any{"volume": journeyVolume, "confirm": true})); !strings.Contains(msg, "is mounted by 1 containers ("+journeyKeeper+" (running))") || !volumeOn(t, host, journeyVolume) {
			t.Fatalf("round %d: removing a mounted volume = %q", round, msg)
		}
		would := acctest.Strs(t, call(t, "volume_prune", on(nil))["would_go"], "would_go")
		if !slices.Contains(would, journeyCopy) || slices.Contains(would, journeyVolume) || slices.Contains(would, webVolume) || !volumeOn(t, host, journeyCopy) {
			t.Fatalf("round %d: what a prune would take = %v", round, would)
		}

		// the copy removed: said first, then gone
		if preview := call(t, "volume_delete", on(map[string]any{"volume": journeyCopy})); !acctest.IsBool(preview["removed"], false) || len(acctest.Strs(t, preview["would_go"], "would_go")) != 1 || !volumeOn(t, host, journeyCopy) {
			t.Fatalf("round %d: removing the copy, not confirmed = %v", round, preview)
		}
		if gone := call(t, "volume_delete", on(map[string]any{"volume": journeyCopy, "confirm": true})); !acctest.IsBool(gone["removed"], true) || volumeOn(t, host, journeyCopy) {
			t.Fatalf("round %d: the copy removed = %v", round, gone)
		}

		// its container gone, the first is unused and a prune takes it, leaving what is mounted
		if out, err := world.Docker(ctx, host, "rm", "-f", journeyKeeper); err != nil {
			t.Fatalf("round %d: removing the container: %v: %s", round, err, out)
		}
		pruned := call(t, "volume_prune", on(map[string]any{"confirm": true}))
		if !slices.Contains(acctest.Strs(t, pruned["removed"], "removed"), journeyVolume) || pruned["would_go"] != nil || volumeOn(t, host, journeyVolume) || !volumeOn(t, host, webVolume) {
			t.Fatalf("round %d: pruned = %v", round, pruned)
		}
	}

	// and through the agent: the helper is started on the host behind it just the same
	helperThere(t, testenv.Beta)
	if made := call(t, "volume_create", map[string]any{"env": testenv.Beta, "name": journeyVolume}); !acctest.IsBool(made["created"], true) {
		t.Fatalf("made behind the agent = %v", made)
	}
	t.Cleanup(func() { _, _ = world.Docker(ctx, testenv.Beta, "volume", "rm", "-f", journeyVolume) })
	before := containersOn(t, testenv.Beta)
	if empty := call(t, "volume_files", map[string]any{"env": testenv.Beta, "volume": journeyVolume}); len(acctest.Rows(t, empty["entries"], "entries")) != 0 || empty["note"] != nil {
		t.Errorf("an empty volume behind the agent = %v", empty)
	}
	if after := containersOn(t, testenv.Beta); after != before {
		t.Errorf("the host behind the agent had %d containers before the look and has %d after", before, after)
	}
	if gone := call(t, "volume_delete", map[string]any{"env": testenv.Beta, "volume": journeyVolume, "confirm": true}); !acctest.IsBool(gone["removed"], true) || volumeOn(t, testenv.Beta, journeyVolume) {
		t.Errorf("removed behind the agent = %v", gone)
	}
}
