//go:build acceptance

package dockhand_test

import (
	"io"
	"slices"
	"strings"
	"testing"

	"github.com/katbyte/dockhand-mcp/lib/testenv"
	"github.com/katbyte/dockhand-mcp/sdk/dockhand"
	"github.com/katbyte/pandorest/client"
)

// laidUnder is where these tests put compose files inside the Dockhand's own container, as a folder mounted into it would hold them.
const laidUnder = "/srv/it-stacks"

// stackFile is a compose file for a stack Dockhand is to keep: one service that says something and is given a variable, with a volume of its own.
func stackFile(says string) string {
	return "services:\n  app:\n    image: " + world.Image + "\n    command: [\"sh\", \"-c\", \"echo " + says + "; sleep 100000\"]\n    stop_grace_period: 1s\n    environment:\n      MODE: ${MODE:-none}\n      DB_PASSWORD: ${DB_PASSWORD:-none}\n    volumes:\n      - kept:/kept\nvolumes:\n  kept: {}\n"
}

// forgotten has a stack a test makes taken away when the test ends, however it ends, and before it starts in case a run cut short left it: what
// Dockhand keeps for it, and its containers, network and volume on the host.
func forgotten(t *testing.T, host, stack string) {
	t.Helper()

	ctx := noCancel(t)
	clean := func() {
		env := world.Env(host)
		// removing a stack Dockhand does not have is no failure of this
		_, _ = world.Dock.DeleteStacksByName(ctx, stack, dockhand.DeleteStacksByNameOptions{Env: env, Force: new(true), Volumes: new(true)})
		if ids, err := world.Docker(ctx, host, "ps", "-aq", "--filter", "label=com.docker.compose.project="+stack); err == nil && strings.TrimSpace(ids) != "" {
			_, _ = world.Docker(ctx, host, append([]string{"rm", "-f", "-v"}, strings.Fields(ids)...)...)
		}
		_, _ = world.Docker(ctx, host, "network", "rm", "-f", stack+"_default")
		_, _ = world.Docker(ctx, host, "volume", "rm", "-f", stack+"_kept")
	}
	clean()
	t.Cleanup(clean)
}

// stackOf is the stack a host lists under a name, or nil when it has none.
func stackOf(t *testing.T, env int, name string) *dockhand.GetStacksAnswer {
	t.Helper()

	listed, err := dock(t).GetStacks(t.Context(), dockhand.GetStacksOptions{Env: dockhand.EnvironmentID(env)})
	if err != nil {
		t.Fatalf("listing the stacks of environment %d: %v", env, err)
	}
	for i := range listed.Answer {
		if listed.Answer[i].Name == name {
			return &listed.Answer[i]
		}
	}

	return nil
}

// on reports whether a host's own Docker has something: a container, a volume or a network, by its name.
func on(t *testing.T, host, kind, name string) bool {
	t.Helper()

	_, err := world.Docker(noCancel(t), host, kind, "inspect", name)

	return err == nil
}

// givenTo is what a host's own Docker says a container was given to run with, each as NAME=value.
func givenTo(t *testing.T, host, container string) []string {
	t.Helper()

	out, err := world.Docker(noCancel(t), host, "inspect", "--format", "{{range .Config.Env}}{{println .}}{{end}}", container)
	if err != nil {
		t.Fatalf("asking %s what %s was given: %v", host, container, err)
	}

	return strings.Fields(out)
}

// Making a stack, and bringing one up, answer one of two ways, by what the caller says it accepts: the work done first and how it went, or a job at
// once that says how it goes. A stack whose file compose will not take is made all the same, and answers 200 saying it was not brought up.
func TestStackMadeBothWays(t *testing.T) {
	c := dock(t)
	const host = testenv.Alpha
	env := world.Env(host)
	stack := named(t, "s")
	container := stack + "-app-1"
	forgotten(t, host, stack)
	at := dockhand.PostStacksOptions{Env: env}
	gone := func() {
		t.Helper()
		if removed, err := c.DeleteStacksByName(t.Context(), stack, dockhand.DeleteStacksByNameOptions{Env: env, Force: new(true), Volumes: new(true)}); err != nil || !removed.Answer.Success {
			t.Fatalf("removing %s = %+v, %v", stack, removed.Answer, err)
		}
	}

	// asked as any call is, for JSON alone: Dockhand makes it, brings it up, and then answers with what compose printed
	waited, err := c.PostStacks(t.Context(), dockhand.PostStacksRequest{Name: stack, Compose: stackFile("waited")}, at)
	if err != nil || !waited.Answer.Success || !waited.Answer.Started || waited.Answer.JobId != "" || !strings.Contains(waited.Answer.Output, container) {
		t.Fatalf("a stack made and waited for = %+v, %v", waited.Answer, err)
	}
	if listed := stackOf(t, int(env), stack); listed == nil || listed.Status != "running" || len(listed.ContainerDetails) != 1 || !on(t, host, "container", container) {
		t.Fatalf("the stack as listed once made = %+v", listed)
	}
	gone()

	// asked as a job: the id at once, and the job says how it goes and how it went
	started, err := c.PostStacks(t.Context(), dockhand.PostStacksRequest{Name: stack, Compose: stackFile("as a job")}, dockhand.PostStacksOptions{Accept: dockhand.AcceptJob, Env: env})
	if err != nil || started.Answer.JobId == "" || started.Answer.Success {
		t.Fatalf("a stack made as a job = %+v, %v", started.Answer, err)
	}
	job := ended(t, started.Answer.JobId)
	if job.Status != "done" || !is(job.Result["success"], true) || !is(job.Result["started"], true) || !strings.Contains(asText(job.Result["output"]), container) || len(job.Lines) < 2 || job.Lines[len(job.Lines)-1].Event != "result" {
		t.Errorf("the job of making a stack = %+v", job)
	}
	// what compose prints comes a line at a time, each under the key line
	if !slices.ContainsFunc(job.Lines, func(l dockhand.GetJobsByIdAnswerLines) bool {
		return l.Event == "progress" && strings.Contains(asText(l.Data["line"]), container)
	}) {
		t.Errorf("no line of the job's progress names %s: %+v", container, job.Lines)
	}
	gone()

	// made and not brought up, when asked not to be: listed with no containers
	quiet, err := c.PostStacks(t.Context(), dockhand.PostStacksRequest{Name: stack, Compose: stackFile("quiet"), Start: new(false)}, at)
	if err != nil || !quiet.Answer.Success || quiet.Answer.Started || quiet.Answer.Output != "" {
		t.Fatalf("a stack made and not started = %+v, %v", quiet.Answer, err)
	}
	if listed := stackOf(t, int(env), stack); listed == nil || listed.Status != "created" || len(listed.Containers) != 0 || on(t, host, "container", container) {
		t.Errorf("the stack as listed, made and not started = %+v", listed)
	}
	// a name that is taken is taken over without a word: the file Dockhand has for it is replaced
	if again, err := c.PostStacks(t.Context(), dockhand.PostStacksRequest{Name: stack, Compose: stackFile("taken over"), Start: new(false)}, at); err != nil || !again.Answer.Success {
		t.Errorf("a stack made under a name that is taken = %+v, %v", again.Answer, err)
	}
	if file, err := c.GetStacksByNameCompose(t.Context(), stack, dockhand.GetStacksByNameComposeOptions{Env: env}); err != nil || file.Answer.Content != stackFile("taken over") {
		t.Errorf("the file once the name was taken over = %+v, %v", file.Answer, err)
	}
	gone()

	// a file compose will not take: a 200 that says it was not brought up, and the stack is kept with the file
	broken, err := c.PostStacks(t.Context(), dockhand.PostStacksRequest{Name: stack, Compose: "services:\n  app: [\n"}, at)
	if err != nil || broken.Answer.Success || broken.Answer.Error == "" {
		t.Fatalf("a stack made from a broken file = %+v, %v", broken.Answer, err)
	}
	if sources, err := c.GetStacksSources(t.Context(), dockhand.GetStacksSourcesOptions{Env: env}); err != nil || sources.Answer[stack].SourceType != "internal" {
		t.Errorf("the stacks Dockhand keeps once a broken one was made = %+v, %v", sources.Answer, err)
	}
	// which only force removes: its file does not parse, so it cannot be taken down by it
	if _, err := c.DeleteStacksByName(t.Context(), stack, dockhand.DeleteStacksByNameOptions{Env: env}); client.StatusCode(err) != 400 {
		t.Errorf("removing a stack whose file does not parse, unforced = %v", err)
	}
	gone()

	// what is refused outright: a name Dockhand will not have, and no file
	if _, err := c.PostStacks(t.Context(), dockhand.PostStacksRequest{Name: "Not A Name", Compose: stackFile("x")}, at); client.StatusCode(err) != 400 || !strings.Contains(err.Error(), "lowercase") {
		t.Errorf("a stack with a name Dockhand will not have = %v", err)
	}
	if _, err := c.PostStacks(t.Context(), dockhand.PostStacksRequest{Name: stack}, at); client.StatusCode(err) != 400 || !strings.Contains(err.Error(), "Compose file content is required") {
		t.Errorf("a stack with no file = %v", err)
	}
}

// A stack Dockhand keeps is read every way there is, given variables and secrets, checked, changed, brought up waited for and as a job, has a deploy
// fail, is made again, taken down with and without its volume, and removed: on the host Dockhand reaches by its socket and on the one behind an
// agent.
func TestStackKeptReadChangedDeployedAndRemoved(t *testing.T) {
	c := dock(t)
	for _, host := range []string{testenv.Alpha, testenv.Beta} {
		env := world.Env(host)
		stack := named(t, host)
		container, volume := stack+"-app-1", stack+"_kept"
		forgotten(t, host, stack)

		made, err := c.PostStacks(t.Context(), dockhand.PostStacksRequest{Name: stack, Compose: stackFile("first"), Start: new(false), RawEnvContent: "MODE=demo\n", EnvVars: []map[string]any{{"key": "DB_PASSWORD", "value": "not-a-real-password", "isSecret": true}}}, dockhand.PostStacksOptions{Env: env})
		if err != nil || !made.Answer.Success {
			t.Fatalf("%s: making the stack = %+v, %v", host, made.Answer, err)
		}

		// where it is kept: under Dockhand's own directory, by host and name
		base, err := c.GetStacksBasePath(t.Context(), dockhand.GetStacksBasePathOptions{Env: env})
		if err != nil || !strings.HasPrefix(base.Answer.BasePath, "/") {
			t.Fatalf("%s: the directory Dockhand keeps stacks in = %+v, %v", host, base.Answer, err)
		}
		dir := base.Answer.BasePath + "/" + host + "/" + stack
		usual, err := c.GetStacksDefaultPath(t.Context(), dockhand.GetStacksDefaultPathOptions{Name: stack, Env: env})
		if err != nil || usual.Answer.StackDir != dir || usual.Answer.ComposePath != dir+"/compose.yaml" || usual.Answer.EnvPath != dir+"/.env" || usual.Answer.Source != "default" {
			t.Errorf("%s: where a stack of that name is kept unless said = %+v, %v", host, usual.Answer, err)
		}
		file, err := c.GetStacksByNameCompose(t.Context(), stack, dockhand.GetStacksByNameComposeOptions{Env: env})
		if err != nil || file.Answer.Content != stackFile("first") || file.Answer.StackDir != dir || file.Answer.ComposePath != dir+"/compose.yaml" || file.Answer.SuggestedEnvPath != dir+"/.env" {
			t.Fatalf("%s: the compose file as kept = %+v, %v", host, file.Answer, err)
		}
		sources, err := c.GetStacksSources(t.Context(), dockhand.GetStacksSourcesOptions{Env: env})
		if kept := sources.Answer[stack]; err != nil || kept.SourceType != "internal" || kept.ComposePath != dir+"/compose.yaml" || kept.EnvVarCount != 2 {
			t.Errorf("%s: how Dockhand keeps the stack = %+v, %v", host, sources.Answer[stack], err)
		}

		// its variables: the ones in its .env file as they are, and the secret hidden
		vars, err := c.GetStacksByNameEnv(t.Context(), stack, dockhand.GetStacksByNameEnvOptions{Env: env})
		if err != nil || len(vars.Answer.Variables) != 2 {
			t.Fatalf("%s: the stack's variables = %+v, %v", host, vars.Answer, err)
		}
		for _, v := range vars.Answer.Variables {
			if (v.Key == "MODE" && (v.Value != "demo" || v.IsSecret)) || (v.Key == "DB_PASSWORD" && (v.Value != "***" || !v.IsSecret)) {
				t.Errorf("%s: a variable as kept = %+v", host, v)
			}
		}
		raw, err := c.GetStacksByNameEnvRaw(t.Context(), stack, dockhand.GetStacksByNameEnvRawOptions{Env: env})
		if err != nil || raw.Answer.Content != "MODE=demo\n" {
			t.Errorf("%s: the .env file as kept = %+v, %v", host, raw.Answer, err)
		}

		// the .env file replaced, removed by saving it empty, and put back; a body with no content at all is refused
		if saved, err := c.PutStacksByNameEnvRaw(t.Context(), stack, dockhand.PutStacksByNameEnvRawRequest{Content: ""}, dockhand.PutStacksByNameEnvRawOptions{Env: env}); err != nil || !saved.Answer.Success || !saved.Answer.Deleted {
			t.Errorf("%s: saving the .env file empty = %+v, %v", host, saved.Answer, err)
		}
		// the value is one no part of the stack's name is: through an agent, the deploy of a stack named with a word that is also the value of one of
		// its variables is recorded with no summary and no log
		if saved, err := c.PutStacksByNameEnvRaw(t.Context(), stack, dockhand.PutStacksByNameEnvRawRequest{Content: "MODE=altered\n"}, dockhand.PutStacksByNameEnvRawOptions{Env: env}); err != nil || !saved.Answer.Success || saved.Answer.Deleted {
			t.Errorf("%s: saving the .env file = %+v, %v", host, saved.Answer, err)
		}
		// Dockhand will not write the mark of a hidden value into the file
		if _, err := c.PutStacksByNameEnvRaw(t.Context(), stack, dockhand.PutStacksByNameEnvRawRequest{Content: "MODE=***\n"}, dockhand.PutStacksByNameEnvRawOptions{Env: env}); client.StatusCode(err) != 400 {
			t.Errorf("%s: saving a .env file with a hidden value in it = %v", host, err)
		}

		// its secrets: the ones sent are kept and the rest dropped, and one sent back hidden keeps the value it had
		secrets := func(sent ...dockhand.PutStacksByNameEnvRequestVariables) {
			t.Helper()
			if saved, err := c.PutStacksByNameEnv(t.Context(), stack, dockhand.PutStacksByNameEnvRequest{Variables: sent}, dockhand.PutStacksByNameEnvOptions{Env: env}); err != nil || !saved.Answer.Success || saved.Answer.Count != len(sent) {
				t.Fatalf("%s: saving %d secrets = %+v, %v", host, len(sent), saved.Answer, err)
			}
		}
		secrets(dockhand.PutStacksByNameEnvRequestVariables{Key: "DB_PASSWORD", Value: "***", IsSecret: new(true)}, dockhand.PutStacksByNameEnvRequestVariables{Key: "SPARE_KEY", Value: "not-a-real-key", IsSecret: new(true)})
		secrets(dockhand.PutStacksByNameEnvRequestVariables{Key: "DB_PASSWORD", Value: "***", IsSecret: new(true)})
		if vars, err = c.GetStacksByNameEnv(t.Context(), stack, dockhand.GetStacksByNameEnvOptions{Env: env}); err != nil || len(vars.Answer.Variables) != 2 || slices.ContainsFunc(vars.Answer.Variables, func(v dockhand.GetStacksByNameEnvAnswerVariables) bool { return v.Key == "SPARE_KEY" }) {
			t.Errorf("%s: the variables once a secret was left out = %+v, %v", host, vars.Answer, err)
		}

		// checked: the file as this stack's, and the variables it uses against the ones named
		checked, err := c.PostStacksByNameValidate(t.Context(), stack, dockhand.PostStacksByNameValidateRequest{Compose: strings.Replace(stackFile("first"), "volumes:\n  kept: {}\n", "  risky:\n    image: "+strings.Split(world.Image, ":")[0]+"\n    privileged: true\nvolumes:\n  kept: {}\n", 1), Existing: new(true)}, dockhand.PostStacksByNameValidateOptions{Env: env})
		if err != nil || len(checked.Answer.Findings) == 0 {
			t.Fatalf("%s: the file checked = %+v, %v", host, checked.Answer, err)
		}
		privileged := slices.IndexFunc(checked.Answer.Findings, func(f dockhand.PostStacksByNameValidateAnswerFindings) bool {
			return f.RuleId == "PRIVILEGED_CONTAINER"
		})
		if privileged < 0 || checked.Answer.Findings[privileged].Severity != "warn" || checked.Answer.Findings[privileged].Service != "risky" || checked.Answer.Findings[privileged].Line == 0 || checked.Answer.Findings[privileged].Message == "" || checked.Answer.Findings[privileged].Hint == "" {
			t.Errorf("%s: what the check found = %+v", host, checked.Answer.Findings)
		}
		uses, err := c.PostStacksByNameEnvValidate(t.Context(), stack, dockhand.PostStacksByNameEnvValidateRequest{Compose: "services:\n  app:\n    image: x:${TAG}\n    environment:\n      A: ${MODE}\n      B: ${LEVEL:-info}\n", Variables: []string{"MODE", "SPARE"}}, dockhand.PostStacksByNameEnvValidateOptions{Env: env})
		if err != nil || uses.Answer.Valid || strings.Join(uses.Answer.Required, " ") != "MODE TAG" || strings.Join(uses.Answer.Optional, " ") != "LEVEL" || strings.Join(uses.Answer.Missing, " ") != "TAG" || strings.Join(uses.Answer.Unused, " ") != "SPARE" {
			t.Errorf("%s: the variables a file uses, against the ones named = %+v, %v", host, uses.Answer, err)
		}

		// brought up, waited for: what compose printed, and a run recorded with what it was asked and what it did
		deployed, err := c.PostStacksByNameDeploy(t.Context(), stack, dockhand.PostStacksByNameDeployRequest{}, dockhand.PostStacksByNameDeployOptions{Env: env})
		if err != nil || !deployed.Answer.Success || deployed.Answer.JobId != "" || !strings.Contains(deployed.Answer.Output, container) {
			t.Fatalf("%s: a deploy waited for = %+v, %v", host, deployed.Answer, err)
		}
		if given := givenTo(t, host, container); !slices.Contains(given, "MODE=altered") || !slices.Contains(given, "DB_PASSWORD=not-a-real-password") {
			t.Errorf("%s: what the container was given = %v", host, given)
		}
		runs, err := c.GetStacksByNameDeploys(t.Context(), stack, dockhand.GetStacksByNameDeploysOptions{Env: env})
		if err != nil || len(runs.Answer.Runs) == 0 {
			t.Fatalf("%s: the stack's deploys = %+v, %v", host, runs.Answer, err)
		}
		first := runs.Answer.Runs[0]
		if first.Id == 0 || first.Status != "success" || first.TriggeredBy != "manual" || first.TriggeredAt.IsZero() || first.Duration <= 0 || first.ErrorMessage != "" || first.Details == nil || first.Details.Options == nil || first.Details.Options.Pull || first.Details.Summary == nil || first.Details.ExitCode != 0 || len(first.Details.ComposeHash) != 64 || first.Details.LogFile == "" {
			t.Errorf("%s: the deploy as recorded = %+v, details %+v", host, first, first.Details)
		}
		one, err := c.GetStacksByNameDeploysByRunId(t.Context(), stack, first.Id)
		if err != nil || one.Answer.Id != first.Id || one.Answer.Status != "success" {
			t.Errorf("%s: the deploy read by its number = %+v, %v", host, one.Answer, err)
		}
		// what compose printed is a file of text, read from the answer itself
		printed, err := c.GetStacksByNameDeploysByRunIdLog(t.Context(), stack, first.Id)
		if err != nil {
			t.Fatalf("%s: what the deploy printed: %v", host, err)
		}
		log, err := io.ReadAll(printed.HttpResponse.Body)
		_ = printed.HttpResponse.Body.Close()
		if err != nil || !strings.Contains(string(log), container) || !strings.HasPrefix(printed.HttpResponse.Header.Get("Content-Type"), "text/plain") {
			t.Errorf("%s: what the deploy printed = %q as %s, %v", host, log, printed.HttpResponse.Header.Get("Content-Type"), err)
		}

		// the file changed, saved without the stack being brought up again, and deployed as a job that makes the container again
		before, _ := world.Docker(noCancel(t), host, "inspect", "--format", "{{.Id}}", container)
		if saved, err := c.PutStacksByNameCompose(t.Context(), stack, dockhand.PutStacksByNameComposeRequest{Content: stackFile("second"), Restart: new(false)}, dockhand.PutStacksByNameComposeOptions{Env: env}); err != nil || !saved.Answer.Success || saved.Answer.JobId != "" || saved.Answer.Output != "" {
			t.Fatalf("%s: saving the compose file = %+v, %v", host, saved.Answer, err)
		}
		started, err := c.PostStacksByNameDeploy(t.Context(), stack, dockhand.PostStacksByNameDeployRequest{ForceRecreate: new(true)}, dockhand.PostStacksByNameDeployOptions{Accept: dockhand.AcceptJob, Env: env})
		if err != nil || started.Answer.JobId == "" || started.Answer.Success {
			t.Fatalf("%s: a deploy asked as a job = %+v, %v", host, started.Answer, err)
		}
		if job := ended(t, started.Answer.JobId); job.Status != "done" || !is(job.Result["success"], true) || !strings.Contains(asText(job.Result["output"]), container) {
			t.Errorf("%s: the job of a deploy = %+v", host, job)
		}
		if after, _ := world.Docker(noCancel(t), host, "inspect", "--format", "{{.Id}}", container); after == before || after == "" {
			t.Errorf("%s: the container after a deploy told to make every one again is the one from before", host)
		}
		if runs, err = c.GetStacksByNameDeploys(t.Context(), stack, dockhand.GetStacksByNameDeploysOptions{Env: env}); err != nil || len(runs.Answer.Runs) < 2 || runs.Answer.Runs[0].Id <= first.Id || runs.Answer.Runs[0].Details == nil || !runs.Answer.Runs[0].Details.Options.ForceRecreate {
			t.Errorf("%s: the deploys after a second = %+v, %v", host, runs.Answer, err)
		}

		// a deploy compose refuses: a 200 that says so with what compose printed, a run recorded as failed, and what ran left running
		absent := strings.Replace(stackFile("third"), world.Image, "it-local/never-built:1\n    pull_policy: never", 1)
		if _, err := c.PutStacksByNameCompose(t.Context(), stack, dockhand.PutStacksByNameComposeRequest{Content: absent, Restart: new(false)}, dockhand.PutStacksByNameComposeOptions{Env: env}); err != nil {
			t.Fatalf("%s: saving a file that cannot be deployed: %v", host, err)
		}
		refusedUp, err := c.PostStacksByNameDeploy(t.Context(), stack, dockhand.PostStacksByNameDeployRequest{}, dockhand.PostStacksByNameDeployOptions{Env: env})
		if err != nil || refusedUp.Answer.Success || refusedUp.Answer.Error == "" || refusedUp.Answer.Output != "" {
			t.Errorf("%s: a deploy that cannot work = %+v, %v", host, refusedUp.Answer, err)
		}
		if runs, err = c.GetStacksByNameDeploys(t.Context(), stack, dockhand.GetStacksByNameDeploysOptions{Env: env}); err != nil || runs.Answer.Runs[0].Status != "failed" || runs.Answer.Runs[0].ErrorMessage == "" || runs.Answer.Runs[0].Details.ExitCode == 0 {
			t.Errorf("%s: the failed deploy as recorded = %+v, %v", host, runs.Answer.Runs[0], err)
		}
		if listed := stackOf(t, int(env), stack); listed == nil || listed.Status != "running" {
			t.Errorf("%s: the stack after a deploy that failed = %+v", host, listed)
		}
		// the record of a deploy removed: once, and not twice
		failedRun := runs.Answer.Runs[0].Id
		if removed, err := c.DeleteStacksByNameDeploysByRunId(t.Context(), stack, failedRun); err != nil || !removed.Answer.Success {
			t.Errorf("%s: removing a deploy's record = %+v, %v", host, removed.Answer, err)
		}
		if _, err := c.DeleteStacksByNameDeploysByRunId(t.Context(), stack, failedRun); !client.IsNotFound(err) {
			t.Errorf("%s: removing a deploy's record twice = %v", host, err)
		}
		if _, err := c.GetStacksByNameDeploysByRunId(t.Context(), stack, failedRun); !client.IsNotFound(err) {
			t.Errorf("%s: reading a deploy's record once removed = %v", host, err)
		}
		if _, err := c.PutStacksByNameCompose(t.Context(), stack, dockhand.PutStacksByNameComposeRequest{Content: stackFile("second"), Restart: new(false)}, dockhand.PutStacksByNameComposeOptions{Env: env}); err != nil {
			t.Fatalf("%s: saving the file back: %v", host, err)
		}

		// a restart told to make the containers again does, where one told nothing leaves them
		before, _ = world.Docker(noCancel(t), host, "inspect", "--format", "{{.Id}}", container)
		if again, err := c.PostStacksByNameRestart(t.Context(), stack, dockhand.PostStacksByNameRestartOptions{Env: env}); err != nil || !again.Answer.Success {
			t.Errorf("%s: a restart = %+v, %v", host, again.Answer, err)
		}
		if after, _ := world.Docker(noCancel(t), host, "inspect", "--format", "{{.Id}}", container); after != before {
			t.Errorf("%s: a plain restart made the container again", host)
		}
		if again, err := c.PostStacksByNameRestart(t.Context(), stack, dockhand.PostStacksByNameRestartOptions{Env: env, Mode: "recreate"}); err != nil || !again.Answer.Success {
			t.Errorf("%s: a restart that makes the containers again = %+v, %v", host, again.Answer, err)
		}
		if after, _ := world.Docker(noCancel(t), host, "inspect", "--format", "{{.Id}}", container); after == before {
			t.Errorf("%s: a restart told to make the container again left the one from before", host)
		}

		// taken down, waited for: the container goes, the volume stays, and the stack is kept with no containers
		down, err := c.PostStacksByNameDown(t.Context(), stack, dockhand.PostStacksByNameDownRequest{}, dockhand.PostStacksByNameDownOptions{Env: env})
		if err != nil || !down.Answer.Success || !strings.Contains(down.Answer.Output, container) {
			t.Fatalf("%s: a down waited for = %+v, %v", host, down.Answer, err)
		}
		if listed := stackOf(t, int(env), stack); listed == nil || listed.Status != "created" || on(t, host, "container", container) || !on(t, host, "volume", volume) {
			t.Errorf("%s: the stack once down = %+v", host, listed)
		}
		// and as a job, with its volume, once it is up again
		if _, err := c.PostStacksByNameDeploy(t.Context(), stack, dockhand.PostStacksByNameDeployRequest{}, dockhand.PostStacksByNameDeployOptions{Env: env}); err != nil {
			t.Fatalf("%s: bringing it back: %v", host, err)
		}
		downJob, err := c.PostStacksByNameDown(t.Context(), stack, dockhand.PostStacksByNameDownRequest{RemoveVolumes: new(true)}, dockhand.PostStacksByNameDownOptions{Accept: dockhand.AcceptJob, Env: env})
		if err != nil || downJob.Answer.JobId == "" {
			t.Fatalf("%s: a down asked as a job = %+v, %v", host, downJob.Answer, err)
		}
		if job := ended(t, downJob.Answer.JobId); job.Status != "done" || !is(job.Result["success"], true) || on(t, host, "volume", volume) {
			t.Errorf("%s: the job of a down with its volume = %+v", host, job)
		}

		// removed: what would go said first, then the stack, its files and its secrets
		if _, err := c.PostStacksByNameDeploy(t.Context(), stack, dockhand.PostStacksByNameDeployRequest{}, dockhand.PostStacksByNameDeployOptions{Env: env}); err != nil {
			t.Fatalf("%s: bringing it back again: %v", host, err)
		}
		preview, err := c.GetStacksByNameDeletePreview(t.Context(), stack, dockhand.GetStacksByNameDeletePreviewOptions{Env: env})
		if err != nil || preview.Answer.StackName != stack || preview.Answer.SourceType != "internal" || preview.Answer.StackDir != dir || !preview.Answer.CanDeleteFiles || !slices.Equal(preview.Answer.NamedVolumes, []string{volume}) {
			t.Errorf("%s: what removing it would take = %+v, %v", host, preview.Answer, err)
		}
		if removed, err := c.DeleteStacksByName(t.Context(), stack, dockhand.DeleteStacksByNameOptions{Env: env}); err != nil || !removed.Answer.Success {
			t.Fatalf("%s: removing it = %+v, %v", host, removed.Answer, err)
		}
		// its containers and its record are gone, and its volume was not asked for and stays
		if stackOf(t, int(env), stack) != nil || on(t, host, "container", container) || !on(t, host, "volume", volume) {
			t.Errorf("%s: the stack is still there once removed, or its volume went with it", host)
		}
		if _, err := c.GetStacksByNameCompose(t.Context(), stack, dockhand.GetStacksByNameComposeOptions{Env: env}); !client.IsNotFound(err) {
			t.Errorf("%s: its compose file once removed = %v", host, err)
		}
		// the record of its deploys outlives it
		if runs, err = c.GetStacksByNameDeploys(t.Context(), stack, dockhand.GetStacksByNameDeploysOptions{Env: env}); err != nil || len(runs.Answer.Runs) == 0 {
			t.Errorf("%s: its deploys once it was removed = %+v, %v", host, runs.Answer, err)
		}
		// removing what is not there is said to have worked
		if removed, err := c.DeleteStacksByName(t.Context(), stack, dockhand.DeleteStacksByNameOptions{Env: env}); err != nil || !removed.Answer.Success {
			t.Errorf("%s: removing it twice = %+v, %v", host, removed.Answer, err)
		}
	}
}

// A stack someone brought up at a host is one Dockhand has no file for until it is shown one: its file is looked for and found, adopted where it
// lies, moved, and left behind when the stack is removed.
func TestStackFoundScannedAdoptedAndMoved(t *testing.T) {
	c := dock(t)
	const host = testenv.Alpha
	env := world.Env(host)
	stack := named(t, "s")
	container := stack + "-app-1"
	dir := laidUnder + "/" + stack
	path := dir + "/found/" + stack + "/compose.yaml"
	file := "name: " + stack + "\nservices:\n  app:\n    image: " + world.Image + "\n    command: [\"sleep\", \"100000\"]\n    stop_grace_period: 1s\n"
	ctx := noCancel(t)
	forgotten(t, host, stack)
	_ = world.Unlay(ctx, dir)
	t.Cleanup(func() { _ = world.Unlay(ctx, dir) })

	if err := world.Compose(ctx, host, stack, file); err != nil {
		t.Fatal(err)
	}
	for laid, content := range map[string]string{path: file, dir + "/found/" + stack + "/.env": "MODE=demo\n"} {
		if err := world.Lay(ctx, laid, content); err != nil {
			t.Fatal(err)
		}
	}

	// as found: no file, no record, and where its containers say it came from
	if _, err := c.GetStacksByNameCompose(t.Context(), stack, dockhand.GetStacksByNameComposeOptions{Env: env}); !client.IsNotFound(err) {
		t.Errorf("the compose file of a stack Dockhand only found = %v", err)
	}
	hints, err := c.GetStacksPathHints(t.Context(), dockhand.GetStacksPathHintsOptions{Name: stack, Env: env})
	if err != nil || hints.Answer.StackName != stack || hints.Answer.WorkingDir != "/stacks/"+stack || !slices.Equal(hints.Answer.ConfigFiles, []string{"/stacks/" + stack + "/compose.yaml"}) {
		t.Errorf("where its containers say it came from = %+v, %v", hints.Answer, err)
	}
	preview, err := c.GetStacksByNameDeletePreview(t.Context(), stack, dockhand.GetStacksByNameDeletePreviewOptions{Env: env})
	if err != nil || preview.Answer.SourceType != "" || preview.Answer.CanDeleteFiles || preview.Answer.StackDir != "" {
		t.Errorf("what removing a stack Dockhand only found would take = %+v, %v", preview.Answer, err)
	}
	// and nothing to save a file to, or to deploy from
	if _, err := c.PutStacksByNameCompose(t.Context(), stack, dockhand.PutStacksByNameComposeRequest{Content: file, Restart: new(false)}, dockhand.PutStacksByNameComposeOptions{Env: env}); client.StatusCode(err) != 500 || !strings.Contains(err.Error(), "not found") {
		t.Errorf("saving a file for a stack Dockhand only found = %v", err)
	}
	if up, err := c.PostStacksByNameDeploy(t.Context(), stack, dockhand.PostStacksByNameDeployRequest{}, dockhand.PostStacksByNameDeployOptions{Env: env}); err != nil || up.Answer.Success || !strings.Contains(up.Answer.Error, "compose file location not configured") {
		t.Errorf("deploying a stack Dockhand only found = %+v, %v", up.Answer, err)
	}

	// a path is one inside Dockhand's own container, or it is not a path
	if usable, err := c.PostStacksValidatePath(t.Context(), dockhand.PostStacksValidatePathRequest{Path: dir + "/found"}); err != nil || !usable.Answer.Valid || usable.Answer.ResolvedPath != dir+"/found" || usable.Answer.Error != "" {
		t.Errorf("a folder that is there = %+v, %v", usable.Answer, err)
	}
	if usable, err := c.PostStacksValidatePath(t.Context(), dockhand.PostStacksValidatePathRequest{Path: dir + "/nowhere"}); err != nil || usable.Answer.Valid || usable.Answer.Error != "Path does not exist" {
		t.Errorf("a folder that is not there = %+v, %v", usable.Answer, err)
	}
	if usable, err := c.PostStacksValidatePath(t.Context(), dockhand.PostStacksValidatePathRequest{Path: path}); err != nil || usable.Answer.Valid || usable.Answer.Error != "Path is not a directory" {
		t.Errorf("a path that is a file = %+v, %v", usable.Answer, err)
	}

	// looked for: found with its .env, and told to be the stack that runs
	scanned, err := c.PostStacksScan(t.Context(), dockhand.PostStacksScanRequest{Path: dir + "/found"})
	if err != nil || len(scanned.Answer.Discovered) != 1 || len(scanned.Answer.Skipped) != 0 || len(scanned.Answer.Errors) != 0 {
		t.Fatalf("what was found = %+v, %v", scanned.Answer, err)
	}
	if d := scanned.Answer.Discovered[0]; d.Name != stack || d.ComposePath != path || d.EnvPath != dir+"/found/"+stack+"/.env" || d.SourceDir != dir+"/found/"+stack || d.ServiceCount != 1 || d.Unadoptable || len(d.RunningOn) != 1 || d.RunningOn[0].EnvId != env || d.RunningOn[0].EnvName != host || d.RunningOn[0].ContainerCount != 1 {
		t.Errorf("the folder as found = %+v", d)
	}

	// adopted, beside one that cannot be: each is answered for by name
	adopted, err := c.PostStacksAdopt(t.Context(), dockhand.PostStacksAdoptRequest{EnvironmentId: env, Stacks: []dockhand.PostStacksAdoptRequestStacks{{Name: stack, ComposePath: path}, {Name: stack + "-absent", ComposePath: dir + "/absent/compose.yaml"}}})
	if err != nil || !slices.Equal(adopted.Answer.Adopted, []string{stack}) || len(adopted.Answer.Failed) != 1 || adopted.Answer.Failed[0].Name != stack+"-absent" || !strings.Contains(adopted.Answer.Failed[0].Error, "Compose file not found") {
		t.Fatalf("adopting = %+v, %v", adopted.Answer, err)
	}
	kept, err := c.GetStacksByNameCompose(t.Context(), stack, dockhand.GetStacksByNameComposeOptions{Env: env})
	if err != nil || kept.Answer.Content != file || kept.Answer.ComposePath != path || kept.Answer.StackDir != dir+"/found/"+stack {
		t.Errorf("the compose file once adopted = %+v, %v", kept.Answer, err)
	}
	if vars, err := c.GetStacksByNameEnv(t.Context(), stack, dockhand.GetStacksByNameEnvOptions{Env: env}); err != nil || len(vars.Answer.Variables) != 1 || vars.Answer.Variables[0].Key != "MODE" {
		t.Errorf("the variables of the adopted stack, from the .env beside its file = %+v, %v", vars.Answer, err)
	}
	// its files are not Dockhand's to delete, and looked for again it is one Dockhand has
	if preview, err = c.GetStacksByNameDeletePreview(t.Context(), stack, dockhand.GetStacksByNameDeletePreviewOptions{Env: env}); err != nil || preview.Answer.SourceType != "internal" || preview.Answer.CanDeleteFiles {
		t.Errorf("what removing an adopted stack would take = %+v, %v", preview.Answer, err)
	}
	if scanned, err = c.PostStacksScan(t.Context(), dockhand.PostStacksScanRequest{Path: dir + "/found"}); err != nil || len(scanned.Answer.Discovered) != 0 || len(scanned.Answer.Skipped) != 1 || scanned.Answer.Skipped[0].Name != stack || scanned.Answer.Skipped[0].ComposePath != path {
		t.Errorf("what was found once it was adopted = %+v, %v", scanned.Answer, err)
	}

	// moved: what it would take said first, then its files are where they were sent
	to := dir + "/moved/" + stack + "/compose.yaml"
	change, err := c.PostStacksByNameCheckPathChange(t.Context(), stack, dockhand.PostStacksByNameCheckPathChangeRequest{NewComposePath: to}, dockhand.PostStacksByNameCheckPathChangeOptions{Env: env})
	if err != nil || !change.Answer.HasChanges || change.Answer.OldDir != dir+"/found/"+stack || change.Answer.NewDir != dir+"/moved/"+stack || change.Answer.FileCount != 2 || change.Answer.CurrentComposePath != path {
		t.Fatalf("what moving it would take = %+v, %v", change.Answer, err)
	}
	if same, err := c.PostStacksByNameCheckPathChange(t.Context(), stack, dockhand.PostStacksByNameCheckPathChangeRequest{NewComposePath: path}, dockhand.PostStacksByNameCheckPathChangeOptions{Env: env}); err != nil || same.Answer.HasChanges || same.Answer.FileCount != 0 {
		t.Errorf("what moving it to where it is would take = %+v, %v", same.Answer, err)
	}
	moved, err := c.PostStacksByNameRelocate(t.Context(), stack, dockhand.PostStacksByNameRelocateRequest{OldDir: change.Answer.OldDir, NewComposePath: to}, dockhand.PostStacksByNameRelocateOptions{Env: env})
	if err != nil || !moved.Answer.Success || len(moved.Answer.MovedFiles) != 2 || moved.Answer.ComposeContent != file || moved.Answer.RawEnvContent != "MODE=demo\n" || len(moved.Answer.Errors) != 0 {
		t.Fatalf("moving it = %+v, %v", moved.Answer, err)
	}
	if laid, err := world.Laid(ctx, to); err != nil || laid != file {
		t.Errorf("the compose file where it was moved to = %q, %v", laid, err)
	}
	if kept, err = c.GetStacksByNameCompose(t.Context(), stack, dockhand.GetStacksByNameComposeOptions{Env: env}); err != nil || kept.Answer.ComposePath != to {
		t.Errorf("where Dockhand has the file once moved = %+v, %v", kept.Answer, err)
	}
	// from a folder that is not there, nothing is moved
	if _, err := c.PostStacksByNameRelocate(t.Context(), stack, dockhand.PostStacksByNameRelocateRequest{OldDir: dir + "/nowhere", NewComposePath: dir + "/again/compose.yaml"}, dockhand.PostStacksByNameRelocateOptions{Env: env}); client.StatusCode(err) != 400 {
		t.Errorf("moving from a folder that is not there = %v", err)
	}

	// removed: the containers go, running as they are, and the files stay where they were moved to
	if removed, err := c.DeleteStacksByName(t.Context(), stack, dockhand.DeleteStacksByNameOptions{Env: env}); err != nil || !removed.Answer.Success {
		t.Fatalf("removing it = %+v, %v", removed.Answer, err)
	}
	if stackOf(t, int(env), stack) != nil || on(t, host, "container", container) {
		t.Error("the stack is still there once removed")
	}
	if laid, err := world.Laid(ctx, to); err != nil || laid != file {
		t.Errorf("the compose file once the stack was removed = %q, %v", laid, err)
	}
}

// A stack Dockhand only found is removed by removing its containers, which it will not do to running ones unless it is forced, and nothing else of it
// goes: not its volume, though it is asked for.
func TestStackFoundIsRemovedByItsContainersAlone(t *testing.T) {
	c := dock(t)
	const host = testenv.Alpha
	env := world.Env(host)
	stack := named(t, "s")
	container, volume := stack+"-app-1", stack+"_kept"
	forgotten(t, host, stack)

	if err := world.Compose(noCancel(t), host, stack, "name: "+stack+"\n"+stackFile("by hand")); err != nil {
		t.Fatal(err)
	}

	// taking it down is stopping it, whatever is asked
	down, err := c.PostStacksByNameDown(t.Context(), stack, dockhand.PostStacksByNameDownRequest{RemoveVolumes: new(true)}, dockhand.PostStacksByNameDownOptions{Env: env})
	if err != nil || !down.Answer.Success || !strings.Contains(down.Answer.Output, "stop completed") {
		t.Fatalf("a down of a stack Dockhand only found = %+v, %v", down.Answer, err)
	}
	if listed := stackOf(t, int(env), stack); listed == nil || listed.Status != "stopped" || !on(t, host, "container", container) || !on(t, host, "volume", volume) {
		t.Errorf("the stack once taken down = %+v", listed)
	}
	if up, err := c.PostStacksByNameStart(t.Context(), stack, dockhand.PostStacksByNameStartOptions{Env: env}); err != nil || !up.Answer.Success {
		t.Fatalf("starting it again = %+v, %v", up.Answer, err)
	}

	// running, it is not removed unless forced
	if _, err := c.DeleteStacksByName(t.Context(), stack, dockhand.DeleteStacksByNameOptions{Env: env}); client.StatusCode(err) != 400 || !strings.Contains(err.Error(), "container is running") {
		t.Errorf("removing a running stack Dockhand only found, unforced = %v", err)
	}
	if removed, err := c.DeleteStacksByName(t.Context(), stack, dockhand.DeleteStacksByNameOptions{Env: env, Force: new(true), Volumes: new(true)}); err != nil || !removed.Answer.Success {
		t.Fatalf("removing it, forced = %+v, %v", removed.Answer, err)
	}
	if stackOf(t, int(env), stack) != nil || on(t, host, "container", container) {
		t.Error("the stack is still there once removed")
	}
	// its volume was asked for and is still there
	if !on(t, host, "volume", volume) {
		t.Error("the volume of a stack Dockhand only found went with it")
	}
}
