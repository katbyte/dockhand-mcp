package tools

import (
	"cmp"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"slices"
	"strings"
	"time"

	"github.com/katbyte/dockhand-mcp/sdk/dockhand"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

const (
	// noTag is what Docker lists for an image with no name left: one a newer image of the same name took the name from
	noTag = "<none>:<none>"
	// digestPrefix leads an image's id
	digestPrefix = "sha256:"
	// defaultImages is how many images image_list lists unless told
	defaultImages = 200
)

// megabytes is a size in bytes as Docker shows one: in millions of bytes, to one place.
func megabytes(bytes int) float64 {
	return math.Round(float64(bytes)/1e5) / 10
}

// shortImageID is the start of an image's id, as docker shows it.
func shortImageID(id string) string {
	id = strings.TrimPrefix(id, digestPrefix)
	if len(id) > shortID {
		return id[:shortID]
	}

	return id
}

// tagsOf is the names and tags an image goes by on its host, sorted, and none for an image that has no name left.
func tagsOf(img *dockhand.GetImagesAnswer) []string {
	tags := make([]string, 0, len(img.RepoTags))
	for _, tag := range img.RepoTags {
		if tag != "" && tag != noTag {
			tags = append(tags, tag)
		}
	}
	slices.Sort(tags)

	return tags
}

// imageRow is one image of a host as image_list answers with it.
type imageRow struct {
	Env        string   `json:"env"                jsonschema:"the environment (Docker host) the image is on"`
	ID         string   `json:"id"                 jsonschema:"the start of the image's id, as docker shows it"`
	Tags       []string `json:"tags"               jsonschema:"every name and tag the image goes by on the host; none for an image a newer one of the same name left behind"`
	SizeMB     float64  `json:"size_mb"            jsonschema:"the image's size in megabytes; images share layers, so the sizes of several add up to more than the disk they take"`
	Created    string   `json:"created,omitempty"  jsonschema:"when the image was built, in UTC"`
	Containers int      `json:"containers"         jsonschema:"how many containers on the host were made from it, running or not; 0 is an image nothing uses"`
	Untagged   bool     `json:"untagged,omitempty" jsonschema:"the image has no name left: a newer image took it"`
}

// imageRowOf is an image of an environment as the tools give one.
func imageRowOf(env string, img *dockhand.GetImagesAnswer) imageRow {
	row := imageRow{Env: env, ID: shortImageID(img.Id), Tags: tagsOf(img), SizeMB: megabytes(img.Size), Containers: img.Containers}
	row.Untagged = len(row.Tags) == 0
	if img.Created > 0 {
		row.Created = time.Unix(int64(img.Created), 0).UTC().Format(time.RFC3339)
	}

	return row
}

// imagesOf is every image on an environment.
func (r *registry) imagesOf(ctx context.Context, env environment) ([]dockhand.GetImagesAnswer, error) {
	listed, err := r.dock.GetImages(ctx, dockhand.GetImagesOptions{Env: env.ID})
	if err != nil {
		return nil, refusal(err)
	}

	return listed.Answer, nil
}

// imageArg is the arguments of a tool about one image: which, and where to look for it.
type imageArg struct {
	envArg

	Image string `json:"image" jsonschema:"the image, by a name and tag it goes by (nginx:1.27, ghcr.io/org/app:v2), by its name alone or the end of it (app, or app:v2) when that is one image, or by the start of its id, as image_list gives them"`
}

// locatedImage is one image and the environment it is on.
type locatedImage struct {
	Env   environment
	Image dockhand.GetImagesAnswer
}

// repository is the name of an image without its tag: what is before the last colon, unless that colon is a registry's port.
func repository(tag string) string {
	if i := strings.LastIndex(tag, ":"); i > strings.LastIndex(tag, "/") {
		return tag[:i]
	}

	return tag
}

// matchImage says how an image answers to what a call names: by one of its names and tags in full, by its name alone or the end of it (busybox for
// registry.example/library/busybox:1.37), by the start of its id, or not at all.
func matchImage(img *dockhand.GetImagesAnswer, want string) (exact, byName, byID bool) {
	for _, tag := range tagsOf(img) {
		switch name := repository(tag); {
		case tag == want:
			exact = true
		case name == want, strings.HasSuffix(name, "/"+want), strings.HasSuffix(tag, "/"+want):
			byName = true
		default:
		}
	}
	id := strings.TrimPrefix(want, digestPrefix)
	byID = len(id) >= minIDPrefix && strings.HasPrefix(strings.TrimPrefix(img.Id, digestPrefix), id)

	return exact, byName, byID
}

// describeImage is an image in a few words, for a refusal that has to tell several apart.
func describeImage(at *locatedImage) string {
	name := shortImageID(at.Image.Id)
	if tags := tagsOf(&at.Image); len(tags) > 0 {
		name = strings.Join(tags, ", ") + " (" + name + ")"
	}

	return name + " on " + at.Env.Name
}

// findImage is the one image a call names, looked for on the environment given or on every one: by a name and tag in full, failing that by its name
// alone, and failing that by the start of its id. The same image is commonly on several hosts, and which is meant is not guessed at: the call is
// refused, naming them, until it says which.
func (r *registry) findImage(ctx context.Context, in imageArg) (locatedImage, error) {
	want := strings.TrimSpace(in.Image)
	if want == "" {
		return locatedImage{}, errors.New("image is required: a name and tag the image goes by, or the start of its id, as image_list gives them")
	}

	envs, err := r.pick(ctx, in.envArg)
	if err != nil {
		return locatedImage{}, err
	}

	var exact, named, byID []locatedImage
	var failed []string
	for _, answer := range ask(ctx, envs, r.imagesOf) {
		if answer.Err != nil {
			failed = append(failed, answer.Env.Name+": "+answer.Err.Error())

			continue
		}
		for i := range answer.Value {
			at := locatedImage{Env: answer.Env, Image: answer.Value[i]}
			switch isExact, isNamed, isID := matchImage(&at.Image, want); {
			case isExact:
				exact = append(exact, at)
			case isNamed:
				named = append(named, at)
			case isID:
				byID = append(byID, at)
			default:
			}
		}
	}

	matches := exact
	if len(matches) == 0 {
		matches = named
	}
	if len(matches) == 0 {
		matches = byID
	}
	switch {
	case len(matches) == 1:
		return matches[0], nil
	case len(matches) > 1:
		at := make([]string, 0, len(matches))
		for i := range matches {
			at = append(at, describeImage(&matches[i]))
		}
		slices.Sort(at)

		return locatedImage{}, fmt.Errorf("%q is more than one image (%s): say which with env, with its tag, or with more of its id", want, strings.Join(at, "; "))
	case len(failed) > 0:
		return locatedImage{}, fmt.Errorf("no image %q was found, and not every environment could be asked: %s", want, strings.Join(failed, "; "))
	default:
		names := make([]string, 0, len(envs))
		for _, e := range envs {
			names = append(names, e.Name)
		}

		return locatedImage{}, fmt.Errorf("no image goes by %q, or has an id that starts with it, on %s: image_list gives the ones there are", want, strings.Join(names, ", "))
	}
}

// usersOf is the containers of an environment made from an image, by name, each with its state.
func (r *registry) usersOf(ctx context.Context, at *locatedImage) ([]string, error) {
	containers, err := r.containersOf(ctx, at.Env)
	if err != nil {
		return nil, err
	}

	users := []string{}
	for i := range containers {
		if containers[i].ImageId == at.Image.Id {
			users = append(users, containers[i].Name+" ("+string(containers[i].State)+")")
		}
	}
	slices.Sort(users)

	return users, nil
}

// settle is how work that may have been started as a job came out. A call answered with no job did its work before it answered, and that answer is
// the outcome. One answered with a job is waited on for as long as the patience given: still running, there is no outcome yet and the state says so;
// ended in an error, the error is why; and ended well, the outcome is the job's result read as the call's own answer.
func settle[T any](ctx context.Context, r *registry, jobID string, answered *T, patience time.Duration) (*T, jobState, error) {
	if jobID == "" {
		return answered, jobState{}, nil
	}

	j, err := r.await(ctx, jobID, patience)
	if err != nil {
		return nil, jobState{}, err
	}
	state := stateOfJob(j)
	if state.Running {
		return nil, state, nil
	}
	if failed(j) {
		return nil, state, errors.New(failureOf(j))
	}

	// the result is free-form in the document and the same shape as the call's own answer on the wire
	var outcome T
	raw, err := json.Marshal(j.Result)
	if err == nil {
		err = json.Unmarshal(raw, &outcome)
	}
	if err != nil {
		return nil, state, fmt.Errorf("job %s ended, and its result could not be read: %w", jobID, err)
	}

	return &outcome, state, nil
}

// imageNamed is the id of the image a host has under a name, or nothing when it has none: a name with no tag is its latest.
func (r *registry) imageNamed(ctx context.Context, env environment, name string) (string, error) {
	tagged := name
	if repository(name) == name && !strings.Contains(name, "@") {
		tagged = name + ":latest"
	}

	images, err := r.imagesOf(ctx, env)
	if err != nil {
		return "", err
	}
	for i := range images {
		if slices.Contains(tagsOf(&images[i]), tagged) {
			return images[i].Id, nil
		}
	}

	return "", nil
}

// pull fetches an image to a host from its registry, as a job on Dockhand, and waits for it as long as the patience given. A state that is running
// means the wait ran out first and the pull goes on; an error is why the image was not pulled.
func (r *registry) pull(ctx context.Context, env environment, name string, patience time.Duration) (jobState, error) {
	started, err := r.dock.PostImagesPull(ctx, dockhand.PostImagesPullRequest{Image: name}, dockhand.PostImagesPullOptions{Accept: dockhand.AcceptJob, Env: env.ID})
	if err != nil {
		return jobState{}, refusal(err)
	}

	outcome, state, err := settle(ctx, r, started.Answer.JobId, started.Answer, patience)
	if err != nil || state.Running {
		return state, err
	}
	if outcome.Status == jobFailed || outcome.Error != "" {
		return state, errors.New(cmp.Or(plainly(outcome.Error), "Dockhand gave no reason"))
	}

	return state, nil
}

//nolint:gocyclo,maintidx // six tools registered in one function, each a handler of its own: the count is theirs added up
func registerImageTools(r *registry) {
	type imageListIn struct {
		envArg

		Name   string `json:"name,omitempty"   jsonschema:"only images one of whose names contains this, whatever its case"`
		Unused bool   `json:"unused,omitempty" jsonschema:"only images no container on the host was made from: what image_prune with all would remove"`
		Limit  int    `json:"limit,omitempty"  jsonschema:"the most images to list, 200 unless given; the counts are of every image that matched"`
	}
	type imageListOut struct {
		Images   []imageRow   `json:"images"           jsonschema:"the images that matched, by environment and then by name, the ones with no name last"`
		Total    int          `json:"total"            jsonschema:"how many images matched, on every environment asked; more than are listed when the list was cut at the limit"`
		TotalMB  float64      `json:"total_mb"         jsonschema:"the sizes of every image that matched, added up, in megabytes: more than the disk they take, since images share layers"`
		Unused   int          `json:"unused"           jsonschema:"how many of those no container was made from"`
		UnusedMB float64      `json:"unused_mb"        jsonschema:"the sizes of the unused ones added up, in megabytes: the most pruning them could give back"`
		Failed   []envFailure `json:"failed,omitempty" jsonschema:"the environments that could not be asked, each with why; their images are in no count"`
		Note     string       `json:"note,omitempty"`
	}
	add(r, readTool, &mcp.Tool{
		Name:        "image_list",
		Description: "List the Docker images on every Docker host (environment) the Dockhand manages, or on one: each with its host, the names and tags it goes by, its size, when it was built and how many containers were made from it. Narrow it to one environment or a part of a name, or ask for unused alone, which is every image no container on its host was made from and so what a prune would remove. The totals say how much the images weigh and how much of that is unused. A host that cannot be asked is named with the reason beside what the others answered.",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in imageListIn) (*mcp.CallToolResult, imageListOut, error) {
		if in.Limit < 0 {
			return nil, imageListOut{}, fmt.Errorf("limit is %d: it is how many images to list at most, a number above 0, or left out for %d", in.Limit, defaultImages)
		}
		limit := cmp.Or(in.Limit, defaultImages)

		envs, err := r.pick(ctx, in.envArg)
		if err != nil {
			return nil, imageListOut{}, err
		}

		out := imageListOut{Images: []imageRow{}}
		var bytes, unusedBytes int
		for _, answer := range ask(ctx, envs, r.imagesOf) {
			if answer.Err != nil {
				out.Failed = append(out.Failed, envFailure{Env: answer.Env.Name, Error: answer.Err.Error()})

				continue
			}

			rows := make([]imageRow, 0, len(answer.Value))
			for i := range answer.Value {
				img := &answer.Value[i]
				row := imageRowOf(answer.Env.Name, img)
				if (in.Unused && row.Containers > 0) || (in.Name != "" && !strings.Contains(strings.ToLower(strings.Join(row.Tags, " ")), strings.ToLower(in.Name))) {
					continue
				}

				out.Total++
				bytes += img.Size
				if row.Containers == 0 {
					out.Unused++
					unusedBytes += img.Size
				}
				rows = append(rows, row)
			}
			// by name, and the ones with none after them by id
			slices.SortFunc(rows, func(a, b imageRow) int {
				return cmp.Or(cmp.Compare(min(len(b.Tags), 1), min(len(a.Tags), 1)), cmp.Compare(strings.Join(a.Tags, " "), strings.Join(b.Tags, " ")), cmp.Compare(a.ID, b.ID))
			})

			out.Images = append(out.Images, rows[:min(len(rows), max(0, limit-len(out.Images)))]...)
		}
		out.TotalMB, out.UnusedMB = megabytes(bytes), megabytes(unusedBytes)

		if len(out.Failed) == len(envs) {
			return nil, out, fmt.Errorf("no environment could be asked for its images: %s: %s", out.Failed[0].Env, out.Failed[0].Error)
		}
		if len(out.Failed) > 0 {
			out.Note = fmt.Sprintf("%d of the %d environments could not be asked, and their images are missing from this", len(out.Failed), len(envs))
		}
		if out.Total > len(out.Images) {
			out.Note = note(out.Note, fmt.Sprintf("%d images matched and the first %d are listed: narrow it by env or name, or raise limit", out.Total, len(out.Images)))
		}

		return nil, out, nil
	})

	type imageLayer struct {
		CreatedBy string  `json:"created_by,omitempty" jsonschema:"the build step that made the layer, as the image records it"`
		SizeMB    float64 `json:"size_mb"              jsonschema:"what the layer adds, in megabytes; 0 for a step that changed no files"`
		Created   string  `json:"created,omitempty"    jsonschema:"when the layer was made, in UTC"`
	}
	type imageGetIn struct {
		imageArg
		revealArg
	}
	type imageGetOut struct {
		imageRow

		FullID  string            `json:"full_id"           jsonschema:"the image's whole id"`
		Digests []string          `json:"digests,omitempty" jsonschema:"the image by the digest it was pulled as, for each repository it came from: what says two hosts have the very same image"`
		Labels  map[string]string `json:"labels,omitempty"  jsonschema:"the labels the image was built with"`
		Layers  []imageLayer      `json:"layers"            jsonschema:"how the image was built, newest step first"`
		UsedBy  []string          `json:"used_by"           jsonschema:"the containers on the host made from it, each with its state; none for an image nothing uses"`
		Note    string            `json:"note,omitempty"`
	}
	add(r, readTool, &mcp.Tool{
		Name:        "image_get",
		Description: "Describe one image on a Docker host in full: its whole id, every name and tag it goes by, the digests it was pulled as, its size and when it was built, its labels, the steps it was built from with what each adds, and the containers on the host made from it with their state. Name it by a name and tag, by its name alone, or by the start of its id. The same image is often on several hosts, so say which with env when it is. A password or token among its labels is hidden unless reveal_secrets asks for it.",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in imageGetIn) (*mcp.CallToolResult, imageGetOut, error) {
		at, err := r.findImage(ctx, in.imageArg)
		if err != nil {
			return nil, imageGetOut{}, err
		}

		out := imageGetOut{imageRow: imageRowOf(at.Env.Name, &at.Image), FullID: at.Image.Id, Digests: at.Image.RepoDigests, Layers: []imageLayer{}, UsedBy: []string{}}
		labels, hidden := maskedLabels(at.Image.Labels, in.RevealSecrets)
		if out.Labels = labels; hidden > 0 {
			out.Note = hiddenNote(hidden, "told by the names beside them")
		}

		// the two that follow describe an image that is known to be there, and one of them failing is said rather than hiding the rest
		if history, err := r.dock.GetImagesByIdHistory(ctx, at.Image.Id, dockhand.GetImagesByIdHistoryOptions{Env: at.Env.ID}); err != nil {
			out.Note = note(out.Note, "how it was built could not be read: "+refusal(err).Error())
		} else {
			for i := range history.Answer {
				layer := imageLayer{CreatedBy: strings.TrimSpace(history.Answer[i].CreatedBy), SizeMB: megabytes(history.Answer[i].Size)}
				if history.Answer[i].Created > 0 {
					layer.Created = time.Unix(int64(history.Answer[i].Created), 0).UTC().Format(time.RFC3339)
				}
				out.Layers = append(out.Layers, layer)
			}
		}

		if users, err := r.usersOf(ctx, &at); err != nil {
			out.Note = note(out.Note, "which containers use it could not be read: "+err.Error())
		} else {
			out.UsedBy = users
		}

		return nil, out, nil
	})

	type imagePullIn struct {
		envArg
		waitArg
		dryRunArg

		Image string `json:"image" jsonschema:"the image to pull, as docker pull takes it: a name and tag (nginx:1.27), with its registry when it is not Docker Hub's (ghcr.io/org/app:v2); a name with no tag is its latest"`
	}
	type imagePullOut struct {
		jobState

		Env     string `json:"env"               jsonschema:"the environment (Docker host) the image was pulled to"`
		Image   string `json:"image"             jsonschema:"the image that was asked for"`
		Was     string `json:"was,omitempty"     jsonschema:"the start of the id the host had under that name before; absent when it had none"`
		Is      string `json:"is,omitempty"      jsonschema:"the start of the id the host has under that name now, read back; absent while the pull is still running"`
		Changed bool   `json:"changed"           jsonschema:"whether the host now has another image under that name than before: false when it already had the newest"`
		DryRun  bool   `json:"dry_run,omitempty" jsonschema:"nothing was pulled: was says what the host has"`
		Note    string `json:"note,omitempty"`
	}
	add(r, writeTool, &mcp.Tool{
		Name:        "image_pull",
		Description: "Pull an image to one Docker host (environment), as docker pull does, from its registry, with the credentials Dockhand keeps for that registry when it has any. It names one environment. Says the id the host had under the name before and has after, and whether that changed; pulling a tag the host already has the newest of changes nothing. Containers made from the older image go on running it: nothing is restarted or recreated. The pull runs in the background on Dockhand: this waits up to wait seconds for it (50 unless said) and, when it is still going then, answers with running true and the job's id, for job_get to follow. A name that cannot be pulled is an error that says why.",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in imagePullIn) (*mcp.CallToolResult, imagePullOut, error) {
		name := strings.TrimSpace(in.Image)
		if name == "" || strings.ContainsAny(name, " \t\n") {
			return nil, imagePullOut{}, fmt.Errorf("image is %q: it is the name and tag to pull, such as nginx:1.27 or ghcr.io/org/app:v2", in.Image)
		}
		patience, err := in.patience()
		if err != nil {
			return nil, imagePullOut{}, err
		}
		env, err := r.one(ctx, in.envArg)
		if err != nil {
			return nil, imagePullOut{}, err
		}

		// what the host has under the name, before and after
		has := func() (string, error) {
			id, failed := r.imageNamed(ctx, env, name)

			return shortImageID(id), failed
		}

		out := imagePullOut{Env: env.Name, Image: name, DryRun: in.DryRun}
		if out.Was, err = has(); err != nil {
			return nil, out, fmt.Errorf("the images of %s could not be listed, so nothing was pulled: %w", env.Name, err)
		}
		if in.DryRun {
			out.Is = out.Was
			out.Note = "nothing was pulled: without dry_run the image is pulled from its registry"

			return nil, out, nil
		}

		state, err := r.pull(ctx, env, name, patience)
		out.jobState = state
		if err != nil {
			return nil, out, fmt.Errorf("%s was not pulled to %s: %w", name, env.Name, err)
		}
		if state.Running {
			out.Note = fmt.Sprintf("the pull was still running after %s and goes on without this: job_get with job %s says how it ends", seconds(patience), state.Job)

			return nil, out, nil
		}

		if out.Is, err = has(); err != nil {
			out.Note = "it was pulled, and the images could not be listed afterwards: " + err.Error()

			return nil, out, nil //nolint:nilerr // the pull went through: that it could not be read back is said in the answer
		}
		switch out.Changed = out.Is != out.Was; {
		case out.Is == "":
			out.Note = "the pull was answered as done, and the host lists no image under that name: image_list says what it has"
		case !out.Changed:
			out.Note = "the host already had the newest image of that name, so nothing changed"
		case out.Was != "":
			out.Note = "containers made from the older image go on running it until they are made again"
		default:
		}

		return nil, out, nil
	})

	type imageTagIn struct {
		imageArg
		dryRunArg

		As string `json:"as" jsonschema:"the new name and tag to give the image as well, such as registry.example/team/app:v2; a name with no tag is its latest"`
	}
	type imageTagOut struct {
		Env    string   `json:"env"`
		ID     string   `json:"id"                jsonschema:"the start of the image's id"`
		Tags   []string `json:"tags"              jsonschema:"every name and tag the image goes by now, read back from the host; with dry_run, as it stands"`
		Added  bool     `json:"added"             jsonschema:"whether the name was added: false when the image already went by it"`
		DryRun bool     `json:"dry_run,omitempty" jsonschema:"nothing was done"`
		Note   string   `json:"note,omitempty"`
	}
	add(r, writeTool, &mcp.Tool{
		Name:        "image_tag",
		Description: "Give an image on a Docker host another name and tag as well, as docker tag does: the image itself is untouched and keeps the names it had. A name that another image on the host goes by is taken from that image, which is how docker tag works, and the answer says when that happens. Read back from the host.",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in imageTagIn) (*mcp.CallToolResult, imageTagOut, error) {
		as := strings.TrimSpace(in.As)
		if as == "" || strings.ContainsAny(as, " \t\n@") {
			return nil, imageTagOut{}, fmt.Errorf("as is %q: it is the new name and tag, such as registry.example/team/app:v2", in.As)
		}
		repo, tag := repository(as), "latest"
		if repo != as {
			tag = as[len(repo)+1:]
		}
		as = repo + ":" + tag

		at, err := r.findImage(ctx, in.imageArg)
		if err != nil {
			return nil, imageTagOut{}, err
		}
		out := imageTagOut{Env: at.Env.Name, ID: shortImageID(at.Image.Id), Tags: tagsOf(&at.Image), DryRun: in.DryRun}
		if slices.Contains(out.Tags, as) {
			out.Note = "the image already goes by that name, so nothing was done"

			return nil, out, nil
		}

		// docker tag moves a name that is in use: say whose it was
		images, err := r.imagesOf(ctx, at.Env)
		if err != nil {
			return nil, out, fmt.Errorf("the images of %s could not be listed, so nothing was tagged: %w", at.Env.Name, err)
		}
		other := ""
		for i := range images {
			if images[i].Id != at.Image.Id && slices.Contains(tagsOf(&images[i]), as) {
				other = shortImageID(images[i].Id)
			}
		}
		if in.DryRun {
			out.Note = "nothing was done: without dry_run the image is given the name " + as
			if other != "" {
				out.Note = note(out.Note, fmt.Sprintf("it is the name of another image, %s, which would no longer go by it", other))
			}

			return nil, out, nil
		}
		if other != "" {
			out.Note = fmt.Sprintf("%s was the name of another image, %s, which no longer goes by it", as, other)
		}

		done, err := r.dock.PostImagesByIdTag(ctx, at.Image.Id, dockhand.PostImagesByIdTagRequest{Repo: repo, Tag: tag}, dockhand.PostImagesByIdTagOptions{Env: at.Env.ID})
		if err != nil {
			return nil, out, fmt.Errorf("%s was not given the name %s: %w", out.ID, as, refusal(err))
		}
		if !done.Answer.Success {
			return nil, out, fmt.Errorf("giving %s the name %s was answered without its being said to have succeeded: image_get says what it goes by", out.ID, as)
		}

		after, err := r.imagesOf(ctx, at.Env)
		if err != nil {
			out.Added = true
			out.Note = note(out.Note, "it was tagged, and the images could not be listed afterwards: "+err.Error())

			return nil, out, nil //nolint:nilerr // the tag went through: that it could not be read back is said in the answer
		}
		for i := range after {
			if after[i].Id == at.Image.Id {
				out.Tags = tagsOf(&after[i])
			}
		}
		if out.Added = slices.Contains(out.Tags, as); !out.Added {
			return nil, out, fmt.Errorf("giving %s the name %s was answered as done, and the host does not list it under that name", out.ID, as)
		}

		return nil, out, nil
	})

	type imageDeleteIn struct {
		imageArg

		Force   bool `json:"force,omitempty"   jsonschema:"remove it although stopped containers were made from it, which are left without their image; Dockhand refuses an image a running container uses either way"`
		Confirm bool `json:"confirm,omitempty" jsonschema:"remove it: without this the answer says what would go and nothing is removed"`
	}
	type imageDeleteOut struct {
		Env     string   `json:"env"`
		ID      string   `json:"id"                 jsonschema:"the start of the image's id"`
		Removed bool     `json:"removed"            jsonschema:"the image was removed, and is read back as gone"`
		WouldGo []string `json:"would_go,omitempty" jsonschema:"what removing it takes away, said before anything is removed"`
		UsedBy  []string `json:"used_by"            jsonschema:"the containers on the host made from it, each with its state"`
		FreedMB float64  `json:"freed_mb,omitempty" jsonschema:"the image's size in megabytes: the most its removal gives back, less whatever layers other images share with it"`
		Note    string   `json:"note,omitempty"`
	}
	add(r, deleteTool, &mcp.Tool{
		Name:        "image_delete",
		Description: "Remove one image from a Docker host: the image and every name and tag it goes by there, since Dockhand removes an image whole and cannot take one name off it. Without confirm the answer says what would go and which containers were made from it, and removes nothing. An image a container was made from is refused unless force is given, and one a running container uses is refused by Dockhand even then: stop the container first. The image can be pulled again from its registry if it came from one; one built on the host cannot be got back. Read back as gone.",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in imageDeleteIn) (*mcp.CallToolResult, imageDeleteOut, error) {
		at, err := r.findImage(ctx, in.imageArg)
		if err != nil {
			return nil, imageDeleteOut{}, err
		}
		tags := tagsOf(&at.Image)
		out := imageDeleteOut{Env: at.Env.Name, ID: shortImageID(at.Image.Id), UsedBy: []string{}, FreedMB: megabytes(at.Image.Size)}

		users, err := r.usersOf(ctx, &at)
		if err != nil {
			return nil, out, fmt.Errorf("which containers use %s could not be read, so it was not removed: %w", out.ID, err)
		}
		out.UsedBy = users

		out.WouldGo = []string{"the image " + out.ID + " on " + at.Env.Name}
		for _, tag := range tags {
			out.WouldGo = append(out.WouldGo, "its name "+tag)
		}
		if len(users) > 0 && !in.Force {
			return nil, out, fmt.Errorf("%s is the image of %d containers on %s (%s): remove them first, or give force to remove it from under the stopped ones", cmp.Or(strings.Join(tags, ", "), out.ID), len(users), at.Env.Name, strings.Join(users, ", "))
		}
		if !in.Confirm {
			out.Note = "nothing was removed: call again with confirm to remove it"
			if len(users) > 0 {
				out.Note = note(out.Note, fmt.Sprintf("the %d containers made from it would be left without their image", len(users)))
			}

			return nil, out, nil
		}

		// Dockhand wants force for an image with more than one name as well as for one a stopped container was made from
		force := in.Force || len(tags) > 1
		gone, err := r.dock.DeleteImagesById(ctx, at.Image.Id, dockhand.DeleteImagesByIdOptions{Env: at.Env.ID, Force: &force})
		if err != nil {
			return nil, out, fmt.Errorf("%s was not removed: %w", out.ID, refusal(err))
		}
		if !gone.Answer.Success {
			return nil, out, fmt.Errorf("the removal of %s was answered without its being said to have succeeded: image_list says whether it is still there", out.ID)
		}

		left, err := r.imagesOf(ctx, at.Env)
		if err != nil {
			out.Note = "it was removed, and the images could not be listed afterwards: " + err.Error()

			return nil, out, nil //nolint:nilerr // the removal went through: that it could not be read back is said in the answer
		}
		if slices.ContainsFunc(left, func(img dockhand.GetImagesAnswer) bool { return img.Id == at.Image.Id }) {
			return nil, out, fmt.Errorf("the removal of %s was answered as done, and it is still listed", out.ID)
		}
		out.Removed, out.WouldGo = true, nil

		return nil, out, nil
	})

	type imagePruneIn struct {
		envArg
		waitArg

		All     bool `json:"all,omitempty"     jsonschema:"remove every image no container on the host was made from, named or not; left out, only the images with no name left are removed"`
		Confirm bool `json:"confirm,omitempty" jsonschema:"remove them: without this the answer says what would go and nothing is removed"`
	}
	type imagePruneOut struct {
		jobState

		Env      string   `json:"env"`
		Removed  []string `json:"removed"            jsonschema:"the images that were removed, each by its names or, with none, the start of its id; with confirm left out, empty"`
		Untagged []string `json:"untagged,omitempty" jsonschema:"the names taken off images that stay: a spare name of an image a container uses under another"`
		WouldGo  []string `json:"would_go,omitempty" jsonschema:"the images that would be removed, said before anything is"`
		FreedMB  float64  `json:"freed_mb"           jsonschema:"the disk given back, in megabytes, as Docker reports it; before anything is removed, the sizes of what would go added up, which is the most it could be"`
		Note     string   `json:"note,omitempty"`
	}
	add(r, deleteTool, &mcp.Tool{
		Name:        "image_prune",
		Description: "Remove the images a Docker host no longer uses, as docker image prune does. It names one environment. Left as it is, it removes only the images with no name left, which a newer image of the same name leaves behind; with all it removes every image no container on the host was made from, named or not, which also takes a spare name off an image that is in use under another. Without confirm the answer lists what would go and removes nothing. An image a container was made from, running or stopped, is never removed. What is removed can be pulled again if it came from a registry; an image built on the host cannot be got back. The pruning runs in the background on Dockhand: this waits up to wait seconds for it and, when it is still going then, answers with running true and the job's id for job_get.",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in imagePruneIn) (*mcp.CallToolResult, imagePruneOut, error) {
		patience, err := in.patience()
		if err != nil {
			return nil, imagePruneOut{}, err
		}
		env, err := r.one(ctx, in.envArg)
		if err != nil {
			return nil, imagePruneOut{}, err
		}

		out := imagePruneOut{Env: env.Name, Removed: []string{}}
		before, err := r.imagesOf(ctx, env)
		if err != nil {
			return nil, out, fmt.Errorf("the images of %s could not be listed, so nothing was removed: %w", env.Name, err)
		}
		// the names containers were started under: with all, any other name of an image in use is taken off it
		inUse := map[string]bool{}
		if in.All {
			containers, failed := r.containersOf(ctx, env)
			if failed != nil {
				return nil, out, fmt.Errorf("the containers of %s could not be listed to say which images they use, so nothing was removed: %w", env.Name, failed)
			}
			for i := range containers {
				inUse[containers[i].Image] = true
				inUse[containers[i].Image+":latest"] = true
			}
		}

		// by id, so that what went can be named afterwards by what it was called
		called := map[string]string{}
		var spare []string
		bytes := 0
		for i := range before {
			tags := tagsOf(&before[i])
			name := cmp.Or(strings.Join(tags, ", "), shortImageID(before[i].Id))
			called[before[i].Id] = name

			switch {
			case before[i].Containers == 0 && (in.All || len(tags) == 0):
				out.WouldGo = append(out.WouldGo, name)
				bytes += before[i].Size
			case in.All && len(tags) > 1:
				for _, tag := range tags {
					if !inUse[tag] {
						spare = append(spare, "the name "+tag+", and not its image, which is in use under another name")
					}
				}
			default:
			}
		}
		slices.Sort(out.WouldGo)
		slices.Sort(spare)
		out.WouldGo = append(out.WouldGo, spare...)

		if !in.Confirm {
			out.FreedMB = megabytes(bytes)
			out.Note = "nothing was removed: call again with confirm to remove them"
			if len(out.WouldGo) == 0 {
				out.Note = "there is nothing to remove"
				if !in.All {
					out.Note = "no image on the host is without a name, so there is nothing to remove: all removes the named ones nothing uses as well"
				}
			}

			return nil, out, nil
		}

		started, err := r.dock.PostPruneImages(ctx, dockhand.PostPruneImagesOptions{Accept: dockhand.AcceptJob, Env: env.ID, Dangling: new(!in.All)})
		if err != nil {
			return nil, out, fmt.Errorf("the images of %s were not pruned: %w", env.Name, refusal(err))
		}
		outcome, state, err := settle(ctx, r, started.Answer.JobId, started.Answer, patience)
		out.jobState = state
		if err != nil {
			return nil, out, fmt.Errorf("the images of %s were not pruned: %w", env.Name, err)
		}
		if state.Running {
			out.Note = fmt.Sprintf("the pruning was still running after %s and goes on without this: job_get with job %s says how it ends", seconds(patience), state.Job)

			return nil, out, nil
		}
		if outcome.Error != "" {
			return nil, out, fmt.Errorf("the images of %s were not pruned: %s", env.Name, plainly(outcome.Error))
		}
		if !outcome.Success || outcome.Result == nil {
			return nil, out, fmt.Errorf("the pruning of %s was answered without its being said to have succeeded: image_list says what is left", env.Name)
		}

		out.WouldGo = nil
		out.FreedMB = megabytes(outcome.Result.SpaceReclaimed)
		// Docker reports each name taken off and each image and layer deleted: the images are the ones that were listed before, and a name taken off
		// one that was not deleted is a spare name of an image that stays
		for i := range outcome.Result.ImagesDeleted {
			if name, wasListed := called[outcome.Result.ImagesDeleted[i].Deleted]; wasListed {
				out.Removed = append(out.Removed, name)
			}
		}
		for i := range outcome.Result.ImagesDeleted {
			name := outcome.Result.ImagesDeleted[i].Untagged
			if name != "" && !slices.ContainsFunc(out.Removed, func(removed string) bool { return slices.Contains(strings.Split(removed, ", "), name) }) {
				out.Untagged = append(out.Untagged, name)
			}
		}
		slices.Sort(out.Removed)
		slices.Sort(out.Untagged)
		if len(out.Removed) == 0 && len(out.Untagged) == 0 {
			out.Note = "nothing was removed: the host had no image this would take"
		}

		return nil, out, nil
	})
}
