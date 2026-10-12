//go:build acceptance

package tools_test

import (
	"context"
	"slices"
	"strings"
	"testing"

	"github.com/katbyte/dockhand-mcp/lib/testenv"
	"github.com/katbyte/go-kt/mcp/acctest"
)

// A stack is given its secrets by a secret store, with the tools alone: the store is tried and saved as a provider, asked what it holds, a stack is
// made that loads a path of it and is seen to run with what is there, the provider is moved out of reach and the deploy that then fails says why, and
// the provider is removed only once the stack it would leave empty-handed has been named. No answer on the way holds the token or a secret.
func TestJourneyAStackGivenItsSecretsByAProvider(t *testing.T) {
	const (
		provider, renamed = "acc-journey-vault", "acc-journey-vault-renamed"
		stack, container  = "acc-journey-secrets", "acc-journey-secrets-app-1"
		path, stored      = "acc/db", "secret/acc/db"
		password, user    = "acc-not-a-real-password", "acc-app" //nolint:gosec // G101: a made-up value for the throwaway server, which is no credential of anything real
		host              = testenv.Alpha
	)
	if world == nil {
		t.Skip(testenv.NotConfigured)
	}
	ctx := context.WithoutCancel(t.Context())
	file := "services:\n  app:\n    image: " + world.Image + "\n    command: [\"sleep\", \"100000\"]\n    stop_grace_period: 1s\n    environment:\n      DB_PASSWORD: ${password}\n      DB_USER: ${user}\n"
	listed := func() map[string]map[string]any {
		return named(acctest.Rows(t, call(t, "secret_provider_list", nil)["providers"], "providers"), "name")
	}
	clean := func() {
		for known := range listed() {
			if strings.HasPrefix(known, "acc-journey-") {
				suite.Call(t, "secret_provider_delete", map[string]any{"provider": known, "confirm": true, "even_used": true})
			}
		}
		_, _ = world.Secrets(ctx, "kv", "metadata", "delete", stored)
	}
	forgotten(t, host, stack)
	clean()
	t.Cleanup(clean)
	if _, err := world.Secrets(ctx, "kv", "put", stored, "password="+password, "user="+user); err != nil {
		t.Fatal(err)
	}
	before := len(listed())
	// said holds every answer of the journey against the token and the secret, neither of which a tool may give
	said := func(what string, answer map[string]any) map[string]any {
		t.Helper()
		if text := flat(answer); strings.Contains(text, world.VaultToken) || strings.Contains(text, password) {
			t.Errorf("%s gave the token or a secret away: %s", what, text)
		}

		return answer
	}
	given := func() []string { return givenTo(t, host, container) }
	settings := map[string]any{"address": world.Vault, "token": world.VaultToken}

	// tried before it is saved, with a token it does not take and with its own
	if tried := said("a try with a wrong token", call(t, "secret_provider_test", map[string]any{"type": "vault", "settings": map[string]any{"address": world.Vault, "token": "acc-wrong"}})); !acctest.IsBool(tried["reached"], false) || !strings.Contains(acctest.Str(tried["error"]), "403") {
		t.Errorf("the store tried with a wrong token = %v", tried)
	}
	if tried := said("a try", call(t, "secret_provider_test", map[string]any{"type": "vault", "settings": settings})); !acctest.IsBool(tried["reached"], true) {
		t.Fatalf("the store tried = %v", tried)
	}

	// saved: not when Dockhand cannot use it, said first, and then for real, as the default
	if msg := refused(t, "secret_provider_create", map[string]any{"name": provider, "type": "vault", "settings": map[string]any{"address": "http://nowhere.invalid:8200", "token": "acc-wrong"}}); !strings.Contains(msg, "Dockhand could not use the provider, so nothing was saved") {
		t.Errorf("a provider that cannot be reached = %q", msg)
	}
	saving := map[string]any{"name": provider, "type": "vault", "settings": settings, "default": true}
	if dry := said("a dry run", call(t, "secret_provider_create", over(saving, map[string]any{"dry_run": true}))); len(acctest.Strs(t, dry["changes"], "changes")) != 2 || len(listed()) != before {
		t.Fatalf("a dry run of saving a provider = %v", dry)
	}
	made := said("saving the provider", call(t, "secret_provider_create", saving))
	row := acctest.Object(t, made["provider"], "provider")
	if row["name"] != provider || row["type"] != "vault" || !acctest.IsBool(row["default"], true) || acctest.Object(t, row["settings"], "settings")["address"] != world.Vault || acctest.Object(t, row["settings"], "settings")["token"] != nil {
		t.Fatalf("a provider saved = %v", made)
	}
	if msg := refused(t, "secret_provider_create", saving); !strings.Contains(msg, "a secret provider is already named "+provider) {
		t.Errorf("a second provider by the same name = %q", msg)
	}
	said("the providers listed", call(t, "secret_provider_list", map[string]any{"reveal_secrets": true}))

	// asked what it holds: the names at the path, and a refusal for a path it has nothing at
	if keys := said("the keys", call(t, "secret_provider_keys", map[string]any{"provider": provider, "path": path})); strings.Join(acctest.Strs(t, keys["keys"], "keys"), " ") != "password user" {
		t.Errorf("what the provider holds at %s = %v", path, keys)
	}
	if msg := refused(t, "secret_provider_keys", map[string]any{"provider": provider, "path": "acc/nothing-here"}); !strings.Contains(msg, "gave nothing for the path acc/nothing-here") {
		t.Errorf("a path the provider has nothing at = %q", msg)
	}

	// a stack made to load that path: refused for a path there is none of, then made and brought up with what the store holds
	making := map[string]any{"env": host, "name": stack, "compose": file, "env_file": "TZ=UTC\n", "secret_provider": provider, "secret_path": path, "wait": 120}
	if msg := refused(t, "stack_create", over(making, map[string]any{"secret_path": "acc/nothing-here"})); !strings.Contains(msg, "so nothing was made") || stackNamed(t, stack) != nil {
		t.Fatalf("a stack made to load a path there is none of = %q", msg)
	}
	up := said("making the stack", call(t, "stack_create", making))
	if up["status"] != "running" || !strings.Contains(strings.Join(acctest.Strs(t, up["changes"], "changes"), "|"), "its secrets loaded from "+provider+" at "+path) {
		t.Fatalf("a stack made with a secret provider = %v", up)
	}
	if got := given(); !slices.Contains(got, "DB_PASSWORD="+password) || !slices.Contains(got, "DB_USER="+user) {
		t.Errorf("the container was not given what the store holds: it has %d variables", len(got))
	}
	described := said("the stack described", call(t, "stack_get", map[string]any{"env": host, "stack": stack, "reveal_secrets": true}))
	if described["secret_provider"] != provider || described["secret_path"] != path || strings.Join(acctest.Strs(t, described["secret_keys"], "secret_keys"), " ") != "password user" {
		t.Errorf("the stack described = %v", described)
	}
	// the variables the provider gives are counted as given, which Dockhand's own check does not know to do
	checked := said("the stack checked", call(t, "stack_validate", map[string]any{"env": host, "stack": stack}))
	if vars := acctest.Object(t, checked["variables"], "variables"); len(acctest.Strs(t, vars["missing"], "missing")) != 0 || slices.Contains(acctest.Strs(t, vars["unused"], "unused"), "DOCKHAND_SECRET_SELECTOR") {
		t.Errorf("the variables of a stack with a provider = %v", vars)
	}
	if uses := listed()[provider]; len(acctest.Rows(t, uses["stacks"], "stacks")) != 1 {
		t.Errorf("the stacks the provider says use it = %v", uses)
	}

	// changed: renamed, and never moved without its token, which Dockhand would send to the new address
	if msg := refused(t, "secret_provider_edit", map[string]any{"provider": provider, "settings": map[string]any{"address": world.VaultByName}}); !strings.Contains(msg, "is changed and no token is given") {
		t.Errorf("a provider moved with no token given = %q", msg)
	}
	changed := said("renaming the provider", call(t, "secret_provider_edit", map[string]any{"provider": provider, "name": renamed, "default": false}))
	if row = acctest.Object(t, changed["provider"], "provider"); row["name"] != renamed || row["default"] != nil || len(acctest.Strs(t, changed["changes"], "changes")) != 2 {
		t.Errorf("a provider renamed = %v", changed)
	}

	// moved out of reach, which has to be asked for: the stack is then not deployed, the tool says why, and what was running is left
	away := said("moving the provider out of reach", call(t, "secret_provider_edit", map[string]any{"provider": renamed, "settings": map[string]any{"address": "http://nowhere.invalid:8200", "token": world.VaultToken}, "save_unreachable": true}))
	if !strings.Contains(acctest.Str(away["note"]), "though Dockhand cannot use the provider with them") {
		t.Errorf("a provider moved out of reach = %v", away)
	}
	if msg := refused(t, "stack_deploy", map[string]any{"env": host, "stack": stack, "wait": 120}); !strings.Contains(msg, "was not deployed") || !strings.Contains(msg, "its secrets are loaded from the secret provider "+renamed) {
		t.Errorf("a deploy with the provider out of reach = %q", msg)
	}
	if !slices.Contains(given(), "DB_PASSWORD="+password) {
		t.Error("the container was changed by a deploy that failed")
	}
	if tried := said("a try out of reach", call(t, "secret_provider_test", map[string]any{"provider": renamed})); !acctest.IsBool(tried["reached"], false) {
		t.Errorf("a provider out of reach, tried = %v", tried)
	}
	back := said("moving the provider back", call(t, "secret_provider_edit", map[string]any{"provider": renamed, "settings": settings}))
	if acctest.Object(t, acctest.Object(t, back["provider"], "provider")["settings"], "settings")["address"] != world.Vault || back["note"] != nil {
		t.Errorf("a provider moved back = %v", back)
	}
	said("a deploy once it is back", call(t, "stack_deploy", map[string]any{"env": host, "stack": stack, "wait": 120}))

	// the stack taken off it and given it again, the path kept all the while
	off := said("taking the stack off it", call(t, "stack_edit", map[string]any{"env": host, "stack": stack, "secret_provider": ""}))
	if !strings.Contains(acctest.Str(off["note"]), "with no secret provider, the variables one gave are empty the next time the stack is deployed") {
		t.Errorf("a stack taken off its provider = %v", off)
	}
	if described = call(t, "stack_get", map[string]any{"env": host, "stack": stack}); described["secret_provider"] != nil || described["secret_path"] != path || !strings.Contains(acctest.Str(described["note"]), "has no secret provider, so nothing is loaded") {
		t.Errorf("the stack with a path and no provider = %v", described)
	}
	if on := said("giving the stack it again", call(t, "stack_edit", map[string]any{"env": host, "stack": stack, "secret_provider": renamed})); len(acctest.Strs(t, on["changes"], "changes")) != 1 {
		t.Errorf("a stack given its provider again = %v", on)
	}

	// removed: what would go names the stack, confirm alone is refused for it, and with even_used it goes and the stack is left without
	preview := said("what removing it would take", call(t, "secret_provider_delete", map[string]any{"provider": renamed}))
	if would := strings.Join(acctest.Strs(t, preview["would_go"], "would_go"), "|"); !strings.Contains(would, stack+" on "+host) || !acctest.IsBool(preview["removed"], false) {
		t.Errorf("what removing a provider a stack uses would take = %v", preview)
	}
	if msg := refused(t, "secret_provider_delete", map[string]any{"provider": renamed, "confirm": true}); !strings.Contains(msg, "the secrets of 1 stack are loaded from "+renamed) || listed()[renamed] == nil {
		t.Fatalf("a provider a stack uses, confirmed alone = %q", msg)
	}
	gone := said("removing it", call(t, "secret_provider_delete", map[string]any{"provider": renamed, "confirm": true, "even_used": true}))
	if !acctest.IsBool(gone["removed"], true) || !strings.Contains(acctest.Str(gone["note"]), stack+" on "+host) {
		t.Errorf("a provider removed from under a stack = %v", gone)
	}
	if described = call(t, "stack_get", map[string]any{"env": host, "stack": stack}); described["secret_provider"] != nil {
		t.Errorf("the stack once its provider is removed = %v", described)
	}
	// the foot-gun the tools guard: deployed now, the stack runs with its secrets empty and Dockhand says nothing of it
	said("a deploy with no provider", call(t, "stack_deploy", map[string]any{"env": host, "stack": stack, "wait": 120}))
	if got := given(); !slices.Contains(got, "DB_PASSWORD=") {
		t.Errorf("the container after a deploy with no provider has %d variables, want its password empty", len(got))
	}

	call(t, "stack_delete", map[string]any{"env": host, "stack": stack, "confirm": true})
	if after := len(listed()); after != before {
		t.Errorf("%d secret providers at the end, want the %d there were", after, before)
	}
}
