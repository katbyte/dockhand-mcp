package tools

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/katbyte/dockhand-mcp/sdk/dockhand"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

const (
	// minAgentToken is the shortest token of a caller's own choosing that is taken: Dockhand takes one of any length, and what an agent calls in with
	// is all that stands between a stranger and the host
	minAgentToken = 16
	// howAgentCalls is how an agent is told where to call and with what, which is said wherever a token is made
	howAgentCalls = "an agent calls in when it is started with TOKEN set to the token and DOCKHAND_SERVER_URL set to wss://<the address the host reaches this Dockhand at>/api/hawser/connect (ws:// where Dockhand is served without TLS)"
	// turnedAway is what follows an agent calling with a token that has stopped working, which costs whoever does not know it five minutes of
	// wondering why the right token is refused
	turnedAway = "an agent left running calls again with the token that no longer works and is turned away, and Dockhand then turns away every call from that agent's address for five minutes, the right token's among them: stop the agent first, or expect one started with a new token to get through only once the five minutes have passed, which it does by itself"
)

// agentToken is one token an agent calls in with, as Dockhand lists it.
type agentToken struct {
	ID       int    `json:"id"                  jsonschema:"the token's number, which is what revoking it takes"`
	Name     string `json:"name"`
	Env      string `json:"env"                 jsonschema:"the environment the token is for"`
	Starts   string `json:"starts"              jsonschema:"how the token begins, which is all of it Dockhand shows again"`
	Active   bool   `json:"active"              jsonschema:"whether an agent can call in with it: an environment has one active token, and making another, or revoking it, ends it"`
	Made     string `json:"made"`
	LastUsed string `json:"last_used,omitempty" jsonschema:"when an agent last called in with it; absent for one never used"`
	Expires  string `json:"expires,omitempty"   jsonschema:"when it stops working; absent for one that does not expire"`
}

// agentTokens is every agent token the Dockhand keeps, the newest first, each with the name of its environment.
func (r *registry) agentTokens(ctx context.Context, envs []environment) ([]agentToken, error) {
	listed, err := r.dock.GetHawserTokens(ctx)
	if err != nil {
		return nil, fmt.Errorf("the agent tokens could not be listed: %w", refusal(err))
	}

	out := make([]agentToken, 0, len(listed.Model))
	for i := range listed.Model {
		t := &listed.Model[i]
		env := "#" + strconv.Itoa(t.EnvironmentId)
		if at := slices.IndexFunc(envs, func(e environment) bool { return e.ID == t.EnvironmentId }); at >= 0 {
			env = envs[at].Name
		}
		out = append(out, agentToken{ID: t.Id, Name: t.Name, Env: env, Starts: t.TokenPrefix, Active: yes(t.IsActive), Made: t.CreatedAt, LastUsed: t.LastUsed, Expires: t.ExpiresAt})
	}
	slices.SortStableFunc(out, func(a, b agentToken) int { return cmp.Compare(b.ID, a.ID) })

	return out, nil
}

// callers is the agents that have called in and are connected now, each by the number of the environment it called in for.
func (r *registry) callers(ctx context.Context) (map[int]*dockhand.GetHawserConnectResponseConnections, error) {
	answer, err := r.dock.GetHawserConnect(ctx)
	if err != nil {
		return nil, fmt.Errorf("the agents that have called in could not be listed: %w", refusal(err))
	}

	out := map[int]*dockhand.GetHawserConnectResponseConnections{}
	for i := range answer.Model.Connections {
		out[answer.Model.Connections[i].EnvironmentId] = &answer.Model.Connections[i]
	}

	return out, nil
}

// expiryOf is when a token is to stop working as a caller gives it, a number of days from now or a date or time, written the way Dockhand keeps it.
// "" is a token that does not expire.
func expiryOf(given string, now time.Time) (string, error) {
	given = strings.TrimSpace(given)
	if given == "" {
		return "", nil
	}

	var at time.Time
	if days, isDays := strings.CutSuffix(given, "d"); isDays {
		n, err := strconv.Atoi(days)
		if err != nil || n < 1 {
			return "", fmt.Errorf("expires is %q: a number of days is a whole number above 0 and a d, as 90d", given)
		}
		at = now.AddDate(0, 0, n)
	} else if day, err := time.Parse(time.DateOnly, given); err == nil {
		at = day
	} else if at, err = time.Parse(time.RFC3339, given); err != nil {
		return "", fmt.Errorf("expires is %q: it is a number of days from now (90d), a date (2027-01-31) or a time (2027-01-31T00:00:00Z)", given)
	}
	if !at.After(now) {
		return "", fmt.Errorf("expires is %q, which is past: a token that has expired lets no agent in", given)
	}

	return at.UTC().Format(time.RFC3339), nil
}

// tokenFor makes the token an environment's agent calls in with: Dockhand's own making unless the caller gives one. Making it ends every token the
// environment had, and Dockhand cuts off the agent that called in with one.
func (r *registry) tokenFor(ctx context.Context, env environment, name, expires, own string) (token string, id int, err error) {
	made, err := r.dock.PostHawserTokens(ctx, dockhand.PostHawserTokensRequest{Name: name, EnvironmentId: env.ID, ExpiresAt: expires, RawToken: own})
	if err != nil {
		return "", 0, refusal(err)
	}
	if own != "" {
		return "", made.Model.TokenId, nil
	}
	if made.Model.Token == "" {
		return "", made.Model.TokenId, errors.New("Dockhand answered that it made a token and gave none") //nolint:staticcheck // ST1005: the sentence begins with the server's name
	}

	return made.Model.Token, made.Model.TokenId, nil
}

// addCalledBy adds an environment an agent calls in for, which is saved with nothing tried, and makes the token its agent is to call in with.
func (r *registry) addCalledBy(ctx context.Context, name, own string, labels []string, dry bool) (*mcp.CallToolResult, envChangeOut, error) {
	out := envChangeOut{Changes: []string{}, DryRun: dry}
	what := fmt.Sprintf("the environment %s, reached by %s: an agent on the host calls Dockhand", name, connectionEdge)
	calledWith := "a token for its agent to call in with, which is given once"
	if own != "" {
		calledWith = "the token given as the one its agent calls in with"
	}
	if dry {
		out.Changes = append(out.Changes, "would add "+what, "would make "+calledWith)

		return nil, out, nil
	}

	made, err := r.dock.PostEnvironments(ctx, dockhand.PostEnvironmentsRequest{Name: name, ConnectionType: connectionEdge, Labels: labels})
	if err != nil {
		return nil, out, fmt.Errorf("%s was not added: %w", name, refusal(err))
	}
	out.Changes = append(out.Changes, "added "+what)
	row := environmentRowOf(&dockhand.GetEnvironmentsResponse{Id: made.Model.Id, Name: name, ConnectionType: connectionEdge})
	if back, err := r.dock.GetEnvironmentsById(ctx, made.Model.Id); err != nil {
		out.Note = "it was added, and could not be read back: " + err.Error()
	} else {
		row = environmentRowOf(&dockhand.GetEnvironmentsResponse{Id: back.Model.Id, Name: back.Model.Name, ConnectionType: back.Model.ConnectionType})
	}
	row.Online = new(false)
	out.Environment = &row

	// an environment with no token is one nothing can call in for, which is said rather than left to be found
	if out.Token, _, err = r.tokenFor(ctx, environment{ID: made.Model.Id, Name: name}, name, "", own); err != nil {
		return nil, out, fmt.Errorf("%s was added, and the token for its agent was not made, so nothing can call in for it yet: %w (agent_token_create makes one)", name, err)
	}
	out.Changes = append(out.Changes, "made "+calledWith)
	out.Note = note(out.Note, "no agent has called in yet, so the host is not reached: "+howAgentCalls)
	if out.Token != "" {
		out.Note = note(out.Note, "this is the only time the token is shown")
	}

	return nil, out, nil
}

func registerAgentTools(r *registry) {
	type agentCaller struct {
		Env           string   `json:"env"                      jsonschema:"the environment the agent called in for"`
		Agent         string   `json:"agent"                    jsonschema:"what the agent calls itself"`
		AgentVersion  string   `json:"agent_version,omitempty"`
		DockerVersion string   `json:"docker_version,omitempty" jsonschema:"the version of the Docker behind it"`
		Hostname      string   `json:"hostname,omitempty"       jsonschema:"what the agent's machine calls itself"`
		Can           []string `json:"can,omitempty"            jsonschema:"what the agent says it can do: exec, metrics, events, compose"`
		Since         string   `json:"since,omitempty"          jsonschema:"when it called in"`
		LastHeard     string   `json:"last_heard,omitempty"     jsonschema:"when it was last heard from, which it is every few seconds"`
	}
	type agentListIn struct {
		envArg

		Revoked bool `json:"revoked,omitempty" jsonschema:"list the tokens that no longer work as well, which Dockhand keeps a record of for good"`
	}
	type agentListOut struct {
		Connected []agentCaller `json:"connected"         jsonschema:"the agents that have called in and are connected now"`
		Waiting   []string      `json:"waiting"           jsonschema:"the environments an agent is to call in for and none has: their hosts are not reached"`
		Tokens    []agentToken  `json:"tokens"            jsonschema:"the tokens agents call in with, the newest first: the ones that work, and with revoked the rest"`
		Revoked   int           `json:"revoked,omitempty" jsonschema:"how many tokens that no longer work are left out"`
		Note      string        `json:"note,omitempty"`
	}
	add(r, readTool, &mcp.Tool{
		Name:        "agent_list",
		Description: "List the Hawser agents that call in to the Dockhand, and the tokens they call in with: which agents are connected right now, each with its name, version, machine and when it was last heard from; which environments are waiting for an agent that has not called; and every token, by the environment it is for, how it begins, when it was made and last used, and when it expires. It is how to tell a host that is down from an agent that was never started or was given the wrong token: a token that was never used is one no agent has got in with. An agent that calls with a token Dockhand does not take is turned away, and so is every call from its address for the five minutes after, whatever token it brings. This is about agents that call Dockhand (hawser-edge): an agent Dockhand calls is tried with env_test. A token is never shown again, only how it begins.",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in agentListIn) (*mcp.CallToolResult, agentListOut, error) {
		named, err := in.named()
		if err != nil {
			return nil, agentListOut{}, err
		}
		only := 0
		if named != "" {
			env, failed := r.one(ctx, in.envArg)
			if failed != nil {
				return nil, agentListOut{}, failed
			}
			only = env.ID
		}
		listed, err := r.dock.GetEnvironments(ctx)
		if err != nil {
			return nil, agentListOut{}, fmt.Errorf("the environments could not be listed: %w", refusal(err))
		}
		callers, err := r.callers(ctx)
		if err != nil {
			return nil, agentListOut{}, err
		}

		out := agentListOut{Connected: []agentCaller{}, Waiting: []string{}, Tokens: []agentToken{}}
		envs := make([]environment, 0, len(listed.Model))
		for i := range listed.Model {
			e := &listed.Model[i]
			envs = append(envs, environment{ID: e.Id, Name: e.Name})
			if only != 0 && e.Id != only {
				continue
			}
			switch caller := callers[e.Id]; {
			case caller != nil:
				out.Connected = append(out.Connected, agentCaller{Env: e.Name, Agent: caller.AgentName, AgentVersion: caller.AgentVersion, DockerVersion: caller.DockerVersion, Hostname: caller.Hostname, Can: caller.Capabilities, Since: caller.ConnectedAt, LastHeard: caller.LastHeartbeat})
			case e.ConnectionType == connectionEdge:
				out.Waiting = append(out.Waiting, e.Name)
			default:
			}
		}

		tokens, err := r.agentTokens(ctx, envs)
		if err != nil {
			return nil, out, err
		}
		for _, token := range tokens {
			switch {
			case only != 0 && token.Env != envs[slices.IndexFunc(envs, func(e environment) bool { return e.ID == only })].Name:
			case token.Active || in.Revoked:
				out.Tokens = append(out.Tokens, token)
			default:
				out.Revoked++
			}
		}
		if len(out.Connected)+len(out.Waiting)+len(out.Tokens)+out.Revoked == 0 {
			out.Note = "no environment here is one an agent calls in for: env_create makes one, with connection hawser-edge"
		}

		return nil, out, nil
	})

	type agentTokenCreateIn struct {
		envArg
		dryRunArg

		Name       string `json:"name,omitempty"        jsonschema:"what to call the token, which is how it is told from the ones before it; the environment's name unless given"`
		Expires    string `json:"expires,omitempty"     jsonschema:"when it is to stop working: a number of days from now (90d), a date (2027-01-31) or a time; left out, it does not expire"`
		AgentToken string `json:"agent_token,omitempty" jsonschema:"a token of your own choosing, 16 characters at least, for an agent that is already started with one; left out, Dockhand makes one and the answer gives it once"`
	}
	type agentTokenCreateOut struct {
		Env     string   `json:"env"`
		Name    string   `json:"name"`
		TokenID int      `json:"token_id,omitempty" jsonschema:"the new token's number"`
		Token   string   `json:"token,omitempty"    jsonschema:"the token itself, when Dockhand made it. Dockhand never shows it again: it is given to whoever starts the agent, and kept nowhere else. Absent for a token of the caller's own, which is not repeated"`
		Expires string   `json:"expires,omitempty"  jsonschema:"when it stops working; absent for one that does not expire"`
		Changes []string `json:"changes"            jsonschema:"what was done, in words"`
		DryRun  bool     `json:"dry_run,omitempty"  jsonschema:"nothing was done: changes says what would be"`
		Note    string   `json:"note,omitempty"`
	}
	add(r, writeTool, &mcp.Tool{
		Name:        "agent_token_create",
		Description: "Make the token a Hawser agent calls in to the Dockhand with, for one environment of the kind an agent calls in for (hawser-edge). The answer holds the token, once: Dockhand keeps only what it needs to know it again, so hand it straight to whoever starts the agent. An environment has one token that works: making another ends the one before it at once, and Dockhand cuts off the agent that called in with it, so the host is not reached again until its agent is started with the new one. That is how a token is replaced, and it is said by dry_run before it is done. An agent left running through it calls again with the old token, and Dockhand then turns away its address for five minutes, the new token included, so stop the agent before replacing its token. A token of your own choosing can be given instead, for an agent that already has one.",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in agentTokenCreateIn) (*mcp.CallToolResult, agentTokenCreateOut, error) {
		if in.AgentToken != "" && len(in.AgentToken) < minAgentToken {
			return nil, agentTokenCreateOut{}, fmt.Errorf("agent_token is %d characters: a token of your own is %d at least, or left out for Dockhand to make one", len(in.AgentToken), minAgentToken)
		}
		expires, err := expiryOf(in.Expires, time.Now())
		if err != nil {
			return nil, agentTokenCreateOut{}, err
		}
		env, err := r.one(ctx, in.envArg)
		if err != nil {
			return nil, agentTokenCreateOut{}, err
		}
		kept, err := r.dock.GetEnvironmentsById(ctx, env.ID)
		if err != nil {
			return nil, agentTokenCreateOut{}, fmt.Errorf("%s could not be read: %w", env.Name, refusal(err))
		}
		if kept.Model.ConnectionType != connectionEdge {
			return nil, agentTokenCreateOut{}, fmt.Errorf("%s is reached by %s: a token made here is what an agent that calls Dockhand (hawser-edge) calls in with. The token of an agent Dockhand calls is the agent's own, which env_edit gives Dockhand", env.Name, kept.Model.ConnectionType)
		}
		out := agentTokenCreateOut{Env: env.Name, Name: cmp.Or(strings.TrimSpace(in.Name), env.Name), Expires: expires, Changes: []string{}, DryRun: in.DryRun}

		// what making it ends: the token in use, and the call of the agent that is using it
		tokens, err := r.agentTokens(ctx, []environment{env})
		if err != nil {
			return nil, out, err
		}
		callers, err := r.callers(ctx)
		if err != nil {
			return nil, out, err
		}
		done := func(did, would string) string {
			if in.DryRun {
				return would
			}

			return did
		}
		what := "the token " + out.Name + " for " + env.Name
		if expires != "" {
			what += ", which stops working at " + expires
		}
		out.Changes = append(out.Changes, done("made ", "would make ")+what)
		for _, token := range tokens {
			if token.Env == env.Name && token.Active {
				out.Changes = append(out.Changes, fmt.Sprintf("%s the token %s (it begins %s), which was the one that worked", done("ended", "would end"), token.Name, token.Starts))
			}
		}
		cutOff := callers[env.ID] != nil
		if cutOff {
			out.Changes = append(out.Changes, fmt.Sprintf("%s the agent %s, which called in with the token before: %s is not reached until its agent is started with the new one", done("cut off", "would cut off"), callers[env.ID].AgentName, env.Name))
		}
		if in.DryRun {
			if cutOff {
				out.Note = turnedAway
			}

			return nil, out, nil
		}

		if out.Token, out.TokenID, err = r.tokenFor(ctx, env, out.Name, expires, in.AgentToken); err != nil {
			out.Changes = []string{}

			return nil, out, fmt.Errorf("the token for %s was not made: %w", env.Name, err)
		}
		out.Note = howAgentCalls
		if out.Token != "" {
			out.Note = "this is the only time the token is shown; " + out.Note
		}
		if cutOff {
			out.Note = note(out.Note, turnedAway)
		}

		return nil, out, nil
	})

	type agentTokenDeleteIn struct {
		ID      int  `json:"id"                jsonschema:"the token's number, as agent_list gives it"`
		Confirm bool `json:"confirm,omitempty" jsonschema:"revoke it: without this the answer says what would follow and nothing is revoked"`
	}
	type agentTokenDeleteOut struct {
		Token   *agentToken `json:"token,omitempty"    jsonschema:"the token as Dockhand has it now"`
		Revoked bool        `json:"revoked"            jsonschema:"the token was revoked, and is read back as one that no longer works"`
		WouldGo []string    `json:"would_go,omitempty" jsonschema:"what revoking it ends, said before anything is revoked"`
		Note    string      `json:"note,omitempty"`
	}
	add(r, deleteTool, &mcp.Tool{
		Name:        "agent_token_delete",
		Description: "Revoke one token a Hawser agent calls in to the Dockhand with, by the token's number: no agent can call in with it again, and it cannot be made to work again, though another can be made in its place. Revoking the token an agent is connected with cuts that agent off at once and leaves the environment with none, so its host is not reached until a new token is made and its agent started with it; an agent left running through it has its address turned away for five minutes, so stop it first. Dockhand keeps a revoked token on its list, marked as one that no longer works. Nothing on any host is touched. Without confirm the answer says what would follow and revokes nothing.",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in agentTokenDeleteIn) (*mcp.CallToolResult, agentTokenDeleteOut, error) {
		if in.ID < 1 {
			return nil, agentTokenDeleteOut{}, errors.New("id is required: the token's number, as agent_list gives it")
		}
		envs, err := r.environments(ctx)
		if err != nil {
			return nil, agentTokenDeleteOut{}, err
		}
		// Dockhand answers that it revoked a token whatever number it is given, so that there is one is looked for first
		found := func() (*agentToken, error) {
			tokens, failed := r.agentTokens(ctx, envs)
			if failed != nil {
				return nil, failed
			}
			at := slices.IndexFunc(tokens, func(t agentToken) bool { return t.ID == in.ID })
			if at < 0 {
				return nil, fmt.Errorf("there is no agent token numbered %d: agent_list gives the ones there are, with revoked for those that no longer work", in.ID)
			}

			return &tokens[at], nil
		}
		token, err := found()
		if err != nil {
			return nil, agentTokenDeleteOut{}, err
		}
		out := agentTokenDeleteOut{Token: token}
		if !token.Active {
			out.Note = "nothing to revoke: the token no longer works, having been revoked or replaced by a newer one"

			return nil, out, nil
		}

		// the agent connected with it is the one that is cut off, which is looked for before it is
		connected := ""
		if callers, failed := r.callers(ctx); failed == nil {
			if at := slices.IndexFunc(envs, func(e environment) bool { return e.Name == token.Env }); at >= 0 && callers[envs[at].ID] != nil {
				connected = callers[envs[at].ID].AgentName
			}
		}
		if !in.Confirm {
			out.WouldGo = []string{fmt.Sprintf("the token %s of %s (it begins %s): no agent can call in with it again", token.Name, token.Env, token.Starts)}
			out.Note = "nothing was revoked: call again with confirm to revoke it"
			if connected != "" {
				out.WouldGo = append(out.WouldGo, fmt.Sprintf("the call of the agent %s, which is connected with it now: %s is not reached until a new token is made and its agent started with it", connected, token.Env))
				out.Note = note(out.Note, turnedAway)
			}

			return nil, out, nil
		}

		if _, err := r.dock.DeleteHawserTokens(ctx, dockhand.DeleteHawserTokensOperationOptions{Id: &in.ID}); err != nil {
			return nil, out, fmt.Errorf("the token %s was not revoked: %w", token.Name, refusal(err))
		}
		after, err := found()
		switch {
		case err != nil:
			out.Note = "it was revoked, and the tokens could not be listed afterwards: " + err.Error()
		case after.Active:
			return nil, out, fmt.Errorf("Dockhand answered that it revoked the token %s, and lists it as one that still works", token.Name) //nolint:staticcheck // ST1005: the sentence begins with the server's name
		default:
			out.Token, out.Revoked = after, true
			out.Note = token.Env + " has no token that works now: agent_token_create makes one"
			if connected != "" {
				out.Note = note(out.Note, fmt.Sprintf("the agent %s was cut off; %s", connected, turnedAway))
			}
		}

		return nil, out, nil
	})
}
