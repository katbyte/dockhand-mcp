package tools

import (
	"archive/tar"
	"bufio"
	"cmp"
	"compress/gzip"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path"
	"path/filepath"
	"slices"
	"strings"

	"github.com/katbyte/dockhand-mcp/sdk/dockhand"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// isGzipped reports whether a file's name says it is a gzipped archive.
func isGzipped(name string) bool {
	lower := strings.ToLower(name)

	return strings.HasSuffix(lower, ".tar.gz") || strings.HasSuffix(lower, ".tgz")
}

// fileNameOf is a name a file can have for something Docker names with slashes and colons in it.
func fileNameOf(name string) string {
	return strings.NewReplacer("/", "_", ":", "_", "@", "_").Replace(name)
}

// namesIn is the names of the images an archive on this machine holds, as docker save and image_export write them, whether it is gzipped or not. An
// archive that says nothing of its images, or cannot be read as one, gives none: Docker is the judge of what it will load.
func namesIn(file string) []string {
	opened, err := os.Open(file) //nolint:gosec // G304: the path is an archive the operator asked to be loaded
	if err != nil {
		return nil
	}
	defer func() { _ = opened.Close() }()

	start := bufio.NewReader(opened)
	var from io.Reader = start
	if magic, _ := start.Peek(2); len(magic) == 2 && magic[0] == 0x1f && magic[1] == 0x8b {
		unzipped, err := gzip.NewReader(start)
		if err != nil {
			return nil
		}
		from = unzipped
	}

	entries := tar.NewReader(from)
	for {
		entry, err := entries.Next()
		if err != nil {
			return nil
		}
		if entry.Name != "manifest.json" {
			continue
		}
		var held []struct {
			RepoTags []string `json:"RepoTags"`
		}
		if err := json.NewDecoder(io.LimitReader(entries, 1<<20)).Decode(&held); err != nil {
			return nil
		}
		var names []string
		for _, image := range held {
			names = append(names, image.RepoTags...)
		}

		return names
	}
}

//nolint:gocyclo // three tools registered in one function, each a handler of its own: the count is theirs added up
func registerExportTools(r *registry) {
	type imageExportIn struct {
		imageArg

		To string `json:"to"           jsonschema:"where on this machine to write the archive: a file that is not there yet, or a folder that is, which takes it under the image's name. A name ending .tar.gz or .tgz is written gzipped"`
		As string `json:"as,omitempty" jsonschema:"for an image that goes by several names: the one the archive is to record, which is the name it has again once loaded. Left out, the first of its names"`
	}
	type imageExportOut struct {
		Env      string `json:"env"                jsonschema:"the environment (Docker host) the image is on"`
		ID       string `json:"id"`
		Recorded string `json:"recorded,omitempty" jsonschema:"the name the archive records for the image; absent for an image that has none, which is loaded with no name"`
		Saved    string `json:"saved"              jsonschema:"where on this machine the archive was written"`
		Bytes    int64  `json:"bytes"`
		Note     string `json:"note,omitempty"`
	}
	add(r, readTool, &mcp.Tool{
		Name:        "image_export",
		Description: "Write an image that is on a Docker host to a file on this machine, as docker save does: a tar archive of the image with every layer, which image_load or docker load puts on a host that cannot pull it. It names one image, by a name or an id as image_list gives them. Nothing on the host changes. The file is new: one that is there already is never written over. An image can be large, and the archive is as large as the image.",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in imageExportIn) (*mcp.CallToolResult, imageExportOut, error) {
		to, as := strings.TrimSpace(in.To), strings.TrimSpace(in.As)
		if to == "" {
			return nil, imageExportOut{}, errors.New("to is required: where on this machine to write the archive")
		}
		at, err := r.findImage(ctx, in.imageArg)
		if err != nil {
			return nil, imageExportOut{}, err
		}
		out := imageExportOut{Env: at.Env.Name, ID: shortImageID(at.Image.Id)}
		names := tagsOf(&at.Image)
		switch {
		case as != "" && !slices.Contains(names, as):
			return nil, out, fmt.Errorf("as is %q, which is none of the image's names: it goes by %s", in.As, strings.Join(names, ", "))
		case as != "":
			out.Recorded = as
		case len(names) > 0:
			out.Recorded = names[0]
		default:
			out.Note = "the image has no name, so the archive records none: loaded, it is an image with an id and no name"
		}

		// a folder that is there takes the archive under the image's name
		if info, statErr := os.Stat(to); statErr == nil && info.IsDir() {
			to = filepath.Join(to, fileNameOf(cmp.Or(out.Recorded, out.ID))+".tar")
		}
		got, err := r.dock.GetImagesByIdExport(ctx, at.Image.Id, dockhand.GetImagesByIdExportOptions{Env: at.Env.ID, Tag: out.Recorded, Compress: new(isGzipped(to))})
		if got.HttpResponse != nil && got.HttpResponse.Body != nil {
			defer func() { _ = got.HttpResponse.Body.Close() }()
		}
		if err != nil {
			return nil, out, fmt.Errorf("the image %s could not be exported from %s: %w", describeImage(&at), at.Env.Name, refusal(err))
		}
		if out.Bytes, err = savedHere(to, got.HttpResponse.Body); err != nil {
			return nil, out, fmt.Errorf("the image %s: %w", describeImage(&at), err)
		}
		out.Saved = to

		return nil, out, nil
	})

	type imageLoadIn struct {
		envArg
		dryRunArg

		File string `json:"file" jsonschema:"the archive on this machine to load: what docker save or image_export wrote, a tar or a gzipped tar"`
	}
	type imageLoadOut struct {
		Env    string   `json:"env"               jsonschema:"the environment (Docker host) the archive was loaded on"`
		File   string   `json:"file"`
		Bytes  int64    `json:"bytes"`
		Images []string `json:"images"            jsonschema:"the images the host now has from the archive, by name, read back as there; for a dry run, the names the archive says it holds"`
		DryRun bool     `json:"dry_run,omitempty" jsonschema:"nothing was loaded: images says what the archive holds"`
		Note   string   `json:"note,omitempty"`
	}
	add(r, writeTool, &mcp.Tool{
		Name:        "image_load",
		Description: "Load an image onto one Docker host (environment) from an archive on this machine, as docker load does: how an image reaches a host that cannot pull it, from what image_export or docker save wrote. The image is added with the name the archive records, and an image the host already has under that name loses the name and nothing else. Dockhand loads only onto a host it reaches by its socket or its address: one behind a Hawser agent is refused. The whole archive is sent to Dockhand.",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in imageLoadIn) (*mcp.CallToolResult, imageLoadOut, error) {
		file := strings.TrimSpace(in.File)
		if file == "" {
			return nil, imageLoadOut{}, errors.New("file is required: the archive on this machine to load")
		}
		info, err := os.Stat(file)
		switch {
		case err != nil:
			return nil, imageLoadOut{}, fmt.Errorf("file could not be read here: %w", err)
		case info.IsDir():
			return nil, imageLoadOut{}, fmt.Errorf("file is %s, which is a folder: it is the archive itself", file)
		case info.Size() == 0:
			return nil, imageLoadOut{}, fmt.Errorf("file is %s, which is empty", file)
		default:
		}
		env, err := r.one(ctx, in.envArg)
		if err != nil {
			return nil, imageLoadOut{}, err
		}
		out := imageLoadOut{Env: env.Name, File: file, Bytes: info.Size(), Images: []string{}, DryRun: in.DryRun}

		// what the archive says it holds, and which of those names the host has given to something already
		held := namesIn(file)
		there, err := r.imagesOf(ctx, env)
		if err != nil {
			return nil, out, fmt.Errorf("the images of %s could not be listed, so nothing was loaded: %w", env.Name, err)
		}
		// has reports whether a host's images include one by a name, or for one with no name by its id
		has := func(images []dockhand.GetImagesAnswer, name string) bool {
			for i := range images {
				if images[i].Id == name || slices.Contains(tagsOf(&images[i]), name) {
					return true
				}
			}

			return false
		}
		var taken []string
		for _, name := range held {
			if has(there, name) {
				taken = append(taken, name)
			}
		}
		if len(taken) > 0 {
			out.Note = fmt.Sprintf("%s has an image named %s already: the name goes to the one in the archive, and an image that had it and differs is left with one name fewer", env.Name, strings.Join(taken, ", "))
		}
		if in.DryRun {
			out.Images = append(out.Images, held...)
			if len(held) == 0 {
				out.Note = "the archive does not say which images it holds: Docker says, once it is loaded"
			}

			return nil, out, nil
		}

		archive, err := os.Open(file) //nolint:gosec // G304: the path is an archive the operator asked to be loaded
		if err != nil {
			return nil, out, fmt.Errorf("file could not be read here: %w", err)
		}
		defer func() { _ = archive.Close() }()
		loaded, err := r.dock.PostImagesLoad(ctx, archive, "application/x-tar", dockhand.PostImagesLoadOptions{Env: env.ID})
		if err != nil {
			return nil, out, fmt.Errorf("the archive was not loaded on %s: %w", env.Name, refusal(err))
		}
		if !loaded.Answer.Success {
			return nil, out, fmt.Errorf("the archive was not loaded on %s, and Dockhand does not say why", env.Name)
		}
		// Docker says what it loaded a line at a time, an image with no name by its id
		for line := range strings.SplitSeq(loaded.Answer.Loaded, "\n") {
			if _, name, is := strings.Cut(line, ": "); is && strings.TrimSpace(name) != "" {
				out.Images = append(out.Images, strings.TrimSpace(name))
			}
		}

		after, err := r.imagesOf(ctx, env)
		if err != nil {
			out.Note = noted(out.Note, "it was loaded, and the images of "+env.Name+" could not be listed afterwards: "+err.Error())

			return nil, out, nil
		}
		for _, name := range out.Images {
			if !has(after, name) {
				return nil, out, fmt.Errorf("Dockhand answered that %s was loaded on %s, and the host does not list it", name, env.Name) //nolint:staticcheck // ST1005: the sentence begins with the server's name
			}
		}

		return nil, out, nil
	})

	type volumeExportIn struct {
		volumeArg

		Path string `json:"path,omitempty" jsonschema:"a folder or a file inside the volume to export alone, from the volume's top; left out, the whole volume"`
		To   string `json:"to"             jsonschema:"where on this machine to write the archive: a file that is not there yet, or a folder that is, which takes it under the volume's name. A name ending .tar.gz or .tgz is written gzipped"`
	}
	type volumeExportOut struct {
		Env    string `json:"env"            jsonschema:"the environment (Docker host) the volume is on"`
		Volume string `json:"volume"`
		Path   string `json:"path"           jsonschema:"what in the volume was exported, / for all of it"`
		Saved  string `json:"saved"          jsonschema:"where on this machine the archive was written"`
		Bytes  int64  `json:"bytes"`
		Note   string `json:"note,omitempty"`
	}
	add(r, writeTool, &mcp.Tool{
		Name:        "volume_export",
		Description: "Write what a Docker volume holds, or one folder or file of it, to a tar archive on this machine: a copy to keep, or to carry to another host. Docker has no way to read a volume, so Dockhand starts a small helper container on the host with the volume mounted read-only, which is why this needs --enable-write; the helper is taken away again afterwards and nothing in the volume changes. It names one volume. The file is new: one that is there already is never written over. tar -xf unpacks it.",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in volumeExportIn) (*mcp.CallToolResult, volumeExportOut, error) {
		to := strings.TrimSpace(in.To)
		if to == "" {
			return nil, volumeExportOut{}, errors.New("to is required: where on this machine to write the archive")
		}
		// from the volume's top and never above it
		within := path.Clean("/" + strings.TrimSpace(in.Path))
		// a volume there is none of is refused here: asked to export one, Dockhand makes it and answers an empty archive
		at, err := r.findVolume(ctx, in.volumeArg)
		if err != nil {
			return nil, volumeExportOut{}, err
		}
		out := volumeExportOut{Env: at.Env.Name, Volume: at.Volume.Name, Path: within}
		if info, statErr := os.Stat(to); statErr == nil && info.IsDir() {
			name := at.Volume.Name
			if within != "/" {
				name += "-" + path.Base(within)
			}
			to = filepath.Join(to, fileNameOf(name)+".tar")
		}

		// always asked for gzipped, as the archives of a container's files are, and unpacked here when a plain tar is wanted
		got, err := r.dock.GetVolumesByNameExport(ctx, at.Volume.Name, dockhand.GetVolumesByNameExportOptions{Env: at.Env.ID, Path: within, Format: gzipped})
		if got.HttpResponse != nil && got.HttpResponse.Body != nil {
			defer func() { _ = got.HttpResponse.Body.Close() }()
		}
		// the helper is wanted for this one export and no longer, however the export went
		if err != nil {
			return nil, out, leftBehind(fmt.Errorf("%s of the volume %s could not be exported from %s: %w", within, at.Volume.Name, at.Env.Name, refusal(err)), r.release(ctx, &at))
		}
		var from io.Reader = got.HttpResponse.Body
		if !isGzipped(to) {
			if from, err = gzip.NewReader(got.HttpResponse.Body); err != nil {
				return nil, out, leftBehind(fmt.Errorf("what Dockhand answered for the volume %s is no archive: %w", at.Volume.Name, err), r.release(ctx, &at))
			}
		}
		if out.Bytes, err = savedHere(to, from); err != nil {
			return nil, out, leftBehind(fmt.Errorf("the volume %s: %w", at.Volume.Name, err), r.release(ctx, &at))
		}
		out.Saved, out.Note = to, r.release(ctx, &at)

		return nil, out, nil
	})
}
