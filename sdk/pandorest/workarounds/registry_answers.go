package workarounds

import (
	"errors"
	"net/http"

	pandorest "github.com/katbyte/pandorest/importer/workarounds"
	"github.com/katbyte/pandorest/openapi"
)

// registryAnswers gives the calls about registries the fields a server sends, where the document leaves the name a registry is signed in to with out
// of its record, and declares a push answered with a job and nothing else.
type registryAnswers struct{}

func (registryAnswers) Name() string    { return Service + "-registry-answers" }
func (registryAnswers) Service() string { return Service }

func (registryAnswers) Bug() string {
	return "a registry is declared with its number, name, address, whether it is the default and whether it has credentials, and a 1.0.51 server sends the user name it signs in with and when it was made and changed as well, wherever it sends one; POST /api/images/push declares its answer a jobId, and to a caller that asks for JSON alone it answers how the push went, with the name pushed under, or why it failed"
}

func (registryAnswers) Apply(spec *openapi.Spec) error {
	for _, call := range []struct{ method, path, status string }{{http.MethodGet, "/api/registries", "200"}, {http.MethodPost, "/api/registries", "201"}, {http.MethodGet, "/api/registries/{id}", "200"}, {http.MethodPut, "/api/registries/{id}", "200"}} {
		op, err := pandorest.Operation(spec, call.method, call.path)
		if err != nil {
			return err
		}
		// making one answers 201, which the helpers for a 200 do not read
		answered := op.Responses[call.status]
		if answered == nil || answered.Content[answersJSON] == nil || answered.Content[answersJSON].Schema == nil {
			return errors.New(call.method + " " + call.path + " no longer declares what its " + call.status + " answers")
		}
		registry := answered.Content[answersJSON].Schema
		if registry.Items != nil {
			registry = registry.Items
		}
		if registry.Properties["hasCredentials"] == nil || registry.Properties["username"] != nil {
			return errors.New(call.method + " " + call.path + " no longer declares a registry without the name it signs in with")
		}
		registry.Properties["username"] = said(text(), "the user name it signs in to the registry with; null for one with no credentials")
		registry.Properties["createdAt"] = text()
		registry.Properties["updatedAt"] = text()
	}

	pushed, err := answerTo(spec, http.MethodPost, "/api/images/push")
	if err != nil {
		return err
	}
	if pushed.Properties["jobId"] == nil || pushed.Properties["targetTag"] != nil {
		return errors.New("POST /api/images/push no longer declares its answer a jobId alone")
	}
	pushed.Properties["status"] = said(text(), "complete, or error with why")
	pushed.Properties["message"] = text()
	pushed.Properties["targetTag"] = said(text(), "the whole name the image was pushed under, its registry first")
	pushed.Properties["error"] = text()

	return nil
}
