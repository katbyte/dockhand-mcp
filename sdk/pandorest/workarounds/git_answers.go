package workarounds

import (
	"errors"
	"net/http"

	"github.com/katbyte/pandorest/openapi"
)

// gitRepositoryLeftovers are the fields a server sends with a git repository beyond the five the document declares: when it was saved and changed,
// and what is left of the days a repository was itself what was deployed, which nothing in the API sets any more.
func gitRepositoryLeftovers() map[string]*openapi.Schema {
	return map[string]*openapi.Schema{
		"composePath":        said(text(), "left from when a repository was itself deployed: always compose.yaml, and nothing sets it"),
		"environmentId":      said(integer(), "left from when a repository was itself deployed: always null, and nothing sets it"),
		"autoUpdate":         flag(),
		"autoUpdateSchedule": text(),
		"autoUpdateCron":     text(),
		"webhookEnabled":     said(flag(), "always false: nothing in the API switches a repository's own webhook on"),
		"webhookSecret":      text(),
		"lastSync":           said(text(), "when the repository's own copy was last fetched, which no git stack uses"),
		"lastCommit":         said(text(), "the commit the repository's own copy is at, shortened"),
		"syncStatus":         said(text(), "pending for a copy never fetched, then synced or error"),
		"syncError":          text(),
		"createdAt":          text(),
		"updatedAt":          text(),
	}
}

// gitStack is a git stack as a server sends one wherever it sends one.
func gitStack() *openapi.Schema {
	return record(map[string]*openapi.Schema{
		"id":                 integer(),
		"stackName":          text(),
		"environmentId":      said(integer(), "the environment the stack is deployed on; null for one made with none"),
		"repositoryId":       integer(),
		"branch":             said(text(), "the branch this stack follows; null for the repository's own"),
		"composePath":        said(text(), "the compose file's path in the repository"),
		"envFilePath":        said(text(), "one more .env file in the repository to fill the compose file in from; null for none"),
		"autoUpdate":         said(flag(), "whether the stack is fetched and, when something changed, deployed on a schedule"),
		"autoUpdateSchedule": said(text(), "a word for the schedule, as the web interface offers them: the cron beside it is what the schedule goes by"),
		"autoUpdateCron":     text(),
		"webhookEnabled":     flag(),
		"webhookSecret":      said(text(), "the webhook's secret, sent back whole as it was given; null for none"),
		"contextDir":         said(text(), "the folder of the repository that is copied beside the compose file; null for the folder the compose file is in"),
		"buildOnDeploy":      flag(),
		"noBuildCache":       flag(),
		"repullImages":       flag(),
		"forceRedeploy":      flag(),
		"lastSync":           text(),
		"lastCommit":         said(text(), "the commit last fetched, shortened"),
		"syncStatus":         said(text(), "pending for a stack never fetched, then synced or error"),
		"syncError":          text(),
		"syncedFiles":        said(text(), "the files last copied from the repository, each with a hash, as JSON in a text; absent in the list"),
		"createdAt":          text(),
		"updatedAt":          text(),
		"repository": said(record(map[string]*openapi.Schema{
			"id":           integer(),
			"name":         text(),
			"url":          text(),
			"branch":       text(),
			"credentialId": integer(),
		}), "the repository the stack is deployed from"),
	})
}

// gitStackVariables is the variables Dockhand keeps for a git stack as they are sent with one being made or changed.
func gitStackVariables() *openapi.Schema {
	return said(listOf(record(map[string]*openapi.Schema{
		"key":      text(),
		"value":    said(text(), "the value; *** for a secret keeps the one it has"),
		"isSecret": flag(),
	})), "the variables Dockhand keeps for the stack, whole: the ones left out are removed, and an empty list removes them all")
}

// gitStackSettings is what making a git stack and changing one both take, beyond the fields the document declares.
func gitStackSettings() map[string]*openapi.Schema {
	return map[string]*openapi.Schema{
		"branch":             said(text(), "the branch this stack follows instead of the repository's own"),
		"composePath":        said(text(), "the compose file's path in the repository; compose.yaml unless said"),
		"envFilePath":        said(text(), "one more .env file in the repository to fill the compose file in from"),
		"autoUpdate":         flag(),
		"autoUpdateSchedule": said(text(), "a word for the schedule: the cron beside it is what the schedule goes by"),
		"autoUpdateCron":     text(),
		"contextDir":         said(text(), "the folder of the repository that is copied beside the compose file"),
		"buildOnDeploy":      flag(),
		"noBuildCache":       flag(),
		"repullImages":       flag(),
		"forceRedeploy":      flag(),
		"envVars":            gitStackVariables(),
		"deployNow":          said(flag(), "deploy it at once, and answer the stack with how that went as deployResult"),
	}
}

// gitAnswers gives the git calls the answers and the bodies a server takes and sends, where the document declares a handful of fields or none.
type gitAnswers struct{}

func (gitAnswers) Name() string    { return Service + "-git-answers" }
func (gitAnswers) Service() string { return Service }

func (gitAnswers) Bug() string {
	return "a git repository is declared with five fields and a 1.0.51 server sends thirteen more, and in the list the name and kind of its credential as well; trying a repository or a git stack also answers the branch and its newest commit; POST /api/git/branches declares each branch as an object of nothing, and answers its name and commit; asking a repository whether it is behind also answers the two commits, and fetching or deploying it what was fetched and what compose printed; POST /api/git/preview-env and POST /api/git/stacks/{id}/env-files declare their variables as objects of nothing, which are texts by name; the five calls that list, make, read, change and remove a git stack declare no answer at all; making and changing a git stack declare six and four fields of a body the server reads twenty-two and seventeen of; fetching a git stack answers what changed, and deploying one what compose printed, where the document declares success and an error. Deploying one also answers resolvedSecrets, the values of the stack's secrets, which is left undeclared on purpose so that no client holds them"
}

//nolint:gocyclo // one check and one fix for each of the calls the bug names
func (gitAnswers) Apply(spec *openapi.Spec) error {
	// a repository, wherever one is answered whole
	for _, call := range []struct{ method, path string }{{http.MethodGet, "/api/git/repositories"}, {http.MethodPost, "/api/git/repositories"}, {http.MethodGet, "/api/git/repositories/{id}"}, {http.MethodPut, "/api/git/repositories/{id}"}} {
		repository, err := answerTo(spec, call.method, call.path)
		if err != nil {
			return err
		}
		inList := repository.Items != nil
		if inList {
			repository = repository.Items
		}
		if repository.Properties["credentialId"] == nil || repository.Properties["syncStatus"] != nil {
			return errors.New(call.method + " " + call.path + " no longer declares a repository by five fields alone")
		}
		repository.Properties = withFields(repository.Properties, gitRepositoryLeftovers())
		if inList {
			repository.Properties["credentialName"] = said(text(), "the name of the credential it is fetched with; null for none")
			repository.Properties["credentialAuthType"] = said(text(), "that credential's kind: none, password or ssh")
		}
	}

	// trying a repository, saved or not, and a git stack
	for _, path := range []string{"/api/git/repositories/test", "/api/git/repositories/{id}/test", "/api/git/stacks/{id}/test"} {
		tried, err := answerTo(spec, http.MethodPost, path)
		if err != nil {
			return err
		}
		if tried.Properties["success"] == nil || tried.Properties["lastCommit"] != nil {
			return errors.New("POST " + path + " no longer declares success and an error alone")
		}
		tried.Properties["branch"] = said(text(), "the branch that was looked for")
		tried.Properties["lastCommit"] = said(text(), "the newest commit on it, shortened")
	}

	branches, err := answerTo(spec, http.MethodPost, "/api/git/branches")
	if err != nil {
		return err
	}
	if branches.Properties["branches"] == nil || branches.Properties["branches"].Items == nil || len(branches.Properties["branches"].Items.Properties) != 0 {
		return errors.New("POST /api/git/branches no longer declares a branch as an object of nothing")
	}
	branches.Properties["branches"].Items = record(map[string]*openapi.Schema{"name": text(), "sha": said(text(), "its newest commit, shortened")})

	// the repository's own copy: whether it is behind, fetching it, deploying it
	behind, err := answerOf(spec, "/api/git/repositories/{id}/sync")
	if err != nil {
		return err
	}
	if behind.Properties["hasUpdates"] == nil || behind.Properties["latestCommit"] != nil {
		return errors.New("GET /api/git/repositories/{id}/sync no longer declares hasUpdates and an error alone")
	}
	behind.Properties["currentCommit"] = said(text(), "the commit the repository's own copy is at; none for one never fetched")
	behind.Properties["latestCommit"] = said(text(), "the newest commit on the branch; unknown for a copy never fetched")

	fetched, err := answerTo(spec, http.MethodPost, "/api/git/repositories/{id}/sync")
	if err != nil {
		return err
	}
	if fetched.Properties["success"] == nil || fetched.Properties["commit"] != nil {
		return errors.New("POST /api/git/repositories/{id}/sync no longer declares success and an error alone")
	}
	fetched.Properties["commit"] = text()
	fetched.Properties["composeContent"] = said(text(), "the compose file at that commit")
	fetched.Properties["updated"] = said(flag(), "whether the commit is a new one")

	deployed, err := answerTo(spec, http.MethodPost, "/api/git/repositories/{id}/deploy")
	if err != nil {
		return err
	}
	if deployed.Properties["success"] == nil || deployed.Properties["output"] != nil {
		return errors.New("POST /api/git/repositories/{id}/deploy no longer declares success and an error alone")
	}
	deployed.Properties["output"] = text()
	deployed.Properties["command"] = said(text(), "the compose command that was run")
	deployed.Properties["exitCode"] = integer()

	// variables read out of a repository
	for path, fields := range map[string][]string{"/api/git/preview-env": {"vars", "sources"}, "/api/git/stacks/{id}/env-files": {"vars"}} {
		read, undeclared := answerTo(spec, http.MethodPost, path)
		if undeclared != nil {
			return undeclared
		}
		for _, field := range fields {
			if read.Properties[field] == nil || read.Properties[field].Type != openapi.TypeObject || len(read.Properties[field].AdditionalProperties) != 0 {
				return errors.New("POST " + path + " no longer declares " + field + " as an object of nothing")
			}
		}
		read.Properties["vars"] = said(byName(text()), "each variable's value, by its name")
		if len(fields) > 1 {
			read.Properties["sources"] = said(byName(text()), "which file each variable came from, by its name: .env for the one beside the compose file, envFile for the one named")
		}
	}

	// a git stack, wherever one is answered
	listing, err := unanswered(spec, http.MethodGet, "/api/git/stacks")
	if err != nil {
		return err
	}
	answering(listing, listOf(gitStack()))
	for _, call := range []struct{ method, path string }{{http.MethodPost, "/api/git/stacks"}, {http.MethodGet, "/api/git/stacks/{id}"}, {http.MethodPut, "/api/git/stacks/{id}"}} {
		one, declared := unanswered(spec, call.method, call.path)
		if declared != nil {
			return declared
		}
		answering(one, gitStack())
	}
	removed, err := unanswered(spec, http.MethodDelete, "/api/git/stacks/{id}")
	if err != nil {
		return err
	}
	answering(removed, record(map[string]*openapi.Schema{"success": flag()}))

	// what making and changing a git stack take
	making, err := jsonBody(spec, http.MethodPost, "/api/git/stacks")
	if err != nil {
		return err
	}
	changing, err := jsonBody(spec, http.MethodPut, "/api/git/stacks/{id}")
	if err != nil {
		return err
	}
	if making.Properties["repositoryId"] == nil || making.Properties["composePath"] != nil || changing.Properties["stackName"] == nil || changing.Properties["composePath"] != nil {
		return errors.New("making and changing a git stack no longer declare a body without the compose file's path")
	}
	making.Properties = withFields(making.Properties, gitStackSettings())
	making.Properties["url"] = said(text(), "in place of repositoryId: the address of a repository to save with the stack")
	making.Properties["credentialId"] = said(integer(), "with url: the credential to fetch it with")
	making.Properties["repoName"] = said(text(), "with url: what to call the repository; the stack's name unless said")
	changing.Properties = withFields(changing.Properties, gitStackSettings())

	// fetching and deploying a git stack
	synced, err := answerTo(spec, http.MethodPost, "/api/git/stacks/{id}/sync")
	if err != nil {
		return err
	}
	if synced.Properties["success"] == nil || synced.Properties["commit"] != nil {
		return errors.New("POST /api/git/stacks/{id}/sync no longer declares success and an error alone")
	}
	synced.Properties["commit"] = said(text(), "the commit fetched, shortened")
	synced.Properties["newCommitFull"] = text()
	synced.Properties["updated"] = said(flag(), "whether anything differs from what was last fetched")
	synced.Properties["changedFiles"] = said(listOf(text()), "the files that differ; one line in brackets for a first fetch")
	synced.Properties["composeContent"] = said(text(), "the compose file at that commit")
	synced.Properties["composeDir"] = said(text(), "where Dockhand keeps its copy of the repository")
	synced.Properties["composeFileName"] = text()
	synced.Properties["newFiles"] = said(byName(text()), "a hash of each file at that commit, by its path")
	synced.Properties["deletionPlan"] = said(record(map[string]*openapi.Schema{
		"toDelete": said(listOf(record(map[string]*openapi.Schema{"path": text(), "hash": text()})), "the files the next deploy removes from the stack's folder, for having gone from the repository"),
		"skipped":  said(listOf(&openapi.Schema{Type: openapi.TypeObject}), "the files it would leave though they are gone from the repository"),
	}), "what the next deploy would take out of the stack's folder")
	synced.Properties["previousManifest"] = said(record(map[string]*openapi.Schema{"commit": text(), "files": byName(text())}), "what was fetched the time before")

	ran, err := answerTo(spec, http.MethodPost, "/api/git/stacks/{id}/deploy")
	if err != nil {
		return err
	}
	if ran.Type != openapi.TypeObject || len(ran.Properties) != 0 {
		return errors.New("POST /api/git/stacks/{id}/deploy no longer declares an object of nothing")
	}
	ran.Properties = map[string]*openapi.Schema{
		"success":  flag(),
		"output":   said(text(), "what compose printed"),
		"error":    text(),
		"command":  said(text(), "the compose command that was run"),
		"exitCode": integer(),
	}

	for _, method := range []string{http.MethodGet, http.MethodPost} {
		called, err := answerTo(spec, method, "/api/git/stacks/{id}/webhook")
		if err != nil {
			return err
		}
		if called.Properties["skipped"] == nil || called.Properties["output"] != nil {
			return errors.New(method + " /api/git/stacks/{id}/webhook no longer declares success, skipped and an error alone")
		}
		called.Properties["output"] = said(text(), "what compose printed, or that nothing had changed and nothing was deployed")
	}

	return nil
}
