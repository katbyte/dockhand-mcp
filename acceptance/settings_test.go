//go:build integration

package acceptance

import (
	"strings"
	"testing"

	"github.com/katbyte/dockhand-mcp/lib/testenv"
	"github.com/katbyte/go-kt/mcp/acctest"
)

// The Dockhand's own settings are read, all of them and by a part of a name; changed, a number that is 0, a list and the ones for newer versions
// among them; found to be so already the second time; given values Dockhand would change or pass over, which are put back and said; and put back as
// they were.
func TestJourneySettingsReadChangedAndPutBack(t *testing.T) {
	if world == nil {
		t.Skip(testenv.NotConfigured)
	}
	settings := func(part string) map[string]any {
		out := map[string]any{}
		for _, row := range acctest.Rows(t, call(t, "settings_get", map[string]any{"name": part})["settings"], "settings") {
			out[acctest.Str(row["name"])] = row["value"]
		}

		return out
	}

	// every one of them, the general ones and the four for newer versions, by the names the API gives them
	all := call(t, "settings_get", nil)
	was := settings("")
	if acctest.Num(t, all["total"], "total") != 63 || len(was) != 63 || all["note"] != nil || acctest.Str(was["eventCleanupCron"]) == "" || acctest.Str(was["defaultTimezone"]) == "" || acctest.Str(was["semver.maxBump"]) == "" {
		t.Fatalf("every setting = %v", all)
	}
	if crons := settings("CRON"); len(crons) != 5 {
		t.Errorf("the settings with cron in their name = %v", crons)
	}
	if none := call(t, "settings_get", map[string]any{"name": "no-such-setting"}); acctest.Num(t, none["total"], "total") != 0 || !strings.Contains(acctest.Str(none["note"]), "no setting has") {
		t.Errorf("a name no setting has = %v", none)
	}
	// put back whatever happens: each setting this journey touches, as it was
	touched := []string{"eventRetentionDays", "scanRetentionGraceDays", "scanRetentionKeep", "externalStackPaths", "primaryStackLocation", "defaultTimezone", "showWhatsNew", "logMaxLines", "downloadFormat", "semver.enabled", "semver.maxBump"}
	back := map[string]any{}
	for _, name := range touched {
		back[name] = was[name]
	}
	t.Cleanup(func() { suite.Call(t, "settings_set", map[string]any{"settings": back}) })

	// refused before anything is sent, where Dockhand would save what breaks it
	for what, tc := range map[string]struct {
		settings map[string]any
		want     string
	}{
		"a setting there is none of":   {map[string]any{"noSuchSetting": 1}, "there is no setting named"},
		"a schedule that is none":      {map[string]any{"eventCleanupCron": "every night"}, "the job it times would then never run"},
		"a time zone there is none of": {map[string]any{"defaultTimezone": "Nowhere/Atall"}, "it is the name of a time zone"},
		"a number below nothing":       {map[string]any{"eventRetentionDays": -1}, "it is a whole number, none below 0"},
		"a step that is none":          {map[string]any{"semver.maxBump": "none"}, "it is major, minor, patch"},
	} {
		if msg := refused(t, "settings_set", map[string]any{"settings": tc.settings}); !strings.Contains(msg, tc.want) {
			t.Errorf("%s = %q, want %q", what, msg, tc.want)
		}
	}
	if now := settings(""); now["eventCleanupCron"] != was["eventCleanupCron"] || now["defaultTimezone"] != was["defaultTimezone"] || now["semver.maxBump"] != was["semver.maxBump"] {
		t.Fatalf("a refused change changed something: %v", now)
	}

	// said, then done: a number, a number that is 0, a list, a text, a time zone, a switch written as text, and two of the ones for newer versions
	days := acctest.Num(t, was["eventRetentionDays"], "eventRetentionDays") + 1
	change := map[string]any{"eventRetentionDays": days, "scanRetentionGraceDays": 0, "externalStackPaths": []any{"/srv/one", "/srv/two"}, "primaryStackLocation": "/srv/stacks", "defaultTimezone": "Europe/Paris", "showWhatsNew": "false", "semver.enabled": true, "semver.maxBump": "minor"}
	if dry := call(t, "settings_set", map[string]any{"settings": change, "dry_run": true}); !acctest.IsBool(dry["dry_run"], true) || len(acctest.Strs(t, dry["changes"], "changes")) != 8 || dry["after"] != nil || settings("eventRetention")["eventRetentionDays"] != was["eventRetentionDays"] {
		t.Fatalf("a dry run of changing settings = %v", dry)
	}
	set := call(t, "settings_set", map[string]any{"settings": change})
	after := acctest.Object(t, set["after"], "after")
	if len(acctest.Strs(t, set["changes"], "changes")) != 8 || set["not_taken"] != nil || acctest.Num(t, after["eventRetentionDays"], "eventRetentionDays") != days || acctest.Num(t, after["scanRetentionGraceDays"], "scanRetentionGraceDays") != 0 || len(acctest.Strs(t, after["externalStackPaths"], "externalStackPaths")) != 2 || after["primaryStackLocation"] != "/srv/stacks" || after["defaultTimezone"] != "Europe/Paris" || !acctest.IsBool(after["showWhatsNew"], false) || !acctest.IsBool(after["semver.enabled"], true) || after["semver.maxBump"] != "minor" {
		t.Fatalf("settings changed = %v", set)
	}
	if now := settings(""); acctest.Num(t, now["scanRetentionGraceDays"], "scanRetentionGraceDays") != 0 || now["defaultTimezone"] != "Europe/Paris" || now["semver.maxBump"] != "minor" || !acctest.IsBool(now["semver.matchFlavor"], acctest.BoolOf(was["semver.matchFlavor"])) {
		t.Errorf("the settings read back = %v", now)
	}
	if again := call(t, "settings_set", map[string]any{"settings": change}); len(acctest.Strs(t, again["changes"], "changes")) != 0 || !strings.Contains(acctest.Str(again["note"]), "that way already") {
		t.Errorf("the same change a second time = %v", again)
	}

	// what Dockhand would change or pass over is said, and is as it was afterwards: a number it raises, one it brings down and a word it does not
	// know
	mangled := call(t, "settings_set", map[string]any{"settings": map[string]any{"scanRetentionKeep": 0, "logMaxLines": 5000, "downloadFormat": "no-such-format"}})
	notTaken := strings.Join(acctest.Strs(t, mangled["not_taken"], "not_taken"), "\n")
	if len(acctest.Strs(t, mangled["changes"], "changes")) != 0 || !strings.Contains(notTaken, "scanRetentionKeep: asked for 0, and Dockhand made it 1; it was put back to") || !strings.Contains(notTaken, "logMaxLines: asked for 5000, and Dockhand") || !strings.Contains(notTaken, `downloadFormat: asked for "no-such-format", and Dockhand left it`) || !strings.Contains(acctest.Str(mangled["note"]), "did not take every value") {
		t.Errorf("settings Dockhand does not take as given = %v", mangled)
	}
	if now := settings(""); now["scanRetentionKeep"] != was["scanRetentionKeep"] || now["logMaxLines"] != was["logMaxLines"] || now["downloadFormat"] != was["downloadFormat"] {
		t.Errorf("the settings Dockhand did not take, afterwards = %v, %v and %v, want them as they were", now["scanRetentionKeep"], now["logMaxLines"], now["downloadFormat"])
	}

	// put back, and every setting is as it was
	call(t, "settings_set", map[string]any{"settings": back})
	now := settings("")
	for name, value := range was {
		if flatValue(now[name]) != flatValue(value) {
			t.Errorf("%s is %v at the end, and was %v", name, now[name], value)
		}
	}
}

// flatValue is a setting's value as text, to compare one that is a list.
func flatValue(value any) string {
	return flat(map[string]any{"value": value})
}
