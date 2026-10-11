package workarounds

import (
	"encoding/json"
	"errors"

	"github.com/katbyte/pandorest/openapi"
)

// Shapes shared by the fixes below: what a field is, said once.
func text() *openapi.Schema    { return &openapi.Schema{Type: openapi.TypeString} }
func flag() *openapi.Schema    { return &openapi.Schema{Type: openapi.TypeBoolean} }
func integer() *openapi.Schema { return &openapi.Schema{Type: openapi.TypeInteger} }

// said is a schema with what it holds said beside it.
func said(s *openapi.Schema, description string) *openapi.Schema {
	s.Description = description

	return s
}

// listOf is a list of one kind of thing.
func listOf(item *openapi.Schema) *openapi.Schema {
	return &openapi.Schema{Type: openapi.TypeArray, Items: item}
}

// record is an object with the fields given.
func record(fields map[string]*openapi.Schema) *openapi.Schema {
	return &openapi.Schema{Type: openapi.TypeObject, Properties: fields}
}

// byName is an object whose keys are names rather than fields: labels by their name, networks by theirs.
func byName(value *openapi.Schema) *openapi.Schema {
	raw, err := json.Marshal(value)
	if err != nil {
		panic(err) // a schema made of strings and lists always marshals
	}

	return &openapi.Schema{Type: openapi.TypeObject, AdditionalProperties: raw}
}

// containerList gives the containers of a host the fields the server sends for each.
type containerList struct{}

func (containerList) Name() string    { return Service + "-container-list" }
func (containerList) Service() string { return Service }

func (containerList) Bug() string {
	return "GET /api/containers declares five fields a container, its id, name, image, state and status, and a 1.0.51 server sends fourteen it gives a value to: the command, when it was made, its health, how often it has restarted, its image's id, its labels, mounts, networks and published ports as well, and for a container that is Dockhand itself or one of its agents which of the two it is"
}

func (containerList) Apply(spec *openapi.Spec) error {
	list, err := answerOf(spec, "/api/containers")
	if err != nil {
		return err
	}
	if list.Items == nil || list.Items.Properties == nil {
		return errors.New("GET /api/containers no longer declares a list of records")
	}
	if list.Items.Properties["restartCount"] != nil {
		return errors.New("GET /api/containers now declares a container's restartCount, so the record may be whole")
	}

	list.Items.Required = nil
	list.Items.Properties = map[string]*openapi.Schema{
		"id":              said(text(), "the container's whole id, sixty-four characters"),
		"name":            text(),
		"image":           said(text(), "the image as the container was started with it: a name and tag, or a digest"),
		"imageId":         text(),
		"command":         text(),
		"created":         said(integer(), "when the container was made, in seconds since 1970"),
		"state":           said(text(), "running, exited, restarting, paused, created or dead"),
		"status":          said(text(), "Docker's own words for the state: Up 2 hours (healthy), Exited (3) 11 seconds ago"),
		"health":          said(text(), "healthy, unhealthy or starting; empty for a container with no health check"),
		"restartCount":    integer(),
		"systemContainer": said(text(), "dockhand or hawser for a container that is Dockhand itself or one of its agents, told by its image's name; nothing for any other"),
		"labels":          byName(text()),
		"mounts": listOf(record(map[string]*openapi.Schema{
			"type":        said(text(), "bind or volume"),
			"name":        said(text(), "the volume's name; empty for a bind"),
			"source":      text(),
			"destination": text(),
			"mode":        text(),
			"rw":          flag(),
		})),
		"networks": byName(record(map[string]*openapi.Schema{
			"ipAddress": said(text(), "the container's address on the network; empty while it is not running"),
		})),
		// Docker's own record of a port, capitals and all
		"ports": listOf(record(map[string]*openapi.Schema{
			"IP":          said(text(), "the address on the host the port is published on"),
			"PrivatePort": integer(),
			"PublicPort":  integer(),
			"Type":        said(text(), "tcp or udp"),
		})),
	}

	return nil
}

// stackList gives the stack list a shape, which the document gives none.
type stackList struct{}

func (stackList) Name() string    { return Service + "-stack-list" }
func (stackList) Service() string { return Service }

func (stackList) Bug() string {
	return "GET /api/stacks declares nothing of what it answers, and a 1.0.51 server sends a list of stacks, each with its name, its state, the icon it was given, its containers' ids and, for each container, the service it is, its state, health, image, ports, networks and restarts"
}

func (stackList) Apply(spec *openapi.Spec) error {
	op := spec.Operation("GET", "/api/stacks")
	if op == nil || op.Responses["200"] == nil {
		return errors.New("GET /api/stacks no longer declares a 200")
	}
	if len(op.Responses["200"].Content) > 0 {
		return errors.New("GET /api/stacks now declares what it answers")
	}
	media := &openapi.MediaType{}
	op.Responses["200"].Content = map[string]*openapi.MediaType{answersJSON: media}

	media.Schema = listOf(record(map[string]*openapi.Schema{
		"name":              text(),
		"status":            said(text(), "running, partial (some of its containers are not running) or stopped"),
		"icon":              said(text(), "the icon the stack was given; absent for one given none"),
		"containers":        said(listOf(text()), "the whole ids of the stack's containers"),
		"updatesAvailable":  flag(),
		"updateCount":       integer(),
		"newerVersionCount": integer(),
		"containerDetails": listOf(record(map[string]*openapi.Schema{
			"id":              text(),
			"name":            text(),
			"service":         said(text(), "the compose service the container is"),
			"image":           text(),
			"state":           text(),
			"status":          text(),
			"health":          said(text(), "healthy, unhealthy or starting; empty for a container with no health check"),
			"restartCount":    integer(),
			"created":         said(integer(), "when the container was made, in seconds since 1970"),
			"volumeCount":     integer(),
			"updateAvailable": flag(),
			"labels":          byName(text()),
			"networks": listOf(record(map[string]*openapi.Schema{
				"name":      text(),
				"ipAddress": text(),
			})),
			"ports": listOf(record(map[string]*openapi.Schema{
				"publicPort":  integer(),
				"privatePort": integer(),
				"type":        said(text(), "tcp or udp"),
				"display":     said(text(), "the port as a person writes it: 8080:80/tcp"),
			})),
		})),
	}))

	return nil
}
