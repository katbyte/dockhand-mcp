package workarounds

import (
	"errors"

	"github.com/katbyte/pandorest/openapi"
)

// diskAnswers gives what Docker's disk holds the shape a server sends, where the document declares an object with nothing in it.
type diskAnswers struct{}

func (diskAnswers) Name() string    { return Service + "-disk-answers" }
func (diskAnswers) Service() string { return Service }

func (diskAnswers) Bug() string {
	return "GET /api/system/disk declares diskUsage an object with nothing in it, and a 1.0.51 server sends Docker's own account of its disk there, in Docker's spelling: every image, container, volume and piece of build cache with its size and whether anything uses it"
}

func (diskAnswers) Apply(spec *openapi.Spec) error {
	answer, err := answerOf(spec, "/api/system/disk")
	if err != nil {
		return err
	}
	if answer.Properties["diskUsage"] == nil || len(answer.Properties["diskUsage"].Properties) > 0 {
		return errors.New("GET /api/system/disk no longer declares diskUsage an object with nothing in it")
	}

	answer.Properties["diskUsage"] = said(record(map[string]*openapi.Schema{
		"LayersSize": said(integer(), "what the images take together, the layers they share counted once, in bytes"),
		"Images": listOf(record(map[string]*openapi.Schema{
			"Id":         text(),
			"RepoTags":   listOf(text()),
			"Size":       said(integer(), "the image's size in bytes, shared layers included"),
			"SharedSize": said(integer(), "how much of it other images share; -1 where Docker did not work it out"),
			"Containers": said(integer(), "how many containers are made from it, running or not"),
			"Created":    said(integer(), "when it was built, in seconds since 1970"),
		})),
		"Containers": listOf(record(map[string]*openapi.Schema{
			"Id":         text(),
			"Names":      listOf(text()),
			"Image":      text(),
			"SizeRw":     said(integer(), "what it has written inside itself, in bytes; absent for one that has written nothing"),
			"SizeRootFs": said(integer(), "what its whole filesystem takes, its image included, in bytes"),
			"State":      text(),
			"Status":     text(),
		})),
		"Volumes": listOf(record(map[string]*openapi.Schema{
			"Name": text(),
			"UsageData": record(map[string]*openapi.Schema{
				"Size":     said(integer(), "what the volume holds, in bytes; -1 where Docker cannot tell"),
				"RefCount": said(integer(), "how many containers mount it"),
			}),
		})),
		"BuildCache": listOf(record(map[string]*openapi.Schema{
			"ID":          text(),
			"Type":        text(),
			"Description": text(),
			"InUse":       flag(),
			"Shared":      flag(),
			"Size":        said(integer(), "in bytes"),
			"LastUsedAt":  text(),
		})),
	}), "Docker's own account of what its disk holds, in Docker's spelling; null when Dockhand could not get it from the host in time")

	return nil
}
