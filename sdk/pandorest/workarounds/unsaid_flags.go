package workarounds

import (
	"errors"
	"net/http"

	"github.com/katbyte/pandorest/openapi"
)

// unsaidFlags marks the flags an answer leaves out to mean something other than no, so that the client keeps "not said" apart from false.
type unsaidFlags struct{}

func (unsaidFlags) Name() string    { return Service + "-unsaid-flags" }
func (unsaidFlags) Service() string { return Service }

func (unsaidFlags) Bug() string {
	return "POST /api/registries/test declares authenticated a plain boolean and leaves it out when it was given no user name to try, which is not the registry turning one away"
}

func (unsaidFlags) Apply(spec *openapi.Spec) error {
	tried, err := answerTo(spec, http.MethodPost, "/api/registries/test")
	if err != nil {
		return err
	}
	flag := tried.Properties["authenticated"]
	if flag == nil || flag.Type != openapi.TypeBoolean || flag.Nullable {
		return errors.New("POST /api/registries/test no longer declares authenticated a plain boolean")
	}
	flag.Nullable = true

	return nil
}
