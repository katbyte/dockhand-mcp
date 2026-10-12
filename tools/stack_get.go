package tools

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"io"
	"slices"
	"strings"

	"github.com/katbyte/dockhand-mcp/sdk/dockhand"
	"github.com/katbyte/pandorest/client"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// How Dockhand comes to have a stack. Internal is its own word for a stack it has the compose file of, as git is for one it deploys from a
// repository, and external for one it keeps a record of with no file, which a stack it only found becomes once it is given an icon. Found is this
// tool's word, for a stack Dockhand has no record of and only saw running on the host.
const (
	keptInternal = "internal"
	keptGit      = "git"
	keptRecorded = "external"
	keptNowhere  = "found"
)

// fileless is whether Dockhand has no compose file for a stack kept that way: one it only found, with a record of it or without.
func fileless(kept string) bool { return kept == keptNowhere || kept == keptRecorded }

const (
	// defaultDeploys is how many of a stack's deploys stack_deploys lists unless told
	defaultDeploys = 10
	// defaultLogTail is how many lines of a deploy's log stack_deploys gives unless told
	defaultLogTail = 100
	// maxDeployLog is the most of a deploy's log that is read, in bytes
	maxDeployLog = 4 << 20
	// placeholderStack names the compose text that is checked when it is no stack's: Dockhand wants a name in the address and makes nothing of it
	placeholderStack = "unsaved"
)

// What Dockhand's checks call a finding that keeps a compose file from being used, and one that is likely a mistake or a risk. Anything else is worth
// knowing and no more.
const (
	findingError   = "error"
	findingWarning = "warn"
)

// seriousness orders findings: what keeps the file from being used, then what is likely wrong, then the rest.
func seriousness(severity string) int {
	switch severity {
	case findingError:
		return 0
	case findingWarning:
		return 1
	default:
		return 2
	}
}

// orNone is a list that is written as empty rather than left out when there is nothing in it.
func orNone(list []string) []string {
	if list == nil {
		return []string{}
	}

	return list
}

// stackVariable is one of the variables a stack's compose file is filled in with.
type stackVariable struct {
	Name   string `json:"name"`
	Value  string `json:"value"            jsonschema:"the variable's value; *** for one that is hidden"`
	Secret bool   `json:"secret,omitempty" jsonschema:"Dockhand keeps this one as a secret, apart from the stack's .env file, and never gives its value back"`
}

// stackDeploy is one time Dockhand brought a stack up.
type stackDeploy struct {
	Run        int      `json:"run"                  jsonschema:"the deploy's number: stack_deploys with it gives what compose printed"`
	When       string   `json:"when"                 jsonschema:"when it was asked for, in UTC"`
	By         string   `json:"by,omitempty"         jsonschema:"what asked for it: manual for a person or a tool, or the schedule, webhook or git change that did"`
	Status     string   `json:"status"               jsonschema:"success, failed, or running for one still under way"`
	Seconds    float64  `json:"seconds,omitempty"    jsonschema:"how long it took"`
	Error      string   `json:"error,omitempty"      jsonschema:"why it failed, when it did: the last thing compose said, the whole of which is the deploy's log"`
	Pulled     bool     `json:"pulled,omitempty"     jsonschema:"it was told to pull the images first"`
	Built      bool     `json:"built,omitempty"      jsonschema:"it was told to build the images first"`
	Recreated  bool     `json:"recreated,omitempty"  jsonschema:"it was told to make every container again whether or not anything had changed"`
	Containers []string `json:"containers,omitempty" jsonschema:"the containers it made or started, when Dockhand recorded them"`
}

// lastLine is the last line of what compose printed that says anything: where it says why it stopped, after every step it took on the way.
func lastLine(printed string) string {
	lines := strings.Split(strings.TrimSpace(printed), "\n")

	return plainly(lines[len(lines)-1])
}

// deployOf is a deploy as the tools give one.
func deployOf(run *dockhand.GetStacksByNameDeploysAnswerRuns) stackDeploy {
	out := stackDeploy{Run: run.Id, When: dockerTime(when(run.TriggeredAt)), By: run.TriggeredBy, Status: string(run.Status), Seconds: float64(run.Duration) / 1000, Error: lastLine(run.ErrorMessage)}
	if details := run.Details; details != nil {
		if o := details.Options; o != nil {
			out.Pulled, out.Built, out.Recreated = o.Pull, o.Build, o.ForceRecreate
		}
		if s := details.Summary; s != nil {
			out.Containers = s.ContainerNames
		}
	}

	return out
}

// deploysOf is every deploy Dockhand has a record of for a stack, the newest first.
func (r *registry) deploysOf(ctx context.Context, env environment, stack string) ([]dockhand.GetStacksByNameDeploysAnswerRuns, error) {
	listed, err := r.dock.GetStacksByNameDeploys(ctx, stack, dockhand.GetStacksByNameDeploysOptions{Env: env.ID})
	if err != nil {
		return nil, refusal(err)
	}
	runs := listed.Answer.Runs
	slices.SortFunc(runs, func(a, b dockhand.GetStacksByNameDeploysAnswerRuns) int { return cmp.Compare(b.Id, a.Id) })

	return runs, nil
}

// recordOf is how Dockhand keeps a stack: its record of where the stack's files are, or that it has none and only found the stack running, with the
// number of the secret provider the stack's secrets are loaded from, 0 for none.
func (r *registry) recordOf(ctx context.Context, env environment, stack string) (kept string, provider int, err error) {
	sources, err := r.dock.GetStacksSources(ctx, dockhand.GetStacksSourcesOptions{Env: env.ID})
	if err != nil {
		return "", 0, refusal(err)
	}
	source, has := sources.Answer[stack]
	if !has {
		return keptNowhere, 0, nil
	}

	return string(cmp.Or(source.SourceType, keptInternal)), int(source.SecretProviderId), nil
}

// keptAs is the first half of recordOf: how Dockhand keeps a stack.
func (r *registry) keptAs(ctx context.Context, env environment, stack string) (kept string, err error) {
	kept, _, err = r.recordOf(ctx, env, stack)

	return kept, err
}

// secretPathIn is the path a .env file names for its stack's secrets to be loaded from, or nothing for a file that names none.
func secretPathIn(envFile string) string {
	for line := range strings.SplitSeq(envFile, "\n") {
		if value, is := strings.CutPrefix(strings.TrimSpace(line), secretPathKey+"="); is {
			return strings.Trim(strings.TrimSpace(value), `"'`)
		}
	}

	return ""
}

// withSecretPath is the text of a .env file with the path its stack's secrets are loaded from set to the one given, and taken out for none; the rest
// of the file is as it was.
func withSecretPath(envFile, path string) string {
	var lines []string
	for line := range strings.SplitSeq(strings.TrimRight(envFile, "\n"), "\n") {
		if !strings.HasPrefix(strings.TrimSpace(line), secretPathKey+"=") && (line != "" || len(lines) > 0) {
			lines = append(lines, line)
		}
	}
	if path != "" {
		lines = append(lines, secretPathKey+"="+path)
	}
	if len(lines) == 0 {
		return ""
	}

	return strings.Join(lines, "\n") + "\n"
}

// keysGivenBy is the names of the variables a secret provider, by its name, gives a stack whose secrets are loaded from a path in it.
func (r *registry) keysGivenBy(ctx context.Context, provider, path string) ([]string, error) {
	listed, err := r.secretProviders(ctx)
	if err != nil {
		return nil, err
	}
	i := slices.IndexFunc(listed, func(p dockhand.GetSecretProvidersAnswer) bool { return p.Name == provider })
	if i < 0 {
		return nil, fmt.Errorf("the Dockhand keeps no secret provider named %s", provider)
	}

	return r.secretKeysAt(ctx, &listed[i], path)
}

// boundTo is the other half of recordOf: the number of the secret provider a stack's secrets are loaded from, 0 for a stack that has none.
func (r *registry) boundTo(ctx context.Context, env environment, stack string) (int, error) {
	_, provider, err := r.recordOf(ctx, env, stack)

	return provider, err
}

// composeOf is a stack's compose file as Dockhand keeps it, and false for a stack whose file Dockhand has not been shown.
func (r *registry) composeOf(ctx context.Context, env environment, stack string) (*dockhand.GetStacksByNameComposeAnswer, bool, error) {
	file, err := r.dock.GetStacksByNameCompose(ctx, stack, dockhand.GetStacksByNameComposeOptions{Env: env.ID})
	switch {
	case client.IsNotFound(err):
		return nil, false, nil
	case err != nil:
		return nil, false, refusal(err)
	default:
		return file.Answer, true, nil
	}
}

//nolint:gocyclo // three tools registered in one function, each a handler of its own: the count is theirs added up
func registerStackGetTools(r *registry) {
	type stackGetIn struct {
		stackArg
		revealArg
	}
	type stackGetOut struct {
		stackRow

		Kept           string          `json:"kept"                      jsonschema:"how Dockhand comes to have the stack: internal when it has the stack's compose file, in its own data directory or adopted where it lay, git when it deploys the stack from a repository, found when it has no file for it at all and only saw it running on the host, and external when it has no file for it either and keeps a record of it, as it does once such a stack is given an icon"`
		Deployable     bool            `json:"deployable"                jsonschema:"whether Dockhand has the stack's compose file and so can bring it up again from it; one it only found can be started, stopped and restarted and nothing more"`
		ComposePath    string          `json:"compose_path,omitempty"    jsonschema:"where Dockhand keeps the compose file, as its own container sees the path"`
		EnvPath        string          `json:"env_path,omitempty"        jsonschema:"where Dockhand keeps the stack's .env file, when it has one"`
		Compose        string          `json:"compose,omitempty"         jsonschema:"the compose file's text, as Dockhand has it"`
		Variables      []stackVariable `json:"variables,omitempty"       jsonschema:"the variables the compose file is filled in with: the ones in the stack's .env file, and the ones Dockhand keeps as secrets"`
		SecretProvider string          `json:"secret_provider,omitempty" jsonschema:"the secret provider Dockhand loads the stack's secrets from each time it deploys it, by name; absent for a stack that has none"`
		SecretPath     string          `json:"secret_path,omitempty"     jsonschema:"the path in that provider whose every key is loaded as a variable of that name: what the stack's .env file has as DOCKHAND_SECRET_SELECTOR"`
		SecretKeys     []string        `json:"secret_keys,omitempty"     jsonschema:"the names of the variables a secret provider gave the stack the last time Dockhand deployed it; their values are never given"`
		WorkingDir     string          `json:"working_dir,omitempty"     jsonschema:"for a stack Dockhand only found: the directory on the host it was brought up from, as its containers' labels record it"`
		ConfigFiles    []string        `json:"config_files,omitempty"    jsonschema:"for a stack Dockhand only found: the compose files on the host it was brought up from, as its containers' labels record them"`
		LastDeploy     *stackDeploy    `json:"last_deploy,omitempty"     jsonschema:"the last time Dockhand brought the stack up; absent when it never has"`
		Hidden         int             `json:"hidden,omitempty"          jsonschema:"how many secrets in the compose file and the variables are hidden"`
		Note           string          `json:"note,omitempty"`
	}
	add(r, readTool, &mcp.Tool{
		Name:        "stack_get",
		Description: "Describe one compose stack in full: its containers by service as stack_list gives them, how Dockhand comes to have it, whether Dockhand can bring it up again, its compose file's text and where that is kept, the variables the file is filled in with, and the last time Dockhand deployed it. A stack Dockhand only found running on a host, brought up there by someone with docker compose, has no file Dockhand can read: for that one the answer gives the directory and compose files on the host that its containers' labels name, which is where to look. Passwords, tokens and keys in the compose file and among the variables are hidden unless reveal_secrets asks for them; the ones Dockhand keeps as secrets it never gives back.",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in stackGetIn) (*mcp.CallToolResult, stackGetOut, error) {
		env, stack, err := r.findStack(ctx, in.stackArg)
		if err != nil {
			return nil, stackGetOut{}, err
		}
		out := stackGetOut{stackRow: stackRowOf(env.Name, &stack), Kept: keptNowhere}

		// each of what follows describes a stack that is known to be there, and one failing is said rather than hiding the rest
		if kept, err := r.keptAs(ctx, env, stack.Name); err != nil {
			out.Note = "how Dockhand keeps it could not be read: " + err.Error()
		} else {
			out.Kept = kept
		}

		file, has, err := r.composeOf(ctx, env, stack.Name)
		switch {
		case err != nil:
			out.Note = noted(out.Note, "its compose file could not be read: "+err.Error())
		case has:
			out.Deployable, out.ComposePath, out.EnvPath, out.Compose = true, file.ComposePath, file.EnvPath, file.Content
			if !in.RevealSecrets {
				out.Compose, out.Hidden = maskText(file.Content)
			}
		default:
			// what the containers' own labels say of where the stack was brought up from
			if hints, err := r.dock.GetStacksPathHints(ctx, dockhand.GetStacksPathHintsOptions{Name: stack.Name, Env: env.ID}); err == nil {
				out.WorkingDir, out.ConfigFiles = hints.Answer.WorkingDir, hints.Answer.ConfigFiles
			}
			if out.Kept == keptGit {
				out.Note = noted(out.Note, "Dockhand deploys this stack from a git repository and has no copy of its compose file: it was never fetched, or the git stack it was is gone and only this record is left. The git tools (--toolsets git) say which")
			} else {
				out.Note = noted(out.Note, "Dockhand has no compose file for this stack: it was brought up on the host itself, so Dockhand can start, stop and restart its containers and cannot deploy it or show its file")
			}
		}

		if has {
			if vars, err := r.dock.GetStacksByNameEnv(ctx, stack.Name, dockhand.GetStacksByNameEnvOptions{Env: env.ID}); err != nil {
				out.Note = noted(out.Note, "its variables could not be read: "+refusal(err).Error())
			} else {
				for i := range vars.Answer.Variables {
					v := stackVariable{Name: vars.Answer.Variables[i].Key, Value: vars.Answer.Variables[i].Value, Secret: vars.Answer.Variables[i].IsSecret}
					// the path a stack's secrets are loaded from has a secret's name and is none
					if !in.RevealSecrets && !v.Secret && v.Name != secretPathKey {
						shown, wasHidden := maskValue(v.Name, v.Value)
						if v.Value = shown; wasHidden {
							out.Hidden++
						}
					}
					out.Variables = append(out.Variables, v)
					if v.Name == secretPathKey {
						out.SecretPath = vars.Answer.Variables[i].Value
					}
				}
				slices.SortFunc(out.Variables, func(a, b stackVariable) int { return cmp.Compare(a.Name, b.Name) })
				out.SecretKeys = vars.Answer.InjectedSecretKeys
				if bound := vars.Answer.SecretProvider; bound != nil {
					out.SecretProvider = bound.Name
				} else if out.SecretPath != "" {
					out.Note = noted(out.Note, "the stack names a path its secrets are loaded from and has no secret provider, so nothing is loaded: the variables a provider gave are empty when it is deployed")
				}
			}
		}

		if runs, err := r.deploysOf(ctx, env, stack.Name); err != nil {
			out.Note = noted(out.Note, "its deploys could not be read: "+err.Error())
		} else if len(runs) > 0 {
			last := deployOf(&runs[0])
			out.LastDeploy = &last
		}

		if out.Hidden > 0 {
			out.Note = noted(out.Note, hiddenNote(out.Hidden, "told by the names beside them"))
		}

		return nil, out, nil
	})

	type stackFinding struct {
		Rule     string `json:"rule"              jsonschema:"the check that found it: LATEST_TAG, PRIVILEGED_CONTAINER, SECRET_IN_ENVIRONMENT, YAML_PARSE_ERROR and the like"`
		Severity string `json:"severity"          jsonschema:"error for what keeps the file from being used at all, warn for what is likely a mistake or a risk, info for what is worth knowing"`
		Service  string `json:"service,omitempty" jsonschema:"the service in the file it is about"`
		Line     int    `json:"line,omitempty"    jsonschema:"the line of the file it is about"`
		Message  string `json:"message"`
		Hint     string `json:"hint,omitempty"    jsonschema:"what to do about it"`
	}
	type stackVariablesCheck struct {
		Required []string `json:"required" jsonschema:"the variables the compose file uses with no value to fall back on"`
		Optional []string `json:"optional" jsonschema:"the variables it uses that have a value to fall back on"`
		Missing  []string `json:"missing"  jsonschema:"the required ones the stack gives no value, which compose fills in with nothing; for text that is no stack's, every required one"`
		Unused   []string `json:"unused"   jsonschema:"the variables the stack gives a value that the file does not use"`
	}
	type stackValidateIn struct {
		Env     any    `json:"env,omitempty"     jsonschema:"the environment (Docker host), by its name or its number as env_list gives them: the one the stack is on when its name is on several, or for compose text alone the host whose containers the names in it are held against"`
		Stack   string `json:"stack,omitempty"   jsonschema:"the stack the file is, by its name as stack_list gives it: its own containers are then no clash, and the file is held against its variables. Alone, the file Dockhand keeps for it is the one checked"`
		Compose string `json:"compose,omitempty" jsonschema:"compose file text to check: a file that is no stack yet, or with stack a change to that stack's file before it is saved"`
	}
	type stackValidateOut struct {
		Env       string              `json:"env,omitempty"   jsonschema:"the environment the file was held against; absent when it was checked against none"`
		Stack     string              `json:"stack,omitempty"`
		Findings  []stackFinding      `json:"findings"        jsonschema:"what the checks found, the most serious first and then by line"`
		Errors    int                 `json:"errors"          jsonschema:"how many findings keep the file from being used"`
		Warnings  int                 `json:"warnings"`
		Info      int                 `json:"info"`
		Usable    bool                `json:"usable"          jsonschema:"whether compose would take the file: no finding is an error"`
		Variables stackVariablesCheck `json:"variables"       jsonschema:"the variables the file uses, held against the ones the stack gives a value"`
		Note      string              `json:"note,omitempty"`
	}
	add(r, readTool, &mcp.Tool{
		Name:        "stack_validate",
		Description: "Check a compose file with Dockhand's own checks before it is trusted: whether it parses and compose would take it, and what in it is likely a mistake or a risk, such as an image with no tag, a container given the whole host, a password written into the file, a container name another container on the host already has, or a service with no restart policy or health check. Each finding names its service and line and says what to do. The variables the file uses are listed too, and which of them the stack gives no value. Give stack to check the file Dockhand keeps for that stack, compose to check text that is no stack yet, or both to check a change to a stack's file before it is saved. Nothing is saved or changed.",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in stackValidateIn) (*mcp.CallToolResult, stackValidateOut, error) {
		name, given := strings.TrimSpace(in.Stack), strings.TrimSpace(in.Compose) != ""
		if name == "" && !given {
			return nil, stackValidateOut{}, errors.New("stack or compose is required: a stack whose compose file Dockhand keeps, or compose file text to check")
		}

		var (
			out   = stackValidateOut{Stack: name, Findings: []stackFinding{}}
			env   dockhand.EnvironmentID
			body  = dockhand.PostStacksByNameValidateRequest{Compose: in.Compose}
			gives = []string{}
		)
		switch {
		case name != "":
			at, stack, err := r.findStack(ctx, stackArg{Env: in.Env, Stack: name})
			if err != nil {
				return nil, out, err
			}
			// the stack's own containers are no clash with the file that makes them
			out.Env, env, body.Existing = at.Name, at.ID, new(true)

			if !given {
				file, has, failed := r.composeOf(ctx, at, stack.Name)
				if failed != nil {
					return nil, out, fmt.Errorf("the compose file of %s could not be read: %w", name, failed)
				}
				if !has {
					return nil, out, fmt.Errorf("Dockhand has no compose file for %s, which was brought up on the host itself: give the file's text as compose as well to have it checked", name) //nolint:staticcheck // ST1005: Dockhand is a name
				}
				body.Compose = file.Content
			}

			// the variables the stack gives a value, the ones in its .env file among them: left to itself, Dockhand's check counts only the ones it
			// keeps itself
			vars, err := r.dock.GetStacksByNameEnv(ctx, stack.Name, dockhand.GetStacksByNameEnvOptions{Env: env})
			if err != nil {
				out.Note = "the stack's variables could not be read, so every variable the file needs is counted as missing: " + refusal(err).Error()
			} else {
				path := ""
				for i := range vars.Answer.Variables {
					// the path the stack's secrets are loaded from is Dockhand's to read, and no variable of the file's
					if vars.Answer.Variables[i].Key == secretPathKey {
						path = vars.Answer.Variables[i].Value

						continue
					}
					gives = append(gives, vars.Answer.Variables[i].Key)
				}
				// and the ones its secret provider gives when it is deployed, which Dockhand's check knows nothing of: asked of the provider now, or
				// failing that the ones the last deploy was given
				if bound := vars.Answer.SecretProvider; bound != nil && path != "" {
					keys, unasked := r.keysGivenBy(ctx, bound.Name, path)
					if unasked != nil {
						keys = vars.Answer.InjectedSecretKeys
						out.Note = fmt.Sprintf("what the stack's secret provider %s gives could not be asked, so the variables its last deploy was given are counted as given: %s", bound.Name, unasked.Error())
					}
					gives = append(gives, keys...)
				}
			}
		case in.Env != nil:
			at, err := r.one(ctx, envArg{Env: in.Env})
			if err != nil {
				return nil, out, err
			}
			out.Env, env = at.Name, at.ID
		default:
		}

		checked, err := r.dock.PostStacksByNameValidate(ctx, cmp.Or(name, placeholderStack), body, dockhand.PostStacksByNameValidateOptions{Env: env})
		if err != nil {
			return nil, out, fmt.Errorf("the compose file could not be checked: %w", refusal(err))
		}
		for i := range checked.Answer.Findings {
			f := &checked.Answer.Findings[i]
			out.Findings = append(out.Findings, stackFinding{Rule: f.RuleId, Severity: string(f.Severity), Service: f.Service, Line: f.Line, Message: f.Message, Hint: f.Hint})
			switch f.Severity {
			case findingError:
				out.Errors++
			case findingWarning:
				out.Warnings++
			default:
				out.Info++
			}
		}
		slices.SortStableFunc(out.Findings, func(a, b stackFinding) int {
			return cmp.Or(cmp.Compare(seriousness(a.Severity), seriousness(b.Severity)), cmp.Compare(a.Line, b.Line))
		})
		out.Usable = out.Errors == 0

		out.Variables = stackVariablesCheck{Required: []string{}, Optional: []string{}, Missing: []string{}, Unused: []string{}}
		if vars, err := r.dock.PostStacksByNameEnvValidate(ctx, cmp.Or(name, placeholderStack), dockhand.PostStacksByNameEnvValidateRequest{Compose: body.Compose, Variables: gives}, dockhand.PostStacksByNameEnvValidateOptions{Env: env}); err != nil {
			out.Note = noted(out.Note, "the variables the file uses could not be listed: "+refusal(err).Error())
		} else {
			out.Variables = stackVariablesCheck{Required: orNone(vars.Answer.Required), Optional: orNone(vars.Answer.Optional), Missing: orNone(vars.Answer.Missing), Unused: orNone(vars.Answer.Unused)}
		}
		if missing := out.Variables.Missing; len(missing) > 0 && name != "" {
			out.Note = noted(out.Note, fmt.Sprintf("%d of the variables the file needs are given no value by the stack, and compose fills them in with nothing: %s", len(missing), strings.Join(missing, ", ")))
		}

		return nil, out, nil
	})

	type stackDeploysIn struct {
		stackArg
		revealArg

		Run   int `json:"run,omitempty"   jsonschema:"one deploy's number, to have what compose printed during it as well"`
		Limit int `json:"limit,omitempty" jsonschema:"the most deploys to list, the newest first: 10 unless given"`
		Tail  int `json:"tail,omitempty"  jsonschema:"with run, how many lines of what was printed to give, from its end: 100 unless given"`
	}
	type stackDeploysOut struct {
		Env     string        `json:"env"`
		Stack   string        `json:"stack"`
		Deploys []stackDeploy `json:"deploys"          jsonschema:"the stack's deploys, the newest first; with run, that one alone"`
		Total   int           `json:"total"            jsonschema:"how many deploys Dockhand has a record of for the stack"`
		Log     []string      `json:"log,omitempty"    jsonschema:"with run: the end of what compose printed during that deploy, a line each"`
		Lines   int           `json:"lines,omitempty"  jsonschema:"with run: how many lines it printed in all"`
		Hidden  int           `json:"hidden,omitempty" jsonschema:"how many secrets in the lines given are hidden"`
		Note    string        `json:"note,omitempty"`
	}
	add(r, readTool, &mcp.Tool{
		Name:        "stack_deploys",
		Description: "List the times Dockhand brought a compose stack up, the newest first: when, what asked for it, whether it succeeded and how long it took, what it was told to do first, and the containers it made or started. Give run, a deploy's number, to have what compose printed during that one as well, which is where a deploy that failed says why. Only Dockhand's own deploys are recorded: a stack brought up on the host by hand has none. Secrets in what was printed are hidden unless reveal_secrets asks for them.",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in stackDeploysIn) (*mcp.CallToolResult, stackDeploysOut, error) {
		if in.Limit < 0 || in.Tail < 0 || in.Run < 0 {
			return nil, stackDeploysOut{}, fmt.Errorf("limit, tail and run are numbers above 0 when they are given: limit is %d, tail %d and run %d", in.Limit, in.Tail, in.Run)
		}
		env, stack, err := r.findStack(ctx, in.stackArg)
		if err != nil {
			return nil, stackDeploysOut{}, err
		}

		out := stackDeploysOut{Env: env.Name, Stack: stack.Name, Deploys: []stackDeploy{}}
		runs, err := r.deploysOf(ctx, env, stack.Name)
		if err != nil {
			return nil, out, fmt.Errorf("the deploys of %s could not be listed: %w", stack.Name, err)
		}
		out.Total = len(runs)

		if in.Run == 0 {
			for i := range runs[:min(len(runs), cmp.Or(in.Limit, defaultDeploys))] {
				out.Deploys = append(out.Deploys, deployOf(&runs[i]))
			}
			switch {
			case out.Total == 0:
				out.Note = "Dockhand has never deployed this stack: one brought up on the host by hand has no deploys recorded"
			case out.Total > len(out.Deploys):
				out.Note = fmt.Sprintf("the newest %d of %d are listed: raise limit for more", len(out.Deploys), out.Total)
			default:
			}

			return nil, out, nil
		}

		at := slices.IndexFunc(runs, func(run dockhand.GetStacksByNameDeploysAnswerRuns) bool { return run.Id == in.Run })
		if at < 0 {
			return nil, out, fmt.Errorf("%s has no deploy numbered %d: stack_deploys without run lists the ones it has", stack.Name, in.Run)
		}
		out.Deploys = append(out.Deploys, deployOf(&runs[at]))

		printed, err := r.dock.GetStacksByNameDeploysByRunIdLog(ctx, stack.Name, in.Run)
		if err != nil {
			out.Note = "what it printed could not be read: " + refusal(err).Error()

			// the deploy is described: that its log could not be read is said in the answer
			return nil, out, nil
		}
		defer func() { _ = printed.HttpResponse.Body.Close() }()
		raw, err := io.ReadAll(io.LimitReader(printed.HttpResponse.Body, maxDeployLog))
		if err != nil {
			out.Note = "what it printed could not be read: " + err.Error()

			return nil, out, nil
		}

		lines := strings.Split(strings.TrimRight(string(raw), "\n"), "\n")
		if len(lines) == 1 && lines[0] == "" {
			lines = nil
		}
		out.Lines = len(lines)
		if tail := cmp.Or(in.Tail, defaultLogTail); len(lines) > tail {
			lines = lines[len(lines)-tail:]
			out.Note = fmt.Sprintf("the last %d of %d lines are given: raise tail for more", tail, out.Lines)
		}
		for _, line := range lines {
			if !in.RevealSecrets {
				var hidden int
				line, hidden = maskText(line)
				out.Hidden += hidden
			}
			out.Log = append(out.Log, strings.TrimRight(line, " \r"))
		}
		if out.Hidden > 0 {
			out.Note = noted(out.Note, hiddenNote(out.Hidden, "told by the names beside them"))
		}

		return nil, out, nil
	})
}
