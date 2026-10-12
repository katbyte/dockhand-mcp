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
		for i := range listed.Model {
			if slices.Contains(names, listed.Model[i].Name) {
				_, _ = c.DeleteSecretProvidersById(ctx, listed.Model[i].Id)
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
		tried, err := c.PostSecretProvidersTest(ctx, dockhand.PostSecretProvidersTestRequest{Type: tc.kind, Config: tc.config})
		if err != nil || yes(tried.Model.Ok) || !strings.Contains(tried.Model.Error, tc.want) {
			t.Errorf("%s, tried = %+v, %v, want it refused with %q", what, tried.Model, err, tc.want)
		}
	}
	if tried, err := c.PostSecretProvidersTest(ctx, dockhand.PostSecretProvidersTestRequest{Type: "vault", Config: good}); err != nil || !yes(tried.Model.Ok) || tried.Model.Error != "" {
		t.Fatalf("the secret store, tried = %+v, %v", tried.Model, err)
	}

	// saved: answered with its number and when, and never with what it was saved with
	made, err := c.PostSecretProviders(ctx, dockhand.PostSecretProvidersRequest{Name: " " + name + " ", Type: "vault", Config: good})
	if err != nil || made.Model.Id == 0 || made.Model.Name != name || made.Model.Type != "vault" || made.Model.CreatedAt == "" || made.Model.UpdatedAt == "" {
		t.Fatalf("a provider saved = %+v, %v", made.Model, err)
	}
	id := made.Model.Id
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
		if _, err := c.DeleteSecretProvidersById(ctx, saved.Model.Id); err != nil {
			t.Errorf("removing %s: %v", other.Name, err)
		}
	}

	listed, err := c.GetSecretProviders(ctx)
	isIt := func(p dockhand.GetSecretProvidersResponse) bool {
		return p.Id == id && p.Name == name && p.Type == "vault" && p.CreatedAt != ""
	}
	if err != nil || !slices.ContainsFunc(listed.Model, isIt) {
		t.Errorf("the providers listed = %+v, %v", listed.Model, err)
	}
	one, err := c.GetSecretProvidersById(ctx, id)
	if err != nil || one.Model.Name != name || one.Model.Config["address"] != world.Vault || one.Model.Config["token"] != nil || len(one.Model.Config) != 1 || len(one.Model.StacksUsing) != 0 {
		t.Fatalf("a provider read = %+v, %v, want its address and no token", one.Model, err)
	}

	// tried as it is saved, and with other settings in place of the saved ones: an address that changed is not tried with the saved token
	if tried, err := c.PostSecretProvidersByIdTest(ctx, id, dockhand.PostSecretProvidersByIdTestRequest{}); err != nil || !yes(tried.Model.Ok) {
		t.Errorf("a saved provider tried = %+v, %v", tried.Model, err)
	}
	if tried, err := c.PostSecretProvidersByIdTest(ctx, id, dockhand.PostSecretProvidersByIdTestRequest{Config: map[string]any{"address": world.Vault, "mount": "it-nope"}}); err != nil || yes(tried.Model.Ok) || !strings.Contains(tried.Model.Error, "404") {
		t.Errorf("a saved provider tried with a mount there is none of = %+v, %v, want the saved token used and the mount refused", tried.Model, err)
	}
	if tried, err := c.PostSecretProvidersByIdTest(ctx, id, dockhand.PostSecretProvidersByIdTestRequest{Config: map[string]any{"address": world.VaultByName}}); err != nil || yes(tried.Model.Ok) || !strings.Contains(tried.Model.Error, "enter its credential") {
		t.Errorf("a saved provider tried at another address = %+v, %v, want the saved token withheld", tried.Model, err)
	}

	// asked what it holds at a path: the names, never the values
	found, err := c.PostSecretProvidersByIdProbe(ctx, id, dockhand.PostSecretProvidersByIdProbeRequest{Selector: path})
	if err != nil || !yes(found.Model.Ok) || !slices.Contains(found.Model.BulkKeys, "password") || !slices.Contains(found.Model.BulkKeys, "user") || len(found.Model.BulkKeys) != 2 {
		t.Errorf("what the provider holds at %s = %+v, %v", path, found.Model, err)
	}
	if none, err := c.PostSecretProvidersByIdProbe(ctx, id, dockhand.PostSecretProvidersByIdProbeRequest{Selector: "it/nothing-here"}); err != nil || yes(none.Model.Ok) || !strings.Contains(none.Model.Error, "path not found") {
		t.Errorf("a path the provider has nothing at = %+v, %v", none.Model, err)
	}
	if bare, err := c.PostSecretProvidersByIdProbe(ctx, id, dockhand.PostSecretProvidersByIdProbeRequest{}); err != nil || !yes(bare.Model.Ok) || len(bare.Model.BulkKeys) != 0 {
		t.Errorf("a provider asked with no path = %+v, %v", bare.Model, err)
	}

	// changed: a name alone; settings whole, with the token kept when none is given, whatever address they name
	if changed, err := c.PutSecretProvidersById(ctx, id, dockhand.PutSecretProvidersByIdRequest{Name: renamed}); err != nil || changed.Model.Name != renamed || changed.Model.Type != "vault" {
		t.Fatalf("a provider renamed = %+v, %v", changed.Model, err)
	}
	if _, err := c.PutSecretProvidersById(ctx, id, dockhand.PutSecretProvidersByIdRequest{Config: map[string]any{"address": world.Vault, "mount": "secret", "it-other": "kept"}}); err != nil {
		t.Fatal(err)
	}
	if after, err := c.GetSecretProvidersById(ctx, id); err != nil || after.Model.Config["mount"] != "secret" || after.Model.Config["it-other"] != "kept" || after.Model.Name != renamed {
		t.Errorf("a provider given other settings = %+v, %v, want them all kept", after.Model, err)
	}
	if tried, err := c.PostSecretProvidersByIdTest(ctx, id, dockhand.PostSecretProvidersByIdTestRequest{}); err != nil || !yes(tried.Model.Ok) {
		t.Errorf("a provider whose settings were saved with no token = %+v, %v, want the token it had kept", tried.Model, err)
	}
	// the same store by another address, with no token given: Dockhand saves it, keeps the token, and signs in to the new address with it
	if _, err := c.PutSecretProvidersById(ctx, id, dockhand.PutSecretProvidersByIdRequest{Config: map[string]any{"address": world.VaultByName}}); err != nil {
		t.Fatal(err)
	}
	if after, err := c.GetSecretProvidersById(ctx, id); err != nil || after.Model.Config["address"] != world.VaultByName || len(after.Model.Config) != 1 {
		t.Errorf("a provider moved = %+v, %v, want the settings replaced whole", after.Model, err)
	}
	if tried, err := c.PostSecretProvidersByIdTest(ctx, id, dockhand.PostSecretProvidersByIdTestRequest{}); err != nil || !yes(tried.Model.Ok) {
		t.Errorf("a provider moved with no token given = %+v, %v, want the saved token sent to the new address", tried.Model, err)
	}
	// its kind alone, to one its settings are none of, is saved; a kind there is none of, and no name, are not
	if retyped, err := c.PutSecretProvidersById(ctx, id, dockhand.PutSecretProvidersByIdRequest{Type: "doppler"}); err != nil || retyped.Model.Type != "doppler" {
		t.Errorf("a provider given another kind = %+v, %v", retyped.Model, err)
	}
	if _, err := c.PutSecretProvidersById(ctx, id, dockhand.PutSecretProvidersByIdRequest{Type: "it-nothing"}); client.StatusCode(err) != http.StatusBadRequest {
		t.Errorf("a provider given a kind there is none of = %v, want a 400", err)
	}
	if _, err := c.PutSecretProvidersById(ctx, id, dockhand.PutSecretProvidersByIdRequest{Type: "vault", Config: good}); err != nil {
		t.Fatal(err)
	}
	if same, err := c.PutSecretProvidersById(ctx, id, dockhand.PutSecretProvidersByIdRequest{}); err != nil || same.Model.Name != renamed {
		t.Errorf("a provider changed with nothing = %+v, %v, want it as it was", same.Model, err)
	}

	// the default: none as a server comes, one that is kept, none again, and never one there is none of
	if standard, err := c.GetSecretProvidersDefault(ctx); err != nil || standard.Model.ProviderId != 0 {
		t.Errorf("the default with none set = %+v, %v", standard.Model, err)
	}
	if set, err := c.PutSecretProvidersDefault(ctx, dockhand.PutSecretProvidersDefaultRequest{ProviderId: id}); err != nil || set.Model.ProviderId != id {
		t.Errorf("the default set = %+v, %v", set.Model, err)
	}
	if _, err := c.PutSecretProvidersDefault(ctx, dockhand.PutSecretProvidersDefaultRequest{ProviderId: nobody}); client.StatusCode(err) != http.StatusBadRequest {
		t.Errorf("the default set to a provider there is none of = %v, want a 400", err)
	}
	if standard, err := c.GetSecretProvidersDefault(ctx); err != nil || standard.Model.ProviderId != id {
		t.Errorf("the default read back = %+v, %v", standard.Model, err)
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
	if removed, err := c.DeleteSecretProvidersById(ctx, id); err != nil || !yes(removed.Model.Success) {
		t.Fatalf("a provider removed = %+v, %v", removed.Model, err)
	}
	if standard, err := c.GetSecretProvidersDefault(ctx); err != nil || standard.Model.ProviderId != 0 {
		t.Errorf("the default once its provider is removed = %+v, %v, want none", standard.Model, err)
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
	at := dockhand.PutStacksByNameComposeOperationOptions{Env: &env}
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
	id := made.Model.Id
	// boundTo is the provider the stack is kept as bound to, 0 for none, and given is what its container was given for the two variables
	boundTo := func() int {
		sources, unread := c.GetStacksSources(ctx, dockhand.GetStacksSourcesOperationOptions{Env: &env})
		if unread != nil {
			t.Fatal(unread)
		}

		return sources.Model[stack].SecretProviderId
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
	deploy := func() *dockhand.PostStacksByNameDeployResponse {
		deployed, failed := c.PostStacksByNameDeploy(ctx, stack, dockhand.PostStacksByNameDeployRequest{}, dockhand.PostStacksByNameDeployOperationOptions{Env: &env})
		if failed != nil {
			t.Fatalf("deploying %s: %v", stack, failed)
		}

		return deployed.Model
	}

	// made bound to the provider, the path a line of its .env file: the provider says the stack uses it, and nothing is loaded until a deploy
	if created, err := c.PostStacks(ctx, dockhand.PostStacksRequest{Name: stack, Compose: file, RawEnvContent: "DOCKHAND_SECRET_SELECTOR=" + path + "\n", SecretProviderId: id, Start: new(false)}, dockhand.PostStacksOperationOptions{Env: &env}); err != nil || !yes(created.Model.Success) {
		t.Fatalf("a stack made with a secret provider = %+v, %v", created.Model, err)
	}
	if boundTo() != id {
		t.Errorf("the stack is kept as bound to %d, want %d", boundTo(), id)
	}
	vars, err := c.GetStacksByNameEnv(ctx, stack, dockhand.GetStacksByNameEnvOperationOptions{Env: &env})
	if err != nil || vars.Model.SecretProvider == nil || vars.Model.SecretProvider.Name != name || vars.Model.SecretProvider.Type != "vault" || len(vars.Model.InjectedSecretKeys) != 0 {
		t.Errorf("the stack's variables before a deploy = %+v, %v, want the provider named and nothing loaded yet", vars.Model, err)
	}
	if used, err := c.GetSecretProvidersById(ctx, id); err != nil || len(used.Model.StacksUsing) != 1 || used.Model.StacksUsing[0].StackName != stack || used.Model.StacksUsing[0].EnvironmentId != env {
		t.Errorf("the stacks the provider says use it = %+v, %v", used.Model.StacksUsing, err)
	}
	// Dockhand's own check of the variables knows nothing of what the provider gives
	if uses, err := c.PostStacksByNameEnvValidate(ctx, stack, dockhand.PostStacksByNameEnvValidateRequest{}, dockhand.PostStacksByNameEnvValidateOperationOptions{Env: &env}); err != nil || !slices.Contains(uses.Model.Missing, "password") {
		t.Errorf("the variables Dockhand counts as missing = %+v, %v, want the provider's among them", uses.Model, err)
	}

	// deployed: the container is given what the store holds, and the stack says which names it was given
	if up := deploy(); !yes(up.Success) {
		t.Fatalf("a deploy with the provider reachable = %+v", up)
	}
	if password, user := given(); password != secretPassword || user != secretUser {
		t.Errorf("what the container was given = a password of %d letters and the user %q, want the ones in the store", len(password), user)
	}
	if vars, err = c.GetStacksByNameEnv(ctx, stack, dockhand.GetStacksByNameEnvOperationOptions{Env: &env}); err != nil || !slices.Contains(vars.Model.InjectedSecretKeys, "password") || !slices.Contains(vars.Model.InjectedSecretKeys, "user") {
		t.Errorf("the names the deploy was given = %+v, %v", vars.Model.InjectedSecretKeys, err)
	}

	// the default is what the web interface offers and no more: a stack made with no provider named has none
	if _, err := c.PutSecretProvidersDefault(ctx, dockhand.PutSecretProvidersDefaultRequest{ProviderId: id}); err != nil {
		t.Fatal(err)
	}
	if _, err := c.PostStacks(ctx, dockhand.PostStacksRequest{Name: plain, Compose: file, Start: new(false)}, dockhand.PostStacksOperationOptions{Env: &env}); err != nil {
		t.Fatal(err)
	}
	if sources, err := c.GetStacksSources(ctx, dockhand.GetStacksSourcesOperationOptions{Env: &env}); err != nil || sources.Model[plain].SecretProviderId != 0 {
		t.Errorf("a stack made with no provider named, while there is a default = %+v, %v, want none", sources.Model[plain], err)
	}

	// a provider that cannot be reached: the deploy fails and the container is left as it was
	if _, err := c.PutSecretProvidersById(ctx, id, dockhand.PutSecretProvidersByIdRequest{Config: map[string]any{"address": "http://nowhere.invalid:8200", "token": world.VaultToken}}); err != nil {
		t.Fatal(err)
	}
	if down := deploy(); yes(down.Success) || !strings.Contains(down.Error, "Failed to deploy") {
		t.Errorf("a deploy with the provider out of reach = %+v, want it refused", down)
	}
	if password, _ := given(); password != secretPassword {
		t.Error("the container was changed by a deploy that failed")
	}
	if _, err := c.PutSecretProvidersById(ctx, id, dockhand.PutSecretProvidersByIdRequest{Config: map[string]any{"address": world.Vault, "token": world.VaultToken}}); err != nil {
		t.Fatal(err)
	}

	// a save that names no provider keeps the one there is; one there is none of is a 500; a null takes the stack off it
	if _, err := c.PutStacksByNameCompose(ctx, stack, dockhand.PutStacksByNameComposeRequest{Content: file}, at); err != nil || boundTo() != id {
		t.Errorf("a save with no provider named = %v, bound to %d, want %d kept", err, boundTo(), id)
	}
	if _, err := c.PutStacksByNameCompose(ctx, stack, dockhand.PutStacksByNameComposeRequest{Content: file, SecretProviderId: 987654}, at); client.StatusCode(err) != http.StatusInternalServerError || !strings.Contains(err.Error(), "FOREIGN KEY") {
		t.Errorf("a save naming a provider there is none of = %v, want a 500", err)
	}
	if _, err := c.ForgetStackSecretProvider(ctx, stack, file, env); err != nil || boundTo() != 0 {
		t.Errorf("a stack taken off its provider = %v, bound to %d", err, boundTo())
	}
	if vars, err = c.GetStacksByNameEnv(ctx, stack, dockhand.GetStacksByNameEnvOperationOptions{Env: &env}); err != nil || vars.Model.SecretProvider != nil {
		t.Errorf("the variables of a stack taken off its provider = %+v, %v, want no provider", vars.Model, err)
	}
	// with no provider and the path still named, a deploy succeeds and the variables are empty
	if up := deploy(); !yes(up.Success) {
		t.Fatalf("a deploy with no provider = %+v", up)
	}
	if password, user := given(); password != "" || user != "" {
		t.Errorf("what the container was given with no provider = a password of %d letters and the user %q, want both empty", len(password), user)
	}

	// bound again by a save, and the provider removed from under it: Dockhand removes it, takes the stack off it, and says nothing
	if _, err := c.PutStacksByNameCompose(ctx, stack, dockhand.PutStacksByNameComposeRequest{Content: file, SecretProviderId: id}, at); err != nil || boundTo() != id {
		t.Fatalf("a stack bound again = %v, bound to %d", err, boundTo())
	}
	if up := deploy(); !yes(up.Success) {
		t.Fatalf("a deploy once bound again = %+v", up)
	}
	if password, _ := given(); password != secretPassword {
		t.Error("the container was not given its password once the stack was bound again")
	}
	if removed, err := c.DeleteSecretProvidersById(ctx, id); err != nil || !yes(removed.Model.Success) {
		t.Fatalf("removing a provider a stack uses = %+v, %v, want it removed all the same", removed.Model, err)
	}
	if boundTo() != 0 {
		t.Errorf("the stack is still bound to %d once its provider is removed", boundTo())
	}
	if standard, err := c.GetSecretProvidersDefault(ctx); err != nil || standard.Model.ProviderId != 0 {
		t.Errorf("the default once its provider is removed = %+v, %v", standard.Model, err)
	}
	if up := deploy(); !yes(up.Success) {
		t.Fatalf("a deploy once the provider is removed = %+v", up)
	}
	if password, _ := given(); password != "" {
		t.Error("the container still has its password after a deploy with the provider removed, want it empty")
	}
}
