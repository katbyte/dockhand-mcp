//go:build integration

package acceptance

import (
	"context"
	"slices"
	"strings"
	"testing"

	"github.com/katbyte/dockhand-mcp/lib/testenv"
	"github.com/katbyte/dockhand-mcp/sdk/dockhand"
	"github.com/katbyte/go-kt/mcp/acctest"
)

// A stack is deployed from a git repository, with the tools alone: a credential is saved and the repository behind its login is tried and saved with
// it, a git stack is made from it and seen to run with what the repository, Dockhand's own variables and a secret give it; a new commit is seen,
// fetched and deployed; the stack is changed, renamed once it is down, and forgotten as a git stack with its container left running; and the
// repository and the credential are removed only once nothing uses them. No answer on the way holds the login's password, the webhook's secret or the
// stack's secret.
func TestJourneyAStackDeployedFromGit(t *testing.T) {
	const (
		prefix             = "acc-journey-git"
		credential         = prefix + "-login"
		repository         = prefix + "-app"
		stack, renamed     = prefix + "-stack", prefix + "-renamed"
		container, later   = stack + "-app-1", renamed + "-app-1"
		secret, hook       = "acc-not-a-real-secret", "acc-not-a-real-hook-secret" //nolint:gosec // G101: made up, and good for nothing
		host               = testenv.Alpha
		everyNightAtThree  = "0 3 * * *"
		composeFile, extra = "compose.yaml", "old.txt"
	)
	if world == nil {
		t.Skip(testenv.NotConfigured)
	}
	ctx := context.WithoutCancel(t.Context())
	file := "services:\n  app:\n    image: " + world.Image + "\n    command: [\"sleep\", \"100000\"]\n    stop_grace_period: 1s\n    environment:\n      MODE: ${MODE:-none}\n      EXTRA: ${EXTRA:-none}\n      HUSH: ${HUSH:-none}\n"
	clean := func() {
		if stacks, err := world.Dock.GetGitStacks(ctx, dockhand.GetGitStacksOperationOptions{}); err == nil {
			for i := range stacks.Model {
				if strings.HasPrefix(stacks.Model[i].StackName, prefix) {
					_, _ = world.Dock.DeleteGitStacksById(ctx, stacks.Model[i].Id)
				}
			}
		}
		if repositories, err := world.Dock.GetGitRepositories(ctx); err == nil {
			for i := range repositories.Model {
				if strings.HasPrefix(repositories.Model[i].Name, prefix) {
					_, _ = world.Dock.DeleteGitRepositoriesById(ctx, repositories.Model[i].Id)
				}
			}
		}
		if credentials, err := world.Dock.GetGitCredentials(ctx); err == nil {
			for i := range credentials.Model {
				if strings.HasPrefix(credentials.Model[i].Name, prefix) {
					_, _ = world.Dock.DeleteGitCredentialsById(ctx, credentials.Model[i].Id)
				}
			}
		}
	}
	forgotten(t, host, stack)
	forgotten(t, host, renamed)
	clean()
	t.Cleanup(clean)
	// the repository as the journey starts with it, whatever an earlier stage left there
	first, err := world.Commit(ctx, repository, map[string]string{composeFile: file, ".env": "MODE=first\n", extra: "to go\n"}, "new.txt")
	if err != nil {
		t.Fatal(err)
	}
	// said holds every answer of the journey against the three secrets, none of which a tool may give
	said := func(what string, answer map[string]any) map[string]any {
		t.Helper()
		if text := flat(answer); strings.Contains(text, world.GitPassword) || strings.Contains(text, secret) || strings.Contains(text, hook) {
			t.Errorf("%s gave a password or a secret away: %s", what, text)
		}

		return answer
	}
	private := world.Repository(repository, true)

	// the credential, and the repository behind its login tried without it and with it before anything is saved
	if made := said("the credential saved", call(t, "git_credential_create", map[string]any{"name": credential, "kind": "password", "username": world.GitUser, "password": world.GitPassword})); !acctest.IsBool(acctest.Object(t, made["credential"], "credential")["has_password"], true) {
		t.Fatalf("the credential saved = %v", made)
	}
	if tried := said("a try with no login", call(t, "git_repository_test", map[string]any{"url": private})); !acctest.IsBool(tried["reached"], false) || !strings.Contains(acctest.Str(tried["error"]), "could not read Username") {
		t.Errorf("the repository tried with no login = %v", tried)
	}
	if tried := said("a try", call(t, "git_repository_test", map[string]any{"url": private, "credential": credential})); !acctest.IsBool(tried["reached"], true) || tried["commit"] != first || len(acctest.Rows(t, tried["branches"], "branches")) != 1 {
		t.Fatalf("the repository tried with its login = %v, want it reached at %s", tried, first)
	}
	if msg := refused(t, "git_repository_create", map[string]any{"name": repository, "url": private}); !strings.Contains(msg, "Dockhand could not fetch the repository, so nothing was saved") {
		t.Errorf("a repository that cannot be fetched = %q", msg)
	}
	if saved := said("the repository saved", call(t, "git_repository_create", map[string]any{"name": repository, "url": private, "credential": credential})); acctest.Object(t, saved["repository"], "repository")["credential"] != credential || saved["commit"] != first {
		t.Fatalf("the repository saved = %v", saved)
	}
	if read := acctest.Rows(t, said("its variables", call(t, "git_repository_variables", map[string]any{"repository": repository}))["variables"], "variables"); len(read) != 1 || read[0]["name"] != "MODE" || read[0]["value"] != "first" || read[0]["from"] != ".env" {
		t.Errorf("the variables of the repository = %v", read)
	}

	// the git stack: said first, then made and brought up, with the repository's variable, one Dockhand keeps and one it keeps as a secret
	making := map[string]any{"env": host, "name": stack, "repository": repository, "env_vars": map[string]any{"EXTRA": "kept"}, "secrets": map[string]any{"HUSH": secret}, "webhook_secret": hook, "auto_update_cron": everyNightAtThree}
	if dry := said("a dry run", call(t, "git_stack_create", over(making, map[string]any{"dry_run": true}))); len(acctest.Strs(t, dry["changes"], "changes")) != 3 || containerOn(t, host, container) {
		t.Fatalf("a dry run of making a git stack = %v", dry)
	}
	made := said("the git stack made", call(t, "git_stack_create", making))
	if made["status"] != "running" || made["commit"] != first || made["webhook_path"] == nil || len(acctest.Strs(t, made["reported"], "reported")) == 0 {
		t.Fatalf("the git stack made = %v", made)
	}
	if got := givenTo(t, host, container); !slices.Contains(got, "MODE=first") || !slices.Contains(got, "EXTRA=kept") || !slices.Contains(got, "HUSH="+secret) {
		t.Fatalf("the container was given %v", got)
	}
	if msg := refused(t, "git_stack_create", making); !strings.Contains(msg, "a stack named "+stack+" is already on "+host) {
		t.Errorf("a second git stack of the name = %q", msg)
	}

	// read every way: among the git stacks, in full with its secrets hidden, as a stack, and on Dockhand's list of what it does by itself
	rows := named(acctest.Rows(t, said("the git stacks", call(t, "git_stack_list", map[string]any{"env": host}))["stacks"], "stacks"), "stack")
	if row := rows[stack]; row["repository"] != repository || row["branch"] != "main" || row["commit"] != first || row["auto_update"] != everyNightAtThree || !acctest.IsBool(row["webhook"], true) || row["sync"] != "synced" {
		t.Errorf("the git stack listed = %v", row)
	}
	got := said("the git stack read", call(t, "git_stack_get", map[string]any{"stack": stack}))
	if got["deployed"] != first || got["newest"] != first || got["behind"] != nil || got["status"] != "running" || got["webhook_secret"] != "***" || len(acctest.Rows(t, got["variables"], "variables")) != 2 {
		t.Errorf("the git stack read = %v", got)
	}
	if shown := call(t, "git_stack_get", map[string]any{"stack": stack, "reveal_secrets": true, "read_env_file": ".env"}); shown["webhook_secret"] != hook || strings.Contains(flat(shown), secret) || len(acctest.Rows(t, shown["file_variables"], "file_variables")) != 1 {
		t.Errorf("the git stack read with its secrets asked for gives the webhook's and never the one Dockhand keeps: %v", shown)
	}
	if seen := said("the stack read", call(t, "stack_get", map[string]any{"env": host, "stack": stack})); seen["kept"] != "git" || !acctest.IsBool(seen["deployable"], true) || !strings.Contains(acctest.Str(seen["compose"]), "MODE") {
		t.Errorf("the stack as the stack tools see it = %v", seen)
	}
	if schedules := said("the schedules", call(t, "schedule_list", map[string]any{"type": "git-stack-sync"})); !strings.Contains(flat(schedules), stack) || !strings.Contains(flat(schedules), everyNightAtThree) {
		t.Errorf("the git stack's schedule among what Dockhand does by itself = %v", schedules)
	}

	// the credential and the repository read, each with what uses it; a credential given a password the server does not take says which repository is
	// lost by it, and a repository is not moved to a branch it does not have
	logins := named(acctest.Rows(t, said("the credentials", call(t, "git_credential_list", nil))["credentials"], "credentials"), "name")
	if login := logins[credential]; login["kind"] != "password" || login["username"] != world.GitUser || !slices.Contains(acctest.Strs(t, login["repositories"], "repositories"), repository) {
		t.Errorf("the credential listed = %v", login)
	}
	kept := named(acctest.Rows(t, said("the repositories", call(t, "git_repository_list", nil))["repositories"], "repositories"), "name")
	if row := kept[repository]; row["url"] != private || row["credential"] != credential || len(acctest.Rows(t, row["stacks"], "stacks")) != 1 {
		t.Errorf("the repository listed = %v", row)
	}
	if lost := said("a wrong password saved", call(t, "git_credential_edit", map[string]any{"credential": credential, "password": "acc-wrong"})); !strings.Contains(acctest.Str(lost["note"]), "1 repository fetched with it can no longer be fetched: "+repository) {
		t.Errorf("the credential given a wrong password = %v", lost)
	}
	if mended := said("the password put right", call(t, "git_credential_edit", map[string]any{"credential": credential, "password": world.GitPassword})); mended["note"] != nil {
		t.Errorf("the credential given its password again = %v", mended)
	}
	if msg := refused(t, "git_repository_edit", map[string]any{"repository": repository, "branch": "acc-nope"}); !strings.Contains(msg, "Dockhand could not fetch "+repository+" that way, so nothing was changed") {
		t.Errorf("the repository moved to a branch there is none of = %q", msg)
	}
	if moved := said("the repository renamed", call(t, "git_repository_edit", map[string]any{"repository": repository, "name": repository + "-kept"})); !strings.Contains(acctest.Str(moved["note"]), stack+" on "+host) {
		t.Errorf("the repository renamed = %v", moved)
	}
	call(t, "git_repository_edit", map[string]any{"repository": repository + "-kept", "name": repository})

	// a new commit: seen to be ahead, fetched alone and said not to be what runs, then deployed, and not deployed twice
	second, err := world.Commit(ctx, repository, map[string]string{".env": "MODE=second\n", "new.txt": "added\n"}, extra)
	if err != nil {
		t.Fatal(err)
	}
	rows = named(acctest.Rows(t, call(t, "git_stack_list", map[string]any{"env": host, "check": true})["stacks"], "stacks"), "stack")
	if row := rows[stack]; row["newest"] != second || row["deployed"] != first || !acctest.IsBool(row["behind"], true) {
		t.Errorf("the git stack with a new commit, checked = %v", row)
	}
	if dry := said("a dry run of a deploy", call(t, "git_stack_deploy", map[string]any{"stack": stack, "dry_run": true})); dry["commit"] != second || dry["from"] != first || !slices.Contains(givenTo(t, host, container), "MODE=first") {
		t.Errorf("a dry run of a deploy = %v", dry)
	}
	fetched := said("the fetch", call(t, "git_stack_sync", map[string]any{"stack": stack}))
	if fetched["commit"] != second || fetched["was"] != first || fetched["deployed"] != first || !slices.Contains(acctest.Strs(t, fetched["changed"], "changed"), ".env") || !slices.Contains(acctest.Strs(t, fetched["would_go"], "would_go"), extra) || !slices.Contains(givenTo(t, host, container), "MODE=first") {
		t.Errorf("the fetch = %v, with the container given %v", fetched, givenTo(t, host, container))
	}
	if got = call(t, "git_stack_get", map[string]any{"stack": stack}); !acctest.IsBool(got["behind"], true) || !strings.Contains(acctest.Str(got["note"]), "was fetched and never deployed") {
		t.Errorf("the git stack after a fetch alone = %v", got)
	}
	deployed := said("the deploy", call(t, "git_stack_deploy", map[string]any{"stack": stack, "only_if_changed": true}))
	if deployed["commit"] != second || deployed["from"] != first || deployed["is"] != "running" || !slices.Contains(givenTo(t, host, container), "MODE=second") {
		t.Fatalf("the deploy = %v, with the container given %v", deployed, givenTo(t, host, container))
	}
	if again := call(t, "git_stack_deploy", map[string]any{"stack": stack, "only_if_changed": true}); len(acctest.Strs(t, again["changes"], "changes")) != 0 || !strings.Contains(acctest.Str(again["note"]), "nothing to do") {
		t.Errorf("a deploy with nothing new = %v", again)
	}

	// its files are the repository's, so the stack tools refuse to change it; what Dockhand keeps for it is changed as a git stack, and takes effect
	// at the next deploy
	if msg := refused(t, "stack_edit", map[string]any{"env": host, "stack": stack, "env_file": "MODE=by-hand\n"}); !strings.Contains(msg, "is deployed from a git repository, and is changed as a git stack") {
		t.Errorf("the stack's file changed by hand = %q", msg)
	}
	if msg := refused(t, "git_stack_edit", map[string]any{"stack": stack, "branch": "acc-nope"}); !strings.Contains(msg, "its repository has no branch acc-nope") {
		t.Errorf("a branch there is none of = %q", msg)
	}
	changed := said("the change", call(t, "git_stack_edit", map[string]any{"stack": stack, "env_vars": map[string]any{"EXTRA": "changed"}, "remove_secrets": []any{"HUSH"}, "auto_update_cron": "", "webhook_secret": "", "recreate": true}))
	if len(acctest.Strs(t, changed["changes"], "changes")) != 5 || !slices.Contains(givenTo(t, host, container), "EXTRA=kept") {
		t.Errorf("the change = %v, with the container given %v before any deploy", changed, givenTo(t, host, container))
	}
	if again := call(t, "git_stack_edit", map[string]any{"stack": stack, "env_vars": map[string]any{"EXTRA": "changed"}, "auto_update_cron": "", "webhook_secret": "", "recreate": true}); len(acctest.Strs(t, again["changes"], "changes")) != 0 {
		t.Errorf("the same change again = %v", again)
	}
	call(t, "git_stack_deploy", map[string]any{"stack": stack})
	if given := givenTo(t, host, container); !slices.Contains(given, "EXTRA=changed") || !slices.Contains(given, "HUSH=none") {
		t.Errorf("after the change was deployed the container was given %v", given)
	}
	if got = call(t, "git_stack_get", map[string]any{"stack": stack}); got["auto_update"] != nil || got["webhook"] != nil || got["webhook_path"] != nil || !acctest.IsBool(got["recreate"], true) {
		t.Errorf("the git stack after the change = %v", got)
	}

	// renamed only once it is down and to a name nothing has, and brought up again under the new name
	if msg := refused(t, "git_stack_edit", map[string]any{"stack": stack, "name": renamed}); !strings.Contains(msg, "Take the stack down first") {
		t.Errorf("a rename while it runs = %q", msg)
	}
	call(t, "stack_down", map[string]any{"env": host, "stack": stack})
	if moved := call(t, "git_stack_edit", map[string]any{"stack": stack, "name": renamed}); moved["stack"] != renamed {
		t.Fatalf("the rename = %v", moved)
	}
	if up := call(t, "git_stack_deploy", map[string]any{"stack": renamed}); up["is"] != "running" || !containerOn(t, host, later) || containerOn(t, host, container) {
		t.Fatalf("the renamed stack deployed = %v", up)
	}
	if given := givenTo(t, host, later); !slices.Contains(given, "EXTRA=changed") || !slices.Contains(given, "MODE=second") {
		t.Errorf("the renamed stack's container was given %v: what Dockhand kept for it came with the name", given)
	}

	// what is in use is not removed: the credential the repository is fetched with, and the repository the git stack is made from
	if msg := refused(t, "git_credential_delete", map[string]any{"credential": credential, "confirm": true}); !strings.Contains(msg, credential+" signs in for 1 repository: "+repository) {
		t.Errorf("the credential removed while in use = %q", msg)
	}
	if msg := refused(t, "git_repository_delete", map[string]any{"repository": repository, "confirm": true}); !strings.Contains(msg, repository+" has 1 git stack deployed from it: "+renamed+" on "+host) {
		t.Errorf("the repository removed while in use = %q", msg)
	}

	// forgotten as a git stack: said first, then done, and its container is left running as a stack Dockhand only found; the stack tools remove that
	if asked := call(t, "git_stack_delete", map[string]any{"stack": renamed}); !acctest.IsBool(asked["removed"], false) || len(acctest.Strs(t, asked["would_go"], "would_go")) != 2 || len(acctest.Strs(t, asked["stays"], "stays")) != 2 {
		t.Errorf("forgetting the git stack, asked about = %v", asked)
	}
	if gone := call(t, "git_stack_delete", map[string]any{"stack": renamed, "confirm": true}); !acctest.IsBool(gone["removed"], true) || !containerOn(t, host, later) {
		t.Fatalf("the git stack forgotten = %v, with its container there: %v", gone, containerOn(t, host, later))
	}
	if left := stackNamed(t, renamed); left == nil || left["status"] != "running" {
		t.Errorf("what is left of it among the stacks = %v", left)
	}
	if seen := call(t, "stack_get", map[string]any{"env": host, "stack": renamed}); seen["kept"] != "found" || !acctest.IsBool(seen["deployable"], false) {
		t.Errorf("the stack once Dockhand has forgotten its git side = %v", seen)
	}
	if len(acctest.Rows(t, call(t, "git_stack_list", map[string]any{"env": host})["stacks"], "stacks")) != len(rows)-1 {
		t.Error("the git stack is still listed as one")
	}
	if removed := call(t, "stack_delete", map[string]any{"env": host, "stack": renamed, "confirm": true}); !acctest.IsBool(removed["deleted"], true) || containerOn(t, host, later) {
		t.Errorf("the stack removed = %v", removed)
	}

	// and now nothing uses the repository or the credential, each is removed on confirm alone
	if removed := call(t, "git_repository_delete", map[string]any{"repository": repository, "confirm": true}); !acctest.IsBool(removed["removed"], true) {
		t.Errorf("the repository removed = %v", removed)
	}
	if removed := call(t, "git_credential_delete", map[string]any{"credential": credential, "confirm": true}); !acctest.IsBool(removed["removed"], true) {
		t.Errorf("the credential removed = %v", removed)
	}
}
