package tools

import (
	"maps"
	"net/http"
	"strings"
	"testing"

	"github.com/katbyte/dockhand-mcp/sdk/dockhandtest"
	"github.com/katbyte/go-kt/mcp/acctest"
)

// What a 1.0.51 server answers about notification channels, with invented names: a channel that sends to two addresses of other services, each
// carrying its own token; a mail channel that is switched off; the events there are, three of a host and one of Dockhand itself; and the ties of the
// first host to both channels.
//
//nolint:gosec // made-up addresses, tokens and passwords: nothing here signs in anywhere
const (
	hookAddress = "json://caller:not-a-real-password@hooks.example/in"
	chatAddress = "discord://123456/not-a-real-token"
	hookChannel = `{"id":6,"type":"apprise","name":"hook","enabled":true,"config":{"urls":["` + hookAddress + `","` + chatAddress + `"]},"eventTypes":["container_started","license_expiring"],"createdAt":"2026-10-10 10:00:00","updatedAt":"2026-10-10 10:00:00"}`
	mailChannel = `{"id":7,"type":"smtp","name":"mail","enabled":false,"config":{"host":"mail.example","port":587,"secure":false,"username":"sender","password":"********","from_email":"dockhand@example.com","from_name":"Dockhand","to_emails":["ops@example.com"]},"eventTypes":["container_started","container_oom","stack_deployed","license_expiring"],"createdAt":"2026-10-10 10:00:00","updatedAt":"2026-10-10 10:00:00"}`
	fourEvents  = `{"eventTypes":[{"id":"container_started","label":"Container started","description":"When a container starts running","group":"container","scope":"environment"},{"id":"container_oom","label":"Out of memory","description":"When a container is killed due to out of memory","group":"container","scope":"environment"},{"id":"stack_deployed","label":"Stack deployed","description":"","group":"stack","scope":"environment"},{"id":"license_expiring","label":"License expiring","description":"When the license is about to expire","group":"system","scope":"system"}],"categories":{"container":["container_started","container_oom"],"stack":["stack_deployed"],"system":["license_expiring"]}}`
	hookTie     = `{"id":1,"environmentId":1,"notificationId":6,"enabled":true,"eventTypes":["container_started","container_oom"],"channelName":"hook","channelType":"apprise","channelEnabled":true}`
	mailTie     = `{"id":2,"environmentId":1,"notificationId":7,"enabled":false,"eventTypes":["container_started","container_oom","stack_deployed"],"channelName":"mail","channelType":"smtp","channelEnabled":false}`
	delivered   = `{"success":true,"message":"Test notification sent successfully"}`

	channelList = "GET /api/notifications"
	eventList   = "GET /api/notifications/trigger-test"
	alphaTies   = "GET /api/environments/1/notifications"
	betaTies    = "GET /api/environments/2/notifications"
	makeChannel = "POST /api/notifications"
)

// withChannels is a Dockhand with two hosts and two notification channels, both tied to the first host.
func withChannels(t *testing.T) *dockhandtest.Server {
	t.Helper()

	dock := twoHosts(t)
	dock.Answer(channelList, `[`+mailChannel+`,`+hookChannel+`]`)
	dock.Answer(eventList, fourEvents)
	dock.Answer(alphaTies, `[`+hookTie+`,`+mailTie+`]`)
	dock.Answer(betaTies, `[]`)

	return dock
}

// over is a set of arguments with more laid over them.
func over(args, more map[string]any) map[string]any {
	out := maps.Clone(args)
	maps.Copy(out, more)

	return out
}

func TestShortAddress(t *testing.T) {
	t.Parallel()

	//nolint:gosec // made-up addresses and tokens
	for address, want := range map[string]string{
		hookAddress:                  "json://hooks.example/…",
		"ntfy://ntfy.example/alerts": "ntfy://ntfy.example/…",
		"ntfys://ntfy.example":       "ntfys://ntfy.example",
		"gotify://gotify.example:8080/not-a-real-token": "gotify://gotify.example:8080/…",
		"json://hooks.example?token=not-a-real-token":   "json://hooks.example/…",
		chatAddress:                          "discord://…",
		"tgram://123456:not-a-real-token/42": "tgram://…",
		"slack://a/b/c":                      "slack://…",
		"no address at all":                  "…",
	} {
		if got := shortAddress(address); got != want || (strings.Contains(address, "not-a-real") && strings.Contains(got, "not-a-real")) {
			t.Errorf("shortAddress(%q) = %q, want %q", address, got, want)
		}
	}
}

func TestNotificationList(t *testing.T) {
	t.Parallel()

	dock := withChannels(t)
	suite := session(t, dock, Options{})

	// by name, each with where it sends less what is secret in it, the events it is told of and the hosts it is tied to
	out := suite.Call(t, "notification_list", nil)
	rows := acctest.Rows(t, out["channels"], "channels")
	if acctest.Num(t, out["total"], "total") != 2 || rows[0]["name"] != "hook" || rows[1]["name"] != "mail" || acctest.Num(t, out["hidden"], "hidden") != 2 || !strings.Contains(acctest.Str(out["note"]), "2 addresses are given by their service and host alone") || out["event_types"] != nil || out["failed"] != nil || strings.Contains(flat(out), "not-a-real") {
		t.Fatalf("the channels = %v", out)
	}
	if hook := rows[0]; acctest.Num(t, hook["id"], "id") != 6 || hook["type"] != "apprise" || !acctest.IsBool(hook["enabled"], true) || strings.Join(acctest.Strs(t, hook["to"], "to"), " ") != "json://hooks.example/… discord://…" || hook["via"] != nil || strings.Join(acctest.Strs(t, hook["events"], "events"), " ") != "container_started license_expiring" || hook["all_events"] != nil {
		t.Errorf("a channel that sends to addresses = %v", hook)
	}
	if tie := acctest.Rows(t, rows[0]["environments"], "environments"); len(tie) != 1 || tie[0]["env"] != "alpha" || !acctest.IsBool(tie[0]["enabled"], true) || strings.Join(acctest.Strs(t, tie[0]["events"], "events"), " ") != "container_started container_oom" || tie[0]["all_events"] != nil {
		t.Errorf("its tie to a host = %v", tie)
	}
	// a mail channel: who it writes, through what, every event; and a tie told of every event of the host, which is not every event there is
	if mail := rows[1]; mail["type"] != "smtp" || !acctest.IsBool(mail["enabled"], false) || strings.Join(acctest.Strs(t, mail["to"], "to"), " ") != "ops@example.com" || mail["via"] != "mail.example:587 as dockhand@example.com" || !acctest.IsBool(mail["all_events"], true) || mail["events"] != nil {
		t.Errorf("a mail channel = %v", mail)
	}
	if tie := acctest.Rows(t, rows[1]["environments"], "environments"); len(tie) != 1 || !acctest.IsBool(tie[0]["enabled"], false) || !acctest.IsBool(tie[0]["all_events"], true) || tie[0]["events"] != nil {
		t.Errorf("a tie told of every event of the host = %v", tie)
	}

	// asked for, the addresses whole and the events there are
	out = suite.Call(t, "notification_list", map[string]any{"reveal_secrets": true, "event_types": true})
	if to := acctest.Strs(t, acctest.Rows(t, out["channels"], "channels")[0]["to"], "to"); to[0] != hookAddress || to[1] != chatAddress || out["hidden"] != nil || out["note"] != nil {
		t.Errorf("the addresses whole = %v", out)
	}
	if events := acctest.Rows(t, out["event_types"], "event_types"); len(events) != 4 || events[0]["id"] != "container_started" || events[0]["means"] != "When a container starts running" || events[0]["scope"] != "environment" || events[2]["means"] != "Stack deployed" || events[3]["scope"] != "system" || events[3]["group"] != "system" {
		t.Errorf("the events there are = %v", out["event_types"])
	}

	// a host whose ties cannot be read is said, and the rest is given; none kept says so
	dock.Fail(betaTies, http.StatusForbidden, "Permission denied")
	if out = suite.Call(t, "notification_list", nil); len(acctest.Rows(t, out["failed"], "failed")) != 1 || acctest.Rows(t, out["failed"], "failed")[0]["env"] != "beta" || len(acctest.Rows(t, out["channels"], "channels")) != 2 {
		t.Errorf("with a host that cannot be asked = %v", out)
	}
	dock.Answer(channelList, `[`+strings.Replace(hookChannel, `,"`+chatAddress+`"`, "", 1)+`]`)
	if out = suite.Call(t, "notification_list", nil); !strings.Contains(acctest.Str(out["note"]), "one address is given by its service and host alone") {
		t.Errorf("one address shortened = %v", out)
	}
	dock.Answer(channelList, `[]`)
	if out = suite.Call(t, "notification_list", nil); !strings.Contains(acctest.Str(out["note"]), "keeps no notification channel") {
		t.Errorf("no channels = %v", out)
	}

	// read in the order channels, events, hosts: each failing in turn, the last asked first
	for _, failing := range [][2]string{{"GET /api/environments", "GET /api/environments"}, {eventList, "the events a channel can be told of could not be listed"}, {channelList, "the notification channels could not be listed"}} {
		dock.Fail(failing[0], http.StatusInternalServerError, "Failed")
		if msg := suite.CallErr(t, "notification_list", nil); !strings.Contains(msg, failing[1]) {
			t.Errorf("with %s failing = %q, want %q", failing[0], msg, failing[1])
		}
	}
}

func TestNotificationTest(t *testing.T) {
	t.Parallel()

	const saved, unsaved, event = "POST /api/notifications/6/test", "POST /api/notifications/test", "POST /api/notifications/trigger-test"
	dock := withChannels(t)
	dock.Answer(saved, delivered)
	dock.Answer("POST /api/notifications/7/test", `{"success":false,"message":"Failed to send test notification","error":"SMTP error: getaddrinfo ENOTFOUND mail.example"}`)
	dock.Answer(unsaved, delivered)
	dock.Answer(event, `{"success":true,"sent":1,"eventType":"container_started","environmentId":1}`)
	suite := session(t, dock, everything)

	// through a channel that is saved: handed over, or turned away with the reason, which is an answer
	if out := suite.Call(t, "notification_test", map[string]any{"channel": "HOOK"}); !acctest.IsBool(out["sent"], true) || out["channel"] != "hook" || out["error"] != nil || out["note"] != nil || out["sent_to"] != nil {
		t.Errorf("a test through a saved channel = %v", out)
	}
	if out := suite.Call(t, "notification_test", map[string]any{"channel": 7}); !acctest.IsBool(out["sent"], false) || !strings.Contains(acctest.Str(out["error"]), "ENOTFOUND") || !strings.Contains(acctest.Str(out["note"]), "the channel is switched off") {
		t.Errorf("a test the mail server turns away = %v", out)
	}

	// through one that is not: addresses, or mail with the port mail is handed in on
	out := suite.Call(t, "notification_test", map[string]any{"urls": []any{" ntfy://ntfy.example/alerts "}})
	body := sent(t, dock.Last(unsaved))
	if body["type"] != "apprise" || acctest.Strs(t, acctest.Object(t, body["config"], "config")["urls"], "urls")[0] != "ntfy://ntfy.example/alerts" || !acctest.IsBool(out["sent"], true) || !strings.Contains(acctest.Str(out["note"]), "not saved") {
		t.Errorf("an unsaved channel tried = %v, sent %v", out, body)
	}
	suite.Call(t, "notification_test", map[string]any{"smtp_host": "mail.example", "smtp_username": "sender", "smtp_password": "not-a-real-password", "from_email": "dockhand@example.com", "to_emails": []any{"ops@example.com"}})
	if cfg := acctest.Object(t, sent(t, dock.Last(unsaved))["config"], "config"); sent(t, dock.Last(unsaved))["type"] != "smtp" || cfg["host"] != "mail.example" || cfg["port"] != float64(587) || cfg["username"] != "sender" || cfg["password"] != "not-a-real-password" || cfg["from_email"] != "dockhand@example.com" || cfg["secure"] != nil {
		t.Errorf("unsaved mail tried with %v", cfg)
	}

	// an event set off for a host: how many channels it went to, with a title and words unless given; one of Dockhand's own needs no host
	out = suite.Call(t, "notification_test", map[string]any{"event": " Container_Started ", "env": "alpha"})
	body = sent(t, dock.Last(event))
	if payload := acctest.Object(t, body["payload"], "payload"); body["eventType"] != "container_started" || body["environmentId"] != float64(1) || payload["title"] != "Test notification" || !strings.Contains(acctest.Str(payload["message"]), "container_started") || !acctest.IsBool(out["sent"], true) || acctest.Num(t, out["sent_to"], "sent_to") != 1 {
		t.Errorf("an event set off = %v, sent %v", out, body)
	}
	dock.Answer(event, `{"success":true,"sent":0,"eventType":"license_expiring"}`)
	out = suite.Call(t, "notification_test", map[string]any{"event": "license_expiring", "title": "A drill", "message": "Nothing is wrong."})
	if body = sent(t, dock.Last(event)); body["environmentId"] != nil || acctest.Object(t, body["payload"], "payload")["title"] != "A drill" || acctest.Object(t, body["payload"], "payload")["message"] != "Nothing is wrong." || !acctest.IsBool(out["sent"], false) || acctest.Num(t, out["sent_to"], "sent_to") != 0 || !strings.Contains(acctest.Str(out["note"]), "none that is switched on is told of this event") {
		t.Errorf("an event of Dockhand's own that nothing is tied to = %v, sent %v", out, body)
	}

	// an event of a host that nothing is tied for, and one a channel took while another turned it away, which Dockhand counts and does not name
	dock.Answer(event, `{"success":true,"sent":0,"eventType":"container_oom","environmentId":2}`)
	if out = suite.Call(t, "notification_test", map[string]any{"event": "container_oom", "env": "beta"}); !acctest.IsBool(out["sent"], false) || sent(t, dock.Last(event))["environmentId"] != float64(2) || !strings.Contains(acctest.Str(out["note"]), "none that is switched on is tied to the environment for this event") {
		t.Errorf("an event of a host nothing is tied for = %v", out)
	}
	dock.Answer(event, `{"success":false,"sent":1,"eventType":"container_oom","environmentId":1}`)
	if out = suite.Call(t, "notification_test", map[string]any{"event": "container_oom", "env": "alpha"}); !acctest.IsBool(out["sent"], true) || acctest.Num(t, out["sent_to"], "sent_to") != 1 || !strings.Contains(acctest.Str(out["note"]), "a channel it went to did not take it") {
		t.Errorf("an event one channel took and another turned away = %v", out)
	}

	// of Dockhand's own events it sets off the license one alone: another would be asked a host for and then reach nobody, so it is refused here
	dock.Answer(eventList, strings.Replace(fourEvents, `{"id":"license_expiring"`, `{"id":"repo_prune_failed","label":"Backup repository prune failed","group":"system","scope":"system"},{"id":"license_expiring"`, 1))
	setOff := len(dock.Requests(event))
	for _, args := range []map[string]any{{"event": "repo_prune_failed"}, {"event": "repo_prune_failed", "env": "alpha"}} {
		if msg := suite.CallErr(t, "notification_test", args); !strings.Contains(msg, "repo_prune_failed is an event of Dockhand itself, and of those it sets off only license_expiring for a test") || len(dock.Requests(event)) != setOff {
			t.Errorf("an event of Dockhand's own that cannot be set off, with %v = %q", args, msg)
		}
	}
	dock.Answer(eventList, fourEvents)

	posted := len(dock.Requests(unsaved)) + len(dock.Requests(event)) + len(dock.Requests(saved))
	for what, tc := range map[string]struct {
		args map[string]any
		want string
	}{
		"nothing to send through":       {nil, "give one of three"},
		"a channel and an event":        {map[string]any{"channel": "hook", "event": "container_started", "env": "alpha"}, "give one of three"},
		"a channel and addresses":       {map[string]any{"channel": "hook", "urls": []any{"ntfy://ntfy.example/a"}}, "give one of three"},
		"an address of no service":      {map[string]any{"urls": []any{"http://hooks.example/in"}}, `urls has an address that begins "http"`},
		"mail with nobody to write":     {map[string]any{"smtp_host": "mail.example", "from_email": "a@example.com"}, "a mail channel needs smtp_host, from_email and to_emails"},
		"addresses and mail both":       {map[string]any{"urls": []any{"ntfy://ntfy.example/a"}, "smtp_host": "mail.example"}, "not both"},
		"an event there is none of":     {map[string]any{"event": "container_exploded", "env": "alpha"}, `event is "container_exploded": an event is one of container_started`},
		"an event of a host, with none": {map[string]any{"event": "container_oom"}, "container_oom is an event of a host, so it is set off for one: give env"},
		"a host there is none of":       {map[string]any{"event": "container_oom", "env": "gamma"}, `no environment is named or numbered "gamma"`},
		"a channel there is none of":    {map[string]any{"channel": "pager"}, `no notification channel is named or numbered "pager": Dockhand keeps mail, hook`},
	} {
		if msg := suite.CallErr(t, "notification_test", tc.args); !strings.Contains(msg, tc.want) {
			t.Errorf("%s = %q, want %q", what, msg, tc.want)
		}
	}
	if len(dock.Requests(unsaved))+len(dock.Requests(event))+len(dock.Requests(saved)) != posted {
		t.Error("a message was sent by a call that was refused")
	}

	// two channels of one name are not chosen between, and Dockhand's own failures are errors
	dock.Answer(channelList, `[`+hookChannel+`,`+strings.Replace(hookChannel, `"id":6`, `"id":9`, 1)+`]`)
	if msg := suite.CallErr(t, "notification_test", map[string]any{"channel": "hook"}); !strings.Contains(msg, `2 notification channels are named "hook", numbered 6 and 9: say which by its number`) {
		t.Errorf("two channels of one name = %q", msg)
	}
	for call, args := range map[string]map[string]any{saved: {"channel": 6}, unsaved: {"urls": []any{"ntfy://ntfy.example/a"}}, event: {"event": "license_expiring"}} {
		dock.Fail(call, http.StatusForbidden, "Permission denied")
		if msg := suite.CallErr(t, "notification_test", args); !strings.Contains(msg, "Permission denied") {
			t.Errorf("with %s refused = %q", call, msg)
		}
	}
}

func TestNotificationCreate(t *testing.T) {
	t.Parallel()

	const pager = `{"id":8,"type":"apprise","name":"pager","enabled":true,"config":{"urls":["gotify://gotify.example/not-a-real-token"]},"eventTypes":["container_oom"]}`
	dock := withChannels(t)
	dock.Answer(makeChannel, pager)
	suite := session(t, dock, everything)
	args := map[string]any{"name": " pager ", "urls": []any{"gotify://gotify.example/not-a-real-token"}, "events": []any{"Container_OOM", "container_oom"}}

	// a dry run says what would be saved, the address without its token, and saves nothing
	out := suite.Call(t, "notification_create", over(args, map[string]any{"dry_run": true}))
	if changes := acctest.Strs(t, out["changes"], "changes"); len(changes) != 1 || changes[0] != "would save the notification channel pager, which sends to gotify://gotify.example/…" || out["channel"] != nil || len(dock.Requests(makeChannel)) != 0 {
		t.Errorf("a dry run = %v", out)
	}
	dock.Then(makeChannel, func(dockhandtest.Request) { dock.Answer(channelList, `[`+mailChannel+`,`+hookChannel+`,`+pager+`]`) })
	out = suite.Call(t, "notification_create", args)
	body := sent(t, dock.Last(makeChannel))
	if body["type"] != "apprise" || body["name"] != "pager" || body["enabled"] != nil || strings.Join(acctest.Strs(t, body["eventTypes"], "eventTypes"), " ") != "container_oom" || acctest.Strs(t, acctest.Object(t, body["config"], "config")["urls"], "urls")[0] != "gotify://gotify.example/not-a-real-token" {
		t.Errorf("sent %v", body)
	}
	if made := acctest.Object(t, out["channel"], "channel"); made["name"] != "pager" || acctest.Num(t, made["id"], "id") != 8 || acctest.Strs(t, made["to"], "to")[0] != "gotify://gotify.example/…" || !strings.Contains(acctest.Str(out["note"]), "it is tied to no environment yet") || strings.Contains(flat(out), "not-a-real") {
		t.Errorf("a channel saved = %v", out)
	}

	// mail, switched off, told of everything: no events are sent, which Dockhand reads as all
	dock.Answer(channelList, `[`+hookChannel+`]`)
	dock.Then(makeChannel, func(dockhandtest.Request) {})
	dock.Answer(makeChannel, strings.Replace(mailChannel, `"id":7`, `"id":8`, 1))
	out = suite.Call(t, "notification_create", map[string]any{"name": "mail", "smtp_host": "mail.example", "smtp_port": 465, "smtp_secure": true, "smtp_username": "sender", "smtp_password": "not-a-real-password", "from_email": "dockhand@example.com", "from_name": "Dockhand", "to_emails": []any{"ops@example.com"}, "disabled": true})
	body = sent(t, dock.Last(makeChannel))
	if cfg := acctest.Object(t, body["config"], "config"); body["type"] != "smtp" || !acctest.IsBool(body["enabled"], false) || body["eventTypes"] != nil || cfg["host"] != "mail.example" || cfg["port"] != float64(465) || !acctest.IsBool(cfg["secure"], true) || cfg["password"] != "not-a-real-password" || cfg["from_name"] != "Dockhand" {
		t.Errorf("mail sent %v", body)
	}
	// saved, and not to be read back since the list does not have it
	if changes := acctest.Strs(t, out["changes"], "changes"); changes[0] != "saved the mail channel mail, which writes to ops@example.com through mail.example, switched off" || out["channel"] != nil || !strings.Contains(acctest.Str(out["note"]), "could not be read back") || strings.Contains(flat(out), "not-a-real") {
		t.Errorf("mail saved and not read back = %v", out)
	}

	saved := len(dock.Requests(makeChannel))
	for what, tc := range map[string]struct {
		args map[string]any
		want string
	}{
		"a blank name":              {over(args, map[string]any{"name": " "}), "name is required"},
		"nothing of how it sends":   {map[string]any{"name": "pager"}, "how the channel sends is required"},
		"addresses and mail both":   {over(args, map[string]any{"smtp_host": "mail.example"}), "not both"},
		"an event there is none of": {over(args, map[string]any{"events": []any{"container_exploded"}}), `events has "container_exploded"`},
		"a name in use":             {over(args, map[string]any{"name": "HOOK"}), `a notification channel is already named "HOOK"`},
	} {
		if msg := suite.CallErr(t, "notification_create", tc.args); !strings.Contains(msg, tc.want) {
			t.Errorf("%s = %q, want %q", what, msg, tc.want)
		}
	}
	if len(dock.Requests(makeChannel)) != saved {
		t.Error("a channel was saved by a call that was refused")
	}
	dock.Fail(makeChannel, http.StatusBadRequest, "Webhook config requires at least one URL")
	if msg := suite.CallErr(t, "notification_create", args); !strings.Contains(msg, "pager was not saved") || !strings.Contains(msg, "at least one URL") {
		t.Errorf("one Dockhand refuses = %q", msg)
	}
}

func TestNotificationEdit(t *testing.T) {
	t.Parallel()

	const hook, mail = "PUT /api/notifications/6", "PUT /api/notifications/7"
	dock := withChannels(t)
	dock.Answer(hook, hookChannel)
	dock.Answer(mail, mailChannel)
	suite := session(t, dock, everything)

	// a name, the switch and the events: said first, then sent, and nothing of how it sends goes with them
	out := suite.Call(t, "notification_edit", map[string]any{"channel": "hook", "name": "hook-two", "enabled": false, "events": []any{"container_oom"}, "dry_run": true})
	if changes := acctest.Strs(t, out["changes"], "changes"); len(changes) != 3 || changes[0] != "would rename it from hook to hook-two" || changes[1] != "would switch it off" || changes[2] != "would change the events it is told of to container_oom" || len(dock.Requests(hook)) != 0 {
		t.Errorf("a dry run = %v", out)
	}
	out = suite.Call(t, "notification_edit", map[string]any{"channel": "hook", "name": "hook-two", "enabled": false, "events": []any{"container_oom"}})
	if body := sent(t, dock.Last(hook)); body["name"] != "hook-two" || !acctest.IsBool(body["enabled"], false) || strings.Join(acctest.Strs(t, body["eventTypes"], "eventTypes"), " ") != "container_oom" || body["config"] != nil || len(acctest.Strs(t, out["changes"], "changes")) != 3 {
		t.Errorf("sent %v, answered %v", body, out)
	}
	// its addresses are replaced whole, and said without their tokens
	out = suite.Call(t, "notification_edit", map[string]any{"channel": 6, "urls": []any{"ntfy://ntfy.example/not-a-real-topic"}})
	if body := sent(t, dock.Last(hook)); acctest.Strs(t, acctest.Object(t, body["config"], "config")["urls"], "urls")[0] != "ntfy://ntfy.example/not-a-real-topic" || body["name"] != nil || acctest.Strs(t, out["changes"], "changes")[0] != "changed where it sends to ntfy://ntfy.example/…" || strings.Contains(flat(out), "not-a-real") {
		t.Errorf("addresses replaced sent %v, answered %v", body, out)
	}

	// mail: what is given changes, the rest goes back as it is, and the password by the mask that stands for it
	out = suite.Call(t, "notification_edit", map[string]any{"channel": "mail", "smtp_host": "mail2.example", "to_emails": []any{"oncall@example.com"}})
	cfg := acctest.Object(t, sent(t, dock.Last(mail))["config"], "config")
	if cfg["host"] != "mail2.example" || cfg["port"] != float64(587) || cfg["username"] != "sender" || cfg["password"] != "********" || cfg["from_email"] != "dockhand@example.com" || cfg["from_name"] != "Dockhand" || acctest.Strs(t, cfg["to_emails"], "to_emails")[0] != "oncall@example.com" || acctest.Strs(t, out["changes"], "changes")[0] != "changed its mail server, who it writes to" {
		t.Errorf("mail changed sent %v, answered %v", cfg, out)
	}
	suite.Call(t, "notification_edit", map[string]any{"channel": "mail", "smtp_password": "another-not-real", "smtp_port": 465, "smtp_secure": true, "smtp_username": "other", "from_email": "alerts@example.com", "from_name": "Alerts"})
	if cfg = acctest.Object(t, sent(t, dock.Last(mail))["config"], "config"); cfg["password"] != "another-not-real" || cfg["port"] != float64(465) || !acctest.IsBool(cfg["secure"], true) || cfg["username"] != "other" || cfg["from_email"] != "alerts@example.com" || cfg["from_name"] != "Alerts" || cfg["host"] != "mail.example" {
		t.Errorf("mail given a new password sent %v", cfg)
	}

	// what is so already is nothing to do, and nothing given is refused
	changed := len(dock.Requests(hook)) + len(dock.Requests(mail))
	for what, args := range map[string]map[string]any{
		"a channel as it is":   {"channel": "hook", "name": "hook", "enabled": true, "events": []any{"license_expiring", "container_started"}, "urls": []any{hookAddress, chatAddress}},
		"a mail channel as is": {"channel": "mail", "smtp_host": "mail.example", "smtp_port": 587, "smtp_secure": false, "smtp_username": "sender", "from_email": "dockhand@example.com", "from_name": "Dockhand", "to_emails": []any{"ops@example.com"}},
	} {
		if out = suite.Call(t, "notification_edit", args); len(acctest.Strs(t, out["changes"], "changes")) != 0 || !strings.Contains(acctest.Str(out["note"]), "nothing to do") {
			t.Errorf("%s = %v", what, out)
		}
	}
	for what, tc := range map[string]struct {
		args map[string]any
		want string
	}{
		"nothing to change":          {map[string]any{"channel": "hook"}, "nothing to change was given"},
		"no channel":                 {map[string]any{"name": "x"}, "channel is required"},
		"a channel there is none of": {map[string]any{"channel": "pager", "name": "x"}, `no notification channel is named or numbered "pager"`},
		"a name another has":         {map[string]any{"channel": "hook", "name": "MAIL"}, `another notification channel is named "MAIL"`},
		"mail fields for addresses":  {map[string]any{"channel": "hook", "smtp_host": "mail.example"}, "hook is a channel that sends to addresses, and what kind a channel is cannot be changed"},
		"addresses for mail":         {map[string]any{"channel": "mail", "urls": []any{"ntfy://ntfy.example/a"}}, "mail is a mail channel"},
		"an address of no service":   {map[string]any{"channel": "hook", "urls": []any{"https://hooks.example"}}, `urls has an address that begins "https"`},
		"an event there is none of":  {map[string]any{"channel": "hook", "events": []any{"container_exploded"}}, `events has "container_exploded"`},
		"a number that is no number": {map[string]any{"channel": 6.5, "name": "x"}, "a channel's number is a whole one"},
	} {
		if msg := suite.CallErr(t, "notification_edit", tc.args); !strings.Contains(msg, tc.want) {
			t.Errorf("%s = %q, want %q", what, msg, tc.want)
		}
	}
	if len(dock.Requests(hook))+len(dock.Requests(mail)) != changed {
		t.Error("a channel was changed by a call that changed nothing or was refused")
	}

	// refused by Dockhand, and changed but not to be read back
	dock.Fail(hook, http.StatusBadRequest, "Webhook config requires at least one URL")
	if msg := suite.CallErr(t, "notification_edit", map[string]any{"channel": "hook", "enabled": false}); !strings.Contains(msg, "hook was not changed") {
		t.Errorf("a change Dockhand refuses = %q", msg)
	}
	dock.Answer(hook, hookChannel)
	dock.Then(hook, func(dockhandtest.Request) { dock.Fail(channelList, http.StatusInternalServerError, "Failed") })
	if out = suite.Call(t, "notification_edit", map[string]any{"channel": "hook", "enabled": false}); !strings.Contains(acctest.Str(out["note"]), "could not be read back") || len(acctest.Strs(t, out["changes"], "changes")) != 1 {
		t.Errorf("a change not read back = %v", out)
	}
}

func TestNotificationLink(t *testing.T) {
	t.Parallel()

	const tie, change = "POST /api/environments/2/notifications", "PUT /api/environments/1/notifications/6"
	dock := withChannels(t)
	dock.Answer(tie, `{"id":3,"environmentId":2,"notificationId":7,"enabled":true}`)
	dock.Answer(change, hookTie)
	suite := session(t, dock, everything)

	// a new tie: said first, then made for the events given and read back; the channel being off is said
	out := suite.Call(t, "notification_link", map[string]any{"channel": "mail", "env": "beta", "events": []any{"container_oom"}, "dry_run": true})
	if changes := acctest.Strs(t, out["changes"], "changes"); len(changes) != 1 || changes[0] != "would tie mail to beta, for container_oom" || out["link"] != nil || len(dock.Requests(tie)) != 0 {
		t.Errorf("a dry run = %v", out)
	}
	dock.Then(tie, func(dockhandtest.Request) {
		dock.Answer(betaTies, `[{"id":3,"environmentId":2,"notificationId":7,"enabled":true,"eventTypes":["container_oom"],"channelName":"mail","channelType":"smtp","channelEnabled":false}]`)
	})
	out = suite.Call(t, "notification_link", map[string]any{"channel": "mail", "env": "beta", "events": []any{"container_oom"}})
	if body := sent(t, dock.Last(tie)); body["notificationId"] != float64(7) || strings.Join(acctest.Strs(t, body["eventTypes"], "eventTypes"), " ") != "container_oom" || body["enabled"] != nil {
		t.Errorf("sent %v", body)
	}
	if made := acctest.Object(t, out["link"], "link"); made["env"] != "beta" || !acctest.IsBool(made["enabled"], true) || acctest.Strs(t, made["events"], "events")[0] != "container_oom" || acctest.Strs(t, out["changes"], "changes")[0] != "tied mail to beta, for container_oom" || !strings.Contains(acctest.Str(out["note"]), "mail is itself switched off") {
		t.Errorf("a tie made = %v", out)
	}
	// for every event, and left switched off, when no events are named
	dock.Answer(betaTies, `[]`)
	dock.Then(tie, func(dockhandtest.Request) {})
	if out = suite.Call(t, "notification_link", map[string]any{"channel": "hook", "env": 2, "enabled": false, "dry_run": true}); strings.Join(acctest.Strs(t, out["changes"], "changes"), "|") != "would tie hook to beta, for every event|would leave the tie switched off" {
		t.Errorf("a dry run of a tie for everything, switched off = %v", out)
	}
	// answered as made, and the host lists no such tie
	if msg := suite.CallErr(t, "notification_link", map[string]any{"channel": "hook", "env": "beta"}); !strings.Contains(msg, "was answered as made, and beta lists none") {
		t.Errorf("a tie made in word alone = %q", msg)
	}

	// a tie there is: the same again changes nothing, and what differs is sent alone
	made := len(dock.Requests(tie))
	if out = suite.Call(t, "notification_link", map[string]any{"channel": "hook", "env": "alpha", "events": []any{"container_oom", "container_started"}, "enabled": true}); len(acctest.Strs(t, out["changes"], "changes")) != 0 || !strings.Contains(acctest.Str(out["note"]), "nothing to do: hook is tied to alpha that way already") || len(dock.Requests(change)) != 0 {
		t.Errorf("a tie as it is = %v", out)
	}
	out = suite.Call(t, "notification_link", map[string]any{"channel": "hook", "env": "alpha", "events": []any{"container_started", "container_oom", "stack_deployed"}, "enabled": false})
	if body := sent(t, dock.Last(change)); !acctest.IsBool(body["enabled"], false) || len(acctest.Strs(t, body["eventTypes"], "eventTypes")) != 3 || strings.Join(acctest.Strs(t, out["changes"], "changes"), "|") != "switched the tie off|changed the events of alpha it is told of to every event" || len(dock.Requests(tie)) != made {
		t.Errorf("a tie changed sent %v, answered %v", body, out)
	}
	if out = suite.Call(t, "notification_link", map[string]any{"channel": "hook", "env": "alpha", "enabled": false, "dry_run": true}); acctest.Strs(t, out["changes"], "changes")[0] != "would switch the tie off" || len(dock.Requests(change)) != 1 {
		t.Errorf("a dry run of switching a tie off = %v", out)
	}

	for what, tc := range map[string]struct {
		args map[string]any
		want string
	}{
		"no channel":                 {map[string]any{"env": "alpha"}, "channel is required"},
		"no host":                    {map[string]any{"channel": "hook"}, "env is required"},
		"a host there is none of":    {map[string]any{"channel": "hook", "env": "gamma"}, `no environment is named or numbered "gamma"`},
		"an event of Dockhand's own": {map[string]any{"channel": "hook", "env": "alpha", "events": []any{"license_expiring"}}, `events has "license_expiring": an event is one of container_started, container_oom, stack_deployed`},
	} {
		if msg := suite.CallErr(t, "notification_link", tc.args); !strings.Contains(msg, tc.want) {
			t.Errorf("%s = %q, want %q", what, msg, tc.want)
		}
	}
	dock.Fail(change, http.StatusForbidden, "Permission denied")
	if msg := suite.CallErr(t, "notification_link", map[string]any{"channel": "hook", "env": "alpha", "enabled": false}); !strings.Contains(msg, "the tie of hook to alpha was not changed") {
		t.Errorf("a change Dockhand refuses = %q", msg)
	}
	dock.Fail(tie, http.StatusConflict, "This notification channel is already configured for this environment")
	if msg := suite.CallErr(t, "notification_link", map[string]any{"channel": "hook", "env": "beta"}); !strings.Contains(msg, "hook was not tied to beta") {
		t.Errorf("a tie Dockhand refuses = %q", msg)
	}
	dock.Fail(alphaTies, http.StatusForbidden, "Permission denied")
	if msg := suite.CallErr(t, "notification_link", map[string]any{"channel": "hook", "env": "alpha", "enabled": false}); !strings.Contains(msg, "the channels tied to alpha could not be read, so nothing was changed") {
		t.Errorf("ties that could not be read = %q", msg)
	}
}

func TestNotificationLinkDelete(t *testing.T) {
	t.Parallel()

	const untie = "DELETE /api/environments/1/notifications/6"
	dock := withChannels(t)
	dock.Answer(untie, `{"success":true}`)
	suite := session(t, dock, everything)
	args := map[string]any{"channel": "hook", "env": "alpha"}

	out := suite.Call(t, "notification_link_delete", args)
	if acctest.BoolOf(out["removed"]) || acctest.Object(t, out["link"], "link")["env"] != "alpha" || !strings.Contains(acctest.Str(out["note"]), "nothing was untied") || len(dock.Requests(untie)) != 0 {
		t.Errorf("a preview = %v", out)
	}
	// Dockhand unties whatever it is told to, tied or not, so one that is not tied is refused here
	if msg := suite.CallErr(t, "notification_link_delete", map[string]any{"channel": "hook", "env": "beta", "confirm": true}); !strings.Contains(msg, "hook is not tied to beta") {
		t.Errorf("untying what is not tied = %q", msg)
	}
	// answered as done and still tied, refused, and not read back
	if msg := suite.CallErr(t, "notification_link_delete", over(args, map[string]any{"confirm": true})); !strings.Contains(msg, "alpha still lists the tie") {
		t.Errorf("untied in word alone = %q", msg)
	}
	dock.Then(untie, func(dockhandtest.Request) { dock.Answer(alphaTies, `[`+mailTie+`]`) })
	if out = suite.Call(t, "notification_link_delete", over(args, map[string]any{"confirm": true})); !acctest.BoolOf(out["removed"]) || out["channel"] != "hook" || out["env"] != "alpha" || out["note"] != nil {
		t.Errorf("a tie removed = %v", out)
	}
	dock.Answer(alphaTies, `[`+hookTie+`]`)
	dock.Then(untie, func(dockhandtest.Request) { dock.Fail(alphaTies, http.StatusInternalServerError, "Failed") })
	if out = suite.Call(t, "notification_link_delete", over(args, map[string]any{"confirm": true})); acctest.BoolOf(out["removed"]) || !strings.Contains(acctest.Str(out["note"]), "could not be read back") {
		t.Errorf("a tie removed and not read back = %v", out)
	}
	if msg := suite.CallErr(t, "notification_link_delete", args); !strings.Contains(msg, "so nothing was untied") {
		t.Errorf("ties that could not be read = %q", msg)
	}
	dock.Answer(alphaTies, `[`+hookTie+`]`)
	dock.Then(untie, func(dockhandtest.Request) {})
	dock.Fail(untie, http.StatusForbidden, "Permission denied")
	if msg := suite.CallErr(t, "notification_link_delete", over(args, map[string]any{"confirm": true})); !strings.Contains(msg, "hook was not untied from alpha") {
		t.Errorf("an untying Dockhand refuses = %q", msg)
	}
	if msg := suite.CallErr(t, "notification_link_delete", map[string]any{"channel": "hook"}); !strings.Contains(msg, "env is required") {
		t.Errorf("no host = %q", msg)
	}
}

func TestNotificationDelete(t *testing.T) {
	t.Parallel()

	const remove = "DELETE /api/notifications/6"
	dock := withChannels(t)
	dock.Answer(remove, `{"success":true}`)
	suite := session(t, dock, everything)

	// without confirm: what would go, the host it is tied to among it
	out := suite.Call(t, "notification_delete", map[string]any{"channel": "hook"})
	if would := acctest.Strs(t, out["would_go"], "would_go"); len(would) != 2 || !strings.Contains(would[0], "the apprise channel hook") || !strings.Contains(would[1], "its tie to alpha") || acctest.BoolOf(out["removed"]) || len(dock.Requests(remove)) != 0 {
		t.Errorf("a preview = %v", out)
	}
	// answered as removed and still listed, without success, refused
	if msg := suite.CallErr(t, "notification_delete", map[string]any{"channel": "hook", "confirm": true}); !strings.Contains(msg, "it is still listed") {
		t.Errorf("removed in word alone = %q", msg)
	}
	dock.Answer(remove, `{"success":false}`)
	if msg := suite.CallErr(t, "notification_delete", map[string]any{"channel": "hook", "confirm": true}); !strings.Contains(msg, "without its being said to have succeeded") {
		t.Errorf("answered without success = %q", msg)
	}
	dock.Fail(remove, http.StatusForbidden, "Permission denied")
	if msg := suite.CallErr(t, "notification_delete", map[string]any{"channel": "hook", "confirm": true}); !strings.Contains(msg, "hook was not removed") {
		t.Errorf("a removal Dockhand refuses = %q", msg)
	}
	// removed, and read back as gone; then not to be read back
	dock.Answer(remove, `{"success":true}`)
	dock.Then(remove, func(dockhandtest.Request) { dock.Answer(channelList, `[`+mailChannel+`]`) })
	if out = suite.Call(t, "notification_delete", map[string]any{"channel": 6, "confirm": true}); !acctest.BoolOf(out["removed"]) || out["channel"] != "hook" {
		t.Errorf("a channel removed = %v", out)
	}
	if msg := suite.CallErr(t, "notification_delete", map[string]any{"channel": "hook", "confirm": true}); !strings.Contains(msg, `no notification channel is named or numbered "hook": Dockhand keeps mail`) {
		t.Errorf("removing it again = %q", msg)
	}
	dock.Answer(channelList, `[`+hookChannel+`]`)
	dock.Then(remove, func(dockhandtest.Request) { dock.Fail(channelList, http.StatusInternalServerError, "Failed") })
	if out = suite.Call(t, "notification_delete", map[string]any{"channel": "hook", "confirm": true}); acctest.BoolOf(out["removed"]) || !strings.Contains(acctest.Str(out["note"]), "could not be listed afterwards") {
		t.Errorf("a channel removed and not read back = %v", out)
	}
}

// TestNotificationsWhenDockhandFails is each notification tool with what it reads first failing, and a channel Dockhand keeps with nothing of how it
// sends.
func TestNotificationsWhenDockhandFails(t *testing.T) {
	t.Parallel()

	const bare = `{"id":9,"type":"apprise","name":"bare","enabled":true,"eventTypes":[]}`
	dock := withChannels(t)
	dock.Answer(channelList, `[`+hookChannel+`,`+bare+`]`)
	dock.Answer("PUT /api/notifications/9", bare)
	dock.Answer("POST /api/environments/2/notifications", `{"id":3}`)
	suite := session(t, dock, everything)

	// a channel with nothing of how it sends is listed as sending nowhere, and given somewhere to send
	out := suite.Call(t, "notification_list", nil)
	if row := acctest.Rows(t, out["channels"], "channels")[0]; row["name"] != "bare" || len(acctest.Strs(t, row["to"], "to")) != 0 || row["events"] != nil || row["all_events"] != nil {
		t.Errorf("a channel with nothing of how it sends = %v", row)
	}
	if out = suite.Call(t, "notification_edit", map[string]any{"channel": "bare", "urls": []any{"ntfy://ntfy.example/alerts"}}); len(acctest.Strs(t, out["changes"], "changes")) != 1 || acctest.Strs(t, acctest.Object(t, sent(t, dock.Last("PUT /api/notifications/9"))["config"], "config")["urls"], "urls")[0] != "ntfy://ntfy.example/alerts" {
		t.Errorf("it given somewhere to send = %v", out)
	}

	// what names a channel is its name or its number, wherever one is named
	for tool, args := range map[string]map[string]any{"notification_test": {"channel": 6.5}, "notification_link_delete": {"channel": true, "env": "alpha"}, "notification_delete": {"channel": []any{"hook"}}} {
		if msg := suite.CallErr(t, tool, args); !strings.Contains(msg, "a channel's number is a whole one") && !strings.Contains(msg, "it is a channel's name or its number") {
			t.Errorf("%s with %v = %q", tool, args, msg)
		}
	}

	// a tie made, and the host not to be asked afterwards
	dock.Then("POST /api/environments/2/notifications", func(dockhandtest.Request) { dock.Fail(betaTies, http.StatusForbidden, "Permission denied") })
	if out = suite.Call(t, "notification_link", map[string]any{"channel": "hook", "env": "beta"}); !strings.Contains(acctest.Str(out["note"]), "the tie could not be read back") || out["link"] != nil {
		t.Errorf("a tie made and not read back = %v", out)
	}

	// a new name is looked for among the channels again, and that reading failing changes nothing
	before := len(dock.Requests(channelList))
	dock.Then(channelList, func(dockhandtest.Request) {
		if len(dock.Requests(channelList)) > before {
			dock.Fail(channelList, http.StatusInternalServerError, "Failed")
		}
	})
	if msg := suite.CallErr(t, "notification_edit", map[string]any{"channel": "hook", "name": "other"}); !strings.Contains(msg, "the notification channels could not be listed") || len(dock.Requests("PUT /api/notifications/6")) != 0 {
		t.Errorf("a rename with the names in use not to be read = %q", msg)
	}
	dock.Then(channelList, func(dockhandtest.Request) {})
	dock.Answer(channelList, `[`+hookChannel+`]`)

	// the events there are not to be read: nothing that names or checks one goes on
	dock.Fail(eventList, http.StatusInternalServerError, "Failed")
	for tool, args := range map[string]map[string]any{
		"notification_test":        {"event": "container_started", "env": "alpha"},
		"notification_create":      {"name": "pager", "urls": []any{"ntfy://ntfy.example/alerts"}},
		"notification_edit":        {"channel": "hook", "enabled": false},
		"notification_link":        {"channel": "hook", "env": "alpha"},
		"notification_link_delete": {"channel": "hook", "env": "alpha"},
	} {
		if msg := suite.CallErr(t, tool, args); !strings.Contains(msg, "the events a channel can be told of could not be listed") {
			t.Errorf("%s with the events not to be read = %q", tool, msg)
		}
	}

	// and the channels: a name is not taken on trust as free
	dock.Answer(eventList, fourEvents)
	dock.Fail(channelList, http.StatusInternalServerError, "Failed")
	if msg := suite.CallErr(t, "notification_create", map[string]any{"name": "pager", "urls": []any{"ntfy://ntfy.example/alerts"}}); !strings.Contains(msg, "the notification channels could not be listed") || len(dock.Requests(makeChannel)) != 0 {
		t.Errorf("a channel made with the names in use not to be read = %q", msg)
	}
}
