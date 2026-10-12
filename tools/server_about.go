package tools

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"

	"github.com/katbyte/dockhand-mcp/sdk/dockhand"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

const (
	// aboutLicence, aboutPrivacy and aboutDependencies are what server_about can be asked for
	aboutLicence      = "licence"
	aboutPrivacy      = "privacy"
	aboutDependencies = "dependencies"
	// defaultAboutText is how many characters of a text server_about answers with, and maxAboutText the most
	defaultAboutText = 4000
	maxAboutText     = 30000
	// defaultDependencies is how many of the software Dockhand is built from server_about lists, and maxDependencies the most
	defaultDependencies = 50
	maxDependencies     = 500
)

// dependencyRow is one piece of software Dockhand is built from or runs.
type dependencyRow struct {
	Name       string `json:"name"`
	Version    string `json:"version"`
	Licence    string `json:"licence,omitempty"`
	Repository string `json:"repository,omitempty"`
}

// licenceCount is how many of them are under one licence.
type licenceCount struct {
	Licence string `json:"licence"`
	Count   int    `json:"count"`
}

// mostFirst orders licences by how much is under each, the most first, and by name among equals.
func mostFirst(a, b licenceCount) int {
	if a.Count != b.Count {
		return b.Count - a.Count
	}

	return strings.Compare(a.Licence, b.Licence)
}

// byDependencyName orders them by name, whatever the case.
func byDependencyName(a, b dependencyRow) int {
	return strings.Compare(strings.ToLower(a.Name), strings.ToLower(b.Name))
}

// noSocket says whether an update check failed because Dockhand has no way to its own Docker.
func noSocket(why string) bool {
	return strings.Contains(why, "docker.sock")
}

func registerServerAboutTools(r *registry) {
	type updateCheckOut struct {
		UpdateAvailable bool   `json:"update_available"         jsonschema:"whether Dockhand found a newer image of itself than the one it runs from"`
		Checked         bool   `json:"checked"                  jsonschema:"whether Dockhand could look at all: false means update_available says nothing, and note says why"`
		Image           string `json:"image,omitempty"          jsonschema:"the image Dockhand's own container runs from"`
		NewerImage      string `json:"newer_image,omitempty"    jsonschema:"the image it would move to"`
		LatestVersion   string `json:"latest_version,omitempty" jsonschema:"the newest version it found; server_info gives the one running"`
		Digest          string `json:"digest,omitempty"         jsonschema:"for an image followed by a tag that moves, such as latest: the digest running"`
		NewerDigest     string `json:"newer_digest,omitempty"   jsonschema:"and the digest that tag now names"`
		Container       string `json:"container,omitempty"      jsonschema:"the name of Dockhand's own container, as its Docker has it"`
		ComposeManaged  bool   `json:"compose_managed"          jsonschema:"whether that container was made by a compose file, which then has to be changed for an update to last"`
		LocalImage      bool   `json:"local_image,omitempty"    jsonschema:"whether the image was built on the host and has no registry to compare with"`
		Note            string `json:"note,omitempty"`
	}
	add(r, readTool, &mcp.Tool{
		Name:        "server_update_check",
		Description: "Ask the Dockhand whether a newer version of itself is out. It looks at its own container through the Docker socket it was given and compares the image it runs from with the newest one published, so it asks the internet each time and needs that socket: a Dockhand without it cannot look, and says so with checked false. This only looks. No tool here updates Dockhand itself, which replaces its own container and is done from its web interface or by whoever runs it; the containers it manages have update tools of their own.",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, _ struct{}) (*mcp.CallToolResult, updateCheckOut, error) {
		check, err := r.dock.GetSelfUpdateCheck(ctx)
		if err != nil {
			return nil, updateCheckOut{}, fmt.Errorf("the Dockhand could not be asked whether an update is out: %w", refusal(err))
		}
		found := check.Answer
		out := updateCheckOut{UpdateAvailable: found.UpdateAvailable, Checked: found.Error == "", Image: found.CurrentImage, NewerImage: found.NewImage, LatestVersion: found.LatestVersion, Digest: found.CurrentDigest, NewerDigest: found.NewDigest, Container: found.ContainerName, ComposeManaged: found.IsComposeManaged, LocalImage: found.IsLocalImage}

		switch {
		case noSocket(found.Error):
			out.Note = "Dockhand could not look: it finds its own container through the Docker socket at /var/run/docker.sock, and this one was not given it (" + found.Error + ")"
		case found.Error != "":
			out.Note = "Dockhand could not look: " + found.Error
		case out.UpdateAvailable && out.ComposeManaged:
			out.Note = "its container was made by a compose file: the image named there is what to change, or the next deploy of that file puts the old version back"
		case out.LocalImage:
			out.Note = "the image it runs from was built on its host, so there is nothing published to compare it with"
		default:
		}

		return nil, out, nil
	})

	type aboutIn struct {
		What    string `json:"what,omitempty"    jsonschema:"licence, the terms Dockhand is used under (the default); privacy, its privacy policy; or dependencies, the software it is built from and runs"`
		Offset  int    `json:"offset,omitempty"  jsonschema:"for licence and privacy: the character to start from, to read on from where the last answer stopped"`
		Limit   int    `json:"limit,omitempty"   jsonschema:"for licence and privacy: how many characters to give, 4000 by default and 30000 at most. For dependencies: how many to list, 50 by default and 500 at most"`
		Find    string `json:"find,omitempty"    jsonschema:"for dependencies: only the ones with this in their name, whatever the case"`
		Licence string `json:"licence,omitempty" jsonschema:"for dependencies: only the ones under this licence, as licences names it (MIT, Apache-2.0)"`
	}
	type aboutOut struct {
		What         string          `json:"what"`
		Text         string          `json:"text,omitempty"         jsonschema:"for licence and privacy: the text, from offset"`
		Characters   int             `json:"characters,omitempty"   jsonschema:"how long the whole text is"`
		Next         int             `json:"next,omitempty"         jsonschema:"the offset to ask with for the rest; absent when this was the end of it"`
		Total        int             `json:"total,omitempty"        jsonschema:"for dependencies: how many there are in all"`
		Matched      int             `json:"matched,omitempty"      jsonschema:"how many of them find and licence leave"`
		Licences     []licenceCount  `json:"licences,omitempty"     jsonschema:"how many of all of them are under each licence, the most first"`
		Dependencies []dependencyRow `json:"dependencies,omitempty" jsonschema:"the ones matched, by name, up to limit"`
		Note         string          `json:"note,omitempty"`
	}
	add(r, readTool, &mcp.Tool{
		Name:        "server_about",
		Description: "Read what the Dockhand says about itself as a product: the licence it is used under, its privacy policy, or the software it is built from and runs, each with its version and its own licence. The two texts are long and come a part at a time: next is the offset to ask with for the rest. The dependencies are counted by licence and listed by name, narrowed with find and licence. It is the same for every Dockhand of a version; server_info describes the one being asked.",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in aboutIn) (*mcp.CallToolResult, aboutOut, error) {
		what := strings.ToLower(strings.TrimSpace(in.What))
		switch what {
		case "", "license":
			what = aboutLicence
		case aboutLicence, aboutPrivacy, aboutDependencies:
		default:
			return nil, aboutOut{}, fmt.Errorf("what is %q: it is licence, privacy or dependencies", in.What)
		}
		if in.Offset < 0 || in.Limit < 0 {
			return nil, aboutOut{}, errors.New("offset and limit are not negative")
		}
		out := aboutOut{What: what}

		if what == aboutDependencies {
			limit := min(cmp.Or(in.Limit, defaultDependencies), maxDependencies)
			find, under := strings.ToLower(strings.TrimSpace(in.Find)), strings.TrimSpace(in.Licence)

			listed, err := r.dock.GetDependencies(ctx)
			if err != nil {
				return nil, out, fmt.Errorf("what the Dockhand is built from could not be read: %w", refusal(err))
			}
			out.Total = len(listed.Answer)

			counts := map[string]int{}
			rows := []dependencyRow{}
			for _, dependency := range listed.Answer {
				counts[dependency.License]++
				if strings.Contains(strings.ToLower(dependency.Name), find) && (under == "" || strings.EqualFold(dependency.License, under)) {
					rows = append(rows, dependencyRow{Name: dependency.Name, Version: dependency.Version, Licence: dependency.License, Repository: dependency.Repository})
				}
			}
			for licence, count := range counts {
				out.Licences = append(out.Licences, licenceCount{Licence: cmp.Or(licence, "(not stated)"), Count: count})
			}
			slices.SortFunc(out.Licences, mostFirst)
			slices.SortFunc(rows, byDependencyName)

			out.Matched = len(rows)
			out.Dependencies = rows[:min(len(rows), limit)]
			switch {
			case out.Matched == 0:
				out.Note = fmt.Sprintf("none of the %d match: licences names the licences there are", out.Total)
			case out.Matched > limit:
				out.Note = fmt.Sprintf("the first %d of %d by name: narrow with find or licence, or raise limit", limit, out.Matched)
			default:
			}

			return nil, out, nil
		}

		limit := min(cmp.Or(in.Limit, defaultAboutText), maxAboutText)
		var text string
		if what == aboutPrivacy {
			privacy, err := r.dock.GetLegalPrivacy(ctx, dockhand.GetLegalPrivacyOptions{})
			if err != nil {
				return nil, out, fmt.Errorf("the Dockhand's privacy policy could not be read: %w", refusal(err))
			}
			text = privacy.Answer.Content
		} else {
			licence, err := r.dock.GetLegalLicense(ctx, dockhand.GetLegalLicenseOptions{})
			if err != nil {
				return nil, out, fmt.Errorf("the Dockhand's licence could not be read: %w", refusal(err))
			}
			text = licence.Answer.Content
		}

		runes := []rune(text)
		out.Characters = len(runes)
		if in.Offset >= len(runes) {
			out.Note = fmt.Sprintf("the text is %d characters long, and offset %d is past its end", len(runes), in.Offset)

			return nil, out, nil
		}
		end := min(in.Offset+limit, len(runes))
		out.Text = string(runes[in.Offset:end])
		if end < len(runes) {
			out.Next = end
			out.Note = fmt.Sprintf("characters %d to %d of %d: ask again with offset %d for the rest", in.Offset, end, len(runes), end)
		}

		return nil, out, nil
	})
}
