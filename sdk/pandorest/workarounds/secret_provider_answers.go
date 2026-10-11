package workarounds

import (
	"errors"
	"net/http"

	pandorest "github.com/katbyte/pandorest/importer/workarounds"
	"github.com/katbyte/pandorest/openapi"
)

// secretProviderAnswers gives a secret provider the two dates a server sends with it, and a stack's variables the provider its secrets come from,
// where the document declares an object of nothing.
type secretProviderAnswers struct{}

func (secretProviderAnswers) Name() string    { return Service + "-secret-provider-answers" }
func (secretProviderAnswers) Service() string { return Service }

func (secretProviderAnswers) Bug() string {
	return "GET and POST /api/secret-providers and GET and PUT /api/secret-providers/{id} declare a provider by its number, name and type, and a 1.0.51 server sends when it was made and when it was last changed as well; GET /api/stacks/{name}/env declares the secret provider of the stack as an object with no fields, and answers its type and its name, or null for a stack that has none"
}

func (secretProviderAnswers) Apply(spec *openapi.Spec) error {
	for _, call := range []struct{ method, path, status string }{{http.MethodGet, "/api/secret-providers", "200"}, {http.MethodPost, "/api/secret-providers", "201"}, {http.MethodGet, "/api/secret-providers/{id}", "200"}, {http.MethodPut, "/api/secret-providers/{id}", "200"}} {
		op, err := pandorest.Operation(spec, call.method, call.path)
		if err != nil {
			return err
		}
		// making one answers 201, which the helpers for a 200 do not read
		answered := op.Responses[call.status]
		if answered == nil || answered.Content[answersJSON] == nil || answered.Content[answersJSON].Schema == nil {
			return errors.New(call.method + " " + call.path + " no longer declares what its " + call.status + " answers")
		}
		provider := answered.Content[answersJSON].Schema
		if provider.Items != nil {
			provider = provider.Items
		}
		if provider.Properties["type"] == nil || provider.Properties["createdAt"] != nil {
			return errors.New(call.method + " " + call.path + " no longer declares a provider by its number, name and type alone")
		}
		provider.Properties["createdAt"] = said(text(), "when it was made, as the server writes it")
		provider.Properties["updatedAt"] = said(text(), "when it was last changed, which is written another way once it has been")
	}

	variables, err := answerOf(spec, "/api/stacks/{name}/env")
	if err != nil {
		return err
	}
	bound := variables.Properties["secretProvider"]
	if bound == nil || bound.Type != openapi.TypeObject || len(bound.Properties) != 0 {
		return errors.New("GET /api/stacks/{name}/env no longer declares the stack's secret provider as an object of nothing")
	}
	variables.Properties["secretProvider"] = said(record(map[string]*openapi.Schema{
		"type": text(),
		"name": text(),
	}), "the secret provider the stack's secrets are loaded from when it is deployed; null for a stack that has none")

	return nil
}
