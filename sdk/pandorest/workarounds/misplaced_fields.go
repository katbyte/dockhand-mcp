package workarounds

import (
	"errors"

	pandorest "github.com/katbyte/pandorest/importer/workarounds"
	"github.com/katbyte/pandorest/openapi"
)

// vulnerabilityCountOptions puts three lists where the server sends them.
type vulnerabilityCountOptions struct{}

func (vulnerabilityCountOptions) Name() string    { return Service + "-vulnerability-count-options" }
func (vulnerabilityCountOptions) Service() string { return Service }

func (vulnerabilityCountOptions) Bug() string {
	return "GET /api/vulnerabilities/count declares the images, containers and stacks its findings can be narrowed to beside its total, and a 1.0.51 server sends the three under options"
}

func (vulnerabilityCountOptions) Apply(spec *openapi.Spec) error {
	count, err := answerOf(spec, "/api/vulnerabilities/count")
	if err != nil {
		return err
	}
	if count.Properties["options"] != nil {
		return errors.New("GET /api/vulnerabilities/count now declares its options")
	}

	options := map[string]*openapi.Schema{}
	for _, name := range []string{"images", "containers", "stacks"} {
		if count.Properties[name] == nil {
			return errors.New("GET /api/vulnerabilities/count no longer declares " + name + " beside its total")
		}
		options[name] = count.Properties[name]
		delete(count.Properties, name)
	}
	count.Properties["options"] = said(record(options), "what the findings can be narrowed to")
	count.Required = nil

	return nil
}

// oidcRedirects says that the two calls a browser is sent through to sign in elsewhere answer by sending it on.
type oidcRedirects struct{}

func (oidcRedirects) Name() string    { return Service + "-oidc-redirects" }
func (oidcRedirects) Service() string { return Service }

func (oidcRedirects) Bug() string {
	return "GET /api/auth/oidc/{id}/initiate and GET /api/auth/oidc/callback declare a 200 beside their 302, and exist to send a browser on: to the identity provider, and back from it. A 1.0.51 server answers the callback with a redirect to its login page whatever it is sent. Where it sends the browser is the answer, and following it would only fetch a page"
}

func (oidcRedirects) Apply(spec *openapi.Spec) error {
	for _, path := range []string{"/api/auth/oidc/{id}/initiate", "/api/auth/oidc/callback"} {
		op := spec.Operation("GET", path)
		if op == nil {
			return errors.New("GET " + path + " is not in the document")
		}
		if op.Responses["302"] == nil {
			return errors.New("GET " + path + " no longer declares a redirect")
		}
		if op.Responses["200"] == nil {
			return errors.New("GET " + path + " no longer declares a 200 beside its redirect")
		}
		delete(op.Responses, "200")
	}

	return nil
}

// scanExportEnv gives the export of an image's scan the environment it cannot do without.
type scanExportEnv struct{}

func (scanExportEnv) Name() string    { return Service + "-scan-export-env" }
func (scanExportEnv) Service() string { return Service }

func (scanExportEnv) Bug() string {
	return "GET /api/images/scan/export declares no env, and a 1.0.51 server with its login on refuses the call without one: env is required"
}

func (scanExportEnv) Apply(spec *openapi.Spec) error {
	return pandorest.AddParameters(spec, []string{"GET /api/images/scan/export"}, pandorest.Param{Name: "env", In: openapi.InQuery, Type: openapi.TypeInteger, Description: "the environment the image is on"})
}
