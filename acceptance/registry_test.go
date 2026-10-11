//go:build integration

package acceptance

import (
	"context"
	"strings"
	"testing"

	"github.com/katbyte/dockhand-mcp/lib/testenv"
	"github.com/katbyte/go-kt/mcp/acctest"
)

// A registry is tried, saved and listed; an image is pushed to it from one host, found in it with its tags and sizes, and pulled from it by the other
// with the login Dockhand keeps; the registry is renamed, given a wrong password and the right one again, and has its login forgotten; a tag is
// deleted, which takes its twin; and the registry is forgotten.
func TestJourneyARegistrySavedPushedToBrowsedAndForgotten(t *testing.T) {
	const name, repository = "acc-journey-registry", "acc/box"
	const from, to = testenv.Alpha, testenv.Beta
	if world == nil {
		t.Skip(testenv.NotConfigured)
	}
	if world.Registry == "" {
		t.Skip("the environment names no registry: .scripts/testenv.sh up starts one")
	}
	address, pushed := "http://"+world.Registry, world.Registry+"/"+repository
	ctx := context.WithoutCancel(t.Context())
	listed := func() map[string]map[string]any {
		return named(acctest.Rows(t, call(t, "registry_list", nil)["registries"], "registries"), "name")
	}
	clean := func() {
		for _, known := range []string{name, name + "-two"} {
			if listed()[known] != nil {
				suite.Call(t, "registry_delete", map[string]any{"registry": known, "confirm": true})
			}
		}
		_, _ = world.Docker(ctx, from, "rmi", "-f", pushed+":1", pushed+":2")
		_, _ = world.Docker(ctx, to, "rmi", "-f", pushed+":1")
	}
	clean()
	t.Cleanup(clean)
	before := len(listed())
	login := func(more map[string]any) map[string]any {
		args := map[string]any{"url": address + "/", "username": world.User, "password": world.Password}
		for k, v := range more {
			args[k] = v
		}

		return args
	}

	// tried before it is saved, with the right login, the wrong one and none; nothing is saved by trying or by a refused save
	if tried := call(t, "registry_test", login(nil)); !acctest.IsBool(tried["reachable"], true) || !acctest.IsBool(tried["authenticated"], true) || !strings.Contains(acctest.Str(tried["note"]), "not saved") {
		t.Fatalf("tried with the right login = %v", tried)
	}
	if tried := call(t, "registry_test", login(map[string]any{"password": "wrong"})); !acctest.IsBool(tried["reachable"], true) || !acctest.IsBool(tried["authenticated"], false) {
		t.Errorf("tried with the wrong password = %v", tried)
	}
	if tried := call(t, "registry_test", map[string]any{"url": address}); !acctest.IsBool(tried["reachable"], true) || tried["authenticated"] != nil {
		t.Errorf("tried with no login = %v", tried)
	}
	if msg := refused(t, "registry_test", map[string]any{"url": world.Registry}); !strings.Contains(msg, "with how it is spoken to before it") {
		t.Errorf("tried by an address with no scheme = %q", msg)
	}
	if msg := refused(t, "registry_create", login(map[string]any{"name": name, "password": "wrong"})); !strings.Contains(msg, "was not saved") || !strings.Contains(msg, "401") {
		t.Errorf("saved with the wrong password = %q", msg)
	}
	if dry := call(t, "registry_create", login(map[string]any{"name": name, "dry_run": true})); len(acctest.Strs(t, dry["changes"], "changes")) != 1 || dry["registry"] != nil || len(listed()) != before {
		t.Fatalf("a dry run of saving it = %v", dry)
	}

	made := call(t, "registry_create", login(map[string]any{"name": name}))
	if row := acctest.Object(t, made["registry"], "registry"); row["name"] != name || row["url"] != address || row["username"] != world.User || !acctest.IsBool(row["has_credentials"], true) || row["default"] != nil || strings.Contains(flat(made), world.Password) {
		t.Fatalf("saved = %v", made)
	}
	if msg := refused(t, "registry_create", login(map[string]any{"name": strings.ToUpper(name)})); !strings.Contains(msg, "already named") {
		t.Errorf("a second by the same name = %q", msg)
	}
	if kept := listed()[name]; kept == nil || len(listed()) != before+1 || !acctest.IsBool(call(t, "registry_test", map[string]any{"registry": name})["authenticated"], true) {
		t.Errorf("as listed and tried once saved = %v", kept)
	}

	// an image on one host pushed under a name of its own: said first, then pushed and read back from the registry, twice over under two tags
	started(t, from, "acc-registry-source")
	if dry := call(t, "image_push", map[string]any{"env": from, "image": world.Image, "registry": name, "as": repository + ":1", "dry_run": true}); dry["pushed_as"] != pushed+":1" || acctest.BoolOf(dry["pushed"]) || imageOn(t, from, pushed+":1") != "" {
		t.Fatalf("a dry run of pushing = %v", dry)
	}
	for _, tag := range []string{"1", "2"} {
		did := call(t, "image_push", map[string]any{"env": from, "image": world.Image, "registry": name, "as": repository + ":" + tag})
		if !acctest.BoolOf(did["pushed"]) || acctest.BoolOf(did["running"]) || did["pushed_as"] != pushed+":"+tag || did["env"] != from || imageOn(t, from, pushed+":"+tag) != imageOn(t, from, world.Image) {
			t.Fatalf("pushing it as %s = %v", tag, did)
		}
	}
	if msg := refused(t, "image_push", map[string]any{"env": from, "image": "acc-no-such-image:1", "registry": name}); !strings.Contains(msg, "acc-no-such-image:1") {
		t.Errorf("pushing an image the host has none of = %q", msg)
	}

	// found in the registry: among what it holds, by a part of its name, and by its tags with their sizes
	if holds := named(acctest.Rows(t, call(t, "registry_images", map[string]any{"registry": name})["images"], "images"), "name"); holds[repository] == nil {
		t.Errorf("what the registry holds = %v", holds)
	}
	if found := call(t, "registry_images", map[string]any{"registry": name, "search": "acc/bo"}); acctest.Num(t, found["total"], "total") != 1 || acctest.Rows(t, found["images"], "images")[0]["name"] != repository {
		t.Errorf("searched by its name = %v", found)
	}
	tags := call(t, "registry_tags", map[string]any{"registry": name, "image": repository, "sizes": true})
	if rows := acctest.Rows(t, tags["tags"], "tags"); acctest.Num(t, tags["total"], "total") != 2 || rows[0]["tag"] != "1" || rows[1]["tag"] != "2" || acctest.Decimal(t, rows[0]["size_mb"], "size_mb") <= 0 {
		t.Errorf("its tags = %v", tags)
	}
	if msg := refused(t, "registry_tags", map[string]any{"registry": name, "image": "acc/no-such-image"}); !strings.Contains(msg, "has no image named acc/no-such-image") {
		t.Errorf("the tags of an image the registry has none of = %q", msg)
	}

	// the other host pulls it, signed in with what Dockhand keeps for the registry
	if pulled := call(t, "image_pull", map[string]any{"env": to, "image": pushed + ":1"}); !acctest.BoolOf(pulled["changed"]) || imageOn(t, to, pushed+":1") == "" {
		t.Errorf("pulled by %s = %v", to, pulled)
	}

	// renamed: said first, then done, and a second go changes nothing
	if dry := call(t, "registry_edit", map[string]any{"registry": name, "name": name + "-two", "dry_run": true}); len(acctest.Strs(t, dry["changes"], "changes")) != 1 || listed()[name] == nil {
		t.Fatalf("a dry run of renaming it = %v", dry)
	}
	if renamed := call(t, "registry_edit", map[string]any{"registry": name, "name": name + "-two"}); len(acctest.Strs(t, renamed["changes"], "changes")) != 1 || renamed["tried"] != nil || listed()[name+"-two"] == nil || listed()[name] != nil {
		t.Fatalf("renaming it = %v", renamed)
	}
	if again := call(t, "registry_edit", map[string]any{"registry": name + "-two", "name": name + "-two"}); len(acctest.Strs(t, again["changes"], "changes")) != 0 {
		t.Errorf("renaming it to what it is = %v", again)
	}
	// given a wrong password it is saved and said not to work; its login forgotten, it is reached and what it holds is refused in words about the
	// registry; then put right
	if wrong := call(t, "registry_edit", map[string]any{"registry": name + "-two", "password": "wrong"}); !strings.Contains(acctest.Str(wrong["note"]), "Dockhand cannot use the registry as it now is") || !acctest.IsBool(acctest.Object(t, wrong["tried"], "tried")["authenticated"], false) {
		t.Errorf("given a wrong password = %v", wrong)
	}
	forgot := call(t, "registry_edit", map[string]any{"registry": name + "-two", "forget_credentials": true, "name": name})
	if row := acctest.Object(t, forgot["registry"], "registry"); row["name"] != name || !acctest.IsBool(row["has_credentials"], false) || row["username"] != nil || len(acctest.Strs(t, forgot["changes"], "changes")) != 2 {
		t.Fatalf("its login forgotten = %v", forgot)
	}
	if msg := refused(t, "registry_images", map[string]any{"registry": name}); !strings.Contains(msg, "turned Dockhand away") || strings.Contains(msg, "API token") {
		t.Errorf("what it holds, asked with no login kept = %q", msg)
	}
	if right := call(t, "registry_edit", map[string]any{"registry": name, "username": world.User, "password": world.Password}); !acctest.IsBool(acctest.Object(t, right["tried"], "tried")["authenticated"], true) || right["note"] != nil || strings.Contains(flat(right), world.Password) {
		t.Fatalf("its login put back = %v", right)
	}

	// a tag deleted: said first, then done, and the tag of the same contents is named as gone with it
	if preview := call(t, "registry_image_delete", map[string]any{"registry": name, "image": repository, "tag": "1"}); acctest.BoolOf(preview["deleted"]) || len(acctest.Strs(t, preview["would_go"], "would_go")) != 1 || acctest.Num(t, call(t, "registry_tags", map[string]any{"registry": name, "image": repository})["total"], "total") != 2 {
		t.Fatalf("a deleting not confirmed = %v", preview)
	}
	gone := call(t, "registry_image_delete", map[string]any{"registry": name, "image": repository, "tag": "1", "confirm": true})
	if !acctest.BoolOf(gone["deleted"]) || strings.Join(acctest.Strs(t, gone["also_gone"], "also_gone"), " ") != "2" {
		t.Errorf("deleting a tag = %v", gone)
	}
	if left := call(t, "registry_tags", map[string]any{"registry": name, "image": repository}); acctest.Num(t, left["total"], "total") != 0 || !strings.Contains(acctest.Str(left["note"]), "has no tags") {
		t.Errorf("its tags once one was deleted = %v", left)
	}
	if msg := refused(t, "registry_image_delete", map[string]any{"registry": name, "image": repository, "tag": "1", "confirm": true}); !strings.Contains(msg, "has no tag 1") {
		t.Errorf("deleting it again = %q", msg)
	}
	// what the hosts already pulled is untouched
	if imageOn(t, to, pushed+":1") == "" {
		t.Errorf("%s lost the image it had pulled when its tag was deleted from the registry", to)
	}

	// forgotten: said first, then done
	if preview := call(t, "registry_delete", map[string]any{"registry": name}); acctest.BoolOf(preview["removed"]) || len(acctest.Strs(t, preview["would_go"], "would_go")) != 1 || listed()[name] == nil {
		t.Fatalf("a removal not confirmed = %v", preview)
	}
	if removed := call(t, "registry_delete", map[string]any{"registry": name, "confirm": true}); !acctest.BoolOf(removed["removed"]) || len(listed()) != before {
		t.Fatalf("removing it = %v, and %d registries are left", removed, len(listed()))
	}
	if msg := refused(t, "registry_delete", map[string]any{"registry": name, "confirm": true}); !strings.Contains(msg, "no registry is named") {
		t.Errorf("removing it again = %q", msg)
	}
}
