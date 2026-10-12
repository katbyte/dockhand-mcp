//go:build acceptance

package tools_test

import (
	"context"
	"net/http/cookiejar"
	"strings"
	"testing"

	"github.com/katbyte/dockhand-mcp/lib/testenv"
	"github.com/katbyte/dockhand-mcp/sdk/dockhand"
	"github.com/katbyte/go-kt/mcp/acctest"
	"github.com/katbyte/pandorest/client"
)

// The made-up users these journeys make and remove, and the passwords they are given.
//

const (
	journeyUser       = "acc-journey-user"
	journeyRenamed    = "acc-journey-renamed"
	journeyDoorkeeper = "acc-journey-doorkeeper"
	journeyPassword   = "acc-journey-password"
	journeyAnother    = "acc-another-password"
)

// signedIn is a client that kept the cookie signing in set, and what signing in answered: nil when the name and password were taken.
func signedIn(t *testing.T, name, password string) (*dockhand.Client, error) {
	t.Helper()

	jar, err := cookiejar.New(nil)
	if err != nil {
		t.Fatal(err)
	}
	session, err := dockhand.NewWithoutLogin(world.Server, client.WithCookies(jar))
	if err != nil {
		t.Fatal(err)
	}
	_, err = session.PostAuthLogin(t.Context(), dockhand.PostAuthLoginRequest{Username: name, Password: password, Provider: "local"})

	return session, err
}

// usersListed is the users the tools list, by name.
func usersListed(t *testing.T) map[string]map[string]any {
	t.Helper()

	return named(acctest.Rows(t, call(t, "user_list", nil)["users"], "users"), "username")
}

// A user is made, found to be an administrator, renamed and given other details, has them forgotten and its password changed, and is removed; with
// the login on it is also switched off and on and unmarked, and the last administrator is refused all three, which Dockhand itself would do.
func TestJourneyAUserMadeChangedSwitchedOffAndRemoved(t *testing.T) {
	if world == nil {
		t.Skip(testenv.NotConfigured)
	}
	on := world.Stage == testenv.StageLogin
	clean := func() {
		for _, known := range []string{journeyUser, journeyRenamed} {
			if usersListed(t)[known] != nil {
				suite.Call(t, "user_delete", map[string]any{"user": known, "confirm": true})
			}
		}
	}
	clean()
	t.Cleanup(clean)
	before := len(usersListed(t))

	// refused before anything is sent, with no word of the password in the refusal; then said, then made
	args := map[string]any{"username": journeyUser, "password": journeyPassword, "email": "journey@example.com", "display_name": "Journey"}
	if msg := refused(t, "user_create", over(args, map[string]any{"password": "short"})); !strings.Contains(msg, "Dockhand takes none shorter than 8") {
		t.Errorf("a user with a password of five characters = %q", msg)
	}
	if dry := call(t, "user_create", over(args, map[string]any{"dry_run": true})); !acctest.IsBool(dry["dry_run"], true) || dry["user"] != nil || len(usersListed(t)) != before {
		t.Fatalf("a dry run of making a user = %v", dry)
	}
	made := call(t, "user_create", args)
	row := acctest.Object(t, made["user"], "user")
	if row["username"] != journeyUser || !acctest.IsBool(row["admin"], true) || !acctest.IsBool(row["active"], true) || row["email"] != "journey@example.com" || row["display_name"] != "Journey" || row["provider"] != "local" || !strings.Contains(acctest.Str(made["note"]), "made it an administrator") || strings.Contains(acctest.Str(made["note"]), "login is switched off") == on || strings.Contains(flat(made), journeyPassword) {
		t.Fatalf("a user made = %v", made)
	}
	if listed := call(t, "user_list", nil); acctest.Num(t, listed["total"], "total") != before+1 || !acctest.IsBool(listed["login"], on) || (listed["note"] == nil) != on {
		t.Errorf("the users once one was made = %v", listed)
	}
	if msg := refused(t, "user_create", over(args, map[string]any{"username": strings.ToUpper(journeyUser)})); !strings.Contains(msg, "already named") {
		t.Errorf("a second user by the same name = %q", msg)
	}

	// renamed and given other details, twice over: the second time there is nothing to do
	change := map[string]any{"user": journeyUser, "username": journeyRenamed, "email": "other@example.com", "display_name": "Other"}
	if dry := call(t, "user_edit", over(change, map[string]any{"dry_run": true})); len(acctest.Strs(t, dry["changes"], "changes")) != 3 || usersListed(t)[journeyRenamed] != nil {
		t.Errorf("a dry run of changing the user = %v", dry)
	}
	changed := call(t, "user_edit", change)
	if row = acctest.Object(t, changed["user"], "user"); row["username"] != journeyRenamed || row["email"] != "other@example.com" || row["display_name"] != "Other" || len(acctest.Strs(t, changed["changes"], "changes")) != 3 {
		t.Fatalf("the user renamed = %v", changed)
	}
	if again := call(t, "user_edit", over(change, map[string]any{"user": journeyRenamed})); len(acctest.Strs(t, again["changes"], "changes")) != 0 || !strings.Contains(acctest.Str(again["note"]), "that way already") {
		t.Errorf("the same change a second time = %v", again)
	}
	// its email and display name forgotten, and its password changed, of which no answer says a word
	forgot := call(t, "user_edit", map[string]any{"user": journeyRenamed, "email": "", "display_name": "", "password": journeyAnother})
	if row = acctest.Object(t, forgot["user"], "user"); row["email"] != nil || row["display_name"] != nil || len(acctest.Strs(t, forgot["changes"], "changes")) != 3 || !strings.Contains(acctest.Str(forgot["note"]), "its API tokens go on working") || strings.Contains(flat(forgot), journeyAnother) {
		t.Errorf("the user's details forgotten and its password changed = %v", forgot)
	}

	if on {
		// the new password is the one that signs in, and a user that is switched off does not
		if _, err := signedIn(t, journeyRenamed, journeyPassword); !client.IsUnauthorized(err) {
			t.Errorf("signing in with the password it was made with = %v, want a 401", err)
		}
		if _, err := signedIn(t, journeyRenamed, journeyAnother); err != nil {
			t.Errorf("signing in with the password it was given: %v", err)
		}
		off := call(t, "user_edit", map[string]any{"user": journeyRenamed, "active": false})
		if row = acctest.Object(t, off["user"], "user"); !acctest.IsBool(row["active"], false) || !strings.Contains(acctest.Str(off["note"]), "can no longer sign in") {
			t.Errorf("the user switched off = %v", off)
		}
		if _, err := signedIn(t, journeyRenamed, journeyAnother); !client.IsUnauthorized(err) {
			t.Errorf("a user that is switched off signing in = %v, want a 401", err)
		}

		// with the other user switched off, the administrator is the last that is switched on: it is not switched off, unmarked or removed, which
		// Dockhand would do
		for what, args := range map[string]map[string]any{"switched off": {"active": false}, "unmarked": {"admin": false}} {
			if msg := refused(t, "user_edit", over(args, map[string]any{"user": world.User})); !strings.Contains(msg, "the only user that is both switched on and an administrator") {
				t.Errorf("the last administrator %s = %q", what, msg)
			}
		}
		if msg := refused(t, "user_delete", map[string]any{"user": world.User, "confirm": true}); !strings.Contains(msg, "the only user that is both switched on and an administrator") {
			t.Errorf("the last administrator removed = %q", msg)
		}
		if admin := usersListed(t)[world.User]; admin == nil || !acctest.IsBool(admin["active"], true) || !acctest.IsBool(admin["admin"], true) {
			t.Fatalf("the administrator after three refusals = %v", admin)
		}

		// switched on again and unmarked, which is said to hold it to nothing; and marked again
		less := call(t, "user_edit", map[string]any{"user": journeyRenamed, "active": true, "admin": false})
		if row = acctest.Object(t, less["user"], "user"); !acctest.IsBool(row["active"], true) || !acctest.IsBool(row["admin"], false) || !strings.Contains(acctest.Str(less["note"]), "no way to hold a user to less") {
			t.Errorf("the user switched on and unmarked = %v", less)
		}
		if _, err := signedIn(t, journeyRenamed, journeyAnother); err != nil {
			t.Errorf("the user signing in once switched on again: %v", err)
		}
		if marked := call(t, "user_edit", map[string]any{"user": journeyRenamed, "admin": true}); !acctest.IsBool(acctest.Object(t, marked["user"], "user")["admin"], true) {
			t.Errorf("the user marked an administrator again = %v", marked)
		}
	} else if msg := refused(t, "user_edit", map[string]any{"user": journeyRenamed, "active": false}); !strings.Contains(msg, "the login is switched off") || !acctest.IsBool(usersListed(t)[journeyRenamed]["active"], true) {
		// with the login off Dockhand would answer this as done and not do it, so it is not asked
		t.Errorf("a user switched off with the login off = %q", msg)
	}

	// removed: said first, then done
	if would := call(t, "user_delete", map[string]any{"user": journeyRenamed}); !acctest.IsBool(would["removed"], false) || len(acctest.Strs(t, would["would_go"], "would_go")) != 1 || usersListed(t)[journeyRenamed] == nil {
		t.Fatalf("a removal not confirmed = %v", would)
	}
	if gone := call(t, "user_delete", map[string]any{"user": journeyRenamed, "confirm": true}); !acctest.IsBool(gone["removed"], true) || gone["note"] != nil {
		t.Errorf("the user removed = %v", gone)
	}
	if msg := refused(t, "user_delete", map[string]any{"user": journeyRenamed, "confirm": true}); !strings.Contains(msg, "no user is named or numbered") {
		t.Errorf("removing it a second time = %q", msg)
	}
	if len(usersListed(t)) != before {
		t.Errorf("%d users at the end, want the %d there were", len(usersListed(t)), before)
	}
}

// How the Dockhand is signed in to is read and set, and its login switched by the tools. With it on: off, when nobody is asked anything, and on
// again, with this session's token still taken. With it off: not on with nobody who could sign in; then on, which turns this session away, as it has
// no token and none can be made with a tool; and off again by the user it made, who signs in to do it.
func TestJourneyTheLoginReadSetAndSwitched(t *testing.T) {
	if world == nil {
		t.Skip(testenv.NotConfigured)
	}
	on := world.Stage == testenv.StageLogin
	open, err := dockhand.NewWithoutLogin(world.Server)
	if err != nil {
		t.Fatal(err)
	}
	loginIs := func() bool {
		session, failed := open.GetAuthSession(context.WithoutCancel(t.Context()))
		if failed != nil {
			t.Fatal(failed)
		}

		return session.Model.AuthEnabled != nil && *session.Model.AuthEnabled
	}
	// whatever happens the stage is left as it began: a login that is on is switched off by the user this journey made, who signs in to do it, and
	// one that is off is switched on by nobody, as Dockhand lets it be
	t.Cleanup(func() {
		ctx := context.WithoutCancel(t.Context())
		switch {
		case !on && loginIs():
			if keeper, err := signedIn(t, journeyDoorkeeper, journeyPassword); err == nil {
				_, _ = keeper.PutAuthSettings(ctx, dockhand.PutAuthSettingsRequest{AuthEnabled: new(false)})
			}
		case on && !loginIs():
			_, _ = open.PutAuthSettings(ctx, dockhand.PutAuthSettingsRequest{AuthEnabled: new(true)})
		default:
		}
		if listed, err := world.Dock.GetUsers(ctx); err == nil {
			for i := range listed.Model {
				if listed.Model[i].Username == journeyDoorkeeper {
					_, _ = world.Dock.DeleteUsersById(ctx, listed.Model[i].Id, dockhand.DeleteUsersByIdOperationOptions{})
				}
			}
		}
		if loginIs() != on {
			t.Errorf("the login is left %t, and this stage began with it %t: every test after this one is run against the wrong server", loginIs(), on)
		}
	})

	// as it is: on or off as the stage has it, a day to a session, a name and password the only way in, and no licence
	had := call(t, "login_get", nil)
	if !acctest.IsBool(had["enabled"], on) || had["default_provider"] != "local" || !acctest.IsBool(had["licensed"], false) || (had["note"] == nil) != on || (len(acctest.Rows(t, had["providers"], "providers")) != 0) != on {
		t.Fatalf("how the Dockhand is signed in to = %v", had)
	}
	timeout := acctest.Num(t, had["session_timeout"], "session_timeout")

	// how long a session lasts: refused outside its bounds, where Dockhand would change it without a word; set, set to never, and put back, the
	// second time with nothing to do
	if msg := refused(t, "login_set", map[string]any{"session_timeout": 10}); !strings.Contains(msg, "from 3600 seconds") {
		t.Errorf("a session of ten seconds = %q", msg)
	}
	if set := call(t, "login_set", map[string]any{"session_timeout": 7200}); acctest.Num(t, set["session_timeout"], "session_timeout") != 7200 || len(acctest.Strs(t, set["changes"], "changes")) != 1 {
		t.Errorf("a session of two hours = %v", set)
	}
	if never := call(t, "login_set", map[string]any{"session_timeout": 0}); acctest.Num(t, never["session_timeout"], "session_timeout") != 0 || acctest.Num(t, call(t, "login_get", nil)["session_timeout"], "session_timeout") != 0 {
		t.Errorf("a session that never ends = %v", never)
	}
	if back := call(t, "login_set", map[string]any{"session_timeout": timeout}); acctest.Num(t, back["session_timeout"], "session_timeout") != timeout {
		t.Errorf("the session put back = %v", back)
	}
	if again := call(t, "login_set", map[string]any{"session_timeout": timeout}); len(acctest.Strs(t, again["changes"], "changes")) != 0 || !strings.Contains(acctest.Str(again["note"]), "that way already") {
		t.Errorf("the same a second time = %v", again)
	}
	// a default provider that is not offered at sign-in is refused, where Dockhand would save any text
	if msg := refused(t, "login_set", map[string]any{"default_provider": "no-such-provider"}); !strings.Contains(msg, "not offered at sign-in") {
		t.Errorf("a default provider there is none of = %q", msg)
	}

	if on {
		// the tokens of the user this session signs in as, and never the token
		tokens := call(t, "token_list", nil)
		if acctest.Num(t, tokens["total"], "total") < 1 || tokens["note"] != nil || strings.Contains(flat(tokens), world.Token) || acctest.Str(acctest.Rows(t, tokens["tokens"], "tokens")[0]["prefix"]) == "" {
			t.Fatalf("the tokens of this session's user = %v", tokens)
		}

		// off: said first, and nothing changed; then done, and nobody is asked anything
		would := call(t, "login_set", map[string]any{"enabled": false})
		if len(acctest.Strs(t, would["would"], "would")) != 2 || !acctest.IsBool(would["enabled"], true) || !loginIs() {
			t.Fatalf("the login asked off without confirm = %v", would)
		}
		off := call(t, "login_set", map[string]any{"enabled": false, "confirm": true})
		if !acctest.IsBool(off["enabled"], false) || !strings.Contains(acctest.Str(off["note"]), "asks nobody who they are") || loginIs() {
			t.Fatalf("the login switched off = %v", off)
		}
		if _, err := open.GetUsers(t.Context()); err != nil {
			t.Errorf("the users asked for with nothing, the login off: %v", err)
		}
		if none := call(t, "token_list", nil); acctest.Num(t, none["total"], "total") != 0 || !strings.Contains(acctest.Str(none["note"]), "the login is switched off") {
			t.Errorf("the tokens with the login off = %v", none)
		}
		if msg := refused(t, "user_edit", map[string]any{"user": world.User, "active": false}); !strings.Contains(msg, "the login is switched off") {
			t.Errorf("a user switched off with the login off = %q", msg)
		}

		// on again: this session's token is taken as before, which the answer says
		back := call(t, "login_set", map[string]any{"enabled": true, "confirm": true})
		if !acctest.IsBool(back["enabled"], true) || back["turned_away"] != nil || back["note"] != "the login is on, and this server is still let in" || !loginIs() {
			t.Fatalf("the login switched on again = %v", back)
		}
		if _, err := open.GetUsers(t.Context()); !client.IsUnauthorized(err) {
			t.Errorf("the users asked for with nothing, the login on again = %v, want a 401", err)
		}
		if again := call(t, "token_list", nil); acctest.Num(t, again["total"], "total") < 1 {
			t.Errorf("the tokens once the login is on again = %v", again)
		}

		return
	}

	// with the login off there are no tokens, which is an answer
	if none := call(t, "token_list", nil); acctest.Num(t, none["total"], "total") != 0 || !strings.Contains(acctest.Str(none["note"]), "the login is switched off") {
		t.Errorf("the tokens of an open Dockhand = %v", none)
	}
	// not on with nobody who could sign in, which Dockhand would allow with any user at all
	if len(usersListed(t)) == 0 {
		if msg := refused(t, "login_set", map[string]any{"enabled": true, "confirm": true}); !strings.Contains(msg, "no user is both switched on and an administrator") || loginIs() {
			t.Fatalf("the login asked on with no user = %q", msg)
		}
	}
	if made := call(t, "user_create", map[string]any{"username": journeyDoorkeeper, "password": journeyPassword}); !strings.Contains(acctest.Str(made["note"]), "nobody is asked for this user until login_set switches it on") {
		t.Errorf("a user made on an open Dockhand = %v", made)
	}
	would := call(t, "login_set", map[string]any{"enabled": true})
	if said := acctest.Strs(t, would["would"], "would"); len(said) != 2 || !strings.Contains(said[0], journeyDoorkeeper+" could sign in") || !strings.Contains(said[1], "never with a tool") || loginIs() {
		t.Fatalf("the login asked on without confirm = %v", would)
	}

	// on: this session has no token, so it is turned away from then on, which the answer says and the next call shows
	locked := call(t, "login_set", map[string]any{"enabled": true, "confirm": true})
	if !acctest.IsBool(locked["enabled"], true) || !acctest.IsBool(locked["turned_away"], true) || !strings.Contains(acctest.Str(locked["note"]), "turns this server away") || !loginIs() {
		t.Fatalf("the login switched on over a session with no token = %v", locked)
	}
	if msg := refused(t, "user_list", nil); !strings.Contains(msg, "401") || !strings.Contains(msg, "DOCKHAND_TOKEN") {
		t.Errorf("a tool once the session is turned away = %q", msg)
	}
	// the user it made signs in and switches the login off again, as a person would in the web interface
	keeper, err := signedIn(t, journeyDoorkeeper, journeyPassword)
	if err != nil {
		t.Fatalf("the user the journey made does not sign in: %v", err)
	}
	if back, err := keeper.PutAuthSettings(t.Context(), dockhand.PutAuthSettingsRequest{AuthEnabled: new(false)}); err != nil || loginIs() {
		t.Fatalf("switching the login off again as that user = %+v, %v", back.Model, err)
	}
	if again := call(t, "login_get", nil); !acctest.IsBool(again["enabled"], false) {
		t.Errorf("the login once it is off again = %v", again)
	}
	if gone := call(t, "user_delete", map[string]any{"user": journeyDoorkeeper, "confirm": true}); !acctest.IsBool(gone["removed"], true) {
		t.Errorf("the user the journey made, removed = %v", gone)
	}
}

// A single sign-on provider is saved switched off, listed without its secret, changed and switched on, offered at sign-in and marked the default, has
// its claim for an administrator forgotten, and is removed.
func TestJourneyASingleSignOnProviderSavedOfferedAndRemoved(t *testing.T) {
	const name, renamed, secret = "acc-journey-sso", "acc-journey-sso-two", "acc-not-a-real-secret" //nolint:gosec // G101: a made-up value for the throwaway server, which is no credential of anything real
	if world == nil {
		t.Skip(testenv.NotConfigured)
	}
	on := world.Stage == testenv.StageLogin
	listed := func() map[string]map[string]any {
		return named(acctest.Rows(t, call(t, "sso_list", nil)["providers"], "providers"), "name")
	}
	clean := func() {
		for _, known := range []string{name, renamed} {
			if listed()[known] != nil {
				suite.Call(t, "sso_delete", map[string]any{"provider": known, "confirm": true})
			}
		}
		if on {
			suite.Call(t, "login_set", map[string]any{"default_provider": "local"})
		}
	}
	clean()
	t.Cleanup(clean)
	before := len(listed())

	// refused before anything is sent; said; and saved, switched off, with nothing of its secret in any answer
	args := map[string]any{"name": name, "issuer_url": "https://sso.invalid", "client_id": "dockhand", "client_secret": secret, "redirect_uri": world.Server + "/api/auth/oidc/callback", "admin_claim": "groups", "admin_value": "admins"}
	if msg := refused(t, "sso_create", over(args, map[string]any{"issuer_url": "sso.invalid"})); !strings.Contains(msg, "it is an address with its scheme") || strings.Contains(msg, secret) {
		t.Errorf("a provider at an address with no scheme = %q", msg)
	}
	if dry := call(t, "sso_create", over(args, map[string]any{"dry_run": true})); !acctest.IsBool(dry["dry_run"], true) || dry["provider"] != nil || len(listed()) != before {
		t.Fatalf("a dry run of saving a provider = %v", dry)
	}
	made := call(t, "sso_create", args)
	row := acctest.Object(t, made["provider"], "provider")
	if row["name"] != name || !acctest.IsBool(row["enabled"], false) || row["issuer_url"] != "https://sso.invalid" || row["scopes"] != "openid profile email" || row["username_claim"] != "preferred_username" || row["admin_claim"] != "groups" || row["admin_value"] != "admins" || !strings.HasPrefix(acctest.Str(row["login_id"]), "oidc:") || made["note"] != nil || strings.Contains(flat(made), secret) || strings.Contains(flat(made), "****") {
		t.Fatalf("a provider saved = %v", made)
	}
	if all := call(t, "sso_list", nil); acctest.Num(t, all["total"], "total") != before+1 || strings.Contains(flat(all), secret) || strings.Contains(flat(all), "****") {
		t.Errorf("the providers once one was saved = %v", all)
	}
	if msg := refused(t, "sso_create", over(args, map[string]any{"name": strings.ToUpper(name)})); !strings.Contains(msg, "already named") {
		t.Errorf("a second provider by the same name = %q", msg)
	}

	// renamed, asked for less, given another secret and switched on, twice over; switched on it is offered at sign-in, where there is a sign-in
	change := map[string]any{"provider": name, "name": renamed, "scopes": "openid email", "enabled": true}
	changed := call(t, "sso_edit", over(change, map[string]any{"client_secret": secret + "-two"}))
	if row = acctest.Object(t, changed["provider"], "provider"); row["name"] != renamed || row["scopes"] != "openid email" || !acctest.IsBool(row["enabled"], true) || len(acctest.Strs(t, changed["changes"], "changes")) != 4 || !strings.Contains(acctest.Str(changed["note"]), "offered to whoever signs in") || strings.Contains(flat(changed), secret) {
		t.Fatalf("the provider changed = %v", changed)
	}
	if again := call(t, "sso_edit", over(change, map[string]any{"provider": renamed})); len(acctest.Strs(t, again["changes"], "changes")) != 0 || !strings.Contains(acctest.Str(again["note"]), "that way already") {
		t.Errorf("the same change a second time = %v", again)
	}
	offered := named(acctest.Rows(t, call(t, "login_get", nil)["providers"], "providers"), "name")
	if (offered[renamed] != nil) != on {
		t.Errorf("a provider that is switched on is offered at sign-in: %v, in the %s stage", offered, world.Stage)
	}

	// its claim for an administrator forgotten, which a second time is nothing to do
	forgot := call(t, "sso_edit", map[string]any{"provider": renamed, "admin_claim": ""})
	if row = acctest.Object(t, forgot["provider"], "provider"); row["admin_claim"] != nil || row["admin_value"] != nil || len(acctest.Strs(t, forgot["changes"], "changes")) != 1 {
		t.Errorf("the provider's claim for an administrator forgotten = %v", forgot)
	}
	if again := call(t, "sso_edit", map[string]any{"provider": renamed, "admin_claim": ""}); !strings.Contains(acctest.Str(again["note"]), "that way already") {
		t.Errorf("forgetting it a second time = %v", again)
	}

	if on {
		// marked the default by its name, which Dockhand keeps by its id, and goes on naming once the provider is gone
		id := acctest.Str(row["login_id"])
		if marked := call(t, "login_set", map[string]any{"default_provider": renamed}); marked["default_provider"] != id {
			t.Errorf("the provider marked the default = %v, want %s", marked, id)
		}
		if would := call(t, "sso_delete", map[string]any{"provider": renamed}); !acctest.IsBool(would["removed"], false) || !strings.Contains(acctest.Str(would["note"]), "it is marked the default provider") || listed()[renamed] == nil {
			t.Fatalf("a removal of the default provider, not confirmed = %v", would)
		}
		if gone := call(t, "sso_delete", map[string]any{"provider": renamed, "confirm": true}); !acctest.IsBool(gone["removed"], true) || !strings.Contains(acctest.Str(gone["note"]), "goes on naming it") {
			t.Errorf("the default provider removed = %v", gone)
		}
		if still := call(t, "login_get", nil); still["default_provider"] != id {
			t.Errorf("the default provider once its provider is gone = %v, want it still named %s", still["default_provider"], id)
		}
		if back := call(t, "login_set", map[string]any{"default_provider": "local"}); back["default_provider"] != "local" {
			t.Errorf("the default provider put back = %v", back)
		}
	} else {
		// with the login off nothing is offered at sign-in, so nothing can be marked the default
		if msg := refused(t, "login_set", map[string]any{"default_provider": renamed}); !strings.Contains(msg, "not offered at sign-in") {
			t.Errorf("a provider marked the default with the login off = %q", msg)
		}
		if would := call(t, "sso_delete", map[string]any{"provider": renamed}); !acctest.IsBool(would["removed"], false) || len(acctest.Strs(t, would["would_go"], "would_go")) != 1 || listed()[renamed] == nil {
			t.Fatalf("a removal not confirmed = %v", would)
		}
		if gone := call(t, "sso_delete", map[string]any{"provider": renamed, "confirm": true}); !acctest.IsBool(gone["removed"], true) {
			t.Errorf("the provider removed = %v", gone)
		}
	}
	if msg := refused(t, "sso_delete", map[string]any{"provider": renamed, "confirm": true}); !strings.Contains(msg, "no single sign-on provider is named or numbered") {
		t.Errorf("removing it a second time = %q", msg)
	}
	if len(listed()) != before {
		t.Errorf("%d providers at the end, want the %d there were", len(listed()), before)
	}
}
