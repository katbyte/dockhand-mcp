package workarounds

import (
	"errors"
	"net/http"

	"github.com/katbyte/pandorest/openapi"
)

// tagAnswers gives a tag the icon a server sends with it, and the two calls that say what every container and every stack of an environment is tagged
// with an answer, where the document declares none.
type tagAnswers struct{}

func (tagAnswers) Name() string    { return Service + "-tag-answers" }
func (tagAnswers) Service() string { return Service }

func (tagAnswers) Bug() string {
	return "GET /api/tags declares a tag by its number, name and colour, and a 1.0.51 server sends its icon as well, which the calls that make and change one take; GET /api/container-tags and GET /api/stack-tags declare no answer, and each answers the numbers of the tags every container, or every stack, of the environment is given, by the container's or the stack's name"
}

func (tagAnswers) Apply(spec *openapi.Spec) error {
	catalogue, err := answerOf(spec, "/api/tags")
	if err != nil {
		return err
	}
	tags := catalogue.Properties["tags"]
	if tags == nil || tags.Items == nil || tags.Items.Properties["color"] == nil || tags.Items.Properties["icon"] != nil {
		return errors.New("GET /api/tags no longer declares a tag without its icon")
	}
	tags.Items.Properties["icon"] = said(text(), "null for a tag given none")

	for _, path := range []string{"/api/container-tags", "/api/stack-tags"} {
		given, err := unanswered(spec, http.MethodGet, path)
		if err != nil {
			return err
		}
		answering(given, byName(listOf(integer())))
	}

	return nil
}
