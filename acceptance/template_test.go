//go:build integration

package acceptance

import (
	"context"
	"strings"
	"testing"

	"github.com/katbyte/dockhand-mcp/lib/testenv"
	"github.com/katbyte/go-kt/mcp/acctest"
)

// journeyCatalogue is a list of templates in the form Portainer's take, made up: a container template with a volume it names no place for and a
// variable it leaves empty, and a stack template whose repository there is none of.
const journeyCatalogue = `{"version":"2","templates":[{"type":1,"title":"Acc Web","name":"acc-web","description":"A made-up web server","categories":["Acc"],"platform":"linux","logo":"","image":"registry.invalid/acc/web:1","ports":["8080:80/tcp"],"volumes":[{"container":"/data"},{"container":"/etc/web","bind":"/srv/web"}],"env":[{"name":"MODE","label":"Mode","default":"demo"},{"name":"API_KEY","label":"Key"}],"restart_policy":"unless-stopped","note":"A note."},{"type":3,"title":"Acc Stack","name":"acc-stack","description":"A made-up stack","categories":["Acc"],"platform":"linux","repository":{"url":"https://repository.invalid/nobody/nothing","stackfile":"compose.yaml"}}]}`

// A source of templates is added, with a list of them served on the test network, and its templates found, read in full and written out as a compose
// file; a source whose address answers nothing is said to give none; the source is switched off and on, and removed.
func TestJourneyATemplateSourceAddedItsTemplatesReadAndTheSourceRemoved(t *testing.T) {
	const name, empty, served = "acc-journey-templates", "acc-journey-empty", "acc-templates"
	if world == nil {
		t.Skip(testenv.NotConfigured)
	}
	listed := func() map[string]map[string]any {
		return named(acctest.Rows(t, call(t, "template_source_list", nil)["sources"], "sources"), "name")
	}
	found := func(args map[string]any) []string {
		rows := acctest.Rows(t, call(t, "template_list", over(map[string]any{"source": name}, args))["templates"], "templates")
		titles := make([]string, 0, len(rows))
		for _, row := range rows {
			titles = append(titles, acctest.Str(row["title"]))
		}

		return titles
	}
	clean := func() {
		for _, known := range []string{name, empty} {
			if listed()[known] != nil {
				suite.Call(t, "template_source_delete", map[string]any{"source": known, "confirm": true})
			}
		}
	}
	clean()
	address, err := world.Serve(t.Context(), testenv.Alpha, served, "acc-templates.json", journeyCatalogue)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		clean()
		_ = world.Unserve(context.WithoutCancel(t.Context()), testenv.Alpha, served)
	})
	before := listed()

	// refused before anything is sent; said; and added, with what came of it counted
	args := map[string]any{"name": name, "url": address}
	if msg := refused(t, "template_source_create", over(args, map[string]any{"url": strings.TrimPrefix(address, "http://")})); !strings.Contains(msg, "with its scheme") {
		t.Errorf("a source at an address with no scheme = %q", msg)
	}
	if dry := call(t, "template_source_create", over(args, map[string]any{"dry_run": true})); !acctest.IsBool(dry["dry_run"], true) || dry["source"] != nil || len(listed()) != len(before) {
		t.Fatalf("a dry run of adding a source = %v", dry)
	}
	made := call(t, "template_source_create", args)
	if row := acctest.Object(t, made["source"], "source"); row["name"] != name || row["url"] != address || !acctest.IsBool(row["enabled"], true) || row["builtin"] != nil || acctest.Num(t, row["templates"], "templates") != 2 || made["note"] != nil {
		t.Fatalf("a source added = %v", made)
	}
	if msg := refused(t, "template_source_create", over(args, map[string]any{"name": strings.ToUpper(name)})); !strings.Contains(msg, "already has that name or that address") {
		t.Errorf("a second source by the same name = %q", msg)
	}
	// one whose address answers nothing Dockhand can read is taken all the same, which only the count of its templates tells
	nothing := call(t, "template_source_create", map[string]any{"name": empty, "url": strings.TrimSuffix(address, "acc-templates.json") + "acc-no-such-file.json"})
	if acctest.Num(t, acctest.Object(t, nothing["source"], "source")["templates"], "templates") != 0 || !strings.Contains(acctest.Str(nothing["note"]), "no template comes of it") {
		t.Errorf("a source whose address answers nothing = %v", nothing)
	}
	if gone := call(t, "template_source_delete", map[string]any{"source": empty, "confirm": true}); !acctest.IsBool(gone["removed"], true) {
		t.Errorf("that source removed = %v", gone)
	}

	// its templates found, by source, by a word and by kind
	if all := found(nil); strings.Join(all, ", ") != "Acc Stack, Acc Web" {
		t.Fatalf("the templates of the source = %v", all)
	}
	if web := found(map[string]any{"search": "WEB server"}); strings.Join(web, ", ") != "Acc Web" {
		t.Errorf("the templates of the source with two words in them = %v", web)
	}
	if stacks := found(map[string]any{"type": "stack", "category": "acc"}); strings.Join(stacks, ", ") != "Acc Stack" {
		t.Errorf("the stack templates of the source = %v", stacks)
	}

	// one in full, with the compose file Dockhand writes for it, and what is wrong with that file said
	web := call(t, "template_get", map[string]any{"template": "acc web", "source": name})
	volumes, variables := acctest.Rows(t, web["volumes"], "volumes"), acctest.Rows(t, web["variables"], "variables")
	if web["title"] != "Acc Web" || web["type"] != "container" || web["image"] != "registry.invalid/acc/web:1" || web["restart_policy"] != "unless-stopped" || web["about"] != "A note." || len(volumes) != 2 || volumes[0]["host"] != nil || volumes[1]["host"] != "/srv/web" || len(variables) != 2 || variables[0]["default"] != "demo" || variables[1]["default"] != nil {
		t.Errorf("the container template in full = %v", web)
	}
	for _, want := range []string{"image: registry.invalid/acc/web:1", "/srv/web:/etc/web", "MODE=demo", `"8080:80/tcp"`} {
		if !strings.Contains(acctest.Str(web["compose"]), want) {
			t.Errorf("the compose file of the container template lacks %q:\n%s", want, web["compose"])
		}
	}
	if !strings.Contains(acctest.Str(web["note"]), `wrote "undefined" as where 1 of its volumes`) {
		t.Errorf("the note on a compose file with a volume of no place = %q", web["note"])
	}
	// by its id it is the same one; a stack template's compose file is fetched from its repository, which here there is none of
	if byID := call(t, "template_get", map[string]any{"template": acctest.Str(web["id"])}); byID["title"] != "Acc Web" || byID["compose"] != web["compose"] {
		t.Errorf("the template by its id = %v", byID)
	}
	if msg := refused(t, "template_get", map[string]any{"template": "Acc Stack", "source": name}); !strings.Contains(msg, "Dockhand fetches a stack template's from its repository, https://repository.invalid/nobody/nothing, and could not") {
		t.Errorf("a stack template whose repository is not there = %q", msg)
	}

	// switched off its templates go from the list at once, twice over; switched on they are back
	off := call(t, "template_source_edit", map[string]any{"source": name, "enabled": false})
	if row := acctest.Object(t, off["source"], "source"); !acctest.IsBool(row["enabled"], false) || acctest.Num(t, row["templates"], "templates") != 0 || len(found(nil)) != 0 {
		t.Errorf("the source switched off = %v, with %v still listed", off, found(nil))
	}
	if again := call(t, "template_source_edit", map[string]any{"source": name, "enabled": false}); len(acctest.Strs(t, again["changes"], "changes")) != 0 || !strings.Contains(acctest.Str(again["note"]), "that way already") {
		t.Errorf("the same a second time = %v", again)
	}
	if on := call(t, "template_source_edit", map[string]any{"source": name, "enabled": true}); acctest.Num(t, acctest.Object(t, on["source"], "source")["templates"], "templates") != 2 {
		t.Errorf("the source switched on again = %v", on)
	}
	if msg := refused(t, "template_source_edit", map[string]any{"source": "acc-no-such-source", "enabled": false}); !strings.Contains(msg, "no template source is named or numbered") {
		t.Errorf("a source there is none of, changed = %q", msg)
	}

	// one of Dockhand's own is said to be that before it is removed, and is not removed here
	for known, row := range before {
		if acctest.BoolOf(row["builtin"]) {
			if would := call(t, "template_source_delete", map[string]any{"source": known}); !acctest.IsBool(would["removed"], false) || !strings.Contains(acctest.Str(would["note"]), "nothing brings it back as that") || listed()[known] == nil {
				t.Errorf("a removal of one of Dockhand's own, not confirmed = %v", would)
			}

			break
		}
	}

	// removed: said first, then done, and the list is as it was
	if would := call(t, "template_source_delete", map[string]any{"source": name}); !acctest.IsBool(would["removed"], false) || len(acctest.Strs(t, would["would_go"], "would_go")) != 1 || listed()[name] == nil {
		t.Fatalf("a removal not confirmed = %v", would)
	}
	if gone := call(t, "template_source_delete", map[string]any{"source": name, "confirm": true}); !acctest.IsBool(gone["removed"], true) || gone["note"] != nil {
		t.Errorf("the source removed = %v", gone)
	}
	if msg := refused(t, "template_source_delete", map[string]any{"source": name, "confirm": true}); !strings.Contains(msg, "no template source is named or numbered") {
		t.Errorf("removing it a second time = %q", msg)
	}
	if len(listed()) != len(before) || len(found(nil)) != 0 {
		t.Errorf("%d sources at the end and %v of the removed one's templates, want the %d there were and none", len(listed()), found(nil), len(before))
	}
}
