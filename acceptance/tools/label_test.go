//go:build acceptance

package tools_test

import (
	"strings"
	"testing"

	"github.com/katbyte/dockhand-mcp/lib/testenv"
	"github.com/katbyte/go-kt/mcp/acctest"
)

// A label is given to both hosts, coloured and renamed, made one with another, taken from each host in turn and removed: what Dockhand would do wrong
// with it on the way - pass over a host there is none of, take a colour away for one it does not know, take a label renamed to itself from every host
// - is refused or never sent.
func TestJourneyALabelGivenColouredRenamedAndRemoved(t *testing.T) {
	const label, other, renamed = "acc-journey-label", "acc-journey-other", "acc-journey-renamed"
	if world == nil {
		t.Skip(testenv.NotConfigured)
	}
	listed := func() map[string]map[string]any {
		return named(acctest.Rows(t, call(t, "label_list", nil)["labels"], "labels"), "label")
	}
	// on is the hosts a label is on, in order, and "" when no host has it
	on := func(name string) string {
		row := listed()[name]
		if row == nil {
			return ""
		}

		return strings.Join(acctest.Strs(t, row["environments"], "environments"), " ")
	}
	clean := func() {
		for name := range listed() {
			if strings.HasPrefix(strings.ToLower(name), "acc-journey-") {
				suite.Call(t, "label_delete", map[string]any{"label": name, "confirm": true})
			}
		}
	}
	clean()
	t.Cleanup(clean)
	before := len(listed())

	// refused before anything is sent; said; and given to both hosts, one by its name and one by its number
	if msg := refused(t, "label_assign", map[string]any{"label": label, "add": []any{testenv.Alpha, "acc-no-such-host"}}); !strings.Contains(msg, "Dockhand would pass over it without a word") {
		t.Errorf("a label for a host there is none of = %q", msg)
	}
	giving := map[string]any{"label": label, "add": []any{testenv.Alpha, world.Env(testenv.Beta)}}
	if dry := call(t, "label_assign", over(giving, map[string]any{"dry_run": true})); !acctest.IsBool(dry["dry_run"], true) || dry["label"] != nil || len(listed()) != before {
		t.Fatalf("a dry run of giving a label = %v", dry)
	}
	given := call(t, "label_assign", giving)
	if row := acctest.Object(t, given["label"], "label"); row["label"] != label || strings.Join(acctest.Strs(t, row["environments"], "environments"), " ") != testenv.Alpha+" "+testenv.Beta || row["color"] != nil || given["note"] != nil {
		t.Fatalf("a label given to both hosts = %v", given)
	}
	if again := call(t, "label_assign", giving); len(acctest.Strs(t, again["changes"], "changes")) != 0 || !strings.Contains(acctest.Str(again["note"]), "nothing to do") {
		t.Errorf("the same label given again = %v", again)
	}
	// in another case it is another label, and is said to be
	twin := call(t, "label_assign", map[string]any{"label": strings.ToUpper(label), "add": []any{testenv.Alpha}})
	if !strings.Contains(acctest.Str(twin["note"]), "is a new label, and another from "+label) || on(strings.ToUpper(label)) != testenv.Alpha {
		t.Errorf("the label in another case = %v", twin)
	}
	if gone := call(t, "label_delete", map[string]any{"label": strings.ToUpper(label), "confirm": true}); !acctest.IsBool(gone["removed"], true) || on(label) != testenv.Alpha+" "+testenv.Beta {
		t.Errorf("that one removed = %v, leaving the first on %q", gone, on(label))
	}

	// coloured: a colour Dockhand does not know is refused, which it would take the colour away for
	if msg := refused(t, "label_edit", map[string]any{"label": label, "color": "red"}); !strings.Contains(msg, "six hexadecimal digits") {
		t.Errorf("a colour that is none = %q", msg)
	}
	if coloured := call(t, "label_edit", map[string]any{"label": label, "color": "#1F9D55"}); acctest.Object(t, coloured["label"], "label")["color"] != "#1F9D55" || coloured["note"] != nil {
		t.Errorf("the label coloured = %v", coloured)
	}
	// its own name and the colour it has are nothing to do, and are not sent: the label is where it was
	if same := call(t, "label_edit", map[string]any{"label": label, "name": label, "color": "#1f9d55"}); len(acctest.Strs(t, same["changes"], "changes")) != 0 || !strings.Contains(acctest.Str(same["note"]), "that way already") || on(label) != testenv.Alpha+" "+testenv.Beta {
		t.Errorf("the label renamed to itself = %v, leaving it on %q", same, on(label))
	}

	// renamed on every host that has it, its colour with it
	if dry := call(t, "label_edit", map[string]any{"label": label, "name": renamed, "dry_run": true}); on(label) == "" || on(renamed) != "" || len(acctest.Strs(t, dry["changes"], "changes")) != 1 {
		t.Fatalf("a dry run of renaming the label = %v", dry)
	}
	moved := call(t, "label_edit", map[string]any{"label": label, "name": renamed})
	if row := acctest.Object(t, moved["label"], "label"); row["label"] != renamed || row["color"] != "#1F9D55" || on(renamed) != testenv.Alpha+" "+testenv.Beta || on(label) != "" {
		t.Fatalf("the label renamed = %v", moved)
	}
	if msg := refused(t, "label_edit", map[string]any{"label": strings.ToUpper(renamed), "name": "acc-journey-x"}); !strings.Contains(msg, "labels differ by their case, and there is "+renamed) {
		t.Errorf("the label named in another case = %q", msg)
	}

	// made one with another: refused until it is asked for, and then on every host that had either
	if only := call(t, "label_assign", map[string]any{"label": other, "add": []any{testenv.Beta}}); on(other) != testenv.Beta {
		t.Fatalf("another label given to one host = %v", only)
	}
	if msg := refused(t, "label_edit", map[string]any{"label": renamed, "name": other}); !strings.Contains(msg, other+" is a label already, on "+testenv.Beta) || on(renamed) == "" {
		t.Errorf("the label renamed to another's name = %q", msg)
	}
	joined := call(t, "label_edit", map[string]any{"label": renamed, "name": other, "merge": true})
	if row := acctest.Object(t, joined["label"], "label"); row["label"] != other || row["color"] != "#1F9D55" || on(other) != testenv.Alpha+" "+testenv.Beta || on(renamed) != "" || joined["note"] != nil || !strings.Contains(acctest.Strs(t, joined["changes"], "changes")[0], "which takes its colour #1F9D55") {
		t.Errorf("the two labels made one = %v", joined)
	}

	// taken from one host and then the other: the last leaves the colour behind, which is said and listed
	if taken := call(t, "label_assign", map[string]any{"label": other, "remove": []any{testenv.Alpha}}); on(other) != testenv.Beta || taken["note"] != nil {
		t.Errorf("the label taken from one host = %v", taken)
	}
	last := call(t, "label_assign", map[string]any{"label": other, "remove": []any{testenv.Alpha, testenv.Beta}})
	if row := listed()[other]; !strings.Contains(acctest.Str(last["note"]), "no environment has it now, and Dockhand keeps the colour #1F9D55 for it") || row == nil || row["color"] != "#1F9D55" || on(other) != "" {
		t.Errorf("the label taken from its last host = %v, listed as %v", last, row)
	}
	if msg := refused(t, "label_edit", map[string]any{"label": other, "color": ""}); !strings.Contains(msg, "no environment has the label "+other) {
		t.Errorf("changing a label no host has = %q", msg)
	}

	// removed: what would go is said first, and then the colour kept for it is gone as well
	if preview := call(t, "label_delete", map[string]any{"label": other}); strings.Join(acctest.Strs(t, preview["would_go"], "would_go"), " | ") != "the colour #1F9D55 kept for it" || listed()[other] == nil {
		t.Fatalf("what removing the label would take = %v", preview)
	}
	if gone := call(t, "label_delete", map[string]any{"label": other, "confirm": true}); !acctest.IsBool(gone["removed"], true) || !strings.Contains(acctest.Str(gone["note"]), "the colour kept for it was cleared") {
		t.Errorf("the label removed = %v", gone)
	}
	// and one that hosts have, removed from all of them at once
	call(t, "label_assign", map[string]any{"label": label, "add": []any{testenv.Alpha, testenv.Beta}})
	if preview := call(t, "label_delete", map[string]any{"label": label}); strings.Join(acctest.Strs(t, preview["would_go"], "would_go"), " | ") != "the label "+label+" from "+testenv.Alpha+", "+testenv.Beta {
		t.Errorf("what removing a label two hosts have would take = %v", preview)
	}
	if gone := call(t, "label_delete", map[string]any{"label": label, "confirm": true}); !acctest.IsBool(gone["removed"], true) || gone["note"] != "it was taken from 2 environments" {
		t.Errorf("a label two hosts have, removed = %v", gone)
	}
	if msg := refused(t, "label_delete", map[string]any{"label": label, "confirm": true}); !strings.Contains(msg, "no label is") {
		t.Errorf("a label removed twice = %q", msg)
	}
	if after := len(listed()); after != before {
		t.Errorf("%d labels at the end, want the %d there were", after, before)
	}
}
