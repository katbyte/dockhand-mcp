//go:build integration

package integration

import (
	"slices"
	"strings"
	"testing"

	"github.com/katbyte/dockhand-mcp/lib/testenv"
	"github.com/katbyte/dockhand-mcp/sdk/dockhand"
	"github.com/katbyte/go-kt/mcp/acctest"
	"github.com/katbyte/pandorest/client"
)

// tokenOf is one agent token as the server lists it, or nil when it lists none by that number.
func tokenOf(t *testing.T, c *dockhand.Client, id int) *dockhand.GetHawserTokensResponse {
	t.Helper()

	listed, err := c.GetHawserTokens(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if at := slices.IndexFunc(listed.Model, func(k dockhand.GetHawserTokensResponse) bool { return k.Id == id }); at >= 0 {
		return &listed.Model[at]
	}

	return nil
}

// callerOf is the agent that has called in for an environment and is connected now, or nil when none is.
func callerOf(t *testing.T, c *dockhand.Client, env int) *dockhand.GetHawserConnectResponseConnections {
	t.Helper()

	answer, err := c.GetHawserConnect(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if at := slices.IndexFunc(answer.Model.Connections, func(k dockhand.GetHawserConnectResponseConnections) bool { return k.EnvironmentId == env }); at >= 0 {
		return &answer.Model.Connections[at]
	}

	return nil
}

// An environment an agent calls in for is saved with nothing but a name, is not reached until an agent calls with its token, and is cut off again
// when the token is replaced or revoked: an environment has one token that works.
func TestAnAgentCallsInWithAToken(t *testing.T) {
	c := dock(t)
	const host = testenv.Beta
	name := named(t, "far")

	made, err := c.PostEnvironments(t.Context(), dockhand.PostEnvironmentsRequest{Name: name, ConnectionType: testenv.ConnectionCaller})
	if err != nil {
		t.Fatal(err)
	}
	env := made.Model.Id
	ctx := noCancel(t)
	t.Cleanup(func() {
		if err := world.HangUp(ctx, host); err != nil {
			t.Error(err)
		}
		if _, err := c.DeleteEnvironmentsById(ctx, env); err != nil && !client.IsNotFound(err) {
			t.Errorf("removing %s: %v", name, err)
		}
	})
	if made.Model.ConnectionType != testenv.ConnectionCaller || made.Model.Host != "" || yes(made.Model.HasHawserToken) {
		t.Errorf("made = %+v", made.Model)
	}

	// nothing has called: trying it is an answer that says so, and its containers are none rather than an error
	tried, err := c.PostEnvironmentsByIdTest(t.Context(), env)
	if err != nil || yes(tried.Model.Success) || tried.Model.Error != "Edge agent is not connected" {
		t.Errorf("tried with no agent = %+v, %v", tried.Model, err)
	}
	if listed, err := c.GetContainers(t.Context(), dockhand.GetContainersOperationOptions{Env: &env}); err != nil || len(listed.Model) != 0 {
		t.Errorf("its containers with no agent = %+v, %v", listed.Model, err)
	}

	// a token: the whole of it is answered once, and listed afterwards by how it begins
	first, err := c.PostHawserTokens(t.Context(), dockhand.PostHawserTokensRequest{Name: name, EnvironmentId: env})
	if err != nil || len(first.Model.Token) < 32 || first.Model.TokenId == 0 {
		t.Fatalf("a token made = %+v, %v", first.Model, err)
	}
	if kept := tokenOf(t, c, first.Model.TokenId); kept == nil || !yes(kept.IsActive) || kept.EnvironmentId != env || kept.Name != name || kept.TokenPrefix != first.Model.Token[:8] || kept.LastUsed != "" || kept.ExpiresAt != "" || kept.CreatedAt == "" {
		t.Errorf("the token as listed = %+v", kept)
	}
	// what a token needs is asked for, and an environment there is none of is a server error rather than a refusal
	if _, err := c.PostHawserTokens(t.Context(), dockhand.PostHawserTokensRequest{EnvironmentId: env}); client.StatusCode(err) != 400 || !strings.Contains(err.Error(), "Token name is required") {
		t.Errorf("a token with no name = %v", err)
	}
	if _, err := c.PostHawserTokens(t.Context(), dockhand.PostHawserTokensRequest{Name: name, EnvironmentId: 99999}); client.StatusCode(err) != 500 {
		t.Errorf("a token for an environment there is none of = %v", err)
	}

	// an agent started with the token calls in, and the host is reached through it
	if err := world.CallIn(t.Context(), host, first.Model.Token); err != nil {
		t.Fatal(err)
	}
	if !acctest.Eventually(func() bool { return callerOf(t, c, env) != nil }) {
		t.Fatalf("the agent started with the token did not call in")
	}
	if caller := callerOf(t, c, env); caller.AgentName != "caller-"+host || caller.AgentVersion == "" || caller.DockerVersion == "" || caller.Hostname == "" || caller.ConnectedAt == "" || caller.LastHeartbeat == "" || !slices.Contains(caller.Capabilities, "exec") {
		t.Errorf("the agent as connected = %+v", caller)
	}
	if tried, err = c.PostEnvironmentsByIdTest(t.Context(), env); err != nil || !yes(tried.Model.Success) || tried.Model.Info == nil || tried.Model.Info.ServerVersion == "" || tried.Model.Hawser["agentName"] != "caller-"+host {
		t.Errorf("tried once its agent called = %+v, %v", tried.Model, err)
	}
	running := started(t, host, "seen")
	if listed, err := c.GetContainers(t.Context(), dockhand.GetContainersOperationOptions{Env: &env}); err != nil || !slices.ContainsFunc(listed.Model, func(k dockhand.GetContainersResponse) bool { return k.Name == running }) {
		t.Errorf("what runs on the host, read through the agent that called = %+v, %v", listed.Model, err)
	}
	// the environment remembers the agent that called, which one Dockhand calls is not remembered as
	if kept, err := c.GetEnvironmentsById(t.Context(), env); err != nil || kept.Model.HawserAgentName != "caller-"+host || kept.Model.HawserVersion == "" || kept.Model.HawserLastSeen == "" {
		t.Errorf("the environment once its agent called = %+v, %v", kept.Model, err)
	}
	if kept := tokenOf(t, c, first.Model.TokenId); kept == nil || kept.LastUsed == "" {
		t.Errorf("the token once used = %+v", kept)
	}

	// another token, one of the caller's own choosing and with an end: the one before stops working and its agent is cut off at once. The agent is
	// held still first: left to itself it would call again with the old token, and Dockhand turns away for five minutes every call from an address
	// that tried a token it does not take
	const own = "it-a-token-of-my-own-choosing"
	if err := world.Freeze(t.Context(), host); err != nil {
		t.Fatal(err)
	}
	if callerOf(t, c, env) == nil {
		t.Fatalf("the agent held still is no longer connected, before its token was touched")
	}
	second, err := c.PostHawserTokens(t.Context(), dockhand.PostHawserTokensRequest{Name: name + "-again", EnvironmentId: env, RawToken: own, ExpiresAt: "2099-01-01T00:00:00Z"})
	if err != nil || second.Model.Token != own {
		t.Fatalf("a token of the caller's own = %+v, %v", second.Model, err)
	}
	if !acctest.Eventually(func() bool { return callerOf(t, c, env) == nil }) {
		t.Errorf("the agent that called with the token before is still connected")
	}
	if old, now := tokenOf(t, c, first.Model.TokenId), tokenOf(t, c, second.Model.TokenId); old == nil || yes(old.IsActive) || now == nil || !yes(now.IsActive) || now.ExpiresAt != "2099-01-01T00:00:00Z" || now.TokenPrefix != own[:8] {
		t.Errorf("the token before and the one now = %+v, %+v", old, now)
	}

	// an agent started with the new one calls in; the token revoked, it is cut off, and the token stays listed as one that no longer works
	if err := world.CallIn(t.Context(), host, own); err != nil {
		t.Fatal(err)
	}
	if !acctest.Eventually(func() bool { return callerOf(t, c, env) != nil }) {
		t.Fatalf("the agent started with the new token did not call in")
	}
	if err := world.Freeze(t.Context(), host); err != nil {
		t.Fatal(err)
	}
	if revoked, err := c.DeleteHawserTokens(t.Context(), dockhand.DeleteHawserTokensOperationOptions{Id: &second.Model.TokenId}); err != nil || !yes(revoked.Model.Success) {
		t.Fatalf("revoking = %+v, %v", revoked.Model, err)
	}
	if !acctest.Eventually(func() bool { return callerOf(t, c, env) == nil }) {
		t.Errorf("the agent whose token was revoked is still connected")
	}
	if kept := tokenOf(t, c, second.Model.TokenId); kept == nil || yes(kept.IsActive) {
		t.Errorf("the token once revoked = %+v", kept)
	}
	// a number no token has is answered as revoked all the same, and none at all is asked for
	nobody := 99999
	if revoked, err := c.DeleteHawserTokens(t.Context(), dockhand.DeleteHawserTokensOperationOptions{Id: &nobody}); err != nil || !yes(revoked.Model.Success) {
		t.Errorf("revoking a token there is none of = %+v, %v", revoked.Model, err)
	}
	if _, err := c.DeleteHawserTokens(t.Context(), dockhand.DeleteHawserTokensOperationOptions{}); client.StatusCode(err) != 400 {
		t.Errorf("revoking no token = %v", err)
	}

	// the environment removed, its tokens go with it
	if _, err := c.DeleteEnvironmentsById(t.Context(), env); err != nil {
		t.Fatal(err)
	}
	if tokenOf(t, c, first.Model.TokenId) != nil || tokenOf(t, c, second.Model.TokenId) != nil {
		t.Errorf("the tokens of an environment that was removed are still listed")
	}
}

// What a host's Docker holds is counted by kind in Docker's own spelling, and it is where a volume's size is told; an environment there is none of is
// answered with no account rather than an error.
func TestDiskCountedByKind(t *testing.T) {
	c := dock(t)
	const host = testenv.Alpha
	env := world.Env(host)
	volume := named(t, "held")
	_, _ = world.Docker(t.Context(), host, "volume", "rm", "-f", volume)
	if _, err := world.Docker(t.Context(), host, "volume", "create", volume); err != nil {
		t.Fatal(err)
	}
	ctx := noCancel(t)
	t.Cleanup(func() {
		if _, err := world.Docker(ctx, host, "volume", "rm", "-f", volume); err != nil {
			t.Errorf("removing %s: %v", volume, err)
		}
	})
	// a megabyte written into the volume, by a container that has ended and so no longer holds it
	if _, err := world.Docker(t.Context(), host, "run", "--rm", "-v", volume+":/held", world.Image, "sh", "-c", "head -c 1048576 /dev/zero > /held/a-megabyte"); err != nil {
		t.Fatal(err)
	}
	writer := started(t, host, "writer", "sh", "-c", "head -c 2097152 /dev/zero > /two-megabytes; sleep 100000")

	answer, err := c.GetSystemDisk(t.Context(), dockhand.GetSystemDiskOperationOptions{Env: &env})
	if err != nil || answer.Model.DiskUsage == nil {
		t.Fatalf("the disk of %s = %+v, %v", host, answer.Model, err)
	}
	disk := answer.Model.DiskUsage
	if at := slices.IndexFunc(disk.Volumes, func(v dockhand.GetSystemDiskResponseDiskUsageVolumes) bool { return v.Name == volume }); at < 0 || disk.Volumes[at].UsageData == nil || disk.Volumes[at].UsageData.Size < 1048576 || disk.Volumes[at].UsageData.RefCount != 0 {
		t.Errorf("a volume with a megabyte in it that nothing mounts = %+v", disk.Volumes)
	}
	if at := slices.IndexFunc(disk.Containers, func(k dockhand.GetSystemDiskResponseDiskUsageContainers) bool {
		return slices.Contains(k.Names, "/"+writer)
	}); at < 0 || disk.Containers[at].SizeRw < 2097152 || disk.Containers[at].SizeRootFs <= disk.Containers[at].SizeRw || disk.Containers[at].State != "running" || disk.Containers[at].Image != world.Image {
		t.Errorf("a container that has written two megabytes = %+v", disk.Containers)
	}
	if at := slices.IndexFunc(disk.Images, func(i dockhand.GetSystemDiskResponseDiskUsageImages) bool {
		return slices.Contains(i.RepoTags, world.Image)
	}); at < 0 || disk.Images[at].Size <= 0 || disk.Images[at].Containers < 1 || !strings.HasPrefix(disk.Images[at].Id, "sha256:") || disk.LayersSize <= 0 {
		t.Errorf("the image the fixtures run from = %+v, of %d bytes of layers", disk.Images, disk.LayersSize)
	}

	// an environment there is none of has no account, and none named is refused
	nowhere := 99999
	if none, err := c.GetSystemDisk(t.Context(), dockhand.GetSystemDiskOperationOptions{Env: &nowhere}); err != nil || none.Model.DiskUsage != nil {
		t.Errorf("the disk of an environment there is none of = %+v, %v", none.Model, err)
	}
	if _, err := c.GetSystemDisk(t.Context(), dockhand.GetSystemDiskOperationOptions{}); client.StatusCode(err) != 400 || !strings.Contains(err.Error(), "Environment ID required") {
		t.Errorf("the disk of no environment = %v", err)
	}
}

// Dockhand shows its own filesystem, less the parts it keeps to itself: directories are listed, text files read and directories made, and what is
// kept back is refused in the same two words whichever is asked.
func TestOwnFilesystemListedReadAndAddedTo(t *testing.T) {
	c := dock(t)
	dir := "/srv/" + named(t, "own")
	file := dir + "/laid/note.txt"
	if err := world.Lay(t.Context(), file, "kept here\n"); err != nil {
		t.Fatal(err)
	}
	ctx := noCancel(t)
	t.Cleanup(func() {
		if err := world.Unlay(ctx, dir); err != nil {
			t.Errorf("removing %s: %v", dir, err)
		}
	})

	// the root: what it shows, and what it leaves out without a word
	root, err := c.GetSystemFiles(t.Context(), dockhand.GetSystemFilesOperationOptions{})
	if err != nil || root.Model.Path != "/" || root.Model.Parent != "" {
		t.Fatalf("the root = %+v, %v", root.Model, err)
	}
	shown := func(name string) bool {
		return slices.ContainsFunc(root.Model.Entries, func(e dockhand.GetSystemFilesResponseEntries) bool { return e.Name == name })
	}
	if !shown("srv") || !shown("app") || shown("etc") || shown("proc") || shown("root") {
		t.Errorf("the root shows %+v", root.Model.Entries)
	}

	listed, err := c.GetSystemFiles(t.Context(), dockhand.GetSystemFilesOperationOptions{Path: dir + "/laid"})
	if err != nil || listed.Model.Parent != dir || len(listed.Model.Entries) != 1 {
		t.Fatalf("a directory = %+v, %v", listed.Model, err)
	}
	if e := listed.Model.Entries[0]; e.Name != "note.txt" || e.Path != file || e.Type != "file" || e.Size != 10 || e.Mode != "644" || e.Mtime == "" {
		t.Errorf("a file in it = %+v", e)
	}
	for what, tc := range map[string]struct {
		path string
		want int
	}{
		"a part kept back":      {"/etc", 403},
		"one reached by ..":     {dir + "/../../etc", 403},
		"one that is not there": {dir + "/nowhere", 404},
		"one not from the root": {"srv", 404},
		"a file":                {file, 400},
	} {
		if _, err := c.GetSystemFiles(t.Context(), dockhand.GetSystemFilesOperationOptions{Path: tc.path}); client.StatusCode(err) != tc.want {
			t.Errorf("listing %s = %v, want %d", what, err, tc.want)
		}
	}

	read, err := c.GetSystemFilesContent(t.Context(), dockhand.GetSystemFilesContentOperationOptions{Path: file})
	if err != nil || read.Model.Content != "kept here\n" || read.Model.Path != file || read.Model.Size != 10 || read.Model.Mtime == "" {
		t.Errorf("a file read = %+v, %v", read.Model, err)
	}
	for what, tc := range map[string]struct {
		path string
		want int
	}{
		"Dockhand's own database": {"/app/data/db/dockhand.db", 403},
		"a file in a part kept":   {"/etc/passwd", 403},
		"one that is not there":   {dir + "/nowhere.txt", 404},
		"a directory":             {dir, 400},
		"no path":                 {"", 400},
	} {
		if _, err := c.GetSystemFilesContent(t.Context(), dockhand.GetSystemFilesContentOperationOptions{Path: tc.path}); client.StatusCode(err) != tc.want {
			t.Errorf("reading %s = %v, want %d", what, err, tc.want)
		}
	}

	// a directory made with the ones above it, once: a second go is refused as there already
	deep := dir + "/made/deeper"
	if made, err := c.PostSystemFiles(t.Context(), dockhand.PostSystemFilesRequest{Path: deep}); err != nil || !yes(made.Model.Success) || made.Model.Path != deep {
		t.Fatalf("a directory made = %+v, %v", made.Model, err)
	}
	if under, err := c.GetSystemFiles(t.Context(), dockhand.GetSystemFilesOperationOptions{Path: dir + "/made"}); err != nil || len(under.Model.Entries) != 1 || under.Model.Entries[0].Type != "directory" || under.Model.Entries[0].Name != "deeper" {
		t.Errorf("what was made = %+v, %v", under.Model, err)
	}
	for what, tc := range map[string]struct {
		path string
		want int
	}{
		"one that is there":     {deep, 409},
		"one in a part kept":    {"/etc/" + named(t, "x"), 403},
		"one not from the root": {"srv/x", 400},
		"no path":               {"", 400},
	} {
		if _, err := c.PostSystemFiles(t.Context(), dockhand.PostSystemFilesRequest{Path: tc.path}); client.StatusCode(err) != tc.want {
			t.Errorf("making %s = %v, want %d", what, err, tc.want)
		}
	}

	// and the Docker sockets Dockhand finds at the usual places on its own machine, each said to be there or not
	sockets, err := c.GetEnvironmentsDetectSocket(t.Context())
	if err != nil || sockets.Model.Homedir == "" {
		t.Errorf("the sockets Dockhand finds = %+v, %v", sockets.Model, err)
	}
	for _, socket := range sockets.Model.Sockets {
		if socket.Path == "" || socket.Name == "" || socket.Exists == nil {
			t.Errorf("a socket found = %+v", socket)
		}
	}
}
