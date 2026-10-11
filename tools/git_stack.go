package tools

import (
	"bytes"
	"cmp"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"slices"
	"strings"
	"time"

	"github.com/katbyte/dockhand-mcp/sdk/dockhand"
	"github.com/katbyte/go-kt/parallel"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

const (
	// noEnvironment is where a git stack made with no environment is said to be: only Dockhand's own API makes one, and nothing here deploys it
	noEnvironment = "(none)"
	// gitSynced is how Dockhand says a git stack's last fetch went well
	gitSynced = "synced"
	// customSchedule is the word Dockhand keeps beside a cron that is none of the ones its web interface offers by name: the cron is what it goes by
	customSchedule = "custom"
	// gitReportKept is how many of the last lines a git deploy reported a tool gives
	gitReportKept = 15
)

// gitStackArg names one git stack.
type gitStackArg struct {
	envArg

	Stack string `json:"stack" jsonschema:"the git stack, by its name as git_stack_list gives it"`
}

// gitStackRow is a git stack as the tools list it.
type gitStackRow struct {
	ID          int    `json:"id"`
	Env         string `json:"env"                   jsonschema:"the environment (Docker host) it is deployed on"`
	Stack       string `json:"stack"`
	Repository  string `json:"repository"            jsonschema:"the git repository it is deployed from, by name"`
	Branch      string `json:"branch"                jsonschema:"the branch it follows: its own, or its repository's"`
	ComposePath string `json:"compose_path"          jsonschema:"the compose file's path in the repository"`
	Sync        string `json:"sync"                  jsonschema:"how its last fetch or deploy went: pending for one never fetched, synced, or error"`
	SyncError   string `json:"sync_error,omitempty"  jsonschema:"why, when it went wrong"`
	Commit      string `json:"commit,omitempty"      jsonschema:"the commit last fetched, shortened"`
	Fetched     string `json:"fetched,omitempty"     jsonschema:"when it was last fetched"`
	AutoUpdate  string `json:"auto_update,omitempty" jsonschema:"the cron it is fetched on by itself, and deployed when something changed; absent when it is not"`
	Webhook     bool   `json:"webhook,omitempty"     jsonschema:"whether a call to its webhook has it fetched and deployed"`
	Deployed    string `json:"deployed,omitempty"    jsonschema:"with check: the commit the files it runs from were taken at"`
	Newest      string `json:"newest,omitempty"      jsonschema:"with check: the newest commit on its branch now"`
	Behind      bool   `json:"behind,omitempty"      jsonschema:"with check: what it runs from is not the newest commit on its branch"`
	CheckError  string `json:"check_error,omitempty" jsonschema:"with check: why its repository could not be asked"`
}

// gitStackRowOf is a git stack as the tools list it, on the environment of that name.
func gitStackRowOf(s *dockhand.GetGitStacksResponse, env string) gitStackRow {
	row := gitStackRow{ID: s.Id, Env: env, Stack: s.StackName, Branch: s.Branch, ComposePath: s.ComposePath, Sync: s.SyncStatus, SyncError: gitSaid(s.SyncError), Commit: s.LastCommit, Fetched: s.LastSync, Webhook: yes(s.WebhookEnabled)}
	if s.SyncError == "" {
		row.SyncError = ""
	}
	if s.Repository != nil {
		row.Repository, row.Branch = s.Repository.Name, cmp.Or(s.Branch, s.Repository.Branch)
	}
	if yes(s.AutoUpdate) {
		row.AutoUpdate = s.AutoUpdateCron
	}

	return row
}

// gitStackOf is a git stack as any of the calls that answer one sends it, read as the list's record: the same fields, declared four times over.
func gitStackOf(answered any) dockhand.GetGitStacksResponse {
	var s dockhand.GetGitStacksResponse
	if raw, err := json.Marshal(answered); err == nil {
		_ = json.Unmarshal(raw, &s)
	}

	return s
}

// deployedCommit is the commit the files a git stack runs from were taken at, shortened as Dockhand shortens one, or nothing for a stack never
// deployed: Dockhand keeps it apart from the commit last fetched, which a fetch alone moves on.
func deployedCommit(syncedFiles string) string {
	var taken struct {
		Commit string `json:"commit"`
	}
	if json.Unmarshal([]byte(syncedFiles), &taken) != nil || len(taken.Commit) < 7 {
		return ""
	}

	return taken.Commit[:7]
}

// webhookPath is where a git stack's webhook is called, on the Dockhand's own address.
func webhookPath(id int) string {
	return fmt.Sprintf("/api/git/stacks/%d/webhook", id)
}

// findGitStack is the one git stack a call names, on the environment given or on every one. A name that is a git stack's on several environments is
// refused until the call says which. One made with no environment is found only when unplaced says so, for the one tool that can act on it, and is
// otherwise refused as what it is.
func (r *registry) findGitStack(ctx context.Context, in gitStackArg, unplaced bool) (environment, *dockhand.GetGitStacksResponse, error) {
	want := strings.TrimSpace(in.Stack)
	if want == "" {
		return environment{}, nil, errors.New("stack is required: the git stack's name, as git_stack_list gives it")
	}
	envs, err := r.pick(ctx, in.envArg)
	if err != nil {
		return environment{}, nil, err
	}
	listed, err := r.gitStacks(ctx)
	if err != nil {
		return environment{}, nil, err
	}

	var (
		on      []environment
		found   []*dockhand.GetGitStacksResponse
		nowhere bool
	)
	for i := range listed {
		if listed[i].StackName != want {
			continue
		}
		at := slices.IndexFunc(envs, func(e environment) bool { return e.ID == listed[i].EnvironmentId })
		switch {
		case at >= 0:
			on, found = append(on, envs[at]), append(found, &listed[i])
		case listed[i].EnvironmentId == 0 && in.Env == nil && unplaced:
			on, found = append(on, environment{Name: noEnvironment}), append(found, &listed[i])
		case listed[i].EnvironmentId == 0 && in.Env == nil:
			nowhere = true
		default:
		}
	}
	switch len(on) {
	case 0:
		if nowhere {
			return environment{}, nil, fmt.Errorf("the git stack %s was made with no environment, which only Dockhand's own API does: nothing here deploys it, and git_stack_delete removes it", want)
		}

		return environment{}, nil, fmt.Errorf("no git stack is named %q on the environments asked: git_stack_list gives the ones there are", want)
	case 1:
		return on[0], found[0], nil
	default:
		names := make([]string, 0, len(on))
		for _, e := range on {
			names = append(names, e.Name)
		}

		return environment{}, nil, fmt.Errorf("a git stack named %q is on %s: say which with env", want, strings.Join(names, " and "))
	}
}

// gitReported is the last of what a git deploy reported as it went, in words, with any secret in a line hidden and a line said twice said once.
func gitReported(lines []dockhand.GetJobsByIdResponseLines) []string {
	var said []string
	for i := range lines {
		line := cmp.Or(textOf(lines[i].Data["message"]), textOf(lines[i].Data["logLine"]), textOf(lines[i].Data["error"]))
		if line == "" || (len(said) > 0 && said[len(said)-1] == line) {
			continue
		}
		shown, _ := maskText(line)
		said = append(said, shown)
	}
	if len(said) > gitReportKept {
		said = said[len(said)-gitReportKept:]
	}

	return said
}

// gitDeploy has Dockhand fetch a git stack's repository and bring the stack up from it, as a job Dockhand runs, and waits for it as long as the
// patience given. Still running then, the state says so and nothing is reported yet. The call that reports as it goes is the one used: the other
// answers the values of the stack's secrets with how it went.
func (r *registry) gitDeploy(ctx context.Context, id int, patience time.Duration) (reported []string, state jobState, err error) {
	started, err := r.dock.PostGitStacksByIdDeployStream(ctx, id, dockhand.PostGitStacksByIdDeployStreamOperationOptions{Accept: dockhand.AcceptJob})
	if err != nil {
		return nil, jobState{}, refusal(err)
	}
	if started.Model.JobId == "" {
		return nil, jobState{}, errors.New("Dockhand started no job for the deploy, and says nothing of how it went") //nolint:staticcheck // ST1005: the sentence begins with the server's name
	}
	j, err := r.await(ctx, started.Model.JobId, patience)
	if err != nil {
		return nil, jobState{}, err
	}
	state = stateOfJob(j)
	switch {
	case state.Running:
		return nil, state, nil
	case failed(j):
		shown, _ := maskText(failureOf(j))

		return nil, state, errors.New(shown)
	default:
		return gitReported(j.Lines), state, nil
	}
}

// variablesNamed checks the names a call gives for variables Dockhand is to keep for a git stack.
func variablesNamed(variables map[string]string) error {
	for _, name := range slices.Sorted(maps.Keys(variables)) {
		switch {
		case !variableNamed.MatchString(name):
			return fmt.Errorf("%q is no name for a variable: it is letters, digits and underscores, and does not start with a digit", name)
		case name == secretPathKey:
			return fmt.Errorf("%s is the path a stack's secrets are loaded from, which secret_path sets", secretPathKey)
		default:
		}
	}

	return nil
}

//nolint:gocyclo,maintidx // the git stack tools registered in one function, each a handler of its own: the count is theirs added up
func registerGitStackTools(r *registry) {
	type gitStackListIn struct {
		envArg

		Check bool `json:"check,omitempty" jsonschema:"ask each stack's repository for the newest commit on its branch, and say which stacks run from an older one. Dockhand reaches for every repository to answer, which takes a while with many"`
	}
	type gitStackListOut struct {
		Stacks []gitStackRow `json:"stacks"`
		Total  int           `json:"total"`
		Behind int           `json:"behind,omitempty" jsonschema:"with check: how many run from a commit older than the newest on their branch"`
		Failed int           `json:"failed,omitempty" jsonschema:"how many say their last fetch or deploy went wrong"`
		Note   string        `json:"note,omitempty"`
	}
	add(r, readTool, &mcp.Tool{
		Name:        "git_stack_list",
		Description: "List the git stacks the Dockhand keeps, on every Docker host (environment) or on one: the compose stacks it deploys from a git repository rather than from a file it was given. Each is given with its repository and branch, where the compose file is in it, the commit last fetched and how that went, and whether it is fetched by itself on a schedule or by a webhook. With check each repository is asked for its newest commit, and a stack that runs from an older one is said to be behind. How a stack's containers stand is stack_list's to say: a git stack is a stack there as well, under the same name.",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in gitStackListIn) (*mcp.CallToolResult, gitStackListOut, error) {
		envs, err := r.pick(ctx, in.envArg)
		if err != nil {
			return nil, gitStackListOut{}, err
		}
		listed, err := r.gitStacks(ctx)
		if err != nil {
			return nil, gitStackListOut{}, err
		}
		out := gitStackListOut{Stacks: []gitStackRow{}}
		unplaced := 0
		for i := range listed {
			at := slices.IndexFunc(envs, func(e environment) bool { return e.ID == listed[i].EnvironmentId })
			switch {
			case at >= 0:
				out.Stacks = append(out.Stacks, gitStackRowOf(&listed[i], envs[at].Name))
			case listed[i].EnvironmentId == 0 && in.Env == nil:
				out.Stacks, unplaced = append(out.Stacks, gitStackRowOf(&listed[i], noEnvironment)), unplaced+1
			default:
			}
		}
		slices.SortFunc(out.Stacks, func(a, b gitStackRow) int { return cmp.Or(cmp.Compare(a.Env, b.Env), cmp.Compare(a.Stack, b.Stack)) })

		if in.Check {
			// each is asked on its own, a few at a time: one repository that cannot be reached is said for its stack, and the others are still asked
			_ = parallel.Each(ctx, len(out.Stacks), askAtOnce, func(ctx context.Context, i int) error {
				row := &out.Stacks[i]
				tried, err := r.dock.PostGitStacksByIdTest(ctx, row.ID)
				switch {
				case err != nil:
					row.CheckError = refusal(err).Error()
				case !yes(tried.Model.Success):
					row.CheckError = gitSaid(tried.Model.Error)
				default:
					row.Newest = tried.Model.LastCommit
					if got, err := r.dock.GetGitStacksById(ctx, row.ID); err == nil {
						row.Deployed = deployedCommit(got.Model.SyncedFiles)
					}
					row.Behind = row.Newest != "" && row.Deployed != row.Newest
				}

				return nil
			})
		}
		for i := range out.Stacks {
			if out.Stacks[i].Behind {
				out.Behind++
			}
			if out.Stacks[i].Sync != gitSynced && out.Stacks[i].SyncError != "" {
				out.Failed++
			}
		}
		out.Total = len(out.Stacks)
		switch {
		case out.Total == 0:
			out.Note = "no stack here is deployed from git"
		case unplaced > 0:
			out.Note = several(unplaced, "git stack") + map[bool]string{true: " was", false: " were"}[unplaced == 1] + " made with no environment, which only Dockhand's own API does: nothing here deploys one, and git_stack_delete removes it"
		default:
		}

		return nil, out, nil
	})

	type gitStackGetIn struct {
		gitStackArg
		revealArg

		ReadEnvFile string `json:"read_env_file,omitempty" jsonschema:"a .env file in Dockhand's copy of the repository to read the variables of, by its path as env_files gives it"`
	}
	type gitStackGetOut struct {
		gitStackRow

		Status         string          `json:"status,omitempty"          jsonschema:"how the stack stands on its host: running, partial, stopped, created for one with no containers, or gone for one Dockhand has never deployed; absent when the host could not be asked"`
		EnvFile        string          `json:"env_file,omitempty"        jsonschema:"one more .env file in the repository that the compose file is filled in from"`
		ContextDir     string          `json:"context_dir,omitempty"     jsonschema:"the folder of the repository copied beside the compose file; absent for the folder the compose file is in"`
		Build          bool            `json:"build,omitempty"           jsonschema:"each deploy builds the images the compose file says to build"`
		NoBuildCache   bool            `json:"no_build_cache,omitempty"  jsonschema:"those builds start from nothing"`
		Pull           bool            `json:"pull,omitempty"            jsonschema:"each deploy pulls every image first"`
		Recreate       bool            `json:"recreate,omitempty"        jsonschema:"each deploy makes every container again, and the schedule and the webhook deploy even when nothing in the repository changed"`
		WebhookPath    string          `json:"webhook_path,omitempty"    jsonschema:"where a git server calls to have the stack fetched and deployed, on the Dockhand's own address: it takes the secret as ?secret=, as GitLab's token header, or as GitHub's signature, and no login"`
		WebhookSecret  string          `json:"webhook_secret,omitempty"  jsonschema:"the webhook's secret, hidden unless reveal_secrets asks for it"`
		EnvFiles       []string        `json:"env_files,omitempty"       jsonschema:"the .env files in Dockhand's copy of the repository, once it has one"`
		Variables      []stackVariable `json:"variables,omitempty"       jsonschema:"the variables Dockhand itself keeps for the stack, which fill in the compose file over the repository's own .env"`
		SecretProvider string          `json:"secret_provider,omitempty" jsonschema:"the secret provider its secrets are loaded from each time it is deployed, by name"`
		SecretPath     string          `json:"secret_path,omitempty"     jsonschema:"the path in that provider whose every key is loaded as a variable of that name"`
		FileVariables  []stackVariable `json:"file_variables,omitempty"  jsonschema:"with read_env_file: the variables in that file"`
		Hidden         int             `json:"hidden,omitempty"          jsonschema:"how many values are hidden for being secrets"`
		Note           string          `json:"note,omitempty"`
	}
	add(r, readTool, &mcp.Tool{
		Name:        "git_stack_get",
		Description: "Describe one git stack in full: its repository, branch and compose file, the commit last fetched, the commit its running files were taken at and the newest on its branch now, how each deploy is done, its schedule and its webhook, the .env files in Dockhand's copy of the repository, and the variables Dockhand keeps for it besides. A stack whose newest commit is not the one it runs from is behind, which a fetch alone does not put right. The webhook's secret and values with a secret's name are hidden unless reveal_secrets asks for them; the ones Dockhand keeps as secrets it never gives back. The compose file itself and the containers are the stack's own, which the stack tools give under the same name.",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in gitStackGetIn) (*mcp.CallToolResult, gitStackGetOut, error) {
		env, listed, err := r.findGitStack(ctx, in.gitStackArg, false)
		if err != nil {
			return nil, gitStackGetOut{}, err
		}
		got, err := r.dock.GetGitStacksById(ctx, listed.Id)
		if err != nil {
			return nil, gitStackGetOut{}, fmt.Errorf("the git stack %s could not be read: %w", listed.StackName, refusal(err))
		}
		stack := gitStackOf(got.Model)
		out := gitStackGetOut{gitStackRow: gitStackRowOf(&stack, env.Name), EnvFile: stack.EnvFilePath, ContextDir: stack.ContextDir, Build: yes(stack.BuildOnDeploy), NoBuildCache: yes(stack.NoBuildCache), Pull: yes(stack.RepullImages), Recreate: yes(stack.ForceRedeploy)}
		out.Deployed = deployedCommit(stack.SyncedFiles)
		if out.Webhook {
			out.WebhookPath, out.WebhookSecret = webhookPath(stack.Id), stack.WebhookSecret
			if !in.RevealSecrets && out.WebhookSecret != "" {
				out.WebhookSecret, out.Hidden = masked, 1
			}
		}

		// each of what follows describes a stack that is known to be there, and one failing is said rather than hiding the rest
		if out.Status, _, err = r.standing(ctx, env, stack.StackName); err != nil {
			out.Status, out.Note = "", "how it stands on "+env.Name+" could not be read: "+err.Error()
		}
		tried, err := r.dock.PostGitStacksByIdTest(ctx, stack.Id)
		switch {
		case err != nil:
			out.CheckError = refusal(err).Error()
		case !yes(tried.Model.Success):
			out.CheckError = gitSaid(tried.Model.Error)
		default:
			out.Newest = tried.Model.LastCommit
			out.Behind = out.Deployed != out.Newest
		}
		if files, err := r.dock.GetGitStacksByIdEnvFiles(ctx, stack.Id); err == nil {
			out.EnvFiles = files.Model.Files
		}
		if vars, err := r.dock.GetStacksByNameEnv(ctx, stack.StackName, dockhand.GetStacksByNameEnvOperationOptions{Env: &env.ID}); err != nil {
			out.Note = noted(out.Note, "the variables Dockhand keeps for it could not be read: "+refusal(err).Error())
		} else {
			for i := range vars.Model.Variables {
				v := stackVariable{Name: vars.Model.Variables[i].Key, Value: vars.Model.Variables[i].Value, Secret: yes(vars.Model.Variables[i].IsSecret)}
				if v.Name == secretPathKey {
					out.SecretPath = v.Value

					continue
				}
				if !in.RevealSecrets && !v.Secret {
					shown, wasHidden := maskValue(v.Name, v.Value)
					if v.Value = shown; wasHidden {
						out.Hidden++
					}
				}
				out.Variables = append(out.Variables, v)
			}
			slices.SortFunc(out.Variables, func(a, b stackVariable) int { return cmp.Compare(a.Name, b.Name) })
			if bound := vars.Model.SecretProvider; bound != nil {
				out.SecretProvider = bound.Name
			}
		}

		if file := strings.TrimSpace(in.ReadEnvFile); file != "" {
			read, err := r.dock.PostGitStacksByIdEnvFiles(ctx, stack.Id, dockhand.PostGitStacksByIdEnvFilesRequest{Path: file})
			if err != nil {
				return nil, out, fmt.Errorf("%s could not be read from Dockhand's copy of the repository: %w", file, refusal(err))
			}
			out.FileVariables = []stackVariable{}
			for _, name := range slices.Sorted(maps.Keys(read.Model.Vars)) {
				value, hidden := read.Model.Vars[name], false
				if !in.RevealSecrets {
					value, hidden = maskValue(name, value)
				}
				if hidden {
					out.Hidden++
				}
				out.FileVariables = append(out.FileVariables, stackVariable{Name: name, Value: value})
			}
		}

		switch {
		case out.Deployed == "":
			out.Note = noted(out.Note, "Dockhand has never deployed it: git_stack_deploy fetches the repository and brings it up")
		case out.Behind && out.Commit == out.Newest:
			out.Note = noted(out.Note, fmt.Sprintf("the newest commit, %s, was fetched and never deployed: it runs from %s, and the schedule and the webhook see nothing new to deploy. git_stack_deploy brings it up to date", out.Newest, out.Deployed))
		case out.Behind:
			out.Note = noted(out.Note, fmt.Sprintf("it runs from %s and the newest commit on %s is %s: git_stack_deploy brings it up to date", out.Deployed, out.Branch, out.Newest))
		default:
		}
		if out.Hidden > 0 {
			out.Note = noted(out.Note, hiddenNote(out.Hidden, "the webhook's, and values told by the names beside them"))
		}

		return nil, out, nil
	})

	type gitStackCreateIn struct {
		envArg
		waitArg
		dryRunArg

		Name           string            `json:"name"                       jsonschema:"the stack's name, which is its compose project: lower-case letters, digits, hyphens and underscores, starting with a letter or a digit"`
		Repository     any               `json:"repository"                 jsonschema:"the git repository to deploy it from, by its name or number as git_repository_list gives them"`
		Branch         string            `json:"branch,omitempty"           jsonschema:"a branch for this stack to follow in place of the repository's own"`
		ComposePath    string            `json:"compose_path,omitempty"     jsonschema:"the compose file's path in the repository; compose.yaml unless said"`
		EnvFile        string            `json:"env_file,omitempty"         jsonschema:"one more .env file in the repository to fill the compose file in from, by its path there, beside the .env next to the compose file"`
		ContextDir     string            `json:"context_dir,omitempty"      jsonschema:"the folder of the repository to copy beside the compose file, for a file that reads others outside its own folder; the compose file's folder unless said"`
		Build          bool              `json:"build,omitempty"            jsonschema:"have each deploy build the images the compose file says to build"`
		NoBuildCache   bool              `json:"no_build_cache,omitempty"   jsonschema:"have those builds start from nothing"`
		Pull           bool              `json:"pull,omitempty"             jsonschema:"have each deploy pull every image first, so that a newer image under the same tag is what runs"`
		Recreate       bool              `json:"recreate,omitempty"         jsonschema:"have each deploy make every container again, and the schedule and the webhook deploy even when nothing in the repository changed"`
		AutoUpdateCron string            `json:"auto_update_cron,omitempty" jsonschema:"have Dockhand fetch the repository by itself at these times, as five cron fields, and deploy when something changed: \"0 3 * * *\" for three every morning"`
		WebhookSecret  string            `json:"webhook_secret,omitempty"   jsonschema:"switch the stack's webhook on with this secret, which a git server then sends to have the stack fetched and deployed. It is kept by Dockhand, which gives it back to whoever reads the stack"`
		Variables      map[string]string `json:"env_vars,omitempty"         jsonschema:"variables for Dockhand to keep for the stack, each name with its value: they fill in the compose file over the repository's own .env"`
		Secrets        map[string]string `json:"secrets,omitempty"          jsonschema:"variables Dockhand is to keep as secrets, each name with its value: they fill in the compose file as the others do and are never given back"`
		SecretProvider any               `json:"secret_provider,omitempty"  jsonschema:"a secret provider the Dockhand keeps, by name or number, to load the stack's secrets from each time it is deployed"`
		SecretPath     string            `json:"secret_path,omitempty"      jsonschema:"with secret_provider: the path in it whose every key is loaded as a variable of that name"`
		Start          *bool             `json:"start,omitempty"            jsonschema:"fetch the repository and bring the stack up once it is made: true unless said. False makes it and leaves it never deployed"`
	}
	type gitStackCreateOut struct {
		jobState

		Env         string   `json:"env"                    jsonschema:"the environment (Docker host) the stack was made on"`
		Stack       string   `json:"stack"`
		Repository  string   `json:"repository"`
		Branch      string   `json:"branch"`
		Commit      string   `json:"commit,omitempty"       jsonschema:"the commit it was brought up from; for one not started, the newest on its branch"`
		Status      string   `json:"status,omitempty"       jsonschema:"how the stack stands, read back: running once brought up, gone for one never deployed; absent while it is still being brought up"`
		WebhookPath string   `json:"webhook_path,omitempty" jsonschema:"with webhook_secret: where a git server calls to have it fetched and deployed, on the Dockhand's own address"`
		Reported    []string `json:"reported,omitempty"     jsonschema:"the last of what the deploy reported as it went"`
		Changes     []string `json:"changes"                jsonschema:"what was done, in words"`
		DryRun      bool     `json:"dry_run,omitempty"      jsonschema:"nothing was done: changes says what would be"`
		Note        string   `json:"note,omitempty"`
	}
	add(r, writeTool, &mcp.Tool{
		Name:        "git_stack_create",
		Description: "Make a compose stack that Dockhand deploys from a git repository it keeps, on one Docker host (environment), and bring it up unless start is false. Each deploy fetches the repository and copies the compose file, with what lies beside it, to where Dockhand keeps the stack, so the stack's files are the repository's and are changed there. The repository and the branch are tried first and a stack whose repository cannot be fetched is not made; so is a name a stack on that host already has. With auto_update_cron Dockhand fetches by itself and deploys what changed, and with webhook_secret a git server can call for the same. Bringing it up runs in the background on Dockhand: this waits up to wait seconds for it and, when it is still going then, answers with running true and the job's id for job_get. A stack that was made and could not be brought up is kept, and the error says so.",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in gitStackCreateIn) (*mcp.CallToolResult, gitStackCreateOut, error) {
		name, cron, hook := strings.TrimSpace(in.Name), strings.TrimSpace(in.AutoUpdateCron), strings.TrimSpace(in.WebhookSecret)
		if !stackNamed.MatchString(name) {
			return nil, gitStackCreateOut{}, fmt.Errorf("name is %q: a stack's name is lower-case letters, digits, hyphens and underscores, and starts with a letter or a digit", in.Name)
		}
		if err := variablesNamed(in.Variables); err != nil {
			return nil, gitStackCreateOut{}, err
		}
		if err := secretsNamed(in.Secrets); err != nil {
			return nil, gitStackCreateOut{}, err
		}
		for secret := range in.Secrets {
			if _, both := in.Variables[secret]; both {
				return nil, gitStackCreateOut{}, fmt.Errorf("%s is given both in env_vars and in secrets", secret)
			}
		}
		if cron != "" {
			checked, err := cronOf(cron)
			if err != nil {
				return nil, gitStackCreateOut{}, fmt.Errorf("auto_update_cron: %w", err)
			}
			cron = checked
		}
		patience, err := in.patience()
		if err != nil {
			return nil, gitStackCreateOut{}, err
		}
		env, err := r.one(ctx, in.envArg)
		if err != nil {
			return nil, gitStackCreateOut{}, err
		}
		repositories, err := r.gitRepositories(ctx)
		if err != nil {
			return nil, gitStackCreateOut{}, err
		}
		repository, err := oneOf(repositories, in.Repository, "repository", "git repository", gitRepositoryID, gitRepositoryName)
		if err != nil {
			return nil, gitStackCreateOut{}, err
		}
		branch := cmp.Or(strings.TrimSpace(in.Branch), repository.Branch)
		out := gitStackCreateOut{Env: env.Name, Stack: name, Repository: repository.Name, Branch: branch, Changes: []string{}, DryRun: in.DryRun}

		// Dockhand takes a name another stack has, and deploys the repository over that stack's files
		is, _, err := r.standing(ctx, env, name)
		if err != nil {
			return nil, out, fmt.Errorf("the stacks of %s could not be listed, so nothing was made: %w", env.Name, err)
		}
		kept, err := r.keptAs(ctx, env, name)
		if err != nil {
			return nil, out, fmt.Errorf("the stacks Dockhand keeps for %s could not be listed, so nothing was made: %w", env.Name, err)
		}
		if is != stackGone || kept != keptNowhere {
			return nil, out, fmt.Errorf("a stack named %s is already on %s: a new stack takes a name of its own", name, env.Name)
		}
		provider, path, err := r.secretsFor(ctx, in.SecretProvider, &in.SecretPath, 0, "")
		if err != nil {
			return nil, out, fmt.Errorf("%w, so nothing was made", err)
		}
		tried, err := r.tryRepository(ctx, repository.Url, branch, repository.CredentialId)
		if err != nil {
			return nil, out, fmt.Errorf("the repository %s could not be tried, so nothing was made: %w", repository.Name, err)
		}
		if !tried.Reached {
			return nil, out, fmt.Errorf("Dockhand could not fetch the branch %s of %s, so nothing was made: %s", branch, repository.Name, gitSaid(tried.Error)) //nolint:staticcheck // ST1005: the sentence begins with the server's name
		}
		out.Commit = tried.Commit

		start := in.Start == nil || *in.Start
		how := fmt.Sprintf("the git stack %s on %s, from %s of the branch %s of %s", name, env.Name, cmp.Or(strings.TrimSpace(in.ComposePath), defaultComposePath), branch, repository.Name)
		if provider != nil {
			how += ", its secrets loaded from " + provider.Name
			if path != "" {
				how += " at " + path
			}
		}
		var also []string
		if cron != "" {
			also = append(also, "fetch it by itself at "+cron)
		}
		if hook != "" {
			also = append(also, "fetch and deploy it when its webhook is called")
		}
		if in.DryRun {
			out.Changes = append(out.Changes, "would make "+how)
			if len(also) > 0 {
				out.Changes = append(out.Changes, "would have Dockhand "+strings.Join(also, ", and "))
			}
			if start {
				out.Changes = append(out.Changes, "would fetch the repository and bring it up")
			}

			return nil, out, nil
		}

		request := dockhand.PostGitStacksRequest{StackName: name, EnvironmentId: env.ID, RepositoryId: repository.Id, Branch: strings.TrimSpace(in.Branch), ComposePath: strings.TrimSpace(in.ComposePath), EnvFilePath: strings.TrimSpace(in.EnvFile), ContextDir: strings.TrimSpace(in.ContextDir)}
		for flag, set := range map[**bool]bool{&request.BuildOnDeploy: in.Build, &request.NoBuildCache: in.NoBuildCache, &request.RepullImages: in.Pull, &request.ForceRedeploy: in.Recreate} {
			if set {
				*flag = new(true)
			}
		}
		if cron != "" {
			request.AutoUpdate, request.AutoUpdateSchedule, request.AutoUpdateCron = new(true), customSchedule, cron
		}
		if hook != "" {
			request.WebhookEnabled, request.WebhookSecret = new(true), hook
		}
		if provider != nil {
			request.SecretProviderId = provider.Id
		}
		for _, variable := range slices.Sorted(maps.Keys(in.Variables)) {
			request.EnvVars = append(request.EnvVars, dockhand.PostGitStacksRequestEnvVars{Key: variable, Value: in.Variables[variable]})
		}
		if path != "" {
			request.EnvVars = append(request.EnvVars, dockhand.PostGitStacksRequestEnvVars{Key: secretPathKey, Value: path})
		}
		for _, secret := range slices.Sorted(maps.Keys(in.Secrets)) {
			request.EnvVars = append(request.EnvVars, dockhand.PostGitStacksRequestEnvVars{Key: secret, Value: in.Secrets[secret], IsSecret: new(true)})
		}
		made, err := r.dock.PostGitStacks(ctx, request)
		if err != nil {
			return nil, out, fmt.Errorf("the git stack %s was not made on %s: %w", name, env.Name, refusal(err))
		}
		out.Changes = append(out.Changes, "made "+how)
		if len(also) > 0 {
			out.Changes = append(out.Changes, "had Dockhand "+strings.Join(also, ", and "))
		}
		if hook != "" {
			out.WebhookPath = webhookPath(made.Model.Id)
		}

		if start {
			reported, state, failed := r.gitDeploy(ctx, made.Model.Id, patience)
			out.jobState = state
			if failed != nil {
				return nil, out, fmt.Errorf("the git stack %s was made on %s and could not be brought up: %w; it is kept, with whatever compose had made by then: git_stack_deploy tries again once what is wrong is put right, and git_stack_delete forgets it", name, env.Name, failed)
			}
			if state.Running {
				out.Note = fmt.Sprintf("it was made and was still being brought up after %s: job_get with the job's id says how it ends", seconds(patience))

				return nil, out, nil
			}
			out.Reported, out.Progress = reported, nil
			out.Changes = append(out.Changes, "fetched the repository and brought it up")
		}

		if out.Status, _, err = r.standing(ctx, env, name); err != nil {
			out.Status, out.Note = "", "how it stands could not be read back: "+err.Error()
		} else if start && out.Status != stackRunning {
			out.Note = fmt.Sprintf("it was brought up and is %s: stack_list says which of its containers are not running", out.Status)
		}

		return nil, out, nil
	})

	type gitStackEditIn struct {
		gitStackArg
		dryRunArg

		Name            string            `json:"name,omitempty"             jsonschema:"another name for the stack, which is given alone and only to a stack with no containers: lower-case letters, digits, hyphens and underscores"`
		Branch          *string           `json:"branch,omitempty"           jsonschema:"another branch for it to follow; an empty text has it follow the repository's own"`
		ComposePath     string            `json:"compose_path,omitempty"     jsonschema:"another path for the compose file in the repository"`
		EnvFile         *string           `json:"env_file,omitempty"         jsonschema:"another extra .env file in the repository, by its path there; an empty text has none read"`
		ContextDir      *string           `json:"context_dir,omitempty"      jsonschema:"another folder of the repository to copy beside the compose file; an empty text has it be the compose file's own"`
		Build           *bool             `json:"build,omitempty"            jsonschema:"whether each deploy builds the images the compose file says to build"`
		NoBuildCache    *bool             `json:"no_build_cache,omitempty"   jsonschema:"whether those builds start from nothing"`
		Pull            *bool             `json:"pull,omitempty"             jsonschema:"whether each deploy pulls every image first"`
		Recreate        *bool             `json:"recreate,omitempty"         jsonschema:"whether each deploy makes every container again, and the schedule and the webhook deploy even when nothing changed"`
		AutoUpdateCron  *string           `json:"auto_update_cron,omitempty" jsonschema:"when Dockhand fetches the repository by itself and deploys what changed, as five cron fields; an empty text has it stop"`
		WebhookSecret   *string           `json:"webhook_secret,omitempty"   jsonschema:"switch the webhook on with this secret, or give it another; an empty text switches the webhook off and has Dockhand forget the secret"`
		Variables       map[string]string `json:"env_vars,omitempty"         jsonschema:"variables for Dockhand to keep for the stack, to add or to give a new value; the others are kept as they are"`
		RemoveVariables []string          `json:"remove_env_vars,omitempty"  jsonschema:"variables Dockhand keeps for the stack to remove, by name"`
		Secrets         map[string]string `json:"secrets,omitempty"          jsonschema:"secrets to add or to give a new value; the stack's other secrets are kept as they are"`
		RemoveSecrets   []string          `json:"remove_secrets,omitempty"   jsonschema:"secrets to remove, by name"`
		SecretProvider  any               `json:"secret_provider,omitempty"  jsonschema:"a secret provider the Dockhand keeps, by name or number, to load the stack's secrets from; an empty text takes the stack off the one it has"`
		SecretPath      *string           `json:"secret_path,omitempty"      jsonschema:"the path in the stack's secret provider whose every key is loaded as a variable of that name; an empty text takes it out"`
	}
	type gitStackEditOut struct {
		Env      string       `json:"env"                 jsonschema:"the environment (Docker host) the stack is on"`
		Stack    string       `json:"stack"               jsonschema:"the stack's name, as it is now"`
		GitStack *gitStackRow `json:"git_stack,omitempty" jsonschema:"the git stack as Dockhand has it now, read back"`
		Changes  []string     `json:"changes"             jsonschema:"what was saved, in words; empty when everything given is as the stack has it already"`
		DryRun   bool         `json:"dry_run,omitempty"   jsonschema:"nothing was saved: changes says what would be"`
		Note     string       `json:"note,omitempty"`
	}
	add(r, writeTool, &mcp.Tool{
		Name:        "git_stack_edit",
		Description: "Change how Dockhand keeps one git stack: the branch it follows, where its compose file and extra .env file are in the repository, how each deploy is done, its schedule, its webhook, the variables and secrets Dockhand keeps for it, and the secret provider its secrets are loaded from. What is left out is kept. Nothing running changes: the stack goes on as it was until git_stack_deploy brings it up with what was saved. A new branch is looked for in the repository first and one that is not there is refused, where Dockhand left to itself saves any; a schedule is checked to be one its scheduler can read. A new name is given alone, to a stack that has no containers and where no other stack has the name: Dockhand renames one that is running and leaves its containers behind under the old name, and a rename it refuses for the name being taken is half done all the same. The repository itself is not changed here, and a stack cannot be moved to another.",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in gitStackEditIn) (*mcp.CallToolResult, gitStackEditOut, error) {
		rename := strings.TrimSpace(in.Name)
		settings := in.Branch != nil || strings.TrimSpace(in.ComposePath) != "" || in.EnvFile != nil || in.ContextDir != nil || in.Build != nil || in.NoBuildCache != nil || in.Pull != nil || in.Recreate != nil || in.AutoUpdateCron != nil || in.WebhookSecret != nil
		kept := len(in.Variables) > 0 || len(in.RemoveVariables) > 0 || len(in.Secrets) > 0 || len(in.RemoveSecrets) > 0 || in.SecretPath != nil
		switch {
		case rename == "" && !settings && !kept && in.SecretProvider == nil:
			return nil, gitStackEditOut{}, errors.New("nothing to change was given: name, branch, compose_path, env_file, context_dir, build, no_build_cache, pull, recreate, auto_update_cron, webhook_secret, env_vars, remove_env_vars, secrets, remove_secrets, secret_provider or secret_path")
		case rename != "" && (settings || kept || in.SecretProvider != nil):
			return nil, gitStackEditOut{}, errors.New("name is given alone: a stack is renamed in a call of its own, and changed in another")
		case rename != "" && !stackNamed.MatchString(rename):
			return nil, gitStackEditOut{}, fmt.Errorf("name is %q: a stack's name is lower-case letters, digits, hyphens and underscores, and starts with a letter or a digit", in.Name)
		default:
		}
		if err := variablesNamed(in.Variables); err != nil {
			return nil, gitStackEditOut{}, err
		}
		if err := secretsNamed(in.Secrets); err != nil {
			return nil, gitStackEditOut{}, err
		}
		for _, name := range slices.Concat(in.RemoveVariables, in.RemoveSecrets) {
			_, variable := in.Variables[name]
			if _, secret := in.Secrets[name]; variable || secret {
				return nil, gitStackEditOut{}, fmt.Errorf("%s is both given a value and named to be removed", name)
			}
		}
		for secret := range in.Secrets {
			if _, both := in.Variables[secret]; both {
				return nil, gitStackEditOut{}, fmt.Errorf("%s is given both in env_vars and in secrets", secret)
			}
		}
		cron := ""
		if in.AutoUpdateCron != nil && strings.TrimSpace(*in.AutoUpdateCron) != "" {
			checked, err := cronOf(strings.TrimSpace(*in.AutoUpdateCron))
			if err != nil {
				return nil, gitStackEditOut{}, fmt.Errorf("auto_update_cron: %w", err)
			}
			cron = checked
		}
		env, stack, err := r.findGitStack(ctx, in.gitStackArg, false)
		if err != nil {
			return nil, gitStackEditOut{}, err
		}
		out := gitStackEditOut{Env: env.Name, Stack: stack.StackName, Changes: []string{}, DryRun: in.DryRun}
		does := func(did, would string) string {
			if in.DryRun {
				return would
			}

			return did
		}
		// readBack is the git stack as it stands after a change, with the note that it could not be read when it could not
		readBack := func() {
			got, err := r.dock.GetGitStacksById(ctx, stack.Id)
			if err != nil {
				out.Note = noted(out.Note, "it could not be read back: "+refusal(err).Error())

				return
			}
			now := gitStackOf(got.Model)
			row := gitStackRowOf(&now, env.Name)
			out.GitStack, out.Stack = &row, now.StackName
		}

		if rename != "" {
			if rename == stack.StackName {
				out.Note = "nothing to do: it is named that already"
				readBack()

				return nil, out, nil
			}
			// a name another stack has is refused here, because Dockhand's own refusal comes after it has renamed the git stack and before it has
			// renamed what it keeps for it, which the next rename then takes from the other stack
			on, err := r.stacksOf(ctx, env)
			if err != nil {
				return nil, out, fmt.Errorf("the stacks of %s could not be listed, so nothing was changed: %w", env.Name, err)
			}
			keptAs, err := r.keptAs(ctx, env, rename)
			if err != nil {
				return nil, out, fmt.Errorf("the stacks Dockhand keeps for %s could not be listed, so nothing was changed: %w", env.Name, err)
			}
			if keptAs != keptNowhere || slices.ContainsFunc(on, func(s dockhand.GetStacksResponse) bool { return s.Name == rename }) {
				return nil, out, fmt.Errorf("a stack named %s is already on %s, so nothing was changed", rename, env.Name)
			}
			if i := slices.IndexFunc(on, func(s dockhand.GetStacksResponse) bool { return s.Name == stack.StackName }); i >= 0 && len(on[i].ContainerDetails) > 0 {
				return nil, out, fmt.Errorf("%s has %s: Dockhand would rename the stack and leave what runs behind under the old name, with nothing deploying it. Take the stack down first, rename it, and deploy it again", stack.StackName, several(len(on[i].ContainerDetails), "container"))
			}
			out.Changes = append(out.Changes, fmt.Sprintf("%s it from %s to %s", does("renamed", "would rename"), stack.StackName, rename))
			if in.DryRun {
				readBack()

				return nil, out, nil
			}
			if _, err := r.dock.PutGitStacksById(ctx, stack.Id, dockhand.PutGitStacksByIdRequest{StackName: rename}); err != nil {
				return nil, gitStackEditOut{Env: env.Name, Stack: stack.StackName, Changes: []string{}}, fmt.Errorf("the git stack %s was not renamed: %w", stack.StackName, refusal(err))
			}
			readBack()
			out.Note = noted(out.Note, "the folder Dockhand kept its files in under the old name is left where it was: the next deploy makes one under the new name")

			return nil, out, nil
		}

		// the settings of the git stack itself: what is given and differs is sent, and what is to be taken away is sent apart, as nothing
		request, forget := dockhand.PutGitStacksByIdRequest{}, dockhand.GitStackSettings{}
		if in.Branch != nil {
			branch := strings.TrimSpace(*in.Branch)
			switch branch {
			case stack.Branch:
			case "":
				forget.Branch = true
				out.Changes = append(out.Changes, does("had", "would have")+" it follow its repository's own branch")
			default:
				tried, err := r.dock.PostGitBranches(ctx, dockhand.PostGitBranchesRequest{RepositoryId: stack.RepositoryId})
				if err != nil {
					return nil, out, fmt.Errorf("the branches of its repository could not be listed, so nothing was changed: %w", refusal(err))
				}
				var names []string
				for _, b := range tried.Model.Branches {
					names = append(names, b.Name)
				}
				if !slices.Contains(names, branch) {
					return nil, out, fmt.Errorf("its repository has no branch %s, so nothing was changed: it has %s", branch, cmp.Or(strings.Join(names, ", "), "none"))
				}
				request.Branch = branch
				out.Changes = append(out.Changes, fmt.Sprintf("%s the branch it follows to %s", does("changed", "would change"), branch))
			}
		}
		if path := strings.TrimSpace(in.ComposePath); path != "" && path != stack.ComposePath {
			request.ComposePath = path
			out.Changes = append(out.Changes, fmt.Sprintf("%s its compose file's path from %s to %s", does("changed", "would change"), stack.ComposePath, path))
		}
		for _, optional := range []struct {
			given  *string
			was    string
			set    *string
			forget *bool
			what   string
			none   string
		}{{in.EnvFile, stack.EnvFilePath, &request.EnvFilePath, &forget.EnvFilePath, "its extra .env file", "it read no extra .env file"}, {in.ContextDir, stack.ContextDir, &request.ContextDir, &forget.ContextDir, "the folder copied beside its compose file", "the folder copied be the compose file's own"}} {
			if optional.given == nil || strings.TrimSpace(*optional.given) == optional.was {
				continue
			}
			if strings.TrimSpace(*optional.given) == "" {
				*optional.forget = true
				out.Changes = append(out.Changes, does("had ", "would have ")+optional.none)

				continue
			}
			*optional.set = strings.TrimSpace(*optional.given)
			out.Changes = append(out.Changes, fmt.Sprintf("%s %s to %s", does("changed", "would change"), optional.what, *optional.set))
		}
		for _, flag := range []struct {
			given *bool
			was   *bool
			set   **bool
			on    string
			off   string
		}{{in.Build, stack.BuildOnDeploy, &request.BuildOnDeploy, "each deploy build its images", "no deploy build its images"}, {in.NoBuildCache, stack.NoBuildCache, &request.NoBuildCache, "its builds start from nothing", "its builds use what was built before"}, {in.Pull, stack.RepullImages, &request.RepullImages, "each deploy pull every image first", "no deploy pull an image the host has"}, {in.Recreate, stack.ForceRedeploy, &request.ForceRedeploy, "each deploy make every container again, changed or not", "a deploy leave alone what did not change"}} {
			if flag.given == nil || *flag.given == yes(flag.was) {
				continue
			}
			*flag.set = flag.given
			out.Changes = append(out.Changes, does("had ", "would have ")+map[bool]string{true: flag.on, false: flag.off}[*flag.given])
		}
		if in.AutoUpdateCron != nil {
			switch {
			case cron == "" && yes(stack.AutoUpdate):
				request.AutoUpdate = new(false)
				out.Changes = append(out.Changes, does("had", "would have")+" Dockhand no longer fetch it by itself")
			case cron != "" && (!yes(stack.AutoUpdate) || cron != stack.AutoUpdateCron):
				request.AutoUpdate, request.AutoUpdateSchedule, request.AutoUpdateCron = new(true), customSchedule, cron
				out.Changes = append(out.Changes, fmt.Sprintf("%s Dockhand fetch it by itself at %s", does("had", "would have"), cron))
			default:
			}
		}
		if in.WebhookSecret != nil {
			hook := strings.TrimSpace(*in.WebhookSecret)
			switch {
			case hook == "" && (yes(stack.WebhookEnabled) || stack.WebhookSecret != ""):
				forget.Webhook = true
				out.Changes = append(out.Changes, does("switched", "would switch")+" its webhook off, with its secret forgotten")
			case hook != "" && (!yes(stack.WebhookEnabled) || hook != stack.WebhookSecret):
				request.WebhookEnabled, request.WebhookSecret = new(true), hook
				out.Changes = append(out.Changes, map[bool]string{true: does("gave", "would give") + " its webhook another secret", false: does("switched", "would switch") + " its webhook on"}[yes(stack.WebhookEnabled)])
			default:
			}
		}

		// the variables Dockhand keeps for it, as they are to be: Dockhand keeps the ones it is sent and drops the rest, and a secret sent back as it
		// is shown, hidden, keeps the value it had. The path its secrets are loaded from is one of them
		var variables []dockhand.PutStacksByNameEnvRequestVariables
		var provider *dockhand.GetSecretProvidersResponse
		rebind, unbind := false, false
		if kept || in.SecretProvider != nil {
			vars, err := r.dock.GetStacksByNameEnv(ctx, stack.StackName, dockhand.GetStacksByNameEnvOperationOptions{Env: &env.ID})
			if err != nil {
				return nil, out, fmt.Errorf("the variables Dockhand keeps for %s could not be read, so nothing was changed: %w", stack.StackName, refusal(err))
			}
			was, secret := map[string]string{}, map[string]bool{}
			for i := range vars.Model.Variables {
				was[vars.Model.Variables[i].Key], secret[vars.Model.Variables[i].Key] = vars.Model.Variables[i].Value, yes(vars.Model.Variables[i].IsSecret)
			}
			for _, gone := range in.RemoveVariables {
				if _, has := was[gone]; !has || secret[gone] || gone == secretPathKey {
					return nil, out, fmt.Errorf("Dockhand keeps no variable named %s for %s to remove: git_stack_get lists the ones it keeps", gone, stack.StackName) //nolint:staticcheck // ST1005: the sentence begins with the server's name
				}
			}
			for _, gone := range in.RemoveSecrets {
				if !secret[gone] {
					return nil, out, fmt.Errorf("%s has no secret named %s to remove: git_stack_get lists the ones it has", stack.StackName, gone)
				}
			}
			if in.SecretProvider != nil || in.SecretPath != nil {
				bound, unread := r.boundTo(ctx, env, stack.StackName)
				if unread != nil {
					return nil, out, fmt.Errorf("how Dockhand keeps %s could not be read, so nothing was changed: %w", stack.StackName, unread)
				}
				wanted, path, refused := r.secretsFor(ctx, in.SecretProvider, in.SecretPath, bound, was[secretPathKey])
				if refused != nil {
					return nil, out, fmt.Errorf("%w, so nothing was changed", refused)
				}
				provider = wanted
				unbind, rebind = provider == nil && bound != 0 && in.SecretProvider != nil, provider != nil && provider.Id != bound
				if in.SecretPath != nil && path != was[secretPathKey] {
					if path == "" {
						out.Changes = append(out.Changes, does("took", "would take")+" out the path its secrets are loaded from")
					} else {
						out.Changes = append(out.Changes, fmt.Sprintf("%s the path its secrets are loaded from to %s", does("set", "would set"), path))
					}
					was[secretPathKey] = path
					if path == "" {
						delete(was, secretPathKey)
					}
					variables = []dockhand.PutStacksByNameEnvRequestVariables{}
				}
			}
			var set, removed, hidden, forgotten []string
			for _, name := range slices.Sorted(maps.Keys(in.Variables)) {
				if value, has := was[name]; !has || secret[name] || value != in.Variables[name] {
					set = append(set, name)
				}
				was[name], secret[name] = in.Variables[name], false
			}
			for _, name := range in.RemoveVariables {
				delete(was, name)
				removed = append(removed, name)
			}
			for _, name := range slices.Sorted(maps.Keys(in.Secrets)) {
				was[name], secret[name] = in.Secrets[name], true
				hidden = append(hidden, name)
			}
			for _, name := range in.RemoveSecrets {
				delete(was, name)
				forgotten = append(forgotten, name)
			}
			for _, change := range []struct {
				names []string
				did   string
				would string
			}{{set, "set the variables ", "would set the variables "}, {removed, "removed the variables ", "would remove the variables "}, {hidden, "saved the secrets ", "would save the secrets "}, {forgotten, "removed the secrets ", "would remove the secrets "}} {
				if len(change.names) > 0 {
					out.Changes = append(out.Changes, does(change.did, change.would)+strings.Join(change.names, ", "))
					variables = []dockhand.PutStacksByNameEnvRequestVariables{}
				}
			}
			if variables != nil {
				for _, name := range slices.Sorted(maps.Keys(was)) {
					value := was[name]
					if _, given := in.Secrets[name]; secret[name] && !given {
						value = masked
					}
					variables = append(variables, dockhand.PutStacksByNameEnvRequestVariables{Key: name, Value: value, IsSecret: new(secret[name])})
				}
			}
			switch {
			case unbind:
				out.Changes = append(out.Changes, does("took", "would take")+" it off its secret provider")
			case rebind:
				out.Changes = append(out.Changes, fmt.Sprintf("%s its secrets loaded from %s", does("had", "would have"), provider.Name))
			default:
			}
		}

		if len(out.Changes) == 0 {
			out.Note = "nothing to do: it is that way already"
			readBack()

			return nil, out, nil
		}
		if in.DryRun {
			readBack()

			return nil, out, nil
		}

		// each step, and what was not saved when one fails: the settings first, since they are refused whole
		if rebind {
			request.SecretProviderId = provider.Id
		}
		forget.SecretProvider = unbind
		if !equalRequests(request, dockhand.PutGitStacksByIdRequest{}) {
			if _, err := r.dock.PutGitStacksById(ctx, stack.Id, request); err != nil {
				return nil, gitStackEditOut{Env: env.Name, Stack: stack.StackName, Changes: []string{}}, fmt.Errorf("the git stack %s was not changed: %w", stack.StackName, refusal(err))
			}
		}
		if forget != (dockhand.GitStackSettings{}) {
			if _, err := r.dock.ForgetGitStackSettings(ctx, stack.Id, forget); err != nil {
				readBack()
				out.Changes = []string{}

				return nil, out, fmt.Errorf("the git stack %s was changed in part: what was to be taken away was not, and git_stack holds how it stands: %w", stack.StackName, refusal(err))
			}
		}
		if variables != nil {
			if _, err := r.dock.PutStacksByNameEnv(ctx, stack.StackName, dockhand.PutStacksByNameEnvRequest{Variables: variables}, dockhand.PutStacksByNameEnvOperationOptions{Env: &env.ID}); err != nil {
				readBack()
				out.Changes = []string{}

				return nil, out, fmt.Errorf("the settings of %s were saved and its variables were not: %w", stack.StackName, refusal(err))
			}
		}
		readBack()
		out.Note = noted(out.Note, "nothing running changed: git_stack_deploy brings it up with what was saved")

		return nil, out, nil
	})

	type gitStackSyncOut struct {
		Env      string   `json:"env"                jsonschema:"the environment (Docker host) the stack is on"`
		Stack    string   `json:"stack"`
		Was      string   `json:"was,omitempty"      jsonschema:"the commit that had been fetched before"`
		Commit   string   `json:"commit"             jsonschema:"the commit fetched now"`
		Deployed string   `json:"deployed,omitempty" jsonschema:"the commit the files it runs from were taken at, which a fetch does not change"`
		Changed  []string `json:"changed"            jsonschema:"the files that differ from what was fetched before; for a first fetch, one line saying it is everything"`
		WouldGo  []string `json:"would_go,omitempty" jsonschema:"the files the next deploy takes out of the stack's folder, for having gone from the repository"`
		Note     string   `json:"note,omitempty"`
	}
	add(r, writeTool, &mcp.Tool{
		Name:        "git_stack_sync",
		Description: "Have Dockhand fetch the newest commit of a git stack's branch into its own copy of the repository, and say what differs from what it had: the files that changed, and the ones a deploy would take away. Nothing running changes and the stack's own files are not touched, so it is how to see what a deploy would bring in before it does. A fetch alone also marks the commit as seen: the stack's schedule and its webhook then find nothing new and do not deploy it, so what was fetched runs only once git_stack_deploy is called.",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in gitStackArg) (*mcp.CallToolResult, gitStackSyncOut, error) {
		env, stack, err := r.findGitStack(ctx, in, false)
		if err != nil {
			return nil, gitStackSyncOut{}, err
		}
		out := gitStackSyncOut{Env: env.Name, Stack: stack.StackName, Was: stack.LastCommit, Changed: []string{}}
		fetched, err := r.dock.PostGitStacksByIdSync(ctx, stack.Id)
		if err != nil {
			return nil, out, fmt.Errorf("%s was not fetched: %w", stack.StackName, refusal(err))
		}
		if !yes(fetched.Model.Success) {
			return nil, out, fmt.Errorf("%s was not fetched: %s", stack.StackName, gitSaid(fetched.Model.Error))
		}
		out.Commit, out.Changed = fetched.Model.Commit, append(out.Changed, fetched.Model.ChangedFiles...)
		if plan := fetched.Model.DeletionPlan; plan != nil {
			for _, gone := range plan.ToDelete {
				out.WouldGo = append(out.WouldGo, gone.Path)
			}
		}
		if got, err := r.dock.GetGitStacksById(ctx, stack.Id); err == nil {
			out.Deployed = deployedCommit(got.Model.SyncedFiles)
		}
		switch out.Deployed {
		case out.Commit:
			out.Note = "it runs from this commit already: there is nothing new to deploy"
		case "":
			out.Note = "it has never been deployed: git_stack_deploy brings it up from what was fetched"
		default:
			out.Note = fmt.Sprintf("it still runs from %s: git_stack_deploy brings it up from what was fetched, which its schedule and its webhook no longer will", out.Deployed)
		}

		return nil, out, nil
	})

	type gitStackDeployIn struct {
		gitStackArg
		waitArg
		dryRunArg

		OnlyIfChanged bool `json:"only_if_changed,omitempty" jsonschema:"deploy only when the newest commit on its branch is not the one it runs from, and answer that nothing was done otherwise"`
	}
	type gitStackDeployOut struct {
		jobState

		Env      string   `json:"env"                jsonschema:"the environment (Docker host) the stack is on"`
		Stack    string   `json:"stack"`
		Was      string   `json:"was"                jsonschema:"how the stack stood before: running, partial, stopped, created, or gone for one never deployed"`
		Is       string   `json:"is,omitempty"       jsonschema:"how it stands after, read back from the host; absent while the deploy is still running"`
		From     string   `json:"from,omitempty"     jsonschema:"the commit its files were taken at before"`
		Commit   string   `json:"commit,omitempty"   jsonschema:"the commit it runs from now; with dry_run, the newest on its branch"`
		Reported []string `json:"reported,omitempty" jsonschema:"the last of what the deploy reported: the files that changed, and what compose printed"`
		Changes  []string `json:"changes"            jsonschema:"what was done, in words; empty when nothing needed doing"`
		DryRun   bool     `json:"dry_run,omitempty"  jsonschema:"nothing was done: changes says what would be"`
		Note     string   `json:"note,omitempty"`
	}
	add(r, writeTool, &mcp.Tool{
		Name:        "git_stack_deploy",
		Description: "Have Dockhand fetch a git stack's repository and bring the stack up from it, as docker compose up does: the compose file and what lies beside it are copied over the stack's own files, files gone from the repository are taken away, containers that are missing are made and ones whose service changed are made again. It is how a commit reaches what runs, and how a change saved with git_stack_edit takes effect. Says the commit it ran from before and runs from now, how the stack stood and stands, and the last of what the deploy reported. A deploy that fails is an error that says why: what was running is left as it was, though the stack's files are by then the new ones. It runs in the background on Dockhand: this waits up to wait seconds for it (50 unless said) and, when it is still going then, answers with running true and the job's id, for job_get to follow.",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in gitStackDeployIn) (*mcp.CallToolResult, gitStackDeployOut, error) {
		patience, err := in.patience()
		if err != nil {
			return nil, gitStackDeployOut{}, err
		}
		env, listed, err := r.findGitStack(ctx, in.gitStackArg, false)
		if err != nil {
			return nil, gitStackDeployOut{}, err
		}
		out := gitStackDeployOut{Env: env.Name, Stack: listed.StackName, Changes: []string{}, DryRun: in.DryRun}
		if out.Was, _, err = r.standing(ctx, env, listed.StackName); err != nil {
			return nil, out, fmt.Errorf("how %s stands could not be read, so nothing was deployed: %w", listed.StackName, err)
		}
		if got, err := r.dock.GetGitStacksById(ctx, listed.Id); err == nil {
			out.From = deployedCommit(got.Model.SyncedFiles)
		}

		if in.DryRun || in.OnlyIfChanged {
			tried, unasked := r.dock.PostGitStacksByIdTest(ctx, listed.Id)
			if unasked != nil {
				return nil, out, fmt.Errorf("the repository of %s could not be asked, so nothing was deployed: %w", listed.StackName, refusal(unasked))
			}
			if !yes(tried.Model.Success) {
				return nil, out, fmt.Errorf("Dockhand could not fetch the repository of %s, so nothing was deployed: %s", listed.StackName, gitSaid(tried.Model.Error)) //nolint:staticcheck // ST1005: the sentence begins with the server's name
			}
			out.Commit = tried.Model.LastCommit
			if in.OnlyIfChanged && out.From == out.Commit {
				out.Is = out.Was
				out.Note = fmt.Sprintf("nothing to do: it runs from %s, which is the newest commit on its branch", out.Commit)

				return nil, out, nil
			}
		}
		how := fmt.Sprintf("%s on %s from it", listed.StackName, env.Name)
		if in.DryRun {
			out.Is = out.Was
			out.Changes = append(out.Changes, fmt.Sprintf("would fetch its repository at the commit %s and bring up %s", out.Commit, how))

			return nil, out, nil
		}

		reported, state, err := r.gitDeploy(ctx, listed.Id, patience)
		out.jobState = state
		if err != nil {
			return nil, out, fmt.Errorf("%s on %s was not deployed: %w; what was running is left as it was", listed.StackName, env.Name, err)
		}
		if state.Running {
			out.Commit = ""
			out.Note = fmt.Sprintf("the deploy was still going after %s: job_get with the job's id says how it ends", seconds(patience))

			return nil, out, nil
		}
		out.Reported, out.Progress = reported, nil
		out.Changes = append(out.Changes, "fetched its repository and brought up "+how)
		if got, err := r.dock.GetGitStacksById(ctx, listed.Id); err == nil {
			out.Commit = deployedCommit(got.Model.SyncedFiles)
		}
		if out.Is, _, err = r.standing(ctx, env, listed.StackName); err != nil {
			out.Is, out.Note = "", "how it stands after could not be read back: "+err.Error()
		} else if out.Is != stackRunning {
			out.Note = fmt.Sprintf("it was deployed and is %s: stack_list says which of its containers are not running", out.Is)
		}

		return nil, out, nil
	})

	type gitStackDeleteIn struct {
		gitStackArg

		Confirm bool `json:"confirm,omitempty" jsonschema:"forget it: without this the answer says what would go and what would stay, and nothing is done"`
	}
	type gitStackDeleteOut struct {
		Env        string   `json:"env"                  jsonschema:"the environment (Docker host) the stack is on"`
		Stack      string   `json:"stack"`
		Containers []string `json:"containers,omitempty" jsonschema:"the stack's containers, which are left as they are"`
		Removed    bool     `json:"removed"              jsonschema:"Dockhand no longer keeps it as a git stack, read back"`
		WouldGo    []string `json:"would_go,omitempty"   jsonschema:"what forgetting it takes away, said before anything is done"`
		Stays      []string `json:"stays,omitempty"      jsonschema:"what is left as it is"`
		Note       string   `json:"note,omitempty"`
	}
	add(r, deleteTool, &mcp.Tool{
		Name:        "git_stack_delete",
		Description: "Have Dockhand forget that a stack is deployed from git, and stop there: its tie to the repository, its schedule, its webhook, Dockhand's copy of the repository and the variables and secrets Dockhand kept for it go, and its containers are left running, with the files they were brought up from left where they lie. The stack is then one Dockhand only found, which it can start and stop and no longer deploy. Without confirm the answer says what would go and what would stay, and nothing is done. To take the stack away altogether, containers and all, remove the stack itself with the stack tools instead, which forgets its git side with it.",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in gitStackDeleteIn) (*mcp.CallToolResult, gitStackDeleteOut, error) {
		env, stack, err := r.findGitStack(ctx, in.gitStackArg, true)
		if err != nil {
			return nil, gitStackDeleteOut{}, err
		}
		out := gitStackDeleteOut{Env: env.Name, Stack: stack.StackName}
		if env.ID != 0 {
			if _, out.Containers, err = r.standing(ctx, env, stack.StackName); err != nil {
				return nil, out, fmt.Errorf("how %s stands could not be read, so nothing was done: %w", stack.StackName, err)
			}
		}
		out.WouldGo = append(out.WouldGo, fmt.Sprintf("the tie of %s to the repository %s, and Dockhand's copy of it", stack.StackName, gitStackRowOf(stack, env.Name).Repository), "the variables and secrets Dockhand keeps for it")
		if yes(stack.AutoUpdate) {
			out.WouldGo = append(out.WouldGo, "its schedule, "+stack.AutoUpdateCron)
		}
		if yes(stack.WebhookEnabled) {
			out.WouldGo = append(out.WouldGo, "its webhook, which a git server may still be calling")
		}
		if len(out.Containers) > 0 {
			out.Stays = append(out.Stays, "its containers, left as they are: "+strings.Join(out.Containers, ", "), "the files they were brought up from, in Dockhand's folder for the stack")
		}
		if !in.Confirm {
			out.Note = "nothing was done: confirm forgets it"

			return nil, out, nil
		}

		if _, err := r.dock.DeleteGitStacksById(ctx, stack.Id); err != nil {
			return nil, out, fmt.Errorf("the git stack %s was not forgotten: %w", stack.StackName, refusal(err))
		}
		after, err := r.gitStacks(ctx)
		switch {
		case err != nil:
			out.Removed, out.WouldGo = true, nil
			out.Note = "it was forgotten, and the git stacks could not be listed afterwards: " + err.Error()
		case slices.ContainsFunc(after, func(s dockhand.GetGitStacksResponse) bool { return s.Id == stack.Id }):
			return nil, out, fmt.Errorf("Dockhand answered that %s was forgotten, and still lists it", stack.StackName) //nolint:staticcheck // ST1005: the sentence begins with the server's name
		default:
			out.Removed, out.WouldGo = true, nil
			if len(out.Containers) > 0 {
				out.Note = "its containers are left as they were, as a stack Dockhand only found: " + strings.Join(out.Containers, ", ")
			}
		}

		return nil, out, nil
	})
}

// equalRequests reports whether two changes to a git stack say the same, which for one against an empty one is whether it says anything.
func equalRequests(a, b dockhand.PutGitStacksByIdRequest) bool {
	one, _ := json.Marshal(a)
	other, _ := json.Marshal(b)

	return bytes.Equal(one, other)
}
