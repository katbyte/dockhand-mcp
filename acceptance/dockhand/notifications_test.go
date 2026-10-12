//go:build acceptance

package dockhand_test

import (
	"slices"
	"strings"
	"testing"

	"github.com/katbyte/dockhand-mcp/lib/testenv"
	"github.com/katbyte/dockhand-mcp/sdk/dockhand"
	"github.com/katbyte/pandorest/client"
)

// channelsNamed is the notification channels the server lists under one name, which more than one can have.
func channelsNamed(t *testing.T, c *dockhand.Client, name string) []dockhand.GetNotificationsAnswer {
	t.Helper()

	listed, err := c.GetNotifications(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	var out []dockhand.GetNotificationsAnswer
	for _, k := range listed.Answer {
		if k.Name == name {
			out = append(out, k)
		}
	}

	return out
}

// setOff sets an event off, for a host unless env is 0, and answers how many channels it was sent to.
func setOff(t *testing.T, c *dockhand.Client, event string, env int) int {
	t.Helper()

	sent, err := c.PostNotificationsTriggerTest(t.Context(), dockhand.PostNotificationsTriggerTestRequest{EventType: event, EnvironmentId: dockhand.EnvironmentID(env), Payload: &dockhand.PostNotificationsTriggerTestRequestPayload{Title: "A test", Message: "Nothing happened: this is " + t.Name() + "."}})
	if err != nil || !sent.Answer.Success || sent.Answer.EventType != event {
		t.Fatalf("setting %s off = %+v, %v", event, sent.Answer, err)
	}

	return sent.Answer.Sent
}

// sameEvents is whether two lists name the same events, in whatever order.
func sameEvents(a, b []string) bool {
	return slices.Equal(slices.Sorted(slices.Values(a)), slices.Sorted(slices.Values(b)))
}

// A notification channel is tried before it is saved, changed, tried again, tied to a host, sent what happens and removed: which of its two lists of
// events decides what, and what switching either off stops. The messages go to the registry of the test network, the one thing on it that answers
// what is posted to it: it takes one from who signs in, and turns one away from who does not.
func TestNotificationChannelsTriedSavedTiedSetOffAndRemoved(t *testing.T) {
	c := dock(t)
	if world.Registry == "" {
		t.Skip("the environment names no registry, which is what takes the messages: .scripts/testenv.sh up starts one")
	}
	taken, turnedAway := "json://"+world.User+":"+world.Password+"@"+world.Registry+"/v2/", "json://"+world.Registry+"/v2/"
	name := named(t, "hook")
	alpha, beta := world.Env(testenv.Alpha), world.Env(testenv.Beta)
	for _, old := range slices.Concat(channelsNamed(t, c, name), channelsNamed(t, c, name+"-two")) {
		_, _ = c.DeleteNotificationsById(t.Context(), old.Id)
	}

	// the events there are: each with what it is about, and whether it happens on a host or to Dockhand itself
	events, err := c.GetNotificationsTriggerTest(t.Context())
	if err != nil || len(events.Answer.EventTypes) == 0 {
		t.Fatalf("the events there are: %v", err)
	}
	all := make([]string, 0, len(events.Answer.EventTypes))
	var ofHosts []string
	for _, e := range events.Answer.EventTypes {
		if e.Id == "" || e.Label == "" || e.Description == "" || e.Group == "" || (e.Scope != "environment" && e.Scope != "system") {
			t.Errorf("an event = %+v", e)
		}
		all = append(all, e.Id)
		if e.Scope == "environment" {
			ofHosts = append(ofHosts, e.Id)
		}
	}
	if !slices.Contains(ofHosts, "container_started") || !slices.Contains(ofHosts, "container_oom") || !slices.Contains(all, "license_expiring") || slices.Contains(ofHosts, "license_expiring") {
		t.Fatalf("the events of a host are %v, of %v", ofHosts, all)
	}

	// tried before it is saved: what the other end says is an answer, and only nowhere to send at all is refused
	for what, tc := range map[string]struct {
		with    dockhand.PostNotificationsTestRequest
		success bool
		says    string
	}{
		"a service that takes it":            {dockhand.PostNotificationsTestRequest{Type: "apprise", Config: &dockhand.PostNotificationsTestRequestConfig{Urls: []string{taken}}}, true, "sent successfully"},
		"a service that turns it away":       {dockhand.PostNotificationsTestRequest{Type: "apprise", Config: &dockhand.PostNotificationsTestRequestConfig{Urls: []string{turnedAway}}}, false, "Webhook error 401"},
		"an address on Dockhand's own host":  {dockhand.PostNotificationsTestRequest{Type: "apprise", Config: &dockhand.PostNotificationsTestRequestConfig{Urls: []string{"json://127.0.0.1:5000/v2/"}}}, false, "loopback address"},
		"an address of no service it knows":  {dockhand.PostNotificationsTestRequest{Type: "apprise", Config: &dockhand.PostNotificationsTestRequestConfig{Urls: []string{"http://" + world.Registry + "/v2/"}}}, false, "Unsupported Apprise protocol: http"},
		"a kind of channel there is none of": {dockhand.PostNotificationsTestRequest{Type: "pigeon", Config: &dockhand.PostNotificationsTestRequestConfig{Urls: []string{turnedAway}}}, false, "Unknown notification type"},
		"a mail server there is none of":     {dockhand.PostNotificationsTestRequest{Type: "smtp", Config: &dockhand.PostNotificationsTestRequestConfig{Host: "nowhere.invalid", Port: 587, FromEmail: "dockhand@example.com", ToEmails: []string{"ops@example.com"}}}, false, "SMTP error"},
	} {
		tried, failed := c.PostNotificationsTest(t.Context(), tc.with)
		if failed != nil || tried.Answer.Success != tc.success || !strings.Contains(tried.Answer.Message+tried.Answer.Error, tc.says) {
			t.Errorf("%s = %+v, %v, want %q", what, tried.Answer, failed, tc.says)
		}
	}
	if _, err := c.PostNotificationsTest(t.Context(), dockhand.PostNotificationsTestRequest{Type: "apprise", Config: &dockhand.PostNotificationsTestRequestConfig{}}); client.StatusCode(err) != 400 || !strings.Contains(err.Error(), "At least one webhook URL is required") {
		t.Errorf("trying nowhere to send = %v", err)
	}

	// saved: for the events it is given, whatever they are called, and with its address kept and given back whole, login and all
	own := []string{"license_expiring", "repo_prune_failed", "no_such_event"}
	made, err := c.PostNotifications(t.Context(), dockhand.PostNotificationsRequest{Type: "apprise", Name: name, Config: &dockhand.PostNotificationsRequestConfig{Urls: []string{taken}}, EventTypes: own})
	if err != nil || made.Answer.Id == 0 || made.Answer.Name != name || made.Answer.Type != "apprise" || !made.Answer.Enabled || made.Answer.Config == nil || !slices.Equal(made.Answer.Config.Urls, []string{taken}) || !slices.Equal(made.Answer.EventTypes, own) || made.Answer.CreatedAt.IsZero() {
		t.Fatalf("saving a channel = %+v, %v", made.Answer, err)
	}
	id := made.Answer.Id
	t.Cleanup(func() { _, _ = c.DeleteNotificationsById(noCancel(t), id) })

	// a second of the same name is saved as well, here mail and switched off: given no events it is told of every one, and its password is never
	// given back
	mail, err := c.PostNotifications(t.Context(), dockhand.PostNotificationsRequest{Type: "smtp", Name: name, Enabled: new(false), Config: &dockhand.PostNotificationsRequestConfig{Host: "mail.example", Port: 587, Username: "sender", Password: "not-a-real-password", FromEmail: "dockhand@example.com", ToEmails: []string{"ops@example.com"}}})
	if err != nil || mail.Answer.Id == 0 || mail.Answer.Id == id || mail.Answer.Type != "smtp" || mail.Answer.Enabled || mail.Answer.Config == nil || mail.Answer.Config.Password != "********" || mail.Answer.Config.Host != "mail.example" || mail.Answer.Config.Port != 587 || mail.Answer.Config.Username != "sender" || !slices.Equal(mail.Answer.Config.ToEmails, []string{"ops@example.com"}) || !sameEvents(mail.Answer.EventTypes, all) {
		t.Fatalf("saving a mail channel of the same name = %+v, %v", mail.Answer, err)
	}
	letters := mail.Answer.Id
	t.Cleanup(func() { _, _ = c.DeleteNotificationsById(noCancel(t), letters) })
	if both := channelsNamed(t, c, name); len(both) != 2 {
		t.Errorf("two channels of one name are listed as %+v", both)
	}
	for what, tc := range map[string]struct {
		with dockhand.PostNotificationsRequest
		says string
	}{
		"nowhere to send":      {dockhand.PostNotificationsRequest{Type: "apprise", Name: name + "-none", Config: &dockhand.PostNotificationsRequestConfig{}}, "Webhook config requires at least one URL"},
		"no name":              {dockhand.PostNotificationsRequest{Type: "apprise", Config: &dockhand.PostNotificationsRequestConfig{Urls: []string{turnedAway}}}, "Type, name, and config are required"},
		"a kind there is none": {dockhand.PostNotificationsRequest{Type: "pigeon", Name: name + "-none", Config: &dockhand.PostNotificationsRequestConfig{Urls: []string{turnedAway}}}, "Type must be smtp or apprise"},
	} {
		if _, err := c.PostNotifications(t.Context(), tc.with); client.StatusCode(err) != 400 || !strings.Contains(err.Error(), tc.says) {
			t.Errorf("saving a channel with %s = %v, want %q", what, err, tc.says)
		}
	}

	// read by its number
	if one, err := c.GetNotificationsById(t.Context(), id); err != nil || one.Answer.Name != name || one.Answer.Config == nil || !slices.Equal(one.Answer.Config.Urls, []string{taken}) {
		t.Errorf("the channel by its number = %+v, %v", one.Answer, err)
	}
	if _, err := c.GetNotificationsById(t.Context(), 99999); !client.IsNotFound(err) {
		t.Errorf("a channel there is none of = %v", err)
	}

	// changed: what is not sent is kept, and how it sends is taken whole or not at all
	if renamed, err := c.PutNotificationsById(t.Context(), id, dockhand.PutNotificationsByIdRequest{Name: name + "-two"}); err != nil || renamed.Answer.Name != name+"-two" || !renamed.Answer.Enabled || renamed.Answer.Config == nil || !slices.Equal(renamed.Answer.Config.Urls, []string{taken}) || !slices.Equal(renamed.Answer.EventTypes, own) {
		t.Errorf("renaming it = %+v, %v", renamed.Answer, err)
	}
	if renamed, err := c.PutNotificationsById(t.Context(), id, dockhand.PutNotificationsByIdRequest{Name: name}); err != nil || renamed.Answer.Name != name {
		t.Errorf("naming it as it was = %+v, %v", renamed.Answer, err)
	}
	if _, err := c.PutNotificationsById(t.Context(), id, dockhand.PutNotificationsByIdRequest{Config: &dockhand.PutNotificationsByIdRequestConfig{Urls: []string{}}}); client.StatusCode(err) != 400 || !strings.Contains(err.Error(), "at least one URL") {
		t.Errorf("leaving it nowhere to send = %v", err)
	}
	if _, err := c.PutNotificationsById(t.Context(), letters, dockhand.PutNotificationsByIdRequest{Config: &dockhand.PutNotificationsByIdRequestConfig{Host: "mail2.example"}}); client.StatusCode(err) != 400 || !strings.Contains(err.Error(), "SMTP config requires host, port, from_email, and to_emails") {
		t.Errorf("giving a mail channel its server alone = %v", err)
	}
	// the mask sent back in place of the password is taken to mean the one that is kept
	if moved, err := c.PutNotificationsById(t.Context(), letters, dockhand.PutNotificationsByIdRequest{Config: &dockhand.PutNotificationsByIdRequestConfig{Host: "mail2.example", Port: 587, Username: "sender", Password: "********", FromEmail: "dockhand@example.com", ToEmails: []string{"ops@example.com"}}}); err != nil || moved.Answer.Config == nil || moved.Answer.Config.Host != "mail2.example" || moved.Answer.Config.Password != "********" || moved.Answer.Enabled || !sameEvents(moved.Answer.EventTypes, all) {
		t.Errorf("moving a mail channel to another server = %+v, %v", moved.Answer, err)
	}
	if _, err := c.PutNotificationsById(t.Context(), 99999, dockhand.PutNotificationsByIdRequest{Name: "none"}); !client.IsNotFound(err) {
		t.Errorf("changing a channel there is none of = %v", err)
	}

	// tried as saved: handed over, turned away with the reason, and refused only for a channel there is none of
	if tried, err := c.PostNotificationsByIdTest(t.Context(), id); err != nil || !tried.Answer.Success || !strings.Contains(tried.Answer.Message, "sent successfully") {
		t.Errorf("a test through the channel = %+v, %v", tried.Answer, err)
	}
	if tried, err := c.PostNotificationsByIdTest(t.Context(), letters); err != nil || tried.Answer.Success || !strings.Contains(tried.Answer.Error, "SMTP error") {
		t.Errorf("a test through a mail server there is none of = %+v, %v", tried.Answer, err)
	}
	if _, err := c.PostNotificationsByIdTest(t.Context(), 99999); !client.IsNotFound(err) {
		t.Errorf("a test through a channel there is none of = %v", err)
	}

	// tied to a host: for every event of a host unless told which, whatever the channel's own list says, and once
	tied, err := c.PostEnvironmentsByIdNotifications(t.Context(), alpha, dockhand.PostEnvironmentsByIdNotificationsRequest{NotificationId: id})
	if err != nil || tied.Answer.NotificationId != id || tied.Answer.EnvironmentId != alpha || !tied.Answer.Enabled || !sameEvents(tied.Answer.EventTypes, ofHosts) || tied.Answer.ChannelName != name || tied.Answer.ChannelType != "apprise" || !tied.Answer.ChannelEnabled {
		t.Fatalf("tying it to a host = %+v, %v", tied.Answer, err)
	}
	if _, err := c.PostEnvironmentsByIdNotifications(t.Context(), alpha, dockhand.PostEnvironmentsByIdNotificationsRequest{NotificationId: id}); client.StatusCode(err) != 409 {
		t.Errorf("tying it a second time = %v", err)
	}
	if _, err := c.PostEnvironmentsByIdNotifications(t.Context(), alpha, dockhand.PostEnvironmentsByIdNotificationsRequest{NotificationId: 99999}); client.StatusCode(err) != 500 || !strings.Contains(err.Error(), "FOREIGN KEY") {
		t.Errorf("tying a channel there is none of = %v", err)
	}
	if _, err := c.PostEnvironmentsByIdNotifications(t.Context(), 99999, dockhand.PostEnvironmentsByIdNotificationsRequest{NotificationId: id}); !client.IsNotFound(err) {
		t.Errorf("tying it to a host there is none of = %v", err)
	}
	if one, err := c.GetEnvironmentsByIdNotificationsByNotificationId(t.Context(), alpha, id); err != nil || one.Answer.NotificationId != id || !one.Answer.Enabled {
		t.Errorf("the tie by its channel = %+v, %v", one.Answer, err)
	}
	if _, err := c.GetEnvironmentsByIdNotificationsByNotificationId(t.Context(), alpha, letters); !client.IsNotFound(err) {
		t.Errorf("the tie of a channel that is not tied = %v", err)
	}
	tiedTo := func(env int) *dockhand.GetEnvironmentsByIdNotificationsAnswer {
		listed, failed := c.GetEnvironmentsByIdNotifications(t.Context(), dockhand.EnvironmentID(env))
		if failed != nil {
			t.Fatal(failed)
		}
		if at := slices.IndexFunc(listed.Answer, func(l dockhand.GetEnvironmentsByIdNotificationsAnswer) bool { return l.NotificationId == id }); at >= 0 {
			return &listed.Answer[at]
		}

		return nil
	}
	if tiedTo(int(alpha)) == nil || tiedTo(int(beta)) != nil {
		t.Errorf("the tie is listed for the first host as %+v and for the second as %+v", tiedTo(int(alpha)), tiedTo(int(beta)))
	}

	// set off: an event of a host goes by the tie, and one of Dockhand's own by the channel's own list, of which the one about its license alone can
	// be set off: another is asked a host for and then sent by that host's ties, which hold none of Dockhand's own, so it reaches nobody
	for what, tc := range map[string]struct {
		event string
		env   int
		sent  int
	}{
		"an event of the host it is tied to":           {"container_started", int(alpha), 1},
		"the same of a host it is not":                 {"container_started", int(beta), 0},
		"the license event, which it is told of":       {"license_expiring", 0, 1},
		"the same with a host, which is not looked at": {"license_expiring", int(beta), 1},
		"another of Dockhand's own that it is told of": {"repo_prune_failed", int(alpha), 0},
	} {
		if sent := setOff(t, c, tc.event, tc.env); sent != tc.sent {
			t.Errorf("%s was sent to %d channels, want %d", what, sent, tc.sent)
		}
	}
	for what, with := range map[string]dockhand.PostNotificationsTriggerTestRequest{
		"an event of a host with none":    {EventType: "container_started", Payload: &dockhand.PostNotificationsTriggerTestRequestPayload{Title: "A test", Message: "Nothing happened."}},
		"one of Dockhand's own with none": {EventType: "repo_prune_failed", Payload: &dockhand.PostNotificationsTriggerTestRequestPayload{Title: "A test", Message: "Nothing happened."}},
		"an event there is none of":       {EventType: "no_such_event", EnvironmentId: alpha, Payload: &dockhand.PostNotificationsTriggerTestRequestPayload{Title: "A test", Message: "Nothing happened."}},
		"an event with nothing to say":    {EventType: "container_started", EnvironmentId: alpha},
	} {
		if _, err := c.PostNotificationsTriggerTest(t.Context(), with); client.StatusCode(err) != 400 {
			t.Errorf("setting off %s = %v", what, err)
		}
	}

	// the tie's own events: what is not an event of a host is dropped from them, and only what is left is sent
	narrowed, err := c.PutEnvironmentsByIdNotificationsByNotificationId(t.Context(), alpha, id, dockhand.PutEnvironmentsByIdNotificationsByNotificationIdRequest{EventTypes: []string{"license_expiring", "repo_prune_failed", "no_such_event", "container_oom"}})
	if err != nil || !slices.Equal(narrowed.Answer.EventTypes, []string{"container_oom"}) || !narrowed.Answer.Enabled {
		t.Fatalf("narrowing the tie = %+v, %v", narrowed.Answer, err)
	}
	if oom, started := setOff(t, c, "container_oom", int(alpha)), setOff(t, c, "container_started", int(alpha)); oom != 1 || started != 0 {
		t.Errorf("with the tie narrowed, what is in it was sent to %d and what is not to %d", oom, started)
	}
	// a channel told of nothing of its own still hears of the host
	if emptied, err := c.PutNotificationsById(t.Context(), id, dockhand.PutNotificationsByIdRequest{EventTypes: []string{}}); err != nil || len(emptied.Answer.EventTypes) != 0 {
		t.Fatalf("telling the channel of nothing of its own = %+v, %v", emptied.Answer, err)
	}
	if own, host := setOff(t, c, "license_expiring", 0), setOff(t, c, "container_oom", int(alpha)); own != 0 || host != 1 {
		t.Errorf("told of nothing of its own, Dockhand's event was sent to %d and the host's to %d", own, host)
	}
	// the tie switched off keeps its events and sends nothing, and switched on sends again
	if off, err := c.PutEnvironmentsByIdNotificationsByNotificationId(t.Context(), alpha, id, dockhand.PutEnvironmentsByIdNotificationsByNotificationIdRequest{Enabled: new(false)}); err != nil || off.Answer.Enabled || !slices.Equal(off.Answer.EventTypes, []string{"container_oom"}) {
		t.Fatalf("switching the tie off = %+v, %v", off.Answer, err)
	}
	if sent := setOff(t, c, "container_oom", int(alpha)); sent != 0 {
		t.Errorf("with the tie off it was sent to %d", sent)
	}
	if on, err := c.PutEnvironmentsByIdNotificationsByNotificationId(t.Context(), alpha, id, dockhand.PutEnvironmentsByIdNotificationsByNotificationIdRequest{Enabled: new(true)}); err != nil || !on.Answer.Enabled {
		t.Fatalf("switching the tie on = %+v, %v", on.Answer, err)
	}
	// the channel switched off sends nothing either way, and its tie says so
	if off, err := c.PutNotificationsById(t.Context(), id, dockhand.PutNotificationsByIdRequest{Enabled: new(false), EventTypes: []string{"license_expiring"}}); err != nil || off.Answer.Enabled {
		t.Fatalf("switching the channel off = %+v, %v", off.Answer, err)
	}
	if own, host := setOff(t, c, "license_expiring", 0), setOff(t, c, "container_oom", int(alpha)); own != 0 || host != 0 {
		t.Errorf("with the channel off, Dockhand's event was sent to %d and the host's to %d", own, host)
	}
	if tie := tiedTo(int(alpha)); tie == nil || !tie.Enabled || tie.ChannelEnabled {
		t.Errorf("the tie of a channel that is off = %+v", tie)
	}
	if _, err := c.PutEnvironmentsByIdNotificationsByNotificationId(t.Context(), alpha, letters, dockhand.PutEnvironmentsByIdNotificationsByNotificationIdRequest{Enabled: new(false)}); !client.IsNotFound(err) {
		t.Errorf("changing a tie there is none of = %v", err)
	}

	// untied: answered as done whether or not there was a tie
	for _, what := range []string{"untying it", "untying it again"} {
		if untied, err := c.DeleteEnvironmentsByIdNotificationsByNotificationId(t.Context(), alpha, id); err != nil || !untied.Answer.Success || tiedTo(int(alpha)) != nil {
			t.Errorf("%s = %+v, %v", what, untied.Answer, err)
		}
	}

	// removed: its ties go with it, and a second removal is refused
	if _, err := c.PostEnvironmentsByIdNotifications(t.Context(), beta, dockhand.PostEnvironmentsByIdNotificationsRequest{NotificationId: id, EventTypes: []string{"container_oom"}, Enabled: new(false)}); err != nil || tiedTo(int(beta)) == nil || tiedTo(int(beta)).Enabled || !slices.Equal(tiedTo(int(beta)).EventTypes, []string{"container_oom"}) {
		t.Fatalf("tying it to the second host, off and for one event = %+v, %v", tiedTo(int(beta)), err)
	}
	if removed, err := c.DeleteNotificationsById(t.Context(), id); err != nil || !removed.Answer.Success || tiedTo(int(beta)) != nil {
		t.Errorf("removing the channel = %+v, %v, and its tie is %+v", removed.Answer, err, tiedTo(int(beta)))
	}
	if _, err := c.DeleteNotificationsById(t.Context(), id); !client.IsNotFound(err) {
		t.Errorf("removing it again = %v", err)
	}
	if removed, err := c.DeleteNotificationsById(t.Context(), letters); err != nil || !removed.Answer.Success || len(channelsNamed(t, c, name)) != 0 {
		t.Errorf("removing the mail channel = %+v, %v", removed.Answer, err)
	}
}
