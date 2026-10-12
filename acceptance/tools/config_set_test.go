//go:build acceptance

package tools_test

import (
	"strings"
	"testing"

	"github.com/katbyte/dockhand-mcp/lib/testenv"
	"github.com/katbyte/go-kt/mcp/acctest"
)

// A config set is saved with everything one holds, a made-up token among its variables, read with the token hidden and then shown, changed a variable
// and a label at a time with the token never given again, renamed, and removed: what Dockhand would save and should not is refused on the way.
func TestJourneyAConfigSetSavedChangedAndRemoved(t *testing.T) {
	const name, renamed, token = "acc-journey-set", "acc-journey-set-renamed", "acc-not-a-real-token" //nolint:gosec // G101: a made-up value for the throwaway server, which is no credential of anything real
	if world == nil {
		t.Skip(testenv.NotConfigured)
	}
	listed := func(args map[string]any) map[string]map[string]any {
		return named(acctest.Rows(t, call(t, "config_set_list", args)["sets"], "sets"), "name")
	}
	clean := func() {
		for known := range listed(nil) {
			if strings.HasPrefix(strings.ToLower(known), "acc-journey-") {
				suite.Call(t, "config_set_delete", map[string]any{"set": known, "confirm": true})
			}
		}
	}
	clean()
	t.Cleanup(clean)
	before := len(listed(nil))

	// refused before anything is sent, each of which Dockhand would save; said; and saved, with its secret hidden in the answer
	saving := map[string]any{"name": name, "description": "a made-up web server", "env_vars": map[string]any{"MODE": "demo", "API_TOKEN": token}, "labels": map[string]any{"team": "ops"}, "ports": []any{"8080:80", "53/udp"}, "volumes": []any{"data:/var/lib/app", "/srv/conf:/etc/app:ro"}, "restart_policy": "unless-stopped"}
	for what, tc := range map[string]struct {
		more map[string]any
		want string
	}{
		"a port that is no port":      {map[string]any{"ports": []any{"web:80"}}, "is no port of the host"},
		"a variable with no name":     {map[string]any{"env_vars": map[string]any{"": "x"}}, "env_vars names"},
		"a mount with nowhere to go":  {map[string]any{"volumes": []any{"data"}}, "a mount is written source:target"},
		"a restart policy that is no": {map[string]any{"restart_policy": "whenever"}, "restart_policy is"},
	} {
		if msg := refused(t, "config_set_create", over(saving, tc.more)); !strings.Contains(msg, tc.want) {
			t.Errorf("%s = %q, want %q", what, msg, tc.want)
		}
	}
	if dry := call(t, "config_set_create", over(saving, map[string]any{"dry_run": true})); !acctest.IsBool(dry["dry_run"], true) || dry["set"] != nil || len(listed(nil)) != before || strings.Contains(flat(dry), token) {
		t.Fatalf("a dry run of saving a config set = %v", dry)
	}
	made := call(t, "config_set_create", saving)
	set := acctest.Object(t, made["set"], "set")
	if vars := acctest.Object(t, set["env_vars"], "env_vars"); set["name"] != name || vars["MODE"] != "demo" || vars["API_TOKEN"] != "***" || acctest.Num(t, set["hidden"], "hidden") != 1 || strings.Contains(flat(made), token) || set["network_mode"] != "bridge" || set["restart_policy"] != "unless-stopped" || strings.Join(acctest.Strs(t, set["ports"], "ports"), " ") != "8080:80/tcp 53/udp" || strings.Join(acctest.Strs(t, set["volumes"], "volumes"), " ") != "data:/var/lib/app:rw /srv/conf:/etc/app:ro" || made["note"] != nil {
		t.Fatalf("a config set saved = %v", made)
	}
	if msg := refused(t, "config_set_create", map[string]any{"name": name}); !strings.Contains(msg, "a config set is already named "+name) {
		t.Errorf("a second config set by the same name = %q", msg)
	}

	// read: hidden unless asked for, and found by a part of its name
	if row := listed(map[string]any{"name": "JOURNEY-SET"})[name]; row == nil || acctest.Object(t, row["env_vars"], "env_vars")["API_TOKEN"] != "***" || acctest.Object(t, row["labels"], "labels")["team"] != "ops" || row["description"] != "a made-up web server" {
		t.Errorf("the config set as listed = %v", row)
	}
	if row := listed(map[string]any{"name": name, "reveal_secrets": true})[name]; row == nil || acctest.Object(t, row["env_vars"], "env_vars")["API_TOKEN"] != token || row["hidden"] != nil {
		t.Errorf("the config set with its secrets asked for = %v", row)
	}

	// changed a thing at a time: the token is never given again and is kept, which only asking for it shows
	changing := map[string]any{"set": name, "env_vars": map[string]any{"MODE": "live", "EXTRA": "1"}, "remove_labels": []any{"team"}, "ports": []any{"9090:90"}, "description": "", "network_mode": "host"}
	if dry := call(t, "config_set_edit", over(changing, map[string]any{"dry_run": true})); len(acctest.Strs(t, dry["changes"], "changes")) != 5 || acctest.Object(t, acctest.Object(t, dry["set"], "set")["env_vars"], "env_vars")["MODE"] != "demo" {
		t.Fatalf("a dry run of changing the config set = %v", dry)
	}
	changed := call(t, "config_set_edit", changing)
	set = acctest.Object(t, changed["set"], "set")
	if vars := acctest.Object(t, set["env_vars"], "env_vars"); vars["MODE"] != "live" || vars["EXTRA"] != "1" || vars["API_TOKEN"] != "***" || set["labels"] != nil || strings.Join(acctest.Strs(t, set["ports"], "ports"), " ") != "9090:90/tcp" || set["description"] != nil || set["network_mode"] != "host" || set["restart_policy"] != "unless-stopped" || len(acctest.Strs(t, set["volumes"], "volumes")) != 2 || changed["note"] != nil || strings.Join(acctest.Strs(t, changed["changes"], "changes"), "; ") != "forgot its description; set the variables EXTRA, MODE; took out the label team; set its ports, which were 8080:80/tcp, 53/udp, to 9090:90/tcp; changed its network mode from bridge to host" {
		t.Errorf("the config set changed = %v", changed)
	}
	if row := listed(map[string]any{"name": name, "reveal_secrets": true})[name]; acctest.Object(t, row["env_vars"], "env_vars")["API_TOKEN"] != token {
		t.Errorf("the token after a change that did not name it = %v", row)
	}
	if again := call(t, "config_set_edit", changing); len(acctest.Strs(t, again["changes"], "changes")) != 0 || !strings.Contains(acctest.Str(again["note"]), "that way already") {
		t.Errorf("the same change again = %v", again)
	}

	// a variable taken out, everything it mounts taken away, and renamed; by its number it is the same set
	number := acctest.Num(t, set["id"], "id")
	trimmed := call(t, "config_set_edit", map[string]any{"set": number, "name": renamed, "remove_env_vars": []any{"EXTRA", "NOT_THERE"}, "volumes": []any{}})
	if set = acctest.Object(t, trimmed["set"], "set"); set["name"] != renamed || acctest.Object(t, set["env_vars"], "env_vars")["EXTRA"] != nil || set["volumes"] != nil || len(acctest.Strs(t, trimmed["changes"], "changes")) != 3 || listed(nil)[name] != nil {
		t.Errorf("the config set trimmed and renamed = %v", trimmed)
	}
	for what, tc := range map[string]struct {
		args map[string]any
		want string
	}{
		"a set there is none of":     {map[string]any{"set": name, "name": "acc-journey-x"}, "no config set is named or numbered"},
		"an address to publish on":   {map[string]any{"set": renamed, "ports": []any{"127.0.0.1:8080:80"}}, "no place for an address to publish on"},
		"one both set and taken out": {map[string]any{"set": renamed, "env_vars": map[string]any{"MODE": "x"}, "remove_env_vars": []any{"MODE"}}, "is in both env_vars and remove_env_vars"},
	} {
		if msg := refused(t, "config_set_edit", tc.args); !strings.Contains(msg, tc.want) {
			t.Errorf("%s = %q, want %q", what, msg, tc.want)
		}
	}

	// removed: what would go is said first, and then it is gone
	preview := call(t, "config_set_delete", map[string]any{"set": renamed})
	if would := acctest.Strs(t, preview["would_go"], "would_go"); len(would) != 1 || !strings.Contains(would[0], "with its 2 variables, 0 labels, 1 port and 0 mounts") || !acctest.IsBool(preview["removed"], false) || listed(nil)[renamed] == nil {
		t.Fatalf("what removing the config set would take = %v", preview)
	}
	if gone := call(t, "config_set_delete", map[string]any{"set": renamed, "confirm": true}); !acctest.IsBool(gone["removed"], true) {
		t.Errorf("the config set removed = %v", gone)
	}
	if msg := refused(t, "config_set_delete", map[string]any{"set": renamed, "confirm": true}); !strings.Contains(msg, "no config set is named or numbered") {
		t.Errorf("a config set removed twice = %q", msg)
	}
	if after := len(listed(nil)); after != before {
		t.Errorf("%d config sets at the end, want the %d there were", after, before)
	}
}
