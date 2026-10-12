//go:build acceptance

package dockhand_test

import (
	"slices"
	"strings"
	"testing"

	"github.com/katbyte/dockhand-mcp/lib/testenv"
	"github.com/katbyte/dockhand-mcp/sdk/dockhand"
	"github.com/katbyte/pandorest/client"
)

// yes reads a flag the API may leave out.
func yes(flag *bool) bool { return flag != nil && *flag }

// The server is run the way the stage says, and says so itself: open, it answers whoever asks; with its login on, it answers the token this run
// minted and nobody else.
func TestTheServerIsRunAsTheStageSays(t *testing.T) {
	c := dock(t)

	session, err := c.GetAuthSession(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if session.Answer.AuthEnabled != (world.Stage == testenv.StageLogin) {
		t.Fatalf("the server says its login is on: %v, in the %s stage", session.Answer.AuthEnabled, world.Stage)
	}

	nobody, err := dockhand.NewWithoutLogin(world.Server)
	if err != nil {
		t.Fatal(err)
	}
	_, err = nobody.GetEnvironments(t.Context())

	if world.Stage == testenv.StageOpen {
		if err != nil {
			t.Errorf("an open server would not answer with nothing sent: %v", err)
		}

		return
	}

	// with the login on, nothing sent is turned away and says what to do about it
	if !client.IsUnauthorized(err) || !strings.Contains(err.Error(), "Authentication required") || !strings.Contains(err.Error(), "login switched on") {
		t.Errorf("a server with its login on, asked with nothing = %v", err)
	}
	// and so is a token it never minted, in the same words from the server
	stranger, err := dockhand.New(world.Server, "dh_0000000000000000000000000000000000000000000")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = stranger.GetEnvironments(t.Context()); !client.IsUnauthorized(err) || !strings.Contains(err.Error(), "the API token was refused") {
		t.Errorf("a token the server never made = %v", err)
	}

	// what stays open with the login on: whether the server is up
	if health, err := nobody.GetHealth(t.Context()); err != nil || health.Answer.Status != "ok" {
		t.Errorf("the health of a server with its login on, asked with nothing = %+v, %v", health.Answer, err)
	}

	// the token this run minted is listed, by its start and never whole
	tokens, err := c.GetAuthTokens(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	found := slices.ContainsFunc(tokens.Answer, func(token dockhand.GetAuthTokensAnswer) bool {
		return token.TokenPrefix != "" && strings.HasPrefix(strings.TrimPrefix(world.Token, "dh_"), token.TokenPrefix)
	})
	if !found {
		t.Errorf("the token this run minted is not among the %d listed", len(tokens.Answer))
	}

	// a token is not a session: asked with one, the session call still says nobody is logged in
	if session.Answer.Authenticated {
		t.Error("the session call says a bearer token is logged in, which it did not on 1.0.51: api-defs/README.md says otherwise")
	}
}

// The two hosts are environments, each reached its own way, and each answers: alpha through its socket, and beta through the Hawser agent in front of
// it, which says who it is.
func TestEnvironments(t *testing.T) {
	c := dock(t)

	listed, err := c.GetEnvironments(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	byName := map[string]dockhand.GetEnvironmentsAnswer{}
	for _, e := range listed.Answer {
		byName[e.Name] = e
	}

	alpha, beta := byName[testenv.Alpha], byName[testenv.Beta]
	if alpha.Id != world.Env(testenv.Alpha) || alpha.ConnectionType != testenv.ConnectionSocket || alpha.SocketPath != world.Socket || alpha.HasHawserToken || alpha.CreatedAt.IsZero() {
		t.Errorf("alpha as listed = %+v", alpha)
	}
	agentHost, agentPort, err := testenv.HostPort(world.Agent)
	if err != nil {
		t.Fatal(err)
	}
	if beta.Id != world.Env(testenv.Beta) || beta.ConnectionType != testenv.ConnectionAgent || beta.Host != agentHost || beta.Port != agentPort || !beta.HasHawserToken {
		t.Errorf("beta as listed = %+v", beta)
	}

	// one read alone is the same record
	one, err := c.GetEnvironmentsById(t.Context(), beta.Id)
	if err != nil {
		t.Fatal(err)
	}
	if one.Answer.Name != testenv.Beta || one.Answer.ConnectionType != testenv.ConnectionAgent || one.Answer.Host != agentHost || !one.Answer.HasHawserToken {
		t.Errorf("beta read alone = %+v", one.Answer)
	}

	// each answers when tried, and the agent says who it is
	for _, host := range []string{testenv.Alpha, testenv.Beta} {
		tried, err := c.PostEnvironmentsByIdTest(t.Context(), world.Env(host))
		if err != nil {
			t.Fatalf("%s: %v", host, err)
		}
		if !tried.Answer.Success || tried.Answer.Info == nil || tried.Answer.Info.ServerVersion == "" {
			t.Errorf("%s tried = %+v, %s", host, tried.Answer, tried.Answer.Error)
		}
		if host == testenv.Beta && tried.Answer.Hawser["agentName"] != "agent-beta" {
			t.Errorf("the agent before beta says it is %v, want agent-beta", tried.Answer.Hawser["agentName"])
		}
		if host == testenv.Alpha && len(tried.Answer.Hawser) != 0 {
			t.Errorf("a socket has no agent, and alpha's says %v", tried.Answer.Hawser)
		}
	}

	// an environment that does not exist is said to be that
	if _, err := c.GetEnvironmentsById(t.Context(), 99999); !client.IsNotFound(err) || !strings.Contains(err.Error(), "not found") {
		t.Errorf("an environment that is not there = %v", err)
	}
}

// A connection can be tried before it is saved, every way a host is reached, and one that fails says why in an answer that is no error.
func TestEnvironmentConnectionsAreTriedUnsaved(t *testing.T) {
	c := dock(t)

	agentHost, agentPort, err := testenv.HostPort(world.Agent)
	if err != nil {
		t.Fatal(err)
	}
	directHost, directPort, err := testenv.HostPort(world.Direct)
	if err != nil {
		t.Fatal(err)
	}

	for name, tc := range map[string]struct {
		request dockhand.PostEnvironmentsTestRequest
		refused string
	}{
		"a socket":                    {dockhand.PostEnvironmentsTestRequest{ConnectionType: testenv.ConnectionSocket, SocketPath: world.Socket}, ""},
		"an address":                  {dockhand.PostEnvironmentsTestRequest{ConnectionType: testenv.ConnectionDirect, Host: directHost, Port: directPort, Protocol: "http"}, ""},
		"an agent":                    {dockhand.PostEnvironmentsTestRequest{ConnectionType: testenv.ConnectionAgent, Host: agentHost, Port: agentPort, Protocol: "http", HawserToken: world.AgentToken}, ""},
		"an agent with another token": {dockhand.PostEnvironmentsTestRequest{ConnectionType: testenv.ConnectionAgent, Host: agentHost, Port: agentPort, Protocol: "http", HawserToken: "not-the-token"}, "Invalid token"},
		"an address nobody is at":     {dockhand.PostEnvironmentsTestRequest{ConnectionType: testenv.ConnectionDirect, Host: "nobody-home", Port: directPort, Protocol: "http"}, "fetch failed"},
	} {
		tried, failed := c.PostEnvironmentsTest(t.Context(), tc.request)
		if failed != nil {
			t.Errorf("%s: %v", name, failed)

			continue
		}
		if tc.refused == "" && (!tried.Answer.Success || tried.Answer.Error != "") {
			t.Errorf("%s = %+v, want it reached", name, tried.Answer)
		}
		if tc.refused != "" && (tried.Answer.Success || !strings.Contains(tried.Answer.Error, tc.refused)) {
			t.Errorf("%s = %+v, want it to fail saying %q", name, tried.Answer, tc.refused)
		}
	}

	// nothing tried was saved
	listed, err := c.GetEnvironments(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if len(listed.Answer) != len(world.Environments) {
		t.Errorf("%d environments after trying connections, want the %d there were", len(listed.Answer), len(world.Environments))
	}
}

// An environment is made, changed and removed, and each is read back from the server: the same Docker as alpha, reached a third way, by its address.
func TestEnvironmentMadeChangedAndRemoved(t *testing.T) {
	c := dock(t)

	host, port, err := testenv.HostPort(world.Direct)
	if err != nil {
		t.Fatal(err)
	}
	name := named(t, "gamma")
	made, err := c.PostEnvironments(t.Context(), dockhand.PostEnvironmentsRequest{Name: name, ConnectionType: testenv.ConnectionDirect, Host: host, Port: port, Protocol: "http", Labels: []string{"made"}, CollectMetrics: new(false)})
	if err != nil {
		t.Fatal(err)
	}
	id := made.Answer.Id
	ctx := noCancel(t)
	t.Cleanup(func() {
		if _, err := c.DeleteEnvironmentsById(ctx, id); err != nil && !client.IsNotFound(err) {
			t.Errorf("removing %s: %v", name, err)
		}
	})
	if id == 0 || made.Answer.Name != name || made.Answer.ConnectionType != testenv.ConnectionDirect || made.Answer.Host != host || made.Answer.Port != port || made.Answer.CollectMetrics || made.Answer.Labels != `["made"]` {
		t.Errorf("made = %+v", made.Answer)
	}

	// read back, the labels are the list they were given as: only the answer to making it has them as text
	if fresh, err := c.GetEnvironmentsById(t.Context(), id); err != nil || !slices.Equal(fresh.Answer.Labels, []string{"made"}) || fresh.Answer.CollectMetrics {
		t.Errorf("read back once made = %+v, %v", fresh.Answer, err)
	}

	// a second by the same name is refused, in the server's words
	if _, err := c.PostEnvironments(t.Context(), dockhand.PostEnvironmentsRequest{Name: name, ConnectionType: testenv.ConnectionDirect, Host: host, Port: port, Protocol: "http"}); client.StatusCode(err) != 409 || !strings.Contains(err.Error(), "already exists") {
		t.Errorf("a second environment named %s = %v", name, err)
	}

	changed, err := c.PutEnvironmentsById(t.Context(), id, dockhand.PutEnvironmentsByIdRequest{Labels: []string{"changed", "twice"}, CollectMetrics: new(true)})
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(changed.Answer.Labels, []string{"changed", "twice"}) || !changed.Answer.CollectMetrics || changed.Answer.Name != name {
		t.Errorf("changed = %+v: what was sent is changed and the name, which was not, is kept", changed.Answer)
	}
	read, err := c.GetEnvironmentsById(t.Context(), id)
	if err != nil || !slices.Equal(read.Answer.Labels, []string{"changed", "twice"}) || !read.Answer.CollectMetrics || read.Answer.Host != host {
		t.Errorf("read back = %+v, %v", read.Answer, err)
	}

	// its public address is kept when a change says nothing of it, and an empty one is nothing said: the generated call leaves it out, so forgetting
	// it is a call of its own
	if set, err := c.PutEnvironmentsById(t.Context(), id, dockhand.PutEnvironmentsByIdRequest{PublicIp: "192.0.2.10"}); err != nil || set.Answer.PublicIp != "192.0.2.10" {
		t.Errorf("a public address set = %+v, %v", set.Answer, err)
	}
	if kept, err := c.PutEnvironmentsById(t.Context(), id, dockhand.PutEnvironmentsByIdRequest{PublicIp: ""}); err != nil || kept.Answer.PublicIp != "192.0.2.10" {
		t.Errorf("a change that says nothing of the public address = %+v, %v", kept.Answer, err)
	}
	if forgot, err := c.ForgetEnvironmentPublicIP(t.Context(), int(id)); err != nil || forgot.Answer.PublicIp != "" || forgot.Answer.Name != name {
		t.Errorf("the public address forgotten = %+v, %v", forgot.Answer, err)
	}

	// what Dockhand does for the host by itself: each read as a new host has it, saved, and read back
	settings(t, c, int(id))

	removed, err := c.DeleteEnvironmentsById(t.Context(), id)
	if err != nil || !removed.Answer.Success {
		t.Fatalf("removing = %+v, %v", removed.Answer, err)
	}
	if _, err := c.GetEnvironmentsById(t.Context(), id); !client.IsNotFound(err) {
		t.Errorf("read after it was removed = %v", err)
	}
}

// settings reads, saves and reads back what Dockhand does for an environment by itself, and holds it to what a 1.0.51 server does: it takes a
// timezone only when there is one, and saves a schedule whatever its time says.
func settings(t *testing.T, c *dockhand.Client, id int) {
	t.Helper()

	if zone, err := c.GetEnvironmentsByIdTimezone(t.Context(), dockhand.EnvironmentID(id)); err != nil || zone.Answer.Timezone != "UTC" {
		t.Errorf("a new host's timezone = %+v, %v", zone.Answer, err)
	}
	if set, err := c.PostEnvironmentsByIdTimezone(t.Context(), dockhand.EnvironmentID(id), dockhand.PostEnvironmentsByIdTimezoneRequest{Timezone: "Europe/Berlin"}); err != nil || !set.Answer.Success || set.Answer.Timezone != "Europe/Berlin" {
		t.Errorf("setting the timezone = %+v, %v", set.Answer, err)
	}
	if _, err := c.PostEnvironmentsByIdTimezone(t.Context(), dockhand.EnvironmentID(id), dockhand.PostEnvironmentsByIdTimezoneRequest{Timezone: "Mars/Olympus"}); client.StatusCode(err) != 400 {
		t.Errorf("a timezone there is none of = %v", err)
	}
	if zone, err := c.GetEnvironmentsByIdTimezone(t.Context(), dockhand.EnvironmentID(id)); err != nil || zone.Answer.Timezone != "Europe/Berlin" {
		t.Errorf("the timezone read back = %+v, %v", zone.Answer, err)
	}

	if check, err := c.GetEnvironmentsByIdUpdateCheck(t.Context(), dockhand.EnvironmentID(id)); err != nil || check.Answer.Settings == nil || check.Answer.Settings.Enabled || check.Answer.Settings.Cron != "0 4 * * *" || check.Answer.Settings.VulnerabilityCriteria != "never" {
		t.Errorf("a new host's update check = %+v, %v", check.Answer.Settings, err)
	}
	// saved whatever its time says: a schedule nothing can read is kept, and never runs
	saved, err := c.PostEnvironmentsByIdUpdateCheck(t.Context(), dockhand.EnvironmentID(id), dockhand.PostEnvironmentsByIdUpdateCheckRequest{Enabled: new(true), Cron: "not a time", AutoUpdate: new(true), VulnerabilityCriteria: "critical"})
	if err != nil || !saved.Answer.Success || saved.Answer.Settings == nil || saved.Answer.Settings.Cron != "not a time" || !saved.Answer.Settings.AutoUpdate {
		t.Errorf("saving an update check = %+v, %v", saved.Answer, err)
	}
	if check, err := c.GetEnvironmentsByIdUpdateCheck(t.Context(), dockhand.EnvironmentID(id)); err != nil || !check.Answer.Settings.Enabled || check.Answer.Settings.VulnerabilityCriteria != "critical" {
		t.Errorf("the update check read back = %+v, %v", check.Answer.Settings, err)
	}

	if prune, err := c.GetEnvironmentsByIdImagePrune(t.Context(), dockhand.EnvironmentID(id)); err != nil || prune.Answer.Settings == nil || prune.Answer.Settings.Enabled || prune.Answer.Settings.PruneMode != "dangling" {
		t.Errorf("a new host's image prune = %+v, %v", prune.Answer.Settings, err)
	}
	if set, err := c.PostEnvironmentsByIdImagePrune(t.Context(), dockhand.EnvironmentID(id), dockhand.PostEnvironmentsByIdImagePruneRequest{Enabled: new(true), CronExpression: "0 2 * * 6", PruneMode: "all"}); err != nil || !set.Answer.Success || set.Answer.Settings.PruneMode != "all" || set.Answer.Settings.CronExpression != "0 2 * * 6" {
		t.Errorf("saving an image prune = %+v, %v", set.Answer, err)
	}
	// run now, outside its schedule
	if ran, err := c.PutEnvironmentsByIdImagePrune(t.Context(), dockhand.EnvironmentID(id)); err != nil || !ran.Answer.Success {
		t.Errorf("an image prune run now = %+v, %v", ran.Answer, err)
	}

	if warning, err := c.GetEnvironmentsByIdDiskWarning(t.Context(), dockhand.EnvironmentID(id)); err != nil || !warning.Answer.Enabled || warning.Answer.Mode != "percentage" || warning.Answer.Threshold != 80 {
		t.Errorf("a new host's disk warning = %+v, %v", warning.Answer, err)
	}
	if set, err := c.PostEnvironmentsByIdDiskWarning(t.Context(), dockhand.EnvironmentID(id), dockhand.PostEnvironmentsByIdDiskWarningRequest{Enabled: new(false), Mode: "absolute", ThresholdGb: 20}); err != nil || !set.Answer.Success {
		t.Errorf("saving a disk warning = %+v, %v", set.Answer, err)
	}
	if warning, err := c.GetEnvironmentsByIdDiskWarning(t.Context(), dockhand.EnvironmentID(id)); err != nil || warning.Answer.Enabled || warning.Answer.Mode != "absolute" || warning.Answer.ThresholdGb != 20 || warning.Answer.Threshold != 80 {
		t.Errorf("the disk warning read back = %+v, %v", warning.Answer, err)
	}

	// the folder stack files are staged in: set, and forgotten by a save that names none
	if set, err := c.PostEnvironmentsByIdRemoteStacksDir(t.Context(), dockhand.EnvironmentID(id), dockhand.PostEnvironmentsByIdRemoteStacksDirRequest{RemoteStacksDir: "/srv/stacks"}); err != nil || !set.Answer.Success || set.Answer.RemoteStacksDir != "/srv/stacks" {
		t.Errorf("setting the remote stacks folder = %+v, %v", set.Answer, err)
	}
	if _, err := c.PostEnvironmentsByIdRemoteStacksDir(t.Context(), dockhand.EnvironmentID(id), dockhand.PostEnvironmentsByIdRemoteStacksDirRequest{}); err != nil {
		t.Errorf("forgetting the remote stacks folder: %v", err)
	}
	if dir, err := c.GetEnvironmentsByIdRemoteStacksDir(t.Context(), dockhand.EnvironmentID(id)); err != nil || dir.Answer.RemoteStacksDir != "" {
		t.Errorf("the remote stacks folder once forgotten = %+v, %v", dir.Answer, err)
	}

	// the machine behind it, and its Docker: reached at an address with no agent, it is not said how long the machine has been up or how much memory
	// is free
	env := id
	if host, err := c.GetHost(t.Context(), dockhand.GetHostOptions{Env: dockhand.EnvironmentID(env)}); err != nil || host.Answer.Hostname == "" || host.Answer.Cpus < 1 || host.Answer.TotalMemory <= 0 || host.Answer.Uptime != 0 || host.Answer.FreeMemory != 0 || host.Answer.DockerVersion == "" {
		t.Errorf("the machine = %+v, %v", host.Answer, err)
	}
	if system, err := c.GetSystem(t.Context(), dockhand.GetSystemOptions{Env: dockhand.EnvironmentID(env)}); err != nil || system.Answer.Docker["kernelVersion"] == "" || system.Answer.Host["storageDriver"] == "" || system.Answer.Stats["containers"] == nil {
		t.Errorf("what Docker says of itself = %+v, %v", system.Answer, err)
	}
}

// The server says what it is: its newest release, each change in it by kind, and the state of its own database.
func TestTheServerDescribesItself(t *testing.T) {
	c := dock(t)

	changelog, err := c.GetChangelog(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if len(changelog.Answer) == 0 || changelog.Answer[0].Version == "" || changelog.Answer[0].Date == "" || len(changelog.Answer[0].Changes) == 0 || changelog.Answer[0].Changes[0].Type == "" || changelog.Answer[0].Changes[0].Text == "" {
		t.Errorf("the newest release = %+v", changelog.Answer[:min(1, len(changelog.Answer))])
	}

	database, err := c.GetHealthDatabase(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if !database.Answer.Healthy || database.Answer.Database == "" || database.Answer.SchemaVersion == "" || database.Answer.Tables == nil || database.Answer.Tables.Expected == 0 || database.Answer.Tables.Expected != database.Answer.Tables.Found {
		t.Errorf("the database = %+v, tables %+v", database.Answer, database.Answer.Tables)
	}
}

// What runs on a host is listed for that host and no other, started through Dockhand or not, and a host is named by its number.
func TestContainersAreListedByHost(t *testing.T) {
	c := dock(t)

	onAlpha, onBeta := started(t, testenv.Alpha, "one"), started(t, testenv.Beta, "two")
	for host, want := range map[string]string{testenv.Alpha: onAlpha, testenv.Beta: onBeta} {
		env := world.Env(host)
		listed, err := c.GetContainers(t.Context(), dockhand.GetContainersOptions{Env: env, All: new(true)})
		if err != nil {
			t.Fatalf("%s: %v", host, err)
		}

		names := make([]string, 0, len(listed.Answer))
		for _, container := range listed.Answer {
			names = append(names, container.Name)
			if container.Name == want && (container.Id == "" || container.Image != world.Image || container.State != "running" || !strings.HasPrefix(container.Status, "Up")) {
				t.Errorf("%s on %s = %+v", want, host, container)
			}
		}
		if !slices.Contains(names, want) || slices.Contains(names, onAlpha) != (host == testenv.Alpha) || slices.Contains(names, onBeta) != (host == testenv.Beta) {
			t.Errorf("%s lists %v, want %s and nothing of the other host's", host, names, want)
		}
	}

	// a host that is not there is said to be that, and no host at all is answered with nothing rather than refused
	if _, err := c.GetContainers(t.Context(), dockhand.GetContainersOptions{Env: 99999}); !client.IsNotFound(err) || !strings.Contains(err.Error(), "Environment not found") {
		t.Errorf("the containers of a host that is not there = %v", err)
	}
	if none, err := c.GetContainers(t.Context(), dockhand.GetContainersOptions{}); err != nil || len(none.Answer) != 0 {
		t.Errorf("the containers of no host = %d, %v: 1.0.51 answered an empty list", len(none.Answer), err)
	}
}
