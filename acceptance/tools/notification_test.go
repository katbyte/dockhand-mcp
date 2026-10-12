//go:build acceptance

package tools_test

import (
	"maps"
	"strings"
	"testing"

	"github.com/katbyte/dockhand-mcp/lib/testenv"
	"github.com/katbyte/go-kt/mcp/acctest"
)

// A way of telling people is tried before it is saved, saved and listed with its address cut down to its service and host, and tested; it is tied to
// a host for two events, which are set off and counted, and the tie switched off and on; it is pointed at a service that turns messages away, which a
// test and an event both say; a mail channel is saved beside it, moved to another server and tested; and the tie and both channels are removed. The
// messages go to the registry of the test network, the one thing on it that answers what is posted to it.
func TestJourneyAChannelTriedSavedTiedSetOffAndRemoved(t *testing.T) {
	const name, mail = "acc-journey-hook", "acc-journey-mail"
	const host, other = testenv.Alpha, testenv.Beta
	if world == nil {
		t.Skip(testenv.NotConfigured)
	}
	if world.Registry == "" {
		t.Skip("the environment names no registry, which is what takes the messages: .scripts/testenv.sh up starts one")
	}
	taken, turnedAway, short := "json://"+world.User+":"+world.Password+"@"+world.Registry+"/v2/", "json://"+world.Registry+"/v2/", "json://"+world.Registry+"/…"
	listed := func() map[string]map[string]any {
		return named(acctest.Rows(t, call(t, "notification_list", nil)["channels"], "channels"), "name")
	}
	setOff := func(event, env string) (int, map[string]any) {
		args := map[string]any{"event": event}
		if env != "" {
			args["env"] = env
		}
		out := call(t, "notification_test", args)

		return acctest.Num(t, out["sent_to"], "sent_to"), out
	}
	clean := func() {
		for _, known := range []string{name, name + "-two", mail} {
			if listed()[known] != nil {
				suite.Call(t, "notification_delete", map[string]any{"channel": known, "confirm": true})
			}
		}
	}
	clean()
	t.Cleanup(clean)
	before := len(listed())

	// tried before it is saved: taken, turned away with the service's reason, and an address of no service refused before it is tried
	if tried := call(t, "notification_test", map[string]any{"urls": []any{taken}}); !acctest.IsBool(tried["sent"], true) || tried["error"] != nil || !strings.Contains(acctest.Str(tried["note"]), "not saved") || strings.Contains(flat(tried), world.Password) {
		t.Fatalf("tried with a login the service takes = %v", tried)
	}
	if tried := call(t, "notification_test", map[string]any{"urls": []any{turnedAway}}); !acctest.IsBool(tried["sent"], false) || !strings.Contains(acctest.Str(tried["error"]), "401") {
		t.Errorf("tried with no login = %v", tried)
	}
	if msg := refused(t, "notification_test", map[string]any{"urls": []any{"http://" + world.Registry + "/v2/"}}); !strings.Contains(msg, `urls has an address that begins "http"`) {
		t.Errorf("tried by an address of no service = %q", msg)
	}
	if len(listed()) != before {
		t.Errorf("trying a channel saved one: %v", listed())
	}

	// saved: said first, then done, with nothing of its login in the answer; a second of the same name is refused
	args := map[string]any{"name": name, "urls": []any{taken}, "events": []any{"license_expiring"}}
	if dry := call(t, "notification_create", over(args, map[string]any{"dry_run": true})); len(acctest.Strs(t, dry["changes"], "changes")) != 1 || dry["channel"] != nil || len(listed()) != before || strings.Contains(flat(dry), world.Password) {
		t.Fatalf("a dry run of saving it = %v", dry)
	}
	made := call(t, "notification_create", args)
	if row := acctest.Object(t, made["channel"], "channel"); row["name"] != name || row["type"] != "apprise" || !acctest.IsBool(row["enabled"], true) || strings.Join(acctest.Strs(t, row["to"], "to"), " ") != short || strings.Join(acctest.Strs(t, row["events"], "events"), " ") != "license_expiring" || row["environments"] != nil || !strings.Contains(acctest.Str(made["note"]), "tied to no environment yet") || strings.Contains(flat(made), world.Password) {
		t.Fatalf("saved = %v", made)
	}
	if msg := refused(t, "notification_create", over(args, map[string]any{"name": strings.ToUpper(name)})); !strings.Contains(msg, "already named") {
		t.Errorf("a second by the same name = %q", msg)
	}

	// listed with its address cut down, and whole only when asked; with every event there is when asked
	all := call(t, "notification_list", map[string]any{"event_types": true})
	if kept := listed()[name]; kept == nil || len(listed()) != before+1 || acctest.Num(t, all["hidden"], "hidden") < 1 || !strings.Contains(acctest.Str(all["note"]), "service and host alone") || strings.Contains(flat(all), world.Password) {
		t.Errorf("as listed = %v", all)
	}
	if events := named(acctest.Rows(t, all["event_types"], "event_types"), "id"); events["container_oom"] == nil || events["container_oom"]["scope"] != "environment" || events["container_oom"]["means"] == "" || events["license_expiring"] == nil || events["license_expiring"]["scope"] != "system" {
		t.Errorf("the events there are = %v", all["event_types"])
	}
	if whole := named(acctest.Rows(t, call(t, "notification_list", map[string]any{"reveal_secrets": true})["channels"], "channels"), "name")[name]; whole == nil || acctest.Strs(t, whole["to"], "to")[0] != taken {
		t.Error("asked for whole, the address was not given whole")
	}

	// tested as saved, and by the event of Dockhand's own it is told of, with no host
	if tried := call(t, "notification_test", map[string]any{"channel": name}); !acctest.IsBool(tried["sent"], true) || tried["channel"] != name {
		t.Errorf("a test through it = %v", tried)
	}
	if sent, out := setOff("license_expiring", ""); sent != 1 || !acctest.IsBool(out["sent"], true) {
		t.Errorf("the event of Dockhand's own it is told of = %v", out)
	}

	// tied to a host for two events: said first, then done and listed; the same again changes nothing, and an event of Dockhand's own is no event of
	// a host
	tie := map[string]any{"channel": name, "env": host, "events": []any{"container_started", "container_oom"}}
	if sent, out := setOff("container_started", host); sent != 0 || !strings.Contains(acctest.Str(out["note"]), "sent to no channel") {
		t.Errorf("an event of a host it is not tied to yet = %v", out)
	}
	if dry := call(t, "notification_link", over(tie, map[string]any{"dry_run": true})); len(acctest.Strs(t, dry["changes"], "changes")) != 1 || dry["link"] != nil || listed()[name]["environments"] != nil {
		t.Fatalf("a dry run of tying it = %v", dry)
	}
	tied := call(t, "notification_link", tie)
	if link := acctest.Object(t, tied["link"], "link"); link["env"] != host || !acctest.IsBool(link["enabled"], true) || strings.Join(acctest.Strs(t, link["events"], "events"), " ") != "container_started container_oom" || link["all_events"] != nil || len(acctest.Strs(t, tied["changes"], "changes")) != 1 {
		t.Fatalf("tied = %v", tied)
	}
	if again := call(t, "notification_link", tie); len(acctest.Strs(t, again["changes"], "changes")) != 0 || !strings.Contains(acctest.Str(again["note"]), "nothing to do") {
		t.Errorf("tied the same way again = %v", again)
	}
	if ties := acctest.Rows(t, listed()[name]["environments"], "environments"); len(ties) != 1 || ties[0]["env"] != host {
		t.Errorf("its ties as listed = %v", ties)
	}
	if msg := refused(t, "notification_link", over(tie, map[string]any{"events": []any{"license_expiring"}})); !strings.Contains(msg, `events has "license_expiring"`) {
		t.Errorf("tied for an event of Dockhand's own = %q", msg)
	}

	// set off: what the tie holds reaches it, what it does not and what happens on another host do not; a host's event needs its host, and of
	// Dockhand's own only the license one is set off
	if sent, out := setOff("container_started", host); sent != 1 || !acctest.IsBool(out["sent"], true) || out["note"] != nil {
		t.Errorf("an event the tie holds = %v", out)
	}
	if sent, out := setOff("container_stopped", host); sent != 0 || !acctest.IsBool(out["sent"], false) {
		t.Errorf("an event the tie does not hold = %v", out)
	}
	if sent, out := setOff("container_started", other); sent != 0 {
		t.Errorf("the same event on a host it is not tied to = %v", out)
	}
	if msg := refused(t, "notification_test", map[string]any{"event": "container_started"}); !strings.Contains(msg, "give env") {
		t.Errorf("an event of a host with no host = %q", msg)
	}
	if msg := refused(t, "notification_test", map[string]any{"event": "repo_prune_failed", "env": host}); !strings.Contains(msg, "sets off only license_expiring") {
		t.Errorf("an event of Dockhand's own that cannot be set off = %q", msg)
	}
	if msg := refused(t, "notification_test", map[string]any{"event": "acc_no_such_event", "env": host}); !strings.Contains(msg, `event is "acc_no_such_event"`) {
		t.Errorf("an event there is none of = %q", msg)
	}

	// the tie switched off keeps its events and sends nothing, and switched on sends again
	if off := call(t, "notification_link", map[string]any{"channel": name, "env": host, "enabled": false}); !acctest.IsBool(acctest.Object(t, off["link"], "link")["enabled"], false) || strings.Join(acctest.Strs(t, acctest.Object(t, off["link"], "link")["events"], "events"), " ") != "container_started container_oom" {
		t.Fatalf("the tie switched off = %v", off)
	}
	if sent, out := setOff("container_started", host); sent != 0 {
		t.Errorf("with the tie off = %v", out)
	}
	call(t, "notification_link", map[string]any{"channel": name, "env": host, "enabled": true})
	if sent, out := setOff("container_started", host); sent != 1 {
		t.Errorf("with the tie on again = %v", out)
	}

	// renamed and pointed at a service that turns messages away: said first, then done; a test says why, and an event says a channel did not take it
	change := map[string]any{"channel": name, "name": name + "-two", "urls": []any{turnedAway}}
	if dry := call(t, "notification_edit", over(change, map[string]any{"dry_run": true})); len(acctest.Strs(t, dry["changes"], "changes")) != 2 || listed()[name] == nil {
		t.Fatalf("a dry run of changing it = %v", dry)
	}
	if changed := call(t, "notification_edit", change); len(acctest.Strs(t, changed["changes"], "changes")) != 2 || acctest.Object(t, changed["channel"], "channel")["name"] != name+"-two" || listed()[name] != nil || listed()[name+"-two"] == nil {
		t.Fatalf("changing it = %v", changed)
	}
	if tried := call(t, "notification_test", map[string]any{"channel": name + "-two"}); !acctest.IsBool(tried["sent"], false) || !strings.Contains(acctest.Str(tried["error"]), "401") {
		t.Errorf("a test through a service that turns it away = %v", tried)
	}
	if sent, out := setOff("container_started", host); sent != 0 || !strings.Contains(acctest.Str(out["note"]), "did not take it") {
		t.Errorf("an event through a service that turns it away = %v", out)
	}
	if msg := refused(t, "notification_edit", map[string]any{"channel": name + "-two", "smtp_host": "mail.example"}); !strings.Contains(msg, "what kind a channel is cannot be changed") {
		t.Errorf("made into a mail channel = %q", msg)
	}
	// put back, switched off, which stops both kinds of event, and on again; the same again is nothing to do
	if changed := call(t, "notification_edit", map[string]any{"channel": name + "-two", "name": name, "urls": []any{taken}, "enabled": false}); len(acctest.Strs(t, changed["changes"], "changes")) != 3 || !acctest.IsBool(acctest.Object(t, changed["channel"], "channel")["enabled"], false) || strings.Contains(flat(changed), world.Password) {
		t.Fatalf("put back and switched off = %v", changed)
	}
	if ofHost, own := first(setOff("container_started", host)), first(setOff("license_expiring", "")); ofHost != 0 || own != 0 {
		t.Errorf("switched off, the host's event went to %d and Dockhand's own to %d", ofHost, own)
	}
	if again := call(t, "notification_edit", map[string]any{"channel": name, "name": name, "urls": []any{taken}, "enabled": false}); len(acctest.Strs(t, again["changes"], "changes")) != 0 || !strings.Contains(acctest.Str(again["note"]), "nothing to do") {
		t.Errorf("changed to what it is = %v", again)
	}
	call(t, "notification_edit", map[string]any{"channel": name, "enabled": true})

	// a mail channel beside it, saved switched off: its password is in no answer, a test says what the mail server said, and one field changes alone
	letters := call(t, "notification_create", map[string]any{"name": mail, "smtp_host": "mail.example", "smtp_username": "sender", "smtp_password": "acc-not-a-real-password", "from_email": "dockhand@example.com", "to_emails": []any{"ops@example.com"}, "disabled": true}) //nolint:gosec // G101: a made-up value for the throwaway server, which is no credential of anything real
	if row := acctest.Object(t, letters["channel"], "channel"); row["type"] != "smtp" || !acctest.IsBool(row["enabled"], false) || row["via"] != "mail.example:587 as dockhand@example.com" || strings.Join(acctest.Strs(t, row["to"], "to"), " ") != "ops@example.com" || !acctest.IsBool(row["all_events"], true) || strings.Contains(flat(letters), "acc-not-a-real-password") {
		t.Fatalf("a mail channel saved = %v", letters)
	}
	if tried := call(t, "notification_test", map[string]any{"channel": mail}); !acctest.IsBool(tried["sent"], false) || !strings.Contains(acctest.Str(tried["error"]), "SMTP error") {
		t.Errorf("a test through a mail server there is none of = %v", tried)
	}
	if moved := call(t, "notification_edit", map[string]any{"channel": mail, "smtp_host": "mail2.example"}); len(acctest.Strs(t, moved["changes"], "changes")) != 1 || acctest.Object(t, moved["channel"], "channel")["via"] != "mail2.example:587 as dockhand@example.com" || strings.Contains(flat(call(t, "notification_list", map[string]any{"reveal_secrets": true})), "acc-not-a-real-password") {
		t.Errorf("a mail channel moved to another server = %v", moved)
	}
	if msg := refused(t, "notification_edit", map[string]any{"channel": mail, "urls": []any{taken}}); !strings.Contains(msg, "is a mail channel") {
		t.Errorf("a mail channel given addresses = %q", msg)
	}

	// untied: said first, then done and read back as gone; what is not tied is refused, though Dockhand would answer it as done
	untie := map[string]any{"channel": name, "env": host}
	if preview := call(t, "notification_link_delete", untie); acctest.BoolOf(preview["removed"]) || acctest.Object(t, preview["link"], "link")["env"] != host || listed()[name]["environments"] == nil {
		t.Fatalf("untying it without confirm = %v", preview)
	}
	if untied := call(t, "notification_link_delete", over(untie, map[string]any{"confirm": true})); !acctest.BoolOf(untied["removed"]) || listed()[name]["environments"] != nil {
		t.Fatalf("untying it = %v", untied)
	}
	if msg := refused(t, "notification_link_delete", over(untie, map[string]any{"confirm": true})); !strings.Contains(msg, "is not tied to "+host) {
		t.Errorf("untying it again = %q", msg)
	}

	// tied to the other host for everything, and removed: what would go is said first, its tie among it, then it is gone with the tie
	if tied := call(t, "notification_link", map[string]any{"channel": name, "env": other}); !acctest.IsBool(acctest.Object(t, tied["link"], "link")["all_events"], true) || acctest.Object(t, tied["link"], "link")["events"] != nil {
		t.Fatalf("tied to another host for every event = %v", tied)
	}
	if preview := call(t, "notification_delete", map[string]any{"channel": name}); acctest.BoolOf(preview["removed"]) || !strings.Contains(strings.Join(acctest.Strs(t, preview["would_go"], "would_go"), " "), "its tie to "+other) || listed()[name] == nil {
		t.Fatalf("removing it without confirm = %v", preview)
	}
	if removed := call(t, "notification_delete", map[string]any{"channel": name, "confirm": true}); !acctest.BoolOf(removed["removed"]) || listed()[name] != nil {
		t.Fatalf("removing it = %v", removed)
	}
	if msg := refused(t, "notification_delete", map[string]any{"channel": name, "confirm": true}); !strings.Contains(msg, "no notification channel is named or numbered") {
		t.Errorf("removing it again = %q", msg)
	}
	if removed := call(t, "notification_delete", map[string]any{"channel": mail, "confirm": true}); !acctest.BoolOf(removed["removed"]) || len(listed()) != before {
		t.Errorf("removing the mail channel = %v", removed)
	}
}

// over is a set of arguments with more laid over them.
func over(args, more map[string]any) map[string]any {
	out := maps.Clone(args)
	maps.Copy(out, more)

	return out
}

// first is how many channels an event was sent to, of that and its answer.
func first(sent int, _ map[string]any) int { return sent }
