package workarounds

import (
	"fmt"
	"net/http"

	pandorest "github.com/katbyte/pandorest/importer/workarounds"
	"github.com/katbyte/pandorest/openapi"
)

// stackActionAnswers says what starting, stopping, restarting, deploying and taking down a stack answer a caller that asks for JSON, which is how
// this client asks for everything.
type stackActionAnswers struct{}

func (stackActionAnswers) Name() string    { return Service + "-stack-action-answers" }
func (stackActionAnswers) Service() string { return Service }

func (stackActionAnswers) Bug() string {
	return "the five calls that act on a stack answer a job's id to a caller that will take anything, and to one that asks for JSON alone they do the work first and answer how it went: success, with what was done in output or why not in error, all with HTTP 200. Starting, stopping and taking down declare the job's id alone; restarting and deploying declare nothing and describe a stream of events"
}

// stackActions is each call that acts on a stack, and whether the document declares an answer for it at all.
var stackActions = map[string]bool{
	"start":   true,
	"stop":    true,
	"down":    true,
	"restart": false,
	"deploy":  false,
}

func (stackActionAnswers) Apply(spec *openapi.Spec) error {
	for action, declared := range stackActions {
		path := "/api/stacks/{name}/" + action
		op, err := pandorest.Operation(spec, http.MethodPost, path)
		if err != nil {
			return err
		}
		ok := op.Responses["200"]
		if ok == nil {
			return fmt.Errorf("POST %s no longer declares a 200", path)
		}

		switch media := ok.Content[answersJSON]; {
		case declared && (media == nil || media.Schema == nil || media.Schema.Properties["jobId"] == nil):
			return fmt.Errorf("POST %s no longer declares a job's id for its answer", path)
		case declared && media.Schema.Properties["success"] != nil:
			return fmt.Errorf("POST %s now declares how the action went", path)
		case !declared && len(ok.Content) > 0:
			return fmt.Errorf("POST %s now declares what it answers", path)
		default:
		}

		ok.Content = map[string]*openapi.MediaType{answersJSON: {Schema: record(map[string]*openapi.Schema{
			"success": said(flag(), "whether the action did what was asked; false comes with HTTP 200 all the same"),
			"output":  said(text(), "what was done, when it succeeded"),
			"error":   said(text(), "why not, when it did not"),
			"jobId":   said(text(), "the job the action runs as, for a caller that did not ask for JSON alone and so was not made to wait: GET /api/jobs/{id} says how it went"),
		})}}
	}

	return nil
}
