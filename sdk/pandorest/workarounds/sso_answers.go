package workarounds

import (
	"errors"
	"net/http"

	pandorest "github.com/katbyte/pandorest/importer/workarounds"
	"github.com/katbyte/pandorest/openapi"
)

// ssoAnswers gives a single sign-on provider the fields a server sends, where the document declares the five that name it.
type ssoAnswers struct{}

func (ssoAnswers) Name() string    { return Service + "-sso-answers" }
func (ssoAnswers) Service() string { return Service }

func (ssoAnswers) Bug() string {
	return "an OIDC provider is declared with its number, name, whether it is switched on, its issuer and its client id, wherever one is answered, and a 1.0.51 server sends the rest of what was saved as well: where it sends a user back to, its scopes, the claims it names for a user's name, email and display name, for an administrator and for roles, when it was made and changed, and a mask in place of its client secret"
}

func (ssoAnswers) Apply(spec *openapi.Spec) error {
	for _, call := range []struct{ method, path, status string }{{http.MethodGet, "/api/auth/oidc", "200"}, {http.MethodPost, "/api/auth/oidc", "201"}, {http.MethodGet, "/api/auth/oidc/{id}", "200"}, {http.MethodPut, "/api/auth/oidc/{id}", "200"}} {
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
		if provider.Properties["clientId"] == nil || provider.Properties["clientSecret"] != nil {
			return errors.New(call.method + " " + call.path + " no longer declares a provider by the five fields that name it")
		}

		provider.Properties["clientSecret"] = said(text(), "eight asterisks, never the secret")
		provider.Properties["redirectUri"] = text()
		provider.Properties["scopes"] = said(text(), "the scopes named, with spaces between: openid profile email unless given")
		provider.Properties["usernameClaim"] = text()
		provider.Properties["emailClaim"] = text()
		provider.Properties["displayNameClaim"] = text()
		provider.Properties["adminClaim"] = said(text(), "the claim named for an administrator, with adminValue; null when none is set")
		provider.Properties["adminValue"] = text()
		provider.Properties["roleMappingsClaim"] = text()
		provider.Properties["createdAt"] = text()
		provider.Properties["updatedAt"] = text()
	}

	return nil
}
