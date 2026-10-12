//go:build acceptance

package dockhand_test

import (
	"net/http"
	"net/http/cookiejar"
	"slices"
	"strings"
	"testing"

	"github.com/katbyte/dockhand-mcp/lib/testenv"
	"github.com/katbyte/dockhand-mcp/sdk/dockhand"
	"github.com/katbyte/pandorest/client"
)

// The made-up user these tests make and remove, and the two passwords it has.
//

const (
	secondUser     = "it-second-user"
	secondPassword = "it-second-password"
	thirdPassword  = "it-third-password"
)

// userNamed is the user the server lists under a name, or nil.
func userNamed(t *testing.T, c *dockhand.Client, name string) *dockhand.GetUsersAnswer {
	t.Helper()

	listed, err := c.GetUsers(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	for i := range listed.Answer {
		if listed.Answer[i].Username == name {
			return &listed.Answer[i]
		}
	}

	return nil
}

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

// loginIs reports whether the server has its login on, asked with nothing.
func loginIs(t *testing.T) bool {
	t.Helper()

	open, err := dockhand.NewWithoutLogin(world.Server)
	if err != nil {
		t.Fatal(err)
	}
	session, err := open.GetAuthSession(noCancel(t))
	if err != nil {
		t.Fatal(err)
	}

	return session.Answer.AuthEnabled
}

// A user is made, read, changed a field at a time, and removed, on a server with its login off and on; and with it on, what a new password, being
// switched off and losing the administrator's mark each do to its session and its token, and what the last administrator is refused.
func TestUsersMadeChangedSwitchedOffAndRemoved(t *testing.T) {
	c := dock(t)
	ctx := t.Context()

	// a run that was cut short may have left it behind
	if left := userNamed(t, c, secondUser); left != nil {
		if _, err := c.DeleteUsersById(ctx, left.Id, dockhand.DeleteUsersByIdOptions{}); err != nil {
			t.Fatal(err)
		}
	}
	before, err := c.GetUsers(ctx)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		left, failed := c.GetUsers(noCancel(t))
		if failed != nil {
			return
		}
		for i := range left.Answer {
			if left.Answer[i].Username == secondUser {
				_, _ = c.DeleteUsersById(noCancel(t), left.Answer[i].Id, dockhand.DeleteUsersByIdOptions{})
			}
		}
	})

	// made: an administrator, as every user is, switched on, and never signed in
	made, err := c.PostUsers(ctx, dockhand.PostUsersRequest{Username: secondUser, Password: secondPassword, Email: "second@example.com", DisplayName: "Second"})
	if err != nil {
		t.Fatal(err)
	}
	if !made.Answer.IsAdmin || !made.Answer.IsActive || made.Answer.DisplayName != "Second" || made.Answer.Email != "second@example.com" || made.Answer.CreatedAt.IsZero() || made.Answer.AutoLoggedIn {
		t.Errorf("a user as it was made = %+v", made.Answer)
	}
	id := made.Answer.Id
	listed := userNamed(t, c, secondUser)
	if listed == nil || listed.Id != id || !listed.IsAdmin || !listed.IsActive || listed.MfaEnabled || !listed.LastLogin.IsZero() || listed.CreatedAt.IsZero() || listed.AuthProvider != "local" {
		t.Fatalf("the user as listed = %+v", listed)
	}
	if one, err := c.GetUsersById(ctx, id); err != nil || one.Answer.Username != secondUser || one.Answer.Email != "second@example.com" {
		t.Errorf("the user read by its number = %+v, %v", one.Answer, err)
	}

	// a name in use and a password too short are refused, and nothing is made by either
	if _, err := c.PostUsers(ctx, dockhand.PostUsersRequest{Username: secondUser, Password: secondPassword}); client.StatusCode(err) != http.StatusConflict {
		t.Errorf("a second user by the same name = %v, want a 409", err)
	}
	if _, err := c.PostUsers(ctx, dockhand.PostUsersRequest{Username: secondUser + "-short", Password: "short"}); client.StatusCode(err) != http.StatusBadRequest {
		t.Errorf("a user with a password of five characters = %v, want a 400", err)
	}
	if after, err := c.GetUsers(ctx); err != nil || len(after.Answer) != len(before.Answer)+1 {
		t.Fatalf("%d users after one was made and two were refused, want %d (%v)", len(after.Answer), len(before.Answer)+1, err)
	}

	// changed a field at a time: what is not sent is kept, and an empty text forgets, which the generated call cannot send
	changed, err := c.PutUsersById(ctx, id, dockhand.PutUsersByIdRequest{DisplayName: "Other"})
	if err != nil || changed.Answer.DisplayName != "Other" || changed.Answer.Email != "second@example.com" || changed.Answer.Username != secondUser {
		t.Errorf("a display name changed = %+v, %v", changed.Answer, err)
	}
	forgot, err := c.ForgetUserDetails(ctx, int(id), true, true)
	if err != nil || forgot.Answer.Email != "" || forgot.Answer.DisplayName != "" || forgot.Answer.Username != secondUser {
		t.Errorf("an email and a display name forgotten = %+v, %v", forgot.Answer, err)
	}
	if now := userNamed(t, c, secondUser); now == nil || now.Email != "" || now.DisplayName != "" {
		t.Errorf("the user once they were forgotten = %+v", now)
	}
	if _, err := c.PutUsersById(ctx, id, dockhand.PutUsersByIdRequest{Password: "short"}); client.StatusCode(err) != http.StatusBadRequest {
		t.Errorf("a password of five characters = %v, want a 400", err)
	}

	if world.Stage == testenv.StageLogin {
		whatAUserLoses(t, c, int(id))
	} else {
		// with the login off who is switched on or an administrator is answered as changed and is not
		for what, request := range map[string]dockhand.PutUsersByIdRequest{"switched off": {IsActive: new(false)}, "unmarked": {IsAdmin: new(false)}} {
			if answered, err := c.PutUsersById(ctx, id, request); err != nil || !answered.Answer.IsActive || !answered.Answer.IsAdmin {
				t.Errorf("a user %s with the login off = %+v, %v: Dockhand answers 200 and changes neither", what, answered.Answer, err)
			}
		}
		// and there are no tokens to list
		if _, err := c.GetAuthTokens(ctx); client.StatusCode(err) != http.StatusBadRequest || !strings.Contains(err.Error(), "Authentication is not enabled") {
			t.Errorf("the tokens with the login off = %v, want a 400 that says it is not enabled", err)
		}
	}

	// removed, and then not there for any call
	gone, err := c.DeleteUsersById(ctx, id, dockhand.DeleteUsersByIdOptions{})
	if err != nil || !gone.Answer.Success || gone.Answer.AuthDisabled {
		t.Fatalf("removing the user = %+v, %v", gone.Answer, err)
	}
	if _, err := c.GetUsersById(ctx, id); !client.IsNotFound(err) {
		t.Errorf("the user once removed = %v, want a 404", err)
	}
	if _, err := c.PutUsersById(ctx, id, dockhand.PutUsersByIdRequest{DisplayName: "Nobody"}); !client.IsNotFound(err) {
		t.Errorf("changing a user that is gone = %v, want a 404", err)
	}
	if _, err := c.DeleteUsersById(ctx, id, dockhand.DeleteUsersByIdOptions{}); !client.IsNotFound(err) {
		t.Errorf("removing a user that is gone = %v, want a 404", err)
	}
	after, err := c.GetUsers(ctx)
	if err != nil || len(after.Answer) != len(before.Answer) {
		t.Fatalf("%d users at the end, want the %d there were (%v)", len(after.Answer), len(before.Answer), err)
	}

	// the only user there is, with the login on, is what Dockhand does guard: unmarking it and removing it are refused unless told the login may go,
	// and nothing here tells it so. Switching it off is refused the same way, and is not asked here: were it ever done, nothing could undo it
	if world.Stage == testenv.StageLogin && len(after.Answer) == 1 {
		only := after.Answer[0]
		if _, err := c.PutUsersById(ctx, only.Id, dockhand.PutUsersByIdRequest{IsAdmin: new(false)}); client.StatusCode(err) != http.StatusConflict || !strings.Contains(err.Error(), "last admin") {
			t.Errorf("the only user unmarked = %v, want a 409 that says it is the last", err)
		}
		if _, err := c.DeleteUsersById(ctx, only.Id, dockhand.DeleteUsersByIdOptions{}); client.StatusCode(err) != http.StatusConflict {
			t.Errorf("the only user removed = %v, want a 409", err)
		}
		if still := userNamed(t, c, world.User); still == nil || !still.IsActive || !still.IsAdmin || !loginIs(t) {
			t.Fatalf("the only user after two refusals = %+v, login on %t", still, loginIs(t))
		}
	}
}

// whatAUserLoses is the part of the above that needs the login on: a second user with a session and a token of its own, and what each change to it
// stops.
func whatAUserLoses(t *testing.T, c *dockhand.Client, id int) {
	t.Helper()
	ctx := t.Context()

	session, err := signedIn(t, secondUser, secondPassword)
	if err != nil {
		t.Fatalf("the second user signing in: %v", err)
	}
	minted, err := session.PostAuthTokens(ctx, dockhand.PostAuthTokensRequest{Name: "it-second-token", Password: secondPassword})
	if err != nil || minted.Answer.Token == "" {
		t.Fatalf("the second user minting a token: %v", err)
	}
	theirs, err := dockhand.Connect(world.Server, minted.Answer.Token)
	if err != nil {
		t.Fatal(err)
	}

	// a token lists its own user's tokens and nobody else's, and can neither make one nor revoke one
	own, err := theirs.GetAuthTokens(ctx)
	if err != nil || len(own.Answer) != 1 || own.Answer[0].Name != "it-second-token" || own.Answer[0].TokenPrefix == "" || !strings.Contains(minted.Answer.Token, own.Answer[0].TokenPrefix) {
		t.Errorf("the tokens the second user's token lists = %+v, %v", own.Answer, err)
	}
	if mine, err := c.GetAuthTokens(ctx); err != nil || len(mine.Answer) == 0 || slices.ContainsFunc(mine.Answer, func(k dockhand.GetAuthTokensAnswer) bool { return k.Name == "it-second-token" }) {
		t.Errorf("the tokens the administrator's token lists = %+v, %v: its own and not the second user's", mine.Answer, err)
	}
	if _, err := theirs.PostAuthTokens(ctx, dockhand.PostAuthTokensRequest{Name: "it-made-by-a-token", Password: secondPassword}); !client.IsForbidden(err) || !strings.Contains(err.Error(), "requires a session login") {
		t.Errorf("a token made with a token = %v, want a 403 that asks for a session", err)
	}
	if _, err := theirs.DeleteAuthTokensById(ctx, own.Answer[0].Id); !client.IsForbidden(err) {
		t.Errorf("a token revoked with a token = %v, want a 403", err)
	}

	// a new password ends the session and not the token, and is the one that signs in
	if _, err := c.PutUsersById(ctx, dockhand.UserID(id), dockhand.PutUsersByIdRequest{Password: thirdPassword}); err != nil {
		t.Fatal(err)
	}
	if _, err := session.GetEnvironments(ctx); !client.IsUnauthorized(err) {
		t.Errorf("the session from before the password changed = %v, want a 401", err)
	}
	if _, err := theirs.GetEnvironments(ctx); err != nil {
		t.Errorf("the token after the password changed: %v", err)
	}
	if _, err := signedIn(t, secondUser, secondPassword); !client.IsUnauthorized(err) {
		t.Errorf("signing in with the old password = %v, want a 401", err)
	}
	if _, err := signedIn(t, secondUser, thirdPassword); err != nil {
		t.Errorf("signing in with the new password: %v", err)
	}

	// switched off: the token and signing in stop at once, and both come back with it
	off, err := c.PutUsersById(ctx, dockhand.UserID(id), dockhand.PutUsersByIdRequest{IsActive: new(false)})
	if err != nil || off.Answer.IsActive {
		t.Fatalf("switching the user off = %+v, %v", off.Answer, err)
	}
	if _, err := theirs.GetEnvironments(ctx); !client.IsUnauthorized(err) {
		t.Errorf("the token of a user that is switched off = %v, want a 401", err)
	}
	if _, err := signedIn(t, secondUser, thirdPassword); !client.IsUnauthorized(err) {
		t.Errorf("a user that is switched off signing in = %v, want a 401", err)
	}
	if on, err := c.PutUsersById(ctx, dockhand.UserID(id), dockhand.PutUsersByIdRequest{IsActive: new(true)}); err != nil || !on.Answer.IsActive {
		t.Fatalf("switching the user on again = %+v, %v", on.Answer, err)
	}
	if _, err := theirs.GetEnvironments(ctx); err != nil {
		t.Errorf("the token once the user is switched on again: %v", err)
	}

	// the administrator's mark taken away holds it to nothing on a server with no licence: it lists the users still, and marks itself again
	less, err := c.PutUsersById(ctx, dockhand.UserID(id), dockhand.PutUsersByIdRequest{IsAdmin: new(false)})
	if err != nil || less.Answer.IsAdmin {
		t.Fatalf("taking the mark from the user = %+v, %v", less.Answer, err)
	}
	if _, err := theirs.GetUsers(ctx); err != nil {
		t.Errorf("a user that is no administrator listing the users: %v. If Dockhand now refuses it, the tools' word that the mark holds nobody to less is out of date", err)
	}

	// with the second user no administrator the first is the only one, and Dockhand lets it unmark itself all the same: its guard is for the only
	// user there is, not the last administrator. That leaves no administrator at all and the login on, which the tools refuse to do; here it is put
	// straight back
	admin := userNamed(t, c, world.User)
	if admin == nil {
		t.Fatalf("the administrator %s is not listed", world.User)
	}
	if none, err := c.PutUsersById(ctx, admin.Id, dockhand.PutUsersByIdRequest{IsAdmin: new(false)}); err != nil || none.Answer.IsAdmin || !loginIs(t) {
		t.Errorf("the only administrator unmarking itself while another user exists = %+v, %v, login on %t: Dockhand does it and leaves the login on. If it now refuses, its guard has widened and the notes are out of date", none.Answer, err, loginIs(t))
	}
	if back, err := c.PutUsersById(ctx, admin.Id, dockhand.PutUsersByIdRequest{IsAdmin: new(true)}); err != nil || !back.Answer.IsAdmin {
		t.Fatalf("the administrator marking itself again = %+v, %v", back.Answer, err)
	}
	if again, err := theirs.PutUsersById(ctx, dockhand.UserID(id), dockhand.PutUsersByIdRequest{IsAdmin: new(true)}); err != nil || !again.Answer.IsAdmin {
		t.Errorf("a user that is no administrator marking itself one = %+v, %v. If Dockhand now refuses it, the tools' word that the mark holds nobody to less is out of date", again.Answer, err)
	}

	// removed with its token, which stops with it; the removal itself is the caller's to make
	t.Cleanup(func() {
		if _, err := theirs.GetEnvironments(noCancel(t)); !client.IsUnauthorized(err) {
			t.Errorf("the token of a user that was removed = %v, want a 401", err)
		}
	})
}

// How the server is signed in to is read and set: how long a session lasts, which is bounded without a word, and the default provider, which is not
// checked at all; and, with the login on, the login is switched off and on again and the token from before still works.
func TestTheLoginReadSetAndSwitched(t *testing.T) {
	c := dock(t)
	ctx := t.Context()

	before, err := c.GetAuthSettings(ctx)
	if err != nil {
		t.Fatal(err)
	}
	on := world.Stage == testenv.StageLogin
	if before.Answer.AuthEnabled != on || before.Answer.DefaultProvider != "local" || before.Answer.Id == 0 {
		t.Fatalf("the login as it is = %+v, in the %s stage", before.Answer, world.Stage)
	}
	t.Cleanup(func() {
		after := noCancel(t)
		if on && !loginIs(t) {
			if open, err := dockhand.NewWithoutLogin(world.Server); err == nil {
				_, _ = open.PutAuthSettings(after, dockhand.PutAuthSettingsRequest{AuthEnabled: new(true)})
			}
		}
		if before.Answer.SessionTimeout == 0 {
			_, _ = c.NeverEndSessions(after)
		}
		_, _ = c.PutAuthSettings(after, dockhand.PutAuthSettingsRequest{SessionTimeout: before.Answer.SessionTimeout, DefaultProvider: before.Answer.DefaultProvider})
	})

	// how long a session lasts: taken within its bounds, made the nearer bound outside them, and 0 kept as never
	for asked, want := range map[int]int{7200: 7200, 10: 3600, 99999999: 604800} {
		if set, err := c.PutAuthSettings(ctx, dockhand.PutAuthSettingsRequest{SessionTimeout: asked}); err != nil || set.Answer.SessionTimeout != want {
			t.Errorf("a session of %d seconds = %+v, %v, want %d", asked, set.Answer, err, want)
		}
	}
	if never, err := c.NeverEndSessions(ctx); err != nil || never.Answer.SessionTimeout != 0 || never.Answer.AuthEnabled != on {
		t.Errorf("a session that never ends = %+v, %v", never.Answer, err)
	}
	if read, err := c.GetAuthSettings(ctx); err != nil || read.Answer.SessionTimeout != 0 {
		t.Errorf("a session that never ends, read back = %+v, %v", read.Answer, err)
	}
	// the default provider is saved whatever it is given
	if set, err := c.PutAuthSettings(ctx, dockhand.PutAuthSettingsRequest{DefaultProvider: "no-such-provider"}); err != nil || set.Answer.DefaultProvider != "no-such-provider" {
		t.Errorf("a default provider there is none of = %+v, %v: Dockhand saves it", set.Answer, err)
	}
	if set, err := c.PutAuthSettings(ctx, dockhand.PutAuthSettingsRequest{SessionTimeout: 86400, DefaultProvider: "local"}); err != nil || set.Answer.DefaultProvider != "local" || set.Answer.SessionTimeout != 86400 {
		t.Errorf("the login put back = %+v, %v", set.Answer, err)
	}

	// what is offered at sign-in, and the licence a free server has none of
	offered, err := c.GetAuthProviders(ctx)
	if err != nil || (on && (len(offered.Answer.Providers) == 0 || offered.Answer.Providers[0].Id != "local" || offered.Answer.DefaultProvider != "local")) || (!on && len(offered.Answer.Providers) != 0) {
		t.Errorf("what is offered at sign-in = %+v, %v, in the %s stage", offered.Answer, err, world.Stage)
	}
	if licence, err := c.GetLicense(ctx); err != nil || licence.Answer.Active || licence.Answer.Valid || licence.Answer.Hostname == "" {
		t.Errorf("the licence of a server with none = %+v, %v", licence.Answer, err)
	}
	if _, err := c.PostLicense(ctx, dockhand.PostLicenseRequest{Name: "nobody", Key: "not-a-licence"}); client.StatusCode(err) != http.StatusBadRequest || !strings.Contains(err.Error(), "Invalid license format") {
		t.Errorf("a licence that is none = %v, want a 400 that says its format is not one", err)
	}

	open, err := dockhand.NewWithoutLogin(world.Server)
	if err != nil {
		t.Fatal(err)
	}
	if !on {
		// a server with no user at all does not have its login switched on; one with a user would, so it is only asked of one with none
		listed, failed := c.GetUsers(ctx)
		if failed != nil {
			t.Fatal(failed)
		}
		if len(listed.Answer) != 0 {
			t.Logf("the server has %d users, so the login is not asked on here: it would come on", len(listed.Answer))

			return
		}
		if _, err := open.PutAuthSettings(ctx, dockhand.PutAuthSettingsRequest{AuthEnabled: new(true)}); client.StatusCode(err) != http.StatusBadRequest || !strings.Contains(err.Error(), "without any users") {
			t.Errorf("the login switched on with no user at all = %v, want a 400 that asks for a user first", err)
		}
		if loginIs(t) {
			t.Fatal("the login came on with no user at all, and the rest of this stage wants it off: the first user is made by nobody, signs in and switches it off again by hand")
		}

		return
	}

	// off: nobody is asked anything, a token there is none of included, and no tokens are listed
	off, err := c.PutAuthSettings(ctx, dockhand.PutAuthSettingsRequest{AuthEnabled: new(false)})
	if err != nil || off.Answer.AuthEnabled || loginIs(t) {
		t.Fatalf("switching the login off = %+v, %v", off.Answer, err)
	}
	if _, err := open.GetUsers(ctx); err != nil {
		t.Errorf("the users asked for with nothing, the login off: %v", err)
	}
	stranger, err := dockhand.Connect(world.Server, "dh_0000000000000000000000000000000000000000000")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := stranger.GetUsers(ctx); err != nil {
		t.Errorf("the users asked for with a token there is none of, the login off: %v", err)
	}
	if _, err := c.GetAuthTokens(ctx); client.StatusCode(err) != http.StatusBadRequest {
		t.Errorf("the tokens with the login off = %v, want a 400", err)
	}
	// on again, by nobody: the token from before is taken as before
	if back, err := open.PutAuthSettings(ctx, dockhand.PutAuthSettingsRequest{AuthEnabled: new(true)}); err != nil || !back.Answer.AuthEnabled || !loginIs(t) {
		t.Fatalf("switching the login on again = %+v, %v", back.Answer, err)
	}
	if _, err := c.GetUsers(ctx); err != nil {
		t.Errorf("the token from before the login went off and on: %v", err)
	}
	if _, err := open.GetUsers(ctx); !client.IsUnauthorized(err) {
		t.Errorf("the users asked for with nothing, the login on again = %v, want a 401", err)
	}
}

// A single sign-on provider is saved, read, changed, switched on, offered at sign-in and removed: what a record needs, what it is made with, and that
// its secret never comes back.
func TestSingleSignOnProvidersSavedChangedOfferedAndRemoved(t *testing.T) {
	const name = "it-sso-provider"
	c := dock(t)
	ctx := t.Context()

	listed, err := c.GetAuthOidc(ctx)
	if err != nil {
		t.Fatal(err)
	}
	for i := range listed.Answer {
		// a run that was cut short may have left it behind
		if listed.Answer[i].Name == name {
			if _, err := c.DeleteAuthOidcById(ctx, listed.Answer[i].Id); err != nil {
				t.Fatal(err)
			}
		}
	}
	before, err := c.GetAuthOidc(ctx)
	if err != nil {
		t.Fatal(err)
	}

	// a record needs its address, and says which field it lacks
	if _, err := c.PostAuthOidc(ctx, dockhand.PostAuthOidcRequest{Name: name}); client.StatusCode(err) != http.StatusBadRequest || !strings.Contains(err.Error(), "Missing required field") {
		t.Errorf("a provider with a name and nothing else = %v, want a 400 that names what is missing", err)
	}

	// saved: switched off unless said, with what it asks for and reads filled in, no claim for an administrator, and a mask for its secret
	made, err := c.PostAuthOidc(ctx, dockhand.PostAuthOidcRequest{Name: name, IssuerUrl: "https://sso.invalid", ClientId: "dockhand", ClientSecret: "it-is-not-a-real-secret", RedirectUri: world.Server + "/api/auth/oidc/callback"})
	if err != nil {
		t.Fatal(err)
	}
	id := made.Answer.Id
	t.Cleanup(func() { _, _ = c.DeleteAuthOidcById(noCancel(t), id) })
	if made.Answer.Enabled || made.Answer.ClientSecret != "********" || made.Answer.Scopes != "openid profile email" || made.Answer.UsernameClaim != "preferred_username" || made.Answer.EmailClaim != "email" || made.Answer.DisplayNameClaim != "name" || made.Answer.RoleMappingsClaim != "groups" || made.Answer.AdminClaim != "" || made.Answer.AdminValue != "" || made.Answer.CreatedAt.IsZero() {
		t.Errorf("a provider as it was saved = %+v", made.Answer)
	}
	if one, err := c.GetAuthOidcById(ctx, id); err != nil || one.Answer.Name != name || one.Answer.IssuerUrl != "https://sso.invalid" || one.Answer.ClientSecret != "********" || one.Answer.RedirectUri == "" {
		t.Errorf("the provider read by its number = %+v, %v", one.Answer, err)
	}
	if after, err := c.GetAuthOidc(ctx); err != nil || len(after.Answer) != len(before.Answer)+1 || !slices.ContainsFunc(after.Answer, func(p dockhand.GetAuthOidcAnswer) bool {
		return p.Id == id && p.ClientSecret == "********" && p.Scopes != ""
	}) {
		t.Errorf("the providers once one was saved = %+v, %v", after.Answer, err)
	}

	// changed: only what is sent, and a claim for an administrator named and forgotten, which the generated call cannot do
	claimed, err := c.PutAuthOidcById(ctx, id, dockhand.PutAuthOidcByIdRequest{AdminClaim: "groups", AdminValue: "admins", Scopes: "openid email"})
	if err != nil || claimed.Answer.AdminClaim != "groups" || claimed.Answer.AdminValue != "admins" || claimed.Answer.Scopes != "openid email" || claimed.Answer.Name != name || claimed.Answer.ClientSecret != "********" {
		t.Errorf("a provider's claims changed = %+v, %v", claimed.Answer, err)
	}
	forgot, err := c.ForgetOidcAdminClaim(ctx, id)
	if err != nil || forgot.Answer.AdminClaim != "" || forgot.Answer.AdminValue != "" || forgot.Answer.Scopes != "openid email" {
		t.Errorf("a provider's claim for an administrator forgotten = %+v, %v", forgot.Answer, err)
	}

	// switched on it is offered at sign-in at once, to a caller that sends nothing, and switched off it is not; with the login off nothing is offered
	offers := func() bool {
		open, err := dockhand.NewWithoutLogin(world.Server)
		if err != nil {
			t.Fatal(err)
		}
		offered, err := open.GetAuthProviders(ctx)
		if err != nil {
			t.Fatal(err)
		}

		return slices.ContainsFunc(offered.Answer.Providers, func(p dockhand.GetAuthProvidersAnswerProviders) bool {
			return p.Name == name && p.Type == "oidc" && strings.HasPrefix(p.Id, "oidc:")
		})
	}
	if switched, err := c.PutAuthOidcById(ctx, id, dockhand.PutAuthOidcByIdRequest{Enabled: new(true)}); err != nil || !switched.Answer.Enabled {
		t.Fatalf("switching the provider on = %+v, %v", switched.Answer, err)
	}
	if offers() != (world.Stage == testenv.StageLogin) {
		t.Errorf("a provider that is switched on is offered at sign-in: %t, in the %s stage", offers(), world.Stage)
	}
	if switched, err := c.PutAuthOidcById(ctx, id, dockhand.PutAuthOidcByIdRequest{Enabled: new(false)}); err != nil || switched.Answer.Enabled || offers() {
		t.Errorf("switching the provider off = %+v, %v, offered %t", switched.Answer, err, offers())
	}

	// removed, and then not there
	if gone, err := c.DeleteAuthOidcById(ctx, id); err != nil || !gone.Answer.Success {
		t.Fatalf("removing the provider = %+v, %v", gone.Answer, err)
	}
	if _, err := c.GetAuthOidcById(ctx, id); !client.IsNotFound(err) {
		t.Errorf("the provider once removed = %v, want a 404", err)
	}
	if _, err := c.DeleteAuthOidcById(ctx, id); !client.IsNotFound(err) {
		t.Errorf("removing a provider that is gone = %v, want a 404", err)
	}
	if after, err := c.GetAuthOidc(ctx); err != nil || len(after.Answer) != len(before.Answer) {
		t.Errorf("%d providers at the end, want the %d there were (%v)", len(after.Answer), len(before.Answer), err)
	}
}
