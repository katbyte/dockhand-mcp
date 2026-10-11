package workarounds

import (
	"net/http"

	"github.com/katbyte/pandorest/openapi"
)

// containerAnswers gives the calls that measure a host's containers, look inside one and ask whether newer images are to be had the answers a server
// sends, where the document declares none.
type containerAnswers struct{}

func (containerAnswers) Name() string    { return Service + "-container-answers" }
func (containerAnswers) Service() string { return Service }

func (containerAnswers) Bug() string {
	return "what every running container of a host is using, how much each container has written, what is in a directory inside one, and a fresh check for newer images declare a 200 and nothing of what it holds, and a 1.0.51 server answers each with JSON: a list with each container's id and name beside its processor share, memory, network and disk counts; a record by container id of what it has written and what its whole filesystem takes; the path with its entries, each with its kind, size, permissions, owner and when it changed; and how many containers were checked and have a newer image, with each one's finding: whether the registry has another image under the name, why it could not be asked, whether the container is Dockhand's own or labelled to be left alone, and a newer version tag when Dockhand is set to look for one"
}

func (containerAnswers) Apply(spec *openapi.Spec) error {
	share := func() *openapi.Schema { return &openapi.Schema{Type: openapi.TypeNumber, Format: "double"} }

	stats, err := unanswered(spec, http.MethodGet, "/api/containers/stats")
	if err != nil {
		return err
	}
	answering(stats, said(listOf(record(map[string]*openapi.Schema{
		"id":            text(),
		"name":          text(),
		"cpuPercent":    said(share(), "the share of the host's processors it is using, where 100 is one whole processor"),
		"memoryUsage":   said(integer(), "the memory it holds, less what is only file cache, in bytes"),
		"memoryRaw":     said(integer(), "the memory it holds with the file cache counted, in bytes"),
		"memoryCache":   integer(),
		"memoryLimit":   said(integer(), "the most memory it may hold, in bytes: the host's own when it has no limit"),
		"memoryPercent": share(),
		"networkRx":     said(integer(), "bytes received since it started"),
		"networkTx":     integer(),
		"blockRead":     said(integer(), "bytes read from disk since it started"),
		"blockWrite":    integer(),
	})), "what each running container of the environment is using, measured as the call is made"))

	sizes, err := unanswered(spec, http.MethodGet, "/api/containers/sizes")
	if err != nil {
		return err
	}
	answering(sizes, said(byName(record(map[string]*openapi.Schema{
		"sizeRw":     said(integer(), "what the container has written over its image, in bytes"),
		"sizeRootFs": said(integer(), "what its whole filesystem takes, image included, in bytes"),
	})), "the size of each container of the environment, running or not, by its whole id"))

	files, err := unanswered(spec, http.MethodGet, "/api/containers/{id}/files")
	if err != nil {
		return err
	}
	answering(files, record(map[string]*openapi.Schema{
		"path": text(),
		"entries": listOf(record(map[string]*openapi.Schema{
			"name":        text(),
			"type":        said(text(), "file, directory or symlink"),
			"size":        integer(),
			"permissions": said(text(), "as ls writes them, without the kind: rwxr-xr-x"),
			"owner":       text(),
			"group":       text(),
			"modified":    said(text(), "when it last changed, in the container's own time and to the minute: 2006-01-02T15:04:00"),
			"linkTarget":  said(text(), "what a symlink points to"),
			"readonly":    flag(),
		})),
	}))

	checked, err := unanswered(spec, http.MethodPost, "/api/containers/check-updates")
	if err != nil {
		return err
	}
	answering(checked, record(map[string]*openapi.Schema{
		"total":        said(integer(), "how many containers were checked"),
		"updatesFound": integer(),
		"results": listOf(record(map[string]*openapi.Schema{
			"containerId":     text(),
			"containerName":   text(),
			"imageName":       text(),
			"hasUpdate":       said(flag(), "whether the registry has another image under the name than the one the container was made from"),
			"currentDigest":   text(),
			"newDigest":       text(),
			"error":           said(text(), "why the registry could not be asked, when it could not"),
			"updateDisabled":  said(flag(), "the container carries the label dockhand.update=false, and was not looked at"),
			"isLocalImage":    said(flag(), "the image was built on the host and no registry has it"),
			"systemContainer": said(text(), "dockhand or hawser for a container that is Dockhand itself or one of its agents"),
			"newerVersion":    newerVersion(),
		})),
	}))

	return nil
}
