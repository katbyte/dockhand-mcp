package tools

import (
	"cmp"
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"

	"github.com/katbyte/dockhand-mcp/sdk/dockhand"
	"github.com/katbyte/pandorest/client"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

const (
	// maxIconPicture is the largest picture sent as an icon, in bytes. Dockhand refuses one much larger, a host's sooner than a container's.
	maxIconPicture = 200_000
	// selfhstIcon and customIcon are how Dockhand writes an icon that is one of selfh.st's, and one that is a picture it was sent.
	selfhstIcon = "selfhst:"
	customIcon  = "custom:"
	// plainEnvIcon is the icon an environment has until it is given another, and has again once its own is removed.
	plainEnvIcon = "globe"
	// defaultIconsFound and maxIconsFound are how many of selfh.st's icons icon_search gives unless told, and at most.
	defaultIconsFound = 25
	maxIconsFound     = 100

	// The three things an icon is of.
	iconOfContainer   = "container"
	iconOfStack       = "stack"
	iconOfEnvironment = "environment"
)

// iconsAre is what every icon tool says an icon is.
const iconsAre = "An icon is how Dockhand's web interface draws a container, a compose stack or an environment (Docker host): the name of one of Dockhand's own icons, selfhst: and the reference of one of selfh.st's, as icon_search finds them, or a picture that was sent. It is Dockhand's own note, and nothing on a Docker host is changed by one"

// selfhstRef is a reference of selfh.st's as Dockhand takes one.
var selfhstRef = regexp.MustCompile(`^[a-z0-9][a-z0-9._-]*$`)

// pictureKinds is the pictures Dockhand was seen to take for an icon, by what their first bytes say they are, each with the ending a file of it has.
var pictureKinds = map[string]string{"image/png": ".png", "image/jpeg": ".jpg", "image/gif": ".gif", "image/webp": ".webp"}

// iconOfArg names what an icon is of: a container or a stack on an environment, or the environment itself.
type iconOfArg struct {
	envArg

	Container string `json:"container,omitempty" jsonschema:"the container whose icon it is, by its name as container_list gives it"`
	Stack     string `json:"stack,omitempty"     jsonschema:"the compose stack whose icon it is, by its name as stack_list gives it. With neither this nor container, it is the environment's own icon"`
}

// iconTarget is what an icon is of, as it stands: whether it is there, the icon it has, and for a stack how Dockhand keeps it.
type iconTarget struct {
	Env   environment
	Kind  string
	Name  string
	Icon  string
	There bool
	Kept  string
}

// called is the target in words: the container web on alpha, the environment alpha.
func (t *iconTarget) called() string {
	if t.Kind == iconOfEnvironment {
		return "the environment " + t.Env.Name
	}

	return fmt.Sprintf("the %s %s on %s", t.Kind, t.Name, t.Env.Name)
}

// iconNow is the icon a target has, read from Dockhand, and "" for none.
func (r *registry) iconNow(ctx context.Context, t *iconTarget) (string, error) {
	switch t.Kind {
	case iconOfContainer:
		given, err := r.dock.GetContainerIcons(ctx, dockhand.GetContainerIconsOptions{Env: t.Env.ID})
		if err != nil {
			return "", refusal(err)
		}

		return given.Answer[t.Name], nil
	case iconOfStack:
		sources, err := r.dock.GetStacksSources(ctx, dockhand.GetStacksSourcesOptions{Env: t.Env.ID})
		if err != nil {
			return "", refusal(err)
		}
		t.Kept = keptNowhere
		if source, has := sources.Answer[t.Name]; has {
			t.Kept = string(cmp.Or(source.SourceType, keptInternal))

			return source.Icon, nil
		}

		return "", nil
	default:
		record, err := r.dock.GetEnvironmentsById(ctx, t.Env.ID)
		if err != nil {
			return "", refusal(err)
		}

		return record.Answer.Icon, nil
	}
}

// iconOf is what a call's icon is of, with whether it is there and the icon it has. It names one environment, and a container or a stack or neither.
func (r *registry) iconOf(ctx context.Context, in iconOfArg) (*iconTarget, error) {
	container, stack := strings.TrimSpace(in.Container), strings.TrimSpace(in.Stack)
	if container != "" && stack != "" {
		return nil, errors.New("give container or stack, not both: an icon is one thing's. With neither it is the environment's own")
	}
	env, err := r.one(ctx, in.envArg)
	if err != nil {
		return nil, err
	}

	t := &iconTarget{Env: env, Kind: iconOfEnvironment, Name: env.Name, There: true}
	switch {
	case container != "":
		t.Kind, t.Name = iconOfContainer, container
		found, failed := r.containerNamed(ctx, env, container)
		if failed != nil {
			return nil, fmt.Errorf("the containers of %s could not be listed: %w", env.Name, failed)
		}
		t.There = found != nil
	case stack != "":
		t.Kind, t.Name = iconOfStack, stack
		stacks, failed := r.stacksOf(ctx, env)
		if failed != nil {
			return nil, fmt.Errorf("the stacks of %s could not be listed: %w", env.Name, failed)
		}
		t.There = slices.ContainsFunc(stacks, func(s dockhand.GetStacksAnswer) bool { return s.Name == stack })
	default:
	}
	if t.Icon, err = r.iconNow(ctx, t); err != nil {
		return nil, fmt.Errorf("the icon of %s could not be read: %w", t.called(), err)
	}

	return t, nil
}

// pictureAt is a picture on this machine as Dockhand takes one for an icon: the file's bytes as a data: address, with what kind of picture it is. A
// file that is no picture is refused here, since Dockhand takes anything at all for an environment's.
func pictureAt(file string) (address, kind string, err error) {
	if !filepath.IsAbs(file) {
		return "", "", fmt.Errorf("file is %q: a picture is given by its full path from the top, on the machine this tool runs on", file)
	}
	info, err := os.Stat(file)
	switch {
	case err != nil:
		return "", "", fmt.Errorf("%s could not be read: %w", file, err)
	case info.IsDir():
		return "", "", fmt.Errorf("%s is a folder, and an icon is one picture", file)
	case info.Size() > maxIconPicture:
		return "", "", fmt.Errorf("%s is %d bytes, and a picture for an icon is %d at most: Dockhand refuses a larger one, and draws it small in any case", file, info.Size(), maxIconPicture)
	default:
	}
	content, err := os.ReadFile(file) //nolint:gosec // G304: the path is a picture the operator asked to be sent
	if err != nil {
		return "", "", fmt.Errorf("%s could not be read: %w", file, err)
	}
	kind = http.DetectContentType(content)
	if pictureKinds[kind] == "" {
		return "", "", fmt.Errorf("%s is no PNG, JPEG, GIF or WebP picture, by what it holds: Dockhand takes no other for an icon, and for an environment's would save it all the same", file)
	}

	return "data:" + kind + ";base64," + base64.StdEncoding.EncodeToString(content), kind, nil
}

// selfhst is every icon selfh.st has, as Dockhand lists them. Dockhand fetches the list from selfh.st's own address and keeps it a week.
func (r *registry) selfhst(ctx context.Context) ([]dockhand.GetIconsSelfhstManifestAnswer, error) {
	listed, err := r.dock.GetIconsSelfhstManifest(ctx)
	if err != nil {
		return nil, fmt.Errorf("the list of selfh.st's icons could not be had, which Dockhand fetches from selfh.st and keeps a week: %w", refusal(err))
	}

	return listed.Answer, nil
}

// savedHere writes what a call answered to a new file on this machine, for its owner alone, and gives its size. It never writes over a file.
func savedHere(to string, from io.Reader) (int64, error) {
	file, err := os.OpenFile(to, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600) //nolint:gosec // G304: the path is where the operator asked for it to be written
	if err != nil {
		return 0, fmt.Errorf("it could not be written here: %w", err)
	}
	size, err := io.Copy(file, from)
	if closed := file.Close(); err == nil {
		err = closed
	}
	if err != nil {
		// a part of a picture passes for the whole: it is this call's own file, made a moment ago, and is taken away again
		_ = os.Remove(to)

		return 0, fmt.Errorf("it was not all fetched, and the part written to %s was removed: %w", to, err)
	}

	return size, nil
}

//nolint:gocyclo // five tools registered in one function, each a handler of its own: the count is theirs added up
func registerIconTools(r *registry) {
	type iconRow struct {
		Env        string            `json:"env"`
		Icon       string            `json:"icon,omitempty"       jsonschema:"the environment's own icon: a name, selfhst: and a reference, or custom: for a picture that was sent"`
		Containers map[string]string `json:"containers,omitempty" jsonschema:"the icon each container that was given one has, by the container's name"`
		Stacks     map[string]string `json:"stacks,omitempty"     jsonschema:"the icon each stack that was given one has, by the stack's name"`
	}
	type iconListOut struct {
		Icons  []iconRow    `json:"icons"            jsonschema:"what was given an icon, for each environment asked"`
		Failed []envFailure `json:"failed,omitempty" jsonschema:"the environments that could not be asked, and why; the others are still given"`
	}
	add(r, readTool, &mcp.Tool{
		Name:        "icon_list",
		Description: "List the icons Dockhand was given, on every environment (Docker host) or on one: the environment's own, and those of the containers and the compose stacks that were given one. " + iconsAre + ". What was given none is drawn with an icon Dockhand picks by itself and is not listed. A container's and a stack's icon is kept under its name, so a name listed here need be no container's or stack's now.",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in envArg) (*mcp.CallToolResult, iconListOut, error) {
		envs, err := r.pick(ctx, in)
		if err != nil {
			return nil, iconListOut{}, err
		}
		type held struct {
			Own        string
			Containers map[string]string
			Stacks     map[string]string
		}
		out := iconListOut{Icons: make([]iconRow, 0, len(envs))}
		for _, answer := range ask(ctx, envs, func(ctx context.Context, env environment) (held, error) {
			own, err := r.iconNow(ctx, &iconTarget{Env: env, Kind: iconOfEnvironment})
			if err != nil {
				return held{}, err
			}
			containers, err := r.dock.GetContainerIcons(ctx, dockhand.GetContainerIconsOptions{Env: env.ID})
			if err != nil {
				return held{}, refusal(err)
			}
			sources, err := r.dock.GetStacksSources(ctx, dockhand.GetStacksSourcesOptions{Env: env.ID})
			if err != nil {
				return held{}, refusal(err)
			}
			stacks := map[string]string{}
			for name, source := range sources.Answer {
				if source.Icon != "" {
					stacks[name] = source.Icon
				}
			}

			return held{Own: own, Containers: containers.Answer, Stacks: stacks}, nil
		}) {
			if answer.Err != nil {
				out.Failed = append(out.Failed, envFailure{Env: answer.Env.Name, Error: answer.Err.Error()})

				continue
			}
			out.Icons = append(out.Icons, iconRow{Env: answer.Env.Name, Icon: answer.Value.Own, Containers: answer.Value.Containers, Stacks: answer.Value.Stacks})
		}

		return nil, out, nil
	})

	type iconSetIn struct {
		iconOfArg
		dryRunArg

		Icon      string `json:"icon,omitempty"       jsonschema:"the icon to give it: the name of one of Dockhand's own icons, as box or server, or selfhst: and a reference icon_search found, as selfhst:grafana. This or file"`
		File      string `json:"file,omitempty"       jsonschema:"a picture to send for it, by its full path on the machine this tool runs on: a PNG, JPEG, GIF or WebP of 200,000 bytes at most. This or icon"`
		EvenFound bool   `json:"even_found,omitempty" jsonschema:"give the icon to a stack Dockhand only found on the host and has no record of. Dockhand then keeps a record of the stack with no file, which stays when the icon is removed and goes only with the stack"`
	}
	type iconSetOut struct {
		Env       string   `json:"env"`
		Container string   `json:"container,omitempty"`
		Stack     string   `json:"stack,omitempty"`
		Icon      string   `json:"icon"                jsonschema:"the icon it has now, read back: a name, selfhst: and a reference, or custom: for a picture that was sent; for a dry run, the one it has before anything is done"`
		Changes   []string `json:"changes"             jsonschema:"what was done, in words; empty when nothing needed doing"`
		DryRun    bool     `json:"dry_run,omitempty"   jsonschema:"nothing was done: changes says what would be"`
		Note      string   `json:"note,omitempty"`
	}
	add(r, writeTool, &mcp.Tool{
		Name:        "icon_set",
		Description: "Give a container, a compose stack or an environment (Docker host) an icon: one by name, or a picture sent from the machine this tool runs on. " + iconsAre + ". Dockhand checks no name it is given and saves any text, so a name is only checked for being one; a picture is checked for being a picture, since Dockhand saves anything at all as an environment's. A name no container or stack has is refused, which Dockhand would keep an icon for all the same, and for a stack would list as a stack with nothing in it. A stack Dockhand only found on the host is refused unless even_found is given: an icon makes Dockhand keep a record of it for good.",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in iconSetIn) (*mcp.CallToolResult, iconSetOut, error) {
		icon, file := strings.TrimSpace(in.Icon), strings.TrimSpace(in.File)
		switch ref, isSelfhst := strings.CutPrefix(icon, selfhstIcon); {
		case (icon == "") == (file == ""):
			return nil, iconSetOut{}, errors.New("give icon or file, one of them: the name of an icon, or the path of a picture to send")
		case strings.HasPrefix(icon, customIcon):
			return nil, iconSetOut{}, fmt.Errorf("icon is %q: custom: is how Dockhand marks a picture it was sent, and is given by sending one with file", icon)
		case isSelfhst && !selfhstRef.MatchString(ref):
			return nil, iconSetOut{}, fmt.Errorf("icon is %q: after selfhst: comes a reference as icon_search gives them, in lower-case letters, digits and hyphens", icon)
		case !isSelfhst && strings.ContainsAny(icon, " \t\n:/"):
			return nil, iconSetOut{}, fmt.Errorf("icon is %q: the name of one of Dockhand's own icons is one word, as box or hard-drive. Dockhand would save it all the same", icon)
		default:
		}
		var picture, kind string
		if file != "" {
			var err error
			if picture, kind, err = pictureAt(file); err != nil {
				return nil, iconSetOut{}, err
			}
		}
		t, err := r.iconOf(ctx, in.iconOfArg)
		if err != nil {
			return nil, iconSetOut{}, err
		}
		out := iconSetOut{Env: t.Env.Name, Icon: t.Icon, Changes: []string{}, DryRun: in.DryRun}
		switch t.Kind {
		case iconOfContainer:
			out.Container = t.Name
		case iconOfStack:
			out.Stack = t.Name
		default:
		}
		switch {
		case !t.There && t.Kind == iconOfStack:
			return nil, out, fmt.Errorf("no stack is named %q on %s: stack_list gives the ones there are. Dockhand would keep the icon under the name all the same, and list a stack of that name with nothing in it", t.Name, t.Env.Name)
		case !t.There:
			return nil, out, fmt.Errorf("no container is named %q on %s: container_list gives the ones there are. Dockhand would keep the icon under the name all the same", t.Name, t.Env.Name)
		case t.Kind == iconOfStack && t.Kept == keptNowhere && !in.EvenFound:
			return nil, out, fmt.Errorf("Dockhand only found the stack %s on %s and has no record of it. Given an icon it keeps one, with no compose file, which stays when the icon is removed and goes only with the stack: say even_found to give it the icon all the same", t.Name, t.Env.Name) //nolint:staticcheck // ST1005: Dockhand is a name
		case icon != "" && icon == t.Icon:
			out.Note = "nothing to do: it has that icon already"

			return nil, out, nil
		default:
		}

		what := "the icon " + icon
		if file != "" {
			what = fmt.Sprintf("the picture %s, a %s, as its icon", file, strings.TrimPrefix(kind, "image/"))
		}
		if in.DryRun {
			out.Changes = append(out.Changes, fmt.Sprintf("would give %s %s", t.called(), what))
			if t.Kind == iconOfStack && t.Kept == keptNowhere {
				out.Note = "Dockhand would keep a record of the stack from then on, with no compose file"
			}

			return nil, out, nil
		}

		var answered string
		switch {
		case t.Kind == iconOfContainer:
			set, failed := r.dock.PostContainerIconsByName(ctx, t.Name, dockhand.PostContainerIconsByNameRequest{Icon: icon, Image: picture}, dockhand.PostContainerIconsByNameOptions{Env: t.Env.ID})
			if failed != nil {
				return nil, out, fmt.Errorf("%s was not given the icon: %w", t.called(), refusal(failed))
			}
			answered = set.Answer.Icon
		case t.Kind == iconOfStack:
			set, failed := r.dock.PostStacksByNameIcon(ctx, t.Name, dockhand.PostStacksByNameIconRequest{Icon: icon, Image: picture}, dockhand.PostStacksByNameIconOptions{Env: t.Env.ID})
			if failed != nil {
				return nil, out, fmt.Errorf("%s was not given the icon: %w", t.called(), refusal(failed))
			}
			answered = set.Answer.Icon
			if t.Kept == keptNowhere {
				out.Note = "Dockhand keeps a record of the stack from now on, with no compose file: stack_get says how it is kept, and stack_adopt gives it its file"
			}
		case file != "":
			set, failed := r.dock.PostEnvironmentsByIdIcon(ctx, t.Env.ID, dockhand.PostEnvironmentsByIdIconRequest{Image: picture})
			if failed != nil {
				return nil, out, fmt.Errorf("%s was not given the icon: %w", t.called(), refusal(failed))
			}
			answered = set.Answer.Icon
		default:
			set, failed := r.dock.PutEnvironmentsById(ctx, t.Env.ID, dockhand.PutEnvironmentsByIdRequest{Icon: icon})
			if failed != nil {
				return nil, out, fmt.Errorf("%s was not given the icon: %w", t.called(), refusal(failed))
			}
			answered = set.Answer.Icon
		}
		out.Changes = append(out.Changes, fmt.Sprintf("gave %s %s", t.called(), what))

		back, err := r.iconNow(ctx, t)
		if err != nil {
			out.Icon = answered
			out.Note = note(out.Note, "it was given, and could not be read back: icon is what Dockhand answered with")

			return nil, out, nil //nolint:nilerr // the icon was given: that it could not be read back is said in the answer
		}
		out.Icon = back
		if (icon != "" && back != icon) || (file != "" && !strings.HasPrefix(back, customIcon)) {
			return nil, out, fmt.Errorf("%s was answered as given the icon, and what it has is %s", t.called(), cmp.Or(back, "none"))
		}

		return nil, out, nil
	})

	type iconDeleteIn struct {
		iconOfArg

		Confirm bool `json:"confirm,omitempty" jsonschema:"remove it: without this the answer says what would go and nothing is removed"`
	}
	type iconDeleteOut struct {
		Env       string   `json:"env"`
		Container string   `json:"container,omitempty"`
		Stack     string   `json:"stack,omitempty"`
		Was       string   `json:"was"                 jsonschema:"the icon it had"`
		Icon      string   `json:"icon,omitempty"      jsonschema:"the icon it has now, read back: for an environment, the plain one Dockhand gives it; absent for a container or a stack, which Dockhand picks an icon for by itself"`
		Removed   bool     `json:"removed"             jsonschema:"its icon was removed, and is read back as gone"`
		WouldGo   []string `json:"would_go,omitempty"  jsonschema:"what removing it takes away, said before anything is removed"`
		Note      string   `json:"note,omitempty"`
	}
	add(r, deleteTool, &mcp.Tool{
		Name:        "icon_delete",
		Description: "Remove the icon a container, a compose stack or an environment (Docker host) was given, a picture that was sent for it included: a container and a stack are then drawn with an icon Dockhand picks by itself, and an environment with the plain one it started with. " + iconsAre + ". The record Dockhand keeps of a stack stays. A name that holds an icon from when a container or a stack had it can have it removed. Without confirm the answer says what would go and removes nothing.",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in iconDeleteIn) (*mcp.CallToolResult, iconDeleteOut, error) {
		t, err := r.iconOf(ctx, in.iconOfArg)
		if err != nil {
			return nil, iconDeleteOut{}, err
		}
		out := iconDeleteOut{Env: t.Env.Name, Was: t.Icon}
		switch t.Kind {
		case iconOfContainer:
			out.Container = t.Name
		case iconOfStack:
			out.Stack = t.Name
		default:
			out.Icon = t.Icon
		}
		switch {
		case t.Icon == "" && !t.There:
			return nil, out, fmt.Errorf("no %s is named %q on %s, and the name holds no icon", t.Kind, t.Name, t.Env.Name)
		case t.Icon == "" || (t.Kind == iconOfEnvironment && t.Icon == plainEnvIcon):
			out.Note = fmt.Sprintf("nothing to remove: %s has no icon of its own", t.called())

			return nil, out, nil
		case !t.There:
			out.Note = fmt.Sprintf("no %s is named %s on %s now: the name holds the icon from when one was", t.Kind, t.Name, t.Env.Name)
		default:
		}
		if !in.Confirm {
			out.WouldGo = []string{fmt.Sprintf("the icon of %s, which is %s", t.called(), t.Icon)}
			out.Note = note(out.Note, "nothing was removed: call again with confirm to remove it. Nothing on the Docker host is touched either way")

			return nil, out, nil
		}

		var said bool
		switch t.Kind {
		case iconOfContainer:
			gone, failed := r.dock.DeleteContainerIconsByName(ctx, t.Name, dockhand.DeleteContainerIconsByNameOptions{Env: t.Env.ID})
			if failed != nil {
				return nil, out, fmt.Errorf("the icon of %s was not removed: %w", t.called(), refusal(failed))
			}
			said = gone.Answer.Success
		case iconOfStack:
			gone, failed := r.dock.DeleteStacksByNameIcon(ctx, t.Name, dockhand.DeleteStacksByNameIconOptions{Env: t.Env.ID})
			if failed != nil {
				return nil, out, fmt.Errorf("the icon of %s was not removed: %w", t.called(), refusal(failed))
			}
			said = gone.Answer.Success
		default:
			gone, failed := r.dock.DeleteEnvironmentsByIdIcon(ctx, t.Env.ID)
			if failed != nil {
				return nil, out, fmt.Errorf("the icon of %s was not removed: %w", t.called(), refusal(failed))
			}
			said = gone.Answer.Success
		}
		if !said {
			return nil, out, fmt.Errorf("the removal of the icon of %s was answered without its being said to have succeeded: icon_list says whether it is still there", t.called())
		}
		back, err := r.iconNow(ctx, t)
		if err != nil {
			out.Icon = ""
			out.Note = note(out.Note, "it was removed, and what is there now could not be read: "+err.Error())

			return nil, out, nil //nolint:nilerr // the removal went through: that it could not be read back is said in the answer
		}
		if back == t.Icon {
			return nil, out, fmt.Errorf("the removal of the icon of %s was answered as done, and it still has %s", t.called(), back)
		}
		out.Removed, out.Icon = true, back

		return nil, out, nil
	})

	type iconDownloadIn struct {
		iconOfArg

		Selfhst string `json:"selfhst,omitempty" jsonschema:"fetch one of selfh.st's icons instead, by its reference as icon_search gives it; then no environment, container or stack is named"`
		To      string `json:"to"                jsonschema:"where to write it, as a full path on the machine this tool runs on: a file's name, or a folder that is there to put it in under a name of its own. Always a new file: a path anything is at already is refused"`
	}
	type iconDownloadOut struct {
		Env       string `json:"env,omitempty"`
		Container string `json:"container,omitempty"`
		Stack     string `json:"stack,omitempty"`
		Selfhst   string `json:"selfhst,omitempty"`
		Kind      string `json:"kind"                jsonschema:"what kind of picture it is, as Dockhand sent it: image/webp for one that was sent to it, and image/svg+xml, image/webp or image/png for one of selfh.st's"`
		Saved     string `json:"saved"               jsonschema:"the file written here, as a full path"`
		Bytes     int64  `json:"bytes"               jsonschema:"its size"`
	}
	add(r, readTool, &mcp.Tool{
		Name:        "icon_download",
		Description: "Fetch the picture of an icon and write it to the machine this tool runs on: the picture that was sent for a container, a compose stack or an environment (Docker host), as Dockhand keeps it, or one of selfh.st's icons by its reference. An icon that is only a name has no picture to fetch, and is said to be one. Nothing is changed on the Dockhand; a file is written here, and never over one that is there.",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in iconDownloadIn) (*mcp.CallToolResult, iconDownloadOut, error) {
		to, ref := strings.TrimSpace(in.To), strings.TrimPrefix(strings.TrimSpace(in.Selfhst), selfhstIcon)
		if !filepath.IsAbs(to) {
			return nil, iconDownloadOut{}, fmt.Errorf("to is %q: where to write it is a full path from the top, on the machine this tool runs on", in.To)
		}
		named, _ := in.named()
		if ref != "" && (named != "" || strings.TrimSpace(in.Container) != "" || strings.TrimSpace(in.Stack) != "") {
			return nil, iconDownloadOut{}, errors.New("selfhst is one of selfh.st's icons, and is no environment's, container's or stack's: give it alone, or leave it out")
		}

		out := iconDownloadOut{Selfhst: ref}
		var (
			picture *http.Response
			name    string
		)
		if ref != "" {
			if !selfhstRef.MatchString(ref) {
				return nil, out, fmt.Errorf("selfhst is %q: a reference is as icon_search gives them, in lower-case letters, digits and hyphens", in.Selfhst)
			}
			// Dockhand answers a reference it has no icon for with a picture of nothing, so the list is asked first
			listed, err := r.selfhst(ctx)
			if err != nil {
				return nil, out, err
			}
			if !slices.ContainsFunc(listed, func(i dockhand.GetIconsSelfhstManifestAnswer) bool { return i.Reference == ref }) {
				return nil, out, fmt.Errorf("selfh.st has no icon with the reference %q: icon_search finds the ones it has", ref)
			}
			fetched, err := r.dock.GetIconsSelfhstByRef(ctx, ref)
			if err != nil {
				return nil, out, fmt.Errorf("the icon %s could not be fetched: %w", ref, refusal(err))
			}
			picture, name = fetched.HttpResponse, ref
		} else {
			t, err := r.iconOf(ctx, in.iconOfArg)
			if err != nil {
				return nil, out, err
			}
			out.Env, name = t.Env.Name, t.Name
			var failed error
			switch t.Kind {
			case iconOfContainer:
				out.Container = t.Name
				var fetched dockhand.GetContainerIconsByNameResponse
				fetched, failed = r.dock.GetContainerIconsByName(ctx, t.Name, dockhand.GetContainerIconsByNameOptions{Env: t.Env.ID})
				picture = fetched.HttpResponse
			case iconOfStack:
				out.Stack = t.Name
				var fetched dockhand.GetStacksByNameIconResponse
				fetched, failed = r.dock.GetStacksByNameIcon(ctx, t.Name, dockhand.GetStacksByNameIconOptions{Env: t.Env.ID})
				picture = fetched.HttpResponse
			default:
				var fetched dockhand.GetEnvironmentsByIdIconResponse
				fetched, failed = r.dock.GetEnvironmentsByIdIcon(ctx, t.Env.ID)
				picture = fetched.HttpResponse
			}
			switch {
			case client.IsNotFound(failed) && t.Icon != "":
				return nil, out, fmt.Errorf("%s has no picture to fetch: its icon is %s, a name and no picture that was sent", t.called(), t.Icon)
			case client.IsNotFound(failed):
				return nil, out, fmt.Errorf("%s has no picture to fetch: it was given no icon", t.called())
			case failed != nil:
				return nil, out, fmt.Errorf("the icon of %s could not be fetched: %w", t.called(), refusal(failed))
			default:
			}
		}
		defer func() { _ = picture.Body.Close() }()

		// a folder that is there takes the picture under the name of what it is of, with the ending its kind has
		out.Kind, _, _ = strings.Cut(picture.Header.Get("Content-Type"), ";")
		if info, statErr := os.Stat(to); statErr == nil && info.IsDir() {
			ending := cmp.Or(pictureKinds[out.Kind], map[bool]string{true: ".svg", false: ".img"}[out.Kind == "image/svg+xml"])
			to = filepath.Join(to, strings.ReplaceAll(name, "/", "_")+ending)
		}
		size, err := savedHere(to, picture.Body)
		if err != nil {
			return nil, out, err
		}
		out.Saved, out.Bytes = to, size

		return nil, out, nil
	})

	type iconSearchIn struct {
		Query string `json:"query"           jsonschema:"what to look for among selfh.st's icons: words, each of which is to be in an icon's name, reference, category or tags, whatever its case"`
		Limit int    `json:"limit,omitempty" jsonschema:"the most icons to give, 25 unless given and 100 at most; total counts every one that matched"`
	}
	type iconFound struct {
		Name     string `json:"name"`
		Icon     string `json:"icon"               jsonschema:"what icon_set takes to give it: selfhst: and its reference"`
		Category string `json:"category,omitempty"`
		Tags     string `json:"tags,omitempty"`
	}
	type iconSearchOut struct {
		Icons []iconFound `json:"icons"          jsonschema:"the icons that matched, by name"`
		Total int         `json:"total"          jsonschema:"how many matched, of which icons holds limit at most"`
		Of    int         `json:"of"             jsonschema:"how many icons selfh.st has"`
		Note  string      `json:"note,omitempty"`
	}
	add(r, readTool, &mcp.Tool{
		Name:        "icon_search",
		Description: "Find an icon among the ones selfh.st keeps for self-hosted applications, a few thousand of them, by words in its name, reference, category or tags: each is given with what icon_set takes to give it to a container, a compose stack or an environment. Dockhand fetches the list from selfh.st itself and keeps it a week, so a Dockhand that cannot reach selfh.st and never has answers that it cannot. Dockhand's own icons, the ones given by a plain name, are not in this list.",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in iconSearchIn) (*mcp.CallToolResult, iconSearchOut, error) {
		words := strings.Fields(strings.ToLower(in.Query))
		if len(words) == 0 {
			return nil, iconSearchOut{}, errors.New("query is required: words to look for in an icon's name, reference, category or tags")
		}
		limit := cmp.Or(in.Limit, defaultIconsFound)
		if limit < 1 || limit > maxIconsFound {
			return nil, iconSearchOut{}, fmt.Errorf("limit is %d: it is from 1 to %d", in.Limit, maxIconsFound)
		}
		listed, err := r.selfhst(ctx)
		if err != nil {
			return nil, iconSearchOut{}, err
		}

		out := iconSearchOut{Icons: []iconFound{}, Of: len(listed)}
		for i := range listed {
			held := strings.ToLower(listed[i].Name + " " + listed[i].Reference + " " + listed[i].Category + " " + listed[i].Tags)
			if slices.ContainsFunc(words, func(word string) bool { return !strings.Contains(held, word) }) {
				continue
			}
			out.Icons = append(out.Icons, iconFound{Name: listed[i].Name, Icon: selfhstIcon + listed[i].Reference, Category: listed[i].Category, Tags: listed[i].Tags})
		}
		slices.SortStableFunc(out.Icons, func(a, b iconFound) int { return cmp.Compare(strings.ToLower(a.Name), strings.ToLower(b.Name)) })
		out.Total = len(out.Icons)
		switch {
		case out.Total == 0:
			out.Note = fmt.Sprintf("none of selfh.st's %d icons has %s in its name, reference, category or tags", out.Of, strings.Join(words, " and "))
		case out.Total > limit:
			out.Icons = out.Icons[:limit]
			out.Note = fmt.Sprintf("the first %d of %d by name: more words narrow it, and limit gives more", limit, out.Total)
		default:
		}

		return nil, out, nil
	})
}
