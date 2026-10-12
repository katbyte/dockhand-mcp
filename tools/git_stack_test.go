package tools

import (
	"net/http"
	"slices"
	"strings"
	"testing"

	"github.com/katbyte/dockhand-mcp/sdk/dockhand"
	"github.com/katbyte/dockhand-mcp/sdk/dockhandtest"
	"github.com/katbyte/go-kt/mcp/acctest"
)

// What a 1.0.51 server answers about one git stack and about deploying one, with invented names: the stack on alpha read by its number, which runs
// from an older commit than its branch has; the one on beta, never deployed; the variables Dockhand keeps for the first; and a deploy's job as it
// reports, done, failed and still going.
const (
	archiveGitStack  = `{"id":5,"stackName":"archive","environmentId":1,"repositoryId":1,"branch":null,"composePath":"compose.yaml","envFilePath":null,"autoUpdate":true,"autoUpdateSchedule":"custom","autoUpdateCron":"0 4 * * *","webhookEnabled":true,"webhookSecret":"hook-secret","contextDir":null,"buildOnDeploy":false,"noBuildCache":false,"repullImages":false,"forceRedeploy":false,"lastSync":"2026-10-11T05:26:11.447Z","lastCommit":"11f65b2","syncStatus":"synced","syncError":null,"syncedFiles":"{\"commit\":\"11f65b2c02358a74cae5a5b2fc6acb59e0aa54be\",\"files\":{\"compose.yaml\":\"97e5\"}}","createdAt":"2026-10-11 05:20:00","updatedAt":"2026-10-11T05:26:11.511Z","repository":{"id":1,"name":"apps","url":"https://git.example/team/apps.git","branch":"main","credentialId":1}}`
	shopGitStack     = `{"id":6,"stackName":"shop","environmentId":2,"repositoryId":1,"branch":"next","composePath":"deploy/compose.yaml","envFilePath":"deploy/shop.env","autoUpdate":false,"autoUpdateSchedule":"daily","autoUpdateCron":"0 3 * * *","webhookEnabled":false,"webhookSecret":null,"contextDir":"deploy","buildOnDeploy":true,"noBuildCache":true,"repullImages":true,"forceRedeploy":true,"lastSync":null,"lastCommit":null,"syncStatus":"error","syncError":"Compose file not found: deploy/compose.yaml","syncedFiles":null,"createdAt":"2026-10-11 05:21:00","updatedAt":"2026-10-11 05:21:00","repository":{"id":1,"name":"apps","url":"https://git.example/team/apps.git","branch":"main","credentialId":1}}`
	madeGitStack     = `{"id":9,"stackName":"blog","environmentId":2,"repositoryId":1,"branch":null,"composePath":"compose.yaml","syncStatus":"pending","repository":{"id":1,"name":"apps","url":"https://git.example/team/apps.git","branch":"main","credentialId":1}}`
	archiveVariables = `{"variables":[{"key":"MODE","value":"live","isSecret":false},{"key":"API_TOKEN","value":"made-up","isSecret":false},{"key":"DB_PASSWORD","value":"***","isSecret":true},{"key":"DOCKHAND_SECRET_SELECTOR","value":"prod/db","isSecret":false}],"injectedSecretKeys":["user"],"secretProvider":{"type":"vault","name":"main"}}`
	noVariables      = `{"variables":[],"injectedSecretKeys":[],"secretProvider":null}`
	boundGitSources  = `{"archive":{"sourceType":"git","composePath":"/app/data/stacks/alpha/archive/compose.yaml","repository":null,"secretProviderId":1,"icon":null,"envVarCount":4}}`
	gitJobStarted    = `{"jobId":"g1"}`
	gitDeployDone    = `{"id":"g1","status":"done","lines":[{"data":{"status":"connecting","message":"Connecting to repository...","step":1,"totalSteps":5}},{"data":{"status":"deploying","message":"File changes: 1 added, 1 updated, 0 removed, 0 skipped, 1 unchanged","step":5,"totalSteps":5}},{"data":{"status":"deploying","logLine":" Container blog-app-1 Started ","step":5,"totalSteps":5}},{"data":{"status":"deploying","logLine":" Container blog-app-1 Started ","step":5,"totalSteps":5}},{"data":{"status":"deploying","step":5,"totalSteps":5}},{"data":{"status":"deploying","logLine":"API_TOKEN=made-up given","step":5,"totalSteps":5}},{"data":{"status":"complete","message":"Successfully deployed"}}],"result":{"status":"complete","message":"Successfully deployed"}}`
	gitDeployFailed  = `{"id":"g1","status":"done","lines":[{"data":{"status":"reading","message":"Reading nope.yaml...","step":4,"totalSteps":5}},{"data":{"status":"error","error":"Compose file not found: nope.yaml"}}],"result":{"status":"error","error":"Compose file not found: nope.yaml"}}`
	gitDeployRunning = `{"id":"g1","status":"running","lines":[{"data":{"status":"cloning","message":"Cloning repository...","step":2,"totalSteps":5}}]}`
	blogRunning      = `[{"name":"blog","status":"running","containers":["ffff"],"containerDetails":[{"id":"ffff","name":"blog-app-1","service":"app","image":"registry.example/blog:1","state":"running","status":"Up 2 seconds"}]}]`
	blogPartial      = `[{"name":"blog","status":"partial","containers":["ffff"],"containerDetails":[{"id":"ffff","name":"blog-app-1","service":"app","image":"registry.example/blog:1","state":"exited","status":"Exited (1) 1 second ago"}]}]`

	gitStackMake    = "POST /api/git/stacks"
	archiveGitCall  = "GET /api/git/stacks/5"
	archiveGitSave  = "PUT /api/git/stacks/5"
	archiveGitTry   = "POST /api/git/stacks/5/test"
	archiveGitSync  = "POST /api/git/stacks/5/sync"
	archiveGitUp    = "POST /api/git/stacks/5/deploy-stream"
	archiveGitFiles = "GET /api/git/stacks/5/env-files"
	archiveGitRead  = "POST /api/git/stacks/5/env-files"
	archiveGitDrop  = "DELETE /api/git/stacks/5"
	archiveVarsCall = "GET /api/stacks/archive/env"
	archiveVarsSave = "PUT /api/stacks/archive/env"
	shopGitCall     = "GET /api/git/stacks/6"
	shopGitSave     = "PUT /api/git/stacks/6"
	shopGitTry      = "POST /api/git/stacks/6/test"
	shopGitFiles    = "GET /api/git/stacks/6/env-files"
	shopVarsCall    = "GET /api/stacks/shop/env"
	lostGitTry      = "POST /api/git/stacks/7/test"
	lostGitDrop     = "DELETE /api/git/stacks/7"
	blogGitUp       = "POST /api/git/stacks/9/deploy-stream"
	gitJob          = "GET /api/jobs/g1"
	stacksCall      = "GET /api/stacks"
)

// withGitStacks is withGit with what reading each of its git stacks in full answers.
func withGitStacks(t *testing.T) *dockhandtest.Server {
	t.Helper()

	dock := withGit(t)
	dock.Answer(archiveGitCall, archiveGitStack)
	dock.Answer(archiveGitTry, fetchable)
	dock.Answer(archiveGitFiles, `{"files":[".env","deploy/shop.env"]}`)
	dock.Answer(archiveVarsCall, archiveVariables)
	dock.Answer(shopGitCall, shopGitStack)
	dock.Answer(shopGitTry, `{"success":false,"error":"Branch 'next' not found in repository"}`)
	dock.Fail(shopGitFiles, http.StatusBadRequest, "Repository not synced - deploy the stack first")
	dock.Answer(shopVarsCall, noVariables)

	return dock
}

func TestGitStackPieces(t *testing.T) {
	t.Parallel()

	for kept, want := range map[string]string{
		`{"commit":"11f65b2c02358a74cae5a5b2fc6acb59e0aa54be","files":{}}`: "11f65b2",
		`{"commit":null,"files":{}}`:                                       "",
		`{"commit":"abc"}`:                                                 "",
		``:                                                                 "",
		`not json`:                                                         "",
	} {
		if got := deployedCommit(kept); got != want {
			t.Errorf("the commit of %q = %q, want %q", kept, got, want)
		}
	}

	// a git stack as any call answers one is read as the list's record
	if s := gitStackOf(dockhand.PutGitStacksByIdAnswer{Id: 5, StackName: "archive", Branch: "next", SyncedFiles: "x"}); s.Id != 5 || s.StackName != "archive" || s.Branch != "next" || s.SyncedFiles != "x" {
		t.Errorf("a git stack answered by a change, read as the list's = %+v", s)
	}
	if s := gitStackOf(func() {}); s.Id != 0 {
		t.Errorf("what is no git stack = %+v", s)
	}

	// what a deploy reported: a line said twice said once, one that says nothing left out, a secret hidden, and only the last few kept
	lines := make([]dockhand.GetJobsByIdAnswerLines, 0, 45)
	for _, said := range []map[string]any{{"message": "one"}, {"message": "one"}, {"status": "deploying"}, {"logLine": " API_TOKEN=made-up given "}, {"error": "two"}} {
		lines = append(lines, dockhand.GetJobsByIdAnswerLines{Data: said})
	}
	if got := strings.Join(gitReported(lines), "|"); got != "one|API_TOKEN=*** given|two" {
		t.Errorf("what a deploy reported = %q", got)
	}
	for range 20 {
		lines = append(lines, dockhand.GetJobsByIdAnswerLines{Data: map[string]any{"message": "more"}}, dockhand.GetJobsByIdAnswerLines{Data: map[string]any{"message": "and more"}})
	}
	if got := gitReported(lines); len(got) != gitReportKept || got[len(got)-1] != "and more" {
		t.Errorf("a long deploy's report = %d lines, ending %q", len(got), got[len(got)-1])
	}

	if err := variablesNamed(map[string]string{"1ST": "x"}); err == nil || !strings.Contains(err.Error(), `"1ST" is no name for a variable`) {
		t.Errorf("a variable's name that is none = %v", err)
	}
	if err := variablesNamed(map[string]string{secretPathKey: "prod/db"}); err == nil || !strings.Contains(err.Error(), "which secret_path sets") {
		t.Errorf("the path of a stack's secrets given as a variable = %v", err)
	}
	if !equalRequests(dockhand.PutGitStacksByIdRequest{}, dockhand.PutGitStacksByIdRequest{}) || equalRequests(dockhand.PutGitStacksByIdRequest{Branch: "next"}, dockhand.PutGitStacksByIdRequest{}) {
		t.Error("a change that says something is told from one that says nothing")
	}
}

func TestGitStackList(t *testing.T) {
	t.Parallel()

	dock := withGitStacks(t)
	suite := session(t, dock, Options{})

	// every git stack, by environment and then by name, the one made with no environment among them and said to be
	out := suite.Call(t, "git_stack_list", nil)
	rows := acctest.Rows(t, out["stacks"], "stacks")
	if len(rows) != 3 || rows[0]["stack"] != "lost" || rows[1]["stack"] != "archive" || rows[2]["stack"] != "shop" || acctest.Num(t, out["total"], "total") != 3 || acctest.Num(t, out["failed"], "failed") != 1 || out["behind"] != nil {
		t.Fatalf("the git stacks = %v", out)
	}
	if got := flat(rows[1]); got != `{"auto_update":"0 4 * * *","branch":"main","commit":"11f65b2","compose_path":"compose.yaml","env":"alpha","fetched":"2026-10-11T05:26:11Z","id":5,"repository":"apps","stack":"archive","sync":"synced","webhook":true}` {
		t.Errorf("a git stack that is fetched by itself = %s", got)
	}
	if got := flat(rows[2]); got != `{"branch":"next","compose_path":"deploy/compose.yaml","env":"beta","id":6,"repository":"apps","stack":"shop","sync":"error","sync_error":"Compose file not found: deploy/compose.yaml"}` {
		t.Errorf("a git stack whose last deploy failed = %s", got)
	}
	if rows[0]["env"] != "(none)" || rows[0]["branch"] != "release" || !strings.Contains(acctest.Str(out["note"]), "1 git stack was made with no environment") {
		t.Errorf("a git stack made with no environment = %v, %v", rows[0], out["note"])
	}

	// on one environment, only its own
	out = suite.Call(t, "git_stack_list", map[string]any{"env": "alpha"})
	if rows = acctest.Rows(t, out["stacks"], "stacks"); len(rows) != 1 || rows[0]["stack"] != "archive" || out["note"] != nil || out["failed"] != nil {
		t.Errorf("the git stacks of alpha = %v", out)
	}

	// with check, each repository is asked: one behind, one whose branch is gone, one whose repository cannot be asked at all
	dock.Fail(lostGitTry, http.StatusInternalServerError, "Failed to test git stack")
	out = suite.Call(t, "git_stack_list", map[string]any{"check": true})
	named := rowsNamed(acctest.Rows(t, out["stacks"], "stacks"), "stack")
	if named["archive"]["newest"] != "d7da76e" || named["archive"]["deployed"] != "11f65b2" || !acctest.BoolOf(named["archive"]["behind"]) || acctest.Num(t, out["behind"], "behind") != 1 {
		t.Errorf("a git stack that is behind = %v", named["archive"])
	}
	if named["shop"]["check_error"] != "Branch 'next' not found in repository" || named["shop"]["behind"] != nil || !strings.Contains(acctest.Str(named["lost"]["check_error"]), "Failed to test git stack") {
		t.Errorf("the git stacks whose repository cannot be asked = %v, %v", named["shop"], named["lost"])
	}
	dock.Answer(archiveGitTry, `{"success":true,"branch":"main","lastCommit":"11f65b2"}`)
	if out = suite.Call(t, "git_stack_list", map[string]any{"env": 1, "check": true}); out["behind"] != nil || acctest.Rows(t, out["stacks"], "stacks")[0]["behind"] != nil {
		t.Errorf("a git stack that runs from the newest commit = %v", out)
	}

	dock.Answer(gitStacksCall, `[]`)
	if out = suite.Call(t, "git_stack_list", nil); acctest.Num(t, out["total"], "total") != 0 || out["note"] != "no stack here is deployed from git" {
		t.Errorf("a Dockhand with no git stack = %v", out)
	}
	dock.Fail(gitStacksCall, http.StatusForbidden, notAllowed)
	if msg := suite.CallErr(t, "git_stack_list", nil); !strings.Contains(msg, "the git stacks could not be listed") {
		t.Errorf("git stacks that cannot be listed = %q", msg)
	}
	if msg := suite.CallErr(t, "git_stack_list", map[string]any{"env": "nope"}); !strings.Contains(msg, `no environment is named or numbered "nope"`) {
		t.Errorf("an environment there is none of = %q", msg)
	}
}

func TestGitStackGet(t *testing.T) {
	t.Parallel()

	dock := withGitStacks(t)
	suite := session(t, dock, Options{})

	// one in full: behind its branch, with what Dockhand keeps for it, and every secret hidden
	out := suite.Call(t, "git_stack_get", map[string]any{"stack": "archive"})
	if got := flat(out); got != `{"auto_update":"0 4 * * *","behind":true,"branch":"main","commit":"11f65b2","compose_path":"compose.yaml","deployed":"11f65b2","env":"alpha","env_files":[".env","deploy/shop.env"],"fetched":"2026-10-11T05:26:11Z","hidden":2,"id":5,"newest":"d7da76e","note":"it runs from 11f65b2 and the newest commit on main is d7da76e: git_stack_deploy brings it up to date; 2 secrets are hidden, the webhook's, and values told by the names beside them: reveal_secrets shows them","repository":"apps","secret_path":"prod/db","secret_provider":"main","stack":"archive","status":"stopped","sync":"synced","variables":[{"name":"API_TOKEN","value":"***"},{"name":"DB_PASSWORD","secret":true,"value":"***"},{"name":"MODE","value":"live"}],"webhook":true,"webhook_path":"/api/git/stacks/5/webhook","webhook_secret":"***"}` {
		t.Errorf("a git stack in full = %s", got)
	}

	// asked for, what is hidden is shown, but for what Dockhand keeps as a secret; and one of the repository's .env files is read
	dock.Answer(archiveGitRead, `{"vars":{"MODE":"live","SMTP_PASSWORD":"made-up"}}`)
	out = suite.Call(t, "git_stack_get", map[string]any{"stack": "archive", "env": "alpha", "reveal_secrets": true, "read_env_file": " .env "})
	vars := rowsNamed(acctest.Rows(t, out["variables"], "variables"), "name")
	if out["webhook_secret"] != "hook-secret" || vars["API_TOKEN"]["value"] != "made-up" || vars["DB_PASSWORD"]["value"] != "***" || out["hidden"] != nil || flat(sent(t, dock.Last(archiveGitRead))) != `{"path":".env"}` {
		t.Errorf("a git stack with its secrets asked for = %v", out)
	}
	if got := flat(map[string]any{"v": out["file_variables"]}); got != `{"v":[{"name":"MODE","value":"live"},{"name":"SMTP_PASSWORD","value":"made-up"}]}` {
		t.Errorf("a .env file of the repository, whole = %s", got)
	}
	out = suite.Call(t, "git_stack_get", map[string]any{"stack": "archive", "read_env_file": ".env"})
	if files := rowsNamed(acctest.Rows(t, out["file_variables"], "file_variables"), "name"); files["SMTP_PASSWORD"]["value"] != "***" || acctest.Num(t, out["hidden"], "hidden") != 3 {
		t.Errorf("a .env file of the repository, with its secrets hidden = %v", out)
	}
	dock.Fail(archiveGitRead, http.StatusBadRequest, "File not found: nope.env")
	if msg := suite.CallErr(t, "git_stack_get", map[string]any{"stack": "archive", "read_env_file": "nope.env"}); !strings.Contains(msg, "nope.env could not be read from Dockhand's copy of the repository") {
		t.Errorf("a .env file that is not there = %q", msg)
	}

	// the newest commit fetched and never deployed is told from one never fetched; a stack that runs from the newest says nothing
	dock.Answer(archiveGitCall, strings.Replace(archiveGitStack, `"lastCommit":"11f65b2"`, `"lastCommit":"d7da76e"`, 1))
	if out = suite.Call(t, "git_stack_get", map[string]any{"stack": "archive"}); !strings.Contains(acctest.Str(out["note"]), "the newest commit, d7da76e, was fetched and never deployed: it runs from 11f65b2") {
		t.Errorf("a commit fetched and never deployed = %v", out["note"])
	}
	dock.Answer(archiveGitCall, archiveGitStack)
	dock.Answer(archiveGitTry, `{"success":true,"branch":"main","lastCommit":"11f65b2"}`)
	dock.Answer(archiveGitCall, strings.Replace(archiveGitStack, `"webhookEnabled":true,"webhookSecret":"hook-secret"`, `"webhookEnabled":false,"webhookSecret":null`, 1))
	dock.Answer(archiveVarsCall, noVariables)
	if out = suite.Call(t, "git_stack_get", map[string]any{"stack": "archive"}); out["behind"] != nil || out["note"] != nil || out["webhook_path"] != nil || out["variables"] != nil {
		t.Errorf("a git stack that runs from the newest commit = %v", out)
	}

	// one never deployed, with everything it was told: what cannot be asked is said for what it is and the rest is still given
	out = suite.Call(t, "git_stack_get", map[string]any{"stack": "shop"})
	if out["status"] != "gone" || out["deployed"] != nil || out["check_error"] != "Branch 'next' not found in repository" || out["env_files"] != nil || out["env_file"] != "deploy/shop.env" || out["context_dir"] != "deploy" {
		t.Errorf("a git stack never deployed = %v", out)
	}
	if !acctest.BoolOf(out["build"]) || !acctest.BoolOf(out["no_build_cache"]) || !acctest.BoolOf(out["pull"]) || !acctest.BoolOf(out["recreate"]) || !strings.Contains(acctest.Str(out["note"]), "Dockhand has never deployed it: git_stack_deploy fetches the repository and brings it up") {
		t.Errorf("how a git stack is deployed = %v", out)
	}
	dock.Fail(shopGitTry, http.StatusInternalServerError, "Failed to test git stack")
	dock.Fail(shopVarsCall, http.StatusForbidden, notAllowed)
	dock.FailWhen(stacksCall, "env", "2", http.StatusInternalServerError, "Failed to list stacks")
	out = suite.Call(t, "git_stack_get", map[string]any{"stack": "shop"})
	if note := acctest.Str(out["note"]); out["status"] != nil || !strings.Contains(note, "how it stands on beta could not be read") || !strings.Contains(note, "the variables Dockhand keeps for it could not be read") || !strings.Contains(acctest.Str(out["check_error"]), "Failed to test git stack") {
		t.Errorf("a git stack little of which can be read = %v", out)
	}

	for what, tc := range map[string]struct {
		args map[string]any
		want string
	}{
		"no name":                         {map[string]any{"stack": " "}, "stack is required: the git stack's name"},
		"one there is none of":            {map[string]any{"stack": "nope"}, `no git stack is named "nope" on the environments asked`},
		"one on another environment":      {map[string]any{"stack": "archive", "env": "beta"}, `no git stack is named "archive" on the environments asked`},
		"one made with no environment":    {map[string]any{"stack": "lost"}, "the git stack lost was made with no environment"},
		"an environment there is none of": {map[string]any{"stack": "archive", "env": "nope"}, `no environment is named or numbered "nope"`},
	} {
		if msg := suite.CallErr(t, "git_stack_get", tc.args); !strings.Contains(msg, tc.want) {
			t.Errorf("%s = %q, want %q", what, msg, tc.want)
		}
	}
	dock.Fail(shopGitCall, http.StatusNotFound, "Git stack not found")
	if msg := suite.CallErr(t, "git_stack_get", map[string]any{"stack": "shop"}); !strings.Contains(msg, "the git stack shop could not be read") {
		t.Errorf("a git stack that cannot be read = %q", msg)
	}

	// a name two environments have a git stack under is refused until one is said
	dock.Answer(gitStacksCall, strings.Replace(threeGitStacks, `"stackName":"shop"`, `"stackName":"archive"`, 1))
	if msg := suite.CallErr(t, "git_stack_get", map[string]any{"stack": "archive"}); !strings.Contains(msg, `a git stack named "archive" is on alpha and beta: say which with env`) {
		t.Errorf("a name on two environments = %q", msg)
	}
	dock.Fail(gitStacksCall, http.StatusForbidden, notAllowed)
	if msg := suite.CallErr(t, "git_stack_get", map[string]any{"stack": "archive"}); !strings.Contains(msg, "the git stacks could not be listed") {
		t.Errorf("git stacks that cannot be listed = %q", msg)
	}
}

func TestGitStackCreate(t *testing.T) {
	t.Parallel()

	blog := map[string]any{"env": "beta", "name": "blog", "repository": "apps"}

	t.Run("refused before anything is made", func(t *testing.T) {
		t.Parallel()

		dock := withGitStacks(t)
		suite := session(t, dock, quick)
		dock.AnswerWhen(keptHowCall, "env", "2", `{"ghost":{"sourceType":"git","composePath":null,"repository":null,"secretProviderId":null,"icon":null,"envVarCount":0}}`)
		dock.Answer(gitRepositoryTry, notFetchable)
		for what, tc := range map[string]struct {
			args map[string]any
			want string
		}{
			"a name no stack can have":       {over(blog, map[string]any{"name": "Blog"}), `name is "Blog": a stack's name is lower-case letters`},
			"a variable's name that is none": {over(blog, map[string]any{"env_vars": map[string]any{"1ST": "x"}}), `"1ST" is no name for a variable`},
			"a secret with no value":         {over(blog, map[string]any{"secrets": map[string]any{"KEY": ""}}), "the secret KEY is given no value"},
			"one name given both ways":       {over(blog, map[string]any{"env_vars": map[string]any{"KEY": "a"}, "secrets": map[string]any{"KEY": "b"}}), "KEY is given both in env_vars and in secrets"},
			"a schedule that is none":        {over(blog, map[string]any{"auto_update_cron": "hourly"}), `auto_update_cron: cron is "hourly"`},
			"a wait that is too long":        {over(blog, map[string]any{"wait": 9999}), "wait is 9999"},
			"no environment":                 {map[string]any{"name": "blog", "repository": "apps"}, "env is required"},
			"a repository there is none of":  {over(blog, map[string]any{"repository": "nope"}), `no git repository is named or numbered "nope"`},
			"a name a stack has":             {map[string]any{"env": "alpha", "name": "mail", "repository": "apps"}, "a stack named mail is already on alpha: a new stack takes a name of its own"},
			"a name Dockhand keeps":          {over(blog, map[string]any{"name": "ghost"}), "a stack named ghost is already on beta"},
			"a provider there is none of":    {over(blog, map[string]any{"secret_provider": "nope"}), `no secret provider is named or numbered "nope"`},
			"a repository out of reach":      {blog, "Dockhand could not fetch the branch main of apps, so nothing was made: could not read Username"},
		} {
			if msg := suite.CallErr(t, "git_stack_create", tc.args); !strings.Contains(msg, tc.want) {
				t.Errorf("%s = %q, want %q", what, msg, tc.want)
			}
		}
		if made := dock.Requests(gitStackMake); len(made) != 0 {
			t.Errorf("a stack was made all the same: %v", made)
		}

		// each of what is read before a stack is made, when it cannot be: the last of them first, since each is read before the one above it
		for _, unread := range []struct{ call, want string }{
			{gitRepositoryTry, "the repository apps could not be tried, so nothing was made"},
			{keptHowCall, "the stacks Dockhand keeps for beta could not be listed, so nothing was made"},
			{stacksCall, "the stacks of beta could not be listed, so nothing was made"},
			{gitRepositoriesCall, "the git repositories could not be listed"},
		} {
			dock.Fail(unread.call, http.StatusForbidden, notAllowed)
			dock.FailWhen(unread.call, "env", "2", http.StatusForbidden, notAllowed)
			if msg := suite.CallErr(t, "git_stack_create", blog); !strings.Contains(msg, unread.want) {
				t.Errorf("%s refused = %q, want %q", unread.call, msg, unread.want)
			}
		}
	})

	t.Run("made and brought up", func(t *testing.T) {
		t.Parallel()

		dock := withGitStacks(t)
		suite := session(t, dock, quick)
		dock.Answer(gitRepositoryTry, fetchable)
		dock.Answer(mainLook, twoKeys)
		whole := over(blog, map[string]any{"branch": " next ", "compose_path": "deploy/compose.yaml", "env_file": "deploy/blog.env", "context_dir": "deploy", "build": true, "no_build_cache": true, "pull": true, "recreate": true, "auto_update_cron": " 0 4 * * * ", "webhook_secret": " hook ", "env_vars": map[string]any{"MODE": "live"}, "secrets": map[string]any{"KEY": "made-up"}, "secret_provider": "main", "secret_path": "prod/db"})

		// a dry run says what would be made and makes nothing
		out := suite.Call(t, "git_stack_create", over(whole, map[string]any{"dry_run": true}))
		if got := strings.Join(acctest.Strs(t, out["changes"], "changes"), "; "); got != "would make the git stack blog on beta, from deploy/compose.yaml of the branch next of apps, its secrets loaded from main at prod/db; would have Dockhand fetch it by itself at 0 4 * * *, and fetch and deploy it when its webhook is called; would fetch the repository and bring it up" || out["commit"] != "d7da76e" || len(dock.Requests(gitStackMake)) != 0 {
			t.Errorf("a dry run = %q, %v", got, out)
		}
		if got := flat(sent(t, dock.Last(gitRepositoryTry))); got != `{"branch":"next","credentialId":1,"url":"https://git.example/team/apps.git"}` {
			t.Errorf("the repository was tried with %s", got)
		}

		// made with everything it was given, then fetched and brought up as a job, and read back as running
		dock.Answer(gitStackMake, madeGitStack)
		dock.Answer(blogGitUp, gitJobStarted)
		dock.Answer(gitJob, gitDeployDone)
		dock.Then(blogGitUp, func(dockhandtest.Request) { dock.AnswerWhen(stacksCall, "env", "2", blogRunning) })
		out = suite.Call(t, "git_stack_create", whole)
		if got := flat(sent(t, dock.Last(gitStackMake))); got != `{"autoUpdate":true,"autoUpdateCron":"0 4 * * *","autoUpdateSchedule":"custom","branch":"next","buildOnDeploy":true,"composePath":"deploy/compose.yaml","contextDir":"deploy","envFilePath":"deploy/blog.env","envVars":[{"key":"MODE","value":"live"},{"key":"DOCKHAND_SECRET_SELECTOR","value":"prod/db"},{"isSecret":true,"key":"KEY","value":"made-up"}],"environmentId":2,"forceRedeploy":true,"noBuildCache":true,"repositoryId":1,"repullImages":true,"secretProviderId":1,"stackName":"blog","webhookEnabled":true,"webhookSecret":"hook"}` {
			t.Errorf("a git stack was made with %s", got)
		}
		if asked := dock.Last(blogGitUp).Accept; asked != dockhand.AcceptJob {
			t.Errorf("the deploy was asked for with Accept %q, which is not the one that starts a job", asked)
		}
		if got := flat(out); got != `{"branch":"next","changes":["made the git stack blog on beta, from deploy/compose.yaml of the branch next of apps, its secrets loaded from main at prod/db","had Dockhand fetch it by itself at 0 4 * * *, and fetch and deploy it when its webhook is called","fetched the repository and brought it up"],"commit":"d7da76e","env":"beta","reported":["Connecting to repository...","File changes: 1 added, 1 updated, 0 removed, 0 skipped, 1 unchanged","Container blog-app-1 Started","API_TOKEN=*** given","Successfully deployed"],"repository":"apps","running":false,"stack":"blog","status":"running","webhook_path":"/api/git/stacks/9/webhook"}` {
			t.Errorf("a git stack made and brought up = %s", got)
		}
	})

	t.Run("made and not as hoped", func(t *testing.T) {
		t.Parallel()

		dock := withGitStacks(t)
		suite := session(t, dock, quick)
		dock.Answer(gitRepositoryTry, fetchable)
		dock.Answer(gitStackMake, madeGitStack)

		// left never deployed when start is false: nothing is fetched, and nothing more is sent than the stack
		out := suite.Call(t, "git_stack_create", over(blog, map[string]any{"start": false}))
		if got := flat(sent(t, dock.Last(gitStackMake))); got != `{"environmentId":2,"repositoryId":1,"stackName":"blog"}` || out["status"] != "gone" || out["reported"] != nil || out["webhook_path"] != nil || len(acctest.Strs(t, out["changes"], "changes")) != 1 {
			t.Errorf("a git stack made and not started was sent %s, and answered %v", got, out)
		}
		if out = suite.Call(t, "git_stack_create", over(blog, map[string]any{"start": false, "dry_run": true})); len(acctest.Strs(t, out["changes"], "changes")) != 1 {
			t.Errorf("a dry run of one not to be started = %v", out)
		}

		// brought up and not all of it running, and one that cannot be read back
		dock.Answer(blogGitUp, gitJobStarted)
		dock.Answer(gitJob, gitDeployDone)
		dock.Then(blogGitUp, func(dockhandtest.Request) { dock.AnswerWhen(stacksCall, "env", "2", blogPartial) })
		if out = suite.Call(t, "git_stack_create", blog); out["status"] != "partial" || !strings.Contains(acctest.Str(out["note"]), "it was brought up and is partial") {
			t.Errorf("a git stack brought up in part = %v", out)
		}
		dock.AnswerWhen(stacksCall, "env", "2", `[]`)
		dock.Then(blogGitUp, func(dockhandtest.Request) {
			dock.FailWhen(stacksCall, "env", "2", http.StatusInternalServerError, "Failed to list stacks")
		})
		if out = suite.Call(t, "git_stack_create", blog); out["status"] != nil || !strings.Contains(acctest.Str(out["note"]), "how it stands could not be read back") {
			t.Errorf("a git stack that cannot be read back = %v", out)
		}
		dock.Then(blogGitUp, nil)
		dock.AnswerWhen(stacksCall, "env", "2", `[]`)

		// still going when the wait is over, the job is given to follow
		dock.Answer(gitJob, gitDeployRunning)
		if out = suite.Call(t, "git_stack_create", over(blog, map[string]any{"wait": 0})); !acctest.BoolOf(out["running"]) || out["job"] != "g1" || !strings.Contains(acctest.Str(out["note"]), "it was made and was still being brought up after 0 seconds: job_get") {
			t.Errorf("a git stack still being brought up = %v", out)
		}

		// one that cannot be brought up is kept, and the error says so; so is one whose deploy cannot be started or followed
		dock.Answer(gitJob, gitDeployFailed)
		if msg := suite.CallErr(t, "git_stack_create", blog); !strings.Contains(msg, "the git stack blog was made on beta and could not be brought up: Compose file not found: nope.yaml; it is kept") {
			t.Errorf("a git stack that cannot be brought up = %q", msg)
		}
		dock.Fail(gitJob, http.StatusNotFound, "Job not found")
		if msg := suite.CallErr(t, "git_stack_create", blog); !strings.Contains(msg, "could not be brought up: no job g1 is known to Dockhand") {
			t.Errorf("a deploy that cannot be followed = %q", msg)
		}
		dock.Answer(blogGitUp, `{}`)
		if msg := suite.CallErr(t, "git_stack_create", blog); !strings.Contains(msg, "Dockhand started no job for the deploy") {
			t.Errorf("a deploy that started no job = %q", msg)
		}
		dock.Fail(blogGitUp, http.StatusInternalServerError, "Failed to deploy git stack")
		if msg := suite.CallErr(t, "git_stack_create", blog); !strings.Contains(msg, "could not be brought up") || !strings.Contains(msg, "Failed to deploy git stack") {
			t.Errorf("a deploy that cannot be started = %q", msg)
		}
		dock.Fail(gitStackMake, http.StatusConflict, "A stack with this name already exists on this environment")
		if msg := suite.CallErr(t, "git_stack_create", blog); !strings.Contains(msg, "the git stack blog was not made on beta") {
			t.Errorf("a git stack Dockhand refuses = %q", msg)
		}
	})
}

func TestGitStackEdit(t *testing.T) {
	t.Parallel()

	t.Run("refused before anything is saved", func(t *testing.T) {
		t.Parallel()

		dock := withGitStacks(t)
		suite := session(t, dock, quick)
		for what, tc := range map[string]struct {
			args map[string]any
			want string
		}{
			"nothing to change":              {map[string]any{"stack": "archive"}, "nothing to change was given: name, branch"},
			"a name and something else":      {map[string]any{"stack": "archive", "name": "store", "pull": true}, "name is given alone"},
			"a name no stack can have":       {map[string]any{"stack": "archive", "name": "Store"}, `name is "Store": a stack's name is lower-case letters`},
			"a variable's name that is none": {map[string]any{"stack": "archive", "env_vars": map[string]any{"1ST": "x"}}, `"1ST" is no name for a variable`},
			"a secret with no value":         {map[string]any{"stack": "archive", "secrets": map[string]any{"KEY": ""}}, "the secret KEY is given no value"},
			"a variable set and removed":     {map[string]any{"stack": "archive", "env_vars": map[string]any{"MODE": "x"}, "remove_env_vars": []any{"MODE"}}, "MODE is both given a value and named to be removed"},
			"a secret set and removed":       {map[string]any{"stack": "archive", "secrets": map[string]any{"KEY": "x"}, "remove_secrets": []any{"KEY"}}, "KEY is both given a value and named to be removed"},
			"one name given both ways":       {map[string]any{"stack": "archive", "env_vars": map[string]any{"KEY": "a"}, "secrets": map[string]any{"KEY": "b"}}, "KEY is given both in env_vars and in secrets"},
			"a schedule that is none":        {map[string]any{"stack": "archive", "auto_update_cron": "hourly"}, `auto_update_cron: cron is "hourly"`},
			"one there is none of":           {map[string]any{"stack": "nope", "pull": true}, `no git stack is named "nope"`},
			"one made with no environment":   {map[string]any{"stack": "lost", "pull": true}, "the git stack lost was made with no environment"},
			"a variable it does not keep":    {map[string]any{"stack": "archive", "remove_env_vars": []any{"NOPE"}}, "Dockhand keeps no variable named NOPE for archive to remove"},
			"a secret named as a variable":   {map[string]any{"stack": "archive", "remove_env_vars": []any{"DB_PASSWORD"}}, "Dockhand keeps no variable named DB_PASSWORD for archive to remove"},
			"a secret it does not have":      {map[string]any{"stack": "archive", "remove_secrets": []any{"MODE"}}, "archive has no secret named MODE to remove"},
			"a provider there is none of":    {map[string]any{"stack": "archive", "secret_provider": "nope"}, `no secret provider is named or numbered "nope"`},
			"a name another stack has":       {map[string]any{"stack": "archive", "name": "mail"}, "a stack named mail is already on alpha, so nothing was changed"},
			"a stack that has containers":    {map[string]any{"stack": "archive", "name": "store"}, "archive has 1 container: Dockhand would rename the stack and leave what runs behind under the old name"},
		} {
			if msg := suite.CallErr(t, "git_stack_edit", tc.args); !strings.Contains(msg, tc.want) {
				t.Errorf("%s = %q, want %q", what, msg, tc.want)
			}
		}
		dock.Answer(gitBranchesCall, twoBranches)
		if msg := suite.CallErr(t, "git_stack_edit", map[string]any{"stack": "archive", "branch": "nope"}); !strings.Contains(msg, "its repository has no branch nope, so nothing was changed: it has main, next") {
			t.Errorf("a branch the repository does not have = %q", msg)
		}
		dock.Fail(gitBranchesCall, http.StatusInternalServerError, "Failed to fetch branches")
		if msg := suite.CallErr(t, "git_stack_edit", map[string]any{"stack": "archive", "branch": "next"}); !strings.Contains(msg, "the branches of its repository could not be listed, so nothing was changed") {
			t.Errorf("branches that cannot be listed = %q", msg)
		}
		dock.Fail(archiveVarsCall, http.StatusForbidden, notAllowed)
		if msg := suite.CallErr(t, "git_stack_edit", map[string]any{"stack": "archive", "env_vars": map[string]any{"MODE": "x"}}); !strings.Contains(msg, "the variables Dockhand keeps for archive could not be read, so nothing was changed") {
			t.Errorf("variables that cannot be read = %q", msg)
		}
		dock.Answer(archiveVarsCall, archiveVariables)
		dock.FailWhen(keptHowCall, "env", "1", http.StatusInternalServerError, "Failed to get stack sources")
		if msg := suite.CallErr(t, "git_stack_edit", map[string]any{"stack": "archive", "secret_path": "prod/api"}); !strings.Contains(msg, "how Dockhand keeps archive could not be read, so nothing was changed") {
			t.Errorf("a record that cannot be read = %q", msg)
		}
		if saved := slices.DeleteFunc(writesOf(dock), func(call string) bool { return call == gitBranchesCall }); len(saved) != 0 {
			t.Errorf("something was saved all the same: %v", saved)
		}
	})

	t.Run("a new name", func(t *testing.T) {
		t.Parallel()

		dock := withGitStacks(t)
		suite := session(t, dock, quick)

		// the name it has is no change, and a dry run renames nothing
		out := suite.Call(t, "git_stack_edit", map[string]any{"stack": "shop", "name": "shop"})
		if out["note"] != "nothing to do: it is named that already" || len(writesOf(dock)) != 0 {
			t.Errorf("a git stack given its own name = %v", out)
		}
		out = suite.Call(t, "git_stack_edit", map[string]any{"stack": "shop", "name": "store", "dry_run": true})
		if got := acctest.Strs(t, out["changes"], "changes"); len(got) != 1 || got[0] != "would rename it from shop to store" || out["stack"] != "shop" || len(writesOf(dock)) != 0 {
			t.Errorf("a dry run of a rename = %v", out)
		}

		// one with no containers, to a name nothing has, is renamed and read back
		dock.Answer(shopGitSave, strings.Replace(shopGitStack, `"stackName":"shop"`, `"stackName":"store"`, 1))
		dock.Then(shopGitSave, func(dockhandtest.Request) {
			dock.Answer(shopGitCall, strings.Replace(shopGitStack, `"stackName":"shop"`, `"stackName":"store"`, 1))
		})
		out = suite.Call(t, "git_stack_edit", map[string]any{"stack": "shop", "name": "store"})
		if got := flat(sent(t, dock.Last(shopGitSave))); got != `{"stackName":"store"}` || out["stack"] != "store" || acctest.Strs(t, out["changes"], "changes")[0] != "renamed it from shop to store" || !strings.Contains(acctest.Str(out["note"]), "the folder Dockhand kept its files in under the old name is left where it was") {
			t.Errorf("a rename was sent %s, and answered %v", got, out)
		}
		dock.Then(shopGitSave, nil)
		dock.Answer(shopGitCall, shopGitStack)

		// a name Dockhand keeps a record under is taken too, and what cannot be read is no licence to rename
		dock.AnswerWhen(keptHowCall, "env", "2", `{"store":{"sourceType":"external","composePath":null,"repository":null,"secretProviderId":null,"icon":null,"envVarCount":0}}`)
		if msg := suite.CallErr(t, "git_stack_edit", map[string]any{"stack": "shop", "name": "store"}); !strings.Contains(msg, "a stack named store is already on beta, so nothing was changed") {
			t.Errorf("a name Dockhand keeps a record under = %q", msg)
		}
		dock.FailWhen(keptHowCall, "env", "2", http.StatusInternalServerError, "Failed to get stack sources")
		if msg := suite.CallErr(t, "git_stack_edit", map[string]any{"stack": "shop", "name": "store"}); !strings.Contains(msg, "the stacks Dockhand keeps for beta could not be listed, so nothing was changed") {
			t.Errorf("records that cannot be read = %q", msg)
		}
		dock.AnswerWhen(keptHowCall, "env", "2", `{}`)
		dock.Fail(shopGitSave, http.StatusBadRequest, "A git stack with this name already exists for this environment")
		if msg := suite.CallErr(t, "git_stack_edit", map[string]any{"stack": "shop", "name": "store"}); !strings.Contains(msg, "the git stack shop was not renamed") {
			t.Errorf("a rename Dockhand refuses = %q", msg)
		}
		dock.FailWhen(stacksCall, "env", "2", http.StatusInternalServerError, "Failed to list stacks")
		if msg := suite.CallErr(t, "git_stack_edit", map[string]any{"stack": "shop", "name": "store"}); !strings.Contains(msg, "the stacks of beta could not be listed, so nothing was changed") {
			t.Errorf("stacks that cannot be listed = %q", msg)
		}
	})

	t.Run("how it is kept", func(t *testing.T) {
		t.Parallel()

		dock := withGitStacks(t)
		suite := session(t, dock, quick)
		dock.Answer(gitBranchesCall, twoBranches)

		// what it has already is not saved again
		same := map[string]any{"stack": "archive", "branch": "", "compose_path": "compose.yaml", "env_file": "", "context_dir": "", "build": false, "no_build_cache": false, "pull": false, "recreate": false, "auto_update_cron": "0 4 * * *", "webhook_secret": "hook-secret"}
		out := suite.Call(t, "git_stack_edit", same)
		if out["note"] != "nothing to do: it is that way already" || len(acctest.Strs(t, out["changes"], "changes")) != 0 || len(writesOf(dock)) != 0 || out["git_stack"] == nil {
			t.Errorf("a git stack given what it has = %v, having sent %v", out, writesOf(dock))
		}
		if out = suite.Call(t, "git_stack_edit", map[string]any{"stack": "shop", "auto_update_cron": "", "webhook_secret": ""}); out["note"] != "nothing to do: it is that way already" {
			t.Errorf("a git stack with no schedule or webhook, given none = %v", out)
		}

		// everything about one changed at once: said in a dry run, then sent as one change
		change := map[string]any{"stack": "archive", "branch": "next", "compose_path": "deploy/compose.yaml", "env_file": "deploy/a.env", "context_dir": "deploy", "build": true, "no_build_cache": true, "pull": true, "recreate": true, "auto_update_cron": "30 2 * * *", "webhook_secret": "another"}
		wants := "the branch it follows to next; its compose file's path from compose.yaml to deploy/compose.yaml; its extra .env file to deploy/a.env; the folder copied beside its compose file to deploy; each deploy build its images; its builds start from nothing; each deploy pull every image first; each deploy make every container again, changed or not; Dockhand fetch it by itself at 30 2 * * *; its webhook another secret"
		out = suite.Call(t, "git_stack_edit", over(change, map[string]any{"dry_run": true}))
		for want := range strings.SplitSeq(wants, "; ") {
			if got := strings.Join(acctest.Strs(t, out["changes"], "changes"), "; "); !strings.Contains(got, "would ") || !strings.Contains(got, want) {
				t.Errorf("a dry run = %q, want %q in it", got, want)
			}
		}
		if len(writesOf(dock)) != 1 {
			t.Errorf("a dry run sent %v", writesOf(dock))
		}
		dock.Answer(archiveGitSave, archiveGitStack)
		out = suite.Call(t, "git_stack_edit", change)
		if got := flat(sent(t, dock.Last(archiveGitSave))); got != `{"autoUpdate":true,"autoUpdateCron":"30 2 * * *","autoUpdateSchedule":"custom","branch":"next","buildOnDeploy":true,"composePath":"deploy/compose.yaml","contextDir":"deploy","envFilePath":"deploy/a.env","forceRedeploy":true,"noBuildCache":true,"repullImages":true,"webhookEnabled":true,"webhookSecret":"another"}` {
			t.Errorf("a git stack was changed with %s", got)
		}
		if got := acctest.Strs(t, out["changes"], "changes"); len(got) != 10 || got[0] != "changed the branch it follows to next" || got[9] != "gave its webhook another secret" || !strings.Contains(acctest.Str(out["note"]), "nothing running changed: git_stack_deploy brings it up with what was saved") {
			t.Errorf("a git stack that was changed = %v", out)
		}

		// what is taken away is sent apart, as nothing: its schedule is stopped with the rest, and its webhook goes with its secret
		before := len(dock.Requests(archiveGitSave))
		out = suite.Call(t, "git_stack_edit", map[string]any{"stack": "archive", "auto_update_cron": " ", "webhook_secret": " "})
		saves := dock.Requests(archiveGitSave)
		if got := strings.Join(acctest.Strs(t, out["changes"], "changes"), "; "); got != "had Dockhand no longer fetch it by itself; switched its webhook off, with its secret forgotten" || len(saves) != before+2 || saves[before].Body != `{"autoUpdate":false}` || saves[before+1].Body != `{"webhookEnabled":false,"webhookSecret":null}` {
			t.Errorf("a schedule and a webhook taken away = %q, sent as %v", got, saves[before:])
		}

		// the other way about, for the one on beta: its own branch, its extra file and its folder taken away, every switch off, a schedule and a
		// webhook on
		dock.Answer(shopGitSave, shopGitStack)
		out = suite.Call(t, "git_stack_edit", map[string]any{"stack": "shop", "branch": " ", "env_file": "", "context_dir": "", "build": false, "no_build_cache": false, "pull": false, "recreate": false, "auto_update_cron": "0 5 * * *", "webhook_secret": "hook"})
		saves = dock.Requests(shopGitSave)
		if len(saves) != 2 || saves[0].Body != `{"autoUpdate":true,"autoUpdateCron":"0 5 * * *","autoUpdateSchedule":"custom","buildOnDeploy":false,"forceRedeploy":false,"noBuildCache":false,"repullImages":false,"webhookEnabled":true,"webhookSecret":"hook"}` || saves[1].Body != `{"branch":null,"contextDir":null,"envFilePath":null}` {
			t.Errorf("a git stack with much taken away was sent %v", saves)
		}
		if got := strings.Join(acctest.Strs(t, out["changes"], "changes"), "; "); got != "had it follow its repository's own branch; had it read no extra .env file; had the folder copied be the compose file's own; had no deploy build its images; had its builds use what was built before; had no deploy pull an image the host has; had a deploy leave alone what did not change; had Dockhand fetch it by itself at 0 5 * * *; switched its webhook on" {
			t.Errorf("a git stack with much taken away = %q", got)
		}

		// what was to be taken away and was not is said, with how the stack stands; and a change Dockhand refuses whole
		dock.Then(shopGitSave, func(r dockhandtest.Request) {
			if strings.Contains(r.Body, "null") {
				dock.Fail(shopGitSave, http.StatusInternalServerError, "Failed to update git stack")
			}
		})
		if msg := suite.CallErr(t, "git_stack_edit", map[string]any{"stack": "shop", "branch": "", "pull": false}); !strings.Contains(msg, "the git stack shop was changed in part: what was to be taken away was not") {
			t.Errorf("a change done in part = %q", msg)
		}
		dock.Then(shopGitSave, nil)
		dock.Fail(shopGitSave, http.StatusBadRequest, "A webhook secret is required when the webhook is enabled")
		if msg := suite.CallErr(t, "git_stack_edit", map[string]any{"stack": "shop", "pull": false}); !strings.Contains(msg, "the git stack shop was not changed") {
			t.Errorf("a change Dockhand refuses = %q", msg)
		}

		// one that cannot be read back is said to be changed all the same
		dock.Fail(archiveGitCall, http.StatusNotFound, "Git stack not found")
		if out = suite.Call(t, "git_stack_edit", map[string]any{"stack": "archive", "pull": true}); out["git_stack"] != nil || !strings.Contains(acctest.Str(out["note"]), "it could not be read back") || len(acctest.Strs(t, out["changes"], "changes")) != 1 {
			t.Errorf("a change that cannot be read back = %v", out)
		}
	})

	t.Run("what Dockhand keeps for it", func(t *testing.T) {
		t.Parallel()

		dock := withGitStacks(t)
		suite := session(t, dock, quick)
		dock.Answer(mainLook, twoKeys)
		dock.Answer(archiveVarsSave, `{"success":true,"count":4}`)
		dock.Answer(archiveGitSave, archiveGitStack)

		// what it has already is not saved again: a variable with the value it has, and the path and the provider it has
		dock.AnswerWhen(keptHowCall, "env", "1", boundGitSources)
		out := suite.Call(t, "git_stack_edit", map[string]any{"stack": "archive", "env_vars": map[string]any{"MODE": "live"}, "secret_provider": "main", "secret_path": "prod/db"})
		if out["note"] != "nothing to do: it is that way already" || len(writesOf(dock)) != 0 {
			t.Errorf("a git stack given the variables it has = %v, having sent %v", out, writesOf(dock))
		}

		// variables and secrets set and removed: the whole of what is kept is sent, a secret left alone as it is shown, hidden
		change := map[string]any{"stack": "archive", "env_vars": map[string]any{"MODE": "test", "NEW": "n", "DB_PASSWORD": "plain"}, "remove_env_vars": []any{"API_TOKEN"}, "secrets": map[string]any{"KEY": "made-up"}}
		out = suite.Call(t, "git_stack_edit", over(change, map[string]any{"dry_run": true}))
		if got := strings.Join(acctest.Strs(t, out["changes"], "changes"), "; "); got != "would set the variables DB_PASSWORD, MODE, NEW; would remove the variables API_TOKEN; would save the secrets KEY" || len(writesOf(dock)) != 0 {
			t.Errorf("a dry run = %q", got)
		}
		out = suite.Call(t, "git_stack_edit", change)
		if got := dock.Last(archiveVarsSave).Body; got != `{"variables":[{"isSecret":false,"key":"DB_PASSWORD","value":"plain"},{"isSecret":false,"key":"DOCKHAND_SECRET_SELECTOR","value":"prod/db"},{"isSecret":true,"key":"KEY","value":"made-up"},{"isSecret":false,"key":"MODE","value":"test"},{"isSecret":false,"key":"NEW","value":"n"}]}` {
			t.Errorf("the variables were saved as %s", got)
		}
		if got := strings.Join(acctest.Strs(t, out["changes"], "changes"), "; "); got != "set the variables DB_PASSWORD, MODE, NEW; removed the variables API_TOKEN; saved the secrets KEY" || len(dock.Requests(archiveGitSave)) != 0 {
			t.Errorf("variables changed = %q", got)
		}
		out = suite.Call(t, "git_stack_edit", map[string]any{"stack": "archive", "remove_secrets": []any{"DB_PASSWORD"}, "secrets": map[string]any{"KEY": "made-up"}})
		if got := dock.Last(archiveVarsSave).Body; !strings.Contains(got, `{"isSecret":true,"key":"KEY","value":"made-up"}`) || strings.Contains(got, "DB_PASSWORD") || !strings.Contains(got, `{"isSecret":false,"key":"API_TOKEN","value":"made-up"}`) || acctest.Strs(t, out["changes"], "changes")[1] != "removed the secrets DB_PASSWORD" {
			t.Errorf("a secret removed was saved as %s, and answered %v", got, out)
		}
		suite.Call(t, "git_stack_edit", map[string]any{"stack": "archive", "env_vars": map[string]any{"NEW": "n"}})
		if got := dock.Last(archiveVarsSave).Body; !strings.Contains(got, `{"isSecret":true,"key":"DB_PASSWORD","value":"***"}`) {
			t.Errorf("a secret left alone was saved as %s", got)
		}

		// the path its secrets are loaded from is one of the variables; taken off its provider, the provider is sent as nothing
		out = suite.Call(t, "git_stack_edit", map[string]any{"stack": "archive", "secret_path": "prod/api"})
		if got := dock.Last(archiveVarsSave).Body; !strings.Contains(got, `{"isSecret":false,"key":"DOCKHAND_SECRET_SELECTOR","value":"prod/api"}`) || acctest.Strs(t, out["changes"], "changes")[0] != "set the path its secrets are loaded from to prod/api" || flat(sent(t, dock.Last(mainLook))) != `{"selector":"prod/api"}` {
			t.Errorf("a path was saved as %s, and answered %v", got, out)
		}
		out = suite.Call(t, "git_stack_edit", map[string]any{"stack": "archive", "secret_provider": "", "secret_path": ""})
		if got := strings.Join(acctest.Strs(t, out["changes"], "changes"), "; "); got != "took out the path its secrets are loaded from; took it off its secret provider" || strings.Contains(dock.Last(archiveVarsSave).Body, "DOCKHAND_SECRET_SELECTOR") || dock.Last(archiveGitSave).Body != `{"secretProviderId":null}` {
			t.Errorf("a provider and a path taken away = %q, sent as %s and %s", got, dock.Last(archiveVarsSave).Body, dock.Last(archiveGitSave).Body)
		}

		// given a provider when it has none, the provider goes with the stack's own settings
		dock.AnswerWhen(keptHowCall, "env", "1", alphaSources)
		out = suite.Call(t, "git_stack_edit", map[string]any{"stack": "archive", "secret_provider": 1})
		if got := dock.Last(archiveGitSave).Body; got != `{"secretProviderId":1}` || acctest.Strs(t, out["changes"], "changes")[0] != "had its secrets loaded from main" {
			t.Errorf("a provider was saved as %s, and answered %v", got, out)
		}

		// settings saved and variables not is said as that
		dock.Fail(archiveVarsSave, http.StatusInternalServerError, "Failed to save variables")
		if msg := suite.CallErr(t, "git_stack_edit", map[string]any{"stack": "archive", "pull": true, "env_vars": map[string]any{"NEW": "n"}}); !strings.Contains(msg, "the settings of archive were saved and its variables were not") {
			t.Errorf("variables that are not saved = %q", msg)
		}
	})
}

func TestGitStackSync(t *testing.T) {
	t.Parallel()

	dock := withGitStacks(t)
	suite := session(t, dock, quick)

	// fetched, with what differs and what a deploy would take away, and that it still runs from the commit before
	dock.Answer(archiveGitSync, `{"success":true,"commit":"d7da76e","composeContent":"services: {}\n","composeDir":"/app/data/git-repos/alpha/archive","composeFileName":"compose.yaml","updated":true,"changedFiles":[".env","extra.txt"],"deletionPlan":{"toDelete":[{"path":"old.txt","hash":"b69e"}],"skipped":[]},"newFiles":{".env":"e527"},"newCommitFull":"d7da76e6ff2b0dda7a8a564af98878ba06c4a2f1","previousManifest":{"commit":"11f65b2c02358a74cae5a5b2fc6acb59e0aa54be","files":{}}}`)
	out := suite.Call(t, "git_stack_sync", map[string]any{"stack": "archive"})
	if got := flat(out); got != `{"changed":[".env","extra.txt"],"commit":"d7da76e","deployed":"11f65b2","env":"alpha","note":"it still runs from 11f65b2: git_stack_deploy brings it up from what was fetched, which its schedule and its webhook no longer will","stack":"archive","was":"11f65b2","would_go":["old.txt"]}` {
		t.Errorf("a git stack fetched = %s", got)
	}

	// nothing new, and one never deployed
	dock.Answer(archiveGitSync, `{"success":true,"commit":"11f65b2","updated":false,"changedFiles":[]}`)
	if out = suite.Call(t, "git_stack_sync", map[string]any{"stack": "archive"}); out["note"] != "it runs from this commit already: there is nothing new to deploy" || len(acctest.Strs(t, out["changed"], "changed")) != 0 || out["would_go"] != nil {
		t.Errorf("a git stack with nothing new = %v", out)
	}
	dock.Answer("POST /api/git/stacks/6/sync", `{"success":true,"commit":"0c08231","updated":true,"changedFiles":["(new clone - all files)"]}`)
	if out = suite.Call(t, "git_stack_sync", map[string]any{"stack": "shop"}); out["note"] != "it has never been deployed: git_stack_deploy brings it up from what was fetched" || out["was"] != nil || out["deployed"] != nil {
		t.Errorf("a git stack never deployed, fetched = %v", out)
	}

	// what git says when it cannot is passed on, with any password in it hidden
	dock.Answer(archiveGitSync, `{"success":false,"error":"Git clone failed: fatal: unable to access 'https://bot:hunter2@git.example/team/apps.git/'"}`)
	if msg := suite.CallErr(t, "git_stack_sync", map[string]any{"stack": "archive"}); !strings.Contains(msg, "archive was not fetched: Git clone failed") || strings.Contains(msg, "hunter2") {
		t.Errorf("a git stack that cannot be fetched = %q", msg)
	}
	dock.Fail(archiveGitSync, http.StatusInternalServerError, "Failed to sync git stack")
	if msg := suite.CallErr(t, "git_stack_sync", map[string]any{"stack": "archive"}); !strings.Contains(msg, "archive was not fetched") || !strings.Contains(msg, "Failed to sync git stack") {
		t.Errorf("a fetch Dockhand refuses = %q", msg)
	}
	if msg := suite.CallErr(t, "git_stack_sync", map[string]any{"stack": "nope"}); !strings.Contains(msg, `no git stack is named "nope"`) {
		t.Errorf("a git stack there is none of = %q", msg)
	}
}

func TestGitStackDeploy(t *testing.T) {
	t.Parallel()

	dock := withGitStacks(t)
	suite := session(t, dock, quick)

	// a dry run asks the repository and says what would be fetched
	out := suite.Call(t, "git_stack_deploy", map[string]any{"stack": "archive", "dry_run": true})
	if got := flat(out); got != `{"changes":["would fetch its repository at the commit d7da76e and bring up archive on alpha from it"],"commit":"d7da76e","dry_run":true,"env":"alpha","from":"11f65b2","is":"stopped","running":false,"stack":"archive","was":"stopped"}` || len(dock.Requests(archiveGitUp)) != 0 {
		t.Errorf("a dry run = %s", got)
	}

	// fetched and brought up as a job, and read back: the commit it runs from now, and how it stands
	dock.Answer(archiveGitUp, gitJobStarted)
	dock.Answer(gitJob, gitDeployDone)
	dock.Then(archiveGitUp, func(dockhandtest.Request) {
		dock.Answer(archiveGitCall, strings.Replace(archiveGitStack, `{\"commit\":\"11f65b2c02358a74cae5a5b2fc6acb59e0aa54be\"`, `{\"commit\":\"d7da76e6ff2b0dda7a8a564af98878ba06c4a2f1\"`, 1))
	})
	out = suite.Call(t, "git_stack_deploy", map[string]any{"stack": "archive", "only_if_changed": true})
	if got := flat(out); got != `{"changes":["fetched its repository and brought up archive on alpha from it"],"commit":"d7da76e","env":"alpha","from":"11f65b2","is":"stopped","note":"it was deployed and is stopped: stack_list says which of its containers are not running","reported":["Connecting to repository...","File changes: 1 added, 1 updated, 0 removed, 0 skipped, 1 unchanged","Container blog-app-1 Started","API_TOKEN=*** given","Successfully deployed"],"running":false,"stack":"archive","was":"stopped"}` {
		t.Errorf("a git stack deployed = %s", got)
	}
	dock.Then(archiveGitUp, nil)

	// asked only for a change, one that runs from the newest commit is left alone
	deploys := len(dock.Requests(archiveGitUp))
	out = suite.Call(t, "git_stack_deploy", map[string]any{"stack": "archive", "only_if_changed": true})
	if out["note"] != "nothing to do: it runs from d7da76e, which is the newest commit on its branch" || len(acctest.Strs(t, out["changes"], "changes")) != 0 || out["is"] != "stopped" || len(dock.Requests(archiveGitUp)) != deploys {
		t.Errorf("a git stack with nothing new = %v", out)
	}

	// still going when the wait is over, a failure, and a stack that cannot be read back afterwards
	dock.Answer(gitJob, gitDeployRunning)
	if out = suite.Call(t, "git_stack_deploy", map[string]any{"stack": "archive", "wait": 0}); !acctest.BoolOf(out["running"]) || out["job"] != "g1" || out["commit"] != nil || !strings.Contains(acctest.Str(out["note"]), "the deploy was still going after 0 seconds") {
		t.Errorf("a deploy still going = %v", out)
	}
	dock.Answer(gitJob, gitDeployFailed)
	if msg := suite.CallErr(t, "git_stack_deploy", map[string]any{"stack": "archive"}); !strings.Contains(msg, "archive on alpha was not deployed: Compose file not found: nope.yaml; what was running is left as it was") {
		t.Errorf("a deploy that fails = %q", msg)
	}
	dock.Answer(gitJob, gitDeployDone)
	dock.Then(archiveGitUp, func(dockhandtest.Request) {
		dock.FailWhen(stacksCall, "env", "1", http.StatusInternalServerError, "Failed to list stacks")
	})
	if out = suite.Call(t, "git_stack_deploy", map[string]any{"stack": "archive"}); out["is"] != nil || !strings.Contains(acctest.Str(out["note"]), "how it stands after could not be read back") {
		t.Errorf("a deploy that cannot be read back = %v", out)
	}
	dock.Then(archiveGitUp, nil)
	if msg := suite.CallErr(t, "git_stack_deploy", map[string]any{"stack": "archive"}); !strings.Contains(msg, "how archive stands could not be read, so nothing was deployed") {
		t.Errorf("a stack that cannot be read = %q", msg)
	}
	dock.AnswerWhen(stacksCall, "env", "1", alphaStacks)

	// a repository that cannot be asked is said before anything is deployed
	dock.Answer(archiveGitTry, notFetchable)
	if msg := suite.CallErr(t, "git_stack_deploy", map[string]any{"stack": "archive", "dry_run": true}); !strings.Contains(msg, "Dockhand could not fetch the repository of archive, so nothing was deployed: could not read Username") {
		t.Errorf("a repository out of reach = %q", msg)
	}
	dock.Fail(archiveGitTry, http.StatusInternalServerError, "Failed to test git stack")
	if msg := suite.CallErr(t, "git_stack_deploy", map[string]any{"stack": "archive", "only_if_changed": true}); !strings.Contains(msg, "the repository of archive could not be asked, so nothing was deployed") {
		t.Errorf("a repository that cannot be asked = %q", msg)
	}
	for what, tc := range map[string]struct {
		args map[string]any
		want string
	}{
		"a wait that is too long": {map[string]any{"stack": "archive", "wait": 9999}, "wait is 9999"},
		"one there is none of":    {map[string]any{"stack": "nope"}, `no git stack is named "nope"`},
	} {
		if msg := suite.CallErr(t, "git_stack_deploy", tc.args); !strings.Contains(msg, tc.want) {
			t.Errorf("%s = %q, want %q", what, msg, tc.want)
		}
	}
}

func TestGitStackDelete(t *testing.T) {
	t.Parallel()

	dock := withGitStacks(t)
	suite := session(t, dock, everything)

	// without confirm, what would go and what would stay is said and nothing is done
	out := suite.Call(t, "git_stack_delete", map[string]any{"stack": "archive"})
	if got := flat(out); got != `{"containers":["archive-job-1"],"env":"alpha","note":"nothing was done: confirm forgets it","removed":false,"stack":"archive","stays":["its containers, left as they are: archive-job-1","the files they were brought up from, in Dockhand's folder for the stack"],"would_go":["the tie of archive to the repository apps, and Dockhand's copy of it","the variables and secrets Dockhand keeps for it","its schedule, 0 4 * * *","its webhook, which a git server may still be calling"]}` || len(writesOf(dock)) != 0 {
		t.Errorf("a git stack asked about = %s, having sent %v", got, writesOf(dock))
	}

	// forgotten, and read back as gone, with its containers said to be left
	dock.Answer(archiveGitDrop, done)
	dock.Then(archiveGitDrop, func(dockhandtest.Request) { dock.Answer(gitStacksCall, `[]`) })
	out = suite.Call(t, "git_stack_delete", map[string]any{"stack": "archive", "confirm": true})
	if !acctest.BoolOf(out["removed"]) || out["would_go"] != nil || out["note"] != "its containers are left as they were, as a stack Dockhand only found: archive-job-1" {
		t.Errorf("a git stack forgotten = %v", out)
	}
	dock.Then(archiveGitDrop, nil)
	dock.Answer(gitStacksCall, threeGitStacks)

	// one made with no environment is found by its name alone, and has no containers to speak of
	dock.Answer(lostGitDrop, done)
	if msg := suite.CallErr(t, "git_stack_delete", map[string]any{"stack": "lost", "confirm": true}); !strings.Contains(msg, "Dockhand answered that lost was forgotten, and still lists it") {
		t.Errorf("a git stack still listed = %q", msg)
	}
	dock.Then(lostGitDrop, func(dockhandtest.Request) { dock.Fail(gitStacksCall, http.StatusForbidden, notAllowed) })
	out = suite.Call(t, "git_stack_delete", map[string]any{"stack": "lost", "confirm": true})
	if out["env"] != "(none)" || !acctest.BoolOf(out["removed"]) || out["containers"] != nil || out["stays"] != nil || !strings.Contains(acctest.Str(out["note"]), "it was forgotten, and the git stacks could not be listed afterwards") {
		t.Errorf("a git stack with no environment, forgotten = %v", out)
	}
	dock.Then(lostGitDrop, nil)
	dock.Answer(gitStacksCall, threeGitStacks)
	if msg := suite.CallErr(t, "git_stack_delete", map[string]any{"stack": "lost", "env": "alpha"}); !strings.Contains(msg, `no git stack is named "lost" on the environments asked`) {
		t.Errorf("a git stack with no environment, asked for on one = %q", msg)
	}

	// one with nothing running is forgotten with nothing to say of containers
	dock.Answer("DELETE /api/git/stacks/6", done)
	dock.Then("DELETE /api/git/stacks/6", func(dockhandtest.Request) { dock.Answer(gitStacksCall, `[]`) })
	if out = suite.Call(t, "git_stack_delete", map[string]any{"stack": "shop", "confirm": true}); !acctest.BoolOf(out["removed"]) || out["note"] != nil || out["stays"] != nil {
		t.Errorf("a git stack never deployed, forgotten = %v", out)
	}
	dock.Then("DELETE /api/git/stacks/6", nil)
	dock.Answer(gitStacksCall, threeGitStacks)

	dock.Fail(archiveGitDrop, http.StatusNotFound, "Git stack not found")
	if msg := suite.CallErr(t, "git_stack_delete", map[string]any{"stack": "archive", "confirm": true}); !strings.Contains(msg, "the git stack archive was not forgotten") {
		t.Errorf("a git stack Dockhand will not forget = %q", msg)
	}
	dock.FailWhen(stacksCall, "env", "1", http.StatusInternalServerError, "Failed to list stacks")
	if msg := suite.CallErr(t, "git_stack_delete", map[string]any{"stack": "archive"}); !strings.Contains(msg, "how archive stands could not be read, so nothing was done") {
		t.Errorf("a stack that cannot be read = %q", msg)
	}
	if msg := suite.CallErr(t, "git_stack_delete", map[string]any{"stack": "nope"}); !strings.Contains(msg, `no git stack is named "nope"`) {
		t.Errorf("a git stack there is none of = %q", msg)
	}
}

// A stack deployed from git is the repository's: the tools that change an ordinary stack's files refuse one, and say rightly why it has no file yet.
func TestStackToolsOnAGitStack(t *testing.T) {
	t.Parallel()

	dock := withGitStacks(t)
	suite := session(t, dock, quick)

	// its files, its secrets and its provider are all refused, since saving any the ordinary way loses something
	for _, args := range []map[string]any{
		{"stack": "archive", "compose": "services: {}\n"},
		{"stack": "archive", "env_file": "MODE=x\n"},
		{"stack": "archive", "secrets": map[string]any{"KEY": "made-up"}},
		{"stack": "archive", "secret_provider": "main"},
	} {
		if msg := suite.CallErr(t, "stack_edit", args); !strings.Contains(msg, "archive is deployed from a git repository, and is changed as a git stack") {
			t.Errorf("stack_edit of a git stack with %v = %q", args, msg)
		}
	}
	if writes := writesOf(dock); len(writes) != 0 {
		t.Errorf("something was saved all the same: %v", writes)
	}

	// one Dockhand has not fetched yet has no file, and is said to be waiting for a fetch rather than to have been found on the host
	dock.Fail("GET /api/stacks/archive/compose", http.StatusNotFound, "Select the compose file location")
	if msg := suite.CallErr(t, "stack_deploy", map[string]any{"stack": "archive"}); !strings.Contains(msg, "archive is deployed from a git repository that Dockhand has not fetched yet") {
		t.Errorf("stack_deploy of a git stack never fetched = %q", msg)
	}
	dock.Answer("GET /api/stacks/archive/deploys", `{"runs":[]}`)
	out := suite.Call(t, "stack_get", map[string]any{"stack": "archive"})
	if out["kept"] != "git" || acctest.BoolOf(out["deployable"]) || !strings.Contains(acctest.Str(out["note"]), "Dockhand deploys this stack from a git repository and has no copy of its compose file") {
		t.Errorf("stack_get of a git stack never fetched = %v", out)
	}
	dock.Answer("GET /api/stacks/archive/delete-preview", `{"sourceType":"git","stackDir":null,"canDeleteFiles":false,"namedVolumes":[]}`)
	if out = suite.Call(t, "stack_delete", map[string]any{"stack": "archive"}); !strings.Contains(acctest.Str(out["note"]), "Dockhand holds no files for this stack to delete: it is deployed from git and was never fetched") {
		t.Errorf("stack_delete of a git stack never fetched = %v", out)
	}
}
