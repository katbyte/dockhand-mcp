package workarounds

import (
	"errors"
	"maps"
	"net/http"

	"github.com/katbyte/pandorest/openapi"
)

// templateAnswers gives the calls about app templates what a template holds besides what names it, on the way out and on the way back in, and a
// source of templates the fields that say whether it is Dockhand's own.
type templateAnswers struct{}

func (templateAnswers) Name() string    { return Service + "-template-answers" }
func (templateAnswers) Service() string { return Service }

func (templateAnswers) Bug() string {
	return "GET /api/templates declares what names a template and where it came from, and a 1.0.51 server sends what it would be run with as well: the repository and file of a stack, and for a container its ports, volumes, variables, restart policy and network, with a note; POST /api/templates/compose declares the template it is given by its id, type, title, image and repository, and writes the ports, volumes, variables, restart policy and network it is given into the compose it answers; GET and POST /api/templates/sources declare a source by its number, name, address and whether it is switched on, and a server sends the id it keeps it under, whether it is one of its own and where it stands in their order as well"
}

// runWith is what a container template holds of how it would be run, each time made afresh so that no two schemas share a part.
func runWith() map[string]*openapi.Schema {
	return map[string]*openapi.Schema{
		"ports":         listOf(said(text(), "a port as a compose file writes one: 8080:80/tcp")),
		"volumes":       listOf(record(map[string]*openapi.Schema{"container": text(), "bind": said(text(), "the place on the host, when the template names one")})),
		"env":           listOf(record(map[string]*openapi.Schema{"name": text(), "label": text(), "default": text()})),
		"restartPolicy": text(),
		"network":       text(),
	}
}

func (templateAnswers) Apply(spec *openapi.Spec) error {
	template, err := listed(spec, "/api/templates", "detailsUrl", "restartPolicy")
	if err != nil {
		return err
	}
	maps.Copy(template.Properties, runWith())
	template.Properties["note"] = text()
	template.Properties["repository"] = record(map[string]*openapi.Schema{"url": text(), "stackfile": text()})

	asked, err := jsonBody(spec, http.MethodPost, "/api/templates/compose")
	if err != nil {
		return err
	}
	given := asked.Properties["template"]
	if given == nil || given.Properties["image"] == nil || given.Properties["ports"] != nil {
		return errors.New("POST /api/templates/compose no longer declares the template it is given without what it is run with")
	}
	maps.Copy(given.Properties, runWith())

	source, err := listed(spec, "/api/templates/sources", "enabled", "builtin")
	if err != nil {
		return err
	}
	made, err := answerTo(spec, http.MethodPost, "/api/templates/sources")
	if err != nil {
		return err
	}
	if made.Properties["enabled"] == nil || made.Properties["builtin"] != nil {
		return errors.New("POST /api/templates/sources no longer declares the source it made without whether it is one of Dockhand's own")
	}
	for _, record := range []*openapi.Schema{source, made} {
		record.Properties["sourceId"] = said(text(), "the id Dockhand keeps the source under")
		record.Properties["builtin"] = said(flag(), "one of the sources Dockhand comes with")
		record.Properties["sortOrder"] = integer()
	}

	return nil
}
