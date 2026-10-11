package workarounds

import (
	"errors"
	"net/http"

	"github.com/katbyte/pandorest/openapi"
)

// channelConfig is how a notification channel sends, as a server sends it: for mail the server it goes through, and for the rest the addresses.
func channelConfig() *openapi.Schema {
	return record(map[string]*openapi.Schema{
		"host":       text(),
		"port":       integer(),
		"secure":     flag(),
		"username":   text(),
		"password":   said(text(), "for mail: eight asterisks where a password is kept, never the password"),
		"from_email": text(),
		"from_name":  text(),
		"to_emails":  listOf(text()),
		"urls":       said(listOf(text()), "for a channel that is not mail: the addresses it sends to, in full, with whatever token or login each carries"),
	})
}

// notificationAnswers gives the calls about notification channels the fields a server sends: a channel's whole configuration and when it was made,
// what an event type is for, and which channel a link to an environment is of.
type notificationAnswers struct{}

func (notificationAnswers) Name() string    { return Service + "-notification-answers" }
func (notificationAnswers) Service() string { return Service }

func (notificationAnswers) Bug() string {
	return "a notification channel's configuration is declared with six fields where it is read and none where it is made or changed, and a 1.0.51 server sends nine, with when the channel was made and changed beside them; an event type is declared with its id and label and is sent with what it means, its group and whether it is an environment's or the system's; and a channel's link to an environment is declared without the environment, the channel's name and kind, or whether the channel itself is switched on"
}

func (notificationAnswers) Apply(spec *openapi.Spec) error {
	for _, call := range []struct{ method, path string }{{http.MethodGet, "/api/notifications"}, {http.MethodPost, "/api/notifications"}, {http.MethodGet, "/api/notifications/{id}"}, {http.MethodPut, "/api/notifications/{id}"}} {
		answer, err := answerTo(spec, call.method, call.path)
		if err != nil {
			return err
		}
		channel := answer
		if answer.Items != nil {
			channel = answer.Items
		}
		if channel.Properties["config"] == nil || channel.Properties["createdAt"] != nil {
			return errors.New(call.method + " " + call.path + " no longer declares a channel without when it was made")
		}
		channel.Properties["config"] = channelConfig()
		channel.Properties["createdAt"] = text()
		channel.Properties["updatedAt"] = text()
	}

	events, err := answerOf(spec, "/api/notifications/trigger-test")
	if err != nil {
		return err
	}
	if events.Properties["eventTypes"] == nil || events.Properties["eventTypes"].Items == nil || events.Properties["eventTypes"].Items.Properties["scope"] != nil {
		return errors.New("GET /api/notifications/trigger-test no longer declares an event type by its id and label alone")
	}
	event := events.Properties["eventTypes"].Items
	event.Properties["description"] = text()
	event.Properties["group"] = said(text(), "container, auto_update, git_stack, stack, security or system")
	event.Properties["scope"] = said(text(), "environment for what happens on a host, which a channel is told of through its link to that environment, or system")

	for _, call := range []struct{ method, path string }{{http.MethodGet, "/api/environments/{id}/notifications"}, {http.MethodPost, "/api/environments/{id}/notifications"}, {http.MethodGet, "/api/environments/{id}/notifications/{notificationId}"}, {http.MethodPut, "/api/environments/{id}/notifications/{notificationId}"}} {
		answer, err := answerTo(spec, call.method, call.path)
		if err != nil {
			return err
		}
		link := answer
		if answer.Items != nil {
			link = answer.Items
		}
		if link.Properties["notificationId"] == nil || link.Properties["channelName"] != nil {
			return errors.New(call.method + " " + call.path + " no longer declares a link without the name of its channel")
		}
		link.Properties["environmentId"] = integer()
		link.Properties["eventTypes"] = listOf(text())
		link.Properties["channelName"] = text()
		link.Properties["channelType"] = text()
		link.Properties["channelEnabled"] = said(flag(), "whether the channel itself is switched on: a link that is on sends nothing through a channel that is off")
		link.Properties["createdAt"] = text()
		link.Properties["updatedAt"] = text()
	}

	return nil
}
