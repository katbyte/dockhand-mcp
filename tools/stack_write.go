package tools

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"maps"
	"regexp"
	"slices"
	"strings"
	"time"

	"github.com/katbyte/dockhand-mcp/sdk/dockhand"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// stackGone is what the tools say of a stack that is no longer listed on its environment.
const stackGone = "gone"

// printedKept is how many of the last lines compose printed a tool that deployed a stack gives: the rest is the deploy's log.
const printedKept = 10

var (
	// stackNamed is what Dockhand takes for a stack's name
	stackNamed = regexp.MustCompile(`^[a-z0-9][a-z0-9_-]*$`)
	// variableNamed is what a .env file and compose take for a variable's name
	variableNamed = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)
)

// noFile is why a stack Dockhand has no compose file for cannot be deployed, edited or moved: one it only found, or one it deploys from git and has
// not fetched yet.
func (r *registry) noFile(ctx context.Context, env environment, stack string) error {
	if kept, err := r.keptAs(ctx, env, stack); err == nil && kept == keptGit {
		return fmt.Errorf("%s is deployed from a git repository that Dockhand has not fetched yet, so there is no compose file for it here: the git tools (--toolsets git) fetch the repository and deploy it", stack)
	}

	return fmt.Errorf("Dockhand has no compose file for %s, which was brought up on the host itself: it can start, stop and restart that stack's containers and nothing more, until stack_adopt shows it where the file is", stack) //nolint:staticcheck // ST1005: Dockhand is a name
}

// fileGiven gives a stack Dockhand keeps a record of, and has no file for, the compose file at a path: the file is read where it lies and saved back
// there as that stack's, unchanged, which is how Dockhand comes to have it.
func (r *registry) fileGiven(ctx context.Context, env environment, stack, path string) error {
	read, err := r.dock.GetSystemFilesContent(ctx, dockhand.GetSystemFilesContentOperationOptions{Path: path})
	if err != nil {
		return fmt.Errorf("%s was not adopted: the compose file at %s could not be read: %w", stack, path, refusal(err))
	}
	saved, err := r.dock.PutStacksByNameCompose(ctx, stack, dockhand.PutStacksByNameComposeRequest{Content: read.Model.Content, ComposePath: path}, dockhand.PutStacksByNameComposeOperationOptions{Env: &env.ID})
	if err != nil {
		return fmt.Errorf("%s was not adopted: %w", stack, refusal(err))
	}
	if !yes(saved.Model.Success) {
		return fmt.Errorf("%s was not adopted: %w", stack, errNotSaid)
	}

	return nil
}

// standing is how a stack stands on its environment, read back: its status, the names of its containers, and gone for one that is no longer listed.
func (r *registry) standing(ctx context.Context, env environment, stack string) (status string, containers []string, err error) {
	listed, err := r.stacksOf(ctx, env)
	if err != nil {
		return "", nil, err
	}
	for i := range listed {
		if listed[i].Name != stack {
			continue
		}
		for j := range listed[i].ContainerDetails {
			containers = append(containers, listed[i].ContainerDetails[j].Name)
		}
		slices.Sort(containers)

		return listed[i].Status, containers, nil
	}

	return stackGone, nil, nil
}

// newestRun is the number of a stack's newest deploy, and 0 when it has none or they cannot be read.
func (r *registry) newestRun(ctx context.Context, env environment, stack string) int {
	runs, err := r.deploysOf(ctx, env, stack)
	if err != nil || len(runs) == 0 {
		return 0
	}

	return runs[0].Id
}

// deploy brings a stack up from the compose file Dockhand has for it, as a job Dockhand runs, and waits for it as long as the patience given. Still
// running then, the state says so and there is nothing printed yet.
func (r *registry) deploy(ctx context.Context, env environment, stack string, how dockhand.PostStacksByNameDeployRequest, patience time.Duration) (printed []string, state jobState, err error) {
	started, err := r.dock.PostStacksByNameDeploy(ctx, stack, how, dockhand.PostStacksByNameDeployOperationOptions{Accept: dockhand.AcceptJob, Env: &env.ID})
	if err != nil {
		return nil, jobState{}, refusal(err)
	}
	outcome, state, err := settle(ctx, r, started.Model.JobId, started.Model, patience)
	if err != nil || state.Running {
		return nil, state, err
	}
	// a caller made to wait is answered a failure with HTTP 200 all the same
	if !yes(outcome.Success) {
		return nil, state, errors.New(cmp.Or(lastLine(outcome.Error), "Dockhand says it could not, and gives no reason"))
	}

	return lastLines(outcome.Output, printedKept), state, nil
}

// lastLines is the last few lines of what compose printed, each trimmed, with the ones that say the same as the line before them said once.
func lastLines(printed string, keep int) []string {
	var lines []string
	for line := range strings.SplitSeq(printed, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || (len(lines) > 0 && lines[len(lines)-1] == line) {
			continue
		}
		shown, _ := maskText(line)
		lines = append(lines, shown)
	}
	if len(lines) > keep {
		lines = lines[len(lines)-keep:]
	}

	return lines
}

// refusedBy is the findings that keep a compose file from being used, in words, and how many more findings there are that do not.
func refusedBy(findings []dockhand.PostStacksByNameValidateResponseFindings) (errs []string, others int) {
	for i := range findings {
		if findings[i].Severity != findingError {
			others++

			continue
		}
		at := ""
		if findings[i].Line > 0 {
			at = fmt.Sprintf("line %d: ", findings[i].Line)
		}
		errs = append(errs, at+strings.TrimSpace(findings[i].Message))
	}

	return errs, others
}

// secretsNamed checks the names and values a call gives for secrets.
func secretsNamed(secrets map[string]string) error {
	for _, name := range slices.Sorted(maps.Keys(secrets)) {
		switch {
		case !variableNamed.MatchString(name):
			return fmt.Errorf("%q is no name for a secret: a variable's name is letters, digits and underscores, and does not start with a digit", name)
		case secrets[name] == "":
			return fmt.Errorf("the secret %s is given no value", name)
		case secrets[name] == masked:
			return fmt.Errorf("the secret %s is given %s, which is the mark of a value that is hidden and not a value", name, masked)
		default:
		}
	}

	return nil
}

//nolint:gocyclo,maintidx // nine tools registered in one function, each a handler of its own: the count is theirs added up
func registerStackWriteTools(r *registry) {
	type stackDeployIn struct {
		stackArg
		waitArg
		dryRunArg

		Pull     bool `json:"pull,omitempty"     jsonschema:"pull each service's image first, so that a newer image under the same tag is what runs"`
		Build    bool `json:"build,omitempty"    jsonschema:"build the images the file says to build first"`
		Recreate bool `json:"recreate,omitempty" jsonschema:"make every container again, whether or not anything about it changed"`
	}
	type stackDeployOut struct {
		jobState

		Env     string   `json:"env"               jsonschema:"the environment (Docker host) the stack is on"`
		Stack   string   `json:"stack"`
		Was     string   `json:"was"               jsonschema:"how the stack stood before: running, partial, stopped, or created for one with no containers yet"`
		Is      string   `json:"is,omitempty"      jsonschema:"how it stands after, read back from the host; absent while the deploy is still running"`
		Run     int      `json:"run,omitempty"     jsonschema:"the deploy's number: stack_deploys with it gives everything compose printed"`
		Printed []string `json:"printed,omitempty" jsonschema:"the last of what compose printed"`
		Changes []string `json:"changes"           jsonschema:"what was done, in words"`
		DryRun  bool     `json:"dry_run,omitempty" jsonschema:"nothing was done: changes says what would be"`
		Note    string   `json:"note,omitempty"`
	}
	add(r, writeTool, &mcp.Tool{
		Name:        "stack_deploy",
		Description: "Bring one compose stack up from the compose file Dockhand has for it, as docker compose up does: containers that are missing are made, ones whose service changed in the file are made again, and ones nothing changed for are left running. It is how a saved change to the file or its variables takes effect, and with pull how a newer image is picked up. Says how the stack stood before and stands after, the deploy's number and the last of what compose printed. A deploy that fails leaves what was running as it was and is an error that says why. A stack Dockhand only found on a host has no file to deploy from and is refused. A stack deployed from git is brought up from the files last copied from its repository, without fetching it: the git toolset's deploy fetches first. The deploy runs in the background on Dockhand: this waits up to wait seconds for it (50 unless said) and, when it is still going then, answers with running true and the job's id, for job_get to follow.",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in stackDeployIn) (*mcp.CallToolResult, stackDeployOut, error) {
		patience, err := in.patience()
		if err != nil {
			return nil, stackDeployOut{}, err
		}
		env, stack, err := r.findStack(ctx, in.stackArg)
		if err != nil {
			return nil, stackDeployOut{}, err
		}
		out := stackDeployOut{Env: env.Name, Stack: stack.Name, Was: stack.Status, Changes: []string{}, DryRun: in.DryRun}

		file, has, err := r.composeOf(ctx, env, stack.Name)
		if err != nil {
			return nil, out, fmt.Errorf("the compose file of %s could not be read, so nothing was deployed: %w", stack.Name, err)
		}
		if !has {
			return nil, out, r.noFile(ctx, env, stack.Name)
		}

		var first []string
		for _, step := range []struct {
			asked bool
			does  string
		}{{in.Pull, "pulling its images first"}, {in.Build, "building its images first"}, {in.Recreate, "making every container again"}} {
			if step.asked {
				first = append(first, step.does)
			}
		}
		how := fmt.Sprintf("%s on %s from %s", stack.Name, env.Name, file.ComposePath)
		if len(first) > 0 {
			how += ", " + strings.Join(first, " and ")
		}
		if in.DryRun {
			out.Is = stack.Status
			out.Changes = append(out.Changes, "would bring up "+how)

			return nil, out, nil
		}

		before := r.newestRun(ctx, env, stack.Name)
		request := dockhand.PostStacksByNameDeployRequest{}
		if in.Pull {
			request.Pull = new(true)
		}
		if in.Build {
			request.Build = new(true)
		}
		if in.Recreate {
			request.ForceRecreate = new(true)
		}
		printed, state, err := r.deploy(ctx, env, stack.Name, request, patience)
		out.jobState = state
		if err != nil {
			where := ""
			if run := r.newestRun(ctx, env, stack.Name); run > before {
				where = fmt.Sprintf("; stack_deploys with run %d gives everything compose printed", run)
			}

			// a stack whose secrets cannot be loaded is not deployed, and Dockhand says only that it failed
			if vars, unread := r.dock.GetStacksByNameEnv(ctx, stack.Name, dockhand.GetStacksByNameEnvOperationOptions{Env: &env.ID}); unread == nil && vars.Model.SecretProvider != nil {
				where += fmt.Sprintf("; its secrets are loaded from the secret provider %s, and a stack is not deployed while its provider cannot be reached or has nothing at the path the stack names", vars.Model.SecretProvider.Name)
			}

			return nil, out, fmt.Errorf("%s on %s was not deployed: %w; what was running is left as it was%s", stack.Name, env.Name, err, where)
		}
		if state.Running {
			out.Note = fmt.Sprintf("the deploy was still going after %s: job_get with the job's id says how it ends", seconds(patience))

			return nil, out, nil
		}

		// what the job reported as it went is the same lines as what compose printed
		out.Printed, out.Progress = printed, nil
		out.Changes = append(out.Changes, "brought up "+how)
		if run := r.newestRun(ctx, env, stack.Name); run > before {
			out.Run = run
		}
		if out.Is, _, err = r.standing(ctx, env, stack.Name); err != nil {
			out.Is, out.Note = "", "how it stands after could not be read back: "+err.Error()
		} else if out.Is != stackRunning {
			out.Note = fmt.Sprintf("it was deployed and is %s: stack_get says which of its containers are not running", out.Is)
		}

		return nil, out, nil
	})

	type stackDownIn struct {
		stackArg
		waitArg
		dryRunArg

		RemoveVolumes bool `json:"remove_volumes,omitempty" jsonschema:"remove the stack's named volumes as well, and everything stored in them, which cannot be got back. Refused unless the server was started with --enable-delete"`
	}
	type stackDownOut struct {
		jobState

		Env     string   `json:"env"               jsonschema:"the environment (Docker host) the stack is on"`
		Stack   string   `json:"stack"`
		Was     string   `json:"was"               jsonschema:"how the stack stood before"`
		Is      string   `json:"is,omitempty"      jsonschema:"how it stands after, read back: created for a stack Dockhand still has the file of and no containers for; absent while it is still being taken down"`
		Removed []string `json:"removed"           jsonschema:"the containers that were removed, read back as gone; with dry_run, the ones that would be"`
		Volumes []string `json:"volumes,omitempty" jsonschema:"with remove_volumes: the named volumes that were removed, read back as gone; with dry_run, the ones that would be"`
		DryRun  bool     `json:"dry_run,omitempty" jsonschema:"nothing was done: removed and volumes say what would go"`
		Note    string   `json:"note,omitempty"`
	}
	add(r, writeTool, &mcp.Tool{
		Name:        "stack_down",
		Description: "Take one compose stack down, as docker compose down does: its containers are stopped and removed and so are the networks it made. Dockhand keeps the stack's compose file and variables, and stack_deploy brings it up again from them; what its containers held outside a volume is gone. Its named volumes are kept unless remove_volumes is given, which deletes what is stored in them and needs the server's --enable-delete. A stack Dockhand only found on a host has no file to take it down by: for that one Dockhand stops the containers and removes nothing. dry_run says what would go. It runs in the background on Dockhand: this waits up to wait seconds for it and, when it is still going then, answers with running true and the job's id for job_get.",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in stackDownIn) (*mcp.CallToolResult, stackDownOut, error) {
		patience, err := in.patience()
		if err != nil {
			return nil, stackDownOut{}, err
		}
		if in.RemoveVolumes && !r.opts.EnableDelete {
			return nil, stackDownOut{}, errors.New("remove_volumes deletes what is stored in the stack's volumes, and this server was started without --enable-delete: without it the stack can be taken down with its volumes kept")
		}
		env, stack, err := r.findStack(ctx, in.stackArg)
		if err != nil {
			return nil, stackDownOut{}, err
		}
		out := stackDownOut{Env: env.Name, Stack: stack.Name, Was: stack.Status, Removed: []string{}, DryRun: in.DryRun}

		_, has, err := r.composeOf(ctx, env, stack.Name)
		if err != nil {
			return nil, out, fmt.Errorf("the compose file of %s could not be read, so nothing was taken down: %w", stack.Name, err)
		}
		if !has && in.RemoveVolumes {
			return nil, out, fmt.Errorf("Dockhand has no compose file for %s, which was brought up on the host itself, and all it does for such a stack is stop its containers: it removes none of its volumes", stack.Name) //nolint:staticcheck // ST1005: Dockhand is a name
		}

		var named []string
		if in.RemoveVolumes {
			preview, failed := r.dock.GetStacksByNameDeletePreview(ctx, stack.Name, dockhand.GetStacksByNameDeletePreviewOperationOptions{Env: &env.ID})
			if failed != nil {
				return nil, out, fmt.Errorf("the volumes of %s could not be listed, so nothing was taken down: %w", stack.Name, refusal(failed))
			}
			named = preview.Model.NamedVolumes
		}
		_, there, err := r.standing(ctx, env, stack.Name)
		if err != nil {
			return nil, out, fmt.Errorf("the containers of %s could not be listed, so nothing was taken down: %w", stack.Name, err)
		}

		const onlyStops = "Dockhand has no compose file for this stack, which was brought up on the host itself, so all it does is stop the stack's containers: nothing is removed"
		if in.DryRun {
			out.Is = stack.Status
			if has {
				out.Removed, out.Volumes = append(out.Removed, there...), named
			} else {
				out.Note = onlyStops
			}

			return nil, out, nil
		}

		request := dockhand.PostStacksByNameDownRequest{}
		if in.RemoveVolumes {
			request.RemoveVolumes = new(true)
		}
		started, err := r.dock.PostStacksByNameDown(ctx, stack.Name, request, dockhand.PostStacksByNameDownOperationOptions{Accept: dockhand.AcceptJob, Env: &env.ID})
		if err != nil {
			return nil, out, fmt.Errorf("%s on %s was not taken down: %w", stack.Name, env.Name, refusal(err))
		}
		outcome, state, err := settle(ctx, r, started.Model.JobId, started.Model, patience)
		out.jobState = state
		if err != nil {
			return nil, out, fmt.Errorf("%s on %s was not taken down: %w", stack.Name, env.Name, err)
		}
		if state.Running {
			out.Note = fmt.Sprintf("it was still being taken down after %s: job_get with the job's id says how it ends", seconds(patience))

			return nil, out, nil
		}
		if !yes(outcome.Success) {
			return nil, out, fmt.Errorf("%s on %s was not taken down: Dockhand says %s", stack.Name, env.Name, cmp.Or(lastLine(outcome.Error), "it could not, and gives no reason"))
		}

		is, left, err := r.standing(ctx, env, stack.Name)
		if err != nil {
			out.Note = "how it stands after could not be read back: " + err.Error()

			return nil, out, nil //nolint:nilerr // the stack was taken down: that it could not be read back is said in the answer
		}
		out.Is = is
		for _, name := range there {
			if !slices.Contains(left, name) {
				out.Removed = append(out.Removed, name)
			}
		}
		switch {
		case !has:
			out.Note = onlyStops
		case len(left) > 0:
			out.Note = fmt.Sprintf("%d of its containers are still there: %s", len(left), strings.Join(left, ", "))
		default:
		}

		if len(named) > 0 {
			volumes, err := r.volumesOf(ctx, env)
			if err != nil {
				out.Note = noted(out.Note, "whether its volumes are gone could not be read back: "+err.Error())

				return nil, out, nil //nolint:nilerr // as above
			}
			var kept []string
			for _, name := range named {
				if slices.ContainsFunc(volumes, func(v dockhand.GetVolumesResponse) bool { return v.Name == name }) {
					kept = append(kept, name)
				} else {
					out.Volumes = append(out.Volumes, name)
				}
			}
			if len(kept) > 0 {
				out.Note = noted(out.Note, fmt.Sprintf("%d of its volumes are still there, which something else may be using: %s", len(kept), strings.Join(kept, ", ")))
			}
		}

		return nil, out, nil
	})

	type stackCreateIn struct {
		envArg
		waitArg
		dryRunArg

		Name           string            `json:"name"                      jsonschema:"the stack's name, which is its compose project: lower-case letters, digits, hyphens and underscores, starting with a letter or a digit"`
		Compose        string            `json:"compose"                   jsonschema:"the compose file's text, whole"`
		EnvFile        string            `json:"env_file,omitempty"        jsonschema:"the text of a .env file to keep beside it, NAME=value a line: the variables the compose file is filled in with that are no secret"`
		Secrets        map[string]string `json:"secrets,omitempty"         jsonschema:"variables Dockhand is to keep as secrets, each name with its value: they fill in the compose file as the others do, are kept apart from the .env file, and are never given back"`
		SecretProvider any               `json:"secret_provider,omitempty" jsonschema:"a secret provider the Dockhand keeps, by name or number, to load the stack's secrets from each time it is deployed, in place of keeping them itself"`
		SecretPath     string            `json:"secret_path,omitempty"     jsonschema:"with secret_provider: the path in it whose every key is loaded as a variable of that name, for the compose file to be filled in with. For a Vault, a path under its mount, as prod/db"`
		Start          *bool             `json:"start,omitempty"           jsonschema:"bring the stack up once it is made: true unless said. False makes it and leaves it with no containers"`
		Pull           bool              `json:"pull,omitempty"            jsonschema:"when bringing it up, pull each service's image first even when the host has one under that tag"`
	}
	type stackCreateOut struct {
		jobState

		Env         string   `json:"env"                    jsonschema:"the environment (Docker host) the stack was made on"`
		Stack       string   `json:"stack"`
		ComposePath string   `json:"compose_path,omitempty" jsonschema:"where Dockhand keeps the compose file, as its own container sees the path; with dry_run, where it would"`
		Status      string   `json:"status,omitempty"       jsonschema:"how the stack stands, read back: running once brought up, created for one made with no containers; absent while it is still being brought up"`
		Run         int      `json:"run,omitempty"          jsonschema:"the number of the deploy that brought it up: stack_deploys with it gives everything compose printed"`
		Printed     []string `json:"printed,omitempty"      jsonschema:"the last of what compose printed bringing it up"`
		Changes     []string `json:"changes"                jsonschema:"what was done, in words"`
		DryRun      bool     `json:"dry_run,omitempty"      jsonschema:"nothing was done: changes says what would be"`
		Note        string   `json:"note,omitempty"`
	}
	add(r, writeTool, &mcp.Tool{
		Name:        "stack_create",
		Description: "Make a new compose stack on one Docker host (environment) from the text of a compose file, and bring it up unless start is false. Dockhand keeps the file, and the .env file and secrets given with it, in its own data directory, and deploys the stack from there from then on. It names one environment. The file is checked first, as stack_validate checks it, and one compose would not take is refused with what is wrong; so is a name a stack on that host already has, which Dockhand left to itself would take over without a word. Bringing it up runs in the background on Dockhand: this waits up to wait seconds for it and, when it is still going then, answers with running true and the job's id for job_get. A stack that was made and could not be brought up is kept, with no containers, and the error says so.",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in stackCreateIn) (*mcp.CallToolResult, stackCreateOut, error) {
		name := strings.TrimSpace(in.Name)
		switch {
		case !stackNamed.MatchString(name):
			return nil, stackCreateOut{}, fmt.Errorf("name is %q: a stack's name is lower-case letters, digits, hyphens and underscores, and starts with a letter or a digit", in.Name)
		case strings.TrimSpace(in.Compose) == "":
			return nil, stackCreateOut{}, errors.New("compose is required: the compose file's text")
		default:
		}
		if err := secretsNamed(in.Secrets); err != nil {
			return nil, stackCreateOut{}, err
		}
		patience, err := in.patience()
		if err != nil {
			return nil, stackCreateOut{}, err
		}
		env, err := r.one(ctx, in.envArg)
		if err != nil {
			return nil, stackCreateOut{}, err
		}
		out := stackCreateOut{Env: env.Name, Stack: name, Changes: []string{}, DryRun: in.DryRun}

		// Dockhand makes a stack under a name that is taken without a word: the file it has for that name is replaced, and a stack it only found
		// becomes one it deploys from the new file
		is, _, err := r.standing(ctx, env, name)
		if err != nil {
			return nil, out, fmt.Errorf("the stacks of %s could not be listed, so nothing was made: %w", env.Name, err)
		}
		provider, path, err := r.secretsFor(ctx, in.SecretProvider, &in.SecretPath, 0, "")
		if err != nil {
			return nil, out, fmt.Errorf("%w, so nothing was made", err)
		}
		kept, err := r.keptAs(ctx, env, name)
		if err != nil {
			return nil, out, fmt.Errorf("the stacks Dockhand keeps for %s could not be listed, so nothing was made: %w", env.Name, err)
		}
		if is != stackGone || kept != keptNowhere {
			return nil, out, fmt.Errorf("a stack named %s is already on %s: stack_edit changes its file, and a new stack takes a name of its own", name, env.Name)
		}

		checked, err := r.dock.PostStacksByNameValidate(ctx, name, dockhand.PostStacksByNameValidateRequest{Compose: in.Compose}, dockhand.PostStacksByNameValidateOperationOptions{Env: &env.ID})
		if err != nil {
			return nil, out, fmt.Errorf("the compose file could not be checked, so nothing was made: %w", refusal(err))
		}
		wrong, others := refusedBy(checked.Model.Findings)
		if len(wrong) > 0 {
			return nil, out, fmt.Errorf("compose would not take the file, so nothing was made: %s", strings.Join(wrong, "; "))
		}
		if others > 0 {
			out.Note = fmt.Sprintf("the checks have %d things to say of the file that do not keep it from being used: stack_validate lists them", others)
		}

		start := in.Start == nil || *in.Start
		loaded := ""
		if provider != nil {
			loaded = ", its secrets loaded from " + provider.Name
			if path != "" {
				loaded += " at " + path
			}
		}
		if in.DryRun {
			out.Changes = append(out.Changes, fmt.Sprintf("would make the stack %s on %s%s", name, env.Name, loaded))
			if start {
				out.Changes = append(out.Changes, "would bring it up")
			}

			// where the file would lie is Dockhand's to say: it depends on the host's name and on where Dockhand was told to keep stacks
			if where, err := r.dock.GetStacksDefaultPath(ctx, dockhand.GetStacksDefaultPathOperationOptions{Name: name, Env: &env.ID}); err != nil {
				out.Note = note(out.Note, "where Dockhand would keep its file could not be asked: "+err.Error())
			} else {
				out.ComposePath = where.Model.ComposePath
			}

			return nil, out, nil
		}

		// made without being started, which is quick: bringing it up is a deploy like any other, and can be waited for
		request := dockhand.PostStacksRequest{Name: name, Compose: in.Compose, RawEnvContent: in.EnvFile, Start: new(false)}
		if provider != nil {
			request.SecretProviderId = provider.Id
			if path != "" {
				request.RawEnvContent = withSecretPath(in.EnvFile, path)
			}
		}
		for _, secret := range slices.Sorted(maps.Keys(in.Secrets)) {
			request.EnvVars = append(request.EnvVars, map[string]any{"key": secret, "value": in.Secrets[secret], "isSecret": true})
		}
		made, err := r.dock.PostStacks(ctx, request, dockhand.PostStacksOperationOptions{Env: &env.ID})
		if err != nil {
			return nil, out, fmt.Errorf("the stack %s was not made on %s: %w", name, env.Name, refusal(err))
		}
		if !yes(made.Model.Success) {
			return nil, out, fmt.Errorf("the stack %s was not made on %s: Dockhand says %s", name, env.Name, cmp.Or(lastLine(made.Model.Error), "it could not, and gives no reason"))
		}
		out.Changes = append(out.Changes, fmt.Sprintf("made the stack %s on %s%s", name, env.Name, loaded))
		if file, has, err := r.composeOf(ctx, env, name); err == nil && has {
			out.ComposePath = file.ComposePath
		}

		if start {
			request := dockhand.PostStacksByNameDeployRequest{}
			if in.Pull {
				request.Pull = new(true)
			}
			printed, state, failed := r.deploy(ctx, env, name, request, patience)
			out.jobState = state
			if failed != nil {
				return nil, out, fmt.Errorf("the stack %s was made on %s and could not be brought up: %w; it is kept, with whatever compose had made by then: stack_deploy tries again once what is wrong is put right, and stack_delete removes it", name, env.Name, failed)
			}
			if state.Running {
				out.Note = noted(out.Note, fmt.Sprintf("it was made and was still being brought up after %s: job_get with the job's id says how it ends", seconds(patience)))

				return nil, out, nil
			}
			out.Printed, out.Progress, out.Run = printed, nil, r.newestRun(ctx, env, name)
			out.Changes = append(out.Changes, "brought it up")
		}

		if out.Status, _, err = r.standing(ctx, env, name); err != nil {
			out.Status, out.Note = "", noted(out.Note, "how it stands could not be read back: "+err.Error())
		} else if start && out.Status != stackRunning {
			out.Note = noted(out.Note, fmt.Sprintf("it was brought up and is %s: stack_get says which of its containers are not running", out.Status))
		}

		return nil, out, nil
	})

	type stackEditIn struct {
		stackArg
		dryRunArg

		Compose        *string           `json:"compose,omitempty"         jsonschema:"the compose file's new text, whole: it replaces the file"`
		EnvFile        *string           `json:"env_file,omitempty"        jsonschema:"the .env file's new text, whole, NAME=value a line: it replaces the file, and an empty text removes it"`
		Secrets        map[string]string `json:"secrets,omitempty"         jsonschema:"secrets to add or to give a new value, each name with its value; the stack's other secrets are kept as they are"`
		RemoveSecrets  []string          `json:"remove_secrets,omitempty"  jsonschema:"secrets to remove, by name"`
		SecretProvider any               `json:"secret_provider,omitempty" jsonschema:"a secret provider the Dockhand keeps, by name or number, to load the stack's secrets from each time it is deployed; an empty text takes the stack off the one it has"`
		SecretPath     *string           `json:"secret_path,omitempty"     jsonschema:"the path in the stack's secret provider whose every key is loaded as a variable of that name, kept in the .env file as DOCKHAND_SECRET_SELECTOR; an empty text takes it out"`
		Force          bool              `json:"force,omitempty"           jsonschema:"save a compose file though the checks say compose would not take it"`
	}
	type stackEditOut struct {
		Env     string   `json:"env"               jsonschema:"the environment (Docker host) the stack is on"`
		Stack   string   `json:"stack"`
		Changes []string `json:"changes"           jsonschema:"what was saved, in words; empty when everything given is as the stack has it already"`
		DryRun  bool     `json:"dry_run,omitempty" jsonschema:"nothing was saved: changes says what would be"`
		Note    string   `json:"note,omitempty"`
	}
	add(r, writeTool, &mcp.Tool{
		Name:        "stack_edit",
		Description: "Change what Dockhand keeps for one compose stack: its compose file, its .env file, and the variables kept as secrets. Each is given whole or left out: compose and env_file replace the file they name, secrets adds or replaces the ones named and keeps the rest, and remove_secrets takes some away. Nothing running changes: the stack goes on as it was until stack_deploy brings it up with what was saved. A new compose file is checked first, as stack_validate checks it, and one compose would not take is refused unless force is given. The file is written where Dockhand keeps it, which for a stack that was adopted is the file that was adopted. A stack Dockhand only found on a host has no file here to change and is refused; so is a stack deployed from git, whose files are the repository's and whose variables are kept another way: the git toolset changes one. A secret's value is never read back, so one given again is saved again.",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in stackEditIn) (*mcp.CallToolResult, stackEditOut, error) {
		if in.Compose == nil && in.EnvFile == nil && len(in.Secrets) == 0 && len(in.RemoveSecrets) == 0 && in.SecretProvider == nil && in.SecretPath == nil {
			return nil, stackEditOut{}, errors.New("nothing to change was given: compose, env_file, secrets, remove_secrets, secret_provider or secret_path")
		}
		if in.Compose != nil && strings.TrimSpace(*in.Compose) == "" {
			return nil, stackEditOut{}, errors.New("compose is empty: a stack's compose file cannot be, and stack_delete is what removes a stack")
		}
		if err := secretsNamed(in.Secrets); err != nil {
			return nil, stackEditOut{}, err
		}
		for _, name := range in.RemoveSecrets {
			if _, given := in.Secrets[name]; given {
				return nil, stackEditOut{}, fmt.Errorf("the secret %s is both given a value and named to be removed", name)
			}
		}
		env, stack, err := r.findStack(ctx, in.stackArg)
		if err != nil {
			return nil, stackEditOut{}, err
		}
		out := stackEditOut{Env: env.Name, Stack: stack.Name, Changes: []string{}, DryRun: in.DryRun}
		at := &env.ID

		// a stack deployed from git is changed as one: its files are the repository's, replaced from there at each deploy, and the variables Dockhand
		// keeps for it are not all secrets, so saving its secrets the way an ordinary stack's are saved would drop the rest
		kept, bound, err := r.recordOf(ctx, env, stack.Name)
		if err != nil {
			return nil, out, fmt.Errorf("how Dockhand keeps %s could not be read, so nothing was changed: %w", stack.Name, err)
		}
		if kept == keptGit {
			return nil, out, fmt.Errorf("%s is deployed from a git repository, and is changed as a git stack: its compose file and .env file are the repository's and are replaced from it at each deploy, and what Dockhand keeps for it besides, its variables, its secrets and its secret provider, is set with the git tools (--toolsets git)", stack.Name)
		}

		file, has, err := r.composeOf(ctx, env, stack.Name)
		if err != nil {
			return nil, out, fmt.Errorf("the compose file of %s could not be read, so nothing was changed: %w", stack.Name, err)
		}
		if !has {
			return nil, out, r.noFile(ctx, env, stack.Name)
		}

		// everything is read and checked before anything is saved, so that what is refused is refused whole
		newCompose := in.Compose != nil && *in.Compose != file.Content
		if newCompose {
			checked, err := r.dock.PostStacksByNameValidate(ctx, stack.Name, dockhand.PostStacksByNameValidateRequest{Compose: *in.Compose, Existing: new(true)}, dockhand.PostStacksByNameValidateOperationOptions{Env: at})
			if err != nil {
				return nil, out, fmt.Errorf("the new compose file could not be checked, so nothing was changed: %w", refusal(err))
			}
			wrong, others := refusedBy(checked.Model.Findings)
			switch {
			case len(wrong) > 0 && !in.Force:
				return nil, out, fmt.Errorf("compose would not take the new file, so nothing was changed: %s", strings.Join(wrong, "; "))
			case len(wrong) > 0:
				out.Note = "the file was saved as force asked though compose would not take it: " + strings.Join(wrong, "; ")
			case others > 0:
				out.Note = fmt.Sprintf("the checks have %d things to say of the new file that do not keep it from being used: stack_validate lists them", others)
			default:
			}
		}

		// the .env file as it is to be: the one given, or the one there is, with the path the stack's secrets are loaded from set as asked. A file
		// given without that path keeps the one the stack had, since losing it would have the next deploy run with those variables empty
		var envNow, envFile, pathNow string
		otherLines := false
		if in.EnvFile != nil || in.SecretPath != nil || in.SecretProvider != nil {
			raw, err := r.dock.GetStacksByNameEnvRaw(ctx, stack.Name, dockhand.GetStacksByNameEnvRawOperationOptions{Env: at})
			if err != nil {
				return nil, out, fmt.Errorf("the .env file of %s could not be read, so nothing was changed: %w", stack.Name, refusal(err))
			}
			envNow, envFile, pathNow = raw.Model.Content, raw.Model.Content, secretPathIn(raw.Model.Content)
		}
		if in.EnvFile != nil {
			envFile, otherLines = *in.EnvFile, withSecretPath(*in.EnvFile, "") != withSecretPath(envNow, "")
			if in.SecretPath == nil && pathNow != "" && secretPathIn(envFile) == "" {
				envFile = withSecretPath(envFile, pathNow)
				out.Note = "the path its secrets are loaded from, " + pathNow + ", was kept in the new .env file: secret_path with an empty text takes it out"
			}
		}
		var provider *dockhand.GetSecretProvidersResponse
		if in.SecretProvider != nil || in.SecretPath != nil {
			wanted, path, refused := r.secretsFor(ctx, in.SecretProvider, in.SecretPath, bound, secretPathIn(envFile))
			if refused != nil {
				return nil, out, fmt.Errorf("%w, so nothing was changed", refused)
			}
			provider = wanted
			if in.SecretPath != nil {
				envFile = withSecretPath(envFile, path)
			}
		}
		newEnvFile := envFile != envNow
		unbind, rebind := provider == nil && bound != 0 && in.SecretProvider != nil, provider != nil && provider.Id != bound

		var secrets []dockhand.PutStacksByNameEnvRequestVariables
		if len(in.Secrets) > 0 || len(in.RemoveSecrets) > 0 {
			vars, err := r.dock.GetStacksByNameEnv(ctx, stack.Name, dockhand.GetStacksByNameEnvOperationOptions{Env: at})
			if err != nil {
				return nil, out, fmt.Errorf("the secrets of %s could not be read, so nothing was changed: %w", stack.Name, refusal(err))
			}
			var existing []string
			for i := range vars.Model.Variables {
				if yes(vars.Model.Variables[i].IsSecret) {
					existing = append(existing, vars.Model.Variables[i].Key)
				}
			}
			for _, name := range in.RemoveSecrets {
				if !slices.Contains(existing, name) {
					return nil, out, fmt.Errorf("%s has no secret named %s to remove: stack_get lists the ones it has", stack.Name, name)
				}
			}
			// Dockhand keeps the secrets it is sent and drops the rest, and one sent back as it is shown, hidden, keeps the value it had
			for _, name := range existing {
				if _, given := in.Secrets[name]; !given && !slices.Contains(in.RemoveSecrets, name) {
					secrets = append(secrets, dockhand.PutStacksByNameEnvRequestVariables{Key: name, Value: masked, IsSecret: new(true)})
				}
			}
			for _, name := range slices.Sorted(maps.Keys(in.Secrets)) {
				secrets = append(secrets, dockhand.PutStacksByNameEnvRequestVariables{Key: name, Value: in.Secrets[name], IsSecret: new(true)})
			}
			if secrets == nil {
				secrets = []dockhand.PutStacksByNameEnvRequestVariables{}
			}
		}

		does := func(did, would string) string {
			if in.DryRun {
				return would
			}

			return did
		}
		// each step, said as done only once it is: one that fails is told with the ones saved before it
		saved := func(step string, err error) error {
			if err == nil {
				out.Changes = append(out.Changes, step)

				return nil
			}
			if len(out.Changes) == 0 {
				return fmt.Errorf("%s was not changed: %w", stack.Name, err)
			}

			return fmt.Errorf("%s was changed in part: %w; before that, this was saved: %s", stack.Name, err, strings.Join(out.Changes, "; "))
		}

		if newCompose {
			var err error
			if !in.DryRun {
				var answer dockhand.PutStacksByNameComposeOperationResponse
				if answer, err = r.dock.PutStacksByNameCompose(ctx, stack.Name, dockhand.PutStacksByNameComposeRequest{Content: *in.Compose, Restart: new(false)}, dockhand.PutStacksByNameComposeOperationOptions{Env: at}); err != nil {
					err = fmt.Errorf("its compose file could not be saved: %w", refusal(err))
				} else if !yes(answer.Model.Success) {
					err = fmt.Errorf("its compose file could not be saved: Dockhand says %s", cmp.Or(lastLine(answer.Model.Error), "it could not, and gives no reason"))
				}
			}
			lines := func(text string) int { return strings.Count(strings.TrimRight(text, "\n"), "\n") + 1 }
			if err := saved(fmt.Sprintf("%s the compose file at %s: %d lines where there were %d", does("replaced", "would replace"), file.ComposePath, lines(*in.Compose), lines(file.Content)), err); err != nil {
				return nil, out, err
			}
		}

		if rebind || unbind {
			content := file.Content
			if newCompose {
				content = *in.Compose
			}
			var err error
			step := does("took", "would take") + " it off its secret provider"
			if rebind {
				step = fmt.Sprintf("%s its secrets loaded from the provider %s", does("had", "would have"), provider.Name)
			}
			switch {
			case in.DryRun:
			case rebind:
				var answer dockhand.PutStacksByNameComposeOperationResponse
				if answer, err = r.dock.PutStacksByNameCompose(ctx, stack.Name, dockhand.PutStacksByNameComposeRequest{Content: content, SecretProviderId: provider.Id, Restart: new(false)}, dockhand.PutStacksByNameComposeOperationOptions{Env: at}); err != nil {
					err = fmt.Errorf("its secret provider could not be saved: %w", refusal(err))
				} else if !yes(answer.Model.Success) {
					err = fmt.Errorf("its secret provider could not be saved: Dockhand says %s", cmp.Or(lastLine(answer.Model.Error), "it could not, and gives no reason"))
				}
			default:
				if _, err = r.dock.ForgetStackSecretProvider(ctx, stack.Name, content, env.ID); err != nil {
					err = fmt.Errorf("it could not be taken off its secret provider: %w", refusal(err))
				}
			}
			if err := saved(step, err); err != nil {
				return nil, out, err
			}
			if unbind {
				out.Note = noted(out.Note, "with no secret provider, the variables one gave are empty the next time the stack is deployed")
			}
		}

		if newEnvFile {
			var err error
			if !in.DryRun {
				if _, err = r.dock.PutStacksByNameEnvRaw(ctx, stack.Name, dockhand.PutStacksByNameEnvRawRequest{Content: envFile}, dockhand.PutStacksByNameEnvRawOperationOptions{Env: at}); err != nil {
					err = fmt.Errorf("its .env file could not be saved: %w", refusal(err))
				}
			}
			var steps []string
			after := secretPathIn(envFile)
			switch {
			case envFile == "":
				steps = append(steps, does("removed", "would remove")+" the .env file")
			case otherLines || after == pathNow:
				steps = append(steps, does("replaced", "would replace")+" the .env file")
			default:
			}
			switch after {
			case pathNow:
			case "":
				steps = append(steps, does("took out", "would take out")+" the path its secrets are loaded from, "+pathNow)
			default:
				steps = append(steps, does("set", "would set")+" the path its secrets are loaded from to "+after)
			}
			if err := saved(strings.Join(steps, " and "), err); err != nil {
				return nil, out, err
			}
		}

		if secrets != nil {
			var err error
			if !in.DryRun {
				if _, err = r.dock.PutStacksByNameEnv(ctx, stack.Name, dockhand.PutStacksByNameEnvRequest{Variables: secrets}, dockhand.PutStacksByNameEnvOperationOptions{Env: at}); err != nil {
					err = fmt.Errorf("its secrets could not be saved: %w", refusal(err))
				}
			}
			var steps []string
			if set := slices.Sorted(maps.Keys(in.Secrets)); len(set) > 0 {
				steps = append(steps, does("set", "would set")+" the secrets "+strings.Join(set, ", "))
			}
			if len(in.RemoveSecrets) > 0 {
				steps = append(steps, does("removed", "would remove")+" the secrets "+strings.Join(slices.Sorted(slices.Values(in.RemoveSecrets)), ", "))
			}
			if err := saved(strings.Join(steps, " and "), err); err != nil {
				return nil, out, err
			}
		}

		switch {
		case len(out.Changes) == 0:
			out.Note = "nothing was changed: what was given is what the stack has already"
		case !in.DryRun:
			out.Note = noted(out.Note, "the stack runs as it did until it is deployed: stack_deploy brings it up with what was saved")
		default:
		}

		return nil, out, nil
	})

	type stackDeleteIn struct {
		stackArg

		Confirm   bool `json:"confirm,omitempty"    jsonschema:"remove it: without this the answer says what would go and nothing is removed"`
		Volumes   bool `json:"volumes,omitempty"    jsonschema:"remove the stack's named volumes as well, and everything stored in them"`
		KeepFiles bool `json:"keep_files,omitempty" jsonschema:"leave the stack's compose file and .env on Dockhand's disk; Dockhand forgets the stack all the same"`
	}
	type stackDeleteOut struct {
		Env        string   `json:"env"               jsonschema:"the environment (Docker host) the stack is on"`
		Stack      string   `json:"stack"`
		Kept       string   `json:"kept"              jsonschema:"how Dockhand came to have the stack: internal when it has the stack's compose file, git when it deploys it from a repository, found when it only saw it running on the host"`
		Containers []string `json:"containers"        jsonschema:"the stack's containers, each with its state: all of them are removed"`
		Volumes    []string `json:"volumes,omitempty" jsonschema:"the stack's named volumes: removed with volumes, and kept without"`
		Files      string   `json:"files,omitempty"   jsonschema:"the directory on Dockhand's disk that is deleted with everything in it; absent when no file is deleted"`
		Deleted    bool     `json:"deleted"           jsonschema:"whether the stack was removed, read back as no longer listed; false with confirm left out"`
		Note       string   `json:"note,omitempty"`
	}
	add(r, deleteTool, &mcp.Tool{
		Name:        "stack_delete",
		Description: "Remove one compose stack: its containers are stopped and removed, running or not, with the networks it made, and Dockhand forgets it, its variables and its secrets. Without confirm the answer says what would go and removes nothing. For a stack Dockhand keeps the file of in its own data directory, the directory is deleted too unless keep_files is given; files outside it, of a stack that was adopted where they lay or moved there, are never deleted. Named volumes are kept unless volumes is given, which deletes what is stored in them. A stack deployed from git is forgotten as a git stack with it: its schedule, its webhook and Dockhand's copy of the repository go too. For a stack Dockhand only found on a host, the containers are removed and nothing else: its volumes, its networks and its files on the host stay. What the containers held outside a volume is gone, and a secret Dockhand kept cannot be read back; the record of the stack's deploys is kept. Read back as gone.",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in stackDeleteIn) (*mcp.CallToolResult, stackDeleteOut, error) {
		env, stack, err := r.findStack(ctx, in.stackArg)
		if err != nil {
			return nil, stackDeleteOut{}, err
		}
		out := stackDeleteOut{Env: env.Name, Stack: stack.Name, Containers: []string{}}

		preview, err := r.dock.GetStacksByNameDeletePreview(ctx, stack.Name, dockhand.GetStacksByNameDeletePreviewOperationOptions{Env: &env.ID})
		if err != nil {
			return nil, out, fmt.Errorf("what removing %s would take could not be read, so nothing was removed: %w", stack.Name, refusal(err))
		}
		out.Kept, out.Volumes = cmp.Or(preview.Model.SourceType, keptNowhere), preview.Model.NamedVolumes
		for i := range stack.ContainerDetails {
			out.Containers = append(out.Containers, fmt.Sprintf("%s (%s)", stack.ContainerDetails[i].Name, stack.ContainerDetails[i].State))
		}
		slices.Sort(out.Containers)
		if yes(preview.Model.CanDeleteFiles) && !in.KeepFiles {
			out.Files = preview.Model.StackDir
		}

		found := fileless(out.Kept)
		switch {
		case found && in.Volumes:
			return nil, out, fmt.Errorf("Dockhand removes only the containers of %s, a stack it found on the host and has no file for: its volumes are not removed with it, and volumes cannot be asked for", stack.Name) //nolint:staticcheck // ST1005: Dockhand is a name
		case found:
			out.Note = "Dockhand only found this stack on the host: its containers are removed and nothing else, so its volumes, its networks and its files on the host stay"
		case out.Kept == keptGit && out.Files == "" && !in.KeepFiles:
			out.Note = "Dockhand holds no files for this stack to delete: it is deployed from git and was never fetched, or the git stack it was is gone and only its record is left"
		case out.Files == "" && !in.KeepFiles:
			out.Note = "the stack's files are outside Dockhand's own data directory, where it was shown them or they were moved to, and are not deleted"
		default:
		}
		if !in.Confirm {
			out.Note = noted(out.Note, "nothing was removed: call again with confirm to remove it")

			return nil, out, nil
		}

		// force is what has Dockhand remove a running container of a stack it only found, and a stack whose compose file no longer parses: what goes
		// was said before this was confirmed
		options := dockhand.DeleteStacksByNameOperationOptions{Env: &env.ID, Force: new(true), Files: new(!in.KeepFiles)}
		if in.Volumes {
			options.Volumes = new(true)
		}
		removed, err := r.dock.DeleteStacksByName(ctx, stack.Name, options)
		if err != nil {
			return nil, out, fmt.Errorf("%s on %s was not removed: %w", stack.Name, env.Name, refusal(err))
		}
		if !yes(removed.Model.Success) {
			return nil, out, fmt.Errorf("%s on %s was not removed, and Dockhand gives no reason", stack.Name, env.Name)
		}

		is, left, err := r.standing(ctx, env, stack.Name)
		if err != nil {
			out.Note = noted(out.Note, "that it is gone could not be read back: "+err.Error())

			return nil, out, nil //nolint:nilerr // Dockhand said it removed the stack: that it could not be read back is said in the answer
		}
		out.Deleted = is == stackGone
		if !out.Deleted {
			out.Note = noted(out.Note, fmt.Sprintf("Dockhand said it removed the stack and it is still listed, as %s with %d containers", is, len(left)))
		}

		if in.Volumes && len(out.Volumes) > 0 {
			if volumes, err := r.volumesOf(ctx, env); err == nil {
				var kept []string
				for _, name := range out.Volumes {
					if slices.ContainsFunc(volumes, func(v dockhand.GetVolumesResponse) bool { return v.Name == name }) {
						kept = append(kept, name)
					}
				}
				if len(kept) > 0 {
					out.Note = noted(out.Note, fmt.Sprintf("%d of its volumes are still there, which something else may be using: %s", len(kept), strings.Join(kept, ", ")))
				}
			}
		}

		return nil, out, nil
	})

	type scannedStack struct {
		Name        string   `json:"name"                 jsonschema:"the name the stack would have: its folder's"`
		ComposePath string   `json:"compose_path"         jsonschema:"where the compose file is, as Dockhand's own container sees the path: what stack_adopt takes"`
		EnvPath     string   `json:"env_path,omitempty"   jsonschema:"the .env file beside it, when there is one"`
		Services    int      `json:"services"             jsonschema:"how many services the file has"`
		RunningOn   []string `json:"running_on,omitempty" jsonschema:"the environments a stack of that name has containers on already, each with how many: adopting it there gives Dockhand the file of a stack that is running"`
		Adopted     bool     `json:"adopted,omitempty"    jsonschema:"Dockhand has this one already"`
	}
	type stackScanIn struct {
		Path string `json:"path" jsonschema:"the directory to look in, as Dockhand's own container sees it: each folder in it that holds a compose file is found"`
	}
	type stackScanOut struct {
		Path   string         `json:"path"`
		Found  []scannedStack `json:"found"            jsonschema:"the compose files found, by name"`
		Errors []string       `json:"errors,omitempty" jsonschema:"what could not be read while looking"`
		Note   string         `json:"note,omitempty"`
	}
	add(r, readTool, &mcp.Tool{
		Name:        "stack_scan",
		Description: "Look through a directory on Dockhand's own filesystem for compose files it could adopt: each folder in it that holds one, with where the file is, how many services it has, whether a stack of that name is already running on a host, and whether Dockhand has it already. The directory is one Dockhand's own container can see, a folder mounted into it, and not a path on a Docker host it manages. Nothing is adopted or changed: stack_adopt does that with a name and a path from here.",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in stackScanIn) (*mcp.CallToolResult, stackScanOut, error) {
		path := strings.TrimSpace(in.Path)
		if path == "" {
			return nil, stackScanOut{}, errors.New("path is required: the directory to look in, as Dockhand's own container sees it")
		}
		out := stackScanOut{Path: path, Found: []scannedStack{}}

		usable, err := r.dock.PostStacksValidatePath(ctx, dockhand.PostStacksValidatePathRequest{Path: path})
		if err != nil {
			return nil, out, fmt.Errorf("%s could not be looked for: %w", path, refusal(err))
		}
		if !yes(usable.Model.Valid) {
			return nil, out, fmt.Errorf("%s cannot be looked through: Dockhand says %s. The path is looked for inside Dockhand's own container, where a folder of a Docker host is seen only when it is mounted in", path, cmp.Or(strings.TrimSpace(usable.Model.Error), "it cannot be used"))
		}

		scanned, err := r.dock.PostStacksScan(ctx, dockhand.PostStacksScanRequest{Path: path})
		if err != nil {
			return nil, out, fmt.Errorf("%s could not be looked through: %w", path, refusal(err))
		}
		for i := range scanned.Model.Discovered {
			d := &scanned.Model.Discovered[i]
			row := scannedStack{Name: d.Name, ComposePath: d.ComposePath, EnvPath: d.EnvPath, Services: d.ServiceCount}
			for _, on := range d.RunningOn {
				row.RunningOn = append(row.RunningOn, fmt.Sprintf("%s (%d containers)", on.EnvName, on.ContainerCount))
			}
			out.Found = append(out.Found, row)
		}
		for i := range scanned.Model.Skipped {
			s := &scanned.Model.Skipped[i]
			out.Found = append(out.Found, scannedStack{Name: s.Name, ComposePath: s.ComposePath, EnvPath: s.EnvPath, Services: s.ServiceCount, Adopted: true})
		}
		slices.SortFunc(out.Found, func(a, b scannedStack) int { return cmp.Compare(a.Name, b.Name) })
		for i := range scanned.Model.Errors {
			out.Errors = append(out.Errors, scanned.Model.Errors[i].Path+": "+scanned.Model.Errors[i].Error)
		}
		if len(out.Found) == 0 {
			out.Note = "no folder in that directory holds a compose file"
		}

		return nil, out, nil
	})

	type stackAdoptIn struct {
		envArg
		dryRunArg

		Name        string `json:"name"         jsonschema:"the name the stack is to have. For one that is already running on the host it is the name stack_list gives it, which is how Dockhand tells the file is that stack's"`
		ComposePath string `json:"compose_path" jsonschema:"where the compose file is, as Dockhand's own container sees the path; stack_scan finds them"`
	}
	type stackAdoptOut struct {
		Env         string   `json:"env"               jsonschema:"the environment (Docker host) the stack is on"`
		Stack       string   `json:"stack"`
		ComposePath string   `json:"compose_path"      jsonschema:"the compose file Dockhand now has for the stack"`
		Status      string   `json:"status,omitempty"  jsonschema:"how the stack stands on the host: running, partial or stopped for one that was there already, and created for one with no containers yet"`
		Changes     []string `json:"changes"           jsonschema:"what was done, in words"`
		DryRun      bool     `json:"dry_run,omitempty" jsonschema:"nothing was done: changes says what would be"`
		Note        string   `json:"note,omitempty"`
	}
	add(r, writeTool, &mcp.Tool{
		Name:        "stack_adopt",
		Description: "Give Dockhand the compose file of a stack, where the file lies: a stack someone brought up on a host by hand, which Dockhand could only start and stop, or one that is not running anywhere yet. It names one environment. The file stays where it is and Dockhand reads it there, so it must be on a path Dockhand's own container can see. Nothing running is touched: the stack's containers go on as they were, and from then on stack_get shows the file, stack_edit writes to it, and stack_deploy brings the stack up from it. It is not undone lightly: Dockhand cannot be made to forget a stack again without removing its containers. A name Dockhand already has a file for is refused.",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in stackAdoptIn) (*mcp.CallToolResult, stackAdoptOut, error) {
		name, path := strings.TrimSpace(in.Name), strings.TrimSpace(in.ComposePath)
		switch {
		case !stackNamed.MatchString(name):
			return nil, stackAdoptOut{}, fmt.Errorf("name is %q: a stack's name is lower-case letters, digits, hyphens and underscores, and starts with a letter or a digit", in.Name)
		case path == "":
			return nil, stackAdoptOut{}, errors.New("compose_path is required: where the compose file is, as Dockhand's own container sees the path")
		default:
		}
		env, err := r.one(ctx, in.envArg)
		if err != nil {
			return nil, stackAdoptOut{}, err
		}
		out := stackAdoptOut{Env: env.Name, Stack: name, ComposePath: path, Changes: []string{}, DryRun: in.DryRun}

		kept, err := r.keptAs(ctx, env, name)
		if err != nil {
			return nil, out, fmt.Errorf("the stacks Dockhand keeps for %s could not be listed, so nothing was adopted: %w", env.Name, err)
		}
		if !fileless(kept) {
			return nil, out, fmt.Errorf("Dockhand already has a compose file for %s on %s: stack_get shows it", name, env.Name) //nolint:staticcheck // ST1005: Dockhand is a name
		}
		is, there, err := r.standing(ctx, env, name)
		if err != nil {
			return nil, out, fmt.Errorf("the stacks of %s could not be listed, so nothing was adopted: %w", env.Name, err)
		}
		what := fmt.Sprintf("%s on %s, which has no containers there yet", name, env.Name)
		if is != stackGone {
			out.Status = is
			what = fmt.Sprintf("%s on %s, which is %s there with %d containers", name, env.Name, is, len(there))
		}
		if in.DryRun {
			out.Changes = append(out.Changes, fmt.Sprintf("would give Dockhand %s as the compose file of %s", path, what))

			return nil, out, nil
		}

		if kept == keptRecorded {
			// Dockhand adopts a file for a stack it has a record of as a second stack, name-1: the record is given its file by saving the file where
			// it lies
			if failed := r.fileGiven(ctx, env, name, path); failed != nil {
				return nil, out, failed
			}
		} else {
			adopted, refused := r.dock.PostStacksAdopt(ctx, dockhand.PostStacksAdoptRequest{EnvironmentId: env.ID, Stacks: []dockhand.PostStacksAdoptRequestStacks{{Name: name, ComposePath: path}}})
			if refused != nil {
				return nil, out, fmt.Errorf("%s was not adopted: %w", name, refusal(refused))
			}
			if failed := adopted.Model.Failed; len(failed) > 0 {
				return nil, out, fmt.Errorf("%s was not adopted: Dockhand says %s", name, cmp.Or(strings.TrimSpace(failed[0].Error), "it could not, and gives no reason"))
			}
			if !slices.Contains(adopted.Model.Adopted, name) {
				return nil, out, fmt.Errorf("%s was not adopted, and Dockhand gives no reason", name)
			}
		}
		out.Changes = append(out.Changes, fmt.Sprintf("gave Dockhand %s as the compose file of %s", path, what))

		if file, has, err := r.composeOf(ctx, env, name); err != nil || !has {
			out.Note = "that Dockhand now has the file could not be read back"
		} else {
			out.ComposePath = file.ComposePath
		}
		if out.Status, _, err = r.standing(ctx, env, name); err != nil {
			out.Status = ""
		}
		out.Note = noted(out.Note, "nothing running was touched: stack_deploy brings the stack up from the file, which replaces any container that differs from what the file says")

		return nil, out, nil
	})

	type stackMoveIn struct {
		stackArg
		dryRunArg

		ComposePath string `json:"compose_path"        jsonschema:"where the compose file is to be, as Dockhand's own container sees the path: the whole path, file name and all. The folder it names is made when it is not there"`
		Overwrite   bool   `json:"overwrite,omitempty" jsonschema:"move into a folder that is already there. Dockhand replaces a file of the same name in it without a word, so this is for a folder known to hold nothing that matters"`
	}
	type stackMoveOut struct {
		Env     string   `json:"env"               jsonschema:"the environment (Docker host) the stack is on"`
		Stack   string   `json:"stack"`
		Was     string   `json:"was"               jsonschema:"where its compose file was"`
		Is      string   `json:"is"                jsonschema:"where its compose file is, read back; with dry_run, where it still is"`
		Moved   []string `json:"moved"             jsonschema:"the files that were moved, by name"`
		Changes []string `json:"changes"           jsonschema:"what was done, in words"`
		DryRun  bool     `json:"dry_run,omitempty" jsonschema:"nothing was moved: changes says what would be"`
		Note    string   `json:"note,omitempty"`
	}
	add(r, writeTool, &mcp.Tool{
		Name:        "stack_move",
		Description: "Move the files Dockhand keeps for one compose stack, its compose file and the .env beside it, to another folder on Dockhand's own filesystem, and have Dockhand deploy the stack from there from then on: out of Dockhand's data directory into a folder that is mounted into it and kept elsewhere, say. Nothing running is touched. A path in the compose file that is relative to the file, a bind mount of ./data say, is found from the new folder at the next deploy. A folder that is already there is refused unless overwrite is given, because Dockhand writes over a file of the same name in it. Once its files are outside Dockhand's data directory, removing the stack no longer deletes them. A stack Dockhand only found on a host has no files here to move and is refused.",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in stackMoveIn) (*mcp.CallToolResult, stackMoveOut, error) {
		to := strings.TrimSpace(in.ComposePath)
		cut := strings.LastIndex(to, "/")
		if !strings.HasPrefix(to, "/") || cut <= 0 || strings.HasSuffix(to, "/") {
			return nil, stackMoveOut{}, fmt.Errorf("compose_path is %q: it is the whole path the compose file is to have inside Dockhand's own container, such as /stacks/web/compose.yaml", in.ComposePath)
		}
		folder := to[:cut]
		env, stack, err := r.findStack(ctx, in.stackArg)
		if err != nil {
			return nil, stackMoveOut{}, err
		}
		out := stackMoveOut{Env: env.Name, Stack: stack.Name, Moved: []string{}, Changes: []string{}, DryRun: in.DryRun}

		file, has, err := r.composeOf(ctx, env, stack.Name)
		if err != nil {
			return nil, out, fmt.Errorf("the compose file of %s could not be read, so nothing was moved: %w", stack.Name, err)
		}
		if !has {
			return nil, out, r.noFile(ctx, env, stack.Name)
		}
		out.Was, out.Is = file.ComposePath, file.ComposePath

		change, err := r.dock.PostStacksByNameCheckPathChange(ctx, stack.Name, dockhand.PostStacksByNameCheckPathChangeRequest{NewComposePath: to}, dockhand.PostStacksByNameCheckPathChangeOperationOptions{Env: &env.ID})
		if err != nil {
			return nil, out, fmt.Errorf("what moving %s would take could not be read, so nothing was moved: %w", stack.Name, refusal(err))
		}
		if to == file.ComposePath || !yes(change.Model.HasChanges) {
			out.Note = "nothing was moved: that is where the stack's files are already"

			return nil, out, nil
		}

		// Dockhand moves into a folder that is there without looking at what it holds, and a file of the same name is replaced
		there, err := r.dock.PostStacksValidatePath(ctx, dockhand.PostStacksValidatePathRequest{Path: folder})
		if err != nil {
			return nil, out, fmt.Errorf("whether %s is there already could not be read, so nothing was moved: %w", folder, refusal(err))
		}
		switch {
		case yes(there.Model.Valid) && !in.Overwrite:
			return nil, out, fmt.Errorf("%s is there already, and Dockhand would write over any file in it named as one of the stack's: give a folder that is not there yet, or overwrite when it is known to hold nothing that matters", folder)
		case !yes(there.Model.Valid) && !strings.Contains(there.Model.Error, "does not exist"):
			return nil, out, fmt.Errorf("the stack's files cannot be moved to %s: Dockhand says %s", folder, cmp.Or(strings.TrimSpace(there.Model.Error), "it cannot be used"))
		default:
		}
		if warning := strings.TrimSpace(change.Model.PersistenceWarning); warning != "" {
			out.Note = "Dockhand warns: " + warning
		}

		what := fmt.Sprintf("the %d files of %s from %s to %s", change.Model.FileCount, stack.Name, change.Model.OldDir, folder)
		if in.DryRun {
			out.Changes = append(out.Changes, "would move "+what)

			return nil, out, nil
		}

		moved, err := r.dock.PostStacksByNameRelocate(ctx, stack.Name, dockhand.PostStacksByNameRelocateRequest{OldDir: change.Model.OldDir, NewComposePath: to}, dockhand.PostStacksByNameRelocateOperationOptions{Env: &env.ID})
		if err != nil {
			return nil, out, fmt.Errorf("the files of %s were not moved: %w", stack.Name, refusal(err))
		}
		if !yes(moved.Model.Success) {
			return nil, out, fmt.Errorf("the files of %s were not moved: Dockhand says %s", stack.Name, cmp.Or(strings.Join(moved.Model.Errors, "; "), "it could not, and gives no reason"))
		}
		out.Moved = append(out.Moved, moved.Model.MovedFiles...)
		slices.Sort(out.Moved)
		out.Changes = append(out.Changes, "moved "+what)

		if now, has, err := r.composeOf(ctx, env, stack.Name); err != nil || !has {
			out.Is, out.Note = "", noted(out.Note, "where the compose file now is could not be read back")
		} else if out.Is = now.ComposePath; out.Is != to {
			out.Note = noted(out.Note, "Dockhand said it moved the files and has the compose file at "+out.Is)
		}
		out.Note = noted(out.Note, "nothing running was touched: the next stack_deploy brings the stack up from the new folder, which is where a path in the file that is relative to it is then found")

		return nil, out, nil
	})

	type stackDeploysDeleteIn struct {
		stackArg

		Run     int  `json:"run"               jsonschema:"the deploy's number, as stack_deploys gives it"`
		Confirm bool `json:"confirm,omitempty" jsonschema:"remove it: without this the answer says which deploy would go and nothing is removed"`
	}
	type stackDeploysDeleteOut struct {
		Env     string      `json:"env"            jsonschema:"the environment (Docker host) the stack is on"`
		Stack   string      `json:"stack"`
		Deploy  stackDeploy `json:"deploy"         jsonschema:"the deploy whose record is removed"`
		Deleted bool        `json:"deleted"        jsonschema:"whether the record was removed, read back as no longer listed; false with confirm left out"`
		Note    string      `json:"note,omitempty"`
	}
	add(r, deleteTool, &mcp.Tool{
		Name:        "stack_deploys_delete",
		Description: "Remove the record of one deploy of a compose stack: the entry stack_deploys lists and what compose printed during it. Nothing about the stack or what it runs changes; it is the history that is shortened, and what is removed cannot be got back. Without confirm the answer says which deploy would go and removes nothing.",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in stackDeploysDeleteIn) (*mcp.CallToolResult, stackDeploysDeleteOut, error) {
		if in.Run <= 0 {
			return nil, stackDeploysDeleteOut{}, fmt.Errorf("run is %d: it is the deploy's number, as stack_deploys gives it", in.Run)
		}
		env, stack, err := r.findStack(ctx, in.stackArg)
		if err != nil {
			return nil, stackDeploysDeleteOut{}, err
		}
		out := stackDeploysDeleteOut{Env: env.Name, Stack: stack.Name}

		listed := func() (*dockhand.GetStacksByNameDeploysResponseRuns, error) {
			runs, failed := r.deploysOf(ctx, env, stack.Name)
			if failed != nil {
				return nil, failed
			}
			for i := range runs {
				if runs[i].Id == in.Run {
					return &runs[i], nil
				}
			}

			return nil, nil //nolint:nilnil // a deploy that is not listed is no failure of listing them
		}
		run, err := listed()
		if err != nil {
			return nil, out, fmt.Errorf("the deploys of %s could not be listed, so nothing was removed: %w", stack.Name, err)
		}
		if run == nil {
			return nil, out, fmt.Errorf("%s has no deploy numbered %d: stack_deploys lists the ones it has", stack.Name, in.Run)
		}
		out.Deploy = deployOf(run)
		if !in.Confirm {
			out.Note = "nothing was removed: call again with confirm to remove the record of this deploy"

			return nil, out, nil
		}

		removed, err := r.dock.DeleteStacksByNameDeploysByRunId(ctx, stack.Name, in.Run)
		if err != nil {
			return nil, out, fmt.Errorf("the record of deploy %d of %s was not removed: %w", in.Run, stack.Name, refusal(err))
		}
		if !yes(removed.Model.Success) {
			return nil, out, fmt.Errorf("the record of deploy %d of %s was not removed, and Dockhand gives no reason", in.Run, stack.Name)
		}

		switch still, err := listed(); {
		case err != nil:
			out.Note = "that it is gone could not be read back: " + err.Error()
		case still != nil:
			out.Note = "Dockhand said it removed the record and it is still listed"
		default:
			out.Deleted = true
		}

		return nil, out, nil
	})
}

// secretsFor is the secret provider a stack is to have and the path in it, as a call asks: the provider named, or the one the stack has when none is,
// or none when an empty name takes the stack off it. A path is held against the provider before anything is saved, since Dockhand saves a path
// nothing is at and then cannot deploy the stack.
func (r *registry) secretsFor(ctx context.Context, named any, path *string, bound int, pathNow string) (*dockhand.GetSecretProvidersResponse, string, error) {
	wanted := pathNow
	if path != nil {
		wanted = strings.TrimSpace(*path)
	}
	off := false
	if text, is := named.(string); is && strings.TrimSpace(text) == "" {
		off = true
	}
	if (named == nil && bound == 0) || off {
		if path != nil && wanted != "" {
			return nil, "", errors.New("secret_path is a path in a secret provider, and the stack has none: secret_provider names one")
		}

		return nil, wanted, nil
	}

	listed, err := r.secretProviders(ctx)
	if err != nil {
		return nil, "", err
	}
	var provider *dockhand.GetSecretProvidersResponse
	if named != nil {
		if provider, err = secretProviderIn(listed, named, "secret_provider"); err != nil {
			return nil, "", err
		}
	} else if i := slices.IndexFunc(listed, func(p dockhand.GetSecretProvidersResponse) bool { return p.Id == bound }); i >= 0 {
		provider = &listed[i]
	}
	if provider == nil {
		return nil, "", errors.New("the secret provider the stack has is no longer one the Dockhand keeps: secret_provider names another")
	}
	// only what the call changes is held against the provider: a path and a provider both left as they were are not asked about again
	if wanted != "" && (wanted != pathNow || provider.Id != bound) {
		if _, err := r.secretKeysAt(ctx, provider, wanted); err != nil {
			return nil, "", err
		}
	}

	return provider, wanted, nil
}
