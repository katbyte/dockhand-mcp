package tools

import (
	"net/http"
	"strings"
	"testing"

	"github.com/katbyte/dockhand-mcp/sdk/dockhandtest"
	"github.com/katbyte/go-kt/mcp/acctest"
)

// What a 1.0.51 server answers about its single sign-on providers, with invented names: one switched on that names a claim for an administrator, and
// one switched off that names none. The secret is always the mask.
//
//nolint:gosec // G101: the secret is made up, and there to be seen never to come back
const (
	companySSO = `{"id":4,"name":"Company","enabled":true,"issuerUrl":"https://sso.example","clientId":"dockhand","clientSecret":"********","redirectUri":"https://dockhand.example/api/auth/oidc/callback","scopes":"openid profile email","usernameClaim":"preferred_username","emailClaim":"email","displayNameClaim":"name","adminClaim":"groups","adminValue":"admins","roleMappingsClaim":"groups","createdAt":"2026-10-10 10:00:00","updatedAt":"2026-10-10 10:00:00"}`
	spareSSO   = `{"id":5,"name":"spare","enabled":false,"issuerUrl":"https://spare.example","clientId":"dockhand","clientSecret":"********","redirectUri":"https://dockhand.example/api/auth/oidc/callback","scopes":"openid profile email","usernameClaim":"preferred_username","emailClaim":"email","displayNameClaim":"name","adminClaim":null,"adminValue":null,"roleMappingsClaim":"groups","createdAt":"2026-10-10 11:00:00","updatedAt":"2026-10-10 11:00:00"}`
	newSSO     = `{"id":6,"name":"other","enabled":false,"issuerUrl":"https://other.example","clientId":"dockhand","clientSecret":"********","redirectUri":"https://dockhand.example/api/auth/oidc/callback","scopes":"openid profile email","usernameClaim":"preferred_username","emailClaim":"email","displayNameClaim":"name","adminClaim":null,"adminValue":null,"roleMappingsClaim":"groups","createdAt":"2026-10-10 12:00:00","updatedAt":"2026-10-10 12:00:00"}`
	changedSSO = `{"id":4,"name":"Renamed","enabled":false,"issuerUrl":"https://sso.example/realms/main","clientId":"dockhand","clientSecret":"********","redirectUri":"https://dockhand.example/api/auth/oidc/callback","scopes":"openid email","usernameClaim":"preferred_username","emailClaim":"email","displayNameClaim":"name","adminClaim":null,"adminValue":null,"roleMappingsClaim":"groups","createdAt":"2026-10-10 10:00:00","updatedAt":"2026-10-10T13:00:00.000Z"}`
	backTo     = "https://dockhand.example/api/auth/oidc/callback"
	// aSecret is what a provider is given to keep, and no real one
	aSecret = "not-a-real-secret"

	ssoCall   = "GET /api/auth/oidc"
	ssoMake   = "POST /api/auth/oidc"
	ssoMade   = "GET /api/auth/oidc/6"
	ssoChange = "PUT /api/auth/oidc/4"
	ssoRemove = "DELETE /api/auth/oidc/4"
)

// withSSO is a Dockhand that keeps two single sign-on providers.
func withSSO(t *testing.T) *dockhandtest.Server {
	t.Helper()

	dock := dockhandtest.New(t)
	dock.Answer(ssoCall, `[`+spareSSO+`,`+companySSO+`]`)
	dock.Answer(loginCall, loginIsOn)

	return dock
}

func TestSSOList(t *testing.T) {
	t.Parallel()

	dock := withSSO(t)
	suite := session(t, dock, Options{})

	out := suite.Call(t, "sso_list", nil)
	rows := acctest.Rows(t, out["providers"], "providers")
	if acctest.Num(t, out["total"], "total") != 2 || out["note"] != nil || rows[0]["name"] != "Company" || !acctest.IsBool(rows[0]["enabled"], true) || rows[0]["login_id"] != "oidc:4" || rows[0]["issuer_url"] != "https://sso.example" || rows[0]["client_id"] != "dockhand" || rows[0]["redirect_uri"] != backTo || rows[0]["scopes"] != "openid profile email" || rows[0]["admin_claim"] != "groups" || rows[0]["admin_value"] != "admins" || rows[0]["role_mappings_claim"] != "groups" {
		t.Fatalf("the single sign-on providers, by name = %v", out)
	}
	// one that names no claim for an administrator, and nowhere a secret or its mask
	if spare := rows[1]; spare["name"] != "spare" || !acctest.IsBool(spare["enabled"], false) || spare["admin_claim"] != nil || spare["admin_value"] != nil || spare["username_claim"] != "preferred_username" || strings.Contains(flat(out), "****") || strings.Contains(flat(out), "secret") {
		t.Errorf("a provider switched off = %v", spare)
	}

	dock.Answer(ssoCall, `[]`)
	if out = suite.Call(t, "sso_list", nil); acctest.Num(t, out["total"], "total") != 0 || !strings.Contains(acctest.Str(out["note"]), "keeps no single sign-on provider") {
		t.Errorf("a Dockhand with no provider = %v", out)
	}
	dock.Fail(ssoCall, http.StatusForbidden, notAllowed)
	if msg := suite.CallErr(t, "sso_list", nil); !strings.Contains(msg, "the single sign-on providers could not be listed") {
		t.Errorf("providers the account may not see = %q", msg)
	}
}

func TestSSOCreate(t *testing.T) {
	t.Parallel()

	dock := withSSO(t)
	dock.AnswerMade(ssoMake, newSSO)
	dock.Answer(ssoMade, newSSO)
	suite := session(t, dock, everything)
	other := map[string]any{"name": " other ", "issuer_url": " https://other.example ", "client_id": " dockhand ", "client_secret": aSecret, "redirect_uri": backTo}

	for what, tc := range map[string]struct {
		more map[string]any
		want string
	}{
		"a name of spaces":             {map[string]any{"name": " "}, "are all required"},
		"a client of spaces":           {map[string]any{"client_id": " "}, "are all required"},
		"no secret":                    {map[string]any{"client_secret": ""}, "are all required"},
		"an issuer with no scheme":     {map[string]any{"issuer_url": "other.example"}, `issuer_url is "other.example": it is an address with its scheme`},
		"a way back with no scheme":    {map[string]any{"redirect_uri": "/api/auth/oidc/callback"}, "redirect_uri is"},
		"a claim with no value":        {map[string]any{"admin_claim": "groups"}, "admin_claim and admin_value go together"},
		"a value with no claim":        {map[string]any{"admin_value": "admins"}, "admin_claim and admin_value go together"},
		"a name in use, whatever case": {map[string]any{"name": "COMPANY"}, `a single sign-on provider is already named "COMPANY"`},
	} {
		if msg := suite.CallErr(t, "sso_create", over(other, tc.more)); !strings.Contains(msg, tc.want) || strings.Contains(msg, aSecret) {
			t.Errorf("%s = %q, want %q", what, msg, tc.want)
		}
	}
	if out := suite.Call(t, "sso_create", over(other, map[string]any{"dry_run": true})); !acctest.IsBool(out["dry_run"], true) || out["provider"] != nil || !strings.Contains(flat(out), "would save the single sign-on provider other at https://other.example, switched off") {
		t.Errorf("a dry run of saving a provider = %v", out)
	}
	if len(dock.Requests(ssoMake)) != 0 {
		t.Fatal("a provider was saved by a call that was refused or was a dry run")
	}

	// saved: what was given, trimmed, switched off because it was not said to be on, and read back without its secret
	out := suite.Call(t, "sso_create", other)
	row := acctest.Object(t, out["provider"], "provider")
	if body := sent(t, dock.Last(ssoMake)); body["name"] != "other" || body["issuerUrl"] != "https://other.example" || body["clientId"] != "dockhand" || body["clientSecret"] != aSecret || body["redirectUri"] != backTo || !acctest.IsBool(body["enabled"], false) || len(body) != 6 {
		t.Errorf("saving a provider sent %v", body)
	}
	if acctest.Num(t, row["id"], "id") != 6 || row["login_id"] != "oidc:6" || !acctest.IsBool(row["enabled"], false) || out["note"] != nil || strings.Contains(flat(out), aSecret) || strings.Contains(flat(out), "****") {
		t.Errorf("a provider saved = %v", out)
	}
	// switched on from the start, with every claim: each is sent, and that nobody has tried it is said
	out = suite.Call(t, "sso_create", over(other, map[string]any{"enabled": true, "scopes": "openid email", "username_claim": "sub", "email_claim": "mail", "display_name_claim": "cn", "admin_claim": "groups", "admin_value": "admins", "role_mappings_claim": "roles"}))
	if body := sent(t, dock.Last(ssoMake)); !acctest.IsBool(body["enabled"], true) || body["scopes"] != "openid email" || body["usernameClaim"] != "sub" || body["emailClaim"] != "mail" || body["displayNameClaim"] != "cn" || body["adminClaim"] != "groups" || body["adminValue"] != "admins" || body["roleMappingsClaim"] != "roles" || len(body) != 13 || !strings.Contains(acctest.Str(out["note"]), "offered to whoever signs in") || !strings.Contains(acctest.Str(out["note"]), "not by a tool") {
		t.Errorf("a provider saved switched on = %v, sent %v", out, body)
	}

	// saved and not read back, and not saved at all
	dock.Fail(ssoMade, http.StatusInternalServerError, "database is locked")
	if out = suite.Call(t, "sso_create", other); out["provider"] != nil || !strings.Contains(acctest.Str(out["note"]), "it was saved, and could not be read back") {
		t.Errorf("a provider saved and not read back = %v", out)
	}
	dock.Fail(ssoMake, http.StatusBadRequest, "Missing required field: issuerUrl")
	if msg := suite.CallErr(t, "sso_create", other); !strings.Contains(msg, "other was not saved") || !strings.Contains(msg, "Missing required field") {
		t.Errorf("a provider Dockhand refuses = %q", msg)
	}
	dock.Fail(ssoCall, http.StatusForbidden, notAllowed)
	if msg := suite.CallErr(t, "sso_create", other); !strings.Contains(msg, "the single sign-on providers could not be listed") {
		t.Errorf("saving a provider when they cannot be listed = %q", msg)
	}
}

func TestSSOEdit(t *testing.T) {
	t.Parallel()

	dock := withSSO(t)
	dock.Answer(ssoChange, changedSSO)
	suite := session(t, dock, everything)

	for what, tc := range map[string]struct {
		args map[string]any
		want string
	}{
		"nothing to change":            {map[string]any{"provider": "company"}, "nothing to change was given"},
		"no provider":                  {map[string]any{"scopes": "openid"}, "provider is required"},
		"a provider there is none of":  {map[string]any{"provider": "nowhere", "scopes": "openid"}, `no single sign-on provider is named or numbered "nowhere": the Dockhand keeps spare, Company`},
		"a number that is none":        {map[string]any{"provider": 4.5, "scopes": "openid"}, "its number is a whole one"},
		"an issuer with no scheme":     {map[string]any{"provider": "company", "issuer_url": "sso.example"}, "it is an address with its scheme"},
		"a claim forgotten and valued": {map[string]any{"provider": "company", "admin_claim": "", "admin_value": "admins"}, "it is not given with a value to keep"},
		"another's name":               {map[string]any{"provider": "company", "name": "SPARE"}, `another single sign-on provider is named "SPARE"`},
	} {
		if msg := suite.CallErr(t, "sso_edit", tc.args); !strings.Contains(msg, tc.want) {
			t.Errorf("%s = %q, want %q", what, msg, tc.want)
		}
	}
	// what is so already, forgetting what is not kept, and a dry run: nothing is sent
	if out := suite.Call(t, "sso_edit", map[string]any{"provider": 4, "name": "Company", "issuer_url": "https://sso.example", "enabled": true, "admin_claim": "groups", "admin_value": "admins"}); !strings.Contains(acctest.Str(out["note"]), "that way already") {
		t.Errorf("a change to what is so already = %v", out)
	}
	if out := suite.Call(t, "sso_edit", map[string]any{"provider": "spare", "admin_claim": ""}); !strings.Contains(acctest.Str(out["note"]), "that way already") {
		t.Errorf("forgetting a claim that is not kept = %v", out)
	}
	if out := suite.Call(t, "sso_edit", map[string]any{"provider": "company", "name": "Renamed", "enabled": false, "admin_claim": "", "client_secret": aSecret, "dry_run": true}); !acctest.IsBool(out["dry_run"], true) || len(acctest.Strs(t, out["changes"], "changes")) != 4 || !strings.Contains(flat(out), "would rename it from Company to Renamed") || !strings.Contains(flat(out), "would switch it off") || !strings.Contains(flat(out), "would forget the claim and value named for an administrator") || strings.Contains(flat(out), aSecret) {
		t.Errorf("a dry run of changing a provider = %v", out)
	}
	if len(dock.Requests(ssoChange)) != 0 {
		t.Fatal("a provider was changed by a call that was refused, changed nothing or was a dry run")
	}

	// several at once: only what changes is sent, the claim is forgotten by a call of its own, and the secret is never said
	out := suite.Call(t, "sso_edit", map[string]any{"provider": "COMPANY", "name": " Renamed ", "issuer_url": "https://sso.example/realms/main", "client_id": "dockhand", "client_secret": aSecret, "scopes": "openid email", "enabled": false, "admin_claim": " "})
	calls := dock.Requests(ssoChange)
	if len(calls) != 2 {
		t.Fatalf("a change with a claim to forget made %d calls, want the change and the forgetting", len(calls))
	}
	if body := sent(t, calls[0]); body["name"] != "Renamed" || body["issuerUrl"] != "https://sso.example/realms/main" || body["clientSecret"] != aSecret || body["scopes"] != "openid email" || !acctest.IsBool(body["enabled"], false) || len(body) != 5 {
		t.Errorf("changing a provider sent %v", body)
	}
	if body := sent(t, calls[1]); body["adminClaim"] != "" || body["adminValue"] != "" || len(body) != 2 {
		t.Errorf("forgetting its claim sent %v", body)
	}
	if row := acctest.Object(t, out["provider"], "provider"); row["name"] != "Renamed" || !acctest.IsBool(row["enabled"], false) || row["admin_claim"] != nil || len(acctest.Strs(t, out["changes"], "changes")) != 6 || out["note"] != nil || strings.Contains(flat(out), aSecret) {
		t.Errorf("a provider changed = %v", out)
	}

	// each claim and the way back, and switched on, which is said to offer it to whoever signs in
	dock.Answer("PUT /api/auth/oidc/5", strings.Replace(spareSSO, `"enabled":false`, `"enabled":true`, 1))
	out = suite.Call(t, "sso_edit", map[string]any{"provider": "spare", "redirect_uri": "https://other.example/api/auth/oidc/callback", "username_claim": "sub", "email_claim": "mail", "display_name_claim": "cn", "admin_claim": "groups", "admin_value": "admins", "role_mappings_claim": "roles", "enabled": true})
	if body := sent(t, dock.Last("PUT /api/auth/oidc/5")); body["redirectUri"] != "https://other.example/api/auth/oidc/callback" || body["usernameClaim"] != "sub" || body["emailClaim"] != "mail" || body["displayNameClaim"] != "cn" || body["adminClaim"] != "groups" || body["adminValue"] != "admins" || body["roleMappingsClaim"] != "roles" || !acctest.IsBool(body["enabled"], true) || len(body) != 8 || !strings.Contains(acctest.Str(out["note"]), "offered to whoever signs in") {
		t.Errorf("a provider's claims changed = %v, sent %v", out, body)
	}
}

func TestSSOEditWhenItGoesWrong(t *testing.T) {
	t.Parallel()

	dock := withSSO(t)
	suite := session(t, dock, everything)

	// two by one name: neither is changed, and their numbers are given
	dock.Answer(ssoCall, `[`+spareSSO+`,`+strings.Replace(companySSO, `"name":"Company"`, `"name":"Spare"`, 1)+`]`)
	if msg := suite.CallErr(t, "sso_edit", map[string]any{"provider": "spare", "scopes": "openid"}); !strings.Contains(msg, `2 single sign-on providers are named "spare": give the number of one, 5 or 4`) {
		t.Errorf("a name two providers share = %q", msg)
	}
	dock.Answer(ssoCall, `[`+spareSSO+`,`+companySSO+`]`)

	// refused by Dockhand: nothing is said to have been done; and a claim it would not forget
	dock.Fail(ssoChange, http.StatusNotFound, "OIDC configuration not found")
	if msg := suite.CallErr(t, "sso_edit", map[string]any{"provider": "company", "scopes": "openid"}); !strings.Contains(msg, "Company was not changed") || !strings.Contains(msg, "OIDC configuration not found") {
		t.Errorf("a change Dockhand refuses = %q", msg)
	}
	if msg := suite.CallErr(t, "sso_edit", map[string]any{"provider": "company", "admin_claim": ""}); !strings.Contains(msg, "the claim named for an administrator was not forgotten") {
		t.Errorf("a forgetting Dockhand refuses = %q", msg)
	}
	dock.Fail(ssoCall, http.StatusForbidden, notAllowed)
	if msg := suite.CallErr(t, "sso_edit", map[string]any{"provider": "company", "scopes": "openid"}); !strings.Contains(msg, "the single sign-on providers could not be listed") {
		t.Errorf("a change when the providers cannot be listed = %q", msg)
	}
}

func TestSSODelete(t *testing.T) {
	t.Parallel()

	dock := withSSO(t)
	dock.Answer(ssoRemove, `{"success":true}`)
	suite := session(t, dock, everything)

	// without confirm it says what would go
	out := suite.Call(t, "sso_delete", map[string]any{"provider": "company"})
	if !acctest.IsBool(out["removed"], false) || out["provider"] != "Company" || !strings.Contains(flat(out), "Dockhand's record of Company at https://sso.example") || !strings.Contains(acctest.Str(out["note"]), "nothing was removed") || strings.Contains(acctest.Str(out["note"]), "default provider") || len(dock.Requests(ssoRemove)) != 0 {
		t.Fatalf("a removal not confirmed = %v", out)
	}
	if msg := suite.CallErr(t, "sso_delete", map[string]any{"provider": "nowhere", "confirm": true}); !strings.Contains(msg, `no single sign-on provider is named or numbered "nowhere"`) {
		t.Errorf("removing a provider there is none of = %q", msg)
	}

	// answered without success, and answered as done with the provider still listed
	dock.Answer(ssoRemove, `{"success":false}`)
	if msg := suite.CallErr(t, "sso_delete", map[string]any{"provider": "company", "confirm": true}); !strings.Contains(msg, "without its being said to have succeeded") {
		t.Errorf("a removal not said to succeed = %q", msg)
	}
	dock.Answer(ssoRemove, `{"success":true}`)
	if msg := suite.CallErr(t, "sso_delete", map[string]any{"provider": "company", "confirm": true}); !strings.Contains(msg, "answered as done, and it is still listed") {
		t.Errorf("a removal that left the provider = %q", msg)
	}

	// the default provider: said before and after, since Dockhand goes on naming it
	dock.Answer(loginCall, strings.Replace(loginIsOn, `"defaultProvider":"local"`, `"defaultProvider":"oidc:4"`, 1))
	if out = suite.Call(t, "sso_delete", map[string]any{"provider": 4}); !strings.Contains(acctest.Str(out["note"]), "it is marked the default provider") {
		t.Errorf("a removal of the default provider, not confirmed = %v", out)
	}
	dock.Then(ssoRemove, func(dockhandtest.Request) { dock.Answer(ssoCall, `[`+spareSSO+`]`) })
	if out = suite.Call(t, "sso_delete", map[string]any{"provider": 4, "confirm": true}); !acctest.IsBool(out["removed"], true) || !strings.Contains(acctest.Str(out["note"]), "goes on naming it as that: login_set marks another") {
		t.Errorf("the default provider removed = %v", out)
	}

	// removed and not read back, and not removed at all; the login unread is no reason to stop
	dock.Fail(loginCall, http.StatusForbidden, notAllowed)
	dock.Answer("DELETE /api/auth/oidc/5", `{"success":true}`)
	dock.Then("DELETE /api/auth/oidc/5", func(dockhandtest.Request) { dock.Fail(ssoCall, http.StatusInternalServerError, "database is locked") })
	if out = suite.Call(t, "sso_delete", map[string]any{"provider": "spare", "confirm": true}); !acctest.IsBool(out["removed"], false) || !strings.Contains(acctest.Str(out["note"]), "it was removed, and the providers could not be listed afterwards") {
		t.Errorf("a removal not read back = %v", out)
	}
	dock.Answer(ssoCall, `[`+spareSSO+`]`)
	dock.Then("DELETE /api/auth/oidc/5", nil)
	dock.Fail("DELETE /api/auth/oidc/5", http.StatusNotFound, "OIDC configuration not found")
	if msg := suite.CallErr(t, "sso_delete", map[string]any{"provider": "spare", "confirm": true}); !strings.Contains(msg, "spare was not removed") {
		t.Errorf("a removal Dockhand refuses = %q", msg)
	}
	dock.Fail(ssoCall, http.StatusForbidden, notAllowed)
	if msg := suite.CallErr(t, "sso_delete", map[string]any{"provider": "spare"}); !strings.Contains(msg, "the single sign-on providers could not be listed") {
		t.Errorf("a removal when the providers cannot be listed = %q", msg)
	}
}
