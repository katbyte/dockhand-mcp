//go:build integration

package acceptance

import (
	"strings"
	"testing"

	"github.com/katbyte/dockhand-mcp/lib/testenv"
	"github.com/katbyte/go-kt/mcp/acctest"
)

// Two tags are made and given to a container and to a stack, one of them is renamed and recoloured with everything that has it keeping it, the
// container and a stack made for the purpose are removed and their names seen to hold their tags still, which are then taken from them, and the tags
// are removed, with what has one said first.
func TestJourneyTagsMadeGivenChangedAndRemoved(t *testing.T) {
	const tag, other, renamed, stack = "acc-journey-tag", "acc-journey-other", "acc-journey-renamed", "acc-journey-tagged-stack"
	host := testenv.Alpha
	container := started(t, host, "acc-journey-tagged")

	listed := func(args map[string]any) map[string]map[string]any {
		return named(acctest.Rows(t, call(t, "tag_list", args)["tags"], "tags"), "name")
	}
	// on is what a tag is on at the host, as "containers a b | stacks c"
	on := func(name string) string {
		rows := acctest.Rows(t, acctest.OrEmptyList(listed(map[string]any{"env": host})[name]["on"]), "on")
		if len(rows) == 0 {
			return ""
		}

		return "containers " + strings.Join(acctest.Strs(t, acctest.OrEmptyList(rows[0]["containers"]), "containers"), " ") + " | stacks " + strings.Join(acctest.Strs(t, acctest.OrEmptyList(rows[0]["stacks"]), "stacks"), " ")
	}
	has := func(what, name string) string {
		// a dry run of taking nothing is how what a name has is read
		said := call(t, "tag_assign", map[string]any{"env": host, what: name, "remove": []any{other}, "dry_run": true})

		return strings.Join(acctest.Strs(t, said["tags"], "tags"), " ")
	}
	clean := func() {
		for name := range listed(nil) {
			if strings.HasPrefix(name, "acc-journey-") {
				suite.Call(t, "tag_delete", map[string]any{"tag": name, "confirm": true})
			}
		}
		if stackNamed(t, stack) != nil {
			suite.Call(t, "stack_delete", map[string]any{"env": host, "stack": stack, "confirm": true})
		}
	}
	clean()
	t.Cleanup(clean)
	before := len(listed(nil))

	// refused before anything is sent; said; and made, the second in the colour Dockhand gives one that asks for none
	if msg := refused(t, "tag_create", map[string]any{"name": tag, "color": "yellow"}); !strings.Contains(msg, "a tag is slate, red") {
		t.Errorf("a tag in a colour Dockhand has not = %q", msg)
	}
	if dry := call(t, "tag_create", map[string]any{"name": tag, "color": "teal", "dry_run": true}); !acctest.IsBool(dry["dry_run"], true) || dry["tag"] != nil || len(listed(nil)) != before {
		t.Fatalf("a dry run of making a tag = %v", dry)
	}
	made := call(t, "tag_create", map[string]any{"name": tag, "color": "teal", "icon": "globe"})
	if row := acctest.Object(t, made["tag"], "tag"); row["name"] != tag || row["color"] != "teal" || row["icon"] != "globe" || made["note"] != nil {
		t.Fatalf("a tag made = %v", made)
	}
	if msg := refused(t, "tag_create", map[string]any{"name": strings.ToUpper(tag), "color": "red"}); !strings.Contains(msg, "a tag is already named "+tag) {
		t.Errorf("a second tag by the same name = %q", msg)
	}
	if plain := acctest.Object(t, call(t, "tag_create", map[string]any{"name": other})["tag"], "tag"); plain["color"] != "slate" || plain["icon"] != nil {
		t.Errorf("a tag made with a name alone = %v", plain)
	}
	if rows := listed(nil); len(rows) != before+2 || rows[tag]["on"] != nil {
		t.Errorf("the tags once two are made = %v", rows)
	}

	// given to a container: said first, then set whole, then one taken and given again
	giving := map[string]any{"env": host, "container": container, "tags": []any{tag, strings.ToUpper(other)}}
	if dry := call(t, "tag_assign", over(giving, map[string]any{"dry_run": true})); !acctest.IsBool(dry["dry_run"], true) || len(acctest.Strs(t, dry["tags"], "tags")) != 0 || acctest.Strs(t, dry["changes"], "changes")[0] != "would give it "+other+", "+tag || has("container", container) != "" {
		t.Fatalf("a dry run of giving a container two tags = %v", dry)
	}
	if set := call(t, "tag_assign", giving); strings.Join(acctest.Strs(t, set["tags"], "tags"), " ") != other+" "+tag || set["container"] != container || set["env"] != host || set["note"] != nil {
		t.Fatalf("a container given two tags = %v", set)
	}
	if again := call(t, "tag_assign", giving); len(acctest.Strs(t, again["changes"], "changes")) != 0 || !strings.Contains(acctest.Str(again["note"]), "has exactly those already") {
		t.Errorf("the same tags given again = %v", again)
	}
	if taken := call(t, "tag_assign", map[string]any{"env": host, "container": container, "remove": []any{other}}); strings.Join(acctest.Strs(t, taken["tags"], "tags"), " ") != tag || acctest.Strs(t, taken["changes"], "changes")[0] != "took "+other+" from it" {
		t.Errorf("one tag taken from the container = %v", taken)
	}
	// and to a stack, beside what it has
	if set := call(t, "tag_assign", map[string]any{"env": host, "stack": webStack, "add": []any{tag}}); strings.Join(acctest.Strs(t, set["tags"], "tags"), " ") != tag || set["stack"] != webStack || set["container"] != nil {
		t.Errorf("a stack given a tag = %v", set)
	}
	if where := on(tag); where != "containers "+container+" | stacks "+webStack || on(other) != "" {
		t.Errorf("what the tag is on = %q, and the other %q", where, on(other))
	}
	if elsewhere := listed(map[string]any{"env": testenv.Beta})[tag]; elsewhere == nil || elsewhere["on"] != nil {
		t.Errorf("the tag as the other host has it = %v", elsewhere)
	}

	// what Dockhand would take and should not: a name nothing has, a tag there is none of, and no environment
	for what, tc := range map[string]struct {
		args map[string]any
		want string
	}{
		"a name no container has": {map[string]any{"env": host, "container": "acc-journey-no-such-container", "add": []any{tag}}, "Dockhand would save tags under the name all the same"},
		"a name no stack has":     {map[string]any{"env": host, "stack": "acc-journey-no-such-stack", "add": []any{tag}}, "no stack is named"},
		"a tag there is none of":  {map[string]any{"env": host, "container": container, "add": []any{"acc-journey-no-such-tag"}}, "no tag is named or numbered"},
		"no environment":          {map[string]any{"container": container, "add": []any{tag}}, "env is required"},
	} {
		if msg := refused(t, "tag_assign", tc.args); !strings.Contains(msg, tc.want) {
			t.Errorf("%s = %q, want %q", what, msg, tc.want)
		}
	}
	if has("container", container) != tag {
		t.Errorf("after what was refused the container has %q, want %s alone", has("container", container), tag)
	}

	// renamed, recoloured and its icon taken away: everything that had it has it still
	for what, tc := range map[string]struct {
		args map[string]any
		want string
	}{
		"another tag's name":  {map[string]any{"tag": tag, "name": strings.ToUpper(other)}, "another tag is named " + other},
		"a colour it has not": {map[string]any{"tag": tag, "color": "brown"}, "a tag is slate, red"},
		"a tag there is none": {map[string]any{"tag": 999999, "name": "acc-journey-nobody"}, "no tag is named or numbered"},
	} {
		if msg := refused(t, "tag_edit", tc.args); !strings.Contains(msg, tc.want) {
			t.Errorf("%s = %q, want %q", what, msg, tc.want)
		}
	}
	changing := map[string]any{"tag": tag, "name": renamed, "color": "pink", "icon": ""}
	if dry := call(t, "tag_edit", over(changing, map[string]any{"dry_run": true})); len(acctest.Strs(t, dry["changes"], "changes")) != 3 || listed(nil)[tag] == nil {
		t.Fatalf("a dry run of changing the tag = %v", dry)
	}
	changed := call(t, "tag_edit", changing)
	if row := acctest.Object(t, changed["tag"], "tag"); row["name"] != renamed || row["color"] != "pink" || row["icon"] != nil || changed["note"] != nil || len(acctest.Strs(t, changed["changes"], "changes")) != 3 {
		t.Fatalf("the tag changed = %v", changed)
	}
	if again := call(t, "tag_edit", map[string]any{"tag": renamed, "color": "PINK"}); len(acctest.Strs(t, again["changes"], "changes")) != 0 || !strings.Contains(acctest.Str(again["note"]), "that way already") {
		t.Errorf("the same change again = %v", again)
	}
	if where := on(renamed); where != "containers "+container+" | stacks "+webStack || listed(nil)[tag] != nil {
		t.Errorf("what the tag is on once renamed = %q", where)
	}

	// the tags are the name's: the container removed through Dockhand, its name holds them, takes no more, and gives them up
	if gone := call(t, "container_delete", map[string]any{"env": host, "container": container, "confirm": true, "force": true}); !acctest.IsBool(gone["removed"], true) {
		t.Fatalf("the container removed = %v", gone)
	}
	if where := on(renamed); where != "containers "+container+" | stacks "+webStack {
		t.Errorf("what the tag is on once the container is gone = %q: its name used to hold it still", where)
	}
	if msg := refused(t, "tag_assign", map[string]any{"env": host, "container": container, "add": []any{other}}); !strings.Contains(msg, "the name holds the tags "+renamed+" from when one was") {
		t.Errorf("a tag given to a name whose container is gone = %q", msg)
	}
	if taken := call(t, "tag_assign", map[string]any{"env": host, "container": container, "tags": []any{}}); len(acctest.Strs(t, taken["tags"], "tags")) != 0 || !strings.Contains(acctest.Str(taken["note"]), "no container is named "+container) {
		t.Errorf("the tags taken from a name whose container is gone = %v", taken)
	}

	// a stack made, tagged and removed through Dockhand leaves its tags under its name as well
	if made := call(t, "stack_create", map[string]any{"env": host, "name": stack, "compose": "services:\n  one:\n    image: " + world.Image + "\n    command: [\"sleep\", \"3600\"]\n", "start": false}); stackNamed(t, stack) == nil {
		t.Fatalf("a stack made and not brought up = %v", made)
	}
	if set := call(t, "tag_assign", map[string]any{"env": host, "stack": stack, "tags": []any{other}}); strings.Join(acctest.Strs(t, set["tags"], "tags"), " ") != other {
		t.Errorf("the new stack given a tag = %v", set)
	}
	if gone := call(t, "stack_delete", map[string]any{"env": host, "stack": stack, "confirm": true}); stackNamed(t, stack) != nil {
		t.Fatalf("the stack removed = %v", gone)
	}
	if where := on(other); where != "containers  | stacks "+stack {
		t.Errorf("what the other tag is on once its stack is gone = %q: the stack's name used to hold it still", where)
	}

	// what has a tag is said before it goes; removed, it is taken from everything
	preview := call(t, "tag_delete", map[string]any{"tag": renamed})
	if would := strings.Join(acctest.Strs(t, preview["would_go"], "would_go"), " | "); would != "the tag "+renamed+" | on "+host+", its place on 1 stack, "+webStack || !acctest.IsBool(preview["removed"], false) || listed(nil)[renamed] == nil {
		t.Fatalf("what removing the tag would take = %v", preview)
	}
	if gone := call(t, "tag_delete", map[string]any{"tag": renamed, "confirm": true}); !acctest.IsBool(gone["removed"], true) || !strings.Contains(acctest.Str(gone["note"]), "it was on 0 containers and 1 stack") || has("stack", webStack) != "" {
		t.Errorf("the tag removed = %v, leaving the stack with %q", gone, has("stack", webStack))
	}
	if gone := call(t, "tag_delete", map[string]any{"tag": other, "confirm": true}); !acctest.IsBool(gone["removed"], true) || !strings.Contains(acctest.Str(gone["note"]), "it was on 0 containers and 1 stack") {
		t.Errorf("the other tag removed = %v", gone)
	}
	if msg := refused(t, "tag_delete", map[string]any{"tag": other, "confirm": true}); !strings.Contains(msg, "no tag is named or numbered") {
		t.Errorf("a tag removed twice = %q", msg)
	}
	if after := len(listed(nil)); after != before {
		t.Errorf("%d tags at the end, want the %d there were", after, before)
	}
}
