package workarounds

import (
	"errors"
	"net/http"

	pandorest "github.com/katbyte/pandorest/importer/workarounds"
	"github.com/katbyte/pandorest/openapi"
)

// userAnswers gives the calls about users and about how they sign in the fields a server sends, where the document leaves out when a user last signed
// in and how long a session lasts.
type userAnswers struct{}

func (userAnswers) Name() string    { return Service + "-user-answers" }
func (userAnswers) Service() string { return Service }

func (userAnswers) Bug() string {
	return "GET /api/users declares eight fields of a user and a 1.0.51 server sends whether MFA is set up, when the user last signed in and when it was made as well; POST /api/users declares four fields of the user it made and is answered with its display name, whether it is switched on, when it was made and whether the caller was signed in as it; and GET and PUT /api/auth/settings declare whether the login is on and its default provider, and are answered with how long a session lasts as well, which the request can set and nothing declared can read back"
}

func (userAnswers) Apply(spec *openapi.Spec) error {
	user, err := listed(spec, "/api/users", "authProvider", "mfaEnabled")
	if err != nil {
		return err
	}
	user.Properties["mfaEnabled"] = flag()
	user.Properties["lastLogin"] = said(text(), "when the user last signed in; null for one who never has")
	user.Properties["createdAt"] = text()

	// making one answers 201, which the helpers for a 200 do not read
	op, err := pandorest.Operation(spec, http.MethodPost, "/api/users")
	if err != nil {
		return err
	}
	made := op.Responses["201"]
	if made == nil || made.Content[answersJSON] == nil || made.Content[answersJSON].Schema == nil || made.Content[answersJSON].Schema.Properties["isAdmin"] == nil || made.Content[answersJSON].Schema.Properties["isActive"] != nil {
		return errors.New("POST /api/users no longer declares the user it made with four fields")
	}
	created := made.Content[answersJSON].Schema
	created.Properties["displayName"] = text()
	created.Properties["isActive"] = flag()
	created.Properties["createdAt"] = text()
	created.Properties["autoLoggedIn"] = said(flag(), "sent false for a user made by a caller that was already let in")

	for _, method := range []string{http.MethodGet, http.MethodPut} {
		settings, err := answerTo(spec, method, "/api/auth/settings")
		if err != nil {
			return err
		}
		if settings.Properties["authEnabled"] == nil || settings.Properties["sessionTimeout"] != nil {
			return errors.New(method + " /api/auth/settings no longer declares its answer without how long a session lasts")
		}
		settings.Properties["id"] = integer()
		settings.Properties["sessionTimeout"] = said(integer(), "how long a session lasts, in seconds: from 3600 to 604800 whatever was asked for, or 0 for one that never ends")
		settings.Properties["createdAt"] = text()
		settings.Properties["updatedAt"] = text()
	}

	return nil
}
