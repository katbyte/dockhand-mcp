//go:build integration

package integration

import (
	"net/http"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/katbyte/dockhand-mcp/lib/testenv"
	"github.com/katbyte/dockhand-mcp/sdk/dockhand"
	"github.com/katbyte/pandorest/client"
)

// What the image tests bring to a host: small images from the mirror the suites' own image comes from, and names of their own for them.
const (
	smallImage   = "mirror.gcr.io/library/busybox:1.36"
	anotherImage = "mirror.gcr.io/library/alpine:3.20"
	noSuchImage  = "mirror.gcr.io/library/it-no-such-image:1"
)

// imageOf is the image a host lists under a name, or nil when it has none.
func imageOf(t *testing.T, env int, name string) *dockhand.GetImagesResponse {
	t.Helper()

	listed, err := dock(t).GetImages(t.Context(), dockhand.GetImagesOperationOptions{Env: &env})
	if err != nil {
		t.Fatalf("listing the images of environment %d: %v", env, err)
	}
	for i := range listed.Model {
		if slices.Contains(listed.Model[i].RepoTags, name) {
			return &listed.Model[i]
		}
	}

	return nil
}

// ended waits for a job to end and gives it as it then stands.
func ended(t *testing.T, id string) *dockhand.GetJobsByIdResponse {
	t.Helper()

	// five minutes: a scan fetches its scanner's whole list of known flaws on the way, which alone has taken two
	for range 600 {
		job, err := dock(t).GetJobsById(t.Context(), id)
		if err != nil {
			t.Fatalf("asking after job %s: %v", id, err)
		}
		if job.Model.Status != "running" {
			return job.Model
		}
		time.Sleep(500 * time.Millisecond)
	}
	t.Fatalf("job %s was still running after five minutes", id)

	return nil
}

// without removes images from a host from outside Dockhand, now and when the test ends: what a run that was cut short may have left, and what this
// one brings.
func without(t *testing.T, host string, names ...string) {
	t.Helper()

	ctx := noCancel(t)
	remove := func() { _, _ = world.Docker(ctx, host, append([]string{"rmi", "-f"}, names...)...) }
	remove()
	t.Cleanup(remove)
}

// Pulling an image answers one of two ways, by what the caller says it accepts: the work done first and how it went, or a job at once that says how
// it goes. Both are had through the one method, and a pull that fails answers 200 either way.
func TestImagePulledBothWays(t *testing.T) {
	c := dock(t)
	env := world.Env(testenv.Alpha)
	without(t, testenv.Alpha, smallImage)

	// asked as any call is, for JSON alone: Dockhand pulls and then answers
	waited, err := c.PostImagesPull(t.Context(), dockhand.PostImagesPullRequest{Image: smallImage}, dockhand.PostImagesPullOperationOptions{Env: &env})
	if err != nil || waited.Model.Status != "complete" || waited.Model.JobId != "" || waited.Model.Error != "" {
		t.Fatalf("a pull waited for = %+v, %v", waited.Model, err)
	}
	img := imageOf(t, env, smallImage)
	if img == nil || !strings.HasPrefix(img.Id, "sha256:") || img.Size <= 0 || img.Created <= 0 || img.Containers != 0 || len(img.RepoDigests) == 0 || !slices.Equal(img.RepoTags, img.Tags) {
		t.Fatalf("the image as listed once pulled = %+v", img)
	}

	// asked as a job: the id at once, and the job says how it goes and how it went
	started, err := c.PostImagesPull(t.Context(), dockhand.PostImagesPullRequest{Image: smallImage}, dockhand.PostImagesPullOperationOptions{Accept: dockhand.AcceptJob, Env: &env})
	if err != nil || started.Model.JobId == "" || started.Model.Status != "" {
		t.Fatalf("a pull asked as a job = %+v, %v", started.Model, err)
	}
	job := ended(t, started.Model.JobId)
	if job.Id != started.Model.JobId || job.Status != "done" || job.Result["status"] != "complete" || len(job.Lines) < 2 || job.Lines[len(job.Lines)-1].Event != "result" || job.Lines[0].Event != "progress" || job.Lines[0].Data["status"] == nil {
		t.Errorf("the job of a pull, once ended = %+v", job)
	}

	// an image there is none of: a 200 that says error, waited for, and a job that ends in one
	failed, err := c.PostImagesPull(t.Context(), dockhand.PostImagesPullRequest{Image: noSuchImage}, dockhand.PostImagesPullOperationOptions{Env: &env})
	if err != nil || failed.Model.Status != "error" || !strings.Contains(failed.Model.Error, "Failed to pull image") {
		t.Errorf("a pull of nothing, waited for = %+v, %v", failed.Model, err)
	}
	started, err = c.PostImagesPull(t.Context(), dockhand.PostImagesPullRequest{Image: noSuchImage}, dockhand.PostImagesPullOperationOptions{Accept: dockhand.AcceptJob, Env: &env})
	if err != nil || started.Model.JobId == "" {
		t.Fatalf("a pull of nothing asked as a job = %+v, %v", started.Model, err)
	}
	if job = ended(t, started.Model.JobId); job.Status != "error" || job.Result["success"] != false || !strings.Contains(asText(job.Result["error"]), "Failed to pull image") {
		t.Errorf("the job of a pull of nothing = %+v", job)
	}

	// a host there is none of, and no host at all, are a 200 that says error as well
	nowhere := 99999
	if lost, err := c.PostImagesPull(t.Context(), dockhand.PostImagesPullRequest{Image: smallImage}, dockhand.PostImagesPullOperationOptions{Env: &nowhere}); err != nil || lost.Model.Status != "error" || !strings.Contains(lost.Model.Error, "Environment 99999 not found") {
		t.Errorf("a pull to an environment there is none of = %+v, %v", lost.Model, err)
	}
	if lost, err := c.PostImagesPull(t.Context(), dockhand.PostImagesPullRequest{Image: smallImage}, dockhand.PostImagesPullOperationOptions{}); err != nil || lost.Model.Status != "error" || !strings.Contains(lost.Model.Error, "No environment specified") {
		t.Errorf("a pull to no environment = %+v, %v", lost.Model, err)
	}
}

// asText is a value of a free-form answer as text.
func asText(v any) string {
	s, isText := v.(string)
	if !isText {
		return ""
	}

	return s
}

// A job is asked after and asked to stop by its id; one Dockhand does not have is a 404 for both, and one that has ended is past stopping.
func TestJobsAskedAfterAndStopped(t *testing.T) {
	c := dock(t)
	env := world.Env(testenv.Alpha)
	without(t, testenv.Alpha, anotherImage)

	started, err := c.PostImagesPull(t.Context(), dockhand.PostImagesPullRequest{Image: anotherImage}, dockhand.PostImagesPullOperationOptions{Accept: dockhand.AcceptJob, Env: &env})
	if err != nil || started.Model.JobId == "" {
		t.Fatalf("a pull asked as a job = %+v, %v", started.Model, err)
	}
	// asked to stop while it runs: the request is taken, whatever Docker then does with a pull it has begun
	stopped, err := c.DeleteJobsById(t.Context(), started.Model.JobId)
	if err != nil || !yes(stopped.Model.Cancelled) {
		t.Errorf("a running job asked to stop = %+v, %v", stopped.Model, err)
	}
	job := ended(t, started.Model.JobId)
	if job.Status != "done" && job.Status != "error" {
		t.Errorf("the job asked to stop ended as %q", job.Status)
	}

	// asked to stop once it has ended: there is nothing to stop, and that is the answer rather than a refusal
	if again, err := c.DeleteJobsById(t.Context(), started.Model.JobId); err != nil || yes(again.Model.Cancelled) {
		t.Errorf("a job that has ended, asked to stop = %+v, %v", again.Model, err)
	}

	if _, err := c.GetJobsById(t.Context(), "00000000-0000-0000-0000-000000000000"); !client.IsNotFound(err) || !strings.Contains(err.Error(), "Job not found") {
		t.Errorf("a job there is none of = %v", err)
	}
	if _, err := c.DeleteJobsById(t.Context(), "00000000-0000-0000-0000-000000000000"); !client.IsNotFound(err) {
		t.Errorf("stopping a job there is none of = %v", err)
	}
}

// An image is told apart by its id alone: its history is read by it, a name is added by it, and it is removed by it, whole. A name with a slash in it
// is no id to Dockhand.
func TestImageDescribedTaggedAndRemoved(t *testing.T) {
	const spare = "it/spare:one"

	c := dock(t)
	env := world.Env(testenv.Alpha)
	without(t, testenv.Alpha, smallImage, spare)
	if _, err := world.Docker(t.Context(), testenv.Alpha, "pull", "-q", smallImage); err != nil {
		t.Fatal(err)
	}
	img := imageOf(t, env, smallImage)
	if img == nil {
		t.Fatalf("the host was given %s and Dockhand does not list it", smallImage)
	}

	history, err := c.GetImagesByIdHistory(t.Context(), img.Id, dockhand.GetImagesByIdHistoryOperationOptions{Env: &env})
	if err != nil || len(history.Model) == 0 || !slices.Contains(history.Model[0].Tags, smallImage) || history.Model[0].CreatedBy == "" || history.Model[0].Created <= 0 || history.Model[0].Size <= 0 {
		t.Errorf("its history = %+v, %v", history.Model, err)
	}
	if _, err := c.GetImagesByIdHistory(t.Context(), smallImage, dockhand.GetImagesByIdHistoryOperationOptions{Env: &env}); client.StatusCode(err) != http.StatusBadRequest || !strings.Contains(err.Error(), "Invalid image ID") {
		t.Errorf("its history asked by its name = %v", err)
	}

	tagged, err := c.PostImagesByIdTag(t.Context(), img.Id, dockhand.PostImagesByIdTagRequest{Repo: "it/spare", Tag: "one"}, dockhand.PostImagesByIdTagOperationOptions{Env: &env})
	if err != nil || !yes(tagged.Model.Success) {
		t.Fatalf("a second name = %+v, %v", tagged.Model, err)
	}
	if now := imageOf(t, env, spare); now == nil || now.Id != img.Id || !slices.Contains(now.RepoTags, smallImage) {
		t.Errorf("the image under its second name = %+v", now)
	}
	if _, err := c.PostImagesByIdTag(t.Context(), "sha256:0000", dockhand.PostImagesByIdTagRequest{Repo: "it/spare", Tag: "two"}, dockhand.PostImagesByIdTagOperationOptions{Env: &env}); client.StatusCode(err) != http.StatusInternalServerError {
		t.Errorf("a name for an image there is none of = %v", err)
	}

	// under two names it is not removed unless forced, and then it goes whole
	if _, err := c.DeleteImagesById(t.Context(), img.Id, dockhand.DeleteImagesByIdOperationOptions{Env: &env}); client.StatusCode(err) != http.StatusConflict || !strings.Contains(err.Error(), "must be forced") {
		t.Errorf("removing an image under two names, not forced = %v", err)
	}
	if _, err := c.DeleteImagesById(t.Context(), spare, dockhand.DeleteImagesByIdOperationOptions{Env: &env}); client.StatusCode(err) != http.StatusBadRequest {
		t.Errorf("removing one of its names = %v", err)
	}
	removed, err := c.DeleteImagesById(t.Context(), img.Id, dockhand.DeleteImagesByIdOperationOptions{Env: &env, Force: new(true)})
	if err != nil || !yes(removed.Model.Success) {
		t.Fatalf("removing it, forced = %+v, %v", removed.Model, err)
	}
	if imageOf(t, env, smallImage) != nil || imageOf(t, env, spare) != nil {
		t.Error("the image is still listed under one of its names after it was removed")
	}

	// one a running container was made from is refused, forced or not
	name := started(t, testenv.Alpha, "user")
	fixture := imageOf(t, env, world.Image)
	if fixture == nil || fixture.Containers < 1 {
		t.Fatalf("the image %s runs = %+v", name, fixture)
	}
	for _, force := range []bool{false, true} {
		if _, err := c.DeleteImagesById(t.Context(), fixture.Id, dockhand.DeleteImagesByIdOperationOptions{Env: &env, Force: &force}); client.StatusCode(err) != http.StatusConflict || !strings.Contains(err.Error(), "running container") {
			t.Errorf("removing an image a running container uses, force %v = %v", force, err)
		}
	}
}

// Pruning answers what Docker says went, by kind, through the agent as well as by the socket; images alone are pruned as a job when asked so, and
// left as they are only the ones with no name go.
func TestPruning(t *testing.T) {
	c := dock(t)
	const host = testenv.Beta
	env := world.Env(host)
	ctx := noCancel(t)

	exited, volume, network := named(t, "exited"), named(t, "volume"), named(t, "network")
	clean := func() {
		_, _ = world.Docker(ctx, host, "rm", "-f", exited)
		_, _ = world.Docker(ctx, host, "volume", "rm", "-f", volume)
		_, _ = world.Docker(ctx, host, "network", "rm", "-f", network)
		_, _ = world.Docker(ctx, host, "rmi", "-f", smallImage, anotherImage)
	}
	clean()
	t.Cleanup(clean)
	// a container that runs the suites' own image for as long as this does, so that a prune of what nothing uses has something it must leave
	started(t, host, "keeper")
	lay := func() string {
		t.Helper()

		for _, args := range [][]string{{"pull", "-q", smallImage}, {"volume", "create", volume}, {"network", "create", network}} {
			if _, err := world.Docker(ctx, host, args...); err != nil {
				t.Fatal(err)
			}
		}
		// the fixtures' own image, so that the container going leaves no image behind for the image prune to find
		id, err := world.Docker(ctx, host, "run", "-d", "--name", exited, world.Image, "true")
		if err != nil {
			t.Fatal(err)
		}
		// a prune passes over a container that has not exited yet, and this one is started and left to
		if _, err := world.Docker(ctx, host, "wait", exited); err != nil {
			t.Fatal(err)
		}

		return strings.TrimSpace(id)
	}

	containerID := lay()
	// left as it is, an image prune takes only what has no name
	nameless, err := c.PostPruneImages(t.Context(), dockhand.PostPruneImagesOperationOptions{Env: &env})
	if err != nil || !yes(nameless.Model.Success) || nameless.Model.Result == nil || nameless.Model.JobId != "" || imageOf(t, env, smallImage) == nil {
		t.Fatalf("a prune of nameless images = %+v, %v", nameless.Model, err)
	}

	containers, err := c.PostPruneContainers(t.Context(), dockhand.PostPruneContainersOperationOptions{Env: &env})
	if err != nil || !yes(containers.Model.Success) || containers.Model.Result == nil || !slices.Contains(containers.Model.Result.ContainersDeleted, containerID) {
		t.Errorf("a prune of stopped containers = %+v, %v, want %s among them", containers.Model.Result, err, containerID)
	}
	volumes, err := c.PostPruneVolumes(t.Context(), dockhand.PostPruneVolumesOperationOptions{Env: &env})
	if err != nil || !yes(volumes.Model.Success) || volumes.Model.Result == nil || !slices.Contains(volumes.Model.Result.VolumesDeleted, volume) {
		t.Errorf("a prune of unused volumes = %+v, %v, want %s among them", volumes.Model.Result, err, volume)
	}
	networks, err := c.PostPruneNetworks(t.Context(), dockhand.PostPruneNetworksOperationOptions{Env: &env})
	if err != nil || !yes(networks.Model.Success) || networks.Model.Result == nil || !slices.Contains(networks.Model.Result.NetworksDeleted, network) {
		t.Errorf("a prune of unused networks = %+v, %v, want %s among them", networks.Model.Result, err, network)
	}
	// every unused image, named or not, waited for
	images, err := c.PostPruneImages(t.Context(), dockhand.PostPruneImagesOperationOptions{Env: &env, Dangling: new(false)})
	if err != nil || !yes(images.Model.Success) || images.Model.Result == nil || images.Model.Result.SpaceReclaimed <= 0 || !slices.ContainsFunc(images.Model.Result.ImagesDeleted, func(d dockhand.PostPruneImagesResponseResultImagesDeleted) bool { return d.Untagged == smallImage }) || !slices.ContainsFunc(images.Model.Result.ImagesDeleted, func(d dockhand.PostPruneImagesResponseResultImagesDeleted) bool {
		return strings.HasPrefix(d.Deleted, "sha256:")
	}) {
		t.Errorf("a prune of every unused image = %+v, %v", images.Model.Result, err)
	}
	if imageOf(t, env, smallImage) != nil || imageOf(t, env, world.Image) == nil {
		t.Error("after the prune the unused image is still listed, or the one in use is not")
	}

	// and as a job, whose result is the same answer
	if _, err := world.Docker(ctx, host, "pull", "-q", anotherImage); err != nil {
		t.Fatal(err)
	}
	started, err := c.PostPruneImages(t.Context(), dockhand.PostPruneImagesOperationOptions{Accept: dockhand.AcceptJob, Env: &env, Dangling: new(false)})
	if err != nil || started.Model.JobId == "" || started.Model.Result != nil {
		t.Fatalf("a prune asked as a job = %+v, %v", started.Model, err)
	}
	job := ended(t, started.Model.JobId)
	report, isReport := job.Result["result"].(map[string]any)
	if job.Status != "done" || job.Result["success"] != true || !isReport || report["ImagesDeleted"] == nil || imageOf(t, env, anotherImage) != nil {
		t.Errorf("the job of a prune = %+v", job)
	}

	// all four at once: one report of each kind
	containerID = lay()
	all, err := c.PostPruneAll(t.Context(), dockhand.PostPruneAllOperationOptions{Env: &env})
	if err != nil || !yes(all.Model.Success) || all.Model.Result == nil {
		t.Fatalf("a prune of everything = %+v, %v", all.Model, err)
	}
	got := all.Model.Result
	if got.Containers == nil || !slices.Contains(got.Containers.ContainersDeleted, containerID) || got.Volumes == nil || !slices.Contains(got.Volumes.VolumesDeleted, volume) || got.Networks == nil || !slices.Contains(got.Networks.NetworksDeleted, network) || got.Images == nil || !slices.ContainsFunc(got.Images.ImagesDeleted, func(d dockhand.PostPruneAllResponseResultImagesImagesDeleted) bool { return d.Untagged == smallImage }) {
		t.Errorf("what a prune of everything took = containers %+v, volumes %+v, networks %+v, images %+v", got.Containers, got.Volumes, got.Networks, got.Images)
	}
	if imageOf(t, env, world.Image) == nil {
		t.Error("a prune of everything took the image a running container uses")
	}

	// an environment there is none of: refused for everything at once, and for images alone a 200 that says it failed
	nowhere := 99999
	if _, err := c.PostPruneAll(t.Context(), dockhand.PostPruneAllOperationOptions{Env: &nowhere}); client.StatusCode(err) != http.StatusInternalServerError || !strings.Contains(err.Error(), "not found") {
		t.Errorf("a prune of an environment there is none of = %v", err)
	}
	if lost, err := c.PostPruneImages(t.Context(), dockhand.PostPruneImagesOperationOptions{Env: &nowhere}); err != nil || yes(lost.Model.Success) || lost.Model.Error == "" {
		t.Errorf("a prune of the images of an environment there is none of = %+v, %v", lost.Model, err)
	}
}
