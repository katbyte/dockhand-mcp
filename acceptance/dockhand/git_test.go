//go:build acceptance

package dockhand_test

import (
	"encoding/json"
	"slices"
	"strings"
	"testing"

	"github.com/katbyte/dockhand-mcp/lib/testenv"
	"github.com/katbyte/dockhand-mcp/sdk/dockhand"
	"github.com/katbyte/pandorest/client"
)

// gitFile is a compose file for a stack deployed from the suites' git server: one service that is given a variable the repository's own .env sets,
// one Dockhand keeps for the stack, and one a secret provider would give.
func gitFile() string {
	return "services:\n  app:\n    image: " + world.Image + "\n    command: [\"sleep\", \"100000\"]\n    stop_grace_period: 1s\n    environment:\n      MODE: ${MODE:-none}\n      EXTRA: ${EXTRA:-none}\n      HUSH: ${HUSH:-none}\n"
}

// gitGone removes what the git tests of this file make, before a test starts in case a run cut short left it and when it ends: the git stacks, the
// repositories and the credentials whose names begin as the test's do.
func gitGone(t *testing.T, prefix string) {
	t.Helper()

	c := dock(t)
	ctx := noCancel(t)
	clean := func() {
		if stacks, err := c.GetGitStacks(ctx, dockhand.GetGitStacksOptions{}); err == nil {
			for i := range stacks.Answer {
				if strings.HasPrefix(stacks.Answer[i].StackName, prefix) {
					_, _ = c.DeleteGitStacksById(ctx, stacks.Answer[i].Id)
				}
			}
		}
		if repositories, err := c.GetGitRepositories(ctx); err == nil {
			for i := range repositories.Answer {
				if strings.HasPrefix(repositories.Answer[i].Name, prefix) {
					_, _ = c.DeleteGitRepositoriesById(ctx, repositories.Answer[i].Id)
				}
			}
		}
		if credentials, err := c.GetGitCredentials(ctx); err == nil {
			for i := range credentials.Answer {
				if strings.HasPrefix(credentials.Answer[i].Name, prefix) || strings.HasPrefix(strings.ToLower(credentials.Answer[i].Name), prefix) {
					_, _ = c.DeleteGitCredentialsById(ctx, credentials.Answer[i].Id)
				}
			}
		}
	}
	clean()
	t.Cleanup(clean)
}

// foldersGone removes what Dockhand keeps on its own disk for stacks of the names given, their files and their copies of a repository, before a test
// starts and when it ends: a git stack that is forgotten, or renamed, leaves both where they lay, and the next git stack of that name starts from
// them rather than from nothing.
func foldersGone(t *testing.T, host string, stacks ...string) {
	t.Helper()

	ctx := noCancel(t)
	clean := func() {
		for _, stack := range stacks {
			for _, kept := range []string{"stacks", "git-repos"} {
				if err := world.Unlay(ctx, "/app/data/"+kept+"/"+host+"/"+stack); err != nil {
					t.Errorf("removing what Dockhand kept on disk for %s: %v", stack, err)
				}
			}
		}
	}
	clean()
	t.Cleanup(clean)
}

// committed puts files in a repository of the suites' git server and answers the commit.
func committed(t *testing.T, repository string, files map[string]string, gone ...string) string {
	t.Helper()

	commit, err := world.Commit(t.Context(), repository, files, gone...)
	if err != nil || commit == "" {
		t.Fatalf("committing to %s: %q, %v", repository, commit, err)
	}

	return commit
}

// deployedAt is the commit the files a git stack runs from were taken at, shortened as Dockhand shortens one, and nothing for one never deployed.
func deployedAt(t *testing.T, id int) string {
	t.Helper()

	got, err := dock(t).GetGitStacksById(t.Context(), dockhand.GitStackID(id))
	if err != nil {
		t.Fatalf("reading git stack %d: %v", id, err)
	}
	var taken struct {
		Commit string `json:"commit"`
	}
	if got.Answer.SyncedFiles == "" || json.Unmarshal([]byte(got.Answer.SyncedFiles), &taken) != nil || len(taken.Commit) < 7 {
		return ""
	}

	return taken.Commit[:7]
}

// A git credential is saved, read, changed and removed: what each call answers and refuses, that what it signs in with is never given back, that
// names differ by case, and that one a repository is fetched with is removed without a word, leaving the repository with none.
func TestGitCredentialsSavedChangedAndRemoved(t *testing.T) {
	const prefix, nobody = "it-git-credential", 987654
	c := dock(t)
	ctx := t.Context()
	gitGone(t, prefix)
	name := prefix + "-login"

	for what, tc := range map[string]struct {
		request dockhand.PostGitCredentialsRequest
		want    string
	}{
		"no name":                  {dockhand.PostGitCredentialsRequest{}, "Name is required"},
		"a kind there is none of":  {dockhand.PostGitCredentialsRequest{Name: name, AuthType: "token"}, "Invalid auth type"},
		"a password one with none": {dockhand.PostGitCredentialsRequest{Name: name, AuthType: "password", Username: "it"}, "Password is required for password authentication"},
		"a key one with no key":    {dockhand.PostGitCredentialsRequest{Name: name, AuthType: "ssh"}, "SSH private key is required for SSH authentication"},
	} {
		if _, err := c.PostGitCredentials(ctx, tc.request); client.StatusCode(err) != 400 || !strings.Contains(err.Error(), tc.want) {
			t.Errorf("%s = %v, want a 400 saying %q", what, err, tc.want)
		}
	}

	// saved: answered with whether a password and a key are kept, and never with either
	made, err := c.PostGitCredentials(ctx, dockhand.PostGitCredentialsRequest{Name: name, AuthType: "password", Username: world.GitUser, Password: "it-wrong"})
	if err != nil || made.Answer.Id == 0 || made.Answer.Name != name || made.Answer.AuthType != "password" || made.Answer.Username != world.GitUser || !made.Answer.HasPassword || made.Answer.HasSshKey || made.Answer.CreatedAt.IsZero() {
		t.Fatalf("a credential saved = %+v, %v", made.Answer, err)
	}
	id := made.Answer.Id
	if _, err = c.PostGitCredentials(ctx, dockhand.PostGitCredentialsRequest{Name: name, AuthType: "none"}); client.StatusCode(err) != 400 || !strings.Contains(err.Error(), "A credential with this name already exists") {
		t.Errorf("a name that is taken = %v", err)
	}
	// a name in another case is another credential; one that signs in with nothing keeps a key it is sent all the same, any text at all
	other, err := c.PostGitCredentials(ctx, dockhand.PostGitCredentialsRequest{Name: strings.ToUpper(name), AuthType: "none", SshPrivateKey: "it-not-a-key"})
	if err != nil || other.Answer.Id == id || other.Answer.AuthType != "none" || !other.Answer.HasSshKey {
		t.Errorf("a credential named in another case = %+v, %v", other.Answer, err)
	}

	listed, err := c.GetGitCredentials(ctx)
	if err != nil || !slices.ContainsFunc(listed.Answer, func(k dockhand.GetGitCredentialsAnswer) bool {
		return k.Id == id && k.Name == name && k.HasPassword
	}) {
		t.Errorf("the credentials listed = %+v, %v", listed.Answer, err)
	}
	if got, unread := c.GetGitCredentialsById(ctx, id); unread != nil || got.Answer.Name != name || got.Answer.Username != world.GitUser || !got.Answer.HasPassword {
		t.Errorf("a credential read = %+v, %v", got.Answer, unread)
	}
	if _, err = c.GetGitCredentialsById(ctx, nobody); !client.IsNotFound(err) || !strings.Contains(err.Error(), "Credential not found") {
		t.Errorf("a credential there is none of, read = %v", err)
	}

	// changed a field at a time: the rest is kept, and what it signs in with is kept through a change of kind
	if changed, unsaved := c.PutGitCredentialsById(ctx, id, dockhand.PutGitCredentialsByIdRequest{Password: world.GitPassword}); unsaved != nil || changed.Answer.Name != name || !changed.Answer.HasPassword || changed.Answer.UpdatedAt == made.Answer.UpdatedAt {
		t.Errorf("a credential given another password = %+v, %v", changed.Answer, unsaved)
	}
	if changed, unsaved := c.PutGitCredentialsById(ctx, id, dockhand.PutGitCredentialsByIdRequest{AuthType: "none"}); unsaved != nil || changed.Answer.AuthType != "none" || !changed.Answer.HasPassword || changed.Answer.Username != world.GitUser {
		t.Errorf("a credential that signs in with nothing keeps what it had = %+v, %v", changed.Answer, unsaved)
	}
	if changed, unsaved := c.PutGitCredentialsById(ctx, id, dockhand.PutGitCredentialsByIdRequest{AuthType: "password", Name: name + "-renamed"}); unsaved != nil || changed.Answer.AuthType != "password" || changed.Answer.Name != name+"-renamed" {
		t.Errorf("a credential renamed = %+v, %v", changed.Answer, unsaved)
	}
	for what, tc := range map[string]struct {
		id      int
		request dockhand.PutGitCredentialsByIdRequest
		status  int
		want    string
	}{
		"a name that is taken":    {int(id), dockhand.PutGitCredentialsByIdRequest{Name: strings.ToUpper(name)}, 400, "A credential with this name already exists"},
		"a kind there is none of": {int(id), dockhand.PutGitCredentialsByIdRequest{AuthType: "token"}, 400, "Invalid auth type"},
		"one there is none of":    {nobody, dockhand.PutGitCredentialsByIdRequest{Name: "it"}, 404, "Credential not found"},
	} {
		if _, err = c.PutGitCredentialsById(ctx, dockhand.GitCredentialID(tc.id), tc.request); client.StatusCode(err) != tc.status || !strings.Contains(err.Error(), tc.want) {
			t.Errorf("%s, changed = %v, want a %d saying %q", what, err, tc.status, tc.want)
		}
	}

	// removed while a repository is fetched with it: not refused, and the repository is left with no credential
	repository, err := c.PostGitRepositories(ctx, dockhand.PostGitRepositoriesRequest{Name: prefix + "-repository", Url: world.Repository("it-none", true), CredentialId: id})
	if err != nil || repository.Answer.CredentialId != id {
		t.Fatalf("a repository fetched with the credential = %+v, %v", repository.Answer, err)
	}
	if removed, unremoved := c.DeleteGitCredentialsById(ctx, id); unremoved != nil || !removed.Answer.Success {
		t.Fatalf("a credential in use, removed = %+v, %v", removed.Answer, unremoved)
	}
	if left, unread := c.GetGitRepositoriesById(ctx, repository.Answer.Id); unread != nil || left.Answer.CredentialId != 0 {
		t.Errorf("the repository it was fetched with is left with credential %d, %v: want none", left.Answer.CredentialId, unread)
	}
	if _, err = c.DeleteGitCredentialsById(ctx, id); !client.IsNotFound(err) || !strings.Contains(err.Error(), "Credential not found") {
		t.Errorf("a credential there is none of, removed = %v", err)
	}
}

// A git repository is tried before it is kept, then saved, read, tried, asked for its branches and its variables, fetched, changed and removed: what
// each call answers and refuses, that one that cannot be fetched is saved all the same, and that its own fetch, deploy and webhook are of a copy no
// git stack uses.
func TestGitRepositoriesTriedSavedChangedAndRemoved(t *testing.T) {
	const prefix, nobody = "it-git-repository", 987654
	c := dock(t)
	ctx := t.Context()
	gitGone(t, prefix)
	name := prefix + "-app"
	first := committed(t, name, map[string]string{"compose.yaml": gitFile(), ".env": "MODE=first\n", "deploy/extra.env": "EXTRA=from-file\n"})
	if err := world.Branch(ctx, name, "next"); err != nil {
		t.Fatal(err)
	}
	open, private := world.Repository(name, false), world.Repository(name, true)
	login, err := c.PostGitCredentials(ctx, dockhand.PostGitCredentialsRequest{Name: prefix + "-login", AuthType: "password", Username: world.GitUser, Password: world.GitPassword})
	if err != nil {
		t.Fatal(err)
	}

	// tried before it is saved: every answer that reaches git is a 200 that says whether it worked, the branch and its newest commit, or why not
	if tried, untried := c.PostGitRepositoriesTest(ctx, dockhand.PostGitRepositoriesTestRequest{Url: open}); untried != nil || !tried.Answer.Success || tried.Answer.Branch != "main" || tried.Answer.LastCommit != first {
		t.Fatalf("the repository, tried = %+v, %v, want it reached at %s", tried.Answer, untried, first)
	}
	for what, tc := range map[string]struct {
		request dockhand.PostGitRepositoriesTestRequest
		want    string
	}{
		"a branch it does not have":     {dockhand.PostGitRepositoriesTestRequest{Url: open, Branch: "it-nope"}, "Branch 'it-nope' not found. Available branches: main, next"},
		"one that asks for a login":     {dockhand.PostGitRepositoriesTestRequest{Url: private}, "could not read Username"},
		"one there is none of":          {dockhand.PostGitRepositoriesTestRequest{Url: world.Repository("it-nowhere", false)}, "not found"},
		"an address nothing answers at": {dockhand.PostGitRepositoriesTestRequest{Url: "http://nowhere.invalid/it.git"}, "nowhere.invalid"},
	} {
		tried, untried := c.PostGitRepositoriesTest(ctx, tc.request)
		if untried != nil || tried.Answer.Success || !strings.Contains(tried.Answer.Error, tc.want) {
			t.Errorf("%s, tried = %+v, %v, want it refused with %q", what, tried.Answer, untried, tc.want)
		}
	}
	if tried, untried := c.PostGitRepositoriesTest(ctx, dockhand.PostGitRepositoriesTestRequest{Url: private, Branch: "next", CredentialId: login.Answer.Id}); untried != nil || !tried.Answer.Success || tried.Answer.Branch != "next" {
		t.Errorf("the repository behind its login, tried with it = %+v, %v", tried.Answer, untried)
	}
	if _, err = c.PostGitRepositoriesTest(ctx, dockhand.PostGitRepositoriesTestRequest{}); client.StatusCode(err) != 400 || !strings.Contains(err.Error(), "Repository URL is required") {
		t.Errorf("no address, tried = %v", err)
	}
	if _, err = c.PostGitRepositoriesTest(ctx, dockhand.PostGitRepositoriesTestRequest{Url: open, CredentialId: nobody}); !client.IsNotFound(err) || !strings.Contains(err.Error(), "Credential not found") {
		t.Errorf("a credential there is none of, tried = %v", err)
	}

	// its branches, by address, and what the call refuses
	if branches, unlisted := c.PostGitBranches(ctx, dockhand.PostGitBranchesRequest{Url: open}); unlisted != nil || len(branches.Answer.Branches) != 2 || branches.Answer.Branches[0].Name != "main" || branches.Answer.Branches[0].Sha != first {
		t.Errorf("the branches of the repository = %+v, %v", branches.Answer, unlisted)
	}
	if _, err = c.PostGitBranches(ctx, dockhand.PostGitBranchesRequest{}); client.StatusCode(err) != 400 || !strings.Contains(err.Error(), "repositoryId or url is required") {
		t.Errorf("the branches of nothing = %v", err)
	}
	if _, err = c.PostGitBranches(ctx, dockhand.PostGitBranchesRequest{RepositoryId: nobody}); !client.IsNotFound(err) {
		t.Errorf("the branches of a repository there is none of = %v", err)
	}
	if _, err = c.PostGitBranches(ctx, dockhand.PostGitBranchesRequest{Url: private}); client.StatusCode(err) != 500 || !strings.Contains(err.Error(), "Failed to fetch branches") {
		t.Errorf("the branches of one that asks for a login = %v", err)
	}

	// saved: with main for a branch unless one is said, and one that cannot be fetched is saved all the same
	for what, tc := range map[string]struct {
		request dockhand.PostGitRepositoriesRequest
		want    string
	}{
		"no name":                       {dockhand.PostGitRepositoriesRequest{Url: open}, "Name is required"},
		"no address":                    {dockhand.PostGitRepositoriesRequest{Name: name}, "Repository URL is required"},
		"a credential there is none of": {dockhand.PostGitRepositoriesRequest{Name: name, Url: open, CredentialId: nobody}, "Invalid credential ID"},
	} {
		if _, err = c.PostGitRepositories(ctx, tc.request); client.StatusCode(err) != 400 || !strings.Contains(err.Error(), tc.want) {
			t.Errorf("%s, saved = %v, want a 400 saying %q", what, err, tc.want)
		}
	}
	made, err := c.PostGitRepositories(ctx, dockhand.PostGitRepositoriesRequest{Name: name, Url: open})
	if err != nil || made.Answer.Id == 0 || made.Answer.Name != name || made.Answer.Url != open || made.Answer.Branch != "main" || made.Answer.CredentialId != 0 || made.Answer.SyncStatus != "pending" || made.Answer.CreatedAt.IsZero() {
		t.Fatalf("a repository saved = %+v, %v", made.Answer, err)
	}
	id := made.Answer.Id
	if _, err = c.PostGitRepositories(ctx, dockhand.PostGitRepositoriesRequest{Name: name, Url: open}); client.StatusCode(err) != 400 || !strings.Contains(err.Error(), "A repository with this name already exists") {
		t.Errorf("a name that is taken = %v", err)
	}
	gone, err := c.PostGitRepositories(ctx, dockhand.PostGitRepositoriesRequest{Name: name + "-gone", Url: world.Repository("it-nowhere", false), Branch: "it-nope", CredentialId: login.Answer.Id})
	if err != nil || gone.Answer.Branch != "it-nope" || gone.Answer.CredentialId != login.Answer.Id {
		t.Errorf("a repository that cannot be fetched is saved all the same: %+v, %v", gone.Answer, err)
	}

	// read: in the list with the name of its credential, and alone
	listed, err := c.GetGitRepositories(ctx)
	if err != nil || !slices.ContainsFunc(listed.Answer, func(p dockhand.GetGitRepositoriesAnswer) bool {
		return p.Id == gone.Answer.Id && p.CredentialName == prefix+"-login" && p.CredentialAuthType == "password"
	}) || !slices.ContainsFunc(listed.Answer, func(p dockhand.GetGitRepositoriesAnswer) bool { return p.Id == id && p.CredentialName == "" }) {
		t.Errorf("the repositories listed = %+v, %v", listed.Answer, err)
	}
	if got, unread := c.GetGitRepositoriesById(ctx, id); unread != nil || got.Answer.Name != name || got.Answer.Url != open {
		t.Errorf("a repository read = %+v, %v", got.Answer, unread)
	}
	if _, err = c.GetGitRepositoriesById(ctx, nobody); !client.IsNotFound(err) || !strings.Contains(err.Error(), "Repository not found") {
		t.Errorf("a repository there is none of, read = %v", err)
	}
	if tried, untried := c.PostGitRepositoriesByIdTest(ctx, id); untried != nil || !tried.Answer.Success || tried.Answer.LastCommit != first {
		t.Errorf("a saved repository, tried = %+v, %v", tried.Answer, untried)
	}
	if tried, untried := c.PostGitRepositoriesByIdTest(ctx, gone.Answer.Id); untried != nil || tried.Answer.Success || tried.Answer.Error == "" {
		t.Errorf("a saved repository that cannot be fetched, tried = %+v, %v", tried.Answer, untried)
	}
	if _, err = c.PostGitRepositoriesByIdTest(ctx, nobody); !client.IsNotFound(err) {
		t.Errorf("a repository there is none of, tried = %v", err)
	}
	if branches, unlisted := c.PostGitBranches(ctx, dockhand.PostGitBranchesRequest{RepositoryId: id}); unlisted != nil || len(branches.Answer.Branches) != 2 {
		t.Errorf("the branches of a saved repository = %+v, %v", branches.Answer, unlisted)
	}

	// the variables its .env files would give a stack, read from a copy made for the purpose
	if read, unread := c.PostGitPreviewEnv(ctx, dockhand.PostGitPreviewEnvRequest{RepositoryId: id, ComposePath: "compose.yaml", EnvFilePath: "deploy/extra.env"}); unread != nil || read.Answer.Vars["MODE"] != "first" || read.Answer.Sources["MODE"] != ".env" || read.Answer.Vars["EXTRA"] != "from-file" || read.Answer.Sources["EXTRA"] != "envFile" {
		t.Errorf("the variables of the repository = %+v, %v", read.Answer, unread)
	}
	// a compose file that is not there is not noticed: only the .env beside where it would be is read
	if read, unread := c.PostGitPreviewEnv(ctx, dockhand.PostGitPreviewEnvRequest{Url: private, CredentialId: login.Answer.Id, Branch: "next", ComposePath: "it-nope.yaml"}); unread != nil || read.Answer.Vars["MODE"] != "first" || len(read.Answer.Vars) != 1 {
		t.Errorf("the variables of the repository by its address = %+v, %v", read.Answer, unread)
	}
	if _, err = c.PostGitPreviewEnv(ctx, dockhand.PostGitPreviewEnvRequest{RepositoryId: id}); client.StatusCode(err) != 400 || !strings.Contains(err.Error(), "Compose path is required") {
		t.Errorf("variables with no compose file named = %v", err)
	}
	if _, err = c.PostGitPreviewEnv(ctx, dockhand.PostGitPreviewEnvRequest{RepositoryId: nobody, ComposePath: "compose.yaml"}); !client.IsNotFound(err) {
		t.Errorf("the variables of a repository there is none of = %v", err)
	}
	if _, err = c.PostGitPreviewEnv(ctx, dockhand.PostGitPreviewEnvRequest{RepositoryId: gone.Answer.Id, ComposePath: "compose.yaml"}); client.StatusCode(err) != 400 || !strings.Contains(err.Error(), "Failed to clone repository") {
		t.Errorf("the variables of a repository that cannot be fetched = %v", err)
	}

	// the repository's own copy, which no git stack uses: behind until it is fetched, then level, then behind again once the branch moves
	if behind, unasked := c.GetGitRepositoriesByIdSync(ctx, id); unasked != nil || !behind.Answer.HasUpdates || behind.Answer.CurrentCommit != "none" {
		t.Errorf("a repository never fetched, asked whether it is behind = %+v, %v", behind.Answer, unasked)
	}
	if fetched, unfetched := c.PostGitRepositoriesByIdSync(ctx, id); unfetched != nil || !fetched.Answer.Success || fetched.Answer.Commit != first || !fetched.Answer.Updated || !strings.Contains(fetched.Answer.ComposeContent, "services:") {
		t.Errorf("a repository fetched = %+v, %v", fetched.Answer, unfetched)
	}
	if behind, unasked := c.GetGitRepositoriesByIdSync(ctx, id); unasked != nil || behind.Answer.HasUpdates || behind.Answer.CurrentCommit != first || behind.Answer.LatestCommit != first {
		t.Errorf("a repository just fetched, asked whether it is behind = %+v, %v", behind.Answer, unasked)
	}
	second := committed(t, name, map[string]string{".env": "MODE=second\n"})
	if behind, unasked := c.GetGitRepositoriesByIdSync(ctx, id); unasked != nil || !behind.Answer.HasUpdates || behind.Answer.CurrentCommit != first || behind.Answer.LatestCommit != second {
		t.Errorf("a repository whose branch moved, asked whether it is behind = %+v, %v", behind.Answer, unasked)
	}
	if got, unread := c.GetGitRepositoriesById(ctx, id); unread != nil || got.Answer.SyncStatus != "synced" || got.Answer.LastCommit != first || got.Answer.LastSync.IsZero() {
		t.Errorf("a repository fetched, read = %+v, %v", got.Answer, unread)
	}
	if _, err = c.GetGitRepositoriesByIdSync(ctx, nobody); !client.IsNotFound(err) {
		t.Errorf("a repository there is none of, asked whether it is behind = %v", err)
	}
	if _, err = c.PostGitRepositoriesByIdSync(ctx, nobody); !client.IsNotFound(err) {
		t.Errorf("a repository there is none of, fetched = %v", err)
	}
	// its own deploy is of no environment: it runs compose against the Docker Dockhand itself is on, under the repository's name, which the suite's
	// Dockhand has none of; and its own webhook is one nothing in the API switches on
	if deployed, undeployed := c.PostGitRepositoriesByIdDeploy(ctx, id); undeployed != nil || deployed.Answer.Success || !strings.Contains(deployed.Answer.Command, "compose -p "+name+" up") || strings.Contains(deployed.Answer.Command, " -H ") || deployed.Answer.ExitCode == 0 {
		t.Errorf("a repository deployed by itself = %+v, %v", deployed.Answer, undeployed)
	}
	if _, err = c.PostGitRepositoriesByIdDeploy(ctx, nobody); !client.IsNotFound(err) {
		t.Errorf("a repository there is none of, deployed = %v", err)
	}
	if _, err = c.GetGitWebhookById(ctx, int(id), dockhand.GetGitWebhookByIdOptions{}); !client.IsForbidden(err) || !strings.Contains(err.Error(), "Webhook is not enabled for this repository") {
		t.Errorf("a repository's own webhook, called = %v", err)
	}
	if _, err = c.PostGitWebhookById(ctx, int(id), dockhand.PostGitWebhookByIdRequest{}); !client.IsForbidden(err) {
		t.Errorf("a repository's own webhook, posted to = %v", err)
	}
	if _, err = c.GetGitWebhookById(ctx, nobody, dockhand.GetGitWebhookByIdOptions{}); !client.IsNotFound(err) {
		t.Errorf("the webhook of a repository there is none of = %v", err)
	}

	// changed a field at a time; its credential is taken away only by sending it as nothing
	if changed, unsaved := c.PutGitRepositoriesById(ctx, id, dockhand.PutGitRepositoriesByIdRequest{Url: private, Branch: "next", CredentialId: login.Answer.Id}); unsaved != nil || changed.Answer.Url != private || changed.Answer.Branch != "next" || changed.Answer.CredentialId != login.Answer.Id || changed.Answer.Name != name {
		t.Errorf("a repository moved behind its login = %+v, %v", changed.Answer, unsaved)
	}
	if changed, unsaved := c.PutGitRepositoriesById(ctx, id, dockhand.PutGitRepositoriesByIdRequest{Name: name + "-renamed"}); unsaved != nil || changed.Answer.Name != name+"-renamed" || changed.Answer.CredentialId != login.Answer.Id {
		t.Errorf("a repository renamed keeps its credential: %+v, %v", changed.Answer, unsaved)
	}
	if changed, unsaved := c.ForgetGitRepositoryCredential(ctx, int(id)); unsaved != nil || changed.Answer.CredentialId != 0 || changed.Answer.Url != private {
		t.Errorf("a repository with its credential taken away = %+v, %v", changed.Answer, unsaved)
	}
	for what, tc := range map[string]struct {
		id      int
		request dockhand.PutGitRepositoriesByIdRequest
		status  int
		want    string
	}{
		"a name that is taken":          {int(id), dockhand.PutGitRepositoriesByIdRequest{Name: name + "-gone"}, 400, "A repository with this name already exists"},
		"a credential there is none of": {int(id), dockhand.PutGitRepositoriesByIdRequest{CredentialId: nobody}, 400, "Invalid credential ID"},
		"one there is none of":          {nobody, dockhand.PutGitRepositoriesByIdRequest{Name: "it"}, 404, "Repository not found"},
	} {
		if _, err = c.PutGitRepositoriesById(ctx, dockhand.GitRepositoryID(tc.id), tc.request); client.StatusCode(err) != tc.status || !strings.Contains(err.Error(), tc.want) {
			t.Errorf("%s, changed = %v, want a %d saying %q", what, err, tc.status, tc.want)
		}
	}
	if _, err = c.ForgetGitRepositoryCredential(ctx, nobody); !client.IsNotFound(err) {
		t.Errorf("a repository there is none of, its credential taken away = %v", err)
	}

	if removed, unremoved := c.DeleteGitRepositoriesById(ctx, id); unremoved != nil || !removed.Answer.Success {
		t.Errorf("a repository removed = %+v, %v", removed.Answer, unremoved)
	}
	if _, err = c.DeleteGitRepositoriesById(ctx, id); !client.IsNotFound(err) || !strings.Contains(err.Error(), "Repository not found") {
		t.Errorf("a repository there is none of, removed = %v", err)
	}
}

// A git stack is made from a repository, read, tried, fetched, deployed, changed and forgotten: what each call answers and refuses; that its files
// are the repository's from each deploy on; that a fetch alone hides a new commit from its webhook; that its webhook takes its secret and no login;
// and that Dockhand forgets one, or the repository under it, and leaves its containers running.
func TestAGitStackMadeFetchedDeployedChangedAndForgotten(t *testing.T) {
	const prefix, nobody, host = "it-git-stack", 987654, testenv.Alpha
	c := dock(t)
	ctx := t.Context()
	env := world.Env(host)
	at := dockhand.GetStacksByNameEnvOptions{Env: env}
	name, other, plain := prefix+"-app", prefix+"-other", prefix+"-plain"
	container := name + "-app-1"
	foldersGone(t, host, name, other, plain)
	for _, stack := range []string{name, other, plain} {
		forgotten(t, host, stack)
	}
	gitGone(t, prefix)
	// the repository as the test starts with it, whatever an earlier stage left there
	first := committed(t, name, map[string]string{"compose.yaml": gitFile(), ".env": "MODE=first\n", "old.txt": "to go\n"}, "new.txt")
	repository, err := c.PostGitRepositories(ctx, dockhand.PostGitRepositoriesRequest{Name: name, Url: world.Repository(name, false)})
	if err != nil {
		t.Fatal(err)
	}
	given := func() []string { return givenTo(t, host, container) }

	// made: refused for what cannot be one, and a name another stack has is refused as a conflict
	for what, tc := range map[string]struct {
		request dockhand.PostGitStacksRequest
		status  int
		want    string
	}{
		"no name":                       {dockhand.PostGitStacksRequest{EnvironmentId: env, RepositoryId: repository.Answer.Id}, 400, "Stack name is required"},
		"a name in capitals":            {dockhand.PostGitStacksRequest{StackName: "It-Git", EnvironmentId: env, RepositoryId: repository.Answer.Id}, 400, "Stack name must be lowercase"},
		"no repository":                 {dockhand.PostGitStacksRequest{StackName: name, EnvironmentId: env}, 400, "Repository URL or existing repository ID is required"},
		"a repository there is none of": {dockhand.PostGitStacksRequest{StackName: name, EnvironmentId: env, RepositoryId: nobody}, 400, "Repository not found"},
		"a webhook with no secret":      {dockhand.PostGitStacksRequest{StackName: name, EnvironmentId: env, RepositoryId: repository.Answer.Id, WebhookEnabled: new(true)}, 400, "A webhook secret is required when the webhook is enabled"},
		"a provider there is none of":   {dockhand.PostGitStacksRequest{StackName: name, EnvironmentId: env, RepositoryId: repository.Answer.Id, SecretProviderId: nobody}, 400, "The selected secret provider no longer exists"},
	} {
		if _, err = c.PostGitStacks(ctx, tc.request); client.StatusCode(err) != tc.status || !strings.Contains(err.Error(), tc.want) {
			t.Errorf("%s, made = %v, want a %d saying %q", what, err, tc.status, tc.want)
		}
	}
	made, err := c.PostGitStacks(ctx, dockhand.PostGitStacksRequest{StackName: name, EnvironmentId: env, RepositoryId: repository.Answer.Id, EnvVars: []dockhand.PostGitStacksRequestEnvVars{{Key: "EXTRA", Value: "kept"}, {Key: "HUSH", Value: "it-not-a-real-secret", IsSecret: new(true)}}})
	if err != nil || made.Answer.Id == 0 || made.Answer.StackName != name || made.Answer.EnvironmentId != env || made.Answer.RepositoryId != repository.Answer.Id || made.Answer.ComposePath != "compose.yaml" || made.Answer.SyncStatus != "pending" || made.Answer.Branch != "" || made.Answer.Repository == nil || made.Answer.Repository.Branch != "main" || made.Answer.AutoUpdate || made.Answer.WebhookEnabled {
		t.Fatalf("a git stack made = %+v, %v", made.Answer, err)
	}
	id := made.Answer.Id
	if _, err = c.PostGitStacks(ctx, dockhand.PostGitStacksRequest{StackName: name, EnvironmentId: env, RepositoryId: repository.Answer.Id}); client.StatusCode(err) != 409 || !strings.Contains(err.Error(), "A stack with this name already exists on this environment") {
		t.Errorf("a name that is taken = %v", err)
	}

	// read: in the list, of every environment and of its own, and alone; a stack made and never deployed is one Dockhand already lists
	for what, options := range map[string]dockhand.GetGitStacksOptions{"every environment": {}, "its own": {Env: env}} {
		listed, unlisted := c.GetGitStacks(ctx, options)
		if unlisted != nil || !slices.ContainsFunc(listed.Answer, func(s dockhand.GetGitStacksAnswer) bool {
			return s.Id == id && s.StackName == name && s.Repository != nil && s.Repository.Name == name
		}) {
			t.Errorf("the git stacks of %s = %+v, %v", what, listed.Answer, unlisted)
		}
	}
	elsewhere := world.Env(testenv.Beta)
	if listed, unlisted := c.GetGitStacks(ctx, dockhand.GetGitStacksOptions{Env: elsewhere}); unlisted != nil || slices.ContainsFunc(listed.Answer, func(s dockhand.GetGitStacksAnswer) bool { return s.Id == id }) {
		t.Errorf("the git stacks of another environment = %+v, %v", listed.Answer, unlisted)
	}
	if got, unread := c.GetGitStacksById(ctx, id); unread != nil || got.Answer.StackName != name || got.Answer.SyncedFiles != "" || got.Answer.LastCommit != "" {
		t.Errorf("a git stack never fetched, read = %+v, %v", got.Answer, unread)
	}
	if _, err = c.GetGitStacksById(ctx, nobody); !client.IsNotFound(err) || !strings.Contains(err.Error(), "Git stack not found") {
		t.Errorf("a git stack there is none of, read = %v", err)
	}
	if listed := stackOf(t, int(env), name); listed == nil || listed.Status != "created" || on(t, host, "container", container) {
		t.Errorf("a git stack never deployed, among the stacks = %+v", listed)
	}
	if vars, unread := c.GetStacksByNameEnv(ctx, name, at); unread != nil || len(vars.Answer.Variables) != 2 || !slices.ContainsFunc(vars.Answer.Variables, func(v dockhand.GetStacksByNameEnvAnswerVariables) bool {
		return v.Key == "HUSH" && v.Value == "***" && v.IsSecret
	}) {
		t.Errorf("the variables Dockhand keeps for it = %+v, %v", vars.Answer, unread)
	}

	// tried, and its .env files, which there are none of until it has been fetched
	if tried, untried := c.PostGitStacksByIdTest(ctx, id); untried != nil || !tried.Answer.Success || tried.Answer.Branch != "main" || tried.Answer.LastCommit != first {
		t.Errorf("a git stack tried = %+v, %v", tried.Answer, untried)
	}
	if _, err = c.PostGitStacksByIdTest(ctx, nobody); !client.IsNotFound(err) {
		t.Errorf("a git stack there is none of, tried = %v", err)
	}
	if _, err = c.GetGitStacksByIdEnvFiles(ctx, id); client.StatusCode(err) != 400 || !strings.Contains(err.Error(), "Repository not synced") {
		t.Errorf("the .env files of a git stack never fetched = %v", err)
	}

	// fetched: Dockhand's own copy is brought to the newest commit and nothing is deployed
	fetched, err := c.PostGitStacksByIdSync(ctx, id)
	if err != nil || !fetched.Answer.Success || fetched.Answer.Commit != first || !fetched.Answer.Updated || len(fetched.Answer.ChangedFiles) != 1 || !strings.Contains(fetched.Answer.ChangedFiles[0], "new clone") || !strings.Contains(fetched.Answer.ComposeContent, "services:") || fetched.Answer.NewFiles[".env"] == "" || !strings.HasPrefix(fetched.Answer.NewCommitFull, first) {
		t.Fatalf("a git stack fetched = %+v, %v", fetched.Answer, err)
	}
	if on(t, host, "container", container) || deployedAt(t, int(id)) != "" {
		t.Error("a fetch alone deployed the stack")
	}
	if _, err = c.PostGitStacksByIdSync(ctx, nobody); !client.IsNotFound(err) {
		t.Errorf("a git stack there is none of, fetched = %v", err)
	}
	if files, unlisted := c.GetGitStacksByIdEnvFiles(ctx, id); unlisted != nil || !slices.Equal(files.Answer.Files, []string{".env"}) {
		t.Errorf("the .env files of a git stack = %+v, %v", files.Answer, unlisted)
	}
	if read, unread := c.PostGitStacksByIdEnvFiles(ctx, id, dockhand.PostGitStacksByIdEnvFilesRequest{Path: ".env"}); unread != nil || read.Answer.Vars["MODE"] != "first" {
		t.Errorf("a .env file of a git stack = %+v, %v", read.Answer, unread)
	}
	if _, err = c.PostGitStacksByIdEnvFiles(ctx, id, dockhand.PostGitStacksByIdEnvFilesRequest{Path: "../../etc/passwd"}); client.StatusCode(err) != 400 || !strings.Contains(err.Error(), "File not found") {
		t.Errorf("a file outside the repository, read = %v", err)
	}

	// deployed with its progress, as a job: the repository's files become the stack's, and its container is given what all three kinds of variable
	// hold. Made to wait instead, that call answers only that there was no result
	started, err := c.PostGitStacksByIdDeployStream(ctx, id, dockhand.PostGitStacksByIdDeployStreamOptions{Accept: dockhand.AcceptJob})
	if err != nil || started.Answer.JobId == "" {
		t.Fatalf("a deploy started = %+v, %v", started.Answer, err)
	}
	job := ended(t, started.Answer.JobId)
	if job.Status != "done" || job.Result["status"] != "complete" || !slices.ContainsFunc(job.Lines, func(l dockhand.GetJobsByIdAnswerLines) bool {
		return strings.Contains(textOf(l.Data["message"]), "File changes: 3 added")
	}) {
		t.Fatalf("the deploy's job = %+v", job)
	}
	if got := given(); !slices.Contains(got, "MODE=first") || !slices.Contains(got, "EXTRA=kept") || !slices.Contains(got, "HUSH=it-not-a-real-secret") {
		t.Errorf("the container was given %v", got)
	}
	if deployedAt(t, int(id)) != first {
		t.Errorf("the stack runs from %q, want %s", deployedAt(t, int(id)), first)
	}
	if waited, unwaited := c.PostGitStacksByIdDeployStream(ctx, id, dockhand.PostGitStacksByIdDeployStreamOptions{}); unwaited != nil || waited.Answer.JobId != "" {
		t.Errorf("the deploy with its progress, made to wait = %+v, %v", waited.Answer, unwaited)
	}
	if _, err = c.PostGitStacksByIdDeployStream(ctx, nobody, dockhand.PostGitStacksByIdDeployStreamOptions{Accept: dockhand.AcceptJob}); !client.IsNotFound(err) {
		t.Errorf("a git stack there is none of, deployed = %v", err)
	}
	// what the ordinary stack calls then say of it: its compose file is there to read, and it is kept as a git stack
	if sources, unread := c.GetStacksSources(ctx, dockhand.GetStacksSourcesOptions{Env: env}); unread != nil || sources.Answer[name].SourceType != "git" {
		t.Errorf("how the stack is kept = %+v, %v", sources.Answer[name], unread)
	}

	// a new commit: seen when it is tried, fetched with what changed and what would go, and still not what runs
	second := committed(t, name, map[string]string{".env": "MODE=second\n", "new.txt": "added\n"}, "old.txt")
	if tried, untried := c.PostGitStacksByIdTest(ctx, id); untried != nil || tried.Answer.LastCommit != second {
		t.Errorf("a git stack with a new commit, tried = %+v, %v", tried.Answer, untried)
	}
	fetched, err = c.PostGitStacksByIdSync(ctx, id)
	if err != nil || fetched.Answer.Commit != second || !slices.Contains(fetched.Answer.ChangedFiles, ".env") || fetched.Answer.DeletionPlan == nil || len(fetched.Answer.DeletionPlan.ToDelete) != 1 || fetched.Answer.DeletionPlan.ToDelete[0].Path != "old.txt" || fetched.Answer.PreviousManifest == nil || !strings.HasPrefix(fetched.Answer.PreviousManifest.Commit, first) {
		t.Errorf("a new commit fetched = %+v, %v", fetched.Answer, err)
	}
	if got, unread := c.GetGitStacksById(ctx, id); unread != nil || got.Answer.LastCommit != second || deployedAt(t, int(id)) != first || !slices.Contains(given(), "MODE=first") {
		t.Errorf("after a fetch alone the stack is at %s and runs from %s with %v: want it fetched at %s and still running from %s", got.Answer.LastCommit, deployedAt(t, int(id)), given(), second, first)
	}

	// its webhook: refused while it is off, takes its secret and no login, and after a fetch alone finds nothing new to deploy
	if _, err = c.GetGitStacksByIdWebhook(ctx, id, dockhand.GetGitStacksByIdWebhookOptions{}); !client.IsForbidden(err) || !strings.Contains(err.Error(), "Webhook is not enabled for this stack") {
		t.Errorf("a webhook that is off, called = %v", err)
	}
	if changed, unsaved := c.PutGitStacksById(ctx, id, dockhand.PutGitStacksByIdRequest{WebhookEnabled: new(true), WebhookSecret: "it-hook-secret"}); unsaved != nil || !changed.Answer.WebhookEnabled || changed.Answer.WebhookSecret != "it-hook-secret" {
		t.Fatalf("a webhook switched on = %+v, %v: the secret is given back whole", changed.Answer, unsaved)
	}
	open, err := dockhand.NewWithoutLogin(world.Server)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = open.GetGitStacksByIdWebhook(ctx, id, dockhand.GetGitStacksByIdWebhookOptions{Secret: "it-wrong"}); client.StatusCode(err) != 401 || !strings.Contains(err.Error(), "Invalid webhook secret") {
		t.Errorf("a webhook called with a wrong secret = %v", err)
	}
	if _, err = open.PostGitStacksByIdWebhook(ctx, id); client.StatusCode(err) != 401 || !strings.Contains(err.Error(), "Invalid webhook signature") {
		t.Errorf("a webhook posted to with nothing signed = %v", err)
	}
	called, err := open.GetGitStacksByIdWebhook(ctx, id, dockhand.GetGitStacksByIdWebhookOptions{Secret: "it-hook-secret"})
	if err != nil || !called.Answer.Success || !called.Answer.Skipped || !strings.Contains(called.Answer.Output, "No changes detected") || !slices.Contains(given(), "MODE=first") {
		t.Errorf("a webhook called with no login, after a fetch alone = %+v, %v, with the container given %v: want it to skip", called.Answer, err, given())
	}
	if _, err = open.GetGitStacksByIdWebhook(ctx, nobody, dockhand.GetGitStacksByIdWebhookOptions{}); !client.IsNotFound(err) {
		t.Errorf("the webhook of a git stack there is none of = %v", err)
	}

	// deployed made to wait, which answers how it went: the new commit runs, and the file gone from the repository is gone from the stack's folder
	deployed, err := c.PostGitStacksByIdDeploy(ctx, id)
	if err != nil || !deployed.Answer.Success || deployed.Answer.ExitCode != 0 || !strings.Contains(deployed.Answer.Command, "compose -p "+name) || !strings.Contains(deployed.Answer.Output, container) {
		t.Fatalf("a git stack deployed = %+v, %v", deployed.Answer, err)
	}
	if got := given(); !slices.Contains(got, "MODE=second") || deployedAt(t, int(id)) != second {
		t.Errorf("after the deploy the container was given %v and the stack runs from %s", got, deployedAt(t, int(id)))
	}
	if folder, unread := world.Laid(ctx, "/app/data/stacks/"+host+"/"+name+"/new.txt"); unread != nil || folder != "added\n" {
		t.Errorf("the file added to the repository, in the stack's folder = %q, %v", folder, unread)
	}
	if _, unread := world.Laid(ctx, "/app/data/stacks/"+host+"/"+name+"/old.txt"); unread == nil {
		t.Error("the file gone from the repository is still in the stack's folder")
	}
	if _, err = c.PostGitStacksByIdDeploy(ctx, nobody); !client.IsNotFound(err) {
		t.Errorf("a git stack there is none of, deployed made to wait = %v", err)
	}

	// changed: a branch the repository does not have is saved, a compose path that is not there too, and each is said only when it is next fetched
	if changed, unsaved := c.PutGitStacksById(ctx, id, dockhand.PutGitStacksByIdRequest{Branch: "it-nope", ComposePath: "it-nope.yaml", EnvFilePath: "it.env", ContextDir: "it", BuildOnDeploy: new(true), NoBuildCache: new(true), RepullImages: new(true), ForceRedeploy: new(true)}); unsaved != nil || changed.Answer.Branch != "it-nope" || changed.Answer.ComposePath != "it-nope.yaml" || changed.Answer.EnvFilePath != "it.env" || changed.Answer.ContextDir != "it" || !changed.Answer.BuildOnDeploy || !changed.Answer.NoBuildCache || !changed.Answer.RepullImages || !changed.Answer.ForceRedeploy {
		t.Errorf("a git stack changed = %+v, %v", changed.Answer, unsaved)
	}
	if tried, untried := c.PostGitStacksByIdTest(ctx, id); untried != nil || tried.Answer.Success || !strings.Contains(tried.Answer.Error, "Branch 'it-nope' not found") {
		t.Errorf("a git stack on a branch there is none of, tried = %+v, %v", tried.Answer, untried)
	}
	if cleared, unsaved := c.ForgetGitStackSettings(ctx, int(id), dockhand.GitStackSettings{Branch: true, EnvFilePath: true, ContextDir: true}); unsaved != nil || cleared.Answer.Branch != "" || cleared.Answer.EnvFilePath != "" || cleared.Answer.ContextDir != "" || cleared.Answer.ComposePath != "it-nope.yaml" {
		t.Errorf("a git stack with its branch and paths taken away = %+v, %v", cleared.Answer, unsaved)
	}
	if refetched, unfetched := c.PostGitStacksByIdSync(ctx, id); unfetched != nil || refetched.Answer.Success || !strings.Contains(refetched.Answer.Error, "Compose file not found: it-nope.yaml") {
		t.Errorf("a git stack whose compose file is not there, fetched = %+v, %v", refetched.Answer, unfetched)
	}
	if got, unread := c.GetGitStacksById(ctx, id); unread != nil || got.Answer.SyncStatus != "error" || !strings.Contains(got.Answer.SyncError, "Compose file not found") {
		t.Errorf("a git stack that could not be fetched, read = %+v, %v", got.Answer, unread)
	}
	if _, err = c.PutGitStacksById(ctx, id, dockhand.PutGitStacksByIdRequest{ComposePath: "compose.yaml", BuildOnDeploy: new(false), NoBuildCache: new(false), RepullImages: new(false), ForceRedeploy: new(false)}); err != nil {
		t.Fatal(err)
	}

	// its schedule is the cron it is given, whatever word is beside it, and one that is no cron is saved and never runs
	if _, err = c.PutGitStacksById(ctx, id, dockhand.PutGitStacksByIdRequest{AutoUpdate: new(true), AutoUpdateSchedule: "daily", AutoUpdateCron: "*/20 * * * *"}); err != nil {
		t.Fatal(err)
	}
	scheduled := func() *dockhand.GetSchedulesAnswerSchedules {
		schedules, unlisted := c.GetSchedules(ctx)
		if unlisted != nil {
			t.Fatal(unlisted)
		}
		i := slices.IndexFunc(schedules.Answer.Schedules, func(s dockhand.GetSchedulesAnswerSchedules) bool {
			return s.Type == "git_stack_sync" && s.EntityName == name
		})
		if i < 0 {
			return nil
		}

		return &schedules.Answer.Schedules[i]
	}
	if schedule := scheduled(); schedule == nil || schedule.Id != int(id) || schedule.CronExpression != "*/20 * * * *" || schedule.NextRun.IsZero() {
		t.Errorf("the schedule of a git stack that updates itself = %+v", schedule)
	}
	if _, err = c.PutGitStacksById(ctx, id, dockhand.PutGitStacksByIdRequest{AutoUpdateCron: "it is no cron"}); err != nil {
		t.Errorf("a schedule that is no cron is saved all the same: %v", err)
	}
	if schedule := scheduled(); schedule == nil || schedule.CronExpression != "it is no cron" || !schedule.NextRun.IsZero() {
		t.Errorf("a schedule that is no cron = %+v, want it kept and never to run", schedule)
	}
	if _, err = c.PutGitStacksById(ctx, id, dockhand.PutGitStacksByIdRequest{AutoUpdate: new(false)}); err != nil || scheduled() != nil {
		t.Errorf("a git stack that no longer updates itself still has a schedule: %+v, %v", scheduled(), err)
	}

	// the variables Dockhand keeps are replaced whole when it is sent any; a secret sent back as it is shown keeps its value
	if _, err = c.PutGitStacksById(ctx, id, dockhand.PutGitStacksByIdRequest{EnvVars: []dockhand.PutGitStacksByIdRequestEnvVars{{Key: "HUSH", Value: "***", IsSecret: new(true)}, {Key: "OTHER", Value: "o"}}}); err != nil {
		t.Fatal(err)
	}
	if vars, unread := c.GetStacksByNameEnv(ctx, name, at); unread != nil || len(vars.Answer.Variables) != 2 || slices.ContainsFunc(vars.Answer.Variables, func(v dockhand.GetStacksByNameEnvAnswerVariables) bool { return v.Key == "EXTRA" }) {
		t.Errorf("the variables after they were sent whole = %+v, %v", vars.Answer, unread)
	}
	if _, err = c.PostGitStacksByIdDeploy(ctx, id); err != nil || !slices.Contains(given(), "HUSH=it-not-a-real-secret") || !slices.Contains(given(), "EXTRA=none") {
		t.Errorf("the container after the variables changed was given %v, %v", given(), err)
	}
	if _, err = c.PutGitStacksById(ctx, id, dockhand.PutGitStacksByIdRequest{EnvVars: []dockhand.PutGitStacksByIdRequestEnvVars{}}); err != nil {
		t.Fatal(err)
	}
	if vars, unread := c.GetStacksByNameEnv(ctx, name, at); unread != nil || len(vars.Answer.Variables) != 0 {
		t.Errorf("the variables after an empty list was sent = %+v, %v: want none", vars.Answer, unread)
	}

	// its webhook goes with its secret, which Dockhand will not forget while the webhook is on
	if cleared, unsaved := c.ForgetGitStackSettings(ctx, int(id), dockhand.GitStackSettings{Webhook: true}); unsaved != nil || cleared.Answer.WebhookEnabled || cleared.Answer.WebhookSecret != "" {
		t.Errorf("a git stack with its webhook taken away = %+v, %v", cleared.Answer, unsaved)
	}
	for what, tc := range map[string]struct {
		id      int
		request dockhand.PutGitStacksByIdRequest
		status  int
		want    string
	}{
		"a webhook with no secret":    {int(id), dockhand.PutGitStacksByIdRequest{WebhookEnabled: new(true)}, 400, "A webhook secret is required when the webhook is enabled"},
		"a provider there is none of": {int(id), dockhand.PutGitStacksByIdRequest{SecretProviderId: nobody}, 400, "The selected secret provider no longer exists"},
		"a name with a space in it":   {int(id), dockhand.PutGitStacksByIdRequest{StackName: "it git"}, 400, "Stack name must start with a letter or number"},
		"one there is none of":        {nobody, dockhand.PutGitStacksByIdRequest{StackName: "it"}, 404, "Git stack not found"},
	} {
		if _, err = c.PutGitStacksById(ctx, dockhand.GitStackID(tc.id), tc.request); client.StatusCode(err) != tc.status || !strings.Contains(err.Error(), tc.want) {
			t.Errorf("%s, changed = %v, want a %d saying %q", what, err, tc.status, tc.want)
		}
	}
	if _, err = c.ForgetGitStackSettings(ctx, nobody, dockhand.GitStackSettings{Branch: true}); !client.IsNotFound(err) {
		t.Errorf("a git stack there is none of, its branch taken away = %v", err)
	}

	// a rename Dockhand refuses, for the name being another stack's, is half done all the same: the git stack carries the other's name from then on
	if _, err = c.PostStacks(ctx, dockhand.PostStacksRequest{Name: plain, Compose: stackFile("plain"), Start: new(false)}, dockhand.PostStacksOptions{Env: env}); err != nil {
		t.Fatal(err)
	}
	if _, err = c.PutGitStacksById(ctx, id, dockhand.PutGitStacksByIdRequest{StackName: plain}); client.StatusCode(err) != 400 || !strings.Contains(err.Error(), "A git stack with this name already exists for this environment") {
		t.Errorf("a rename to a name another stack has = %v", err)
	}
	if got, unread := c.GetGitStacksById(ctx, id); unread != nil || got.Answer.StackName != plain {
		t.Errorf("after the refused rename the git stack is named %q, %v: a 1.0.51 server has renamed it by then", got.Answer.StackName, unread)
	}

	// forgotten while it runs: Dockhand no longer keeps it as a git stack, and its container is left running
	if removed, unremoved := c.DeleteGitStacksById(ctx, id); unremoved != nil || !removed.Answer.Success {
		t.Fatalf("a git stack forgotten = %+v, %v", removed.Answer, unremoved)
	}
	if _, err = c.GetGitStacksById(ctx, id); !client.IsNotFound(err) {
		t.Errorf("a git stack forgotten, read = %v", err)
	}
	if !on(t, host, "container", container) {
		t.Error("forgetting the git stack took its container away")
	}
	if _, err = c.DeleteGitStacksById(ctx, nobody); !client.IsNotFound(err) || !strings.Contains(err.Error(), "Git stack not found") {
		t.Errorf("a git stack there is none of, forgotten = %v", err)
	}

	// a repository removed with a git stack made from it: the git stack goes with it, without a word, and the stack's record stays behind
	second2, err := c.PostGitStacks(ctx, dockhand.PostGitStacksRequest{StackName: other, EnvironmentId: env, RepositoryId: repository.Answer.Id})
	if err != nil {
		t.Fatal(err)
	}
	if removed, unremoved := c.DeleteGitRepositoriesById(ctx, repository.Answer.Id); unremoved != nil || !removed.Answer.Success {
		t.Fatalf("a repository in use, removed = %+v, %v", removed.Answer, unremoved)
	}
	if _, err = c.GetGitStacksById(ctx, second2.Answer.Id); !client.IsNotFound(err) {
		t.Errorf("the git stack of a repository that was removed, read = %v: want it gone", err)
	}
	if sources, unread := c.GetStacksSources(ctx, dockhand.GetStacksSourcesOptions{Env: env}); unread != nil || sources.Answer[other].SourceType != "git" {
		t.Errorf("what is left of it among the stacks = %+v, %v: want a record of a git stack that is no more", sources.Answer[other], unread)
	}
}

// A git stack on the host behind the agent is deployed like any other, and removing the stack itself forgets its git side with it.
func TestAGitStackOnTheAgentsHostRemovedAsAStack(t *testing.T) {
	const prefix, host = "it-git-far", testenv.Beta
	c := dock(t)
	ctx := t.Context()
	env := world.Env(host)
	name := prefix + "-app"
	foldersGone(t, host, name)
	forgotten(t, host, name)
	gitGone(t, prefix)
	commit := committed(t, name, map[string]string{"compose.yaml": gitFile(), ".env": "MODE=far\n"})
	login, err := c.PostGitCredentials(ctx, dockhand.PostGitCredentialsRequest{Name: prefix + "-login", AuthType: "password", Username: world.GitUser, Password: world.GitPassword})
	if err != nil {
		t.Fatal(err)
	}

	// made from an address rather than a saved repository, which saves one under the name given with it
	made, err := c.PostGitStacks(ctx, dockhand.PostGitStacksRequest{StackName: name, EnvironmentId: env, Url: world.Repository(name, true), CredentialId: login.Answer.Id, RepoName: prefix + "-repository"})
	if err != nil || made.Answer.Repository == nil || made.Answer.Repository.Name != prefix+"-repository" || made.Answer.Repository.CredentialId != login.Answer.Id {
		t.Fatalf("a git stack made from an address = %+v, %v", made.Answer, err)
	}
	started, err := c.PostGitStacksByIdDeployStream(ctx, made.Answer.Id, dockhand.PostGitStacksByIdDeployStreamOptions{Accept: dockhand.AcceptJob})
	if err != nil {
		t.Fatal(err)
	}
	if job := ended(t, started.Answer.JobId); job.Result["status"] != "complete" || !slices.Contains(givenTo(t, host, name+"-app-1"), "MODE=far") || deployedAt(t, int(made.Answer.Id)) != commit {
		t.Fatalf("a git stack deployed behind the agent = %+v, with its container given %v", job, givenTo(t, host, name+"-app-1"))
	}

	// the stack removed as a stack: its container goes, and so does the git stack
	if removed, unremoved := c.DeleteStacksByName(ctx, name, dockhand.DeleteStacksByNameOptions{Env: env, Force: new(true)}); unremoved != nil || !removed.Answer.Success {
		t.Fatalf("the stack removed = %+v, %v", removed.Answer, unremoved)
	}
	if _, err = c.GetGitStacksById(ctx, made.Answer.Id); !client.IsNotFound(err) || on(t, host, "container", name+"-app-1") {
		t.Errorf("after the stack was removed its git stack reads %v and its container is there: %v", err, on(t, host, "container", name+"-app-1"))
	}
}

// textOf is a value of a job's free-form answer as text, and nothing for one that is not text.
func textOf(v any) string {
	if text, is := v.(string); is {
		return text
	}

	return ""
}
