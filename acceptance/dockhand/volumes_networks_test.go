//go:build acceptance

package dockhand_test

import (
	"net/http"
	"slices"
	"strings"
	"testing"

	"github.com/katbyte/dockhand-mcp/lib/testenv"
	"github.com/katbyte/dockhand-mcp/sdk/dockhand"
	"github.com/katbyte/pandorest/client"
)

// What Dockhand runs to look inside a volume, as Docker Hub names it, and where the suites fetch it from instead.
const (
	helperImage  = "busybox:latest"
	helperMirror = "mirror.gcr.io/library/busybox:latest"
)

// helperThere gives a host the image Dockhand runs to look inside a volume, from the mirror, so that looking does not have the host fetch it from
// Docker Hub in the middle of a test.
func helperThere(t *testing.T, host string) {
	t.Helper()

	ctx := noCancel(t)
	if _, err := world.Docker(ctx, host, "image", "inspect", helperImage); err == nil {
		return
	}
	for _, args := range [][]string{{"pull", "-q", helperMirror}, {"tag", helperMirror, helperImage}, {"rmi", helperMirror}} {
		if out, err := world.Docker(ctx, host, args...); err != nil {
			t.Fatalf("giving %s the helper image: %v: %s", host, err, out)
		}
	}
}

// volumeOf is the volume a host lists under a name, or nil when it has none.
func volumeOf(t *testing.T, env int, name string) *dockhand.GetVolumesAnswer {
	t.Helper()

	listed, err := dock(t).GetVolumes(t.Context(), dockhand.GetVolumesOptions{Env: dockhand.EnvironmentID(env)})
	if err != nil {
		t.Fatalf("listing the volumes of environment %d: %v", env, err)
	}
	for i := range listed.Answer {
		if listed.Answer[i].Name == name {
			return &listed.Answer[i]
		}
	}

	return nil
}

// networkOf is the network a host lists under a name, or nil when it has none.
func networkOf(t *testing.T, env int, name string) *dockhand.GetNetworksAnswer {
	t.Helper()

	listed, err := dock(t).GetNetworks(t.Context(), dockhand.GetNetworksOptions{Env: dockhand.EnvironmentID(env)})
	if err != nil {
		t.Fatalf("listing the networks of environment %d: %v", env, err)
	}
	for i := range listed.Answer {
		if listed.Answer[i].Name == name {
			return &listed.Answer[i]
		}
	}

	return nil
}

// A volume is made, listed with who mounts it, read on its own, copied and removed; one a container mounts is refused, and one there is none of is a
// 404 to a read and to a removal.
func TestVolumeMadeReadCopiedAndRemoved(t *testing.T) {
	c := dock(t)
	const host = testenv.Alpha
	env := world.Env(host)
	ctx := noCancel(t)
	name, copyName, user := named(t, "vol"), named(t, "copy"), named(t, "user")
	clean := func() {
		_, _ = world.Docker(ctx, host, "rm", "-f", user)
		_, _ = world.Docker(ctx, host, "volume", "rm", "-f", name, copyName)
	}
	clean()
	t.Cleanup(clean)

	made, err := c.PostVolumes(t.Context(), dockhand.PostVolumesRequest{Name: name, Labels: map[string]any{"made-by": "integration"}}, dockhand.PostVolumesOptions{Env: env})
	if err != nil || !made.Answer.Success || made.Answer.Name != name {
		t.Fatalf("made = %+v, %v", made.Answer, err)
	}
	// made a second time by the same name, it is answered as made again: Docker hands back the one that is there
	if again, err := c.PostVolumes(t.Context(), dockhand.PostVolumesRequest{Name: name}, dockhand.PostVolumesOptions{Env: env}); err != nil || !again.Answer.Success {
		t.Errorf("made a second time = %+v, %v", again.Answer, err)
	}

	listed := volumeOf(t, int(env), name)
	if listed == nil || listed.Driver != "local" || listed.Scope != "local" || !strings.Contains(listed.Mountpoint, name) || listed.Created == "" || listed.Labels["made-by"] != "integration" || len(listed.UsedBy) != 0 {
		t.Fatalf("as listed = %+v", listed)
	}

	// read on its own, by both of the calls that do it: Docker's own record, capitals and all
	one, err := c.GetVolumesByName(t.Context(), name, dockhand.GetVolumesByNameOptions{Env: env})
	if err != nil || one.Answer.Name != name || one.Answer.Driver != "local" || one.Answer.Labels["made-by"] != "integration" || one.Answer.CreatedAt.IsZero() || !strings.Contains(one.Answer.Mountpoint, name) {
		t.Errorf("read on its own = %+v, %v", one.Answer, err)
	}
	inspected, err := c.GetVolumesByNameInspect(t.Context(), name, dockhand.GetVolumesByNameInspectOptions{Env: env})
	if err != nil || inspected.Answer.Name != name || inspected.Answer.Scope != "local" || inspected.Answer.Labels["made-by"] != "integration" {
		t.Errorf("inspected = %+v, %v", inspected.Answer, err)
	}

	// mounted by a container, stopped or running, it lists who and is not removed
	if out, err := world.Docker(ctx, host, "run", "--name", user, "-v", name+":/d", world.Image, "sh", "-c", "echo kept > /d/note.txt"); err != nil {
		t.Fatalf("mounting it: %v: %s", err, out)
	}
	if mounted := volumeOf(t, int(env), name); mounted == nil || len(mounted.UsedBy) != 1 || mounted.UsedBy[0].ContainerName != user || len(mounted.UsedBy[0].ContainerId) != 64 {
		t.Errorf("as listed with a stopped container that mounts it = %+v", mounted)
	}
	if _, err := c.DeleteVolumesByName(t.Context(), name, dockhand.DeleteVolumesByNameOptions{Env: env}); client.StatusCode(err) != http.StatusConflict || !strings.Contains(err.Error(), "volume is in use") {
		t.Errorf("removing a volume a container mounts = %v", err)
	}

	// copied, files and all; and copied again onto the copy, which Dockhand does not refuse
	helperThere(t, host)
	copied, err := c.PostVolumesByNameClone(t.Context(), name, dockhand.PostVolumesByNameCloneRequest{Name: copyName}, dockhand.PostVolumesByNameCloneOptions{Env: env})
	if err != nil || !copied.Answer.Success || copied.Answer.Name != copyName {
		t.Fatalf("copied = %+v, %v", copied.Answer, err)
	}
	if out, err := world.Docker(ctx, host, "run", "--rm", "-v", copyName+":/d", world.Image, "cat", "/d/note.txt"); err != nil || strings.TrimSpace(out) != "kept" {
		t.Errorf("the file in the copy = %q, %v", out, err)
	}
	if onto, err := c.PostVolumesByNameClone(t.Context(), name, dockhand.PostVolumesByNameCloneRequest{Name: copyName}, dockhand.PostVolumesByNameCloneOptions{Env: env}); err != nil || !onto.Answer.Success {
		t.Errorf("copied onto a volume that is there = %+v, %v", onto.Answer, err)
	}
	if _, err := c.PostVolumesByNameClone(t.Context(), named(t, "none"), dockhand.PostVolumesByNameCloneRequest{Name: named(t, "never")}, dockhand.PostVolumesByNameCloneOptions{Env: env}); client.StatusCode(err) != http.StatusInternalServerError || !strings.Contains(err.Error(), "no such volume") {
		t.Errorf("copying a volume there is none of = %v", err)
	}

	removed, err := c.DeleteVolumesByName(t.Context(), copyName, dockhand.DeleteVolumesByNameOptions{Env: env})
	if err != nil || !removed.Answer.Success || volumeOf(t, int(env), copyName) != nil {
		t.Errorf("removing the copy = %+v, %v", removed.Answer, err)
	}
	if _, err := c.DeleteVolumesByName(t.Context(), copyName, dockhand.DeleteVolumesByNameOptions{Env: env}); !client.IsNotFound(err) || !strings.Contains(err.Error(), "no such volume") {
		t.Errorf("removing it a second time = %v", err)
	}
	if _, err := c.GetVolumesByName(t.Context(), copyName, dockhand.GetVolumesByNameOptions{Env: env}); !client.IsNotFound(err) {
		t.Errorf("reading a volume there is none of = %v", err)
	}
}

// What a volume holds is listed and read through a helper container Dockhand starts on the host and keeps until it is released.
func TestVolumeLookedInto(t *testing.T) {
	c := dock(t)
	const host = testenv.Beta
	env := world.Env(host)
	ctx := noCancel(t)
	name, missing := named(t, "vol"), named(t, "none")
	clean := func() { _, _ = world.Docker(ctx, host, "volume", "rm", "-f", name, missing) }
	clean()
	t.Cleanup(clean)
	helperThere(t, host)

	if out, err := world.Docker(ctx, host, "run", "--rm", "-v", name+":/d", world.Image, "sh", "-c", "mkdir /d/sub && echo deeper > /d/sub/more.txt && echo kept > /d/note.txt && head -c 1200000 /dev/zero > /d/big.bin"); err != nil {
		t.Fatalf("filling the volume: %v: %s", err, out)
	}
	containers := func() int {
		out, err := world.Docker(ctx, host, "ps", "-aq")
		if err != nil {
			t.Fatal(err)
		}

		return len(strings.Fields(out))
	}
	before := containers()

	top, err := c.GetVolumesByNameBrowse(t.Context(), name, dockhand.GetVolumesByNameBrowseOptions{Env: env})
	if err != nil || top.Answer.Path != "/" || top.Answer.HelperId == "" || top.Answer.IsInUse || len(top.Answer.Entries) != 3 {
		t.Fatalf("its top = %+v, %v", top.Answer, err)
	}
	entries := map[string]dockhand.GetVolumesByNameBrowseAnswerEntries{}
	for _, e := range top.Answer.Entries {
		entries[e.Name] = e
	}
	if note := entries["note.txt"]; note.Type != "file" || note.Size != 5 || note.Permissions == "" || note.Owner != "root" || note.Group != "root" || note.Modified == "" {
		t.Errorf("a file as listed = %+v", note)
	}
	if entries["sub"].Type != "directory" || entries["big.bin"].Size != 1200000 {
		t.Errorf("a directory and a large file as listed = %+v, %+v", entries["sub"], entries["big.bin"])
	}
	// the helper is a container on the host for as long as it is not released
	if during := containers(); during != before+1 {
		t.Errorf("the host had %d containers before the look and has %d during it, want one more: the helper", before, during)
	}

	inside, err := c.GetVolumesByNameBrowse(t.Context(), name, dockhand.GetVolumesByNameBrowseOptions{Env: env, Path: "/sub"})
	if err != nil || inside.Answer.Path != "/sub" || len(inside.Answer.Entries) != 1 || inside.Answer.Entries[0].Name != "more.txt" || inside.Answer.HelperId != top.Answer.HelperId {
		t.Errorf("a directory in it = %+v, %v", inside.Answer, err)
	}
	if _, err := c.GetVolumesByNameBrowse(t.Context(), name, dockhand.GetVolumesByNameBrowseOptions{Env: env, Path: "/nope"}); !client.IsNotFound(err) || !strings.Contains(err.Error(), "Directory not found") {
		t.Errorf("a directory there is none of = %v", err)
	}
	// a file asked for as a directory is answered alone, by where the helper has it
	if file, err := c.GetVolumesByNameBrowse(t.Context(), name, dockhand.GetVolumesByNameBrowseOptions{Env: env, Path: "/note.txt"}); err != nil || len(file.Answer.Entries) != 1 || !strings.HasSuffix(file.Answer.Entries[0].Name, "/note.txt") || file.Answer.Entries[0].Type != "file" {
		t.Errorf("a file asked for as a directory = %+v, %v", file.Answer, err)
	}

	read, err := c.GetVolumesByNameBrowseContent(t.Context(), name, dockhand.GetVolumesByNameBrowseContentOptions{Env: env, Path: "/sub/more.txt"})
	if err != nil || read.Answer.Content != "deeper\n" || read.Answer.Path != "/sub/more.txt" {
		t.Errorf("a file read = %+v, %v", read.Answer, err)
	}
	for path, status := range map[string]int{"/nope.txt": http.StatusNotFound, "/sub": http.StatusBadRequest, "/big.bin": http.StatusRequestEntityTooLarge} {
		if _, err := c.GetVolumesByNameBrowseContent(t.Context(), name, dockhand.GetVolumesByNameBrowseContentOptions{Env: env, Path: path}); client.StatusCode(err) != status {
			t.Errorf("reading %s = %v, want HTTP %d", path, err, status)
		}
	}

	// released, the helper is gone; and released again there is nothing to do, which is no failure
	for i := range 2 {
		if released, err := c.PostVolumesByNameBrowseRelease(t.Context(), name, dockhand.PostVolumesByNameBrowseReleaseOptions{Env: env}); err != nil || !released.Answer.Success {
			t.Errorf("release %d = %+v, %v", i+1, released.Answer, err)
		}
	}
	if after := containers(); after != before {
		t.Errorf("the host had %d containers before the look and has %d after the helper was released", before, after)
	}

	// looking into a volume there is none of makes it, empty: Docker's doing, when the helper is started with it mounted
	if volumeOf(t, int(env), missing) != nil {
		t.Fatalf("%s is there before it was looked into", missing)
	}
	ghost, err := c.GetVolumesByNameBrowse(t.Context(), missing, dockhand.GetVolumesByNameBrowseOptions{Env: env})
	if err != nil || len(ghost.Answer.Entries) != 0 {
		t.Errorf("a volume there is none of, looked into = %+v, %v", ghost.Answer, err)
	}
	if _, err := c.PostVolumesByNameBrowseRelease(t.Context(), missing, dockhand.PostVolumesByNameBrowseReleaseOptions{Env: env}); err != nil {
		t.Errorf("releasing its helper = %v", err)
	}
	if volumeOf(t, int(env), missing) == nil {
		t.Error("a volume there was none of is still not there after it was looked into: Dockhand no longer makes it, and volume_files' own check before it looks could go")
	}
}

// A network is made with a range of its own, listed with who is on it, read on its own, joined and left by a container, and removed; Docker's own are
// refused, and so is one a running container is on.
func TestNetworkMadeJoinedLeftAndRemoved(t *testing.T) {
	const subnet, gateway = "172.31.251.0/24", "172.31.251.1"

	c := dock(t)
	const host = testenv.Alpha
	env := world.Env(host)
	ctx := noCancel(t)
	name := named(t, "net")
	_, _ = world.Docker(ctx, host, "network", "rm", "-f", name)
	t.Cleanup(func() { _, _ = world.Docker(ctx, host, "network", "rm", "-f", name) })
	user := started(t, host, "user")

	made, err := c.PostNetworks(t.Context(), dockhand.PostNetworksRequest{Name: name, Driver: "bridge", Internal: new(true), Labels: map[string]any{"made-by": "integration"}, Ipam: &dockhand.PostNetworksRequestIpam{Config: []map[string]any{{"subnet": subnet, "gateway": gateway}}}}, dockhand.PostNetworksOptions{Env: env})
	if err != nil || !made.Answer.Success || len(made.Answer.Id) != 64 {
		t.Fatalf("made = %+v, %v", made.Answer, err)
	}
	id := made.Answer.Id
	if _, err := c.PostNetworks(t.Context(), dockhand.PostNetworksRequest{Name: name}, dockhand.PostNetworksOptions{Env: env}); client.StatusCode(err) != http.StatusInternalServerError || !strings.Contains(err.Error(), "already exists") {
		t.Errorf("made a second time = %v", err)
	}

	listed := networkOf(t, int(env), name)
	if listed == nil || listed.Id != id || listed.Driver != "bridge" || listed.Scope != "local" || !listed.Internal || listed.Ipam == nil || len(listed.Ipam.Config) != 1 || listed.Ipam.Config[0].Subnet != subnet || listed.Ipam.Config[0].Gateway != gateway || len(listed.Containers) != 0 {
		t.Fatalf("as listed = %+v", listed)
	}

	// read on its own, by name and by id, through both of the calls that do it: Docker's own record
	for _, by := range []string{name, id} {
		one, failed := c.GetNetworksById(t.Context(), by, dockhand.GetNetworksByIdOptions{Env: env})
		if failed != nil || one.Answer.Id != id || one.Answer.Name != name || !one.Answer.Internal || one.Answer.Attachable || one.Answer.EnableIPv6 || one.Answer.Created == "" || one.Answer.Labels["made-by"] != "integration" || one.Answer.IPAM == nil || len(one.Answer.IPAM.Config) != 1 || one.Answer.IPAM.Config[0].Subnet != subnet {
			t.Errorf("read on its own by %s = %+v, %v", by, one.Answer, failed)
		}
	}
	inspected, err := c.GetNetworksByIdInspect(t.Context(), id, dockhand.GetNetworksByIdInspectOptions{Env: env})
	if err != nil || inspected.Answer.Name != name || inspected.Answer.IPAM == nil || inspected.Answer.IPAM.Config[0].Gateway != gateway || len(inspected.Answer.Containers) != 0 {
		t.Errorf("inspected = %+v, %v", inspected.Answer, err)
	}

	// a container is put on it by its id: its name alone is not taken for it
	containerID, err := world.Docker(ctx, host, "inspect", "--format", "{{.Id}}", user)
	if err != nil {
		t.Fatal(err)
	}
	containerID = strings.TrimSpace(containerID)
	if _, err := c.PostNetworksByIdConnect(t.Context(), id, dockhand.PostNetworksByIdConnectRequest{ContainerName: user}, dockhand.PostNetworksByIdConnectOptions{Env: env}); client.StatusCode(err) != http.StatusBadRequest || !strings.Contains(err.Error(), "Container ID is required") {
		t.Errorf("connecting by a name alone = %v", err)
	}
	joined, err := c.PostNetworksByIdConnect(t.Context(), id, dockhand.PostNetworksByIdConnectRequest{ContainerId: containerID, ContainerName: user}, dockhand.PostNetworksByIdConnectOptions{Env: env})
	if err != nil || !joined.Answer.Success {
		t.Fatalf("connected = %+v, %v", joined.Answer, err)
	}
	if _, err := c.PostNetworksByIdConnect(t.Context(), id, dockhand.PostNetworksByIdConnectRequest{ContainerId: containerID}, dockhand.PostNetworksByIdConnectOptions{Env: env}); client.StatusCode(err) != http.StatusInternalServerError || !strings.Contains(err.Error(), "already exists in network") {
		t.Errorf("connected a second time = %v", err)
	}

	// on it, it is in the network's list and in Docker's record, with an address in the range
	if with := networkOf(t, int(env), name); with == nil || with.Containers[containerID].Name != user || !strings.HasPrefix(with.Containers[containerID].Ipv4Address, "172.31.251.") {
		t.Errorf("as listed with the container on it = %+v", with)
	}
	if rec, err := c.GetNetworksByIdInspect(t.Context(), id, dockhand.GetNetworksByIdInspectOptions{Env: env}); err != nil || rec.Answer.Containers[containerID].Name != user || !strings.HasPrefix(rec.Answer.Containers[containerID].IPv4Address, "172.31.251.") || rec.Answer.Containers[containerID].MacAddress == "" || rec.Answer.Containers[containerID].EndpointID == "" {
		t.Errorf("inspected with the container on it = %+v, %v", rec.Answer, err)
	}
	// and the container's own record names the network
	listedContainers, err := c.GetContainers(t.Context(), dockhand.GetContainersOptions{Env: env, All: new(true)})
	if err != nil {
		t.Fatal(err)
	}
	if at := slices.IndexFunc(listedContainers.Answer, func(lc dockhand.GetContainersAnswer) bool { return lc.Id == containerID }); at < 0 || !strings.HasPrefix(listedContainers.Answer[at].Networks[name].IpAddress, "172.31.251.") {
		t.Error("the container's own record does not name the network with its address")
	}

	// with a running container on it, it is not removed
	if _, err := c.DeleteNetworksById(t.Context(), id, dockhand.DeleteNetworksByIdOptions{Env: env}); client.StatusCode(err) != http.StatusInternalServerError || !strings.Contains(err.Error(), "has active endpoints") {
		t.Errorf("removing a network in use = %v", err)
	}

	left, err := c.PostNetworksByIdDisconnect(t.Context(), id, dockhand.PostNetworksByIdDisconnectRequest{ContainerId: containerID, ContainerName: user}, dockhand.PostNetworksByIdDisconnectOptions{Env: env})
	if err != nil || !left.Answer.Success {
		t.Fatalf("disconnected = %+v, %v", left.Answer, err)
	}
	if _, err := c.PostNetworksByIdDisconnect(t.Context(), id, dockhand.PostNetworksByIdDisconnectRequest{ContainerId: containerID, Force: new(true)}, dockhand.PostNetworksByIdDisconnectOptions{Env: env}); client.StatusCode(err) != http.StatusInternalServerError || !strings.Contains(err.Error(), "is not connected to network") {
		t.Errorf("disconnected a second time = %v", err)
	}

	removed, err := c.DeleteNetworksById(t.Context(), id, dockhand.DeleteNetworksByIdOptions{Env: env})
	if err != nil || !removed.Answer.Success || networkOf(t, int(env), name) != nil {
		t.Errorf("removed = %+v, %v", removed.Answer, err)
	}
	// one that is not there is a 500, to a read and to a removal alike; and Docker's own are refused in Docker's words
	if _, err := c.DeleteNetworksById(t.Context(), id, dockhand.DeleteNetworksByIdOptions{Env: env}); client.StatusCode(err) != http.StatusInternalServerError || !strings.Contains(err.Error(), "not found") {
		t.Errorf("removing it a second time = %v", err)
	}
	if _, err := c.GetNetworksById(t.Context(), id, dockhand.GetNetworksByIdOptions{Env: env}); client.StatusCode(err) != http.StatusInternalServerError {
		t.Errorf("reading a network there is none of = %v", err)
	}
	if _, err := c.DeleteNetworksById(t.Context(), "bridge", dockhand.DeleteNetworksByIdOptions{Env: env}); client.StatusCode(err) != http.StatusInternalServerError || !strings.Contains(err.Error(), "pre-defined network") {
		t.Errorf("removing the bridge = %v", err)
	}
	// a range that is none is refused in Docker's words
	if _, err := c.PostNetworks(t.Context(), dockhand.PostNetworksRequest{Name: name, Ipam: &dockhand.PostNetworksRequestIpam{Config: []map[string]any{{"subnet": "not-a-range"}}}}, dockhand.PostNetworksOptions{Env: env}); client.StatusCode(err) != http.StatusInternalServerError || networkOf(t, int(env), name) != nil {
		t.Errorf("a network with a range that is none = %v", err)
	}
}

// Docker prunes a network that no running container is on, whether or not a stopped one is, and the stopped one then cannot be started: which is why
// network_prune says who it would strand before it does.
func TestPruningANetworkStrandsAStoppedContainer(t *testing.T) {
	c := dock(t)
	const host = testenv.Beta
	env := world.Env(host)
	ctx := noCancel(t)
	name, user := named(t, "net"), named(t, "user")
	clean := func() {
		_, _ = world.Docker(ctx, host, "rm", "-f", user)
		_, _ = world.Docker(ctx, host, "network", "rm", "-f", name)
	}
	clean()
	t.Cleanup(clean)

	for _, args := range [][]string{{"network", "create", name}, {"run", "-d", "--name", user, "--network", name, world.Image, "sleep", "100000"}} {
		if out, err := world.Docker(ctx, host, args...); err != nil {
			t.Fatalf("%v: %v: %s", args, err, out)
		}
	}

	// with the container running, a prune leaves the network
	running, err := c.PostPruneNetworks(t.Context(), dockhand.PostPruneNetworksOptions{Env: env})
	if err != nil || slices.Contains(running.Answer.Result.NetworksDeleted, name) || networkOf(t, int(env), name) == nil {
		t.Fatalf("a prune with the container running = %+v, %v", running.Answer.Result, err)
	}

	// stopped, it is still on the network by its own record, and the network's list of who is on it still names it
	if out, err := world.Docker(ctx, host, "stop", "-t", "1", user); err != nil {
		t.Fatalf("stopping it: %v: %s", err, out)
	}
	listedContainers, err := c.GetContainers(t.Context(), dockhand.GetContainersOptions{Env: env, All: new(true)})
	if err != nil {
		t.Fatal(err)
	}
	at := slices.IndexFunc(listedContainers.Answer, func(lc dockhand.GetContainersAnswer) bool { return lc.Name == user })
	if _, onIt := listedContainers.Answer[max(at, 0)].Networks[name]; at < 0 || !onIt || listedContainers.Answer[at].State != "exited" {
		t.Fatalf("the stopped container's own record = %+v", listedContainers.Answer[max(at, 0)])
	}

	stopped, err := c.PostPruneNetworks(t.Context(), dockhand.PostPruneNetworksOptions{Env: env})
	if err != nil || !slices.Contains(stopped.Answer.Result.NetworksDeleted, name) || networkOf(t, int(env), name) != nil {
		t.Fatalf("a prune with the container stopped = %+v, %v: want the network taken", stopped.Answer.Result, err)
	}
	// and the container no longer starts
	if out, err := world.Docker(ctx, host, "start", user); err == nil || !strings.Contains(out, "not found") {
		t.Errorf("starting the container after its network was pruned = %q, %v: want it to fail for the network", out, err)
	}
}
