//go:build integration

package acceptance

import (
	"context"
	"slices"
	"strings"
	"testing"

	"github.com/katbyte/dockhand-mcp/lib/testenv"
	"github.com/katbyte/go-kt/mcp/acctest"
)

// What the image journey brings to a host and takes away again: two small images from the mirror the fixtures' own image comes from, and a name of
// its own for one of them.
const (
	pulledImage    = "mirror.gcr.io/library/busybox:1.36"
	cancelledImage = "mirror.gcr.io/library/alpine:3.20"
	spareName      = "acc/spare:one"
)

// imageOn is the whole id a host's own Docker has under a name, or "" when it has none: what an image tool's answer is held against.
func imageOn(t *testing.T, host, name string) string {
	t.Helper()

	out, err := world.Docker(context.WithoutCancel(t.Context()), host, "image", "inspect", "--format", "{{.Id}}", name)
	if err != nil {
		return ""
	}

	return strings.TrimSpace(out)
}

// imagesNamed is the images of an answer by the first name each goes by.
func imagesNamed(t *testing.T, out map[string]any) map[string]map[string]any {
	t.Helper()

	rows := map[string]map[string]any{}
	for _, row := range acctest.Rows(t, out["images"], "images") {
		if tags := acctest.Strs(t, row["tags"], "tags"); len(tags) > 0 {
			rows[acctest.Str(row["env"])+" "+tags[0]] = row
		}
	}

	return rows
}

func TestImageList(t *testing.T) {
	out := call(t, "image_list", nil)
	rows := imagesNamed(t, out)

	// the image the fixtures run is on both hosts, in use on each
	for _, host := range []string{testenv.Alpha, testenv.Beta} {
		row := rows[host+" "+world.Image]
		if row == nil || acctest.Num(t, row["containers"], "containers") < 1 || acctest.Decimal(t, row["size_mb"], "size_mb") <= 0 || acctest.Str(row["created"]) == "" || row["untagged"] != nil {
			t.Errorf("the fixtures' image on %s = %v", host, row)

			continue
		}
		if id := imageOn(t, host, world.Image); !strings.HasPrefix(id, "sha256:"+acctest.Str(row["id"])) {
			t.Errorf("its id on %s = %v, and the host's own Docker says %s", host, row["id"], id)
		}
	}

	// the counts are of the rows: how many, how many of them nothing uses, and what they weigh
	unused, weight := 0, 0.0
	listed := acctest.Rows(t, out["images"], "images")
	for _, row := range listed {
		weight += acctest.Decimal(t, row["size_mb"], "size_mb")
		if acctest.Num(t, row["containers"], "containers") == 0 {
			unused++
		}
	}
	if acctest.Num(t, out["total"], "total") != len(listed) || acctest.Num(t, out["unused"], "unused") != unused || acctest.Decimal(t, out["total_mb"], "total_mb") < weight-1 || acctest.Decimal(t, out["total_mb"], "total_mb") > weight+1 || out["failed"] != nil {
		t.Errorf("the counts = total %v, unused %v, %v MB over %d rows, %d of them unused, weighing %.1f MB", out["total"], out["unused"], out["total_mb"], len(listed), unused, weight)
	}

	// narrowed to one host and a part of a name, and to what nothing uses
	one := acctest.Rows(t, call(t, "image_list", map[string]any{"env": testenv.Beta, "name": "BUSYBOX:1.37"})["images"], "images")
	if len(one) != 1 || one[0]["env"] != testenv.Beta || !slices.Contains(acctest.Strs(t, one[0]["tags"], "tags"), world.Image) {
		t.Errorf("narrowed to beta and a part of the name = %v", one)
	}
	for _, row := range acctest.Rows(t, call(t, "image_list", map[string]any{"unused": true})["images"], "images") {
		if acctest.Num(t, row["containers"], "containers") != 0 {
			t.Errorf("listed as unused, with containers made from it: %v", row)
		}
	}
}

func TestImageGet(t *testing.T) {
	out := call(t, "image_get", map[string]any{"image": world.Image, "env": testenv.Alpha})

	if out["env"] != testenv.Alpha || out["full_id"] != imageOn(t, testenv.Alpha, world.Image) || !slices.Contains(acctest.Strs(t, out["tags"], "tags"), world.Image) || len(acctest.Strs(t, out["digests"], "digests")) == 0 {
		t.Errorf("the fixtures' image on alpha = %v", out)
	}
	// how it was built, and which containers were made from it, running or not
	if layers := acctest.Rows(t, out["layers"], "layers"); len(layers) == 0 || acctest.Decimal(t, layers[len(layers)-1]["size_mb"], "size_mb") <= 0 {
		t.Errorf("its layers = %v", out["layers"])
	}
	users := acctest.Strs(t, out["used_by"], "used_by")
	for _, want := range []string{loose + " (running)", crashed + " (exited)", webFront + " (running)"} {
		if !slices.Contains(users, want) {
			t.Errorf("%s is not among the containers made from it: %v", want, users)
		}
	}

	// by the start of its id, as the list gives it, and by the end of its name
	for _, by := range []string{acctest.Str(out["id"]), "busybox:1.37", "library/busybox"} {
		if found := call(t, "image_get", map[string]any{"image": by, "env": testenv.Alpha}); found["full_id"] != out["full_id"] {
			t.Errorf("found by %q = %v", by, found["full_id"])
		}
	}
	// it is on both hosts, and which is meant is not guessed at
	if msg := refused(t, "image_get", map[string]any{"image": world.Image}); !strings.Contains(msg, "more than one image") || !strings.Contains(msg, "on "+testenv.Alpha) || !strings.Contains(msg, "on "+testenv.Beta) {
		t.Errorf("an image on two hosts, with no host said = %q", msg)
	}
	if msg := refused(t, "image_get", map[string]any{"image": "acc/no-such-image:1"}); !strings.Contains(msg, "no image goes by") {
		t.Errorf("an image there is none of = %q", msg)
	}
}

// An image is pulled to the host behind the agent as a job that is followed to its end, pulled again to no change, described, given a second name,
// removed with both, pulled once more and pruned away, twice over; and every step is held against what the host's own Docker says it has.
func TestJourneyAnImagePulledTaggedAndRemoved(t *testing.T) {
	if world == nil {
		t.Skip(testenv.NotConfigured)
	}
	const host = testenv.Beta
	ctx := context.WithoutCancel(t.Context())
	on := func(more map[string]any) map[string]any {
		args := map[string]any{"env": host}
		for k, v := range more {
			args[k] = v
		}

		return args
	}
	// a run that was cut short may have left them behind
	_, _ = world.Docker(ctx, host, "rmi", "-f", pulledImage, spareName, cancelledImage)
	fixture := imageOn(t, host, world.Image)

	for round := range 2 {
		// a dry run says the host has none, and pulls nothing
		if dry := call(t, "image_pull", on(map[string]any{"image": pulledImage, "dry_run": true})); !acctest.IsBool(dry["dry_run"], true) || dry["was"] != nil || imageOn(t, host, pulledImage) != "" {
			t.Fatalf("round %d: a dry run of the pull = %v", round, dry)
		}

		// started and not waited for: a job to follow, and nothing claimed of an image that is not there yet
		begun := call(t, "image_pull", on(map[string]any{"image": pulledImage, "wait": 0}))
		job := acctest.Str(begun["job"])
		if !acctest.IsBool(begun["running"], true) || job == "" || begun["is"] != nil || !acctest.IsBool(begun["changed"], false) || !strings.Contains(acctest.Str(begun["note"]), job) {
			t.Fatalf("round %d: a pull not waited for = %v", round, begun)
		}
		// followed to its end
		ended := call(t, "job_get", map[string]any{"job": job, "wait": 120})
		if ended["status"] != "done" || !acctest.IsBool(ended["running"], false) || ended["error"] != nil || len(acctest.Strs(t, ended["progress"], "progress")) == 0 {
			t.Fatalf("round %d: the pull's job, waited on = %v", round, ended)
		}
		// which there is then nothing left of to stop
		if stopped := call(t, "job_cancel", map[string]any{"job": job}); stopped["was"] != "done" || !acctest.IsBool(stopped["cancelled"], false) {
			t.Errorf("round %d: stopping a job that has ended = %v", round, stopped)
		}
		pulledID := imageOn(t, host, pulledImage)
		if pulledID == "" {
			t.Fatalf("round %d: the job is done and the host has no %s", round, pulledImage)
		}

		// pulled again, and waited for: the host already has the newest, and nothing changes
		again := call(t, "image_pull", on(map[string]any{"image": pulledImage}))
		if !acctest.IsBool(again["running"], false) || !acctest.IsBool(again["changed"], false) || again["was"] != again["is"] || !strings.HasPrefix(pulledID, "sha256:"+acctest.Str(again["is"])) || !strings.Contains(acctest.Str(again["note"]), "already had the newest") {
			t.Errorf("round %d: pulled a second time = %v", round, again)
		}

		// described: nothing was made from it yet
		got := call(t, "image_get", on(map[string]any{"image": pulledImage}))
		if got["full_id"] != pulledID || acctest.Num(t, got["containers"], "containers") != 0 || len(acctest.Strs(t, got["used_by"], "used_by")) != 0 {
			t.Errorf("round %d: the image as described = %v", round, got)
		}

		// a second name: said first, then given, and given again to no change
		if dry := call(t, "image_tag", on(map[string]any{"image": pulledImage, "as": spareName, "dry_run": true})); !acctest.IsBool(dry["added"], false) || imageOn(t, host, spareName) != "" {
			t.Errorf("round %d: a dry run of the name = %v", round, dry)
		}
		tagged := call(t, "image_tag", on(map[string]any{"image": pulledImage, "as": spareName}))
		if !acctest.IsBool(tagged["added"], true) || strings.Join(acctest.Strs(t, tagged["tags"], "tags"), "|") != spareName+"|"+pulledImage || imageOn(t, host, spareName) != pulledID {
			t.Fatalf("round %d: given a second name = %v", round, tagged)
		}
		if twice := call(t, "image_tag", on(map[string]any{"image": spareName, "as": spareName})); !acctest.IsBool(twice["added"], false) || !strings.Contains(acctest.Str(twice["note"]), "already goes by that name") {
			t.Errorf("round %d: given the name again = %v", round, twice)
		}

		// removing it says first what would go, both names with it, and removes nothing
		preview := call(t, "image_delete", on(map[string]any{"image": spareName}))
		if !acctest.IsBool(preview["removed"], false) || strings.Join(acctest.Strs(t, preview["would_go"], "would_go"), "|") != "the image "+acctest.Str(preview["id"])+" on "+host+"|its name "+spareName+"|its name "+pulledImage || imageOn(t, host, pulledImage) != pulledID {
			t.Fatalf("round %d: removing it, not confirmed = %v", round, preview)
		}
		gone := call(t, "image_delete", on(map[string]any{"image": spareName, "confirm": true}))
		if !acctest.IsBool(gone["removed"], true) || imageOn(t, host, pulledImage) != "" || imageOn(t, host, spareName) != "" {
			t.Fatalf("round %d: removed = %v, and the host still has %q under one name and %q under the other", round, gone, imageOn(t, host, pulledImage), imageOn(t, host, spareName))
		}
		if msg := refused(t, "image_delete", on(map[string]any{"image": pulledImage, "confirm": true})); !strings.Contains(msg, "no image goes by") {
			t.Errorf("round %d: removing it a second time = %q", round, msg)
		}

		// a pull asked to stop: the request is taken, and whichever way the pull ends it is said rather than assumed
		running := call(t, "image_pull", on(map[string]any{"image": cancelledImage, "wait": 0}))
		if stopping := call(t, "job_cancel", map[string]any{"job": acctest.Str(running["job"])}); stopping["was"] == "running" && !acctest.IsBool(stopping["cancelled"], true) {
			t.Errorf("round %d: a running pull asked to stop = %v", round, stopping)
		}
		if after := call(t, "job_get", map[string]any{"job": acctest.Str(running["job"]), "wait": 120}); !acctest.IsBool(after["running"], false) {
			t.Fatalf("round %d: the pull asked to stop, two minutes on = %v", round, after)
		}

		// pulled once more, then pruned: said first, and then everything nothing uses goes and what is in use stays
		call(t, "image_pull", on(map[string]any{"image": pulledImage, "wait": 120}))
		would := call(t, "image_prune", on(map[string]any{"all": true}))
		if !slices.Contains(acctest.Strs(t, would["would_go"], "would_go"), pulledImage) || len(acctest.Strs(t, would["removed"], "removed")) != 0 || imageOn(t, host, pulledImage) == "" {
			t.Fatalf("round %d: pruning, not confirmed = %v", round, would)
		}
		// left as it is, a prune takes only what has no name, and this has one
		if kept := call(t, "image_prune", on(map[string]any{"confirm": true})); slices.Contains(acctest.Strs(t, kept["removed"], "removed"), pulledImage) || imageOn(t, host, pulledImage) == "" {
			t.Fatalf("round %d: a prune of the nameless took a named image: %v", round, kept)
		}
		pruned := call(t, "image_prune", on(map[string]any{"all": true, "confirm": true, "wait": 120}))
		if !acctest.IsBool(pruned["running"], false) || !slices.Contains(acctest.Strs(t, pruned["removed"], "removed"), pulledImage) || acctest.Decimal(t, pruned["freed_mb"], "freed_mb") <= 0 || imageOn(t, host, pulledImage) != "" || imageOn(t, host, cancelledImage) != "" {
			t.Fatalf("round %d: pruned = %v", round, pruned)
		}
		if imageOn(t, host, world.Image) != fixture || stateOf(t, host, solo) != "running" {
			t.Fatalf("round %d: the image the host's container runs did not come through the prune, or the container did not", round)
		}
	}

	// what cannot be pulled says why, and an image a container runs is not removed from under it, forced or not
	if msg := refused(t, "image_pull", on(map[string]any{"image": "mirror.gcr.io/library/acc-no-such-image:1", "wait": 120})); !strings.Contains(msg, "was not pulled to "+host) || strings.Contains(msg, `{"message"`) {
		t.Errorf("a pull of an image there is none of = %q", msg)
	}
	if msg := refused(t, "image_delete", on(map[string]any{"image": world.Image, "confirm": true})); !strings.Contains(msg, "is the image of 1 containers on "+host) || !strings.Contains(msg, solo+" (running)") {
		t.Errorf("removing an image in use = %q", msg)
	}
	if msg := refused(t, "image_delete", on(map[string]any{"image": world.Image, "confirm": true, "force": true})); !strings.Contains(msg, "was not removed") {
		t.Errorf("removing an image a running container uses, forced = %q", msg)
	}
	if imageOn(t, host, world.Image) != fixture || stateOf(t, host, solo) != "running" {
		t.Error("the image a container runs was touched by a removal that was refused")
	}
}
