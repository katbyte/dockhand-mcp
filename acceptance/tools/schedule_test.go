//go:build acceptance

package tools_test

import (
	"maps"
	"strings"
	"testing"

	"github.com/katbyte/dockhand-mcp/lib/testenv"
	"github.com/katbyte/go-kt/mcp/acctest"
)

// What Dockhand does by itself is found, run before its time, read back with what it logged, switched off and on, and removed: a container kept on
// the newest image, on the host Dockhand reaches by its socket and on the one behind an agent, and then Dockhand's own jobs.
func TestJourneyWhatDockhandDoesByItselfRunSwitchedAndRemoved(t *testing.T) {
	const kind = "container-update"
	for _, host := range []string{testenv.Alpha, testenv.Beta} {
		name := ran(t, host, "acc-sched-"+host)
		t.Cleanup(func() {
			suite.Call(t, "container_autoupdate_set", map[string]any{"env": host, "container": name, "enabled": false})
		})
		if set := call(t, "container_autoupdate_set", map[string]any{"env": host, "container": name, "enabled": true, "cron": "0 4 * * *"}); !acctest.IsBool(set["enabled"], true) {
			t.Fatalf("%s: keeping %s on the newest image = %v", host, name, set)
		}

		// found among what Dockhand does for the host, by its type, with when it next runs and no run yet
		found := named(acctest.Rows(t, call(t, "schedule_list", map[string]any{"env": host, "type": kind})["schedules"], "schedules"), "name")["Update container: "+name]
		if found == nil || found["env"] != host || !acctest.IsBool(found["enabled"], true) || found["cron"] != "0 4 * * *" || found["next_run"] == nil || found["last_run"] != nil || found["system"] != nil {
			t.Fatalf("%s: the schedule as listed = %v", host, found)
		}
		id := acctest.Num(t, found["id"], "id")
		one := func(more map[string]any) map[string]any {
			args := map[string]any{"type": kind, "id": id}
			maps.Copy(args, more)

			return args
		}
		listed := func() map[string]any {
			for _, row := range acctest.Rows(t, call(t, "schedule_list", map[string]any{"env": host, "type": kind, "hide_system": true})["schedules"], "schedules") {
				if acctest.Num(t, row["id"], "id") == id {
					return row
				}
			}

			return nil
		}

		// run before its time: said first, then run and waited for, and the run read back with what it logged
		if dry := call(t, "schedule_run", one(map[string]any{"dry_run": true})); len(acctest.Strs(t, dry["changes"], "changes")) != 1 || dry["run"] != nil || acctest.Num(t, call(t, "schedule_history", one(nil))["total"], "total") != 0 {
			t.Fatalf("%s: a dry run of running it = %v", host, dry)
		}
		did := call(t, "schedule_run", one(nil))
		run := acctest.Object(t, did["run"], "run")
		if acctest.BoolOf(did["running"]) || run["type"] != kind || acctest.Num(t, run["id"], "id") != id || run["name"] != name || run["env"] != host || run["triggered_by"] != "manual" || run["status"] == "running" || run["status"] == "queued" || did["name"] != "Update container: "+name {
			t.Fatalf("%s: running it now = %v", host, did)
		}
		number := acctest.Num(t, run["run"], "run")
		history := call(t, "schedule_history", one(map[string]any{"env": host, "triggered_by": "manual", "since": "10m"}))
		if rows := acctest.Rows(t, history["runs"], "runs"); acctest.Num(t, history["total"], "total") != 1 || acctest.Num(t, rows[0]["run"], "run") != number || rows[0]["status"] != run["status"] {
			t.Errorf("%s: its runs = %v", host, history)
		}
		if none := call(t, "schedule_history", one(map[string]any{"triggered_by": "cron"})); acctest.Num(t, none["total"], "total") != 0 || !strings.Contains(acctest.Str(none["note"]), "nothing matched") {
			t.Errorf("%s: its runs set off by its schedule = %v", host, none)
		}
		if full := call(t, "schedule_history", map[string]any{"run": number}); !strings.Contains(acctest.Str(full["log"]), "Checking container: "+name) || acctest.Num(t, acctest.Rows(t, full["runs"], "runs")[0]["run"], "run") != number {
			t.Errorf("%s: the run in full = %v", host, full)
		}
		if last := acctest.Object(t, listed()["last_run"], "last_run"); acctest.Num(t, last["run"], "run") != number {
			t.Errorf("%s: the schedule's latest run = %v", host, last)
		}

		// switched off: said first, then done and read back, and a second go changes nothing; then on again
		if dry := call(t, "schedule_set", one(map[string]any{"enabled": false, "dry_run": true})); len(acctest.Strs(t, dry["changes"], "changes")) != 1 || !acctest.IsBool(listed()["enabled"], true) {
			t.Fatalf("%s: a dry run of switching it off = %v", host, dry)
		}
		if off := call(t, "schedule_set", one(map[string]any{"enabled": false})); len(acctest.Strs(t, off["changes"], "changes")) != 1 || !acctest.IsBool(off["enabled"], false) || off["next_run"] != nil || !acctest.IsBool(listed()["enabled"], false) {
			t.Fatalf("%s: switching it off = %v", host, off)
		}
		if again := call(t, "schedule_set", one(map[string]any{"enabled": false})); len(acctest.Strs(t, again["changes"], "changes")) != 0 || !strings.Contains(acctest.Str(again["note"]), "switched off already") {
			t.Errorf("%s: switching it off again = %v", host, again)
		}
		if on := call(t, "schedule_set", one(map[string]any{"enabled": true})); len(acctest.Strs(t, on["changes"], "changes")) != 1 || !acctest.IsBool(on["enabled"], true) || on["next_run"] == nil {
			t.Errorf("%s: switching it on again = %v", host, on)
		}

		// the record of its run removed: said first, then done, and a second go finds none
		if preview := call(t, "schedule_history_delete", map[string]any{"run": number}); acctest.BoolOf(preview["removed"]) || acctest.Num(t, call(t, "schedule_history", one(nil))["total"], "total") != 1 {
			t.Fatalf("%s: a removal of the run's record not confirmed = %v", host, preview)
		}
		if gone := call(t, "schedule_history_delete", map[string]any{"run": number, "confirm": true}); !acctest.BoolOf(gone["removed"]) || acctest.Num(t, call(t, "schedule_history", one(nil))["total"], "total") != 0 {
			t.Errorf("%s: removing the run's record = %v", host, gone)
		}
		if msg := refused(t, "schedule_history_delete", map[string]any{"run": number, "confirm": true}); !strings.Contains(msg, "there is no run numbered") {
			t.Errorf("%s: removing the run's record again = %q", host, msg)
		}

		// the schedule removed: said first, then done; the container is untouched and no longer kept on the newest image
		if preview := call(t, "schedule_delete", one(nil)); acctest.BoolOf(preview["removed"]) || len(acctest.Strs(t, preview["would_go"], "would_go")) != 1 || listed() == nil {
			t.Fatalf("%s: a removal not confirmed = %v", host, preview)
		}
		if gone := call(t, "schedule_delete", one(map[string]any{"confirm": true})); !acctest.BoolOf(gone["removed"]) || listed() != nil {
			t.Fatalf("%s: removing it = %v", host, gone)
		}
		if msg := refused(t, "schedule_delete", one(map[string]any{"confirm": true})); !strings.Contains(msg, "there is no container-update numbered") {
			t.Errorf("%s: removing it again = %q", host, msg)
		}
		if got := call(t, "container_get", map[string]any{"env": host, "container": name}); got["state"] != "running" {
			t.Errorf("%s: %s once its schedule was removed = %v", host, name, got)
		}
	}

	// Dockhand's own jobs: listed last and left out when asked, run now, three of them switched off, the rest and their removal refused
	const tidy, helpers, events = "system-cleanup", 3, 2
	all := acctest.Rows(t, call(t, "schedule_list", nil)["schedules"], "schedules")
	if last := all[len(all)-1]; !acctest.IsBool(last["system"], true) || last["env"] != nil || last["does"] == nil {
		t.Errorf("the last of what Dockhand does by itself = %v", last)
	}
	for _, row := range acctest.Rows(t, call(t, "schedule_list", map[string]any{"hide_system": true})["schedules"], "schedules") {
		if row["system"] != nil {
			t.Errorf("one of Dockhand's own jobs listed though left out = %v", row)
		}
	}
	if did := call(t, "schedule_run", map[string]any{"type": tidy, "id": helpers}); acctest.BoolOf(did["running"]) || acctest.Object(t, did["run"], "run")["status"] != "success" || acctest.Object(t, did["run"], "run")["env"] != nil {
		t.Errorf("running one of Dockhand's own jobs = %v", did)
	}
	if msg := refused(t, "schedule_set", map[string]any{"type": tidy, "id": helpers, "enabled": false}); !strings.Contains(msg, "cannot be switched off") {
		t.Errorf("switching off a job that cannot be = %q", msg)
	}
	if off := call(t, "schedule_set", map[string]any{"type": tidy, "id": events, "enabled": false}); len(acctest.Strs(t, off["changes"], "changes")) != 1 || !acctest.IsBool(off["enabled"], false) {
		t.Errorf("switching off one that can be = %v", off)
	}
	if on := call(t, "schedule_set", map[string]any{"type": tidy, "id": events, "enabled": true}); len(acctest.Strs(t, on["changes"], "changes")) != 1 || !acctest.IsBool(on["enabled"], true) {
		t.Errorf("switching it on again = %v", on)
	}
	if msg := refused(t, "schedule_delete", map[string]any{"type": tidy, "id": helpers, "confirm": true}); !strings.Contains(msg, "cannot be removed") {
		t.Errorf("removing one of Dockhand's own jobs = %q", msg)
	}
	if msg := refused(t, "schedule_run", map[string]any{"type": "hourly", "id": 1}); !strings.Contains(msg, `type is "hourly"`) {
		t.Errorf("running a type there is none of = %q", msg)
	}
}
