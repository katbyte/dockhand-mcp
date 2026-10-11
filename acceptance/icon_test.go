//go:build integration

package acceptance

import (
	"context"
	"encoding/base64"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/katbyte/dockhand-mcp/lib/testenv"
	"github.com/katbyte/go-kt/mcp/acctest"
)

// journeyPixel is a picture of one pixel, as a PNG.
const journeyPixel = "iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAYAAAAfFcSJAAAADUlEQVR42mP8z8BQDwAEhQGAhKmMIQAAAABJRU5ErkJggg=="

// An icon is found among selfh.st's and given to a container, then a picture is sent for it, fetched back and removed; an environment is given a name
// and a picture and has the plain icon again once they are removed; and a stack Dockhand only found is given an icon only when that is asked for, is
// then one Dockhand keeps a record of, and is adopted and removed all the same.
func TestJourneyIconsFoundGivenFetchedAndRemoved(t *testing.T) {
	const (
		host  = testenv.Alpha
		other = testenv.Beta
		stack = "acc-journey-pictured-stack"
		dir   = laidUnder + "/pictured"
		path  = dir + "/" + stack + "/compose.yaml"
	)
	container := started(t, host, "acc-journey-pictured")
	here := t.TempDir()
	picture, text := filepath.Join(here, "icon.png"), filepath.Join(here, "icon.txt")
	pixel, err := base64.StdEncoding.DecodeString(journeyPixel)
	if err != nil {
		t.Fatal(err)
	}
	for file, content := range map[string][]byte{picture: pixel, text: []byte("not a picture")} {
		if err := os.WriteFile(file, content, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	// given is what was given an icon on a host: the host's own, and its containers' and stacks' by name
	given := func(env string) (own string, containers, stacks map[string]any) {
		row := named(acctest.Rows(t, call(t, "icon_list", map[string]any{"env": env})["icons"], "icons"), "env")[env]

		byName := func(v any) map[string]any {
			held, _ := v.(map[string]any)

			return held
		}

		return acctest.Str(row["icon"]), byName(row["containers"]), byName(row["stacks"])
	}
	plain, _, _ := given(other)
	ctx := context.WithoutCancel(t.Context())
	forgotten(t, host, stack)
	_ = world.Unlay(ctx, dir)
	t.Cleanup(func() {
		_ = world.Unlay(ctx, dir)
		for _, args := range []map[string]any{{"env": host, "container": container, "confirm": true}, {"env": other, "confirm": true}} {
			suite.Call(t, "icon_delete", args)
		}
	})

	// found among selfh.st's by a word, and given to the container: said first, then done, then nothing more to do
	found := named(acctest.Rows(t, call(t, "icon_search", map[string]any{"query": "docker"})["icons"], "icons"), "icon")
	if found["selfhst:docker"] == nil || found["selfhst:docker"]["name"] == nil {
		t.Fatalf("selfh.st's icons with docker in them = %v", found)
	}
	giving := map[string]any{"env": host, "container": container, "icon": "selfhst:docker"}
	if dry := call(t, "icon_set", over(giving, map[string]any{"dry_run": true})); !acctest.IsBool(dry["dry_run"], true) || dry["icon"] != "" {
		t.Fatalf("a dry run of giving an icon = %v", dry)
	}
	if _, containers, _ := given(host); containers[container] != nil {
		t.Fatalf("the container has the icon %v after a dry run", containers[container])
	}
	if set := call(t, "icon_set", giving); set["icon"] != "selfhst:docker" || set["container"] != container || set["note"] != nil {
		t.Fatalf("the container given an icon = %v", set)
	}
	if again := call(t, "icon_set", giving); len(acctest.Strs(t, again["changes"], "changes")) != 0 || !strings.Contains(acctest.Str(again["note"]), "has that icon already") {
		t.Errorf("the same icon given again = %v", again)
	}

	// what Dockhand would save and should not: a name nothing has, a name that is no icon's, a file that is no picture
	for what, tc := range map[string]struct {
		args map[string]any
		want string
	}{
		"a name no container has":   {map[string]any{"env": host, "container": "acc-journey-no-such-container", "icon": "box"}, "Dockhand would keep the icon under the name all the same"},
		"a name no stack has":       {map[string]any{"env": host, "stack": "acc-journey-no-such-stack", "icon": "box"}, "list a stack of that name with nothing in it"},
		"a name of two words":       {map[string]any{"env": host, "container": container, "icon": "two words"}, "is one word"},
		"a file that is no picture": {map[string]any{"env": other, "file": text}, "is no PNG, JPEG, GIF or WebP picture"},
		"an icon with no picture":   {map[string]any{"env": host, "container": container}, "give icon or file, one of them"},
	} {
		if msg := refused(t, "icon_set", tc.args); !strings.Contains(msg, tc.want) {
			t.Errorf("%s = %q, want %q", what, msg, tc.want)
		}
	}
	// an icon that is a name has no picture to fetch
	if msg := refused(t, "icon_download", map[string]any{"env": host, "container": container, "to": here}); !strings.Contains(msg, "its icon is selfhst:docker, a name and no picture that was sent") {
		t.Errorf("fetching the picture of an icon that is a name = %q", msg)
	}

	// a picture sent for it, and fetched back as Dockhand keeps it: a new file, never written over
	if set := call(t, "icon_set", map[string]any{"env": host, "container": container, "file": picture}); set["icon"] != "custom:container" {
		t.Fatalf("the container given a picture = %v", set)
	}
	fetched := call(t, "icon_download", map[string]any{"env": host, "container": container, "to": here})
	if saved := filepath.Join(here, container+".webp"); fetched["saved"] != saved || fetched["kind"] != "image/webp" || acctest.Num(t, fetched["bytes"], "bytes") == 0 {
		t.Errorf("the container's picture fetched = %v", fetched)
	} else if info, err := os.Stat(saved); err != nil || info.Size() == 0 {
		t.Errorf("the file written = %v, %v", info, err)
	}
	if msg := refused(t, "icon_download", map[string]any{"env": host, "container": container, "to": here}); !strings.Contains(msg, "it could not be written here") {
		t.Errorf("fetching it over the file that is there = %q", msg)
	}
	// one of selfh.st's fetched by its reference, and one it has none of said to be none
	if icon := call(t, "icon_download", map[string]any{"selfhst": "docker", "to": here}); !strings.HasPrefix(acctest.Str(icon["kind"]), "image/") || !strings.HasPrefix(acctest.Str(icon["saved"]), filepath.Join(here, "docker.")) {
		t.Errorf("one of selfh.st's icons fetched = %v", icon)
	}
	if msg := refused(t, "icon_download", map[string]any{"selfhst": "acc-no-such-app-anywhere", "to": here}); !strings.Contains(msg, "selfh.st has no icon with the reference") {
		t.Errorf("one selfh.st has none of = %q", msg)
	}
	if _, containers, _ := given(host); containers[container] != "custom:container" {
		t.Errorf("the icons of the host's containers = %v", containers)
	}

	// removed: said first, then done, then nothing left to remove
	if preview := call(t, "icon_delete", map[string]any{"env": host, "container": container}); !acctest.IsBool(preview["removed"], false) || preview["was"] != "custom:container" || len(acctest.Strs(t, preview["would_go"], "would_go")) != 1 {
		t.Fatalf("what removing the container's icon would take = %v", preview)
	}
	if gone := call(t, "icon_delete", map[string]any{"env": host, "container": container, "confirm": true}); !acctest.IsBool(gone["removed"], true) {
		t.Errorf("the container's icon removed = %v", gone)
	}
	if again := call(t, "icon_delete", map[string]any{"env": host, "container": container, "confirm": true}); !strings.Contains(acctest.Str(again["note"]), "nothing to remove") {
		t.Errorf("the container's icon removed twice = %v", again)
	}

	// an environment: a name, then a picture, and the plain icon again once that is removed
	if set := call(t, "icon_set", map[string]any{"env": other, "icon": "server"}); set["icon"] != "server" || set["container"] != nil {
		t.Errorf("the environment given an icon by name = %v", set)
	}
	if set := call(t, "icon_set", map[string]any{"env": other, "file": picture}); !strings.HasPrefix(acctest.Str(set["icon"]), "custom:env-") {
		t.Errorf("the environment given a picture = %v", set)
	}
	if own, _, _ := given(other); !strings.HasPrefix(own, "custom:env-") {
		t.Errorf("the environment's own icon = %q", own)
	}
	if fetched = call(t, "icon_download", map[string]any{"env": other, "to": filepath.Join(here, "host-icon")}); fetched["kind"] != "image/webp" || fetched["saved"] != filepath.Join(here, "host-icon") {
		t.Errorf("the environment's picture fetched = %v", fetched)
	}
	if gone := call(t, "icon_delete", map[string]any{"env": other, "confirm": true}); !acctest.IsBool(gone["removed"], true) || gone["icon"] != plain {
		t.Errorf("the environment's icon removed = %v, want it %s again", gone, plain)
	}

	// a stack brought up at the host, which Dockhand only found: given an icon only when that is asked for, and kept a record of from then on
	file := "name: " + stack + "\nservices:\n  app:\n    image: " + world.Image + "\n    command: [\"sleep\", \"100000\"]\n    stop_grace_period: 1s\n"
	if err := world.Compose(ctx, host, stack, file); err != nil {
		t.Fatal(err)
	}
	if err := world.Lay(ctx, path, file); err != nil {
		t.Fatal(err)
	}
	on := map[string]any{"env": host, "stack": stack}
	if msg := refused(t, "icon_set", over(on, map[string]any{"icon": "box"})); !strings.Contains(msg, "Dockhand only found the stack "+stack) || !strings.Contains(msg, "even_found") || call(t, "stack_get", on)["kept"] != "found" {
		t.Fatalf("a stack Dockhand only found given an icon = %q", msg)
	}
	set := call(t, "icon_set", over(on, map[string]any{"icon": "box", "even_found": true}))
	if set["icon"] != "box" || set["stack"] != stack || !strings.Contains(acctest.Str(set["note"]), "Dockhand keeps a record of the stack from now on") {
		t.Fatalf("that stack given an icon when asked = %v", set)
	}
	if _, _, stacks := given(host); stacks[stack] != "box" || call(t, "stack_get", on)["kept"] != "external" {
		t.Errorf("the icons of the host's stacks = %v, and the stack is kept as %v", stacks, call(t, "stack_get", on)["kept"])
	}

	// the record is no bar to giving Dockhand its file: adopted, it is the same stack with its containers and its icon
	adopted := call(t, "stack_adopt", map[string]any{"env": host, "name": stack, "compose_path": path})
	kept := call(t, "stack_get", on)
	if _, _, stacks := given(host); adopted["compose_path"] != path || adopted["status"] != "running" || kept["kept"] != "internal" || !acctest.BoolOf(kept["deployable"]) || stacks[stack] != "box" || stackNamed(t, stack+"-1") != nil {
		t.Errorf("the recorded stack adopted = %v, kept as %v with the icons %v", adopted, kept["kept"], stacks)
	}
	if gone := call(t, "icon_delete", over(on, map[string]any{"confirm": true})); !acctest.IsBool(gone["removed"], true) || gone["was"] != "box" {
		t.Errorf("the stack's icon removed = %v", gone)
	}
	if removed := call(t, "stack_delete", over(on, map[string]any{"confirm": true, "keep_files": true})); !acctest.BoolOf(removed["deleted"]) || stackNamed(t, stack) != nil {
		t.Errorf("the stack removed = %v", removed)
	}
}
