package workarounds

import (
	"errors"
	"fmt"
	"net/http"

	pandorest "github.com/katbyte/pandorest/importer/workarounds"
	"github.com/katbyte/pandorest/openapi"
)

// unanswered is the 200 of a call the document gives no answer, to give it one: a call that has since been given an answer is refused, so a fix does
// not write over what the document now says.
func unanswered(spec *openapi.Spec, method, path string) (*openapi.Response, error) {
	op, err := pandorest.Operation(spec, method, path)
	if err != nil {
		return nil, err
	}
	ok := op.Responses["200"]
	if ok == nil {
		return nil, fmt.Errorf("%s %s no longer declares a 200", method, path)
	}
	if len(ok.Content) > 0 {
		return nil, fmt.Errorf("%s %s now declares what it answers", method, path)
	}

	return ok, nil
}

// answering gives a 200 a JSON answer of one shape.
func answering(ok *openapi.Response, shape *openapi.Schema) {
	ok.Content = map[string]*openapi.MediaType{answersJSON: {Schema: shape}}
}

// listed is the record a GET that answers a list declares for each thing in it, which must still be spelled with the field given and not yet hold the
// other: how a fix tells the list it was written for from one the document has since put right.
func listed(spec *openapi.Spec, path, declares, lacks string) (*openapi.Schema, error) {
	list, err := answerOf(spec, path)
	if err != nil {
		return nil, err
	}
	if list.Items == nil || list.Items.Properties == nil {
		return nil, fmt.Errorf("GET %s no longer declares a list of records", path)
	}
	if list.Items.Properties[declares] == nil {
		return nil, fmt.Errorf("GET %s no longer declares %s, so its record may be spelled as the server spells it", path, declares)
	}
	if list.Items.Properties[lacks] != nil {
		return nil, fmt.Errorf("GET %s now declares %s, so its record may be whole", path, lacks)
	}

	return list.Items, nil
}

// imageAnswers gives the images of a host the fields the server sends, their history its tags, and pulling one an answer.
type imageAnswers struct{}

func (imageAnswers) Name() string    { return Service + "-image-answers" }
func (imageAnswers) Service() string { return Service }

func (imageAnswers) Bug() string {
	return "GET /api/images declares four fields an image, spelled as Docker spells them (Id, RepoTags, Size, Created), and a 1.0.51 server sends nine spelled its own way: id, repoTags and the same again as tags, repoDigests, size, virtualSize, created, labels and how many containers use it. The history of an image leaves out the tags each layer carries. Pulling an image declares no answer, and to a caller that asks for JSON alone the server pulls first and answers a status, complete or error, with why in error and HTTP 200 either way; any other caller is answered the id of a job at once"
}

func (imageAnswers) Apply(spec *openapi.Spec) error {
	image, err := listed(spec, "/api/images", "RepoTags", "repoDigests")
	if err != nil {
		return err
	}
	image.Required = nil
	image.Properties = map[string]*openapi.Schema{
		"id":          said(text(), "the image's whole id: sha256: and sixty-four characters"),
		"repoTags":    said(listOf(text()), "every name and tag the image goes by on the host; none for an image left behind by a newer one of the same name"),
		"tags":        said(listOf(text()), "the same again"),
		"repoDigests": said(listOf(text()), "the image by the digest it was pulled as, for each repository it came from"),
		"size":        said(integer(), "bytes"),
		"virtualSize": integer(),
		"created":     said(integer(), "when the image was built, in seconds since 1970"),
		"labels":      byName(text()),
		"containers":  said(integer(), "how many containers on the host were made from it, running or not"),
	}

	history, err := answerOf(spec, "/api/images/{id}/history")
	if err != nil {
		return err
	}
	if history.Items == nil || history.Items.Properties["CreatedBy"] == nil {
		return errors.New("GET /api/images/{id}/history no longer declares a list of layers")
	}
	if history.Items.Properties["Tags"] != nil {
		return errors.New("GET /api/images/{id}/history now declares a layer's tags")
	}
	history.Items.Properties["Tags"] = said(listOf(text()), "the names and tags that point at this layer; none for a layer beneath the top one")

	pull, err := unanswered(spec, http.MethodPost, "/api/images/pull")
	if err != nil {
		return err
	}
	answering(pull, record(map[string]*openapi.Schema{
		"status": said(text(), "complete, or error; an error comes with HTTP 200 all the same"),
		"error":  said(text(), "why not, when the image could not be pulled"),
		"jobId":  said(text(), "the job the pull runs as, for a caller that did not ask for JSON alone and so was not made to wait: GET /api/jobs/{id} says how it goes"),
	}))

	return nil
}

// volumeAnswers gives the volumes of a host the fields the server sends.
type volumeAnswers struct{}

func (volumeAnswers) Name() string    { return Service + "-volume-answers" }
func (volumeAnswers) Service() string { return Service }

func (volumeAnswers) Bug() string {
	return "GET /api/volumes declares five fields a volume, spelled as Docker spells them (Name, Driver, Mountpoint, Scope, CreatedAt), and a 1.0.51 server sends seven spelled its own way: name, driver, mountpoint, scope, created, labels and the containers that use it. Reading one volume declares its labels and options as objects of nothing, where they are texts by name, and a file in a volume leaves out whether it can be written"
}

func (volumeAnswers) Apply(spec *openapi.Spec) error {
	volume, err := listed(spec, "/api/volumes", "CreatedAt", "usedBy")
	if err != nil {
		return err
	}
	volume.Required = nil
	volume.Properties = map[string]*openapi.Schema{
		"name":       text(),
		"driver":     said(text(), "local, for a volume kept on the host's own disk"),
		"mountpoint": said(text(), "where the volume's files are on the host"),
		"scope":      text(),
		"created":    said(text(), "when the volume was made, as 2006-01-02T15:04:05Z"),
		"labels":     byName(text()),
		"usedBy": said(listOf(record(map[string]*openapi.Schema{
			"containerId":   text(),
			"containerName": text(),
		})), "the containers that mount the volume, running or not; none for a volume nothing uses"),
	}

	for _, path := range []string{"/api/volumes/{name}", "/api/volumes/{name}/inspect"} {
		one, missing := answerOf(spec, path)
		if missing != nil {
			return missing
		}
		for _, field := range []string{"Labels", "Options"} {
			if one.Properties[field] == nil || len(one.Properties[field].Properties) > 0 || len(one.Properties[field].AdditionalProperties) > 0 {
				return fmt.Errorf("GET %s no longer declares %s as an object of nothing", path, field)
			}
			one.Properties[field] = byName(text())
		}
	}

	browse, err := answerOf(spec, "/api/volumes/{name}/browse")
	if err != nil {
		return err
	}
	entries := browse.Properties["entries"]
	if entries == nil || entries.Items == nil || entries.Items.Properties["permissions"] == nil {
		return errors.New("GET /api/volumes/{name}/browse no longer declares a list of entries")
	}
	if entries.Items.Properties["readonly"] != nil {
		return errors.New("GET /api/volumes/{name}/browse now declares whether an entry can be written")
	}
	entries.Items.Properties["readonly"] = said(flag(), "whether the volume was opened for reading alone, as it is while a container uses it")

	return nil
}

// networkAnswers gives the networks of a host, and one read on its own, the fields the server sends.
type networkAnswers struct{}

func (networkAnswers) Name() string    { return Service + "-network-answers" }
func (networkAnswers) Service() string { return Service }

func (networkAnswers) Bug() string {
	return "GET /api/networks declares six fields a network, spelled as Docker spells them (Id, Name, Driver, Scope, Internal, Attachable), and a 1.0.51 server sends seven spelled its own way: id, name, driver, scope, internal, its address ranges under ipam and the containers on it by their id. Reading one network, which is Docker's own record of it, declares six of its fields with its address ranges and containers as objects of nothing, and leaves out when it was made, whether it is internal, attachable or carries IPv6, its labels and its options"
}

func (networkAnswers) Apply(spec *openapi.Spec) error {
	network, err := listed(spec, "/api/networks", "Attachable", "ipam")
	if err != nil {
		return err
	}
	network.Required = nil
	network.Properties = map[string]*openapi.Schema{
		"id":       said(text(), "the network's whole id, sixty-four characters"),
		"name":     text(),
		"driver":   said(text(), "bridge for a network of the host's own, host and null for the two Docker always has, overlay for one a swarm shares"),
		"scope":    text(),
		"internal": said(flag(), "whether the network is cut off from everything outside the host"),
		"ipam": record(map[string]*openapi.Schema{
			"driver": text(),
			"config": listOf(record(map[string]*openapi.Schema{
				"subnet":  text(),
				"gateway": text(),
			})),
		}),
		"containers": said(byName(record(map[string]*openapi.Schema{
			"name":        text(),
			"ipv4Address": said(text(), "the container's address on the network; empty while it is not running"),
		})), "the containers on the network, by their whole id"),
	}

	for _, path := range []string{"/api/networks/{id}", "/api/networks/{id}/inspect"} {
		one, err := answerOf(spec, path)
		if err != nil {
			return err
		}
		if one.Properties["IPAM"] == nil || one.Properties["Containers"] == nil {
			return fmt.Errorf("GET %s no longer declares a network's address ranges and containers", path)
		}
		if one.Properties["Created"] != nil || len(one.Properties["IPAM"].Properties) > 0 {
			return fmt.Errorf("GET %s now declares more of a network, so its record may be whole", path)
		}

		one.Properties["Created"] = said(text(), "when the network was made, as 2006-01-02T15:04:05.999999999Z")
		one.Properties["EnableIPv4"] = flag()
		one.Properties["EnableIPv6"] = flag()
		one.Properties["Internal"] = flag()
		one.Properties["Attachable"] = flag()
		one.Properties["Ingress"] = flag()
		one.Properties["Labels"] = byName(text())
		one.Properties["Options"] = byName(text())
		one.Properties["IPAM"] = record(map[string]*openapi.Schema{
			"Driver": text(),
			"Config": listOf(record(map[string]*openapi.Schema{
				"Subnet":  text(),
				"Gateway": text(),
			})),
		})
		one.Properties["Containers"] = said(byName(record(map[string]*openapi.Schema{
			"Name":        text(),
			"EndpointID":  text(),
			"MacAddress":  text(),
			"IPv4Address": said(text(), "the container's address on the network with the size of the range after it: 172.19.0.2/16"),
			"IPv6Address": text(),
		})), "the running containers on the network, by their whole id")
	}

	return nil
}

// pruneAnswers gives the five calls that clear out what a host no longer uses an answer.
type pruneAnswers struct{}

func (pruneAnswers) Name() string    { return Service + "-prune-answers" }
func (pruneAnswers) Service() string { return Service }

func (pruneAnswers) Bug() string {
	return "the five calls that prune a host, its stopped containers, unused images, volumes and networks and all four at once, declare no answer, and a 1.0.51 server asked for JSON alone answers success with Docker's own report of what went under result: the ids or names deleted and the bytes got back, and for all four at once one such report under each of containers, images, volumes and networks. Pruning images alone is run as a job for any caller that does not ask for JSON alone, who is answered the job's id at once, and alone answers a failure with HTTP 200, success false and why in error"
}

func (pruneAnswers) Apply(spec *openapi.Spec) error {
	// Docker's own report of each kind, capitals and all; a kind nothing of which went answers null for its list
	reclaimed := func() *openapi.Schema { return said(integer(), "the bytes got back") }
	reports := map[string]func() *openapi.Schema{
		"containers": func() *openapi.Schema {
			return record(map[string]*openapi.Schema{"ContainersDeleted": said(listOf(text()), "the whole ids of the containers removed"), "SpaceReclaimed": reclaimed()})
		},
		"images": func() *openapi.Schema {
			return record(map[string]*openapi.Schema{"ImagesDeleted": said(listOf(record(map[string]*openapi.Schema{
				"Untagged": said(text(), "a name and tag that was taken off an image"),
				"Deleted":  said(text(), "the id of an image or of a layer that was deleted"),
			})), "each name taken off and each image and layer deleted, one to an entry"), "SpaceReclaimed": reclaimed()})
		},
		"volumes": func() *openapi.Schema {
			return record(map[string]*openapi.Schema{"VolumesDeleted": said(listOf(text()), "the names of the volumes removed"), "SpaceReclaimed": reclaimed()})
		},
		"networks": func() *openapi.Schema {
			return record(map[string]*openapi.Schema{"NetworksDeleted": said(listOf(text()), "the names of the networks removed")})
		},
	}

	for kind, report := range reports {
		ok, err := unanswered(spec, http.MethodPost, "/api/prune/"+kind)
		if err != nil {
			return err
		}
		answer := map[string]*openapi.Schema{
			"success": flag(),
			"result":  report(),
		}
		if kind == "images" {
			answer["error"] = said(text(), "why not, when success is false: it comes with HTTP 200 all the same")
			answer["jobId"] = said(text(), "the job the pruning runs as, for a caller that did not ask for JSON alone and so was not made to wait: GET /api/jobs/{id} says how it goes")
		}
		answering(ok, record(answer))
	}

	all, err := unanswered(spec, http.MethodPost, "/api/prune/all")
	if err != nil {
		return err
	}
	every := map[string]*openapi.Schema{}
	for kind, report := range reports {
		every[kind] = report()
	}
	answering(all, record(map[string]*openapi.Schema{
		"success": flag(),
		"result":  record(every),
	}))

	return nil
}
