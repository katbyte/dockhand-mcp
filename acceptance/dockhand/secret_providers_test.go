//go:build acceptance

package dockhand_test

import (
	"net/http"
	"slices"
	"strings"
	"testing"

	"github.com/katbyte/dockhand-mcp/lib/testenv"
	"github.com/katbyte/dockhand-mcp/sdk/dockhand"
	"github.com/katbyte/pandorest/client"
)

// What the suites keep in the secret store: made-up values under a path of their own.
const (
	secretPath     = "it/db"
	secretPassword = "it-not-a-real-password" //nolint:gosec // G101: a made-up value for the throwaway server, which is no credential of anything real
	secretUser     = "it-app"
)

// kept puts the suites' secrets in the secret store for the length of a test, at a path of the test's own under the store's one mount, and returns
// the path as a provider names it.
func kept(t *testing.T) string {
	t.Helper()

	if _, err := world.Secrets(t.Context(), "kv", "put", "secret/"+secretPath, "password="+secretPassword, "user="+secretUser); err != nil {
		t.Fatalf("putting the test's secrets in the store: %v", err)
	}
	ctx := noCancel(t)
	t.Cleanup(func() {
		if _, err := world.Secrets(ctx, "kv", "metadata", "delete", "secret/"+secretPath); err != nil {
			t.Errorf("taking the test's secrets out of the store: %v", err)
		}
	})

	return secretPath
}

// providersGone removes every secret provider a test of this file made, before it starts in case a run cut short left one and when it ends.
func providersGone(t *testing.T, names ...string) {
	t.Helper()

	c := dock(t)
	ctx := noCancel(t)
	clean := func() {
		listed, err := c.GetSecretProviders(ctx)
		if err != nil {
			t.Errorf("listing the secret providers to tidy up: %v", err)

			return
		}
		for i := range listed.Answer {
			if slices.Contains(names, listed.Answer[i].Name) {
				_, _ = c.DeleteSecretProvidersById(ctx, listed.Answer[i].Id)
			}
		}
		_, _ = c.PutSecretProvidersDefault(ctx, dockhand.PutSecretProvidersDefaultRequest{})
	}
	clean()
	t.Cleanup(clean)
}

// A secret provider is tried before it is kept, then saved, read, tried again, asked what it holds, changed, made the default and removed: what each
// call answers and refuses, that its token is never given back, that one Dockhand cannot reach is saved all the same, and that a token is kept and
// sent on when the address it was given for changes.
func TestSecretProvidersTriedSavedChangedAndRemoved(t *testing.T) {
	const name, renamed, unreachable, nobody = "it-secret-provider", "it-secret-provider-renamed", "it-secret-provider-unreachable", 987654
	c := dock(t)
	ctx := t.Context()
	providersGone(t, name, renamed, unreachable, strings.ToUpper(name))
	path := kept(t)
	good := map[string]any{"address": world.Vault, "token": world.VaultToken}

	// tried before it is saved: every answer is a 200 that says whether it worked, and why not
	for what, tc := range map[string]struct {
		kind   string
		config map[string]any
		want   string
	}{
		"a kind there is none of":       {"it-nothing", map[string]any{}, "A valid provider type is required"},
		"a kind in another case":        {"Vault", good, "A valid provider type is required"},
		"a Vault with no address":       {"vault", map[string]any{}, "Address is empty"},
		"a Vault with no token":         {"vault", map[string]any{"address": world.Vault}, "Token is empty"},
		"a token it does not take":      {"vault", map[string]any{"address": world.Vault, "token": "it-wrong"}, "403"},
		"a mount it does not have":      {"vault", map[string]any{"address": world.Vault, "token": world.VaultToken, "mount": "it-nope"}, "404"},
		"an address nothing answers at": {"vault", map[string]any{"address": "http://nowhere.invalid:8200", "token": "it-wrong"}, "nowhere.invalid"},
		"the other kinds, with nothing": {"infisical", map[string]any{}, "Host is empty"},
		"1Password by its server":       {"op-connect", map[string]any{}, "Host is empty"},
		"1Password by its account":      {"op-service-account", map[string]any{}, "Token is empty"},
		"Doppler":                       {"doppler", map[string]any{}, "Token is empty"},
		"Bitwarden":                     {"bitwarden", map[string]any{}, "token is empty"},
		"KeePass":                       {"keepass", map[string]any{}, "database path is empty"},
		"Azure":                         {"azure-kv", map[string]any{}, "Azure Key Vault"},
	} {
		tried, err := c.PostSecretProvidersTest(ctx, dockhand.PostSecretProvidersTestRequest{Type: dockhand.SecretProviderType(tc.kind), Config: tc.config})
		if err != nil || tried.Answer.Ok || !strings.Contains(tried.Answer.Error, tc.want) {
			t.Errorf("%s, tried = %+v, %v, want it refused with %q", what, tried.Answer, err, tc.want)
		}
	}
	if tried, err := c.PostSecretProvidersTest(ctx, dockhand.PostSecretProvidersTestRequest{Type: "vault", Config: good}); err != nil || !tried.Answer.Ok || tried.Answer.Error != "" {
		t.Fatalf("the secret store, tried = %+v, %v", tried.Answer, err)
	}

	// saved: answered with its number and when, and never with what it was saved with
	made, err := c.PostSecretProviders(ctx, dockhand.PostSecretProvidersRequest{Name: " " + name + " ", Type: "vault", Config: good})
	if err != nil || made.Answer.Id == 0 || made.Answer.Name != name || made.Answer.Type != "vault" || made.Answer.CreatedAt.IsZero() || made.Answer.UpdatedAt.IsZero() {
		t.Fatalf("a provider saved = %+v, %v", made.Answer, err)
	}
	id := made.Answer.Id
	for what, request := range map[string]dockhand.PostSecretProvidersRequest{
		"the same name again":     {Name: name, Type: "vault", Config: good},
		"no name":                 {Name: " ", Type: "vault", Config: good},
		"a kind there is none of": {Name: name + "-x", Type: "it-nothing", Config: good},
	} {
		if _, err := c.PostSecretProviders(ctx, request); client.StatusCode(err) != http.StatusBadRequest {
			t.Errorf("%s = %v, want a 400", what, err)
		}
	}
	// a name is another's only letter for letter, and one Dockhand cannot reach is saved without a word
	for _, other := range []dockhand.PostSecretProvidersRequest{{Name: strings.ToUpper(name), Type: "vault", Config: good}, {Name: unreachable, Type: "vault", Config: map[string]any{"address": "http://nowhere.invalid:8200", "token": "it-wrong"}}} {
		saved, refused := c.PostSecretProviders(ctx, other)
		if refused != nil {
			t.Errorf("a provider named %s = %v, want it saved", other.Name, refused)

			continue
		}
		if _, err := c.DeleteSecretProvidersById(ctx, saved.Answer.Id); err != nil {
			t.Errorf("removing %s: %v", other.Name, err)
		}
	}

	listed, err := c.GetSecretProviders(ctx)
	isIt := func(p dockhand.GetSecretProvidersAnswer) bool {
		return p.Id == id && p.Name == name && p.Type == "vault" && !p.CreatedAt.IsZero()
	}
	if err != nil || !slices.ContainsFunc(listed.Answer, isIt) {
		t.Errorf("the providers listed = %+v, %v", listed.Answer, err)
	}
	one, err := c.GetSecretProvidersById(ctx, id)
	if err != nil || one.Answer.Name != name || one.Answer.Config["address"] != world.Vault || one.Answer.Config["token"] != nil || len(one.Answer.Config) != 1 || len(one.Answer.StacksUsing) != 0 {
		t.Fatalf("a provider read = %+v, %v, want its address and no token", one.Answer, err)
	}

	// tried as it is saved, and with other settings in place of the saved ones: an address that changed is not tried with the saved token
	if tried, err := c.PostSecretProvidersByIdTest(ctx, id, dockhand.PostSecretProvidersByIdTestRequest{}); err != nil || !tried.Answer.Ok {
		t.Errorf("a saved provider tried = %+v, %v", tried.Answer, err)
	}
	if tried, err := c.PostSecretProvidersByIdTest(ctx, id, dockhand.PostSecretProvidersByIdTestRequest{Config: map[string]any{"address": world.Vault, "mount": "it-nope"}}); err != nil || tried.Answer.Ok || !strings.Contains(tried.Answer.Error, "404") {
		t.Errorf("a saved provider tried with a mount there is none of = %+v, %v, want the saved token used and the mount refused", tried.Answer, err)
	}
	if tried, err := c.PostSecretProvidersByIdTest(ctx, id, dockhand.PostSecretProvidersByIdTestRequest{Config: map[string]any{"address": world.VaultByName}}); err != nil || tried.Answer.Ok || !strings.Contains(tried.Answer.Error, "enter its credential") {
		t.Errorf("a saved provider tried at another address = %+v, %v, want the saved token withheld", tried.Answer, err)
	}

	// asked what it holds at a path: the names, never the values
	found, err := c.PostSecretProvidersByIdProbe(ctx, id, dockhand.PostSecretProvidersByIdProbeRequest{Selector: path})
	if err != nil || !found.Answer.Ok || !slices.Contains(found.Answer.BulkKeys, "password") || !slices.Contains(found.Answer.BulkKeys, "user") || len(found.Answer.BulkKeys) != 2 {
		t.Errorf("what the provider holds at %s = %+v, %v", path, found.Answer, err)
	}
	if none, err := c.PostSecretProvidersByIdProbe(ctx, id, dockhand.PostSecretProvidersByIdProbeRequest{Selector: "it/nothing-here"}); err != nil || none.Answer.Ok || !strings.Contains(none.Answer.Error, "path not found") {
		t.Errorf("a path the provider has nothing at = %+v, %v", none.Answer, err)
	}
	if bare, err := c.PostSecretProvidersByIdProbe(ctx, id, dockhand.PostSecretProvidersByIdProbeRequest{}); err != nil || !bare.Answer.Ok || len(bare.Answer.BulkKeys) != 0 {
		t.Errorf("a provider asked with no path = %+v, %v", bare.Answer, err)
	}

	// changed: a name alone; settings whole, with the token kept when none is given, whatever address they name
	if changed, err := c.PutSecretProvidersById(ctx, id, dockhand.PutSecretProvidersByIdRequest{Name: renamed}); err != nil || changed.Answer.Name != renamed || changed.Answer.Type != "vault" {
		t.Fatalf("a provider renamed = %+v, %v", changed.Answer, err)
	}
	if _, err := c.PutSecretProvidersById(ctx, id, dockhand.PutSecretProvidersByIdRequest{Config: map[string]any{"address": world.Vault, "mount": "secret", "it-other": "kept"}}); err != nil {
		t.Fatal(err)
	}
	if after, err := c.GetSecretProvidersById(ctx, id); err != nil || after.Answer.Config["mount"] != "secret" || after.Answer.Config["it-other"] != "kept" || after.Answer.Name != renamed {
		t.Errorf("a provider given other settings = %+v, %v, want them all kept", after.Answer, err)
	}
	if tried, err := c.PostSecretProvidersByIdTest(ctx, id, dockhand.PostSecretProvidersByIdTestRequest{}); err != nil || !tried.Answer.Ok {
		t.Errorf("a provider whose settings were saved with no token = %+v, %v, want the token it had kept", tried.Answer, err)
	}
	// the same store by another address, with no token given: Dockhand saves it, keeps the token, and signs in to the new address with it
	if _, err := c.PutSecretProvidersById(ctx, id, dockhand.PutSecretProvidersByIdRequest{Config: map[string]any{"address": world.VaultByName}}); err != nil {
		t.Fatal(err)
	}
	if after, err := c.GetSecretProvidersById(ctx, id); err != nil || after.Answer.Config["address"] != world.VaultByName || len(after.Answer.Config) != 1 {
		t.Errorf("a provider moved = %+v, %v, want the settings replaced whole", after.Answer, err)
	}
	if tried, err := c.PostSecretProvidersByIdTest(ctx, id, dockhand.PostSecretProvidersByIdTestRequest{}); err != nil || !tried.Answer.Ok {
		t.Errorf("a provider moved with no token given = %+v, %v, want the saved token sent to the new address", tried.Answer, err)
	}
	// its kind alone, to one its settings are none of, is saved; a kind there is none of, and no name, are not
	if retyped, err := c.PutSecretProvidersById(ctx, id, dockhand.PutSecretProvidersByIdRequest{Type: "doppler"}); err != nil || retyped.Answer.Type != "doppler" {
		t.Errorf("a provider given another kind = %+v, %v", retyped.Answer, err)
	}
	if _, err := c.PutSecretProvidersById(ctx, id, dockhand.PutSecretProvidersByIdRequest{Type: "it-nothing"}); client.StatusCode(err) != http.StatusBadRequest {
		t.Errorf("a provider given a kind there is none of = %v, want a 400", err)
	}
	if _, err := c.PutSecretProvidersById(ctx, id, dockhand.PutSecretProvidersByIdRequest{Type: "vault", Config: good}); err != nil {
		t.Fatal(err)
	}
	if same, err := c.PutSecretProvidersById(ctx, id, dockhand.PutSecretProvidersByIdRequest{}); err != nil || same.Answer.Name != renamed {
		t.Errorf("a provider changed with nothing = %+v, %v, want it as it was", same.Answer, err)
	}

	// the default: none as a server comes, one that is kept, none again, and never one there is none of
	if standard, err := c.GetSecretProvidersDefault(ctx); err != nil || standard.Answer.ProviderId != 0 {
		t.Errorf("the default with none set = %+v, %v", standard.Answer, err)
	}
	if set, err := c.PutSecretProvidersDefault(ctx, dockhand.PutSecretProvidersDefaultRequest{ProviderId: id}); err != nil || set.Answer.ProviderId != id {
		t.Errorf("the default set = %+v, %v", set.Answer, err)
	}
	if _, err := c.PutSecretProvidersDefault(ctx, dockhand.PutSecretProvidersDefaultRequest{ProviderId: nobody}); client.StatusCode(err) != http.StatusBadRequest {
		t.Errorf("the default set to a provider there is none of = %v, want a 400", err)
	}
	if standard, err := c.GetSecretProvidersDefault(ctx); err != nil || standard.Answer.ProviderId != id {
		t.Errorf("the default read back = %+v, %v", standard.Answer, err)
	}

	// one there is none of is a 404 whatever is asked of it
	if _, err := c.GetSecretProvidersById(ctx, nobody); !client.IsNotFound(err) {
		t.Errorf("reading a provider there is none of = %v", err)
	}
	if _, err := c.PutSecretProvidersById(ctx, nobody, dockhand.PutSecretProvidersByIdRequest{Name: "x"}); !client.IsNotFound(err) {
		t.Errorf("changing a provider there is none of = %v", err)
	}
	if _, err := c.PostSecretProvidersByIdTest(ctx, nobody, dockhand.PostSecretProvidersByIdTestRequest{}); !client.IsNotFound(err) {
		t.Errorf("trying a provider there is none of = %v", err)
	}
	if _, err := c.PostSecretProvidersByIdProbe(ctx, nobody, dockhand.PostSecretProvidersByIdProbeRequest{}); !client.IsNotFound(err) {
		t.Errorf("asking a provider there is none of what it holds = %v", err)
	}
	if _, err := c.DeleteSecretProvidersById(ctx, nobody); !client.IsNotFound(err) {
		t.Errorf("removing a provider there is none of = %v", err)
	}

	// removed, and the default with it
	if removed, err := c.DeleteSecretProvidersById(ctx, id); err != nil || !removed.Answer.Success {
		t.Fatalf("a provider removed = %+v, %v", removed.Answer, err)
	}
	if standard, err := c.GetSecretProvidersDefault(ctx); err != nil || standard.Answer.ProviderId != 0 {
		t.Errorf("the default once its provider is removed = %+v, %v, want none", standard.Answer, err)
	}
	if _, err := c.GetSecretProvidersById(ctx, id); !client.IsNotFound(err) {
		t.Errorf("a provider read after it was removed = %v", err)
	}
}

// A stack takes its secrets from a provider: bound when it is made and by a later save, with the path in its .env file, it is given each key there as
// a variable when it is deployed; it is not deployed while the provider cannot be reached; and taken off the provider, or left with the provider
// removed from under it, it is deployed with those variables empty and no word said.
func TestAStackTakesItsSecretsFromAProvider(t *testing.T) {
	const name, host = "it-secret-provider-for-a-stack", testenv.Alpha
	c := dock(t)
	ctx := t.Context()
	env := world.Env(host)
	at := dockhand.PutStacksByNameComposeOptions{Env: env}
	stack, plain := named(t, "stack"), named(t, "plain")
	container := stack + "-app-1"
	file := "services:\n  app:\n    image: " + world.Image + "\n    command: [\"sleep\", \"100000\"]\n    stop_grace_period: 1s\n    environment:\n      DB_PASSWORD: ${password}\n      DB_USER: ${user}\n"
	providersGone(t, name)
	forgotten(t, host, stack)
	forgotten(t, host, plain)
	path := kept(t)

	made, err := c.PostSecretProviders(ctx, dockhand.PostSecretProvidersRequest{Name: name, Type: "vault", Config: map[string]any{"address": world.Vault, "token": world.VaultToken}})
	if err != nil {
		t.Fatal(err)
	}
	id := made.Answer.Id
	// boundTo is the provider the stack is kept as bound to, 0 for none, and given is what its container was given for the two variables
	boundTo := func() int {
		sources, unread := c.GetStacksSources(ctx, dockhand.GetStacksSourcesOptions{Env: env})
		if unread != nil {
			t.Fatal(unread)
		}

		return int(sources.Answer[stack].SecretProviderId)
	}
	given := func() (password, user string) {
		for _, variable := range givenTo(t, host, container) {
			if value, is := strings.CutPrefix(variable, "DB_PASSWORD="); is {
				password = value
			}
			if value, is := strings.CutPrefix(variable, "DB_USER="); is {
				user = value
			}
		}

		return password, user
	}
	deploy := func() *dockhand.PostStacksByNameDeployAnswer {
		deployed, failed := c.PostStacksByNameDeploy(ctx, stack, dockhand.PostStacksByNameDeployRequest{}, dockhand.PostStacksByNameDeployOptions{Env: env})
		if failed != nil {
			t.Fatalf("deploying %s: %v", stack, failed)
		}

		return deployed.Answer
	}

	// made bound to the provider, the path a line of its .env file: the provider says the stack uses it, and nothing is loaded until a deploy
	if created, err := c.PostStacks(ctx, dockhand.PostStacksRequest{Name: stack, Compose: file, RawEnvContent: "DOCKHAND_SECRET_SELECTOR=" + path + "\n", SecretProviderId: id, Start: new(false)}, dockhand.PostStacksOptions{Env: env}); err != nil || !created.Answer.Success {
		t.Fatalf("a stack made with a secret provider = %+v, %v", created.Answer, err)
	}
	if boundTo() != int(id) {
		t.Errorf("the stack is kept as bound to %d, want %d", boundTo(), id)
	}
	vars, err := c.GetStacksByNameEnv(ctx, stack, dockhand.GetStacksByNameEnvOptions{Env: env})
	if err != nil || vars.Answer.SecretProvider == nil || vars.Answer.SecretProvider.Name != name || vars.Answer.SecretProvider.Type != "vault" || len(vars.Answer.InjectedSecretKeys) != 0 {
		t.Errorf("the stack's variables before a deploy = %+v, %v, want the provider named and nothing loaded yet", vars.Answer, err)
	}
	if used, err := c.GetSecretProvidersById(ctx, id); err != nil || len(used.Answer.StacksUsing) != 1 || used.Answer.StacksUsing[0].StackName != stack || used.Answer.StacksUsing[0].EnvironmentId != env {
		t.Errorf("the stacks the provider says use it = %+v, %v", used.Answer.StacksUsing, err)
	}
	// Dockhand's own check of the variables knows nothing of what the provider gives
	if uses, err := c.PostStacksByNameEnvValidate(ctx, stack, dockhand.PostStacksByNameEnvValidateRequest{}, dockhand.PostStacksByNameEnvValidateOptions{Env: env}); err != nil || !slices.Contains(uses.Answer.Missing, "password") {
		t.Errorf("the variables Dockhand counts as missing = %+v, %v, want the provider's among them", uses.Answer, err)
	}

	// deployed: the container is given what the store holds, and the stack says which names it was given
	if up := deploy(); !up.Success {
		t.Fatalf("a deploy with the provider reachable = %+v", up)
	}
	if password, user := given(); password != secretPassword || user != secretUser {
		t.Errorf("what the container was given = a password of %d letters and the user %q, want the ones in the store", len(password), user)
	}
	if vars, err = c.GetStacksByNameEnv(ctx, stack, dockhand.GetStacksByNameEnvOptions{Env: env}); err != nil || !slices.Contains(vars.Answer.InjectedSecretKeys, "password") || !slices.Contains(vars.Answer.InjectedSecretKeys, "user") {
		t.Errorf("the names the deploy was given = %+v, %v", vars.Answer.InjectedSecretKeys, err)
	}

	// the default is what the web interface offers and no more: a stack made with no provider named has none
	if _, err := c.PutSecretProvidersDefault(ctx, dockhand.PutSecretProvidersDefaultRequest{ProviderId: id}); err != nil {
		t.Fatal(err)
	}
	if _, err := c.PostStacks(ctx, dockhand.PostStacksRequest{Name: plain, Compose: file, Start: new(false)}, dockhand.PostStacksOptions{Env: env}); err != nil {
		t.Fatal(err)
	}
	if sources, err := c.GetStacksSources(ctx, dockhand.GetStacksSourcesOptions{Env: env}); err != nil || sources.Answer[plain].SecretProviderId != 0 {
		t.Errorf("a stack made with no provider named, while there is a default = %+v, %v, want none", sources.Answer[plain], err)
	}

	// a provider that cannot be reached: the deploy fails and the container is left as it was
	if _, err := c.PutSecretProvidersById(ctx, id, dockhand.PutSecretProvidersByIdRequest{Config: map[string]any{"address": "http://nowhere.invalid:8200", "token": world.VaultToken}}); err != nil {
		t.Fatal(err)
	}
	if down := deploy(); down.Success || !strings.Contains(down.Error, "Failed to deploy") {
		t.Errorf("a deploy with the provider out of reach = %+v, want it refused", down)
	}
	if password, _ := given(); password != secretPassword {
		t.Error("the container was changed by a deploy that failed")
	}
	if _, err := c.PutSecretProvidersById(ctx, id, dockhand.PutSecretProvidersByIdRequest{Config: map[string]any{"address": world.Vault, "token": world.VaultToken}}); err != nil {
		t.Fatal(err)
	}

	// a save that names no provider keeps the one there is; one there is none of is a 500; a null takes the stack off it
	if _, err := c.PutStacksByNameCompose(ctx, stack, dockhand.PutStacksByNameComposeRequest{Content: file}, at); err != nil || boundTo() != int(id) {
		t.Errorf("a save with no provider named = %v, bound to %d, want %d kept", err, boundTo(), id)
	}
	if _, err := c.PutStacksByNameCompose(ctx, stack, dockhand.PutStacksByNameComposeRequest{Content: file, SecretProviderId: 987654}, at); client.StatusCode(err) != http.StatusInternalServerError || !strings.Contains(err.Error(), "FOREIGN KEY") {
		t.Errorf("a save naming a provider there is none of = %v, want a 500", err)
	}
	if _, err := c.ForgetStackSecretProvider(ctx, stack, file, int(env)); err != nil || boundTo() != 0 {
		t.Errorf("a stack taken off its provider = %v, bound to %d", err, boundTo())
	}
	if vars, err = c.GetStacksByNameEnv(ctx, stack, dockhand.GetStacksByNameEnvOptions{Env: env}); err != nil || vars.Answer.SecretProvider != nil {
		t.Errorf("the variables of a stack taken off its provider = %+v, %v, want no provider", vars.Answer, err)
	}
	// with no provider and the path still named, a deploy succeeds and the variables are empty
	if up := deploy(); !up.Success {
		t.Fatalf("a deploy with no provider = %+v", up)
	}
	if password, user := given(); password != "" || user != "" {
		t.Errorf("what the container was given with no provider = a password of %d letters and the user %q, want both empty", len(password), user)
	}

	// bound again by a save, and the provider removed from under it: Dockhand removes it, takes the stack off it, and says nothing
	if _, err := c.PutStacksByNameCompose(ctx, stack, dockhand.PutStacksByNameComposeRequest{Content: file, SecretProviderId: id}, at); err != nil || boundTo() != int(id) {
		t.Fatalf("a stack bound again = %v, bound to %d", err, boundTo())
	}
	if up := deploy(); !up.Success {
		t.Fatalf("a deploy once bound again = %+v", up)
	}
	if password, _ := given(); password != secretPassword {
		t.Error("the container was not given its password once the stack was bound again")
	}
	if removed, err := c.DeleteSecretProvidersById(ctx, id); err != nil || !removed.Answer.Success {
		t.Fatalf("removing a provider a stack uses = %+v, %v, want it removed all the same", removed.Answer, err)
	}
	if boundTo() != 0 {
		t.Errorf("the stack is still bound to %d once its provider is removed", boundTo())
	}
	if standard, err := c.GetSecretProvidersDefault(ctx); err != nil || standard.Answer.ProviderId != 0 {
		t.Errorf("the default once its provider is removed = %+v, %v", standard.Answer, err)
	}
	if up := deploy(); !up.Success {
		t.Fatalf("a deploy once the provider is removed = %+v", up)
	}
	if password, _ := given(); password != "" {
		t.Error("the container still has its password after a deploy with the provider removed, want it empty")
	}
}
