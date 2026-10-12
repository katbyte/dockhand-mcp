package tools

import (
	"cmp"
	"context"
	"fmt"
	"slices"
	"strings"

	"github.com/katbyte/dockhand-mcp/sdk/dockhand"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

const (
	// defaultBiggest is how many of the biggest of each kind env_disk names unless told
	defaultBiggest = 5
	// maxBiggest is the most of each kind it names
	maxBiggest = 50
)

// diskItem is one thing on a host's Docker disk.
type diskItem struct {
	Name  string  `json:"name"   jsonschema:"an image by its name and tag, or the start of its id when it has none; a container or a volume by its name; a piece of build cache by what made it"`
	MB    float64 `json:"mb"     jsonschema:"what it takes, in megabytes: for a container, what it has written inside itself"`
	InUse bool    `json:"in_use" jsonschema:"an image a container is made from, a container that is running, a volume a container mounts, build cache a build is using"`
}

// diskKind is what one kind of thing takes of a host's Docker disk.
type diskKind struct {
	Count    int        `json:"count"     jsonschema:"how many there are"`
	MB       float64    `json:"mb"        jsonschema:"what they take together, in megabytes"`
	Unused   int        `json:"unused"    jsonschema:"how many nothing uses"`
	UnusedMB float64    `json:"unused_mb" jsonschema:"what the unused ones take: the most that removing them could give back"`
	Biggest  []diskItem `json:"biggest"   jsonschema:"the biggest of them, the biggest first"`
}

// measured adds one thing to what its kind takes.
func (k *diskKind) measured(name string, bytes int, inUse bool) {
	bytes = max(bytes, 0)
	k.Count++
	k.MB += float64(bytes)
	if !inUse {
		k.Unused++
		k.UnusedMB += float64(bytes)
	}
	k.Biggest = append(k.Biggest, diskItem{Name: name, MB: megabytes(bytes), InUse: inUse})
}

// summed turns what was added up in bytes into megabytes, and keeps the biggest few.
func (k *diskKind) summed(keep int) {
	k.MB, k.UnusedMB = megabytes(int(k.MB)), megabytes(int(k.UnusedMB))
	slices.SortStableFunc(k.Biggest, func(a, b diskItem) int { return cmp.Or(cmp.Compare(b.MB, a.MB), cmp.Compare(a.Name, b.Name)) })
	if len(k.Biggest) > keep {
		k.Biggest = k.Biggest[:keep]
	}
	if k.Biggest == nil {
		k.Biggest = []diskItem{}
	}
}

func registerEnvironmentDiskTools(r *registry) {
	type envDiskIn struct {
		envArg

		Biggest int `json:"biggest,omitempty" jsonschema:"how many of the biggest of each kind to name, 5 unless given and 50 at most"`
	}
	type envDiskOut struct {
		Env        string   `json:"env"`
		Images     diskKind `json:"images"         jsonschema:"the images: together they take less than their sizes added up, since images share layers, and mb counts a shared layer once"`
		Containers diskKind `json:"containers"     jsonschema:"what the containers have written inside themselves, over their images and outside their volumes; the unused ones are those not running"`
		Volumes    diskKind `json:"volumes"        jsonschema:"the volumes; the unused ones are those no container mounts"`
		BuildCache diskKind `json:"build_cache"    jsonschema:"what building images has left behind to make the next build quicker"`
		TotalMB    float64  `json:"total_mb"       jsonschema:"all four together: what Docker's data takes of the host's disk"`
		UnusedMB   float64  `json:"unused_mb"      jsonschema:"what the unused of all four take together"`
		Note       string   `json:"note,omitempty"`
	}
	add(r, readTool, &mcp.Tool{
		Name:        "env_disk",
		Description: "Say what Docker's disk on one host (environment) holds, and how much of it nothing uses: its images, what its containers have written inside themselves, its volumes and its build cache, each with how many there are, what they take, how much of that is unused and the biggest few by name. It is how to learn what is filling a host, and what pruning would give back, before anything is pruned; and it is the one place a volume's size is told, since Docker does not give it with a volume. Docker measures all of it afresh each time it is asked, which takes a host with a great deal on it some seconds.",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in envDiskIn) (*mcp.CallToolResult, envDiskOut, error) {
		if in.Biggest < 0 || in.Biggest > maxBiggest {
			return nil, envDiskOut{}, fmt.Errorf("biggest is %d: it is how many of the biggest of each kind to name, from 1 to %d, or left out for %d", in.Biggest, maxBiggest, defaultBiggest)
		}
		env, err := r.one(ctx, in.envArg)
		if err != nil {
			return nil, envDiskOut{}, err
		}
		out := envDiskOut{Env: env.Name}

		answer, err := r.dock.GetSystemDisk(ctx, dockhand.GetSystemDiskOptions{Env: env.ID})
		if err != nil {
			return nil, out, fmt.Errorf("what the disk of %s holds could not be read: %w", env.Name, refusal(err))
		}
		disk := answer.Answer.DiskUsage
		if disk == nil {
			return nil, out, fmt.Errorf("Dockhand gave no account of the disk of %s: it gives none when the host's Docker does not answer it in time, or cannot be reached at all, which env_test tells", env.Name) //nolint:staticcheck // ST1005: the sentence begins with the server's name
		}

		for i := range disk.Images {
			img := &disk.Images[i]
			name := shortImageID(img.Id)
			if tags := slices.DeleteFunc(slices.Clone(img.RepoTags), func(tag string) bool { return tag == "" || tag == noTag }); len(tags) > 0 {
				name = slices.Min(tags)
			}
			// an unused image gives back what no other image shares with it, where Docker worked that out
			out.Images.measured(name, img.Size, img.Containers > 0)
			if img.Containers <= 0 && img.SharedSize > 0 {
				out.Images.UnusedMB -= float64(min(img.SharedSize, img.Size))
			}
		}
		for i := range disk.Containers {
			c := &disk.Containers[i]
			out.Containers.measured(strings.TrimPrefix(cmp.Or(cmp.Or(c.Names...), shortened(c.Id)), "/"), c.SizeRw, c.State == stateRunning)
		}
		for i := range disk.Volumes {
			v := &disk.Volumes[i]
			var holds, mounted int
			if v.UsageData != nil {
				holds, mounted = v.UsageData.Size, v.UsageData.RefCount
			}
			out.Volumes.measured(v.Name, holds, mounted > 0)
		}
		for i := range disk.BuildCache {
			// a shared piece is counted with the piece it is shared from, as docker counts it
			if b := &disk.BuildCache[i]; !b.Shared {
				out.BuildCache.measured(cmp.Or(b.Description, b.Type, b.ID), b.Size, b.InUse)
			}
		}

		// the images' own sizes add up to more than they take: Docker gives what they take, each shared layer once
		images := megabytes(disk.LayersSize)
		keep := cmp.Or(in.Biggest, defaultBiggest)
		for _, kind := range []*diskKind{&out.Images, &out.Containers, &out.Volumes, &out.BuildCache} {
			kind.summed(keep)
		}
		if disk.LayersSize > 0 {
			out.Images.MB, out.Images.UnusedMB = images, min(out.Images.UnusedMB, images)
		}
		out.TotalMB = megabytes(int((out.Images.MB + out.Containers.MB + out.Volumes.MB + out.BuildCache.MB) * 1e6))
		out.UnusedMB = megabytes(int((out.Images.UnusedMB + out.Containers.UnusedMB + out.Volumes.UnusedMB + out.BuildCache.UnusedMB) * 1e6))
		if out.Images.Count+out.Containers.Count+out.Volumes.Count+out.BuildCache.Count == 0 {
			out.Note = "the host's Docker holds nothing: no image, container, volume or build cache"
		}

		return nil, out, nil
	})
}
