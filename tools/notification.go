package tools

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"net"
	"slices"
	"strconv"
	"strings"

	"github.com/katbyte/dockhand-mcp/sdk/dockhand"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

const (
	// channelMail is the kind of channel that sends mail through a mail server, and channelOther the kind that sends to an address of another service
	channelMail  = "smtp"
	channelOther = "apprise"
	// defaultMailPort is the port mail is handed in on unless another is given
	defaultMailPort = 587
	// scopeHost is the scope of an event that happens on a host, as Dockhand names it
	scopeHost = "environment"
	// eventLicense is the one event of Dockhand's own that it sets off for a test: for the rest it asks for an environment and sends through that
	// environment's ties, which hold no event of its own, so they reach nobody
	eventLicense = "license_expiring"
)

var (
	// channelSchemes is the services a channel that is not mail can send to, by how their addresses begin
	channelSchemes = []string{"json", "jsons", "ntfy", "ntfys", "gotify", "gotifys", "discord", "slack", "tgram", "pushover", "pover", "mmost", "mmosts", "signal", "workflows", "apprise", "apprises"}
	// hostSchemes is the ones whose address names a host first: in the rest what comes first is itself a token
	hostSchemes = []string{"json", "jsons", "ntfy", "ntfys", "gotify", "gotifys", "mmost", "mmosts", "signal", "workflows", "apprise", "apprises"}
)

// shortAddress is an address a channel sends to with whatever token or login it carries left out: the service, and the host where the address names
// one. An address is a secret nearly whole: its token is what lets anyone send to it.
func shortAddress(address string) string {
	scheme, rest, cut := strings.Cut(strings.TrimSpace(address), "://")
	if !cut {
		return "…"
	}
	if !slices.Contains(hostSchemes, strings.ToLower(scheme)) {
		return scheme + "://…"
	}
	host, more, _ := strings.Cut(rest, "/")
	if at := strings.LastIndex(host, "@"); at >= 0 {
		host, more = host[at+1:], "login"
	}
	if q := strings.IndexAny(host, "?#"); q >= 0 {
		host, more = host[:q], "query"
	}
	if more != "" {
		return scheme + "://" + host + "/…"
	}

	return scheme + "://" + host
}

// channelArg names one notification channel the Dockhand keeps.
type channelArg struct {
	Channel any `json:"channel,omitempty" jsonschema:"the notification channel, by its name or its number as notification_list gives them"`
}

// named is the channel an argument names, as text.
func (a channelArg) named() (string, error) {
	switch given := a.Channel.(type) {
	case nil:
		return "", nil
	case string:
		return strings.TrimSpace(given), nil
	case float64:
		if given != float64(int(given)) {
			return "", fmt.Errorf("channel is %v: a channel's number is a whole one", given)
		}

		return strconv.Itoa(int(given)), nil
	default:
		return "", fmt.Errorf("channel is %v: it is a channel's name or its number", given)
	}
}

// channels is every notification channel the Dockhand keeps.
func (r *registry) channels(ctx context.Context) ([]dockhand.GetNotificationsResponse, error) {
	listed, err := r.dock.GetNotifications(ctx)
	if err != nil {
		return nil, fmt.Errorf("the notification channels could not be listed: %w", refusal(err))
	}

	return listed.Model, nil
}

// oneChannel is the single channel a call names, by its name whatever its case or by its number. Dockhand lets two channels share a name, and which
// of them is meant is not guessed at.
func (r *registry) oneChannel(ctx context.Context, a channelArg) (*dockhand.GetNotificationsResponse, error) {
	named, err := a.named()
	if err != nil {
		return nil, err
	}
	if named == "" {
		return nil, errors.New("channel is required: the notification channel's name or number, as notification_list gives them")
	}
	listed, err := r.channels(ctx)
	if err != nil {
		return nil, err
	}
	var found []*dockhand.GetNotificationsResponse
	names := make([]string, 0, len(listed))
	for i := range listed {
		if strconv.Itoa(listed[i].Id) == named {
			return &listed[i], nil
		}
		if strings.EqualFold(listed[i].Name, named) {
			found = append(found, &listed[i])
		}
		names = append(names, listed[i].Name)
	}
	switch len(found) {
	case 1:
		return found[0], nil
	case 0:
		return nil, fmt.Errorf("no notification channel is named or numbered %q: Dockhand keeps %s", named, cmp.Or(strings.Join(names, ", "), "none"))
	default:
		numbers := make([]string, 0, len(found))
		for _, k := range found {
			numbers = append(numbers, strconv.Itoa(k.Id))
		}

		return nil, fmt.Errorf("%d notification channels are named %q, numbered %s: say which by its number", len(found), named, strings.Join(numbers, " and "))
	}
}

// eventType is one thing a channel can be told of.
type eventType struct {
	ID    string `json:"id"              jsonschema:"what the event is called where events are chosen"`
	Means string `json:"means,omitempty" jsonschema:"when it happens"`
	Group string `json:"group,omitempty" jsonschema:"container, auto_update, git_stack, stack, security or system"`
	Scope string `json:"scope,omitempty" jsonschema:"environment for what happens on a host, which a channel is told of only through a link to that environment; system for what happens to Dockhand itself, which a channel is told of by its own events"`
}

// eventTypes is every event a channel can be told of, as Dockhand lists them.
func (r *registry) eventTypes(ctx context.Context) ([]eventType, error) {
	listed, err := r.dock.GetNotificationsTriggerTest(ctx)
	if err != nil {
		return nil, fmt.Errorf("the events a channel can be told of could not be listed: %w", refusal(err))
	}
	out := make([]eventType, 0, len(listed.Model.EventTypes))
	for i := range listed.Model.EventTypes {
		e := &listed.Model.EventTypes[i]
		out = append(out, eventType{ID: e.Id, Means: cmp.Or(e.Description, e.Label), Group: e.Group, Scope: e.Scope})
	}

	return out, nil
}

// chosen is the events a caller names, each one there is, without repeats; nil for none named.
func chosen(given []string, known []eventType) ([]string, error) {
	if len(given) == 0 {
		return nil, nil
	}
	out := make([]string, 0, len(given))
	for _, name := range given {
		name = strings.ToLower(strings.TrimSpace(name))
		if !slices.ContainsFunc(known, func(e eventType) bool { return e.ID == name }) {
			ids := make([]string, 0, len(known))
			for _, e := range known {
				ids = append(ids, e.ID)
			}

			return nil, fmt.Errorf("events has %q: an event is one of %s", name, strings.Join(ids, ", "))
		}
		if !slices.Contains(out, name) {
			out = append(out, name)
		}
	}

	return out, nil
}

// told is the events of a channel or a link as the tools give them: the list, or nothing and all set when it is every event there is.
func told(events []string, known []eventType) (some []string, all bool) {
	if len(known) > 0 && !slices.ContainsFunc(known, func(e eventType) bool { return !slices.Contains(events, e.ID) }) {
		return nil, true
	}

	return events, false
}

// channelLink is one environment a channel is told the events of.
type channelLink struct {
	Env       string   `json:"env"`
	Enabled   bool     `json:"enabled"              jsonschema:"whether the environment's events are sent to the channel"`
	Events    []string `json:"events,omitempty"     jsonschema:"the events of the environment the channel is told of; absent when it is all of them"`
	AllEvents bool     `json:"all_events,omitempty" jsonschema:"it is told of every event of the environment"`
}

// channelRow is one notification channel the Dockhand keeps.
type channelRow struct {
	ID           int           `json:"id"`
	Name         string        `json:"name"`
	Type         string        `json:"type"                   jsonschema:"smtp for mail sent through a mail server, apprise for an address of another service: a webhook, ntfy, Gotify, Discord, Slack, Telegram and the like"`
	Enabled      bool          `json:"enabled"                jsonschema:"whether anything is sent through it at all"`
	To           []string      `json:"to"                     jsonschema:"where it sends: for mail the addresses written to, for the rest the service and its host, with the token or login each address carries left out unless reveal_secrets asks"`
	Via          string        `json:"via,omitempty"          jsonschema:"for mail: the mail server it goes through, and the address it is sent from"`
	Events       []string      `json:"events,omitempty"       jsonschema:"the events the channel itself is told of, which decides the ones of Dockhand itself and nothing about a host, whose events go by the tie to its environment; absent when it is all of them"`
	AllEvents    bool          `json:"all_events,omitempty"   jsonschema:"it is told of every event"`
	Environments []channelLink `json:"environments,omitempty" jsonschema:"the environments whose events it is sent: a channel tied to none is told of nothing that happens on a host"`
}

func channelRowOf(k *dockhand.GetNotificationsResponse, known []eventType, reveal bool) (row channelRow, hidden int) {
	row = channelRow{ID: k.Id, Name: k.Name, Type: k.Type, Enabled: yes(k.Enabled), To: []string{}}
	row.Events, row.AllEvents = told(k.EventTypes, known)
	if k.Config == nil {
		return row, 0
	}
	if k.Type == channelMail {
		row.To = append(row.To, k.Config.ToEmails...)
		row.Via = net.JoinHostPort(k.Config.Host, strconv.Itoa(k.Config.Port)) + " as " + k.Config.FromEmail

		return row, 0
	}
	for _, address := range k.Config.Urls {
		if reveal {
			row.To = append(row.To, address)

			continue
		}
		short := shortAddress(address)
		if short != address {
			hidden++
		}
		row.To = append(row.To, short)
	}

	return row, hidden
}

// ofHosts is the events that happen on a host, which are the ones a tie to an environment is about: every one of them is all a tie can be told of.
func ofHosts(known []eventType) []eventType {
	return slices.DeleteFunc(slices.Clone(known), func(e eventType) bool { return e.Scope != scopeHost })
}

// linksOf is the channels each environment's events are sent to, by the channel's number: one call an environment, side by side.
func (r *registry) linksOf(ctx context.Context, envs []environment, known []eventType) (map[int][]channelLink, []envFailure) {
	known = ofHosts(known)
	out := map[int][]channelLink{}
	var failed []envFailure
	for _, answer := range ask(ctx, envs, func(ctx context.Context, env environment) ([]dockhand.GetEnvironmentsByIdNotificationsResponse, error) {
		listed, err := r.dock.GetEnvironmentsByIdNotifications(ctx, env.ID)
		if err != nil {
			return nil, refusal(err)
		}

		return listed.Model, nil
	}) {
		if answer.Err != nil {
			failed = append(failed, envFailure{Env: answer.Env.Name, Error: answer.Err.Error()})

			continue
		}
		for i := range answer.Value {
			link := channelLink{Env: answer.Env.Name, Enabled: yes(answer.Value[i].Enabled)}
			link.Events, link.AllEvents = told(answer.Value[i].EventTypes, known)
			out[answer.Value[i].NotificationId] = append(out[answer.Value[i].NotificationId], link)
		}
	}

	return out, failed
}

// channelArgs is how a channel sends, as the tools that try, make and change one take it: addresses for a service, or a mail server and who to write.
type channelArgs struct {
	URLs         []string `json:"urls,omitempty"          jsonschema:"for a channel that is not mail: the addresses to send to, each beginning with its service, as ntfy://ntfy.example/alerts, gotify://host/token, discord://id/token, slack://..., tgram://bot_token/chat_id, or json://host/path for a webhook sent JSON. An address carries its own token or login, and is kept by Dockhand as given"`
	SMTPHost     string   `json:"smtp_host,omitempty"     jsonschema:"for mail: the mail server to send through"`
	SMTPPort     int      `json:"smtp_port,omitempty"     jsonschema:"for mail: its port, 587 unless given"`
	SMTPSecure   *bool    `json:"smtp_secure,omitempty"   jsonschema:"for mail: speak TLS from the first word, as port 465 expects, where the others start plain and switch"`
	SMTPUsername string   `json:"smtp_username,omitempty" jsonschema:"for mail: the user name to sign in to the mail server with"`
	SMTPPassword string   `json:"smtp_password,omitempty" jsonschema:"for mail: the password to sign in with. It is sent to Dockhand and kept there, and is never given back"`
	FromEmail    string   `json:"from_email,omitempty"    jsonschema:"for mail: the address it is sent from"`
	FromName     string   `json:"from_name,omitempty"     jsonschema:"for mail: the name it is sent under"`
	ToEmails     []string `json:"to_emails,omitempty"     jsonschema:"for mail: the addresses to write to"`
}

// mail reports whether anything of a mail channel was given, and other whether any address of another service was.
func (a *channelArgs) mail() bool {
	return a.SMTPHost != "" || a.SMTPPort != 0 || a.SMTPSecure != nil || a.SMTPUsername != "" || a.SMTPPassword != "" || a.FromEmail != "" || a.FromName != "" || len(a.ToEmails) > 0
}

// addresses is the addresses given, each checked to begin with a service Dockhand sends to.
func (a *channelArgs) addresses() ([]string, error) {
	out := make([]string, 0, len(a.URLs))
	for _, address := range a.URLs {
		address = strings.TrimSpace(address)
		scheme, _, cut := strings.Cut(address, "://")
		if !cut || !slices.Contains(channelSchemes, strings.ToLower(scheme)) {
			return nil, fmt.Errorf("urls has an address that begins %q: an address begins with the service it is for, one of %s, each followed by ://", cmp.Or(scheme, address)[:min(len(cmp.Or(scheme, address)), 20)], strings.Join(channelSchemes, ", "))
		}
		out = append(out, address)
	}

	return out, nil
}

// config is how a new channel sends, of the kind the arguments say: checked to say enough for that kind.
func (a *channelArgs) config() (kind string, cfg dockhand.PostNotificationsRequestConfig, err error) {
	switch {
	case len(a.URLs) > 0 && a.mail():
		return "", cfg, errors.New("a channel is mail, with smtp_host and who to write, or addresses of another service, with urls: not both")
	case len(a.URLs) > 0:
		if cfg.Urls, err = a.addresses(); err != nil {
			return "", cfg, err
		}

		return channelOther, cfg, nil
	case a.mail():
		if strings.TrimSpace(a.SMTPHost) == "" || strings.TrimSpace(a.FromEmail) == "" || len(a.ToEmails) == 0 {
			return "", cfg, errors.New("a mail channel needs smtp_host, from_email and to_emails: the server to send through, the address to send from and who to write")
		}
		cfg = dockhand.PostNotificationsRequestConfig{Host: strings.TrimSpace(a.SMTPHost), Port: cmp.Or(a.SMTPPort, defaultMailPort), Secure: a.SMTPSecure, Username: a.SMTPUsername, Password: a.SMTPPassword, FromEmail: strings.TrimSpace(a.FromEmail), FromName: a.FromName, ToEmails: a.ToEmails}

		return channelMail, cfg, nil
	default:
		return "", cfg, errors.New("how the channel sends is required: urls, the addresses of a service, or smtp_host, from_email and to_emails for mail")
	}
}

// sentOut is what trying to send through a channel came to.
type sentOut struct {
	Channel string `json:"channel,omitempty" jsonschema:"the channel that was tried; absent for one that is not saved, and for an event, which goes to whichever channels are tied to it"`
	Sent    bool   `json:"sent"              jsonschema:"whether the message was handed over: the service or mail server took it"`
	SentTo  *int   `json:"sent_to,omitempty" jsonschema:"for an event: how many channels took it, which is none when no channel that is switched on is tied to the environment for that event"`
	Error   string `json:"error,omitempty"   jsonschema:"why it was not sent, in the words of the service or mail server"`
	Note    string `json:"note,omitempty"`
}

// channelChangeOut is what making or changing a channel answers.
type channelChangeOut struct {
	Channel *channelRow `json:"channel,omitempty" jsonschema:"the channel as Dockhand has it now, read back; absent for a dry run of making one"`
	Changes []string    `json:"changes"           jsonschema:"what was done, in words; empty when nothing needed doing"`
	DryRun  bool        `json:"dry_run,omitempty" jsonschema:"nothing was done: changes says what would be"`
	Note    string      `json:"note,omitempty"`
}

//nolint:gocyclo,maintidx // seven tools registered in one function, each a handler of its own: the count is theirs added up
func registerNotificationTools(r *registry) {
	type notificationListIn struct {
		revealArg

		EventTypes bool `json:"event_types,omitempty" jsonschema:"also list every event a channel can be told of, with when each happens"`
	}
	type notificationListOut struct {
		Channels   []channelRow `json:"channels"              jsonschema:"the notification channels Dockhand keeps"`
		Total      int          `json:"total"`
		EventTypes []eventType  `json:"event_types,omitempty" jsonschema:"with event_types: every event there is"`
		Hidden     int          `json:"hidden,omitempty"      jsonschema:"how many addresses are given without the token or login they carry"`
		Failed     []envFailure `json:"failed,omitempty"      jsonschema:"the environments whose ties to channels could not be read"`
		Note       string       `json:"note,omitempty"`
	}
	add(r, readTool, &mcp.Tool{
		Name:        "notification_list",
		Description: "List the notification channels the Dockhand keeps, the ways it tells people what happened: each one's name, whether it is mail or an address of another service such as a webhook, ntfy, Gotify, Discord, Slack or Telegram, where it sends, whether it is switched on, which events it is told of, and which environments' events are sent to it. A channel hears of what happens on a host, a container dying or an update, only for the environments it is tied to, so one tied to none is silent about every host. With event_types it also lists every event there is and when each happens. An address is given by its service and host alone unless reveal_secrets asks, since the rest of it is the token that lets anyone send to it; a mail password is never given.",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in notificationListIn) (*mcp.CallToolResult, notificationListOut, error) {
		listed, err := r.channels(ctx)
		if err != nil {
			return nil, notificationListOut{}, err
		}
		known, err := r.eventTypes(ctx)
		if err != nil {
			return nil, notificationListOut{}, err
		}
		envs, err := r.environments(ctx)
		if err != nil {
			return nil, notificationListOut{}, err
		}
		links, failed := r.linksOf(ctx, envs, known)

		out := notificationListOut{Channels: make([]channelRow, 0, len(listed)), Total: len(listed), Failed: failed}
		for i := range listed {
			row, hidden := channelRowOf(&listed[i], known, in.RevealSecrets)
			row.Environments = links[listed[i].Id]
			out.Channels, out.Hidden = append(out.Channels, row), out.Hidden+hidden
		}
		slices.SortStableFunc(out.Channels, func(a, b channelRow) int { return cmp.Compare(strings.ToLower(a.Name), strings.ToLower(b.Name)) })
		if in.EventTypes {
			out.EventTypes = known
		}
		switch {
		case out.Total == 0:
			out.Note = "Dockhand keeps no notification channel: nothing that happens is told to anyone"
		case out.Hidden == 1:
			out.Note = "one address is given by its service and host alone: reveal_secrets gives it whole, token and all"
		case out.Hidden > 1:
			out.Note = fmt.Sprintf("%d addresses are given by their service and host alone: reveal_secrets gives them whole, token and all", out.Hidden)
		default:
		}

		return nil, out, nil
	})

	type notificationTestIn struct {
		channelArg
		envArg
		channelArgs

		Event   string `json:"event,omitempty"   jsonschema:"set off this event, as notification_list names them with event_types, for the environment given: it goes to every channel tied to that environment for it, which is how a tie is tried rather than a channel"`
		Title   string `json:"title,omitempty"   jsonschema:"with event: the title of the message, 'Test notification' unless given"`
		Message string `json:"message,omitempty" jsonschema:"with event: what the message says"`
	}
	add(r, writeTool, &mcp.Tool{
		Name:        "notification_test",
		Description: "Send a test message for real, and say whether it was handed over. Give channel to send one through a channel that is saved; or how a channel would send, urls or the mail fields, to try one before it is saved, which saves nothing; or event with env, to set an event off for an environment, which goes to every channel tied to that environment for that event and answers how many took it. Of the events of Dockhand itself only license_expiring can be set off, with no env, and goes to the channels told of it. Whoever reads the channel receives the message. A service or mail server that turns it away is an answer, with its reason, and not an error.",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in notificationTestIn) (*mcp.CallToolResult, sentOut, error) {
		named, err := in.channelArg.named()
		if err != nil {
			return nil, sentOut{}, err
		}
		event := strings.ToLower(strings.TrimSpace(in.Event))
		unsaved := len(in.URLs) > 0 || in.mail()
		if b2i(named != "")+b2i(event != "")+b2i(unsaved) != 1 {
			return nil, sentOut{}, errors.New("give one of three: channel, to send through one that is saved; urls or the mail fields, to try one that is not; or event with env, to set an event off for an environment")
		}

		var out sentOut
		switch {
		case named != "":
			saved, failed := r.oneChannel(ctx, in.channelArg)
			if failed != nil {
				return nil, sentOut{}, failed
			}
			out.Channel = saved.Name
			answer, failed := r.dock.PostNotificationsByIdTest(ctx, saved.Id)
			if failed != nil {
				return nil, out, fmt.Errorf("%s could not be tried: %w", saved.Name, refusal(failed))
			}
			out.Sent, out.Error = yes(answer.Model.Success), answer.Model.Error
			if !yes(saved.Enabled) {
				out.Note = "the channel is switched off: a test goes through all the same, and nothing else does"
			}
		case unsaved:
			kind, cfg, failed := in.config()
			if failed != nil {
				return nil, sentOut{}, failed
			}
			answer, failed := r.dock.PostNotificationsTest(ctx, dockhand.PostNotificationsTestRequest{Type: kind, Name: "test", Config: new(dockhand.PostNotificationsTestRequestConfig(cfg))})
			if failed != nil {
				return nil, out, fmt.Errorf("the channel could not be tried: %w", refusal(failed))
			}
			out.Sent, out.Error = yes(answer.Model.Success), answer.Model.Error
			out.Note = "this channel is not saved: nothing on the Dockhand was changed by trying it"
		default:
			known, failed := r.eventTypes(ctx)
			if failed != nil {
				return nil, sentOut{}, failed
			}
			if _, failed = chosen([]string{event}, known); failed != nil {
				return nil, sentOut{}, errors.New(strings.Replace(failed.Error(), "events has", "event is", 1))
			}
			ofHost := slices.ContainsFunc(known, func(e eventType) bool { return e.ID == event && e.Scope == scopeHost })
			if !ofHost && event != eventLicense {
				return nil, sentOut{}, fmt.Errorf("%s is an event of Dockhand itself, and of those it sets off only %s for a test: this one would reach nobody. To try a channel itself, give channel", event, eventLicense)
			}
			if envNamed, _ := in.envArg.named(); ofHost && envNamed == "" {
				return nil, sentOut{}, fmt.Errorf("%s is an event of a host, so it is set off for one: give env", event)
			}
			request := dockhand.PostNotificationsTriggerTestRequest{EventType: event, Payload: &dockhand.PostNotificationsTriggerTestRequestPayload{Title: cmp.Or(strings.TrimSpace(in.Title), "Test notification"), Message: cmp.Or(strings.TrimSpace(in.Message), "This is a test of the "+event+" notification, set off by hand.")}}
			if ofHost {
				env, unknown := r.one(ctx, in.envArg)
				if unknown != nil {
					return nil, sentOut{}, unknown
				}
				request.EnvironmentId = env.ID
			}
			answer, failed := r.dock.PostNotificationsTriggerTest(ctx, request)
			if failed != nil {
				return nil, out, fmt.Errorf("the event could not be set off: %w", refusal(failed))
			}
			out.SentTo, out.Sent = &answer.Model.Sent, answer.Model.Sent > 0
			switch {
			case !yes(answer.Model.Success):
				out.Note = "a channel it went to did not take it, which Dockhand does not name: trying each channel by itself says which and why"
			case !out.Sent && ofHost:
				out.Note = "it was sent to no channel: none that is switched on is tied to the environment for this event, or the tie itself is switched off"
			case !out.Sent:
				out.Note = "it was sent to no channel: none that is switched on is told of this event"
			default:
			}
		}

		return nil, out, nil
	})

	type notificationCreateIn struct {
		dryRunArg
		channelArgs

		Name     string   `json:"name"               jsonschema:"what to call the channel: the name the notification tools then take"`
		Events   []string `json:"events,omitempty"   jsonschema:"the events it is told of, as notification_list names them with event_types; left out, every one. It is the ones of Dockhand itself this decides: those of a host go by the tie to its environment, whatever is named here"`
		Disabled bool     `json:"disabled,omitempty" jsonschema:"save it switched off, to be switched on later"`
	}
	add(r, writeTool, &mcp.Tool{
		Name:        "notification_create",
		Description: "Save a notification channel on the Dockhand, a way for it to tell people what happened: mail through a mail server, or an address of another service, a webhook, ntfy, Gotify, Discord, Slack, Telegram and the like. A new channel is told of nothing that happens on a host until it is tied to an environment, which notification_link does; its own events are the ones of Dockhand itself. Nothing is sent by saving it: notification_test sends a test. A name already in use is refused. A mail password is kept by Dockhand and never given back, and the addresses are kept as given.",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in notificationCreateIn) (*mcp.CallToolResult, channelChangeOut, error) {
		name := strings.TrimSpace(in.Name)
		if name == "" {
			return nil, channelChangeOut{}, errors.New("name is required: what to call the channel")
		}
		kind, cfg, err := in.config()
		if err != nil {
			return nil, channelChangeOut{}, err
		}
		known, err := r.eventTypes(ctx)
		if err != nil {
			return nil, channelChangeOut{}, err
		}
		events, err := chosen(in.Events, known)
		if err != nil {
			return nil, channelChangeOut{}, err
		}
		listed, err := r.channels(ctx)
		if err != nil {
			return nil, channelChangeOut{}, err
		}
		if slices.ContainsFunc(listed, func(k dockhand.GetNotificationsResponse) bool { return strings.EqualFold(k.Name, name) }) {
			return nil, channelChangeOut{}, fmt.Errorf("a notification channel is already named %q: notification_list gives it, and another name is needed for another", name)
		}

		out := channelChangeOut{Changes: []string{}, DryRun: in.DryRun}
		what := fmt.Sprintf("the %s channel %s", map[string]string{channelMail: "mail", channelOther: "notification"}[kind], name)
		if kind == channelMail {
			what += fmt.Sprintf(", which writes to %s through %s", strings.Join(cfg.ToEmails, ", "), cfg.Host)
		} else {
			short := make([]string, 0, len(cfg.Urls))
			for _, address := range cfg.Urls {
				short = append(short, shortAddress(address))
			}
			what += ", which sends to " + strings.Join(short, ", ")
		}
		if in.Disabled {
			what += ", switched off"
		}
		if in.DryRun {
			out.Changes = append(out.Changes, "would save "+what)

			return nil, out, nil
		}

		request := dockhand.PostNotificationsRequest{Type: kind, Name: name, Config: &cfg, EventTypes: events}
		if in.Disabled {
			request.Enabled = new(false)
		}
		made, err := r.dock.PostNotifications(ctx, request)
		if err != nil {
			return nil, out, fmt.Errorf("%s was not saved: %w", name, refusal(err))
		}
		out.Changes = append(out.Changes, "saved "+what)
		out.Note = "it is tied to no environment yet, so it is told of nothing that happens on a host: notification_link ties it to one"
		back, err := r.oneChannel(ctx, channelArg{Channel: float64(made.Model.Id)})
		if err != nil {
			out.Note = note("it was saved, and could not be read back: "+err.Error(), out.Note)

			return nil, out, nil //nolint:nilerr // it was saved: that it could not be read back is said in the answer
		}
		row, _ := channelRowOf(back, known, false)
		out.Channel = &row

		return nil, out, nil
	})

	type notificationEditIn struct {
		channelArg
		dryRunArg
		channelArgs

		Name    string   `json:"name,omitempty"    jsonschema:"another name for the channel"`
		Enabled *bool    `json:"enabled,omitempty" jsonschema:"false to switch the channel off, so that nothing is sent through it, and true to switch it on again"`
		Events  []string `json:"events,omitempty"  jsonschema:"every event it is to be told of, in place of the ones it is: the events of Dockhand itself, since those of a host go by the channel's tie to its environment"`
	}
	add(r, writeTool, &mcp.Tool{
		Name:        "notification_edit",
		Description: "Change a notification channel the Dockhand keeps: its name, whether it is switched on, the events it is told of, and how it sends. For a channel that is not mail, urls replaces every address it has. For mail, only the fields given change, and the password is kept unless another is given. What kind of channel it is cannot be changed. A channel switched off sends nothing, whatever it is tied to. Nothing is sent by changing it: notification_test sends a test.",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in notificationEditIn) (*mcp.CallToolResult, channelChangeOut, error) {
		saved, err := r.oneChannel(ctx, in.channelArg)
		if err != nil {
			return nil, channelChangeOut{}, err
		}
		known, err := r.eventTypes(ctx)
		if err != nil {
			return nil, channelChangeOut{}, err
		}
		events, err := chosen(in.Events, known)
		if err != nil {
			return nil, channelChangeOut{}, err
		}
		row, _ := channelRowOf(saved, known, false)
		out := channelChangeOut{Channel: &row, Changes: []string{}, DryRun: in.DryRun}
		if (saved.Type == channelMail && len(in.URLs) > 0) || (saved.Type != channelMail && in.mail()) {
			return nil, out, fmt.Errorf("%s is %s, and what kind a channel is cannot be changed: it takes %s", saved.Name, map[bool]string{true: "a mail channel", false: "a channel that sends to addresses"}[saved.Type == channelMail], map[bool]string{true: "the mail fields, and no urls", false: "urls, and no mail fields"}[saved.Type == channelMail])
		}

		done := func(did, would string) string {
			if in.DryRun {
				return would
			}

			return did
		}
		var request dockhand.PutNotificationsByIdRequest
		if name := strings.TrimSpace(in.Name); name != "" && name != saved.Name {
			listed, failed := r.channels(ctx)
			if failed != nil {
				return nil, out, failed
			}
			if slices.ContainsFunc(listed, func(k dockhand.GetNotificationsResponse) bool {
				return k.Id != saved.Id && strings.EqualFold(k.Name, name)
			}) {
				return nil, out, fmt.Errorf("another notification channel is named %q, so %s is not renamed to it", name, saved.Name)
			}
			request.Name = name
			out.Changes = append(out.Changes, fmt.Sprintf("%s it from %s to %s", done("renamed", "would rename"), saved.Name, name))
		}
		if in.Enabled != nil && *in.Enabled != yes(saved.Enabled) {
			request.Enabled = in.Enabled
			out.Changes = append(out.Changes, done("switched", "would switch")+" it "+map[bool]string{true: "on", false: "off"}[*in.Enabled])
		}
		if events != nil && !slices.Equal(slices.Sorted(slices.Values(events)), slices.Sorted(slices.Values(saved.EventTypes))) {
			request.EventTypes = events
			out.Changes = append(out.Changes, fmt.Sprintf("%s the events it is told of to %s", done("changed", "would change"), strings.Join(events, ", ")))
		}

		// how it sends: Dockhand takes the whole of it or none, so what is not given is sent back as it is, the mail password by the mask that stands
		// for it
		have := saved.Config
		if have == nil {
			have = &dockhand.GetNotificationsResponseConfig{}
		}
		switch {
		case len(in.URLs) > 0:
			addresses, failed := in.addresses()
			if failed != nil {
				return nil, out, failed
			}
			if !slices.Equal(addresses, have.Urls) {
				short := make([]string, 0, len(addresses))
				for _, address := range addresses {
					short = append(short, shortAddress(address))
				}
				request.Config = &dockhand.PutNotificationsByIdRequestConfig{Urls: addresses}
				out.Changes = append(out.Changes, fmt.Sprintf("%s where it sends to %s", done("changed", "would change"), strings.Join(short, ", ")))
			}
		case in.mail():
			cfg := dockhand.PutNotificationsByIdRequestConfig{Host: have.Host, Port: have.Port, Secure: have.Secure, Username: have.Username, Password: have.Password, FromEmail: have.FromEmail, FromName: have.FromName, ToEmails: have.ToEmails}
			var changed []string
			set := func(what string, differs bool, apply func()) {
				if differs {
					apply()
					changed = append(changed, what)
				}
			}
			set("its mail server", strings.TrimSpace(in.SMTPHost) != "" && strings.TrimSpace(in.SMTPHost) != have.Host, func() { cfg.Host = strings.TrimSpace(in.SMTPHost) })
			set("its port", in.SMTPPort != 0 && in.SMTPPort != have.Port, func() { cfg.Port = in.SMTPPort })
			set("whether it speaks TLS from the first word", in.SMTPSecure != nil && *in.SMTPSecure != yes(have.Secure), func() { cfg.Secure = in.SMTPSecure })
			set("the user name it signs in with", in.SMTPUsername != "" && in.SMTPUsername != have.Username, func() { cfg.Username = in.SMTPUsername })
			set("the password it signs in with", in.SMTPPassword != "", func() { cfg.Password = in.SMTPPassword })
			set("the address it sends from", strings.TrimSpace(in.FromEmail) != "" && strings.TrimSpace(in.FromEmail) != have.FromEmail, func() { cfg.FromEmail = strings.TrimSpace(in.FromEmail) })
			set("the name it sends under", in.FromName != "" && in.FromName != have.FromName, func() { cfg.FromName = in.FromName })
			set("who it writes to", len(in.ToEmails) > 0 && !slices.Equal(in.ToEmails, have.ToEmails), func() { cfg.ToEmails = in.ToEmails })
			if len(changed) > 0 {
				// the password goes back as Dockhand gave it, which is the mask that stands for it and is read as the one it has
				request.Config = &cfg
				out.Changes = append(out.Changes, done("changed ", "would change ")+strings.Join(changed, ", "))
			}
		default:
		}

		if len(out.Changes) == 0 {
			if strings.TrimSpace(in.Name) == "" && in.Enabled == nil && events == nil && len(in.URLs) == 0 && !in.mail() {
				return nil, out, errors.New("nothing to change was given: name, enabled, events, urls or the mail fields")
			}
			out.Note = "nothing to do: it is that way already"

			return nil, out, nil
		}
		if in.DryRun {
			return nil, out, nil
		}

		if _, err := r.dock.PutNotificationsById(ctx, saved.Id, request); err != nil {
			out.Changes = []string{}

			return nil, out, fmt.Errorf("%s was not changed: %w", saved.Name, refusal(err))
		}
		back, err := r.oneChannel(ctx, channelArg{Channel: float64(saved.Id)})
		if err != nil {
			out.Note = "it was changed, and could not be read back: " + err.Error()

			return nil, out, nil //nolint:nilerr // it was changed: that it could not be read back is said in the answer
		}
		row, _ = channelRowOf(back, known, false)
		out.Channel = &row

		return nil, out, nil
	})

	type notificationLinkIn struct {
		channelArg
		envArg
		dryRunArg

		Events  []string `json:"events,omitempty"  jsonschema:"the events of the environment the channel is to be told of, as notification_list names them with event_types, in place of the ones it is; left out for a new tie, every one"`
		Enabled *bool    `json:"enabled,omitempty" jsonschema:"false to keep the tie and send nothing over it, true to send again; a new tie is on unless said otherwise"`
	}
	type notificationLinkOut struct {
		Channel string       `json:"channel"`
		Link    *channelLink `json:"link,omitempty"    jsonschema:"the tie as Dockhand has it now, read back; absent for a dry run of making one"`
		Changes []string     `json:"changes"           jsonschema:"what was done, in words; empty when nothing needed doing"`
		DryRun  bool         `json:"dry_run,omitempty" jsonschema:"nothing was done: changes says what would be"`
		Note    string       `json:"note,omitempty"`
	}
	add(r, writeTool, &mcp.Tool{
		Name:        "notification_link",
		Description: "Tie a notification channel to an environment, so that what happens on that host is sent to it, or change a tie there is: which of the environment's events the channel is told of, and whether the tie is on. It is the one way a channel hears of a container starting, dying or being updated. Given again with the same it changes nothing. A tie that is on sends nothing through a channel that is itself switched off, which the answer says.",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in notificationLinkIn) (*mcp.CallToolResult, notificationLinkOut, error) {
		saved, err := r.oneChannel(ctx, in.channelArg)
		if err != nil {
			return nil, notificationLinkOut{}, err
		}
		env, err := r.one(ctx, in.envArg)
		if err != nil {
			return nil, notificationLinkOut{}, err
		}
		known, err := r.eventTypes(ctx)
		if err != nil {
			return nil, notificationLinkOut{}, err
		}
		// a tie is about what happens on the host: an event of Dockhand itself would be saved with it and never sent over it
		events, err := chosen(in.Events, ofHosts(known))
		if err != nil {
			return nil, notificationLinkOut{}, err
		}
		out := notificationLinkOut{Channel: saved.Name, Changes: []string{}, DryRun: in.DryRun}
		links, failed := r.linksOf(ctx, []environment{env}, known)
		if len(failed) > 0 {
			return nil, out, fmt.Errorf("the channels tied to %s could not be read, so nothing was changed: %s", env.Name, failed[0].Error)
		}
		done := func(did, would string) string {
			if in.DryRun {
				return would
			}

			return did
		}
		told := func(some []string) string { return cmp.Or(strings.Join(some, ", "), "every event") }

		have := links[saved.Id]
		switch {
		case len(have) == 0:
			out.Changes = append(out.Changes, fmt.Sprintf("%s %s to %s, for %s", done("tied", "would tie"), saved.Name, env.Name, told(events)))
			if in.Enabled != nil && !*in.Enabled {
				out.Changes = append(out.Changes, done("left", "would leave")+" the tie switched off")
			}
			if in.DryRun {
				return nil, out, nil
			}
			if _, err := r.dock.PostEnvironmentsByIdNotifications(ctx, env.ID, dockhand.PostEnvironmentsByIdNotificationsRequest{NotificationId: saved.Id, Enabled: in.Enabled, EventTypes: events}); err != nil {
				out.Changes = []string{}

				return nil, out, fmt.Errorf("%s was not tied to %s: %w", saved.Name, env.Name, refusal(err))
			}
		default:
			out.Link = &have[0]
			var request dockhand.PutEnvironmentsByIdNotificationsByNotificationIdRequest
			if in.Enabled != nil && *in.Enabled != have[0].Enabled {
				request.Enabled = in.Enabled
				out.Changes = append(out.Changes, done("switched", "would switch")+" the tie "+map[bool]string{true: "on", false: "off"}[*in.Enabled])
			}
			was, wasAll := have[0].Events, have[0].AllEvents
			if now, nowAll := toldOf(events, ofHosts(known)); events != nil && (nowAll != wasAll || !slices.Equal(slices.Sorted(slices.Values(now)), slices.Sorted(slices.Values(was)))) {
				request.EventTypes = events
				out.Changes = append(out.Changes, fmt.Sprintf("%s the events of %s it is told of to %s", done("changed", "would change"), env.Name, told(now)))
			}
			if len(out.Changes) == 0 {
				out.Note = fmt.Sprintf("nothing to do: %s is tied to %s that way already", saved.Name, env.Name)

				return nil, out, nil
			}
			if in.DryRun {
				return nil, out, nil
			}
			if _, err := r.dock.PutEnvironmentsByIdNotificationsByNotificationId(ctx, env.ID, saved.Id, request); err != nil {
				out.Changes = []string{}

				return nil, out, fmt.Errorf("the tie of %s to %s was not changed: %w", saved.Name, env.Name, refusal(err))
			}
		}

		links, failed = r.linksOf(ctx, []environment{env}, known)
		switch {
		case len(failed) > 0:
			out.Note = "it was done, and the tie could not be read back: " + failed[0].Error
		case len(links[saved.Id]) == 0:
			return nil, out, fmt.Errorf("the tie of %s to %s was answered as made, and %s lists none", saved.Name, env.Name, env.Name)
		default:
			out.Link = &links[saved.Id][0]
			if out.Link.Enabled && !yes(saved.Enabled) {
				out.Note = saved.Name + " is itself switched off, so nothing is sent over the tie until notification_edit switches it on"
			}
		}

		return nil, out, nil
	})

	type notificationUnlinkIn struct {
		channelArg
		envArg

		Confirm bool `json:"confirm,omitempty" jsonschema:"untie it: without this the answer says what would go and nothing is untied"`
	}
	type notificationUnlinkOut struct {
		Channel string       `json:"channel"`
		Env     string       `json:"env"`
		Link    *channelLink `json:"link,omitempty" jsonschema:"the tie that is, or was"`
		Removed bool         `json:"removed"        jsonschema:"the tie was removed, and is read back as gone"`
		Note    string       `json:"note,omitempty"`
	}
	add(r, deleteTool, &mcp.Tool{
		Name:        "notification_link_delete",
		Description: "Untie a notification channel from an environment, so that nothing that happens on that host is sent to it any more. The channel itself is kept, with its ties to other environments, and so is the choice of events only as long as the tie is: tied again, it starts from every event. To stop a tie for a while and keep its events, notification_link switches it off instead. Without confirm the answer says what would go and unties nothing.",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in notificationUnlinkIn) (*mcp.CallToolResult, notificationUnlinkOut, error) {
		saved, err := r.oneChannel(ctx, in.channelArg)
		if err != nil {
			return nil, notificationUnlinkOut{}, err
		}
		env, err := r.one(ctx, in.envArg)
		if err != nil {
			return nil, notificationUnlinkOut{}, err
		}
		known, err := r.eventTypes(ctx)
		if err != nil {
			return nil, notificationUnlinkOut{}, err
		}
		out := notificationUnlinkOut{Channel: saved.Name, Env: env.Name}
		// Dockhand answers that it untied a channel whether or not it was tied, so that it is tied is looked for first
		links, failed := r.linksOf(ctx, []environment{env}, known)
		if len(failed) > 0 {
			return nil, out, fmt.Errorf("the channels tied to %s could not be read, so nothing was untied: %s", env.Name, failed[0].Error)
		}
		if len(links[saved.Id]) == 0 {
			return nil, out, fmt.Errorf("%s is not tied to %s: notification_list gives the environments each channel is tied to", saved.Name, env.Name)
		}
		out.Link = &links[saved.Id][0]
		if !in.Confirm {
			out.Note = fmt.Sprintf("nothing was untied: call again with confirm, and nothing that happens on %s is sent to %s any more", env.Name, saved.Name)

			return nil, out, nil
		}

		if _, err := r.dock.DeleteEnvironmentsByIdNotificationsByNotificationId(ctx, env.ID, saved.Id); err != nil {
			return nil, out, fmt.Errorf("%s was not untied from %s: %w", saved.Name, env.Name, refusal(err))
		}
		links, failed = r.linksOf(ctx, []environment{env}, known)
		switch {
		case len(failed) > 0:
			out.Note = "it was untied, and that it is could not be read back: " + failed[0].Error
		case len(links[saved.Id]) > 0:
			return nil, out, fmt.Errorf("the untying of %s from %s was answered as done, and %s still lists the tie", saved.Name, env.Name, env.Name)
		default:
			out.Removed = true
		}

		return nil, out, nil
	})

	type notificationDeleteIn struct {
		channelArg

		Confirm bool `json:"confirm,omitempty" jsonschema:"remove it: without this the answer says what would go and nothing is removed"`
	}
	type notificationDeleteOut struct {
		Channel string   `json:"channel"`
		Removed bool     `json:"removed"            jsonschema:"the channel was removed, and is read back as gone"`
		WouldGo []string `json:"would_go,omitempty" jsonschema:"what removing it takes away, said before anything is removed"`
		Note    string   `json:"note,omitempty"`
	}
	add(r, deleteTool, &mcp.Tool{
		Name:        "notification_delete",
		Description: "Remove a notification channel from the Dockhand: it forgets where the channel sends, any mail password kept for it, and its tie to every environment, so nothing is sent that way again. Nothing on any host is touched. What it was tied to is said before it is removed, since those environments may then tell nobody of what happens on them. Without confirm the answer says what would go and removes nothing.",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in notificationDeleteIn) (*mcp.CallToolResult, notificationDeleteOut, error) {
		saved, err := r.oneChannel(ctx, in.channelArg)
		if err != nil {
			return nil, notificationDeleteOut{}, err
		}
		out := notificationDeleteOut{Channel: saved.Name}
		if !in.Confirm {
			out.WouldGo = []string{fmt.Sprintf("the %s channel %s, with where it sends", saved.Type, saved.Name)}
			out.Note = "nothing was removed: call again with confirm to remove it"
			// what it is tied to goes with it: an environment that cannot be asked is no reason not to say the rest
			if envs, failed := r.environments(ctx); failed == nil {
				links, _ := r.linksOf(ctx, envs, nil)
				for _, link := range links[saved.Id] {
					out.WouldGo = append(out.WouldGo, fmt.Sprintf("its tie to %s: what happens there is no longer sent to it", link.Env))
				}
			}

			return nil, out, nil
		}

		gone, err := r.dock.DeleteNotificationsById(ctx, saved.Id)
		if err != nil {
			return nil, out, fmt.Errorf("%s was not removed: %w", saved.Name, refusal(err))
		}
		if !yes(gone.Model.Success) {
			return nil, out, fmt.Errorf("the removal of %s was answered without its being said to have succeeded: notification_list says whether it is still there", saved.Name)
		}
		left, err := r.channels(ctx)
		if err != nil {
			out.Note = "it was removed, and the channels could not be listed afterwards: " + err.Error()

			return nil, out, nil //nolint:nilerr // the removal went through: that it could not be read back is said in the answer
		}
		if slices.ContainsFunc(left, func(k dockhand.GetNotificationsResponse) bool { return k.Id == saved.Id }) {
			return nil, out, fmt.Errorf("the removal of %s was answered as done, and it is still listed", saved.Name)
		}
		out.Removed = true

		return nil, out, nil
	})
}

// toldOf is told for the events a caller chose: nil chosen is nothing asked.
func toldOf(events []string, known []eventType) (some []string, all bool) {
	if events == nil {
		return nil, false
	}

	return told(events, known)
}
