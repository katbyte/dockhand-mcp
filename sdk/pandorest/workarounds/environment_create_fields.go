package workarounds

import (
	"errors"
	"net/http"

	pandorest "github.com/katbyte/pandorest/importer/workarounds"
	"github.com/katbyte/pandorest/openapi"
)

// environmentCreateFields gives creating an environment the fields the document declares only for updating one.
type environmentCreateFields struct{}

func (environmentCreateFields) Name() string    { return Service + "-environment-create-fields" }
func (environmentCreateFields) Service() string { return Service }

func (environmentCreateFields) Bug() string {
	return "POST /api/environments declares nine fields and takes every one PUT /api/environments/{id} does: an agent's token (hawserToken), the TLS settings and what is collected were all kept by a 1.0.51 server, and without the token an environment behind a Hawser agent cannot be made at all"
}

func (environmentCreateFields) Apply(spec *openapi.Spec) error {
	create, err := jsonBody(spec, http.MethodPost, "/api/environments")
	if err != nil {
		return err
	}
	update, err := jsonBody(spec, http.MethodPut, "/api/environments/{id}")
	if err != nil {
		return err
	}

	added := 0
	for name, property := range update.Properties {
		if _, declared := create.Properties[name]; declared {
			continue
		}
		create.Properties[name] = property
		added++
	}
	if added == 0 {
		return errors.New("POST /api/environments now declares every field the update does")
	}

	return nil
}

// jsonBody is the schema of an operation's JSON request body, or the error a workaround returns when the document no longer declares one with fields.
func jsonBody(spec *openapi.Spec, method, path string) (*openapi.Schema, error) {
	op, err := pandorest.Operation(spec, method, path)
	if err != nil {
		return nil, err
	}
	if op.RequestBody == nil || op.RequestBody.Content["application/json"] == nil || op.RequestBody.Content["application/json"].Schema == nil || op.RequestBody.Content["application/json"].Schema.Properties == nil {
		return nil, errors.New(method + " " + path + " no longer declares a JSON body with fields")
	}

	return op.RequestBody.Content["application/json"].Schema, nil
}
