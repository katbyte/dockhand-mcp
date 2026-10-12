package tools

import (
	"net/http"
	"strings"
	"testing"

	"github.com/katbyte/dockhand-mcp/sdk/dockhandtest"
	"github.com/katbyte/go-kt/mcp/acctest"
)

// What a 1.0.51 server answers about its secret providers, with invented names: a Vault that two of alpha's stacks and a stack of an environment
// since removed take their secrets from, which is the default and has a setting with a secret's name; one that differs from it only by case, with no
// settings at all; and a spare of another kind nothing uses.
const (
	threeProviders  = `[{"id":4,"type":"vault","name":"Main","createdAt":"2026-10-11 03:25:22","updatedAt":"2026-10-11 03:25:22"},{"id":1,"type":"vault","name":"main","createdAt":"2026-10-11 03:22:58","updatedAt":"2026-10-11T03:25:22.481Z"},{"id":7,"type":"doppler","name":"spare","createdAt":"2026-10-11 03:30:00","updatedAt":"2026-10-11 03:30:00"}]`
	mainProvider    = `{"id":1,"type":"vault","name":"main","createdAt":"2026-10-11 03:22:58","updatedAt":"2026-10-11T03:25:22.481Z","config":{"address":"http://vault.example:8200","mount":"kv","audit_token":"abc","verify":true},"stacksUsing":[{"stackName":"mail","environmentId":1},{"stackName":"old","environmentId":9},{"stackName":"apps","environmentId":1}]}`
	otherProvider   = `{"id":4,"type":"vault","name":"Main","createdAt":"2026-10-11 03:25:22","updatedAt":"2026-10-11 03:25:22","stacksUsing":[]}`
	spareProvider   = `{"id":7,"type":"doppler","name":"spare","createdAt":"2026-10-11 03:30:00","updatedAt":"2026-10-11 03:30:00","config":{"project":"web"},"stacksUsing":[]}`
	providerReached = `{"ok":true}`
	tokenRefused    = `{"ok":false,"error":"Vault returned 403: permission denied."}` //nolint:gosec // G101: what a server answers, and no token
	twoKeys         = `{"ok":true,"bulkKeys":["user","password"],"resolvedRefs":[]}`
	nothingThere    = `{"ok":false,"error":"HashiCorp Vault: KV v2 path not found: kv/nope"}`

	providersCall  = "GET /api/secret-providers"
	providerMake   = "POST /api/secret-providers"
	providerTry    = "POST /api/secret-providers/test"
	defaultCall    = "GET /api/secret-providers/default"
	defaultSet     = "PUT /api/secret-providers/default"
	mainCall       = "GET /api/secret-providers/1"
	mainSave       = "PUT /api/secret-providers/1"
	mainTry        = "POST /api/secret-providers/1/test"
	mainLook       = "POST /api/secret-providers/1/probe"
	mainRemove     = "DELETE /api/secret-providers/1"
	spareLook      = "POST /api/secret-providers/7/probe"
	spareRemove    = "DELETE /api/secret-providers/7"
	madeProvider   = `{"id":9,"type":"vault","name":"second","createdAt":"2026-10-11 04:00:00","updatedAt":"2026-10-11 04:00:00"}`
	secondProvider = `{"id":9,"type":"vault","name":"second","createdAt":"2026-10-11 04:00:00","updatedAt":"2026-10-11 04:00:00","config":{"address":"http://vault.example:8200"},"stacksUsing":[]}`
)

// withProviders is a Dockhand managing alpha and beta that keeps three secret providers, the first of them the default.
func withProviders(t *testing.T) *dockhandtest.Server {
	t.Helper()

	dock := withStackFiles(t)
	dock.Answer(providersCall, threeProviders)
	dock.Answer(defaultCall, `{"providerId":1}`)
	dock.Answer(mainCall, mainProvider)
	dock.Answer("GET /api/secret-providers/4", otherProvider)
	dock.Answer("GET /api/secret-providers/7", spareProvider)

	return dock
}

func TestSecretProviderList(t *testing.T) {
	t.Parallel()

	dock := withProviders(t)
	suite := session(t, dock, Options{})

	// by name whatever its case, each with where it is, what uses it, and nothing it signs in with
	out := suite.Call(t, "secret_provider_list", nil)
	rows := acctest.Rows(t, out["providers"], "providers")
	if len(rows) != 3 || rows[0]["name"] != "main" || rows[1]["name"] != "Main" || rows[2]["name"] != "spare" || acctest.Num(t, out["total"], "total") != 3 {
		t.Fatalf("the providers, by name = %v", out)
	}
	if got := flat(rows[0]); got != `{"changed":"2026-10-11T03:25:22Z","created":"2026-10-11T03:22:58Z","default":true,"id":1,"name":"main","settings":{"address":"http://vault.example:8200","audit_token":"***","mount":"kv","verify":"true"},"stacks":[{"env":"#9","stack":"old"},{"env":"alpha","stack":"apps"},{"env":"alpha","stack":"mail"}],"type":"vault"}` {
		t.Errorf("a provider stacks use = %s", got)
	}
	if rows[1]["settings"] != nil || rows[1]["stacks"] != nil || rows[1]["default"] != nil || acctest.Object(t, rows[2]["settings"], "settings")["project"] != "web" {
		t.Errorf("the providers nothing uses = %v, %v", rows[1], rows[2])
	}
	if acctest.Num(t, out["hidden"], "hidden") != 1 || !strings.Contains(acctest.Str(out["note"]), "reveal_secrets") {
		t.Errorf("a setting with a secret's name is hidden and said to be: %v", out)
	}

	// one alone, by name letter for letter, by number, and whatever its case when that is one provider; asked for, a hidden setting is shown
	for given, want := range map[any]string{"Main": "Main", 4: "Main", "SPARE": "spare", "main": "main"} {
		one := acctest.Rows(t, suite.Call(t, "secret_provider_list", map[string]any{"provider": given})["providers"], "providers")
		if len(one) != 1 || one[0]["name"] != want {
			t.Errorf("the provider %v = %v, want %s", given, one, want)
		}
	}
	shown := suite.Call(t, "secret_provider_list", map[string]any{"provider": "main", "reveal_secrets": true})
	if row := acctest.Rows(t, shown["providers"], "providers")[0]; acctest.Object(t, row["settings"], "settings")["audit_token"] != "abc" || shown["hidden"] != nil || shown["note"] != nil {
		t.Errorf("a provider with its settings asked for whole = %v", shown)
	}
	for what, tc := range map[string]struct {
		given any
		want  string
	}{
		"one there is none of":      {"nope", `no secret provider is named or numbered "nope": the Dockhand keeps Main, main, spare`},
		"two but for their case":    {"MAIN", `no secret provider is named "MAIN" letter for letter, and 2 are but for their case: give the number of one, Main (4) or main (1)`},
		"no name at all":            {" ", "provider is required: the secret provider's name or number"},
		"what is no name or number": {[]any{1}, "provider is [1]: it is a name or a number"},
	} {
		if msg := suite.CallErr(t, "secret_provider_list", map[string]any{"provider": tc.given}); !strings.Contains(msg, tc.want) {
			t.Errorf("%s = %q, want %q", what, msg, tc.want)
		}
	}

	// any part of it that cannot be read is said
	for call, want := range map[string]string{
		providersCall:           "the secret providers could not be listed",
		defaultCall:             "which secret provider is the default could not be read",
		"GET /api/environments": "the environments could not be listed",
		mainCall:                "the secret provider main could not be read",
	} {
		refusing := withProviders(t)
		refusing.Fail(call, http.StatusForbidden, notAllowed)
		if msg := session(t, refusing, Options{}).CallErr(t, "secret_provider_list", nil); !strings.Contains(msg, want) {
			t.Errorf("%s refused = %q, want %q", call, msg, want)
		}
	}

	dock.Answer(providersCall, `[]`)
	if out = suite.Call(t, "secret_provider_list", nil); acctest.Num(t, out["total"], "total") != 0 || !strings.Contains(acctest.Str(out["note"]), "keeps no secret provider") {
		t.Errorf("a Dockhand with no provider = %v", out)
	}
}

func TestSecretProviderTest(t *testing.T) {
	t.Parallel()

	dock := withProviders(t)
	suite := session(t, dock, Options{})
	given := map[string]any{"address": "http://vault.example:8200", "token": "made-up"}

	// one that is not saved, by its kind and everything it would be saved with
	dock.Answer(providerTry, providerReached)
	out := suite.Call(t, "secret_provider_test", map[string]any{"type": " vault ", "settings": given})
	if got := flat(out); got != `{"reached":true,"type":"vault"}` || dock.Last(providerTry).Body != `{"config":{"address":"http://vault.example:8200","token":"made-up"},"type":"vault"}` {
		t.Errorf("a provider not saved, tried = %s, sent %s", got, dock.Last(providerTry).Body)
	}
	dock.Answer(providerTry, tokenRefused)
	if out = suite.Call(t, "secret_provider_test", map[string]any{"type": "vault", "settings": given}); !acctest.IsBool(out["reached"], false) || out["error"] != "Vault returned 403: permission denied." {
		t.Errorf("a provider that turns the token away = %v", out)
	}

	// one the Dockhand keeps, as it is saved and with other settings, which are sent whole and change nothing
	dock.Answer(mainTry, providerReached)
	out = suite.Call(t, "secret_provider_test", map[string]any{"provider": "main"})
	if got := flat(out); got != `{"provider":"main","reached":true,"type":"vault"}` || dock.Last(mainTry).Body != `{}` {
		t.Errorf("a saved provider tried = %s, sent %s", got, dock.Last(mainTry).Body)
	}
	dock.Answer(mainTry, `{"ok":false,"error":"You changed the host or address, so enter its credential to test - the saved one is not reused for a different server."}`)
	out = suite.Call(t, "secret_provider_test", map[string]any{"provider": 1, "settings": map[string]any{"address": "http://elsewhere.example:8200"}})
	if !acctest.IsBool(out["reached"], false) || !strings.Contains(acctest.Str(out["error"]), "enter its credential") || !strings.Contains(acctest.Str(out["note"]), "in place of the saved ones") || dock.Last(mainTry).Body != `{"config":{"address":"http://elsewhere.example:8200"}}` {
		t.Errorf("a saved provider tried with another address = %v, sent %s", out, dock.Last(mainTry).Body)
	}

	for what, tc := range map[string]struct {
		args map[string]any
		want string
	}{
		"nothing named":             {map[string]any{}, "provider or type is required"},
		"a kind with no settings":   {map[string]any{"type": "vault"}, "settings is required with type"},
		"a kind for a saved one":    {map[string]any{"provider": "main", "type": "vault"}, "type is for a provider that is not saved"},
		"a provider there is none":  {map[string]any{"provider": "nope"}, `no secret provider is named or numbered "nope"`},
		"a provider with no number": {map[string]any{"provider": 1.5}, "provider is 1.5: its number is a whole one"},
	} {
		if msg := suite.CallErr(t, "secret_provider_test", tc.args); !strings.Contains(msg, tc.want) {
			t.Errorf("%s = %q, want %q", what, msg, tc.want)
		}
	}
	dock.Fail(providerTry, http.StatusForbidden, notAllowed)
	if msg := suite.CallErr(t, "secret_provider_test", map[string]any{"type": "vault", "settings": given}); !strings.Contains(msg, "the provider could not be tried") {
		t.Errorf("a try the account may not make = %q", msg)
	}
	dock.Fail(mainTry, http.StatusNotFound, "Secret provider not found")
	if msg := suite.CallErr(t, "secret_provider_test", map[string]any{"provider": "main"}); !strings.Contains(msg, "the provider main could not be tried") {
		t.Errorf("a saved provider that went away = %q", msg)
	}
	dock.Fail(providersCall, http.StatusForbidden, notAllowed)
	if msg := suite.CallErr(t, "secret_provider_test", map[string]any{"provider": "main"}); !strings.Contains(msg, "the secret providers could not be listed") {
		t.Errorf("providers the account may not see = %q", msg)
	}
}

func TestSecretProviderKeys(t *testing.T) {
	t.Parallel()

	dock := withProviders(t)
	suite := session(t, dock, Options{})

	// the names at a path, in order, and never a value
	dock.Answer(mainLook, twoKeys)
	out := suite.Call(t, "secret_provider_keys", map[string]any{"provider": "main", "path": " prod/db "})
	if got := flat(out); got != `{"keys":["password","user"],"path":"prod/db","provider":"main","total":2}` || dock.Last(mainLook).Body != `{"selector":"prod/db"}` {
		t.Errorf("the keys at a path = %s, asked with %s", got, dock.Last(mainLook).Body)
	}
	dock.Answer(mainLook, `{"ok":true,"bulkKeys":[],"resolvedRefs":[]}`)
	if out = suite.Call(t, "secret_provider_keys", map[string]any{"provider": "main", "path": "empty"}); acctest.Num(t, out["total"], "total") != 0 || !strings.Contains(acctest.Str(out["note"]), "has no secret there") {
		t.Errorf("a path with nothing at it = %v", out)
	}

	// a path the provider has nothing at is an error that gives Dockhand's word for it, as is a provider that went away or cannot be asked
	dock.Answer(mainLook, nothingThere)
	if msg := suite.CallErr(t, "secret_provider_keys", map[string]any{"provider": "main", "path": "nope"}); !strings.Contains(msg, "the provider main gave nothing for the path nope: Dockhand says HashiCorp Vault: KV v2 path not found: kv/nope") {
		t.Errorf("a path there is none of = %q", msg)
	}
	dock.Answer(mainLook, `{"ok":false}`)
	if msg := suite.CallErr(t, "secret_provider_keys", map[string]any{"provider": "main", "path": "nope"}); !strings.Contains(msg, "it could not look, and gives no reason") {
		t.Errorf("a look that failed for no reason given = %q", msg)
	}
	dock.Fail(mainLook, http.StatusNotFound, "Secret provider not found")
	if msg := suite.CallErr(t, "secret_provider_keys", map[string]any{"provider": "main", "path": "prod/db"}); !strings.Contains(msg, "the provider main is gone") {
		t.Errorf("a provider removed meanwhile = %q", msg)
	}
	dock.Fail(mainLook, http.StatusForbidden, notAllowed)
	if msg := suite.CallErr(t, "secret_provider_keys", map[string]any{"provider": "main", "path": "prod/db"}); !strings.Contains(msg, "the provider main could not be asked what it has at prod/db") {
		t.Errorf("a look the account may not take = %q", msg)
	}
	for what, tc := range map[string]struct {
		args map[string]any
		want string
	}{
		"no path":                  {map[string]any{"provider": "main", "path": " "}, "path is required"},
		"a provider there is none": {map[string]any{"provider": "nope", "path": "x"}, `no secret provider is named or numbered "nope"`},
	} {
		if msg := suite.CallErr(t, "secret_provider_keys", tc.args); !strings.Contains(msg, tc.want) {
			t.Errorf("%s = %q, want %q", what, msg, tc.want)
		}
	}
	dock.Fail(providersCall, http.StatusForbidden, notAllowed)
	if msg := suite.CallErr(t, "secret_provider_keys", map[string]any{"provider": "main", "path": "x"}); !strings.Contains(msg, "the secret providers could not be listed") {
		t.Errorf("providers the account may not see = %q", msg)
	}
}

func TestSecretProviderCreate(t *testing.T) {
	t.Parallel()

	saving := map[string]any{"name": " second ", "type": "vault", "settings": map[string]any{"address": "http://vault.example:8200", "token": "made-up-token"}}
	// made is a Dockhand that saves the provider, after which it is listed and read back
	made := func(t *testing.T) *dockhandtest.Server {
		t.Helper()

		dock := withProviders(t)
		dock.Answer(providerTry, providerReached)
		dock.AnswerMade(providerMake, madeProvider)
		dock.Answer(defaultSet, `{"providerId":9}`)
		dock.Answer("GET /api/secret-providers/9", secondProvider)
		dock.Then(defaultSet, func(dockhandtest.Request) { dock.Answer(defaultCall, `{"providerId":9}`) })

		return dock
	}

	t.Run("refused before anything is saved", func(t *testing.T) {
		t.Parallel()

		dock := made(t)
		suite := session(t, dock, everything)
		for what, tc := range map[string]struct {
			more map[string]any
			want string
		}{
			"no name":         {map[string]any{"name": " "}, "name is required"},
			"no kind":         {map[string]any{"type": ""}, "type is required: the provider's kind, vault (HashiCorp Vault"},
			"no settings":     {map[string]any{"settings": map[string]any{}}, "settings is required"},
			"a name in use":   {map[string]any{"name": "main"}, "a secret provider is already named main: secret_provider_edit changes it"},
			"another's name ": {map[string]any{"name": "Main"}, "a secret provider is already named Main"},
		} {
			if msg := suite.CallErr(t, "secret_provider_create", over(saving, tc.more)); !strings.Contains(msg, tc.want) {
				t.Errorf("%s = %q, want %q", what, msg, tc.want)
			}
		}

		// tried first: one Dockhand cannot use is not saved, and its own full stop is not doubled
		dock.Answer(providerTry, tokenRefused)
		if msg := suite.CallErr(t, "secret_provider_create", saving); !strings.Contains(msg, "Dockhand could not use the provider, so nothing was saved: Vault returned 403: permission denied. save_unreachable saves it all the same") {
			t.Errorf("a provider that cannot be used = %q", msg)
		}
		dock.Answer(providerTry, `{"ok":false}`)
		if msg := suite.CallErr(t, "secret_provider_create", saving); !strings.Contains(msg, "so nothing was saved: it gives no reason") {
			t.Errorf("a provider that cannot be used for no reason given = %q", msg)
		}

		// a dry run says what would be, of one that can be used and of one that cannot and is asked for all the same
		dry := suite.Call(t, "secret_provider_create", over(saving, map[string]any{"dry_run": true, "save_unreachable": true}))
		if got := strings.Join(acctest.Strs(t, dry["changes"], "changes"), "; "); got != "would save the vault provider second" || !strings.Contains(acctest.Str(dry["note"]), "it would be saved as save_unreachable asks though Dockhand cannot use it") || dry["provider"] != nil {
			t.Errorf("a dry run of one that cannot be used = %v", dry)
		}
		dock.Answer(providerTry, providerReached)
		dry = suite.Call(t, "secret_provider_create", over(saving, map[string]any{"dry_run": true, "default": true}))
		if got := strings.Join(acctest.Strs(t, dry["changes"], "changes"), "; "); got != "would save the vault provider second; would make it the default" || !acctest.IsBool(dry["dry_run"], true) || dry["note"] != nil {
			t.Errorf("a dry run = %v", dry)
		}
		if n := len(dock.Requests(providerMake)); n != 0 {
			t.Errorf("%d providers were saved by what was refused or was a dry run", n)
		}

		dock.Fail(providerTry, http.StatusForbidden, notAllowed)
		if msg := suite.CallErr(t, "secret_provider_create", saving); !strings.Contains(msg, "the provider could not be tried, so nothing was saved") {
			t.Errorf("a try the account may not make = %q", msg)
		}
		dock.Fail(providersCall, http.StatusForbidden, notAllowed)
		if msg := suite.CallErr(t, "secret_provider_create", saving); !strings.Contains(msg, "the secret providers could not be listed") {
			t.Errorf("providers the account may not see = %q", msg)
		}
	})

	t.Run("saved", func(t *testing.T) {
		t.Parallel()

		dock := made(t)
		suite := session(t, dock, everything)

		// tried with what it is saved with, saved under its name trimmed, made the default, and read back without its token
		out := suite.Call(t, "secret_provider_create", over(saving, map[string]any{"default": true}))
		if body := dock.Last(providerMake).Body; body != `{"config":{"address":"http://vault.example:8200","token":"made-up-token"},"name":"second","type":"vault"}` || dock.Last(providerTry).Body != `{"config":{"address":"http://vault.example:8200","token":"made-up-token"},"type":"vault"}` || dock.Last(defaultSet).Body != `{"providerId":9}` {
			t.Errorf("a provider was saved with %s, tried with %s, made the default with %s", body, dock.Last(providerTry).Body, dock.Last(defaultSet).Body)
		}
		if got := flat(out); got != `{"changes":["saved the vault provider second","made it the default"],"provider":{"changed":"2026-10-11T04:00:00Z","created":"2026-10-11T04:00:00Z","default":true,"id":9,"name":"second","settings":{"address":"http://vault.example:8200"},"type":"vault"}}` {
			t.Errorf("a provider saved = %s", got)
		}

		// one that cannot be used, saved because that was asked: the note says so
		dock.Answer(providerTry, tokenRefused)
		out = suite.Call(t, "secret_provider_create", over(saving, map[string]any{"save_unreachable": true}))
		if !strings.Contains(acctest.Str(out["note"]), "it was saved as save_unreachable asked though Dockhand could not use it: Vault returned 403: permission denied") || len(acctest.Strs(t, out["changes"], "changes")) != 1 {
			t.Errorf("a provider saved though it cannot be used = %v", out)
		}
	})

	t.Run("not saved, or saved in part", func(t *testing.T) {
		t.Parallel()

		dock := made(t)
		suite := session(t, dock, everything)

		// saved, and not made the default: an error that says both, with the provider as it is
		dock.Fail(defaultSet, http.StatusBadRequest, "Secret provider not found")
		if msg := suite.CallErr(t, "secret_provider_create", over(saving, map[string]any{"default": true})); !strings.Contains(msg, "the provider second was saved and could not be made the default") {
			t.Errorf("a provider saved and not made the default = %q", msg)
		}
		dock.Fail(providerMake, http.StatusBadRequest, "A secret provider with this name already exists")
		if msg := suite.CallErr(t, "secret_provider_create", saving); !strings.Contains(msg, "the provider second was not saved") || !strings.Contains(msg, "already exists") {
			t.Errorf("a provider Dockhand would not save = %q", msg)
		}

		// saved, and not to be read back: no error, and a note
		for call, want := range map[string]string{defaultCall: "which secret provider is the default could not be read", "GET /api/environments": "the environments could not be listed", "GET /api/secret-providers/9": "HTTP 403"} {
			unread := made(t)
			unread.Then(providerMake, func(dockhandtest.Request) { unread.Fail(call, http.StatusForbidden, notAllowed) })
			out := session(t, unread, everything).Call(t, "secret_provider_create", saving)
			if note := acctest.Str(out["note"]); !strings.Contains(note, "it was done, and could not be read back") || !strings.Contains(note, want) || out["provider"] != nil || len(acctest.Strs(t, out["changes"], "changes")) != 1 {
				t.Errorf("a provider saved and not read back for %s = %v", call, out)
			}
		}
	})
}

func TestSecretProviderEdit(t *testing.T) {
	t.Parallel()

	//nolint:gosec // G101: a made-up token
	const newToken = "made-up-new-token"

	t.Run("refused before anything is saved", func(t *testing.T) {
		t.Parallel()

		dock := withProviders(t)
		dock.Answer(mainTry, providerReached)
		suite := session(t, dock, everything)
		for what, tc := range map[string]struct {
			args map[string]any
			want string
		}{
			"nothing to change":          {map[string]any{"provider": "main"}, "nothing to change was given: name, type, settings, remove_settings or default"},
			"one set and taken out":      {map[string]any{"provider": "main", "settings": map[string]any{"mount": "x"}, "remove_settings": []any{"mount"}}, "the setting mount is in both settings and remove_settings"},
			"a provider there is none":   {map[string]any{"provider": "nope", "name": "x"}, `no secret provider is named or numbered "nope"`},
			"another's name":             {map[string]any{"provider": "main", "name": "spare"}, "a secret provider is already named spare"},
			"an address with no token":   {map[string]any{"provider": "main", "settings": map[string]any{"address": "http://elsewhere.example:8200"}}, "the address of main is changed and no token is given: Dockhand would keep the token it has and send it to the new address"},
			"a host with no token":       {map[string]any{"provider": "spare", "settings": map[string]any{"project": "http://x.example"}, "remove_settings": []any{"nope"}}, "nothing was changed"},
			"an address and a new mount": {map[string]any{"provider": "main", "settings": map[string]any{"address": "http://elsewhere.example:8200", "mount": "other"}}, "the address of main is changed and no token is given"},
		} {
			if what == "a host with no token" {
				dock.Answer("POST /api/secret-providers/7/test", tokenRefused)
			}
			if msg := suite.CallErr(t, "secret_provider_edit", tc.args); !strings.Contains(msg, tc.want) {
				t.Errorf("%s = %q, want %q", what, msg, tc.want)
			}
		}

		// new settings are tried whole before they are saved, and ones Dockhand cannot use the provider with are not
		dock.Answer(mainTry, tokenRefused)
		msg := suite.CallErr(t, "secret_provider_edit", map[string]any{"provider": "main", "settings": map[string]any{"mount": "other"}})
		if !strings.Contains(msg, "Dockhand could not use main with the new settings, so nothing was changed: Vault returned 403: permission denied. save_unreachable saves them all the same") || dock.Last(mainTry).Body != `{"config":{"address":"http://vault.example:8200","audit_token":"abc","mount":"other","verify":true}}` {
			t.Errorf("settings that do not work = %q, tried with %s", msg, dock.Last(mainTry).Body)
		}
		dock.Fail(mainTry, http.StatusForbidden, notAllowed)
		if msg = suite.CallErr(t, "secret_provider_edit", map[string]any{"provider": "main", "settings": map[string]any{"mount": "other"}}); !strings.Contains(msg, "the new settings could not be tried, so nothing was changed") {
			t.Errorf("a try the account may not make = %q", msg)
		}
		dock.Answer(mainTry, providerReached)

		// what is that way already is not saved again: a name, a kind, a setting as it is, one to take out that is not there, the default it is
		out := suite.Call(t, "secret_provider_edit", map[string]any{"provider": "main", "name": "main", "type": "vault", "settings": map[string]any{"mount": "kv"}, "remove_settings": []any{"namespace"}, "default": true})
		if !strings.Contains(acctest.Str(out["note"]), "nothing to do: it is that way already") || len(acctest.Strs(t, out["changes"], "changes")) != 0 || acctest.Object(t, out["provider"], "provider")["name"] != "main" {
			t.Errorf("a provider that is that way already = %v", out)
		}

		// a dry run says each thing that would be done, and gives the provider as it is
		dry := suite.Call(t, "secret_provider_edit", map[string]any{"provider": "main", "name": "primary", "type": "infisical", "settings": map[string]any{"mount": "other", "token": newToken}, "remove_settings": []any{"verify"}, "default": false, "dry_run": true})
		if got := strings.Join(acctest.Strs(t, dry["changes"], "changes"), "; "); got != "would rename it from main to primary; would change its kind from vault to infisical; would set its mount, token; would take out its verify; would have it be the default no longer" || !acctest.IsBool(dry["dry_run"], true) || acctest.Object(t, dry["provider"], "provider")["name"] != "main" {
			t.Errorf("a dry run = %v", dry)
		}
		if n := len(dock.Requests(mainSave)) + len(dock.Requests(defaultSet)); n != 0 {
			t.Errorf("%d changes were sent for what was refused, was that way already or was a dry run", n)
		}

		for call, want := range map[string]string{mainCall: "the provider main could not be read, so nothing was changed", defaultCall: "which secret provider is the default could not be read", providersCall: "the secret providers could not be listed"} {
			refusing := withProviders(t)
			refusing.Fail(call, http.StatusForbidden, notAllowed)
			if msg := session(t, refusing, everything).CallErr(t, "secret_provider_edit", map[string]any{"provider": "main", "name": "x"}); !strings.Contains(msg, want) {
				t.Errorf("%s refused = %q, want %q", call, msg, want)
			}
		}
	})

	t.Run("changed", func(t *testing.T) {
		t.Parallel()

		dock := withProviders(t)
		dock.Answer(mainTry, providerReached)
		dock.Answer(mainSave, `{"id":1,"type":"vault","name":"primary","createdAt":"2026-10-11 03:22:58","updatedAt":"2026-10-11T05:00:00.000Z"}`)
		dock.Answer(defaultSet, `{"providerId":null}`)
		dock.Then(mainSave, func(dockhandtest.Request) {
			dock.Answer(mainCall, `{"id":1,"type":"vault","name":"primary","createdAt":"2026-10-11 03:22:58","updatedAt":"2026-10-11T05:00:00.000Z","config":{"address":"http://elsewhere.example:8200","mount":"kv","audit_token":"abc"},"stacksUsing":[]}`)
		})
		dock.Then(defaultSet, func(dockhandtest.Request) { dock.Answer(defaultCall, `{"providerId":null}`) })
		suite := session(t, dock, everything)

		// the settings go whole, what Dockhand gave back with what is given over it and without what is taken out; an address goes with its token
		out := suite.Call(t, "secret_provider_edit", map[string]any{"provider": "main", "name": " primary ", "settings": map[string]any{"address": "http://elsewhere.example:8200", "token": newToken, "mount": "kv"}, "remove_settings": []any{"verify", "nope"}, "default": false})
		if body := dock.Last(mainSave).Body; body != `{"config":{"address":"http://elsewhere.example:8200","audit_token":"abc","mount":"kv","token":"made-up-new-token"},"name":"primary"}` || dock.Last(mainTry).Body != `{"config":{"address":"http://elsewhere.example:8200","audit_token":"abc","mount":"kv","token":"made-up-new-token"}}` || dock.Last(defaultSet).Body != `{}` {
			t.Errorf("a provider was changed with %s, tried with %s, the default set with %s", body, dock.Last(mainTry).Body, dock.Last(defaultSet).Body)
		}
		if got := strings.Join(acctest.Strs(t, out["changes"], "changes"), "; "); got != "renamed it from main to primary; set its address, token; took out its verify; had it be the default no longer" {
			t.Errorf("what was done = %q", got)
		}
		if row := acctest.Object(t, out["provider"], "provider"); row["name"] != "primary" || row["default"] != nil || acctest.Object(t, row["settings"], "settings")["address"] != "http://elsewhere.example:8200" || strings.Contains(flat(out), newToken) {
			t.Errorf("the provider read back = %v", out)
		}

		// only the default: nothing of the provider itself is sent
		before := len(dock.Requests(mainSave))
		dock.Then(defaultSet, func(dockhandtest.Request) { dock.Answer(defaultCall, `{"providerId":7}`) })
		out = suite.Call(t, "secret_provider_edit", map[string]any{"provider": "spare", "default": true})
		if got := strings.Join(acctest.Strs(t, out["changes"], "changes"), "; "); got != "made it the default" || dock.Last(defaultSet).Body != `{"providerId":7}` || len(dock.Requests(mainSave)) != before || !acctest.IsBool(acctest.Object(t, out["provider"], "provider")["default"], true) {
			t.Errorf("a provider made the default = %v, sent %s", out, dock.Last(defaultSet).Body)
		}

		// one with no settings at all given its first, and a kind changed alone
		dock.Answer("POST /api/secret-providers/4/test", tokenRefused)
		dock.Answer("PUT /api/secret-providers/4", otherProvider)
		out = suite.Call(t, "secret_provider_edit", map[string]any{"provider": 4, "type": "doppler", "settings": map[string]any{"project": "web"}, "save_unreachable": true})
		if body := dock.Last("PUT /api/secret-providers/4").Body; body != `{"config":{"project":"web"},"type":"doppler"}` || !strings.Contains(acctest.Str(out["note"]), "the settings were saved as save_unreachable asks though Dockhand cannot use the provider with them: Vault returned 403: permission denied") {
			t.Errorf("a provider given its first settings = %v, sent %s", out, body)
		}
	})

	t.Run("not changed, or changed in part", func(t *testing.T) {
		t.Parallel()

		dock := withProviders(t)
		dock.Answer(mainTry, providerReached)
		suite := session(t, dock, everything)

		dock.Fail(mainSave, http.StatusBadRequest, "Name cannot be empty")
		if msg := suite.CallErr(t, "secret_provider_edit", map[string]any{"provider": "main", "name": "primary"}); !strings.Contains(msg, "the provider main was not changed") || !strings.Contains(msg, "Name cannot be empty") {
			t.Errorf("a change Dockhand refused = %q", msg)
		}
		dock.Answer(mainSave, `{"id":1,"type":"vault","name":"primary"}`)
		dock.Fail(defaultSet, http.StatusInternalServerError, "Failed to set default secret provider")
		if msg := suite.CallErr(t, "secret_provider_edit", map[string]any{"provider": "main", "name": "primary", "default": false}); !strings.Contains(msg, "which provider is the default was not changed") {
			t.Errorf("a default Dockhand would not change = %q", msg)
		}
	})
}

func TestSecretProviderDelete(t *testing.T) {
	t.Parallel()

	dock := withProviders(t)
	suite := session(t, dock, everything)

	// without confirm it says what would go, the stacks that use it among them, and removes nothing
	preview := suite.Call(t, "secret_provider_delete", map[string]any{"provider": "main"})
	would := acctest.Strs(t, preview["would_go"], "would_go")
	if len(would) != 3 || would[0] != "the vault provider main, with the token Dockhand keeps for it" || would[1] != "where the secrets of 3 stacks are loaded from: old on #9, apps on alpha, mail on alpha. Each is taken off it, and is deployed with those variables empty from then on" || !strings.Contains(would[2], "the default the web interface offers") {
		t.Errorf("what removing a provider stacks use would take = %v", would)
	}
	if !acctest.IsBool(preview["removed"], false) || !strings.Contains(acctest.Str(preview["note"]), "confirm with even_used removes it") || len(acctest.Rows(t, preview["stacks"], "stacks")) != 3 {
		t.Errorf("a preview of a provider stacks use = %v", preview)
	}
	if preview = suite.Call(t, "secret_provider_delete", map[string]any{"provider": "spare"}); len(acctest.Strs(t, preview["would_go"], "would_go")) != 1 || !strings.Contains(acctest.Str(preview["note"]), "nothing was removed: confirm removes it") || preview["stacks"] != nil {
		t.Errorf("a preview of a provider nothing uses = %v", preview)
	}

	// one stacks use is not removed on confirm alone: Dockhand would leave each to be deployed with its secrets empty
	if msg := suite.CallErr(t, "secret_provider_delete", map[string]any{"provider": "main", "confirm": true}); !strings.Contains(msg, "the secrets of 3 stacks are loaded from main: old on #9, apps on alpha, mail on alpha. Dockhand would take each off it without a word") || !strings.Contains(msg, "even_used removes it all the same") {
		t.Errorf("a provider stacks use, confirmed alone = %q", msg)
	}
	if n := len(dock.Requests(mainRemove)) + len(dock.Requests(spareRemove)); n != 0 {
		t.Errorf("%d providers were removed by a preview or a refusal", n)
	}

	// removed, and read back as gone; the stacks left without one are named
	dock.Answer(mainRemove, done)
	dock.Then(mainRemove, func(dockhandtest.Request) { dock.Answer(providersCall, `[{"id":7,"type":"doppler","name":"spare"}]`) })
	gone := suite.Call(t, "secret_provider_delete", map[string]any{"provider": "main", "confirm": true, "even_used": true})
	if !acctest.IsBool(gone["removed"], true) || gone["would_go"] != nil || !strings.Contains(acctest.Str(gone["note"]), "3 stacks left with no secret provider, to be deployed with those variables empty: old on #9, apps on alpha, mail on alpha") {
		t.Errorf("a provider stacks used, removed = %v", gone)
	}
	dock.Answer(spareRemove, done)
	dock.Then(spareRemove, func(dockhandtest.Request) { dock.Answer(providersCall, `[]`) })
	if gone = suite.Call(t, "secret_provider_delete", map[string]any{"provider": 7, "confirm": true}); !acctest.IsBool(gone["removed"], true) || gone["note"] != nil || gone["provider"] != "spare" {
		t.Errorf("a provider nothing used, removed = %v", gone)
	}

	t.Run("not removed, or not read back", func(t *testing.T) {
		t.Parallel()

		dock := withProviders(t)
		suite := session(t, dock, everything)
		if msg := suite.CallErr(t, "secret_provider_delete", map[string]any{"provider": "nope"}); !strings.Contains(msg, `no secret provider is named or numbered "nope"`) {
			t.Errorf("a provider there is none of = %q", msg)
		}
		dock.Fail(spareRemove, http.StatusNotFound, "Secret provider not found")
		if msg := suite.CallErr(t, "secret_provider_delete", map[string]any{"provider": "spare", "confirm": true}); !strings.Contains(msg, "the provider spare was not removed") {
			t.Errorf("a provider Dockhand would not remove = %q", msg)
		}
		// answered as removed and still listed is an error, not a success
		dock.Answer(spareRemove, done)
		if msg := suite.CallErr(t, "secret_provider_delete", map[string]any{"provider": "spare", "confirm": true}); !strings.Contains(msg, "Dockhand answered that spare was removed, and still lists it") {
			t.Errorf("a provider answered as removed and still there = %q", msg)
		}
		dock.Then(spareRemove, func(dockhandtest.Request) { dock.Fail(providersCall, http.StatusForbidden, notAllowed) })
		if out := suite.Call(t, "secret_provider_delete", map[string]any{"provider": "spare", "confirm": true}); !acctest.IsBool(out["removed"], true) || !strings.Contains(acctest.Str(out["note"]), "it was removed, and the providers could not be listed afterwards") {
			t.Errorf("a provider removed and not read back = %v", out)
		}

		for call, want := range map[string]string{providersCall: "the secret providers could not be listed", defaultCall: "which secret provider is the default could not be read", "GET /api/environments": "the environments could not be listed", "GET /api/secret-providers/7": "the provider spare could not be read, so nothing was removed"} {
			refusing := withProviders(t)
			refusing.Fail(call, http.StatusForbidden, notAllowed)
			if msg := session(t, refusing, everything).CallErr(t, "secret_provider_delete", map[string]any{"provider": "spare", "confirm": true}); !strings.Contains(msg, want) {
				t.Errorf("%s refused = %q, want %q", call, msg, want)
			}
		}
	})
}
