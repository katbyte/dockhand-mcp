package workarounds

import (
	"errors"
	"fmt"
	"net/http"

	"github.com/katbyte/pandorest/openapi"
)

// stackAnswers gives the calls that read and write one stack, and the one that looks for compose files to adopt, the answers a server sends, where
// the document declares none, leaves the middle out or has the wrong type.
type stackAnswers struct{}

func (stackAnswers) Name() string    { return Service + "-stack-answers" }
func (stackAnswers) Service() string { return Service }

func (stackAnswers) Bug() string {
	return "reading a stack's compose file, listing where each stack's files are kept, making a stack, saving its compose file and saving its variables declare no answer at all, and a 1.0.51 server answers each with JSON: the file's text with the paths it and its .env are kept at; a record by stack name of how the stack is kept and where; success with whether it was started and what compose printed, or the job it runs as; success, or the job; and success with how many variables were sent. A deploy's record declares its details as an object of nothing, where a server sends the options it ran with, what it made and started, its exit code and the name of its log. Looking through a directory for compose files declares each one found as a name alone and each one skipped as text, where a server sends for both a record of the name, the compose file, the .env beside it and how many services it has, which as text cannot be read at all; and checking a path does not declare the path it resolved"
}

func (stackAnswers) Apply(spec *openapi.Spec) error {
	compose, err := unanswered(spec, http.MethodGet, "/api/stacks/{name}/compose")
	if err != nil {
		return err
	}
	answering(compose, record(map[string]*openapi.Schema{
		"content":          said(text(), "the compose file's text, as it is kept"),
		"stackDir":         said(text(), "the directory the stack's files are kept in, as Dockhand's own container sees it"),
		"composePath":      text(),
		"envPath":          said(text(), "where the stack's .env file is kept; absent when it has none"),
		"suggestedEnvPath": said(text(), "where a .env file would be kept if the stack were given one"),
	}))

	sources, err := unanswered(spec, http.MethodGet, "/api/stacks/sources")
	if err != nil {
		return err
	}
	answering(sources, said(byName(record(map[string]*openapi.Schema{
		"sourceType":       said(text(), "internal for a stack Dockhand has the compose file of, wherever the file lies, git for one deployed from a repository, and external for one it keeps a record of with no file, as a stack it only found is once it is given an icon"),
		"composePath":      text(),
		"secretProviderId": integer(),
		"icon":             text(),
		"envVarCount":      said(integer(), "how many variables the stack is given: the ones in its .env file and the ones Dockhand keeps as secrets"),
	})), "how each stack Dockhand has a record of is kept, by the stack's name; a stack it only found running on the host is not here"))

	made, err := unanswered(spec, http.MethodPost, "/api/stacks")
	if err != nil {
		return err
	}
	answering(made, record(map[string]*openapi.Schema{
		"success": flag(),
		"started": said(flag(), "whether the stack was brought up as well as made"),
		"output":  said(text(), "what compose printed bringing it up, when it was"),
		"error":   said(text(), "why not, when it was made and could not be brought up"),
		"jobId":   said(text(), "the job the stack is made and brought up as, for a caller that did not ask for JSON alone and so was not made to wait: GET /api/jobs/{id} says how it went"),
	}))

	saved, err := unanswered(spec, http.MethodPut, "/api/stacks/{name}/compose")
	if err != nil {
		return err
	}
	answering(saved, record(map[string]*openapi.Schema{
		"success": flag(),
		"output":  said(text(), "what compose printed, when the stack was brought up again with the file"),
		"error":   said(text(), "why not, when the file was saved and the stack could not be brought up again"),
		"jobId":   said(text(), "the job the file is saved and the stack brought up again as, for a caller that did not ask for JSON alone"),
	}))

	variables, err := unanswered(spec, http.MethodPut, "/api/stacks/{name}/env")
	if err != nil {
		return err
	}
	answering(variables, record(map[string]*openapi.Schema{
		"success": flag(),
		"count":   said(integer(), "how many variables were sent"),
	}))

	for _, path := range []string{"/api/stacks/{name}/deploys", "/api/stacks/{name}/deploys/{runId}"} {
		run, failed := answerOf(spec, path)
		if failed != nil {
			return failed
		}
		if runs := run.Properties["runs"]; runs != nil {
			run = runs.Items
		}
		if run == nil || run.Properties["details"] == nil {
			return fmt.Errorf("GET %s no longer declares a deploy's details", path)
		}
		if len(run.Properties["details"].Properties) > 0 {
			return errors.New("GET " + path + " now declares what a deploy's details hold")
		}
		run.Properties["details"] = record(map[string]*openapi.Schema{
			"options": record(map[string]*openapi.Schema{
				"pull":          flag(),
				"build":         flag(),
				"forceRecreate": flag(),
			}),
			"summary": record(map[string]*openapi.Schema{
				"containersCreated":   integer(),
				"containersRecreated": integer(),
				"containersStarted":   integer(),
				"containerNames":      listOf(text()),
				"imagesBuilt":         listOf(text()),
				"imagesPulled":        listOf(text()),
				"buildSteps":          integer(),
				"buildStepsCached":    integer(),
			}),
			"exitCode":    integer(),
			"composeHash": said(text(), "a digest of the compose file the deploy ran with: two deploys with the same one ran the same file"),
			"envHash":     text(),
			"logFile":     text(),
		})
	}

	scan, err := answerTo(spec, http.MethodPost, "/api/stacks/scan")
	if err != nil {
		return err
	}
	discovered, skipped := scan.Properties["discovered"], scan.Properties["skipped"]
	if discovered == nil || discovered.Items == nil || skipped == nil || skipped.Items == nil {
		return errors.New("POST /api/stacks/scan no longer declares what it discovered and skipped as lists")
	}
	if discovered.Items.Properties["composePath"] != nil || skipped.Items.Type != openapi.TypeString {
		return errors.New("POST /api/stacks/scan now declares where a compose file it found is, or what it skipped as more than a name")
	}
	// a new one each time it is asked for: withFields adds to the one it is given
	found := func() map[string]*openapi.Schema {
		return map[string]*openapi.Schema{
			"name":         said(text(), "the name the stack would have: the folder's"),
			"composePath":  text(),
			"envPath":      said(text(), "the .env file beside it, when there is one"),
			"sourceDir":    text(),
			"serviceCount": integer(),
		}
	}
	discovered.Items = record(withFields(found(), map[string]*openapi.Schema{
		"runningOn": said(listOf(record(map[string]*openapi.Schema{
			"envId":          integer(),
			"envName":        text(),
			"containerCount": integer(),
		})), "the environments a compose project of that name has containers on already"),
		"unadoptable": flag(),
	}))
	skipped.Description = "the compose files found that are stacks Dockhand has already"
	skipped.Items = record(found())

	checked, err := answerTo(spec, http.MethodPost, "/api/stacks/validate-path")
	if err != nil {
		return err
	}
	if checked.Properties["resolvedPath"] != nil {
		return errors.New("POST /api/stacks/validate-path now declares the path it resolved")
	}
	checked.Properties["resolvedPath"] = said(text(), "the path as Dockhand resolved it, when it is one that can be used")

	return nil
}
