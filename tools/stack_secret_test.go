package tools

import (
	"net/http"
	"strings"
	"testing"

	"github.com/katbyte/dockhand-mcp/sdk/dockhandtest"
	"github.com/katbyte/go-kt/mcp/acctest"
)

// What a 1.0.51 server keeps for alpha's mail stack once its secrets are loaded from the provider main, at a path its .env file names: the stack's
// variables say which provider and what the last deploy was given, and how the stack is kept says the provider's number.
const (
	boundEnv     = `{"variables":[{"key":"TZ","value":"UTC","isSecret":false},{"key":"DOCKHAND_SECRET_SELECTOR","value":"prod/db","isSecret":false}],"injectedSecretKeys":["password","user"],"secretProvider":{"type":"vault","name":"main"}}`
	boundRaw     = `{"content":"TZ=UTC\nDOCKHAND_SECRET_SELECTOR=prod/db\n"}`
	boundSources = `{"mail":{"sourceType":"internal","composePath":"/app/data/stacks/alpha/mail/compose.yaml","repository":null,"secretProviderId":1,"icon":null,"envVarCount":2}}`

	mailEnvCall  = "GET /api/stacks/mail/env"
	mailRawCall  = "GET /api/stacks/mail/env/raw"
	mailRawSave  = "PUT /api/stacks/mail/env/raw"
	mailFileSave = "PUT /api/stacks/mail/compose"
	keptHowCall  = "GET /api/stacks/sources"
)

// withBoundStack is a Dockhand whose mail stack on alpha has its secrets loaded from the provider main, at prod/db.
func withBoundStack(t *testing.T) *dockhandtest.Server {
	t.Helper()

	dock := withProviders(t)
	dock.AnswerWhen(keptHowCall, "env", "1", boundSources)
	dock.Answer(mailEnvCall, boundEnv)
	dock.Answer(mailRawCall, boundRaw)
	dock.Answer(mainLook, twoKeys)
	dock.Answer(mailRawSave, done)
	dock.Answer(mailFileSave, done)

	return dock
}

func TestSecretPathInAnEnvFile(t *testing.T) {
	t.Parallel()

	for file, want := range map[string]string{
		"":                                 "",
		"TZ=UTC\n":                         "",
		"DOCKHAND_SECRET_SELECTOR=prod/db": "prod/db",
		"A=1\n  DOCKHAND_SECRET_SELECTOR= \"prod/db\" \nB=2\n": "prod/db",
		"DOCKHAND_SECRET_SELECTOR='a b'\n":                     "a b",
		"NOT_DOCKHAND_SECRET_SELECTOR=x\n":                     "",
	} {
		if got := secretPathIn(file); got != want {
			t.Errorf("the path in %q = %q, want %q", file, got, want)
		}
	}
	for _, tc := range []struct{ file, path, want string }{
		{"", "", ""},
		{"", "prod/db", "DOCKHAND_SECRET_SELECTOR=prod/db\n"},
		{"TZ=UTC", "prod/db", "TZ=UTC\nDOCKHAND_SECRET_SELECTOR=prod/db\n"},
		{"TZ=UTC\n\n", "", "TZ=UTC\n"},
		{"DOCKHAND_SECRET_SELECTOR=old\nTZ=UTC\n# kept\n\nA=1\n", "new", "TZ=UTC\n# kept\n\nA=1\nDOCKHAND_SECRET_SELECTOR=new\n"},
		{"DOCKHAND_SECRET_SELECTOR=old\n", "", ""},
		{"\nTZ=UTC\n", "", "TZ=UTC\n"},
	} {
		if got := withSecretPath(tc.file, tc.path); got != tc.want {
			t.Errorf("%q with the path %q = %q, want %q", tc.file, tc.path, got, tc.want)
		}
	}
}

func TestStackGetOfAStackWithASecretProvider(t *testing.T) {
	t.Parallel()

	dock := withBoundStack(t)
	suite := session(t, dock, Options{})

	// the provider by name, the path as it is and not hidden for the secret in its name, and the names the last deploy was given
	out := suite.Call(t, "stack_get", map[string]any{"stack": "mail"})
	vars := rowsNamed(acctest.Rows(t, out["variables"], "variables"), "name")
	if out["secret_provider"] != "main" || out["secret_path"] != "prod/db" || strings.Join(acctest.Strs(t, out["secret_keys"], "secret_keys"), " ") != "password user" || vars["DOCKHAND_SECRET_SELECTOR"]["value"] != "prod/db" {
		t.Errorf("a stack with a secret provider = %v", out)
	}

	// a path with no provider loads nothing, which is said; what the last deploy was given is still what it was given
	dock.Answer(mailEnvCall, strings.Replace(boundEnv, `{"type":"vault","name":"main"}`, `null`, 1))
	out = suite.Call(t, "stack_get", map[string]any{"stack": "mail"})
	if out["secret_provider"] != nil || out["secret_path"] != "prod/db" || len(acctest.Strs(t, out["secret_keys"], "secret_keys")) != 2 || !strings.Contains(acctest.Str(out["note"]), "names a path its secrets are loaded from and has no secret provider, so nothing is loaded") {
		t.Errorf("a stack with a path and no provider = %v", out)
	}
}

func TestStackValidateOfAStackWithASecretProvider(t *testing.T) {
	t.Parallel()

	const (
		held      = "POST /api/stacks/mail/env/validate"
		allGiven  = `{"valid":true,"required":["password","TZ"],"optional":[],"defined":["TZ","password","user"],"missing":[],"unused":["user"]}`
		variables = "variables"
	)
	dock := withBoundStack(t)
	dock.Answer("POST /api/stacks/mail/validate", noFindings)
	dock.Answer(held, allGiven)
	suite := session(t, dock, Options{})

	// what the provider gives at the stack's path is asked for and counted as given; the path itself is no variable of the file's
	out := suite.Call(t, "stack_validate", map[string]any{"stack": "mail"})
	if got := strings.Join(acctest.Strs(t, sent(t, dock.Last(held))[variables], variables), " "); got != "TZ password user" || dock.Last(mainLook).Body != `{"selector":"prod/db"}` || out["note"] != nil {
		t.Errorf("the variables a provider gives were checked as %q, asked with %s: %v", got, dock.Last(mainLook).Body, out)
	}

	// a provider that cannot be asked: the ones the last deploy was given stand in, and that is said
	dock.Answer(mainLook, nothingThere)
	out = suite.Call(t, "stack_validate", map[string]any{"stack": "mail"})
	if got := strings.Join(acctest.Strs(t, sent(t, dock.Last(held))[variables], variables), " "); got != "TZ password user" || !strings.Contains(acctest.Str(out["note"]), "what the stack's secret provider main gives could not be asked, so the variables its last deploy was given are counted as given") {
		t.Errorf("the variables with a provider that cannot be asked = %q: %v", got, out)
	}
	dock.Answer(providersCall, `[]`)
	if out = suite.Call(t, "stack_validate", map[string]any{"stack": "mail"}); !strings.Contains(acctest.Str(out["note"]), "the Dockhand keeps no secret provider named main") {
		t.Errorf("a provider that is no longer kept = %v", out)
	}
	dock.Fail(providersCall, http.StatusForbidden, notAllowed)
	if out = suite.Call(t, "stack_validate", map[string]any{"stack": "mail"}); !strings.Contains(acctest.Str(out["note"]), "the secret providers could not be listed") {
		t.Errorf("providers the account may not see = %v", out)
	}
}

func TestStackCreateWithASecretProvider(t *testing.T) {
	t.Parallel()

	const (
		check  = "POST /api/stacks/blog/validate"
		create = "POST /api/stacks"
	)
	dock := withProviders(t)
	dock.Answer(check, noFindings)
	dock.Answer(create, `{"success":true,"started":false}`)
	dock.Answer(mainLook, twoKeys)
	dock.Answer("GET /api/stacks/blog/compose", `{"content":"services: {}\n","stackDir":"/app/data/stacks/alpha/blog","composePath":"/app/data/stacks/alpha/blog/compose.yaml","envPath":null}`)
	then(t, dock, create, map[string]string{"blog": "created"})
	suite := session(t, dock, quick)
	args := map[string]any{"env": "alpha", "name": "blog", "compose": blogFile, "env_file": "TZ=UTC\n", "secret_provider": "main", "secret_path": " prod/db ", "start": false} //nolint:gosec // G101: the name of an argument and a made-up path, and no secret

	// refused before anything is made: a path with no provider, a provider there is none of, and a path the provider has nothing at
	for what, tc := range map[string]struct {
		more map[string]any
		want string
	}{
		"a path and no provider":   {map[string]any{"secret_provider": nil}, "secret_path is a path in a secret provider, and the stack has none: secret_provider names one, so nothing was made"},
		"a provider there is none": {map[string]any{"secret_provider": "nope"}, `no secret provider is named or numbered "nope": the Dockhand keeps Main, main, spare, so nothing was made`},
		"a provider with no name":  {map[string]any{"secret_provider": " "}, "secret_path is a path in a secret provider, and the stack has none"},
	} {
		if msg := suite.CallErr(t, "stack_create", over(args, tc.more)); !strings.Contains(msg, tc.want) {
			t.Errorf("%s = %q, want %q", what, msg, tc.want)
		}
	}
	dock.Answer(mainLook, nothingThere)
	if msg := suite.CallErr(t, "stack_create", args); !strings.Contains(msg, "the provider main gave nothing for the path prod/db") || !strings.Contains(msg, "so nothing was made") || len(dock.Requests(create)) != 0 {
		t.Errorf("a path the provider has nothing at = %q", msg)
	}
	dock.Answer(mainLook, twoKeys)

	dry := suite.Call(t, "stack_create", over(args, map[string]any{"dry_run": true}))
	if got := strings.Join(acctest.Strs(t, dry["changes"], "changes"), "|"); got != "would make the stack blog on alpha, its secrets loaded from main at prod/db" || len(dock.Requests(create)) != 0 {
		t.Errorf("a dry run = %v", dry)
	}

	// made with the provider's number, and the path as a line of the .env file after what was given
	out := suite.Call(t, "stack_create", args)
	body := sent(t, dock.Last(create))
	if got := strings.Join(acctest.Strs(t, out["changes"], "changes"), "|"); got != "made the stack blog on alpha, its secrets loaded from main at prod/db" || acctest.Num(t, body["secretProviderId"], "secretProviderId") != 1 || body["rawEnvContent"] != "TZ=UTC\nDOCKHAND_SECRET_SELECTOR=prod/db\n" {
		t.Errorf("a stack made with a secret provider = %v, sent %v", out, body)
	}

	// a provider and no path: the stack is bound and nothing is loaded until it names one
	dock.AnswerWhen("GET /api/stacks", "env", "1", alphaStacks)
	out = suite.Call(t, "stack_create", map[string]any{"env": "alpha", "name": "blog", "compose": blogFile, "secret_provider": 7, "start": false})
	body = sent(t, dock.Last(create))
	if got := strings.Join(acctest.Strs(t, out["changes"], "changes"), "|"); got != "made the stack blog on alpha, its secrets loaded from spare" || acctest.Num(t, body["secretProviderId"], "secretProviderId") != 7 || body["rawEnvContent"] != nil {
		t.Errorf("a stack made with a provider and no path = %v, sent %v", out, body)
	}
}

func TestStackEditOfItsSecretProvider(t *testing.T) {
	t.Parallel()

	changes := func(t *testing.T, out map[string]any) string {
		t.Helper()

		return strings.Join(acctest.Strs(t, out["changes"], "changes"), "|")
	}

	t.Run("refused before anything is saved", func(t *testing.T) {
		t.Parallel()

		dock := withBoundStack(t)
		suite := session(t, dock, quick)
		dock.Answer(spareLook, nothingThere)
		for what, tc := range map[string]struct {
			args map[string]any
			want string
		}{
			"a provider there is none of":      {map[string]any{"stack": "mail", "secret_provider": "nope"}, `no secret provider is named or numbered "nope": the Dockhand keeps Main, main, spare, so nothing was changed`},
			"another without the stack's path": {map[string]any{"stack": "mail", "secret_provider": "spare"}, "the provider spare gave nothing for the path prod/db"},
			"a path taken off its provider":    {map[string]any{"stack": "mail", "secret_provider": "", "secret_path": "prod/api"}, "secret_path is a path in a secret provider, and the stack has none"},
		} {
			if msg := suite.CallErr(t, "stack_edit", tc.args); !strings.Contains(msg, tc.want) {
				t.Errorf("%s = %q, want %q", what, msg, tc.want)
			}
		}
		dock.Answer(mainLook, nothingThere)
		if msg := suite.CallErr(t, "stack_edit", map[string]any{"stack": "mail", "secret_path": "nope"}); !strings.Contains(msg, "the provider main gave nothing for the path nope") || !strings.Contains(msg, "so nothing was changed") {
			t.Errorf("a path the provider has nothing at = %q", msg)
		}

		// what the stack has already is not asked about again, nor saved
		out := suite.Call(t, "stack_edit", map[string]any{"stack": "mail", "secret_provider": "main", "secret_path": "prod/db"})
		if !strings.Contains(acctest.Str(out["note"]), "nothing was changed: what was given is what the stack has already") || len(dock.Requests(mailRawSave))+len(dock.Requests(mailFileSave)) != 0 {
			t.Errorf("the provider and path a stack has already = %v", out)
		}

		// a stack whose provider the Dockhand no longer keeps cannot be given a path in it
		dock.Answer(providersCall, `[{"id":7,"type":"doppler","name":"spare"}]`)
		if msg := suite.CallErr(t, "stack_edit", map[string]any{"stack": "mail", "secret_path": "prod/api"}); !strings.Contains(msg, "the secret provider the stack has is no longer one the Dockhand keeps") {
			t.Errorf("a path in a provider that is gone = %q", msg)
		}
		dock.Fail(providersCall, http.StatusForbidden, notAllowed)
		if msg := suite.CallErr(t, "stack_edit", map[string]any{"stack": "mail", "secret_path": "prod/api"}); !strings.Contains(msg, "the secret providers could not be listed") {
			t.Errorf("providers the account may not see = %q", msg)
		}
		dock.FailWhen(keptHowCall, "env", "1", http.StatusInternalServerError, "Failed to get stack sources")
		if msg := suite.CallErr(t, "stack_edit", map[string]any{"stack": "mail", "secret_path": "prod/api"}); !strings.Contains(msg, "could not be read, so nothing was changed") {
			t.Errorf("a stack whose record cannot be read = %q", msg)
		}
		dock.AnswerWhen(keptHowCall, "env", "1", boundSources)
		dock.Fail(mailRawCall, http.StatusInternalServerError, "Failed to read env file")
		if msg := suite.CallErr(t, "stack_edit", map[string]any{"stack": "mail", "secret_provider": "main"}); !strings.Contains(msg, "the .env file of mail could not be read, so nothing was changed") {
			t.Errorf("a stack whose .env file cannot be read = %q", msg)
		}
	})

	t.Run("the path", func(t *testing.T) {
		t.Parallel()

		dock := withBoundStack(t)
		suite := session(t, dock, quick)

		// another path: asked of the provider first, then written as the one line of the .env file that changes
		dry := suite.Call(t, "stack_edit", map[string]any{"stack": "mail", "secret_path": "prod/api", "dry_run": true})
		if got := changes(t, dry); got != "would set the path its secrets are loaded from to prod/api" || len(dock.Requests(mailRawSave)) != 0 {
			t.Errorf("a dry run of another path = %v", dry)
		}
		out := suite.Call(t, "stack_edit", map[string]any{"stack": "mail", "secret_path": " prod/api "}) //nolint:gosec // G101: the name of an argument and a made-up path, and no secret
		if got := changes(t, out); got != "set the path its secrets are loaded from to prod/api" || sent(t, dock.Last(mailRawSave))["content"] != "TZ=UTC\nDOCKHAND_SECRET_SELECTOR=prod/api\n" || dock.Last(mainLook).Body != `{"selector":"prod/api"}` {
			t.Errorf("another path = %v, saved %s", out, dock.Last(mailRawSave).Body)
		}

		// a new .env file that does not name the path keeps it, and says so: losing it would deploy the stack with those variables empty
		out = suite.Call(t, "stack_edit", map[string]any{"stack": "mail", "env_file": "TZ=Europe/Paris\n"})
		if got := changes(t, out); got != "replaced the .env file" || sent(t, dock.Last(mailRawSave))["content"] != "TZ=Europe/Paris\nDOCKHAND_SECRET_SELECTOR=prod/db\n" || !strings.Contains(acctest.Str(out["note"]), "the path its secrets are loaded from, prod/db, was kept in the new .env file") {
			t.Errorf("a new .env file with no path in it = %v, saved %s", out, dock.Last(mailRawSave).Body)
		}
		// the file and the path together, and a file that says the same but for how it ends
		out = suite.Call(t, "stack_edit", map[string]any{"stack": "mail", "env_file": "TZ=Europe/Paris\n", "secret_path": "prod/api"})
		if got := changes(t, out); got != "replaced the .env file and set the path its secrets are loaded from to prod/api" || sent(t, dock.Last(mailRawSave))["content"] != "TZ=Europe/Paris\nDOCKHAND_SECRET_SELECTOR=prod/api\n" {
			t.Errorf("a new .env file and another path = %v, saved %s", out, dock.Last(mailRawSave).Body)
		}
		if out = suite.Call(t, "stack_edit", map[string]any{"stack": "mail", "env_file": "TZ=UTC\nDOCKHAND_SECRET_SELECTOR=prod/db\n\n"}); changes(t, out) != "replaced the .env file" {
			t.Errorf("a .env file that differs only in how it ends = %v", out)
		}

		// taken out: the line goes, and with it the file when that was all it held
		out = suite.Call(t, "stack_edit", map[string]any{"stack": "mail", "secret_path": ""})
		if got := changes(t, out); got != "took out the path its secrets are loaded from, prod/db" || sent(t, dock.Last(mailRawSave))["content"] != "TZ=UTC\n" {
			t.Errorf("the path taken out = %v, saved %s", out, dock.Last(mailRawSave).Body)
		}
		dock.Answer(mailRawCall, `{"content":"DOCKHAND_SECRET_SELECTOR=prod/db\n"}`)
		out = suite.Call(t, "stack_edit", map[string]any{"stack": "mail", "secret_path": ""})
		if got := changes(t, out); got != "removed the .env file and took out the path its secrets are loaded from, prod/db" || sent(t, dock.Last(mailRawSave))["content"] != "" {
			t.Errorf("the path taken out of a file that held nothing else = %v, saved %s", out, dock.Last(mailRawSave).Body)
		}
	})

	t.Run("the provider", func(t *testing.T) {
		t.Parallel()

		dock := withBoundStack(t)
		dock.Answer(spareLook, twoKeys)
		dock.Answer("POST /api/stacks/mail/validate", noFindings)
		suite := session(t, dock, quick)

		// another provider: the path the stack names is asked of it first, and the compose file goes with its number, not brought up again
		out := suite.Call(t, "stack_edit", map[string]any{"stack": "mail", "secret_provider": "spare"})
		if got := changes(t, out); got != "had its secrets loaded from the provider spare" || dock.Last(spareLook).Body != `{"selector":"prod/db"}` {
			t.Errorf("another provider = %v, asked with %s", out, dock.Last(spareLook).Body)
		}
		if body := sent(t, dock.Last(mailFileSave)); body["content"] != mailFile || acctest.Num(t, body["secretProviderId"], "secretProviderId") != 7 || !acctest.IsBool(body["restart"], false) || dock.Last(mailFileSave).Query.Get("env") != "1" {
			t.Errorf("another provider was saved with %v", body)
		}

		// taken off it: the provider is sent as nothing, which the generated call cannot, and what that means for the next deploy is said
		dry := suite.Call(t, "stack_edit", map[string]any{"stack": "mail", "secret_provider": " ", "dry_run": true})
		if got := changes(t, dry); got != "would take it off its secret provider" {
			t.Errorf("a dry run of taking a stack off its provider = %v", dry)
		}
		before := len(dock.Requests(mailFileSave))
		out = suite.Call(t, "stack_edit", map[string]any{"stack": "mail", "secret_provider": ""})
		saved := dock.Requests(mailFileSave)
		if got := changes(t, out); got != "took it off its secret provider" || len(saved) != before+1 || !strings.HasSuffix(saved[len(saved)-1].Body, `"secretProviderId":null}`) || !strings.Contains(acctest.Str(out["note"]), "with no secret provider, the variables one gave are empty the next time the stack is deployed") {
			t.Errorf("a stack taken off its provider = %v, saved %s", out, saved[len(saved)-1].Body)
		}

		// with a new compose file in the same call, the provider goes with the new file and not the one it replaces
		out = suite.Call(t, "stack_edit", map[string]any{"stack": "mail", "compose": blogFile, "secret_provider": "spare"})
		if body := sent(t, dock.Last(mailFileSave)); !strings.HasSuffix(changes(t, out), "|had its secrets loaded from the provider spare") || body["content"] != blogFile || acctest.Num(t, body["secretProviderId"], "secretProviderId") != 7 {
			t.Errorf("a new file and another provider = %v, saved %v", out, body)
		}

		// a stack that has none is given one, and its path with it
		dock.AnswerWhen(keptHowCall, "env", "1", alphaSources)
		dock.Answer(mailRawCall, `{"content":"TZ=UTC\n"}`)
		out = suite.Call(t, "stack_edit", map[string]any{"stack": "mail", "secret_provider": 1, "secret_path": "prod/db"})
		if got := changes(t, out); got != "had its secrets loaded from the provider main|set the path its secrets are loaded from to prod/db" || sent(t, dock.Last(mailRawSave))["content"] != "TZ=UTC\nDOCKHAND_SECRET_SELECTOR=prod/db\n" {
			t.Errorf("a stack given a provider and a path = %v", out)
		}
		// and taking one off a stack that has none is nothing to do
		if out = suite.Call(t, "stack_edit", map[string]any{"stack": "mail", "secret_provider": ""}); !strings.Contains(acctest.Str(out["note"]), "nothing was changed") {
			t.Errorf("a stack with no provider taken off it = %v", out)
		}
	})

	t.Run("not saved", func(t *testing.T) {
		t.Parallel()

		dock := withBoundStack(t)
		dock.Answer(spareLook, twoKeys)
		suite := session(t, dock, quick)

		dock.Fail(mailFileSave, http.StatusInternalServerError, "FOREIGN KEY constraint failed")
		if msg := suite.CallErr(t, "stack_edit", map[string]any{"stack": "mail", "secret_provider": "spare"}); !strings.Contains(msg, "mail was not changed: its secret provider could not be saved") {
			t.Errorf("a provider Dockhand would not save = %q", msg)
		}
		if msg := suite.CallErr(t, "stack_edit", map[string]any{"stack": "mail", "secret_provider": ""}); !strings.Contains(msg, "mail was not changed: it could not be taken off its secret provider") {
			t.Errorf("a stack Dockhand would not take off its provider = %q", msg)
		}
		dock.Answer(mailFileSave, `{"success":false,"error":"Failed to save compose file"}`)
		if msg := suite.CallErr(t, "stack_edit", map[string]any{"stack": "mail", "secret_provider": "spare"}); !strings.Contains(msg, "its secret provider could not be saved: Dockhand says Failed to save compose file") {
			t.Errorf("a save answered as failed = %q", msg)
		}
	})
}

func TestStackDeployOfAStackWhoseProviderCannotBeReached(t *testing.T) {
	t.Parallel()

	const call = "POST /api/stacks/mail/deploy"

	// Dockhand says only that the deploy failed: that the stack's secrets come from a provider is what a caller needs to hear
	dock := withBoundStack(t)
	dock.Answer(call, `{"success":false,"error":"Failed to deploy compose stack"}`)
	msg := session(t, dock, quick).CallErr(t, "stack_deploy", map[string]any{"stack": "mail"})
	for _, want := range []string{"mail on alpha was not deployed: Failed to deploy compose stack", "what was running is left as it was", "its secrets are loaded from the secret provider main, and a stack is not deployed while its provider cannot be reached or has nothing at the path the stack names"} {
		if !strings.Contains(msg, want) {
			t.Errorf("a deploy that failed for a stack with a provider = %q, want it to say %q", msg, want)
		}
	}
}
