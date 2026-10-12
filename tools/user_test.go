package tools

import (
	"net/http"
	"strings"
	"testing"

	"github.com/katbyte/dockhand-mcp/sdk/dockhandtest"
	"github.com/katbyte/go-kt/mcp/acctest"
)

// What a 1.0.51 server answers about its users and how they sign in, with invented names: two users, both administrators as every user is made, the
// login on, and nothing but a name and password to sign in with.
//
//nolint:gosec // G101: the tokens are a made-up listing of them, and the calls about tokens are no tokens
const (
	opsUser     = `{"id":1,"username":"ops","email":null,"displayName":"Ops","mfaEnabled":false,"isAdmin":true,"isActive":true,"isSso":false,"authProvider":"local","lastLogin":"2026-10-10T12:00:00.000Z","createdAt":"2026-10-10 10:00:00"}`
	secondUser  = `{"id":2,"username":"second","email":"second@example.com","displayName":"Second","mfaEnabled":true,"isAdmin":true,"isActive":true,"isSso":false,"authProvider":"local","lastLogin":null,"createdAt":"2026-10-10 11:00:00"}`
	secondOff   = `{"id":2,"username":"second","email":"second@example.com","displayName":"Second","mfaEnabled":true,"isAdmin":true,"isActive":false,"isSso":false,"authProvider":"local","lastLogin":null,"createdAt":"2026-10-10 11:00:00"}`
	secondLess  = `{"id":2,"username":"second","email":"second@example.com","displayName":"Second","mfaEnabled":true,"isAdmin":false,"isActive":true,"isSso":false,"authProvider":"local","lastLogin":null,"createdAt":"2026-10-10 11:00:00"}`
	secondBare  = `{"id":2,"username":"renamed","email":null,"displayName":null,"mfaEnabled":true,"isAdmin":true,"isActive":true,"isSso":false,"authProvider":"local","lastLogin":null,"createdAt":"2026-10-10 11:00:00"}`
	thirdUser   = `{"id":3,"username":"third","email":"third@example.com","displayName":"Third","mfaEnabled":false,"isAdmin":true,"isActive":true,"isSso":false,"authProvider":"local","lastLogin":null,"createdAt":"2026-10-10 12:00:00"}`
	thirdMade   = `{"id":3,"username":"third","email":"third@example.com","displayName":"Third","isAdmin":true,"isActive":true,"createdAt":"2026-10-10 12:00:00","autoLoggedIn":false}`
	userChanged = `{"id":2,"username":"second","email":"second@example.com","displayName":"Second","mfaEnabled":true,"isAdmin":true,"isActive":true,"lastLogin":null,"createdAt":"2026-10-10 11:00:00","updatedAt":"2026-10-10T13:00:00.000Z"}`
	loginIsOn   = `{"id":1,"authEnabled":true,"defaultProvider":"local","sessionTimeout":86400,"createdAt":"2026-10-10 10:00:00","updatedAt":"2026-10-10T10:00:00.000Z"}`
	loginIsOut  = `{"id":1,"authEnabled":false,"defaultProvider":"local","sessionTimeout":86400,"createdAt":"2026-10-10 10:00:00","updatedAt":"2026-10-10T10:00:00.000Z"}`
	offered     = `{"providers":[{"id":"local","name":"Local","type":"local"},{"id":"oidc:4","name":"Company","type":"oidc","initiateUrl":"/api/auth/oidc/4/initiate"}],"defaultProvider":"local"}`
	noLicence   = `{"valid":false,"active":false,"hostname":"dockhand"}`
	twoTokens   = `[{"id":1,"name":"first","tokenPrefix":"aaaaaaaa","lastUsed":null,"expiresAt":null,"createdAt":"2026-10-10 10:00:00"},{"id":2,"name":"later","tokenPrefix":"bbbbbbbb","lastUsed":"2026-10-10T12:00:00.000Z","expiresAt":"2027-01-01T00:00:00.000Z","createdAt":"2026-10-10 11:00:00"}]`
	notAllowed  = "Permission denied"

	usersCall   = "GET /api/users"
	userMake    = "POST /api/users"
	userChange  = "PUT /api/users/2"
	userRemove  = "DELETE /api/users/2"
	loginCall   = "GET /api/auth/settings"
	loginSave   = "PUT /api/auth/settings"
	offeredCall = "GET /api/auth/providers"
	licenceCall = "GET /api/license"
	tokensCall  = "GET /api/auth/tokens"
)

// withUsers is a Dockhand with its login on and two users.
func withUsers(t *testing.T) *dockhandtest.Server {
	t.Helper()

	dock := dockhandtest.New(t)
	dock.Answer(usersCall, `[`+secondUser+`,`+opsUser+`]`)
	dock.Answer(loginCall, loginIsOn)

	return dock
}

func TestUserList(t *testing.T) {
	t.Parallel()

	dock := withUsers(t)
	suite := session(t, dock, Options{})

	out := suite.Call(t, "user_list", nil)
	rows := acctest.Rows(t, out["users"], "users")
	if acctest.Num(t, out["total"], "total") != 2 || !acctest.IsBool(out["login"], true) || out["note"] != nil || rows[0]["username"] != "ops" || rows[0]["email"] != nil || rows[0]["display_name"] != "Ops" || rows[0]["last_login"] != "2026-10-10T12:00:00Z" || rows[0]["mfa"] != nil {
		t.Fatalf("the users, by name = %v", out)
	}
	if second := rows[1]; acctest.Num(t, second["id"], "id") != 2 || second["email"] != "second@example.com" || !acctest.IsBool(second["admin"], true) || !acctest.IsBool(second["active"], true) || second["provider"] != "local" || !acctest.IsBool(second["mfa"], true) || second["last_login"] != nil || second["created"] != "2026-10-10T11:00:00Z" {
		t.Errorf("a user that never signed in = %v", second)
	}

	// with the login off the users are listed and said to be asked for by nobody
	dock.Answer(loginCall, loginIsOut)
	if out = suite.Call(t, "user_list", nil); !acctest.IsBool(out["login"], false) || !strings.Contains(acctest.Str(out["note"]), "asks nobody who they are") {
		t.Errorf("the users of an open Dockhand = %v", out)
	}
	dock.Fail(loginCall, http.StatusForbidden, notAllowed)
	if out = suite.Call(t, "user_list", nil); acctest.Num(t, out["total"], "total") != 2 || !strings.Contains(acctest.Str(out["note"]), "whether the login is on could not be read") {
		t.Errorf("the users when the login cannot be read = %v", out)
	}
	dock.Fail(usersCall, http.StatusForbidden, notAllowed)
	if msg := suite.CallErr(t, "user_list", nil); !strings.Contains(msg, "the users could not be listed") || !strings.Contains(msg, notAllowed) {
		t.Errorf("users the account may not see = %q", msg)
	}
}

func TestUserCreate(t *testing.T) {
	t.Parallel()

	dock := withUsers(t)
	dock.AnswerMade(userMake, thirdMade)
	suite := session(t, dock, everything)
	third := map[string]any{"username": " third ", "password": "not-a-real-password", "email": " third@example.com ", "display_name": " Third "}

	for what, tc := range map[string]struct {
		args map[string]any
		want string
	}{
		"a name of spaces":      {map[string]any{"username": "  ", "password": "not-a-real-password"}, "with no spaces in it"},
		"a name with a space":   {map[string]any{"username": "the third", "password": "not-a-real-password"}, "with no spaces in it"},
		"a short password":      {map[string]any{"username": "third", "password": "short"}, "password is 5 characters: Dockhand takes none shorter than 8"},
		"an email that is none": {map[string]any{"username": "third", "password": "not-a-real-password", "email": "third"}, "it is an address"},
		"a name in use":         {map[string]any{"username": "SECOND", "password": "not-a-real-password"}, `a user is already named "SECOND"`},
	} {
		if msg := suite.CallErr(t, "user_create", tc.args); !strings.Contains(msg, tc.want) || strings.Contains(msg, "not-a-real-password") {
			t.Errorf("%s = %q, want %q", what, msg, tc.want)
		}
	}
	// a dry run says what would be made, and that it would be an administrator
	if out := suite.Call(t, "user_create", over(third, map[string]any{"dry_run": true})); !acctest.IsBool(out["dry_run"], true) || out["user"] != nil || !strings.Contains(flat(out), "would make the user third, an administrator") {
		t.Errorf("a dry run of making a user = %v", out)
	}
	if len(dock.Requests(userMake)) != 0 {
		t.Fatal("a user was made by a call that was refused or was a dry run")
	}

	// made: what was given, trimmed, and read back from the list, with what Dockhand made of it said
	dock.Then(userMake, func(dockhandtest.Request) { dock.Answer(usersCall, `[`+secondUser+`,`+opsUser+`,`+thirdUser+`]`) })
	out := suite.Call(t, "user_create", third)
	row := acctest.Object(t, out["user"], "user")
	if body := sent(t, dock.Last(userMake)); body["username"] != "third" || body["password"] != "not-a-real-password" || body["email"] != "third@example.com" || body["displayName"] != "Third" || len(body) != 4 {
		t.Errorf("making a user sent %v", body)
	}
	if acctest.Num(t, row["id"], "id") != 3 || row["username"] != "third" || !acctest.IsBool(row["admin"], true) || !strings.Contains(acctest.Str(out["note"]), "made it an administrator") || strings.Contains(acctest.Str(out["note"]), "switched off") || strings.Contains(flat(out), "not-a-real-password") || !strings.Contains(flat(out), "made the user third") {
		t.Errorf("a user made = %v", out)
	}
}

func TestUserCreateOnAnOpenDockhandAndWhenItGoesWrong(t *testing.T) {
	t.Parallel()

	dock := withUsers(t)
	dock.Answer(usersCall, `[`+opsUser+`]`)
	dock.Answer(loginCall, loginIsOut)
	dock.AnswerMade(userMake, thirdMade)
	suite := session(t, dock, everything)
	third := map[string]any{"username": "third", "password": "not-a-real-password"}

	// with the login off the user is made, and nobody is asked for it yet
	dock.Then(userMake, func(dockhandtest.Request) { dock.Answer(usersCall, `[`+opsUser+`,`+thirdUser+`]`) })
	out := suite.Call(t, "user_create", third)
	if body := sent(t, dock.Last(userMake)); len(body) != 2 || !strings.Contains(acctest.Str(out["note"]), "made it an administrator") || !strings.Contains(acctest.Str(out["note"]), "nobody is asked for this user until login_set switches it on") {
		t.Errorf("a user made on an open Dockhand = %v, sent %v", out, body)
	}

	// made and not listed afterwards, made and not read back, and not made at all
	dock.Answer(usersCall, `[`+opsUser+`]`)
	dock.Then(userMake, nil)
	if msg := suite.CallErr(t, "user_create", third); !strings.Contains(msg, "was answered as done, and it is not listed") {
		t.Errorf("a user made and not listed = %q", msg)
	}
	dock.Then(userMake, func(dockhandtest.Request) { dock.Fail(usersCall, http.StatusInternalServerError, "database is locked") })
	if out = suite.Call(t, "user_create", third); out["user"] != nil || !strings.Contains(acctest.Str(out["note"]), "it was made, and could not be read back") || !strings.Contains(flat(out), "made the user third") {
		t.Errorf("a user made and not read back = %v", out)
	}
	dock.Then(userMake, nil)
	dock.Answer(usersCall, `[`+opsUser+`]`)
	dock.Fail(userMake, http.StatusConflict, "Username already exists")
	if msg := suite.CallErr(t, "user_create", third); !strings.Contains(msg, "third was not made") || !strings.Contains(msg, "Username already exists") {
		t.Errorf("a user Dockhand refuses = %q", msg)
	}
	dock.Fail(usersCall, http.StatusForbidden, notAllowed)
	if msg := suite.CallErr(t, "user_create", third); !strings.Contains(msg, "the users could not be listed") {
		t.Errorf("making a user when they cannot be listed = %q", msg)
	}
}

func TestUserEdit(t *testing.T) {
	t.Parallel()

	dock := withUsers(t)
	dock.Answer(userChange, userChanged)
	suite := session(t, dock, everything)

	for what, tc := range map[string]struct {
		args map[string]any
		want string
	}{
		"nothing to change":       {map[string]any{"user": "second"}, "nothing to change was given"},
		"no user":                 {map[string]any{"display_name": "Other"}, "user is required"},
		"a user there is none of": {map[string]any{"user": "nobody", "display_name": "Other"}, `no user is named or numbered "nobody": the Dockhand has second, ops`},
		"a number that is none":   {map[string]any{"user": 1.5, "display_name": "Other"}, "its number is a whole one"},
		"a name that is neither":  {map[string]any{"user": true, "display_name": "Other"}, "it is a name or a number"},
		"a name with a space":     {map[string]any{"user": "second", "username": "the second"}, "with no spaces in it"},
		"a short password":        {map[string]any{"user": "second", "password": "short"}, "Dockhand takes none shorter than 8"},
		"an email that is none":   {map[string]any{"user": "second", "email": "second"}, "it is an address"},
		"another's name":          {map[string]any{"user": "second", "username": "OPS"}, `another user is named "OPS"`},
	} {
		if msg := suite.CallErr(t, "user_edit", tc.args); !strings.Contains(msg, tc.want) {
			t.Errorf("%s = %q, want %q", what, msg, tc.want)
		}
	}
	// what is so already is said to be, and nothing is sent; a dry run says what would be
	if out := suite.Call(t, "user_edit", map[string]any{"user": 2, "username": "second", "email": "second@example.com", "display_name": "Second", "active": true, "admin": true}); !strings.Contains(acctest.Str(out["note"]), "that way already") || len(acctest.Strs(t, out["changes"], "changes")) != 0 {
		t.Errorf("a change to what is so already = %v", out)
	}
	if out := suite.Call(t, "user_edit", map[string]any{"user": "second", "username": "renamed", "email": "", "display_name": "", "password": "not-a-real-password", "active": false, "admin": false, "dry_run": true}); !acctest.IsBool(out["dry_run"], true) || len(acctest.Strs(t, out["changes"], "changes")) != 6 || !strings.Contains(flat(out), "would rename it from second to renamed") || !strings.Contains(flat(out), "would forget its email") || !strings.Contains(flat(out), "would switch it off") || !strings.Contains(flat(out), "would take the administrator's mark from it") || strings.Contains(flat(out), "not-a-real-password") {
		t.Errorf("a dry run of changing a user = %v", out)
	}
	if len(dock.Requests(userChange)) != 0 {
		t.Fatal("a user was changed by a call that was refused, changed nothing or was a dry run")
	}

	// a name, an email and a display name set, each trimmed: only what changes is sent
	dock.Then(userChange, func(dockhandtest.Request) { dock.Answer(usersCall, `[`+secondBare+`,`+opsUser+`]`) })
	out := suite.Call(t, "user_edit", map[string]any{"user": "SECOND", "username": " renamed ", "email": " other@example.com ", "display_name": " Other "})
	if body := sent(t, dock.Last(userChange)); body["username"] != "renamed" || body["email"] != "other@example.com" || body["displayName"] != "Other" || len(body) != 3 || acctest.Object(t, out["user"], "user")["username"] != "renamed" || len(acctest.Strs(t, out["changes"], "changes")) != 3 || out["note"] != nil {
		t.Errorf("a user renamed = %v, sent %v", out, body)
	}
}

func TestUserEditPasswordForgettingAndMarks(t *testing.T) {
	t.Parallel()

	dock := withUsers(t)
	dock.Answer(userChange, userChanged)
	suite := session(t, dock, everything)

	// a password alone, and what follows from one is said; then an email and a display name forgotten, which is a call of its own
	out := suite.Call(t, "user_edit", map[string]any{"user": "second", "password": "not-a-real-password"})
	if body := sent(t, dock.Last(userChange)); body["password"] != "not-a-real-password" || len(body) != 1 || !strings.Contains(acctest.Str(out["note"]), "ends the user's signed-in sessions, and its API tokens go on working") || strings.Contains(flat(out), "not-a-real-password") {
		t.Errorf("a password changed = %v, sent %v", out, body)
	}
	before := len(dock.Requests(userChange))
	dock.Then(userChange, func(dockhandtest.Request) { dock.Answer(usersCall, `[`+secondBare+`,`+opsUser+`]`) })
	out = suite.Call(t, "user_edit", map[string]any{"user": "second", "email": " ", "display_name": ""})
	if body := sent(t, dock.Last(userChange)); len(dock.Requests(userChange)) != before+1 || body["email"] != "" || body["displayName"] != "" || len(body) != 2 || acctest.Object(t, out["user"], "user")["email"] != nil || !strings.Contains(flat(out), "forgot its email") || !strings.Contains(flat(out), "forgot its display name") {
		t.Errorf("an email and a display name forgotten = %v, sent %v", out, body)
	}
	// forgetting what is not kept is nothing to do
	if out = suite.Call(t, "user_edit", map[string]any{"user": "renamed", "email": "", "display_name": ""}); !strings.Contains(acctest.Str(out["note"]), "that way already") {
		t.Errorf("forgetting what is not kept = %v", out)
	}

	// switched off: sent as it is, read back, and what it means is said
	dock.Answer(usersCall, `[`+secondUser+`,`+opsUser+`]`)
	dock.Then(userChange, func(dockhandtest.Request) { dock.Answer(usersCall, `[`+secondOff+`,`+opsUser+`]`) })
	out = suite.Call(t, "user_edit", map[string]any{"user": "second", "active": false})
	if body := sent(t, dock.Last(userChange)); !acctest.IsBool(body["isActive"], false) || len(body) != 1 || !acctest.IsBool(acctest.Object(t, out["user"], "user")["active"], false) || !strings.Contains(acctest.Str(out["note"]), "its API tokens are refused until it is switched on again") {
		t.Errorf("a user switched off = %v, sent %v", out, body)
	}
	// with the other switched off, the one left is the only administrator: neither mark is taken from it, and nothing is sent
	before = len(dock.Requests(userChange)) + len(dock.Requests("PUT /api/users/1"))
	for _, mark := range []string{"active", "admin"} {
		if msg := suite.CallErr(t, "user_edit", map[string]any{"user": "ops", mark: false}); !strings.Contains(msg, "ops is the only user that is both switched on and an administrator") || !strings.Contains(msg, "login_set") {
			t.Errorf("%s taken from the only administrator = %q", mark, msg)
		}
	}
	if len(dock.Requests(userChange))+len(dock.Requests("PUT /api/users/1")) != before {
		t.Error("the only administrator was changed by a call that was refused")
	}
	// switched on again, and the mark taken away, which holds nobody to less and is said so
	dock.Then(userChange, func(dockhandtest.Request) { dock.Answer(usersCall, `[`+secondLess+`,`+opsUser+`]`) })
	out = suite.Call(t, "user_edit", map[string]any{"user": "second", "active": true, "admin": false})
	if body := sent(t, dock.Last(userChange)); !acctest.IsBool(body["isActive"], true) || !acctest.IsBool(body["isAdmin"], false) || len(body) != 2 || !acctest.IsBool(acctest.Object(t, out["user"], "user")["admin"], false) || !strings.Contains(acctest.Str(out["note"]), "no way to hold a user to less") {
		t.Errorf("a user switched on and unmarked = %v, sent %v", out, body)
	}

	// with the login off Dockhand makes neither change, so neither is asked of it; the rest still is
	dock.Then(userChange, nil)
	dock.Answer(usersCall, `[`+secondUser+`,`+opsUser+`]`)
	dock.Answer(loginCall, loginIsOut)
	before = len(dock.Requests(userChange))
	if msg := suite.CallErr(t, "user_edit", map[string]any{"user": "second", "admin": false}); !strings.Contains(msg, "the login is switched off") || !strings.Contains(msg, "does not make it") || len(dock.Requests(userChange)) != before {
		t.Errorf("a mark changed with the login off = %q", msg)
	}
	dock.Fail(loginCall, http.StatusForbidden, notAllowed)
	if msg := suite.CallErr(t, "user_edit", map[string]any{"user": "second", "active": false}); !strings.Contains(msg, "whether the login is on could not be read") {
		t.Errorf("a mark changed when the login cannot be read = %q", msg)
	}
}

func TestUserEditWhenItGoesWrong(t *testing.T) {
	t.Parallel()

	dock := withUsers(t)
	dock.Answer(userChange, userChanged)
	suite := session(t, dock, everything)
	off := map[string]any{"user": "second", "active": false}

	// answered as done and read back as it was: Dockhand did not make it
	if msg := suite.CallErr(t, "user_edit", off); !strings.Contains(msg, "answered the change to second as done and did not make it") {
		t.Errorf("a change answered and not made = %q", msg)
	}
	// the user this server signs in as, switched off: the read back is refused, which is said and is no error
	dock.Then(userChange, func(dockhandtest.Request) { dock.Fail(usersCall, http.StatusUnauthorized, "Authentication required") })
	if out := suite.Call(t, "user_edit", off); out["user"] != nil || !strings.Contains(acctest.Str(out["note"]), "now turns this server away") || !strings.Contains(flat(out), "switched it off") {
		t.Errorf("this server's own user switched off = %v", out)
	}
	dock.Answer(usersCall, `[`+secondUser+`,`+opsUser+`]`)
	dock.Then(userChange, func(dockhandtest.Request) { dock.Fail(usersCall, http.StatusInternalServerError, "database is locked") })
	if out := suite.Call(t, "user_edit", off); !strings.Contains(acctest.Str(out["note"]), "it was changed, and could not be read back") {
		t.Errorf("a change not read back = %v", out)
	}
	dock.Answer(usersCall, `[`+secondUser+`,`+opsUser+`]`)
	dock.Then(userChange, func(dockhandtest.Request) { dock.Answer(usersCall, `[`+opsUser+`]`) })
	if msg := suite.CallErr(t, "user_edit", off); !strings.Contains(msg, "second was changed, and is no longer listed") {
		t.Errorf("a user gone once changed = %q", msg)
	}

	// refused by Dockhand: nothing is said to have been done
	dock.Answer(usersCall, `[`+secondUser+`,`+opsUser+`]`)
	dock.Then(userChange, nil)
	dock.Fail(userChange, http.StatusConflict, "Username already exists")
	if msg := suite.CallErr(t, "user_edit", map[string]any{"user": "second", "username": "other"}); !strings.Contains(msg, "second was not changed") || !strings.Contains(msg, "Username already exists") {
		t.Errorf("a change Dockhand refuses = %q", msg)
	}
	if msg := suite.CallErr(t, "user_edit", map[string]any{"user": "second", "email": ""}); !strings.Contains(msg, "what was to be forgotten was not") {
		t.Errorf("a forgetting Dockhand refuses = %q", msg)
	}
	dock.Fail(usersCall, http.StatusForbidden, notAllowed)
	if msg := suite.CallErr(t, "user_edit", off); !strings.Contains(msg, "the users could not be listed") {
		t.Errorf("a change when the users cannot be listed = %q", msg)
	}
}

func TestUserDelete(t *testing.T) {
	t.Parallel()

	dock := withUsers(t)
	dock.Answer(userRemove, `{"success":true}`)
	suite := session(t, dock, everything)

	// without confirm it says what would go, and that this server may be the one shut out
	out := suite.Call(t, "user_delete", map[string]any{"user": "second"})
	if !acctest.IsBool(out["removed"], false) || out["user"] != "second" || !strings.Contains(flat(out), "the user second, its password and every API token it made") || !strings.Contains(acctest.Str(out["note"]), "nothing was removed") || !strings.Contains(acctest.Str(out["note"]), "this server is turned away once it is removed") || len(dock.Requests(userRemove)) != 0 {
		t.Fatalf("a removal not confirmed = %v", out)
	}
	if msg := suite.CallErr(t, "user_delete", map[string]any{"user": "nobody", "confirm": true}); !strings.Contains(msg, `no user is named or numbered "nobody"`) {
		t.Errorf("removing a user there is none of = %q", msg)
	}

	// answered without success, and answered as done with the user still listed
	dock.Answer(userRemove, `{"success":false}`)
	if msg := suite.CallErr(t, "user_delete", map[string]any{"user": "second", "confirm": true}); !strings.Contains(msg, "without its being said to have succeeded") {
		t.Errorf("a removal not said to succeed = %q", msg)
	}
	dock.Answer(userRemove, `{"success":true}`)
	if msg := suite.CallErr(t, "user_delete", map[string]any{"user": "second", "confirm": true}); !strings.Contains(msg, "answered as done, and it is still listed") {
		t.Errorf("a removal that left the user = %q", msg)
	}
	// removed, and read back as gone
	dock.Then(userRemove, func(dockhandtest.Request) { dock.Answer(usersCall, `[`+opsUser+`]`) })
	if out = suite.Call(t, "user_delete", map[string]any{"user": 2, "confirm": true}); !acctest.IsBool(out["removed"], true) || out["note"] != nil || len(dock.Last(userRemove).Query) != 0 {
		t.Errorf("a user removed = %v, asked %v", out, dock.Last(userRemove).Query)
	}

	// the only administrator is not removed while the login is on, confirmed or not, and is once it is off
	for _, confirm := range []bool{false, true} {
		if msg := suite.CallErr(t, "user_delete", map[string]any{"user": "ops", "confirm": confirm}); !strings.Contains(msg, "ops is the only user that is both switched on and an administrator") || len(dock.Requests("DELETE /api/users/1")) != 0 {
			t.Errorf("the only administrator removed, confirm %t = %q", confirm, msg)
		}
	}
	dock.Answer(loginCall, loginIsOut)
	dock.Answer("DELETE /api/users/1", `{"success":true}`)
	dock.Then("DELETE /api/users/1", func(dockhandtest.Request) { dock.Answer(usersCall, `[]`) })
	if out = suite.Call(t, "user_delete", map[string]any{"user": "ops"}); strings.Contains(acctest.Str(out["note"]), "this server is turned away") {
		t.Errorf("a removal not confirmed on an open Dockhand = %v", out)
	}
	if out = suite.Call(t, "user_delete", map[string]any{"user": "ops", "confirm": true}); !acctest.IsBool(out["removed"], true) {
		t.Errorf("the last user removed from an open Dockhand = %v", out)
	}
}

func TestUserDeleteWhenItGoesWrong(t *testing.T) {
	t.Parallel()

	dock := withUsers(t)
	dock.Answer(userRemove, `{"success":true}`)
	suite := session(t, dock, everything)
	gone := map[string]any{"user": "second", "confirm": true}

	// the user this server signs in as: removed, and the read back refused
	dock.Then(userRemove, func(dockhandtest.Request) { dock.Fail(usersCall, http.StatusUnauthorized, "Authentication required") })
	if out := suite.Call(t, "user_delete", gone); !acctest.IsBool(out["removed"], true) || !strings.Contains(acctest.Str(out["note"]), "now turns this server away") {
		t.Errorf("this server's own user removed = %v", out)
	}
	dock.Answer(usersCall, `[`+secondUser+`,`+opsUser+`]`)
	dock.Then(userRemove, func(dockhandtest.Request) { dock.Fail(usersCall, http.StatusInternalServerError, "database is locked") })
	if out := suite.Call(t, "user_delete", gone); !acctest.IsBool(out["removed"], false) || !strings.Contains(acctest.Str(out["note"]), "it was removed, and the users could not be listed afterwards") {
		t.Errorf("a removal not read back = %v", out)
	}
	dock.Answer(usersCall, `[`+secondUser+`,`+opsUser+`]`)
	dock.Then(userRemove, nil)
	dock.Fail(userRemove, http.StatusNotFound, "User not found")
	if msg := suite.CallErr(t, "user_delete", gone); !strings.Contains(msg, "second was not removed") || !strings.Contains(msg, "User not found") {
		t.Errorf("a removal Dockhand refuses = %q", msg)
	}
	dock.Fail(loginCall, http.StatusForbidden, notAllowed)
	if msg := suite.CallErr(t, "user_delete", gone); !strings.Contains(msg, "whether the login is on could not be read") {
		t.Errorf("a removal when the login cannot be read = %q", msg)
	}
	dock.Fail(usersCall, http.StatusForbidden, notAllowed)
	if msg := suite.CallErr(t, "user_delete", gone); !strings.Contains(msg, "the users could not be listed") {
		t.Errorf("a removal when the users cannot be listed = %q", msg)
	}
}

func TestLoginGet(t *testing.T) {
	t.Parallel()

	dock := withUsers(t)
	dock.Answer(offeredCall, offered)
	dock.Answer(licenceCall, noLicence)
	suite := session(t, dock, Options{})

	out := suite.Call(t, "login_get", nil)
	providers := acctest.Rows(t, out["providers"], "providers")
	if !acctest.IsBool(out["enabled"], true) || acctest.Num(t, out["session_timeout"], "session_timeout") != 86400 || out["default_provider"] != "local" || !acctest.IsBool(out["licensed"], false) || out["note"] != nil || len(providers) != 2 || providers[1]["id"] != "oidc:4" || providers[1]["name"] != "Company" || providers[1]["type"] != "oidc" {
		t.Fatalf("how a Dockhand is signed in to = %v", out)
	}

	// open, licensed, and with what cannot be read said rather than failed on
	dock.Answer(loginCall, loginIsOut)
	dock.Answer(licenceCall, `{"valid":true,"active":true,"hostname":"dockhand"}`)
	if out = suite.Call(t, "login_get", nil); !acctest.IsBool(out["enabled"], false) || !acctest.IsBool(out["licensed"], true) || !strings.Contains(acctest.Str(out["note"]), "asks nobody who they are") {
		t.Errorf("an open, licensed Dockhand = %v", out)
	}
	dock.Fail(offeredCall, http.StatusInternalServerError, "database is locked")
	dock.Fail(licenceCall, http.StatusInternalServerError, "database is locked")
	if out = suite.Call(t, "login_get", nil); len(acctest.Rows(t, out["providers"], "providers")) != 0 || !strings.Contains(acctest.Str(out["note"]), "what is offered at sign-in could not be read") || !strings.Contains(acctest.Str(out["note"]), "whether there is a licence could not be read") {
		t.Errorf("a login whose providers and licence cannot be read = %v", out)
	}
	dock.Fail(loginCall, http.StatusForbidden, notAllowed)
	if msg := suite.CallErr(t, "login_get", nil); !strings.Contains(msg, "how the Dockhand is signed in to could not be read") {
		t.Errorf("a login the account may not see = %q", msg)
	}
}

func TestLoginSet(t *testing.T) {
	t.Parallel()

	dock := withUsers(t)
	dock.Answer(offeredCall, offered)
	dock.Answer(loginSave, loginIsOn)
	suite := session(t, dock, everything)

	for what, tc := range map[string]struct {
		args map[string]any
		want string
	}{
		"nothing to set":               {nil, "nothing to set was given"},
		"a session under an hour":      {map[string]any{"session_timeout": 10}, "session_timeout is 10: it is from 3600 seconds"},
		"a session over a week":        {map[string]any{"session_timeout": 700000}, "to 604800, a week"},
		"a provider that is not there": {map[string]any{"default_provider": "ldap"}, `default_provider is "ldap", which is not offered at sign-in: there is local, oidc:4`},
	} {
		if msg := suite.CallErr(t, "login_set", tc.args); !strings.Contains(msg, tc.want) {
			t.Errorf("%s = %q, want %q", what, msg, tc.want)
		}
	}
	// what is so already, and the login asked on when it is on
	if out := suite.Call(t, "login_set", map[string]any{"enabled": true, "session_timeout": 86400, "default_provider": "LOCAL"}); !strings.Contains(acctest.Str(out["note"]), "that way already") || len(acctest.Strs(t, out["changes"], "changes")) != 0 {
		t.Errorf("a login set to what it is = %v", out)
	}
	if len(dock.Requests(loginSave)) != 0 {
		t.Fatal("the login was changed by a call that was refused or changed nothing")
	}

	// how long a session lasts and the default provider, by its name: sent together, and read back
	changed := `{"id":1,"authEnabled":true,"defaultProvider":"oidc:4","sessionTimeout":7200}`
	dock.Answer(loginSave, changed)
	dock.Then(loginSave, func(dockhandtest.Request) { dock.Answer(loginCall, changed) })
	out := suite.Call(t, "login_set", map[string]any{"session_timeout": 7200, "default_provider": "company"})
	if body := sent(t, dock.Last(loginSave)); body["sessionTimeout"] != float64(7200) || body["defaultProvider"] != "oidc:4" || len(body) != 2 || acctest.Num(t, out["session_timeout"], "session_timeout") != 7200 || out["default_provider"] != "oidc:4" || len(acctest.Strs(t, out["changes"], "changes")) != 2 || out["turned_away"] != nil {
		t.Errorf("a session and a default provider set = %v, sent %v", out, body)
	}
	// a session that never ends is a time of 0, sent as it is
	before := len(dock.Requests(loginSave))
	out = suite.Call(t, "login_set", map[string]any{"session_timeout": 0})
	if body := sent(t, dock.Last(loginSave)); len(dock.Requests(loginSave)) != before+1 || body["sessionTimeout"] != float64(0) || len(body) != 1 || acctest.Num(t, out["session_timeout"], "session_timeout") != 0 || !strings.Contains(flat(out), "never to end") {
		t.Errorf("a session that never ends = %v, sent %v", out, body)
	}
}

func TestLoginSwitchedOffAndOn(t *testing.T) {
	t.Parallel()

	dock := withUsers(t)
	dock.Answer(loginSave, loginIsOut)
	suite := session(t, dock, everything)

	// off: nothing at all without confirm, the session's time included, and what it means is said
	out := suite.Call(t, "login_set", map[string]any{"enabled": false, "session_timeout": 7200})
	if would := acctest.Strs(t, out["would"], "would"); len(would) != 2 || !strings.Contains(would[0], "asks nobody who they are") || !strings.Contains(would[1], "work again once the login is switched back on") || !strings.Contains(acctest.Str(out["note"]), "call again with confirm to switch the login off") || !acctest.IsBool(out["enabled"], true) || len(dock.Requests(loginSave)) != 0 {
		t.Fatalf("the login asked off without confirm = %v", out)
	}
	dock.Then(loginSave, func(dockhandtest.Request) { dock.Answer(loginCall, loginIsOut) })
	out = suite.Call(t, "login_set", map[string]any{"enabled": false, "confirm": true})
	if body := sent(t, dock.Last(loginSave)); !acctest.IsBool(body["authEnabled"], false) || len(body) != 1 || !acctest.IsBool(out["enabled"], false) || out["would"] != nil || !strings.Contains(flat(out), "switched the login off") || !strings.Contains(acctest.Str(out["note"]), "asks nobody who they are") {
		t.Errorf("the login switched off = %v, sent %v", out, body)
	}

	// on: not with nobody who could sign in, which Dockhand itself would allow
	dock.Then(loginSave, nil)
	sends := len(dock.Requests(loginSave))
	for what, users := range map[string]string{"no user at all": `[]`, "a user that is switched off": `[` + secondOff + `]`, "a user that is no administrator": `[` + secondLess + `]`} {
		dock.Answer(usersCall, users)
		if msg := suite.CallErr(t, "login_set", map[string]any{"enabled": true, "confirm": true}); !strings.Contains(msg, "no user is both switched on and an administrator") || !strings.Contains(msg, "user_create makes one") {
			t.Errorf("the login asked on with %s = %q", what, msg)
		}
	}
	// nor without confirm, which names who could sign in and what it means for this server
	dock.Answer(usersCall, `[`+secondOff+`,`+opsUser+`]`)
	out = suite.Call(t, "login_set", map[string]any{"enabled": true})
	if would := acctest.Strs(t, out["would"], "would"); len(would) != 2 || !strings.Contains(would[0], "ops could sign in") || strings.Contains(would[0], "second") || !strings.Contains(would[1], "never with a tool") || !acctest.IsBool(out["enabled"], false) || len(dock.Requests(loginSave)) != sends {
		t.Fatalf("the login asked on without confirm = %v", out)
	}

	// on, and this server still let in
	dock.Answer(loginSave, loginIsOn)
	dock.Then(loginSave, func(dockhandtest.Request) { dock.Answer(loginCall, loginIsOn) })
	out = suite.Call(t, "login_set", map[string]any{"enabled": true, "confirm": true})
	if body := sent(t, dock.Last(loginSave)); !acctest.IsBool(body["authEnabled"], true) || len(body) != 1 || !acctest.IsBool(out["enabled"], true) || out["turned_away"] != nil || out["note"] != "the login is on, and this server is still let in" {
		t.Errorf("the login switched on = %v, sent %v", out, body)
	}
	// on, and this server turned away: it is said, and is no error
	dock.Answer(loginCall, loginIsOut)
	dock.Then(loginSave, func(dockhandtest.Request) { dock.Fail(loginCall, http.StatusUnauthorized, "Authentication required") })
	if out = suite.Call(t, "login_set", map[string]any{"enabled": true, "confirm": true}); !acctest.IsBool(out["enabled"], true) || !acctest.IsBool(out["turned_away"], true) || !strings.Contains(acctest.Str(out["note"]), "never with a tool") {
		t.Errorf("the login switched on over this server = %v", out)
	}
}

func TestLoginSetWhenItGoesWrong(t *testing.T) {
	t.Parallel()

	dock := withUsers(t)
	dock.Answer(offeredCall, offered)
	dock.Answer(loginSave, loginIsOn)
	suite := session(t, dock, everything)
	off := map[string]any{"enabled": false, "confirm": true}

	// answered as done and read back as it was; changed and not read back; and refused
	if msg := suite.CallErr(t, "login_set", off); !strings.Contains(msg, "the change was answered as done, and the login is read back as on") {
		t.Errorf("a switch answered and not made = %q", msg)
	}
	dock.Then(loginSave, func(dockhandtest.Request) { dock.Fail(loginCall, http.StatusInternalServerError, "database is locked") })
	if out := suite.Call(t, "login_set", off); !strings.Contains(acctest.Str(out["note"]), "it was changed, and could not be read back") {
		t.Errorf("a switch not read back = %v", out)
	}
	dock.Then(loginSave, nil)
	dock.Answer(loginCall, loginIsOn)
	dock.Fail(loginSave, http.StatusForbidden, notAllowed)
	if msg := suite.CallErr(t, "login_set", off); !strings.Contains(msg, "how the Dockhand is signed in to was not changed") {
		t.Errorf("a switch Dockhand refuses = %q", msg)
	}
	if msg := suite.CallErr(t, "login_set", map[string]any{"session_timeout": 0}); !strings.Contains(msg, "how long a session lasts was not changed") {
		t.Errorf("a session Dockhand refuses to make endless = %q", msg)
	}

	// what has to be read first and cannot be
	dock.Fail(offeredCall, http.StatusInternalServerError, "database is locked")
	if msg := suite.CallErr(t, "login_set", map[string]any{"default_provider": "local"}); !strings.Contains(msg, "what is offered at sign-in could not be read") {
		t.Errorf("a provider set when they cannot be read = %q", msg)
	}
	dock.Answer(loginCall, loginIsOut)
	dock.Fail(usersCall, http.StatusForbidden, notAllowed)
	if msg := suite.CallErr(t, "login_set", map[string]any{"enabled": true, "confirm": true}); !strings.Contains(msg, "the users could not be listed") {
		t.Errorf("the login asked on when the users cannot be listed = %q", msg)
	}
	dock.Fail(loginCall, http.StatusForbidden, notAllowed)
	if msg := suite.CallErr(t, "login_set", off); !strings.Contains(msg, "how the Dockhand is signed in to could not be read") {
		t.Errorf("the login set when it cannot be read = %q", msg)
	}
}

func TestTokenList(t *testing.T) {
	t.Parallel()

	dock := withUsers(t)
	dock.Answer(tokensCall, twoTokens)
	suite := session(t, dock, Options{})

	out := suite.Call(t, "token_list", nil)
	rows := acctest.Rows(t, out["tokens"], "tokens")
	if acctest.Num(t, out["total"], "total") != 2 || out["note"] != nil || rows[0]["name"] != "later" || rows[0]["prefix"] != "bbbbbbbb" || rows[0]["last_used"] != "2026-10-10T12:00:00Z" || rows[0]["expires"] != "2027-01-01T00:00:00Z" || rows[1]["name"] != "first" || rows[1]["last_used"] != nil || rows[1]["expires"] != nil || rows[1]["created"] != "2026-10-10T10:00:00Z" {
		t.Fatalf("the tokens, the newest first = %v", out)
	}

	// with the login off Dockhand lists none, which is an answer; anything else it refuses is an error
	dock.Fail(tokensCall, http.StatusBadRequest, "Authentication is not enabled")
	if out = suite.Call(t, "token_list", nil); acctest.Num(t, out["total"], "total") != 0 || len(acctest.Rows(t, out["tokens"], "tokens")) != 0 || !strings.Contains(acctest.Str(out["note"]), "the login is switched off") {
		t.Errorf("the tokens of an open Dockhand = %v", out)
	}
	dock.Fail(tokensCall, http.StatusInternalServerError, "database is locked")
	if msg := suite.CallErr(t, "token_list", nil); !strings.Contains(msg, "the tokens could not be listed") {
		t.Errorf("tokens that cannot be listed = %q", msg)
	}
}
