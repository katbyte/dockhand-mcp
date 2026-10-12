package tools

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"net/http"
	"path"
	"slices"
	"strings"
	"unicode/utf8"

	"github.com/katbyte/dockhand-mcp/sdk/dockhand"
	"github.com/katbyte/pandorest/client"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

const (
	// defaultStats is how many containers container_stats lists unless told
	defaultStats = 50
	// defaultEntries is how many of a directory's entries container_files lists unless told
	defaultEntries = 200
)

// What container_stats puts first.
const (
	byMemory = "memory"
	byCPU    = "cpu"
)

// containerUse is what one running container is using.
type containerUse struct {
	Env           string  `json:"env"             jsonschema:"the environment (Docker host) it is on"`
	Name          string  `json:"name"`
	CPUPercent    float64 `json:"cpu_percent"     jsonschema:"the share of the host's processors it is using, where 100 is one whole processor"`
	MemoryMB      float64 `json:"memory_mb"       jsonschema:"the memory it holds, less what is only file cache"`
	MemoryLimitMB float64 `json:"memory_limit_mb" jsonschema:"the most memory it may hold: its own limit, or the whole host's when it has none"`
	MemoryPercent float64 `json:"memory_percent"  jsonschema:"how much of that it holds"`
	NetRxMB       float64 `json:"net_rx_mb"       jsonschema:"what it has received over the network since it started"`
	NetTxMB       float64 `json:"net_tx_mb"`
	DiskReadMB    float64 `json:"disk_read_mb"    jsonschema:"what it has read from disk since it started"`
	DiskWriteMB   float64 `json:"disk_write_mb"`
}

// statsOf is what each running container of an environment is using, as the call is made: Dockhand samples every one, which takes a second or two.
func (r *registry) statsOf(ctx context.Context, env environment) ([]dockhand.GetContainersStatsAnswer, error) {
	measured, err := r.dock.GetContainersStats(ctx, dockhand.GetContainersStatsOptions{Env: env.ID})
	if err != nil {
		return nil, refusal(err)
	}

	return measured.Answer, nil
}

// inside is the one container a call looks into, which must be running: Dockhand looks inside a container by running a command in it.
func (r *registry) inside(ctx context.Context, in containerArg, what string) (located, error) {
	at, err := r.find(ctx, in)
	if err != nil {
		return located{}, err
	}
	if !running(string(at.Container.State)) {
		return at, fmt.Errorf("%s on %s is %s, and %s only of a container that is running: Dockhand looks inside one by running a command in it", at.Container.Name, at.Env.Name, at.Container.State, what)
	}

	return at, nil
}

//nolint:gocyclo,maintidx // five tools registered in one function, each a handler of its own: the count is theirs added up
func registerContainerInsideTools(r *registry) {
	type containerStatsIn struct {
		envArg

		Container string `json:"container,omitempty" jsonschema:"only the container of this name, or whose id starts with this"`
		Sort      string `json:"sort,omitempty"      jsonschema:"what to put first: memory, which is the default, or cpu"`
		Limit     int    `json:"limit,omitempty"     jsonschema:"the most containers to list, 50 unless given; total counts every one that was measured"`
	}
	type containerStatsOut struct {
		Containers []containerUse `json:"containers"       jsonschema:"what each running container is using, the heaviest first"`
		Total      int            `json:"total"            jsonschema:"how many running containers were measured"`
		Failed     []envFailure   `json:"failed,omitempty" jsonschema:"the environments that could not be asked, and why; the others' containers are still given"`
		Note       string         `json:"note,omitempty"`
	}
	add(r, readTool, &mcp.Tool{
		Name:        "container_stats",
		Description: "Measure what the running containers are using right now, on every Docker host (environment) or on one: each one's share of the processors, the memory it holds against the most it may, and what it has sent, received, read and written since it started. The heaviest are first, by memory unless sort says cpu. It is one sample taken as the call is made, which takes a second or two a host, and says nothing of a minute ago. A container that is not running uses nothing and is not listed.",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in containerStatsIn) (*mcp.CallToolResult, containerStatsOut, error) {
		by := cmp.Or(strings.ToLower(strings.TrimSpace(in.Sort)), byMemory)
		switch {
		case by != byMemory && by != byCPU:
			return nil, containerStatsOut{}, fmt.Errorf("sort is %q: it is %s or %s", in.Sort, byMemory, byCPU)
		case in.Limit < 0:
			return nil, containerStatsOut{}, fmt.Errorf("limit is %d: it is how many containers to list at most, a number above 0, or left out for %d", in.Limit, defaultStats)
		default:
		}
		want := strings.TrimPrefix(strings.TrimSpace(in.Container), "/")

		envs, err := r.pick(ctx, in.envArg)
		if err != nil {
			return nil, containerStatsOut{}, err
		}

		out := containerStatsOut{Containers: []containerUse{}}
		for _, answer := range ask(ctx, envs, r.statsOf) {
			if answer.Err != nil {
				out.Failed = append(out.Failed, envFailure{Env: answer.Env.Name, Error: answer.Err.Error()})

				continue
			}
			for i := range answer.Value {
				s := &answer.Value[i]
				if want != "" && s.Name != want && (len(want) < minIDPrefix || !strings.HasPrefix(s.Id, want)) {
					continue
				}
				out.Containers = append(out.Containers, containerUse{Env: answer.Env.Name, Name: s.Name, CPUPercent: s.CpuPercent, MemoryMB: megabytes(s.MemoryUsage), MemoryLimitMB: megabytes(s.MemoryLimit), MemoryPercent: s.MemoryPercent, NetRxMB: megabytes(s.NetworkRx), NetTxMB: megabytes(s.NetworkTx), DiskReadMB: megabytes(s.BlockRead), DiskWriteMB: megabytes(s.BlockWrite)})
			}
		}
		if len(out.Failed) == len(envs) {
			return nil, out, fmt.Errorf("no environment could be asked what its containers are using: %s: %s", out.Failed[0].Env, out.Failed[0].Error)
		}

		slices.SortStableFunc(out.Containers, func(a, b containerUse) int {
			if by == byCPU {
				return cmp.Or(cmp.Compare(b.CPUPercent, a.CPUPercent), cmp.Compare(b.MemoryMB, a.MemoryMB), cmp.Compare(a.Name, b.Name))
			}

			return cmp.Or(cmp.Compare(b.MemoryMB, a.MemoryMB), cmp.Compare(b.CPUPercent, a.CPUPercent), cmp.Compare(a.Name, b.Name))
		})
		out.Total = len(out.Containers)
		if limit := cmp.Or(in.Limit, defaultStats); out.Total > limit {
			out.Containers = out.Containers[:limit]
			out.Note = fmt.Sprintf("%d containers were measured and the heaviest %d are listed: narrow it by env or container, or raise limit", out.Total, limit)
		}
		if want != "" && out.Total == 0 {
			out.Note = note(out.Note, fmt.Sprintf("no running container is named %s on the environments asked: one that is not running uses nothing and is not measured", want))
		}
		if len(out.Failed) > 0 {
			out.Note = note(out.Note, fmt.Sprintf("%d of the %d environments could not be asked, and their containers are missing from this", len(out.Failed), len(envs)))
		}

		return nil, out, nil
	})

	type containerSize struct {
		Env       string  `json:"env"        jsonschema:"the environment (Docker host) it is on"`
		Name      string  `json:"name"`
		State     string  `json:"state"`
		WrittenMB float64 `json:"written_mb" jsonschema:"what it has written inside itself, over its image and outside its volumes: what is lost when it is made again, and what grows when a container fills a disk"`
		TotalMB   float64 `json:"total_mb"   jsonschema:"what its whole filesystem takes, its image included; containers of one image share the image's part"`
	}
	type containerSizesIn struct {
		envArg

		Limit int `json:"limit,omitempty" jsonschema:"the most containers to list, 50 unless given; total counts every one"`
	}
	type containerSizesOut struct {
		Containers []containerSize `json:"containers"       jsonschema:"each container's size on disk, the one that has written most first"`
		Total      int             `json:"total"`
		Failed     []envFailure    `json:"failed,omitempty" jsonschema:"the environments that could not be asked, and why; the others' containers are still given"`
		Note       string          `json:"note,omitempty"`
	}
	add(r, readTool, &mcp.Tool{
		Name:        "container_sizes",
		Description: "Measure what each container takes on disk, on every Docker host (environment) or on one, running or not: what it has written inside itself over its image, and what its whole filesystem comes to. The one that has written most is first, which is where to look when a host's disk fills: logs written to a file inside the container, a cache, a download that was meant for a volume. What is in a volume is not counted here. Docker walks every container's files to answer, which takes seconds on a host with many.",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in containerSizesIn) (*mcp.CallToolResult, containerSizesOut, error) {
		if in.Limit < 0 {
			return nil, containerSizesOut{}, fmt.Errorf("limit is %d: it is how many containers to list at most, a number above 0, or left out for %d", in.Limit, defaultStats)
		}
		envs, err := r.pick(ctx, in.envArg)
		if err != nil {
			return nil, containerSizesOut{}, err
		}

		out := containerSizesOut{Containers: []containerSize{}}
		for _, answer := range ask(ctx, envs, func(ctx context.Context, env environment) ([]containerSize, error) {
			sizes, failed := r.dock.GetContainersSizes(ctx, dockhand.GetContainersSizesOptions{Env: env.ID})
			if failed != nil {
				return nil, refusal(failed)
			}
			// the sizes are by id alone: the names are the list's
			listed, failed := r.containersOf(ctx, env)
			if failed != nil {
				return nil, failed
			}
			measured := make([]containerSize, 0, len(listed))
			for i := range listed {
				if size, has := sizes.Answer[listed[i].Id]; has {
					measured = append(measured, containerSize{Env: env.Name, Name: listed[i].Name, State: string(listed[i].State), WrittenMB: megabytes(size.SizeRw), TotalMB: megabytes(size.SizeRootFs)})
				}
			}

			return measured, nil
		}) {
			if answer.Err != nil {
				out.Failed = append(out.Failed, envFailure{Env: answer.Env.Name, Error: answer.Err.Error()})

				continue
			}
			out.Containers = append(out.Containers, answer.Value...)
		}
		if len(out.Failed) == len(envs) {
			return nil, out, fmt.Errorf("no environment could be asked what its containers take: %s: %s", out.Failed[0].Env, out.Failed[0].Error)
		}

		slices.SortStableFunc(out.Containers, func(a, b containerSize) int {
			return cmp.Or(cmp.Compare(b.WrittenMB, a.WrittenMB), cmp.Compare(b.TotalMB, a.TotalMB), cmp.Compare(a.Env, b.Env), cmp.Compare(a.Name, b.Name))
		})
		out.Total = len(out.Containers)
		if limit := cmp.Or(in.Limit, defaultStats); out.Total > limit {
			out.Containers = out.Containers[:limit]
			out.Note = fmt.Sprintf("%d containers were measured and the %d that have written most are listed: narrow it by env, or raise limit", out.Total, limit)
		}
		if len(out.Failed) > 0 {
			out.Note = note(out.Note, fmt.Sprintf("%d of the %d environments could not be asked, and their containers are missing from this", len(out.Failed), len(envs)))
		}

		return nil, out, nil
	})

	type containerTopOut struct {
		Env       string              `json:"env"              jsonschema:"the environment (Docker host) the container is on"`
		Container string              `json:"container"`
		Columns   []string            `json:"columns"          jsonschema:"what ps reports of each process, in the order given: PID, USER, TIME and COMMAND as a rule"`
		Processes []map[string]string `json:"processes"        jsonschema:"the processes running in the container, each by the columns' names"`
		Hidden    int                 `json:"hidden,omitempty" jsonschema:"how many secrets on the command lines are hidden"`
		Note      string              `json:"note,omitempty"`
	}
	type containerTopIn struct {
		containerArg
		revealArg
	}
	add(r, readTool, &mcp.Tool{
		Name:        "container_top",
		Description: "List the processes running inside one container, as ps reports them: each one's id, the user it runs as, the processor time it has had and its command line. It is how to see whether the thing a container is for is still running in it, and what else is. Only a running container has any. A password or token passed on a command line is hidden unless reveal_secrets asks for it.",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in containerTopIn) (*mcp.CallToolResult, containerTopOut, error) {
		at, err := r.inside(ctx, in.containerArg, "processes can be listed")
		if err != nil {
			return nil, containerTopOut{}, err
		}
		out := containerTopOut{Env: at.Env.Name, Container: at.Container.Name, Columns: []string{}, Processes: []map[string]string{}}

		top, err := r.dock.GetContainersByIdTop(ctx, at.Container.Id, dockhand.GetContainersByIdTopOptions{Env: at.Env.ID})
		if err != nil {
			return nil, out, fmt.Errorf("the processes of %s could not be listed: %w", out.Container, refusal(err))
		}
		out.Columns = append(out.Columns, top.Answer.Titles...)
		for _, fields := range top.Answer.Processes {
			process := map[string]string{}
			for i, value := range fields {
				if i >= len(out.Columns) {
					break
				}
				if !in.RevealSecrets {
					shown, hidden := maskText(value)
					value, out.Hidden = shown, out.Hidden+hidden
				}
				process[out.Columns[i]] = value
			}
			out.Processes = append(out.Processes, process)
		}
		if out.Hidden > 0 {
			out.Note = hiddenNote(out.Hidden, "told by the names beside them")
		}

		return nil, out, nil
	})

	type fileEntry struct {
		Name     string `json:"name"`
		Type     string `json:"type"               jsonschema:"file, directory or symlink"`
		Size     int    `json:"size"               jsonschema:"in bytes"`
		Mode     string `json:"mode"               jsonschema:"its permissions as ls writes them: rwxr-xr-x"`
		Owner    string `json:"owner"              jsonschema:"the user and group it belongs to, as user:group"`
		Modified string `json:"modified,omitempty" jsonschema:"when it last changed, in the container's own time and to the minute"`
		Target   string `json:"target,omitempty"   jsonschema:"what a symlink points to"`
	}
	type containerFilesIn struct {
		containerArg

		Path  string `json:"path,omitempty"  jsonschema:"the directory to list, from the container's root; left out, the root itself"`
		Limit int    `json:"limit,omitempty" jsonschema:"the most entries to list, 200 unless given; total counts every one"`
	}
	type containerFilesOut struct {
		Env       string      `json:"env"            jsonschema:"the environment (Docker host) the container is on"`
		Container string      `json:"container"`
		Path      string      `json:"path"           jsonschema:"the directory that was listed"`
		Entries   []fileEntry `json:"entries"        jsonschema:"what is in it, directories first and then by name"`
		Total     int         `json:"total"          jsonschema:"how many entries the directory has"`
		Note      string      `json:"note,omitempty"`
	}
	add(r, readTool, &mcp.Tool{
		Name:        "container_files",
		Description: "List what is in a directory inside one running container: each entry's name, whether it is a file, a directory or a link, its size, permissions, owner and when it changed. It is the container's filesystem as the container sees it, its image's files and what it has written and what is mounted into it alike. Dockhand lists it by running ls in the container, so the container must be running and must have an ls. container_read reads a file found here.",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in containerFilesIn) (*mcp.CallToolResult, containerFilesOut, error) {
		if in.Limit < 0 {
			return nil, containerFilesOut{}, fmt.Errorf("limit is %d: it is how many entries to list at most, a number above 0, or left out for %d", in.Limit, defaultEntries)
		}
		at, err := r.inside(ctx, in.containerArg, "its files can be listed")
		if err != nil {
			return nil, containerFilesOut{}, err
		}
		dir := path.Clean("/" + strings.TrimSpace(in.Path))
		out := containerFilesOut{Env: at.Env.Name, Container: at.Container.Name, Path: dir, Entries: []fileEntry{}}

		listed, err := r.dock.GetContainersByIdFiles(ctx, at.Container.Id, dockhand.GetContainersByIdFilesOptions{Env: at.Env.ID, Path: dir})
		if err != nil {
			// a directory that is not there is answered as ls failing to run, in words about ls
			return nil, out, fmt.Errorf("%s in %s could not be listed, which is what a path that is not there or is no directory answers: %w", dir, out.Container, refusal(err))
		}
		for i := range listed.Answer.Entries {
			e := &listed.Answer.Entries[i]
			out.Entries = append(out.Entries, fileEntry{Name: e.Name, Type: string(e.Type), Size: e.Size, Mode: e.Permissions, Owner: e.Owner + ":" + e.Group, Modified: e.Modified, Target: e.LinkTarget})
		}
		slices.SortStableFunc(out.Entries, func(a, b fileEntry) int {
			return cmp.Or(cmp.Compare(b2i(a.Type != "directory"), b2i(b.Type != "directory")), cmp.Compare(a.Name, b.Name))
		})
		out.Total = len(out.Entries)
		if limit := cmp.Or(in.Limit, defaultEntries); out.Total > limit {
			out.Entries = out.Entries[:limit]
			out.Note = fmt.Sprintf("the directory has %d entries and the first %d are listed: raise limit for more", out.Total, limit)
		}

		return nil, out, nil
	})

	type containerReadIn struct {
		containerArg
		revealArg

		Path     string `json:"path"                jsonschema:"the file to read, from the container's root, as container_files lists them"`
		MaxChars int    `json:"max_chars,omitempty" jsonschema:"how much of the file to give, from its start: 20000 characters unless given, 200000 at most"`
	}
	type containerReadOut struct {
		Env       string `json:"env"                 jsonschema:"the environment (Docker host) the container is on"`
		Container string `json:"container"`
		Path      string `json:"path"`
		Content   string `json:"content"             jsonschema:"the file's text, from its start"`
		Chars     int    `json:"chars"               jsonschema:"how long the whole file is, in characters"`
		Truncated bool   `json:"truncated,omitempty" jsonschema:"the file is longer than what is given"`
		Hidden    int    `json:"hidden,omitempty"    jsonschema:"how many secrets in it are hidden"`
		Note      string `json:"note,omitempty"`
	}
	add(r, readTool, &mcp.Tool{
		Name:        "container_read",
		Description: "Read one text file inside a running container: the configuration it is really running with, a file it wrote, a script in its image. Gives its start, 20000 characters unless max_chars says otherwise, and says how long the whole is. Dockhand reads files up to 1 MB and no larger, and a file that is not text is refused rather than shown as noise. Passwords, tokens and keys are hidden where a name beside them says what they are, unless reveal_secrets asks for them.",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in containerReadIn) (*mcp.CallToolResult, containerReadOut, error) {
		if strings.TrimSpace(in.Path) == "" {
			return nil, containerReadOut{}, errors.New("path is required: the file to read, from the container's root, as container_files lists them")
		}
		if in.MaxChars < 0 || in.MaxChars > maxFileChars {
			return nil, containerReadOut{}, fmt.Errorf("max_chars is %d: it is how much of the file to give, from 1 to %d, or left out for %d", in.MaxChars, maxFileChars, defaultFileChars)
		}
		at, err := r.inside(ctx, in.containerArg, "a file can be read")
		if err != nil {
			return nil, containerReadOut{}, err
		}
		file := path.Clean("/" + strings.TrimSpace(in.Path))
		out := containerReadOut{Env: at.Env.Name, Container: at.Container.Name, Path: file}

		read, err := r.dock.GetContainersByIdFilesContent(ctx, at.Container.Id, dockhand.GetContainersByIdFilesContentOptions{Env: at.Env.ID, Path: file})
		switch status := client.StatusCode(err); {
		case status == http.StatusNotFound:
			return nil, out, fmt.Errorf("%s has no file %s: container_files lists what it has", out.Container, file)
		case status == http.StatusBadRequest:
			return nil, out, fmt.Errorf("%s in %s is a directory: container_files lists it", file, out.Container)
		case status == http.StatusRequestEntityTooLarge:
			return nil, out, fmt.Errorf("%s in %s is larger than the 1 MB Dockhand reads of a file", file, out.Container)
		case err != nil:
			return nil, out, fmt.Errorf("%s in %s could not be read: %w", file, out.Container, refusal(err))
		default:
		}

		text := read.Answer.Content
		// Dockhand hands a file over as text whatever it is: what was not text shows as bytes that are none, or as the mark of ones that could not be
		// read
		if strings.ContainsRune(text, 0) || strings.ContainsRune(text, utf8.RuneError) {
			return nil, out, fmt.Errorf("%s in %s is not a text file, so it is not shown", file, out.Container)
		}
		if !in.RevealSecrets {
			text, out.Hidden = maskText(text)
		}
		out.Chars = utf8.RuneCountInString(text)
		if limit := cmp.Or(in.MaxChars, defaultFileChars); out.Chars > limit {
			text, out.Truncated = string([]rune(text)[:limit]), true
			out.Note = fmt.Sprintf("the file is %d characters and its first %d are given: raise max_chars for more", out.Chars, limit)
		}
		out.Content = text
		if out.Hidden > 0 {
			out.Note = noted(out.Note, hiddenNote(out.Hidden, "told by the names beside them"))
		}

		return nil, out, nil
	})
}
