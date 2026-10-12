package tools

import (
	"net/http"
	"strings"
	"testing"

	"github.com/katbyte/dockhand-mcp/sdk/dockhandtest"
	"github.com/katbyte/go-kt/mcp/acctest"
)

// What a 1.0.51 server answers about git, with invented names: three credentials, two of which differ only by case; three repositories, two fetched
// with the first credential, and two with a token or a password written into their address; and three git stacks, one on each environment and one
// made with none.
//
//nolint:gosec // G101: made-up addresses, tokens and passwords, as a server would answer them
const (
	threeCredentials  = `[{"id":1,"name":"deploy","authType":"password","username":"deployer","hasPassword":true,"hasSshKey":false,"createdAt":"2026-10-11 05:00:00","updatedAt":"2026-10-11 05:00:00"},{"id":2,"name":"Deploy","authType":"ssh","username":null,"hasPassword":false,"hasSshKey":true,"createdAt":"2026-10-11 05:01:00","updatedAt":"2026-10-11 05:01:00"},{"id":3,"name":"spare","authType":"none","username":null,"hasPassword":false,"hasSshKey":false,"createdAt":"2026-10-11 05:02:00","updatedAt":"2026-10-11 05:02:00"}]`
	deployCredential  = `{"id":1,"name":"deploy","authType":"password","username":"deployer","hasPassword":true,"hasSshKey":false,"createdAt":"2026-10-11 05:00:00","updatedAt":"2026-10-11T05:30:00.000Z"}`
	madeCredential    = `{"id":9,"name":"robot","authType":"password","username":"bot","hasPassword":true,"hasSshKey":false,"createdAt":"2026-10-11 06:00:00","updatedAt":"2026-10-11 06:00:00"}`
	threeRepositories = `[{"id":1,"name":"apps","url":"https://git.example/team/apps.git","branch":"main","credentialId":1,"credentialName":"deploy","credentialAuthType":"password","syncStatus":"pending","createdAt":"2026-10-11 05:10:00","updatedAt":"2026-10-11 05:10:00"},{"id":2,"name":"site","url":"https://sometoken@git.example/team/site.git","branch":"release","credentialId":null,"syncStatus":"pending","createdAt":"2026-10-11 05:11:00","updatedAt":"2026-10-11 05:11:00"},{"id":3,"name":"Site","url":"https://bot:hunter2@git.example/team/other.git","branch":"main","credentialId":1,"credentialName":"deploy","syncStatus":"pending","createdAt":"2026-10-11 05:12:00","updatedAt":"2026-10-11 05:12:00"}]`
	appsRepository    = `{"id":1,"name":"apps","url":"https://git.example/team/apps.git","branch":"main","credentialId":1,"syncStatus":"pending","createdAt":"2026-10-11 05:10:00","updatedAt":"2026-10-11T05:40:00.000Z"}`
	siteRepository    = `{"id":2,"name":"site","url":"https://sometoken@git.example/team/site.git","branch":"release","credentialId":null,"syncStatus":"pending","createdAt":"2026-10-11 05:11:00","updatedAt":"2026-10-11 05:11:00"}`
	madeRepository    = `{"id":9,"name":"docs","url":"https://git.example/team/docs.git","branch":"main","credentialId":3,"syncStatus":"pending","createdAt":"2026-10-11 06:10:00","updatedAt":"2026-10-11 06:10:00"}`
	threeGitStacks    = `[{"id":5,"stackName":"archive","environmentId":1,"repositoryId":1,"branch":null,"composePath":"compose.yaml","envFilePath":null,"autoUpdate":true,"autoUpdateSchedule":"custom","autoUpdateCron":"0 4 * * *","webhookEnabled":true,"webhookSecret":"hook-secret","contextDir":null,"buildOnDeploy":false,"noBuildCache":false,"repullImages":false,"forceRedeploy":false,"lastSync":"2026-10-11T05:26:11.447Z","lastCommit":"11f65b2","syncStatus":"synced","syncError":null,"createdAt":"2026-10-11 05:20:00","updatedAt":"2026-10-11T05:26:11.511Z","repository":{"id":1,"name":"apps","url":"https://git.example/team/apps.git","branch":"main","credentialId":1}},{"id":6,"stackName":"shop","environmentId":2,"repositoryId":1,"branch":"next","composePath":"deploy/compose.yaml","envFilePath":"deploy/shop.env","autoUpdate":false,"autoUpdateSchedule":"daily","autoUpdateCron":"0 3 * * *","webhookEnabled":false,"webhookSecret":null,"contextDir":"deploy","buildOnDeploy":true,"noBuildCache":true,"repullImages":true,"forceRedeploy":true,"lastSync":null,"lastCommit":null,"syncStatus":"error","syncError":"Compose file not found: deploy/compose.yaml","createdAt":"2026-10-11 05:21:00","updatedAt":"2026-10-11 05:21:00","repository":{"id":1,"name":"apps","url":"https://git.example/team/apps.git","branch":"main","credentialId":1}},{"id":7,"stackName":"lost","environmentId":null,"repositoryId":2,"branch":null,"composePath":"compose.yaml","envFilePath":null,"autoUpdate":false,"autoUpdateSchedule":"daily","autoUpdateCron":"0 3 * * *","webhookEnabled":false,"webhookSecret":null,"contextDir":null,"buildOnDeploy":false,"noBuildCache":false,"repullImages":false,"forceRedeploy":false,"lastSync":null,"lastCommit":null,"syncStatus":"pending","syncError":null,"createdAt":"2026-10-11 05:22:00","updatedAt":"2026-10-11 05:22:00","repository":{"id":2,"name":"site","url":"https://sometoken@git.example/team/site.git","branch":"release","credentialId":null}}]`
	fetchable         = `{"success":true,"branch":"main","lastCommit":"d7da76e"}`
	notFetchable      = `{"success":false,"error":"could not read Username for 'https://git.example': terminal prompts disabled"}`
	twoBranches       = `{"branches":[{"name":"main","sha":"d7da76e"},{"name":"next","sha":"0c08231"}]}`
	privateKeyText    = "-----BEGIN OPENSSH PRIVATE KEY-----\nmade-up\n-----END OPENSSH PRIVATE KEY-----\n"

	gitCredentialsCall  = "GET /api/git/credentials"
	gitCredentialMake   = "POST /api/git/credentials"
	deployCall          = "GET /api/git/credentials/1"
	deploySave          = "PUT /api/git/credentials/1"
	deployRemove        = "DELETE /api/git/credentials/1"
	spareRemoveCall     = "DELETE /api/git/credentials/3"
	gitRepositoriesCall = "GET /api/git/repositories"
	gitRepositoryMake   = "POST /api/git/repositories"
	gitRepositoryTry    = "POST /api/git/repositories/test"
	gitBranchesCall     = "POST /api/git/branches"
	gitPreviewCall      = "POST /api/git/preview-env"
	appsCall            = "GET /api/git/repositories/1"
	appsSave            = "PUT /api/git/repositories/1"
	appsTry             = "POST /api/git/repositories/1/test"
	appsRemove          = "DELETE /api/git/repositories/1"
	siteCall            = "GET /api/git/repositories/2"
	siteSave            = "PUT /api/git/repositories/2"
	siteRemove          = "DELETE /api/git/repositories/2"
	otherTry            = "POST /api/git/repositories/3/test"
	gitStacksCall       = "GET /api/git/stacks"
)

// withGit is a Dockhand managing alpha and beta that keeps three git credentials, three repositories and three git stacks.
func withGit(t *testing.T) *dockhandtest.Server {
	t.Helper()

	dock := withProviders(t)
	dock.AnswerWhen(keptHowCall, "env", "2", `{}`)
	dock.Answer(gitCredentialsCall, threeCredentials)
	dock.Answer(gitRepositoriesCall, threeRepositories)
	dock.Answer(gitStacksCall, threeGitStacks)

	return dock
}

func TestGitAddress(t *testing.T) {
	t.Parallel()

	//nolint:gosec // made-up addresses and tokens
	for address, want := range map[string]string{
		"https://git.example/team/apps.git":             "https://git.example/team/apps.git",
		"https://bot:hunter2@git.example/team/apps.git": "https://bot:***@git.example/team/apps.git",
		"https://sometoken@git.example/team/apps.git":   "https://***@git.example/team/apps.git",
		"git@git.example:team/apps.git":                 "git@git.example:team/apps.git",
	} {
		shown, hidden := gitAddress(address, false)
		if shown != want || hidden != (shown != address) {
			t.Errorf("%s is shown as %s (hidden %v), want %s", address, shown, hidden, want)
		}
		if whole, hid := gitAddress(address, true); whole != address || hid {
			t.Errorf("%s asked for whole = %s, %v", address, whole, hid)
		}
	}
	if note := embedsSecret("https://git.example/a.git"); note != "" {
		t.Errorf("an address with nothing written into it = %q", note)
	}
	if said := gitSaid(" fatal: unable to access 'https://bot:hunter2@git.example/a.git/'. "); strings.Contains(said, "hunter2") || !strings.Contains(said, "bot:***@") {
		t.Errorf("what git said, with a password in it = %q", said)
	}
	if got := repositoriesCounted(1) + ", " + repositoriesCounted(3); got != "1 repository, 3 repositories" {
		t.Errorf("repositories counted = %s", got)
	}
}

func TestGitCredentialList(t *testing.T) {
	t.Parallel()

	dock := withGit(t)
	suite := session(t, dock, Options{})

	// by name whatever its case, each with how it signs in and the repositories fetched with it, and never with what it signs in with
	out := suite.Call(t, "git_credential_list", nil)
	rows := acctest.Rows(t, out["credentials"], "credentials")
	if len(rows) != 3 || rows[0]["name"] != "deploy" || rows[1]["name"] != "Deploy" || rows[2]["name"] != "spare" || acctest.Num(t, out["total"], "total") != 3 {
		t.Fatalf("the credentials, by name = %v", out)
	}
	if got := flat(rows[0]); got != `{"changed":"2026-10-11T05:00:00Z","created":"2026-10-11T05:00:00Z","has_password":true,"id":1,"kind":"password","name":"deploy","repositories":["Site","apps"],"username":"deployer"}` {
		t.Errorf("a credential repositories are fetched with = %s", got)
	}
	if rows[1]["kind"] != "ssh" || !acctest.BoolOf(rows[1]["has_key"]) || rows[1]["has_password"] != nil || rows[1]["repositories"] != nil || rows[2]["kind"] != "none" {
		t.Errorf("the credentials nothing uses = %v, %v", rows[1], rows[2])
	}

	// one alone, by name letter for letter, by number, and whatever its case when that is one credential
	for given, want := range map[any]string{"Deploy": "Deploy", 2: "Deploy", "SPARE": "spare", "deploy": "deploy"} {
		one := acctest.Rows(t, suite.Call(t, "git_credential_list", map[string]any{"credential": given})["credentials"], "credentials")
		if len(one) != 1 || one[0]["name"] != want {
			t.Errorf("the credential %v = %v, want %s", given, one, want)
		}
	}
	for what, tc := range map[string]struct {
		given any
		want  string
	}{
		"one there is none of":      {"nope", `no git credential is named or numbered "nope": the Dockhand keeps deploy, Deploy, spare`},
		"two but for their case":    {"DEPLOY", `no git credential is named "DEPLOY" letter for letter, and 2 are but for their case: give the number of one, deploy (1) or Deploy (2)`},
		"no name at all":            {" ", "credential is required: the git credential's name or number"},
		"what is no name or number": {[]any{1}, "credential is [1]: it is a name or a number"},
	} {
		if msg := suite.CallErr(t, "git_credential_list", map[string]any{"credential": tc.given}); !strings.Contains(msg, tc.want) {
			t.Errorf("%s = %q, want %q", what, msg, tc.want)
		}
	}

	// either list that cannot be read is said
	for call, want := range map[string]string{gitCredentialsCall: "the git credentials could not be listed", gitRepositoriesCall: "the git repositories could not be listed"} { //nolint:gosec // G101: the names of two calls, and no credential
		refusing := withGit(t)
		refusing.Fail(call, http.StatusForbidden, notAllowed)
		if msg := session(t, refusing, Options{}).CallErr(t, "git_credential_list", nil); !strings.Contains(msg, want) {
			t.Errorf("%s refused = %q, want %q", call, msg, want)
		}
	}

	dock.Answer(gitCredentialsCall, `[]`)
	if out = suite.Call(t, "git_credential_list", nil); acctest.Num(t, out["total"], "total") != 0 || !strings.Contains(acctest.Str(out["note"]), "keeps no git credential") {
		t.Errorf("a Dockhand with no credential = %v", out)
	}
}

func TestGitCredentialCreate(t *testing.T) {
	t.Parallel()

	dock := withGit(t)
	suite := session(t, dock, everything)
	robot := map[string]any{"name": " robot ", "kind": "password", "username": " bot ", "password": "made-up"}

	// what cannot be a credential is refused before the Dockhand is asked anything
	for what, tc := range map[string]struct {
		args map[string]any
		want string
	}{
		"no name":                      {map[string]any{"name": " ", "kind": "none"}, "name is required"},
		"a kind there is none of":      {map[string]any{"name": "x", "kind": "token"}, `kind is "token": a git credential signs in with password, ssh or none`},
		"a password one with none":     {map[string]any{"name": "x", "kind": "password", "username": "u"}, "password is required for a credential that signs in with one"},
		"an ssh one with no key":       {map[string]any{"name": "x", "kind": "ssh"}, "ssh_key is required for a credential that signs in over ssh"},
		"a key that is none":           {map[string]any{"name": "x", "kind": "ssh", "ssh_key": "nope"}, "ssh_key is not a private key"},
		"a password for one with none": {map[string]any{"name": "x", "kind": "none", "password": "p"}, "username and password are for a credential of the kind password, and this one is none"},
		"a key for a password one":     {map[string]any{"name": "x", "kind": "password", "password": "p", "ssh_passphrase": "s"}, "ssh_key and ssh_passphrase are for a credential of the kind ssh, and this one is password"},
		"a name that is taken":         {over(robot, map[string]any{"name": "spare"}), "a git credential is already named spare: git_credential_edit changes it"},
	} {
		if msg := suite.CallErr(t, "git_credential_create", tc.args); !strings.Contains(msg, tc.want) {
			t.Errorf("%s = %q, want %q", what, msg, tc.want)
		}
	}

	// a dry run says what would be saved and saves nothing
	out := suite.Call(t, "git_credential_create", over(robot, map[string]any{"dry_run": true}))
	if flat(out) != `{"changes":["would save the git credential robot, which signs in with password"],"dry_run":true}` || len(writesOf(dock)) != 0 {
		t.Errorf("a dry run = %s, having sent %v", flat(out), writesOf(dock))
	}

	// saved with its name and user name trimmed, and read back with nothing it signs in with
	dock.Answer(gitCredentialMake, madeCredential)
	dock.Answer("GET /api/git/credentials/9", madeCredential)
	out = suite.Call(t, "git_credential_create", robot)
	if got := flat(sent(t, dock.Last(gitCredentialMake))); got != `{"authType":"password","name":"robot","password":"made-up","username":"bot"}` {
		t.Errorf("a credential was saved as %s", got)
	}
	if got := flat(out); got != `{"changes":["saved the git credential robot, which signs in with password"],"credential":{"changed":"2026-10-11T06:00:00Z","created":"2026-10-11T06:00:00Z","has_password":true,"id":9,"kind":"password","name":"robot","username":"bot"}}` {
		t.Errorf("a credential that was saved = %s", got)
	}

	// one that signs in with a key is sent the key and its passphrase
	suite.Call(t, "git_credential_create", map[string]any{"name": "keyed", "kind": "ssh", "ssh_key": privateKeyText, "ssh_passphrase": "open"})
	if body := sent(t, dock.Last(gitCredentialMake)); body["authType"] != "ssh" || body["sshPrivateKey"] != privateKeyText || body["sshPassphrase"] != "open" || body["password"] != nil {
		t.Errorf("a key credential was saved as %v", body)
	}

	// what cannot be read back is said beside what was done
	dock.Fail(gitRepositoriesCall, http.StatusForbidden, notAllowed)
	if out = suite.Call(t, "git_credential_create", robot); out["credential"] == nil || !strings.Contains(acctest.Str(out["note"]), "the repositories fetched with it could not be listed") {
		t.Errorf("a credential whose repositories cannot be listed = %v", out)
	}
	dock.Fail("GET /api/git/credentials/9", http.StatusNotFound, "Credential not found")
	if out = suite.Call(t, "git_credential_create", robot); out["credential"] != nil || !strings.Contains(acctest.Str(out["note"]), "it was done, and could not be read back") {
		t.Errorf("a credential that cannot be read back = %v", out)
	}
	dock.Fail(gitCredentialMake, http.StatusBadRequest, "Invalid auth type")
	if msg := suite.CallErr(t, "git_credential_create", robot); !strings.Contains(msg, "the git credential robot was not saved") || !strings.Contains(msg, "Invalid auth type") {
		t.Errorf("a credential Dockhand refuses = %q", msg)
	}
	dock.Fail(gitCredentialsCall, http.StatusForbidden, notAllowed)
	if msg := suite.CallErr(t, "git_credential_create", robot); !strings.Contains(msg, "the git credentials could not be listed") {
		t.Errorf("credentials that cannot be listed = %q", msg)
	}
}

func TestGitCredentialEdit(t *testing.T) {
	t.Parallel()

	dock := withGit(t)
	suite := session(t, dock, everything)
	dock.Answer(deployCall, deployCredential)

	for what, tc := range map[string]struct {
		args map[string]any
		want string
	}{
		"nothing to change":            {map[string]any{"credential": "deploy"}, "nothing to change was given: name, kind, username, password, ssh_key or ssh_passphrase"},
		"one there is none of":         {map[string]any{"credential": "nope", "name": "x"}, `no git credential is named or numbered "nope"`},
		"a name that is taken":         {map[string]any{"credential": "deploy", "name": "spare"}, "a git credential is already named spare"},
		"to a key with none kept":      {map[string]any{"credential": "deploy", "kind": "ssh"}, "ssh_key is required for a credential that signs in over ssh"},
		"to a password with none kept": {map[string]any{"credential": "spare", "kind": "password"}, "password is required for a credential that signs in with one"},
		"a user name taken away":       {map[string]any{"credential": "deploy", "username": " "}, "username is empty: Dockhand keeps the user name it is not given another for"},
	} {
		if msg := suite.CallErr(t, "git_credential_edit", tc.args); !strings.Contains(msg, tc.want) {
			t.Errorf("%s = %q, want %q", what, msg, tc.want)
		}
	}

	// what it is already is not saved again
	out := suite.Call(t, "git_credential_edit", map[string]any{"credential": "deploy", "name": "deploy", "kind": "password", "username": "deployer"})
	if !strings.Contains(acctest.Str(out["note"]), "nothing to do: it is that way already") || len(acctest.Strs(t, out["changes"], "changes")) != 0 || len(writesOf(dock)) != 0 {
		t.Errorf("a credential given what it has = %v, having sent %v", out, writesOf(dock))
	}

	// a dry run says what would change and changes nothing
	change := map[string]any{"credential": "deploy", "name": "robot", "username": "bot", "password": "made-up"}
	out = suite.Call(t, "git_credential_edit", over(change, map[string]any{"dry_run": true}))
	if got := strings.Join(acctest.Strs(t, out["changes"], "changes"), "; "); got != "would rename it from deploy to robot; would change its user name to bot; would save a new password" || len(writesOf(dock)) != 0 {
		t.Errorf("a dry run = %q, having sent %v", got, writesOf(dock))
	}

	// saved, and every repository fetched with it tried afterwards: none is lost
	dock.Answer(deploySave, deployCredential)
	dock.Answer(appsTry, fetchable)
	dock.Answer(otherTry, fetchable)
	out = suite.Call(t, "git_credential_edit", change)
	if got := flat(sent(t, dock.Last(deploySave))); got != `{"name":"robot","password":"made-up","username":"bot"}` {
		t.Errorf("a credential was changed with %s", got)
	}
	if got := strings.Join(acctest.Strs(t, out["changes"], "changes"), "; "); got != "renamed it from deploy to robot; changed its user name to bot; saved a new password" || out["note"] != nil || len(dock.Requests(appsTry)) != 1 || len(dock.Requests(otherTry)) != 1 {
		t.Errorf("a credential that was changed = %v", out)
	}

	// one that can no longer be fetched is named, and so are all of them
	dock.Answer(otherTry, notFetchable)
	if out = suite.Call(t, "git_credential_edit", change); !strings.Contains(acctest.Str(out["note"]), "1 of the 2 repositories fetched with it can no longer be fetched: Site (could not read Username for 'https://git.example': terminal prompts disabled)") {
		t.Errorf("one repository lost = %v", out["note"])
	}
	dock.Fail(appsTry, http.StatusInternalServerError, "Failed to test repository")
	if out = suite.Call(t, "git_credential_edit", change); !strings.Contains(acctest.Str(out["note"]), "2 repositories fetched with it can no longer be fetched: apps (could not be tried: ") {
		t.Errorf("every repository lost = %v", out["note"])
	}

	// a new name alone changes nothing about how anything is fetched, so nothing is tried
	tries := len(dock.Requests(appsTry))
	if out = suite.Call(t, "git_credential_edit", map[string]any{"credential": 1, "name": "robot"}); out["note"] != nil || len(dock.Requests(appsTry)) != tries {
		t.Errorf("a rename alone = %v", out)
	}

	// another way to sign in, with what that way needs
	dock.Answer("GET /api/git/credentials/2", `{"id":2,"name":"Deploy","authType":"ssh","hasPassword":false,"hasSshKey":true}`)
	dock.Answer("PUT /api/git/credentials/2", `{"id":2,"name":"Deploy","authType":"ssh","hasPassword":false,"hasSshKey":true}`)
	out = suite.Call(t, "git_credential_edit", map[string]any{"credential": 2, "ssh_key": privateKeyText, "ssh_passphrase": "open"})
	if body := sent(t, dock.Last("PUT /api/git/credentials/2")); body["sshPrivateKey"] != privateKeyText || body["sshPassphrase"] != "open" || acctest.Strs(t, out["changes"], "changes")[0] != "saved a new private key" {
		t.Errorf("a new key was saved as %v, and answered %v", body, out)
	}
	dock.Answer("GET /api/git/credentials/3", `{"id":3,"name":"spare","authType":"password","hasPassword":true,"hasSshKey":false}`)
	dock.Answer("PUT /api/git/credentials/3", `{"id":3,"name":"spare","authType":"password","hasPassword":true,"hasSshKey":false}`)
	out = suite.Call(t, "git_credential_edit", map[string]any{"credential": "spare", "kind": "password", "password": "made-up"})
	if body := sent(t, dock.Last("PUT /api/git/credentials/3")); body["authType"] != "password" || acctest.Strs(t, out["changes"], "changes")[0] != "changed how it signs in from none to password" {
		t.Errorf("another way to sign in was saved as %v, and answered %v", body, out)
	}

	dock.Fail(deploySave, http.StatusBadRequest, "A credential with this name already exists")
	if msg := suite.CallErr(t, "git_credential_edit", change); !strings.Contains(msg, "the git credential deploy was not changed") {
		t.Errorf("a change Dockhand refuses = %q", msg)
	}
	dock.Fail(gitCredentialsCall, http.StatusForbidden, notAllowed)
	if msg := suite.CallErr(t, "git_credential_edit", change); !strings.Contains(msg, "the git credentials could not be listed") {
		t.Errorf("credentials that cannot be listed = %q", msg)
	}
}

func TestGitCredentialDelete(t *testing.T) {
	t.Parallel()

	dock := withGit(t)
	suite := session(t, dock, everything)

	// without confirm, what would go is said and nothing is removed
	out := suite.Call(t, "git_credential_delete", map[string]any{"credential": "spare"})
	if got := flat(out); got != `{"credential":"spare","note":"nothing was removed: confirm removes it","removed":false,"would_go":["the git credential spare, with what Dockhand keeps for it to sign in with"]}` {
		t.Errorf("a credential nothing uses, asked about = %s", got)
	}
	out = suite.Call(t, "git_credential_delete", map[string]any{"credential": "deploy"})
	if got := acctest.Strs(t, out["would_go"], "would_go"); len(got) != 2 || got[1] != "the credential of 2 repositories: Site, apps. Each is left with none" || out["note"] != "nothing was removed: confirm with even_used removes it" || len(writesOf(dock)) != 0 {
		t.Errorf("a credential in use, asked about = %v, having sent %v", out, writesOf(dock))
	}

	// one in use is not removed on confirm alone
	if msg := suite.CallErr(t, "git_credential_delete", map[string]any{"credential": "deploy", "confirm": true}); !strings.Contains(msg, "deploy signs in for 2 repositories: Site, apps. Dockhand would leave each with no credential without a word") || len(writesOf(dock)) != 0 {
		t.Errorf("a credential in use, confirmed alone = %q", msg)
	}

	// removed, and read back as gone
	dock.Answer(deployRemove, done)
	dock.Then(deployRemove, func(dockhandtest.Request) {
		dock.Answer(gitCredentialsCall, `[{"id":3,"name":"spare","authType":"none","hasPassword":false,"hasSshKey":false}]`)
	})
	out = suite.Call(t, "git_credential_delete", map[string]any{"credential": "deploy", "confirm": true, "even_used": true})
	if !acctest.BoolOf(out["removed"]) || out["would_go"] != nil || out["note"] != "2 repositories left with no credential: Site, apps" {
		t.Errorf("a credential in use, removed = %v", out)
	}
	dock.Then(deployRemove, nil)

	dock.Answer(spareRemoveCall, done)
	if msg := suite.CallErr(t, "git_credential_delete", map[string]any{"credential": "spare", "confirm": true}); !strings.Contains(msg, "Dockhand answered that spare was removed, and still lists it") {
		t.Errorf("a credential still listed = %q", msg)
	}
	dock.Then(spareRemoveCall, func(dockhandtest.Request) { dock.Fail(gitCredentialsCall, http.StatusForbidden, notAllowed) })
	if out = suite.Call(t, "git_credential_delete", map[string]any{"credential": "spare", "confirm": true}); !acctest.BoolOf(out["removed"]) || !strings.Contains(acctest.Str(out["note"]), "it was removed, and the credentials could not be listed afterwards") {
		t.Errorf("a credential removed and not read back = %v", out)
	}
	dock.Then(spareRemoveCall, nil)
	dock.Answer(gitCredentialsCall, threeCredentials)

	dock.Fail(spareRemoveCall, http.StatusNotFound, "Credential not found")
	if msg := suite.CallErr(t, "git_credential_delete", map[string]any{"credential": "spare", "confirm": true}); !strings.Contains(msg, "the git credential spare was not removed") {
		t.Errorf("a removal Dockhand refuses = %q", msg)
	}
	if msg := suite.CallErr(t, "git_credential_delete", map[string]any{"credential": "nope"}); !strings.Contains(msg, `no git credential is named or numbered "nope"`) {
		t.Errorf("a credential there is none of = %q", msg)
	}
	dock.Fail(gitRepositoriesCall, http.StatusForbidden, notAllowed)
	if msg := suite.CallErr(t, "git_credential_delete", map[string]any{"credential": "spare"}); !strings.Contains(msg, "the git repositories could not be listed") || !strings.Contains(msg, "so nothing was removed") {
		t.Errorf("repositories that cannot be listed = %q", msg)
	}
	dock.Fail(gitCredentialsCall, http.StatusForbidden, notAllowed)
	if msg := suite.CallErr(t, "git_credential_delete", map[string]any{"credential": "spare"}); !strings.Contains(msg, "the git credentials could not be listed") {
		t.Errorf("credentials that cannot be listed = %q", msg)
	}
}

func TestGitRepositoryList(t *testing.T) {
	t.Parallel()

	dock := withGit(t)
	suite := session(t, dock, Options{})

	// by name, each with where it is fetched from, its credential and the git stacks made from it; what is written into an address is hidden
	out := suite.Call(t, "git_repository_list", nil)
	rows := acctest.Rows(t, out["repositories"], "repositories")
	if len(rows) != 3 || rows[0]["name"] != "apps" || rows[1]["name"] != "site" || rows[2]["name"] != "Site" || acctest.Num(t, out["total"], "total") != 3 {
		t.Fatalf("the repositories, by name = %v", out)
	}
	if got := flat(rows[0]); got != `{"branch":"main","changed":"2026-10-11T05:10:00Z","created":"2026-10-11T05:10:00Z","credential":"deploy","id":1,"name":"apps","stacks":[{"env":"alpha","stack":"archive"},{"env":"beta","stack":"shop"}],"url":"https://git.example/team/apps.git"}` {
		t.Errorf("a repository git stacks are made from = %s", got)
	}
	if rows[1]["url"] != "https://***@git.example/team/site.git" || rows[1]["credential"] != nil || flat(acctest.Rows(t, rows[1]["stacks"], "stacks")[0]) != `{"env":"(none)","stack":"lost"}` || rows[2]["url"] != "https://bot:***@git.example/team/other.git" { //nolint:gosec // G101: a made-up address, with what was written into it hidden
		t.Errorf("the repositories with something written into their address = %v, %v", rows[1], rows[2])
	}
	if acctest.Num(t, out["hidden"], "hidden") != 2 || !strings.Contains(acctest.Str(out["note"]), "2 secrets are hidden, written into an address") {
		t.Errorf("what is hidden is said to be: %v", out)
	}

	// one alone, and its address whole when asked for
	shown := suite.Call(t, "git_repository_list", map[string]any{"repository": 3, "reveal_secrets": true})
	if one := acctest.Rows(t, shown["repositories"], "repositories"); len(one) != 1 || one[0]["url"] != "https://bot:hunter2@git.example/team/other.git" || shown["hidden"] != nil || shown["note"] != nil { //nolint:gosec // G101: a made-up password in a made-up address
		t.Errorf("a repository with its address asked for whole = %v", shown)
	}
	if msg := suite.CallErr(t, "git_repository_list", map[string]any{"repository": "SITE"}); !strings.Contains(msg, `no git repository is named "SITE" letter for letter, and 2 are but for their case: give the number of one, site (2) or Site (3)`) {
		t.Errorf("two repositories but for their case = %q", msg)
	}

	// any part of it that cannot be read is said
	for call, want := range map[string]string{ //nolint:gosec // G101: the names of calls, and no credential
		gitRepositoriesCall:     "the git repositories could not be listed",
		gitCredentialsCall:      "the git credentials could not be listed",
		gitStacksCall:           "the git stacks could not be listed",
		"GET /api/environments": "the environments could not be listed",
	} {
		refusing := withGit(t)
		refusing.Fail(call, http.StatusForbidden, notAllowed)
		if msg := session(t, refusing, Options{}).CallErr(t, "git_repository_list", nil); !strings.Contains(msg, want) {
			t.Errorf("%s refused = %q, want %q", call, msg, want)
		}
	}

	dock.Answer(gitRepositoriesCall, `[]`)
	if out = suite.Call(t, "git_repository_list", nil); acctest.Num(t, out["total"], "total") != 0 || !strings.Contains(acctest.Str(out["note"]), "keeps no git repository") {
		t.Errorf("a Dockhand with no repository = %v", out)
	}
}

func TestGitRepositoryTest(t *testing.T) {
	t.Parallel()

	dock := withGit(t)
	suite := session(t, dock, Options{})

	// one that is not saved, with the credential named and the branch given
	dock.Answer(gitRepositoryTry, `{"success":true,"branch":"next","lastCommit":"0c08231"}`)
	dock.Answer(gitBranchesCall, twoBranches)
	out := suite.Call(t, "git_repository_test", map[string]any{"url": " https://git.example/team/docs.git ", "branch": "next", "credential": "spare"})
	if got := flat(sent(t, dock.Last(gitRepositoryTry))); got != `{"branch":"next","credentialId":3,"url":"https://git.example/team/docs.git"}` {
		t.Errorf("an unsaved repository was tried with %s", got)
	}
	if got := flat(out); got != `{"branch":"next","branches":[{"commit":"d7da76e","name":"main"},{"commit":"0c08231","name":"next"}],"commit":"0c08231","reached":true}` {
		t.Errorf("an unsaved repository that can be fetched = %s", got)
	}

	// one that cannot be fetched says why, and lists no branches when they cannot be asked for either
	dock.Answer(gitRepositoryTry, notFetchable)
	dock.Fail(gitBranchesCall, http.StatusInternalServerError, "Failed to fetch branches")
	if out = suite.Call(t, "git_repository_test", map[string]any{"url": "https://git.example/team/docs.git"}); acctest.BoolOf(out["reached"]) || out["branch"] != "main" || out["branches"] != nil || !strings.Contains(acctest.Str(out["error"]), "could not read Username") {
		t.Errorf("an unsaved repository that cannot be fetched = %v", out)
	}
	if body := sent(t, dock.Last(gitRepositoryTry)); body["credentialId"] != nil || body["branch"] != nil {
		t.Errorf("an unsaved repository with no credential or branch was tried with %v", body)
	}

	// a saved one is tried as it is saved
	dock.Answer(appsTry, fetchable)
	dock.Answer(gitBranchesCall, twoBranches)
	out = suite.Call(t, "git_repository_test", map[string]any{"repository": "apps"})
	if got := flat(out); got != `{"branch":"main","branches":[{"commit":"d7da76e","name":"main"},{"commit":"0c08231","name":"next"}],"commit":"d7da76e","reached":true,"repository":"apps"}` || flat(sent(t, dock.Last(gitBranchesCall))) != `{"repositoryId":1}` {
		t.Errorf("a saved repository that can be fetched = %s", got)
	}
	dock.Answer(appsTry, notFetchable)
	if out = suite.Call(t, "git_repository_test", map[string]any{"repository": 1}); acctest.BoolOf(out["reached"]) || out["branch"] != "main" || !strings.Contains(acctest.Str(out["error"]), "terminal prompts disabled") {
		t.Errorf("a saved repository that cannot be fetched = %v", out)
	}

	for what, tc := range map[string]struct {
		args map[string]any
		want string
	}{
		"neither":                       {nil, "repository or url is required"},
		"a saved one with an address":   {map[string]any{"repository": "apps", "url": "https://git.example/x.git"}, "url, branch and credential are for a repository that is not saved"},
		"a saved one there is none of":  {map[string]any{"repository": "nope"}, `no git repository is named or numbered "nope"`},
		"a credential there is none of": {map[string]any{"url": "https://git.example/x.git", "credential": "nope"}, `no git credential is named or numbered "nope"`},
	} {
		if msg := suite.CallErr(t, "git_repository_test", tc.args); !strings.Contains(msg, tc.want) {
			t.Errorf("%s = %q, want %q", what, msg, tc.want)
		}
	}
	dock.Fail(appsTry, http.StatusNotFound, "Repository not found")
	if msg := suite.CallErr(t, "git_repository_test", map[string]any{"repository": "apps"}); !strings.Contains(msg, "the repository apps could not be tried") {
		t.Errorf("a saved repository that cannot be tried = %q", msg)
	}
	dock.Fail(gitRepositoryTry, http.StatusNotFound, "Credential not found")
	if msg := suite.CallErr(t, "git_repository_test", map[string]any{"url": "https://git.example/x.git"}); !strings.Contains(msg, "the repository could not be tried") {
		t.Errorf("an unsaved repository that cannot be tried = %q", msg)
	}
	dock.Fail(gitCredentialsCall, http.StatusForbidden, notAllowed)
	if msg := suite.CallErr(t, "git_repository_test", map[string]any{"url": "https://git.example/x.git", "credential": "spare"}); !strings.Contains(msg, "the git credentials could not be listed") {
		t.Errorf("credentials that cannot be listed = %q", msg)
	}
	dock.Fail(gitRepositoriesCall, http.StatusForbidden, notAllowed)
	if msg := suite.CallErr(t, "git_repository_test", map[string]any{"repository": "apps"}); !strings.Contains(msg, "the git repositories could not be listed") {
		t.Errorf("repositories that cannot be listed = %q", msg)
	}
}

func TestGitRepositoryVariables(t *testing.T) {
	t.Parallel()

	dock := withGit(t)
	suite := session(t, dock, Options{})

	// the variables of both files, each with where it is from, and a value with a secret's name hidden
	dock.Answer(gitPreviewCall, `{"vars":{"MODE":"live","API_TOKEN":"made-up","PORT":"8080"},"sources":{"MODE":".env","API_TOKEN":"envFile","PORT":".env"}}`)
	out := suite.Call(t, "git_repository_variables", map[string]any{"repository": "apps", "compose_path": " deploy/compose.yaml ", "env_file": "deploy/shop.env"})
	if got := flat(sent(t, dock.Last(gitPreviewCall))); got != `{"composePath":"deploy/compose.yaml","envFilePath":"deploy/shop.env","repositoryId":1}` {
		t.Errorf("a repository's variables were asked for with %s", got)
	}
	if got := flat(out); got != `{"hidden":1,"note":"1 secret is hidden, told by the names beside them: reveal_secrets shows it","repository":"apps","total":3,"variables":[{"from":"envFile","name":"API_TOKEN","value":"***"},{"from":".env","name":"MODE","value":"live"},{"from":".env","name":"PORT","value":"8080"}]}` {
		t.Errorf("a repository's variables = %s", got)
	}
	shown := suite.Call(t, "git_repository_variables", map[string]any{"repository": "apps", "reveal_secrets": true})
	if rows := acctest.Rows(t, shown["variables"], "variables"); rows[0]["value"] != "made-up" || shown["hidden"] != nil || shown["note"] != nil || sent(t, dock.Last(gitPreviewCall))["composePath"] != "compose.yaml" {
		t.Errorf("a repository's variables asked for whole = %v", shown)
	}

	dock.Answer(gitPreviewCall, `{"vars":{},"sources":{}}`)
	if out = suite.Call(t, "git_repository_variables", map[string]any{"repository": "apps"}); acctest.Num(t, out["total"], "total") != 0 || !strings.Contains(acctest.Str(out["note"]), "the repository has no .env file there, or an empty one") {
		t.Errorf("a repository with no .env file = %v", out)
	}

	// what git says when the repository cannot be fetched is passed on with any password in it hidden
	dock.Fail(gitPreviewCall, http.StatusBadRequest, "Failed to clone repository: fatal: unable to access 'https://bot:hunter2@git.example/team/apps.git/'")
	if msg := suite.CallErr(t, "git_repository_variables", map[string]any{"repository": "apps"}); !strings.Contains(msg, "the variables of apps could not be read") || strings.Contains(msg, "hunter2") {
		t.Errorf("a repository that cannot be fetched = %q", msg)
	}
	if msg := suite.CallErr(t, "git_repository_variables", map[string]any{"repository": "nope"}); !strings.Contains(msg, `no git repository is named or numbered "nope"`) {
		t.Errorf("a repository there is none of = %q", msg)
	}
	dock.Fail(gitRepositoriesCall, http.StatusForbidden, notAllowed)
	if msg := suite.CallErr(t, "git_repository_variables", map[string]any{"repository": "apps"}); !strings.Contains(msg, "the git repositories could not be listed") {
		t.Errorf("repositories that cannot be listed = %q", msg)
	}
}

func TestGitRepositoryCreate(t *testing.T) {
	t.Parallel()

	dock := withGit(t)
	suite := session(t, dock, everything)
	docs := map[string]any{"name": " docs ", "url": " https://git.example/team/docs.git ", "credential": "spare"}

	for what, tc := range map[string]struct {
		args map[string]any
		want string
	}{
		"no name":                       {map[string]any{"name": " ", "url": "https://git.example/x.git"}, "name is required"},
		"no address":                    {map[string]any{"name": "x", "url": " "}, "url is required"},
		"a name that is taken":          {over(docs, map[string]any{"name": "apps"}), "a git repository is already named apps: git_repository_edit changes it"},
		"a credential there is none of": {over(docs, map[string]any{"credential": "nope"}), `no git credential is named or numbered "nope"`},
	} {
		if msg := suite.CallErr(t, "git_repository_create", tc.args); !strings.Contains(msg, tc.want) {
			t.Errorf("%s = %q, want %q", what, msg, tc.want)
		}
	}

	// one that cannot be fetched is not saved, unless it is asked for all the same
	dock.Answer(gitRepositoryTry, notFetchable)
	if msg := suite.CallErr(t, "git_repository_create", docs); !strings.Contains(msg, "Dockhand could not fetch the repository, so nothing was saved: could not read Username") || !strings.Contains(msg, "save_unreachable saves it all the same") || len(dock.Requests(gitRepositoryMake)) != 0 {
		t.Errorf("a repository that cannot be fetched = %q", msg)
	}
	out := suite.Call(t, "git_repository_create", over(docs, map[string]any{"save_unreachable": true, "dry_run": true}))
	if !strings.Contains(acctest.Str(out["note"]), "it would be saved as save_unreachable asks though Dockhand cannot fetch it") || len(dock.Requests(gitRepositoryMake)) != 0 {
		t.Errorf("a dry run of one that cannot be fetched = %v", out)
	}
	dock.Answer(gitRepositoryMake, madeRepository)
	dock.Answer("GET /api/git/repositories/9", madeRepository)
	if out = suite.Call(t, "git_repository_create", over(docs, map[string]any{"save_unreachable": true})); !strings.Contains(acctest.Str(out["note"]), "it was saved as save_unreachable asks though Dockhand cannot fetch it") || out["repository"] == nil {
		t.Errorf("one that cannot be fetched, saved all the same = %v", out)
	}

	// tried as it would be saved, then saved, and read back
	dock.Answer(gitRepositoryTry, fetchable)
	out = suite.Call(t, "git_repository_create", over(docs, map[string]any{"dry_run": true}))
	if got := flat(out); got != `{"changes":["would save the git repository docs, the branch main of https://git.example/team/docs.git, fetched with the credential spare"],"commit":"d7da76e","dry_run":true}` {
		t.Errorf("a dry run = %s", got)
	}
	out = suite.Call(t, "git_repository_create", docs)
	if got := flat(sent(t, dock.Last(gitRepositoryTry))); got != `{"branch":"main","credentialId":3,"url":"https://git.example/team/docs.git"}` {
		t.Errorf("a repository was tried with %s", got)
	}
	if got := flat(sent(t, dock.Last(gitRepositoryMake))); got != `{"branch":"main","credentialId":3,"name":"docs","url":"https://git.example/team/docs.git"}` {
		t.Errorf("a repository was saved as %s", got)
	}
	if got := flat(out); got != `{"changes":["saved the git repository docs, the branch main of https://git.example/team/docs.git, fetched with the credential spare"],"commit":"d7da76e","repository":{"branch":"main","changed":"2026-10-11T06:10:00Z","created":"2026-10-11T06:10:00Z","credential":"spare","id":9,"name":"docs","url":"https://git.example/team/docs.git"}}` {
		t.Errorf("a repository that was saved = %s", got)
	}

	// one anyone may read is saved with no credential, and a password written into its address is said to be a poor place for one
	out = suite.Call(t, "git_repository_create", map[string]any{"name": "open", "url": "https://bot:hunter2@git.example/team/open.git", "branch": "release"}) //nolint:gosec // G101: a made-up password in a made-up address
	if body := sent(t, dock.Last(gitRepositoryMake)); body["credentialId"] != nil || body["branch"] != "release" || !strings.Contains(acctest.Str(out["note"]), "the address has a password or token written into it") || !strings.Contains(acctest.Strs(t, out["changes"], "changes")[0], "https://bot:***@git.example/team/open.git") {
		t.Errorf("a repository with a password in its address was saved as %v, and answered %v", body, out)
	}

	// what cannot be read back is said beside what was done
	dock.Fail(gitStacksCall, http.StatusForbidden, notAllowed)
	if out = suite.Call(t, "git_repository_create", docs); out["repository"] != nil || !strings.Contains(acctest.Str(out["note"]), "it was done, and could not be read back: the git stacks could not be listed") {
		t.Errorf("a repository whose stacks cannot be listed = %v", out)
	}
	dock.Fail("GET /api/git/repositories/9", http.StatusNotFound, "Repository not found")
	if out = suite.Call(t, "git_repository_create", docs); out["repository"] != nil || !strings.Contains(acctest.Str(out["note"]), "it was done, and could not be read back") {
		t.Errorf("a repository that cannot be read back = %v", out)
	}
	dock.Fail(gitRepositoryMake, http.StatusBadRequest, "A repository with this name already exists")
	if msg := suite.CallErr(t, "git_repository_create", docs); !strings.Contains(msg, "the git repository docs was not saved") {
		t.Errorf("a repository Dockhand refuses = %q", msg)
	}
	dock.Fail(gitRepositoryTry, http.StatusNotFound, "Credential not found")
	if msg := suite.CallErr(t, "git_repository_create", docs); !strings.Contains(msg, "the repository could not be tried, so nothing was saved") {
		t.Errorf("a repository that cannot be tried = %q", msg)
	}
	dock.Fail(gitCredentialsCall, http.StatusForbidden, notAllowed)
	if msg := suite.CallErr(t, "git_repository_create", docs); !strings.Contains(msg, "the git credentials could not be listed") {
		t.Errorf("credentials that cannot be listed = %q", msg)
	}
	dock.Fail(gitRepositoriesCall, http.StatusForbidden, notAllowed)
	if msg := suite.CallErr(t, "git_repository_create", docs); !strings.Contains(msg, "the git repositories could not be listed") {
		t.Errorf("repositories that cannot be listed = %q", msg)
	}
}

func TestGitRepositoryEdit(t *testing.T) {
	t.Parallel()

	dock := withGit(t)
	suite := session(t, dock, everything)
	dock.Answer(appsCall, appsRepository)
	dock.Answer(siteCall, siteRepository)

	for what, tc := range map[string]struct {
		args map[string]any
		want string
	}{
		"nothing to change":             {map[string]any{"repository": "apps"}, "nothing to change was given: name, url, branch or credential"},
		"one there is none of":          {map[string]any{"repository": "nope", "name": "x"}, `no git repository is named or numbered "nope"`},
		"a name that is taken":          {map[string]any{"repository": "apps", "name": "site"}, "a git repository is already named site"},
		"a credential there is none of": {map[string]any{"repository": "apps", "credential": "nope"}, `no git credential is named or numbered "nope"`},
	} {
		if msg := suite.CallErr(t, "git_repository_edit", tc.args); !strings.Contains(msg, tc.want) {
			t.Errorf("%s = %q, want %q", what, msg, tc.want)
		}
	}

	// what it is already is not tried or saved again: the credential it has, no credential for one that has none, its own name, address and branch
	for _, same := range []map[string]any{
		{"repository": "apps", "name": "apps", "url": "https://git.example/team/apps.git", "branch": "main", "credential": "deploy"},
		{"repository": 2, "credential": ""},
	} {
		out := suite.Call(t, "git_repository_edit", same)
		if !strings.Contains(acctest.Str(out["note"]), "nothing to do: it is that way already") || len(acctest.Strs(t, out["changes"], "changes")) != 0 || len(writesOf(dock)) != 0 {
			t.Errorf("a repository given what it has = %v, having sent %v", out, writesOf(dock))
		}
	}

	// a new name alone fetches nothing differently, so nothing is tried
	dock.Answer(appsSave, appsRepository)
	out := suite.Call(t, "git_repository_edit", map[string]any{"repository": "apps", "name": "programs"})
	if got := flat(sent(t, dock.Last(appsSave))); got != `{"name":"programs"}` || len(dock.Requests(gitRepositoryTry)) != 0 || acctest.Strs(t, out["changes"], "changes")[0] != "renamed it from apps to programs" {
		t.Errorf("a rename alone was saved as %s, and answered %v", got, out)
	}
	if !strings.Contains(acctest.Str(out["note"]), "the next deploy of each git stack made from it fetches it this way: archive on alpha, shop on beta") {
		t.Errorf("the git stacks made from it are named: %v", out["note"])
	}

	// a change to how it is fetched is tried as it would be saved, and one that cannot be fetched is refused
	move := map[string]any{"repository": "apps", "url": "https://bot:hunter2@git.example/team/moved.git", "branch": "next", "credential": "spare"} //nolint:gosec // G101: a made-up password in a made-up address
	dock.Answer(gitRepositoryTry, notFetchable)
	if msg := suite.CallErr(t, "git_repository_edit", move); !strings.Contains(msg, "Dockhand could not fetch apps that way, so nothing was changed") || !strings.Contains(msg, "save_unreachable saves it all the same") || len(dock.Requests(appsSave)) != 1 {
		t.Errorf("a change that cannot be fetched = %q", msg)
	}
	if got := flat(sent(t, dock.Last(gitRepositoryTry))); got != `{"branch":"next","credentialId":3,"url":"https://bot:hunter2@git.example/team/moved.git"}` { //nolint:gosec // G101: a made-up password in a made-up address
		t.Errorf("a change was tried with %s", got)
	}
	out = suite.Call(t, "git_repository_edit", over(move, map[string]any{"save_unreachable": true, "dry_run": true}))
	if got := strings.Join(acctest.Strs(t, out["changes"], "changes"), "; "); got != "would have it fetched with the credential spare; would change its address to https://bot:***@git.example/team/moved.git; would change the branch it follows from main to next" || len(dock.Requests(appsSave)) != 1 {
		t.Errorf("a dry run = %q", got)
	}
	if note := acctest.Str(out["note"]); !strings.Contains(note, "the address has a password or token written into it") || !strings.Contains(note, "it would be saved as save_unreachable asks though Dockhand cannot fetch the repository that way") {
		t.Errorf("a dry run of a change that cannot be fetched says so: %q", note)
	}
	if out = suite.Call(t, "git_repository_edit", over(move, map[string]any{"save_unreachable": true})); !strings.Contains(acctest.Str(out["note"]), "it was saved as save_unreachable asks") {
		t.Errorf("a change that cannot be fetched, saved all the same = %v", out)
	}
	if got := flat(sent(t, dock.Last(appsSave))); got != `{"branch":"next","credentialId":3,"url":"https://bot:hunter2@git.example/team/moved.git"}` { //nolint:gosec // G101: a made-up password in a made-up address
		t.Errorf("a change was saved as %s", got)
	}
	dock.Answer(gitRepositoryTry, fetchable)
	if out = suite.Call(t, "git_repository_edit", map[string]any{"repository": "apps", "branch": "next"}); out["commit"] != "d7da76e" || flat(sent(t, dock.Last(appsSave))) != `{"branch":"next"}` {
		t.Errorf("a branch that can be fetched = %v", out)
	}

	// its credential taken away is sent apart, as nothing, after it was tried with none
	out = suite.Call(t, "git_repository_edit", map[string]any{"repository": "apps", "credential": " ", "name": "programs"})
	saves := dock.Requests(appsSave)
	if got := strings.Join(acctest.Strs(t, out["changes"], "changes"), "; "); got != "had it fetched with no credential; renamed it from apps to programs" || saves[len(saves)-2].Body != `{"name":"programs"}` || saves[len(saves)-1].Body != `{"credentialId":null}` || sent(t, dock.Last(gitRepositoryTry))["credentialId"] != nil {
		t.Errorf("a credential taken away = %q, sent as %v", got, saves[len(saves)-2:])
	}
	before := len(dock.Requests(appsSave))
	if out = suite.Call(t, "git_repository_edit", map[string]any{"repository": "apps", "credential": ""}); len(dock.Requests(appsSave)) != before+1 || dock.Last(appsSave).Body != `{"credentialId":null}` {
		t.Errorf("a credential taken away alone = %v", out)
	}

	// one that is not taken away is said not to have been, with what was saved before it
	dock.Then(appsSave, func(r dockhandtest.Request) {
		if strings.Contains(r.Body, "null") {
			dock.Fail(appsSave, http.StatusInternalServerError, "Failed to update repository")
		}
	})
	msg := suite.CallErr(t, "git_repository_edit", map[string]any{"repository": "apps", "credential": "", "name": "programs"})
	if !strings.Contains(msg, "the credential of apps was not taken away") {
		t.Errorf("a credential that is not taken away = %q", msg)
	}
	dock.Then(appsSave, nil)

	dock.Fail(appsSave, http.StatusBadRequest, "A repository with this name already exists")
	if msg := suite.CallErr(t, "git_repository_edit", map[string]any{"repository": "apps", "name": "programs"}); !strings.Contains(msg, "the git repository apps was not changed") {
		t.Errorf("a change Dockhand refuses = %q", msg)
	}
	dock.Fail(gitRepositoryTry, http.StatusNotFound, "Credential not found")
	if msg := suite.CallErr(t, "git_repository_edit", map[string]any{"repository": "apps", "branch": "next"}); !strings.Contains(msg, "the change could not be tried, so nothing was changed") {
		t.Errorf("a change that cannot be tried = %q", msg)
	}
	dock.Fail(gitCredentialsCall, http.StatusForbidden, notAllowed)
	if msg := suite.CallErr(t, "git_repository_edit", map[string]any{"repository": "apps", "credential": "spare"}); !strings.Contains(msg, "the git credentials could not be listed") {
		t.Errorf("credentials that cannot be listed = %q", msg)
	}
	dock.Fail(gitRepositoriesCall, http.StatusForbidden, notAllowed)
	if msg := suite.CallErr(t, "git_repository_edit", map[string]any{"repository": "apps", "name": "x"}); !strings.Contains(msg, "the git repositories could not be listed") {
		t.Errorf("repositories that cannot be listed = %q", msg)
	}
}

func TestGitRepositoryDelete(t *testing.T) {
	t.Parallel()

	dock := withGit(t)
	suite := session(t, dock, everything)

	// without confirm, what would go is said and nothing is removed
	out := suite.Call(t, "git_repository_delete", map[string]any{"repository": "Site"})
	if got := flat(out); got != `{"note":"nothing was removed: confirm removes it","removed":false,"repository":"Site","would_go":["the git repository Site, as Dockhand keeps it: its address, its branch and which credential it is fetched with"]}` {
		t.Errorf("a repository nothing is made from, asked about = %s", got)
	}
	out = suite.Call(t, "git_repository_delete", map[string]any{"repository": "apps"})
	if got := acctest.Strs(t, out["would_go"], "would_go"); len(got) != 2 || got[1] != "2 git stacks deployed from it: archive on alpha, shop on beta. Dockhand forgets each, with its schedule and its webhook, and leaves its containers running" || out["note"] != "nothing was removed: confirm with even_used removes it" || len(writesOf(dock)) != 0 {
		t.Errorf("a repository in use, asked about = %v, having sent %v", out, writesOf(dock))
	}

	// one in use is not removed on confirm alone
	if msg := suite.CallErr(t, "git_repository_delete", map[string]any{"repository": "apps", "confirm": true}); !strings.Contains(msg, "apps has 2 git stacks deployed from it: archive on alpha, shop on beta. Dockhand would forget each without a word") || len(writesOf(dock)) != 0 {
		t.Errorf("a repository in use, confirmed alone = %q", msg)
	}

	// removed, and read back as gone
	dock.Answer(appsRemove, done)
	dock.Then(appsRemove, func(dockhandtest.Request) { dock.Answer(gitRepositoriesCall, `[]`) })
	out = suite.Call(t, "git_repository_delete", map[string]any{"repository": "apps", "confirm": true, "even_used": true})
	if !acctest.BoolOf(out["removed"]) || out["would_go"] != nil || out["note"] != "2 git stacks forgotten with it, their containers left running with nothing deploying them: archive on alpha, shop on beta" {
		t.Errorf("a repository in use, removed = %v", out)
	}
	dock.Then(appsRemove, nil)
	dock.Answer(gitRepositoriesCall, threeRepositories)

	dock.Answer("DELETE /api/git/repositories/3", done)
	if msg := suite.CallErr(t, "git_repository_delete", map[string]any{"repository": "Site", "confirm": true}); !strings.Contains(msg, "Dockhand answered that Site was removed, and still lists it") {
		t.Errorf("a repository still listed = %q", msg)
	}
	dock.Then("DELETE /api/git/repositories/3", func(dockhandtest.Request) { dock.Fail(gitRepositoriesCall, http.StatusForbidden, notAllowed) })
	if out = suite.Call(t, "git_repository_delete", map[string]any{"repository": "Site", "confirm": true}); !acctest.BoolOf(out["removed"]) || !strings.Contains(acctest.Str(out["note"]), "it was removed, and the repositories could not be listed afterwards") {
		t.Errorf("a repository removed and not read back = %v", out)
	}
	dock.Then("DELETE /api/git/repositories/3", nil)
	dock.Answer(gitRepositoriesCall, threeRepositories)

	dock.Fail(siteRemove, http.StatusNotFound, "Repository not found")
	dock.Answer(gitStacksCall, `[]`)
	if msg := suite.CallErr(t, "git_repository_delete", map[string]any{"repository": "site", "confirm": true}); !strings.Contains(msg, "the git repository site was not removed") {
		t.Errorf("a removal Dockhand refuses = %q", msg)
	}
	if msg := suite.CallErr(t, "git_repository_delete", map[string]any{"repository": "nope"}); !strings.Contains(msg, `no git repository is named or numbered "nope"`) {
		t.Errorf("a repository there is none of = %q", msg)
	}
	dock.Fail(gitStacksCall, http.StatusForbidden, notAllowed)
	if msg := suite.CallErr(t, "git_repository_delete", map[string]any{"repository": "apps"}); !strings.Contains(msg, "the git stacks could not be listed") || !strings.Contains(msg, "so nothing was removed") {
		t.Errorf("git stacks that cannot be listed = %q", msg)
	}
	dock.Fail(gitRepositoriesCall, http.StatusForbidden, notAllowed)
	if msg := suite.CallErr(t, "git_repository_delete", map[string]any{"repository": "apps"}); !strings.Contains(msg, "the git repositories could not be listed") {
		t.Errorf("repositories that cannot be listed = %q", msg)
	}
}
