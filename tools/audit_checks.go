package tools

import (
	"context"
	"fmt"
	"maps"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/katbyte/dockhand-mcp/sdk/dockhand"
)

const (
	// healthOff is what a container's health check is set to when it was made with its image's one switched off
	healthOff = "NONE"
	// networkHost is the network mode, and the PID mode, of a container that shares its host's
	networkHost = "host"
	// tagLatest is the tag an image has when none is given
	tagLatest = "latest"
	// saidChars is how much of what a failing health check printed a finding gives
	saidChars = 200
	// graveFindings is how many critical and high vulnerabilities of one host audit_vulnerabilities reads to count them by image
	graveFindings = 5000
	// day is how long a day is
	day = 24 * time.Hour
)

// reaching are the capabilities that let a container act on its host rather than on itself.
var reaching = []string{"ALL", "SYS_ADMIN", "SYS_MODULE", "SYS_PTRACE", "SYS_RAWIO", "SYS_BOOT", "SYS_TIME", "NET_ADMIN", "DAC_READ_SEARCH", "BPF", "PERFMON"}

// imageID is an image named by its id alone, whole or cut short.
var imageID = regexp.MustCompile(`^(sha256:)?[0-9a-f]{12,64}$`)

// byContainerName, byStackName and byNotedName order what a host lists by name, and byKindAndName findings by what they are about and then by name.
func byContainerName(a, b dockhand.GetContainersAnswer) int {
	return strings.Compare(a.Name, b.Name)
}

func byStackName(a, b dockhand.GetStacksAnswer) int {
	return strings.Compare(a.Name, b.Name)
}

func byNotedName(a, b dockhand.GetContainersPendingUpdatesAnswerPendingUpdates) int {
	return strings.Compare(a.ContainerName, b.ContainerName)
}

func byKindAndName(a, b auditFinding) int {
	return strings.Compare(a.Kind+" "+a.Name, b.Kind+" "+b.Name)
}

// oneLine is text as one line of at most so many characters.
func oneLine(text string, most int) string {
	line := strings.Join(strings.Fields(text), " ")
	if runes := []rune(line); len(runes) > most {
		return string(runes[:most]) + "..."
	}

	return line
}

// ailing is what is broken about a container right now.
func ailing(c *surveyed, _ *survey) (bool, []flaw) {
	doc := c.Doc
	var flaws []flaw

	switch {
	case doc.State.Status == stateRestarting:
		flaws = append(flaws, flaw{"restarting", fmt.Sprintf("Docker keeps starting it again, %d times so far; it last exited with code %d", doc.RestartCount, doc.State.ExitCode)})
	case doc.State.Status == stateDead:
		flaws = append(flaws, flaw{"dead", "Docker could not remove it"})
	case doc.State.OOMKilled:
		flaws = append(flaws, flaw{"out_of_memory", "the host killed it for using more memory than it may" + ended(doc)})
	case doc.State.Error != "":
		flaws = append(flaws, flaw{"start_failed", "Docker could not start it: " + oneLine(doc.State.Error, saidChars)})
	case doc.State.Status == stateExited && strconv.Itoa(doc.State.ExitCode) == killed:
		flaws = append(flaws, flaw{"exited_with_error", "exited with code 137: it was killed, which is how a container ends when it is stopped and does not stop by itself" + ended(doc)})
	case doc.State.Status == stateExited && doc.State.ExitCode != 0:
		flaws = append(flaws, flaw{"exited_with_error", fmt.Sprintf("exited with code %d", doc.State.ExitCode) + ended(doc)})
	case doc.State.Running && doc.RestartCount > 0 && upForSeconds.MatchString(c.Container.Status):
		flaws = append(flaws, flaw{"restart_loop", fmt.Sprintf("started again by Docker %d times, the last only seconds ago", doc.RestartCount)})
	default:
	}

	if check := doc.State.Health; doc.State.Running && check != nil && check.Status == healthUnhealthy {
		detail := fmt.Sprintf("its health check has failed %d times in a row", check.FailingStreak)
		if len(check.Log) > 0 {
			if said := oneLine(check.Log[len(check.Log)-1].Output, saidChars); said != "" {
				detail += "; the latest printed: " + said
			}
		}
		flaws = append(flaws, flaw{"unhealthy", detail})
	}

	return true, flaws
}

// ended is when a container stopped, as the end of a sentence; nothing for one Docker gives no time for.
func ended(doc *inspected) string {
	if at := dockerTime(doc.State.FinishedAt); at != "" {
		return ", at " + at
	}

	return ""
}

// unchecked is a running container nothing checks the health of.
func unchecked(c *surveyed, _ *survey) (bool, []flaw) {
	doc := c.Doc
	switch {
	case !doc.State.Running:
		return false, nil
	case doc.Config.Healthcheck != nil && len(doc.Config.Healthcheck.Test) > 0 && doc.Config.Healthcheck.Test[0] == healthOff:
		return true, []flaw{{"health_check_off", "it was made with its health check switched off, so Docker calls it running for as long as its process lives"}}
	case doc.State.Health == nil:
		return true, []flaw{{"no_health_check", "nothing checks it, so Docker calls it running for as long as its process lives, whether or not it still answers"}}
	default:
		return true, nil
	}
}

// unrestarted is a running container Docker would not start again.
func unrestarted(c *surveyed, _ *survey) (bool, []flaw) {
	doc := c.Doc
	if !doc.State.Running {
		return false, nil
	}
	if policy := doc.HostConfig.RestartPolicy.Name; policy == "" || policy == "no" {
		return true, []flaw{{"no_restart_policy", "nothing starts it again when it exits or when the host reboots"}}
	}

	return true, nil
}

// untagged is a container whose image does not say which version it is.
func untagged(c *surveyed, _ *survey) (bool, []flaw) {
	image := c.Doc.Config.Image
	if imageID.MatchString(image) {
		return true, []flaw{{"image_without_name", "it was made from an image by its id, so nothing says what it runs and no update check can follow it"}}
	}
	// an image pinned by its digest is exactly one image, whatever tag stands beside it
	if strings.Contains(image, "@sha256:") {
		return true, nil
	}

	// the tag is what follows a colon in the last part of the name: a colon before that is a registry's port
	last := image[strings.LastIndex(image, "/")+1:]
	_, tag, tagged := strings.Cut(last, ":")
	switch {
	case !tagged:
		return true, []flaw{{"no_tag", "its image is named without a tag, which is latest: what it runs changes whenever the image is pulled again"}}
	case tag == tagLatest:
		return true, []flaw{{"latest_tag", "it follows latest: what it runs changes whenever the image is pulled again, and nothing records which version that was"}}
	default:
		return true, nil
	}
}

// exposed is a container given more of its host than a container needs.
func exposed(c *surveyed, _ *survey) (bool, []flaw) {
	doc := c.Doc
	var flaws []flaw

	if doc.HostConfig.Privileged {
		flaws = append(flaws, flaw{"privileged", "it runs with every capability of the host, which is the host's root"})
	}
	if doc.HostConfig.NetworkMode == networkHost {
		flaws = append(flaws, flaw{"host_network", "it is on the host's own network: every port it opens is open on the host"})
	}
	if doc.HostConfig.PidMode == networkHost {
		flaws = append(flaws, flaw{"host_pid", "it sees every process of the host and can signal them"})
	}

	for _, m := range doc.Mounts {
		switch {
		case strings.HasSuffix(m.Source, "docker.sock") && c.Container.SystemContainer != "":
			// Dockhand and its agent work through the socket: it is what they are for
		case strings.HasSuffix(m.Source, "docker.sock") && m.RW:
			flaws = append(flaws, flaw{"docker_socket", "the Docker socket is mounted in it, which is control of every container on the host and of the host itself"})
		case strings.HasSuffix(m.Source, "docker.sock"):
			flaws = append(flaws, flaw{"docker_socket", "the Docker socket is mounted in it read-only, which still lets it give Docker any order: read-only is about the file, not what is sent through it"})
		case m.Type == "bind" && m.Source == "/" && m.RW:
			flaws = append(flaws, flaw{"host_root", "the whole of the host's disk is mounted in it at " + m.Destination + ", and it may write to it"})
		case m.Type == "bind" && m.Source == "/":
			flaws = append(flaws, flaw{"host_root", "the whole of the host's disk is mounted in it at " + m.Destination + ", to read"})
		default:
		}
	}

	var added []string
	for _, capability := range doc.HostConfig.CapAdd {
		if name := strings.TrimPrefix(strings.ToUpper(capability), "CAP_"); slices.Contains(reaching, name) {
			added = append(added, name)
		}
	}
	if len(added) > 0 {
		slices.Sort(added)
		flaws = append(flaws, flaw{"added_capabilities", "it was given capabilities that reach outside it: " + strings.Join(added, ", ")})
	}

	return true, flaws
}

// unlimited is a running container that may use all of its host's memory.
func unlimited(c *surveyed, _ *survey) (bool, []flaw) {
	doc := c.Doc
	if !doc.State.Running {
		return false, nil
	}
	if doc.HostConfig.Memory == 0 {
		return true, []flaw{{"no_memory_limit", "it may use all of the host's memory"}}
	}

	return true, nil
}

// leftOver is a container that has not run for longer than the audit was asked about.
func leftOver(c *surveyed, s *survey) (bool, []flaw) {
	doc := c.Doc
	if doc.State.Running || doc.State.Status == stateRestarting || doc.State.Status == "paused" {
		return false, nil
	}

	stopped, err := time.Parse(time.RFC3339Nano, doc.State.FinishedAt)
	if err == nil && stopped.Year() > 1 {
		if age := s.now.Sub(stopped); age > time.Duration(s.days)*day {
			return true, []flaw{{"stopped_long", fmt.Sprintf("it stopped %d days ago, at %s, with exit code %d", int(age/day), dockerTime(doc.State.FinishedAt), doc.State.ExitCode)}}
		}

		return true, nil
	}

	// one that never ran has no time it stopped at, only the time it was made
	made, err := time.Parse(time.RFC3339Nano, doc.Created)
	if err != nil {
		return true, nil
	}
	if age := s.now.Sub(made); age > time.Duration(s.days)*day {
		return true, []flaw{{"never_started", fmt.Sprintf("it was made %d days ago, at %s, and has never run", int(age/day), dockerTime(doc.Created))}}
	}

	return true, nil
}

// auditHosts finds the environments the Dockhand cannot reach.
func (r *registry) auditHosts(ctx context.Context, s *survey) auditOut {
	out := auditOut{Findings: []auditFinding{}, ByCheck: map[string]int{}}

	stats, err := r.dock.GetDashboardStats(ctx)
	if err != nil {
		for _, env := range s.envs {
			out.Failed = append(out.Failed, envFailure{Env: env.Name, Error: "whether it is online could not be read: " + refusal(err).Error()})
		}

		return out
	}

	online := map[dockhand.EnvironmentID]bool{}
	for i := range stats.Answer {
		online[stats.Answer[i].Id] = stats.Answer[i].Online
	}
	for _, env := range s.envs {
		reached, said := online[env.ID]
		if !said {
			out.Failed = append(out.Failed, envFailure{Env: env.Name, Error: "Dockhand did not say whether it is online"})

			continue
		}
		out.Checked++
		if !reached {
			out.found(auditFinding{Env: env.Name, Kind: kindHost, Name: env.Name, Check: "offline", Detail: "Dockhand cannot reach it, so nothing on it is seen by any audit"})
		}
	}

	return out
}

// auditStacks finds the compose stacks that are not wholly up.
func (r *registry) auditStacks(ctx context.Context, s *survey) auditOut {
	out := auditOut{Findings: []auditFinding{}, ByCheck: map[string]int{}}
	for _, host := range ask(ctx, s.envs, r.stacksOf) {
		if host.Err != nil {
			out.Failed = append(out.Failed, envFailure{Env: host.Env.Name, Error: host.Err.Error()})

			continue
		}
		slices.SortFunc(host.Value, byStackName)

		for i := range host.Value {
			stack := &host.Value[i]
			if s.stack != "" && !strings.EqualFold(stack.Name, s.stack) {
				continue
			}
			out.Checked++
			if stack.Status == dockhand.StackStatusRunning {
				continue
			}

			var down []string
			for _, c := range stack.ContainerDetails {
				if c.State != stateRunning {
					down = append(down, c.Name+" ("+string(c.State)+")")
				}
			}
			slices.Sort(down)

			if stack.Status == dockhand.StackStatusPartial {
				out.found(auditFinding{Env: host.Env.Name, Kind: kindStack, Name: stack.Name, Check: "partly_running", Detail: fmt.Sprintf("%d of its %d containers are not running: %s", len(down), len(stack.ContainerDetails), strings.Join(down, ", "))})

				continue
			}
			out.found(auditFinding{Env: host.Env.Name, Kind: kindStack, Name: stack.Name, Check: "not_running", Detail: fmt.Sprintf("none of its %d containers is running: Dockhand calls it %s", len(stack.ContainerDetails), stack.Status)})
		}
	}

	return out
}

// auditUpdates finds the containers Dockhand has noted something newer for.
func (r *registry) auditUpdates(ctx context.Context, s *survey) auditOut {
	out := auditOut{Findings: []auditFinding{}, ByCheck: map[string]int{}}
	stale := 0
	for _, host := range ask(ctx, s.envs, func(ctx context.Context, env environment) (hostUpdates, error) {
		found := hostUpdates{Has: map[string]dockhand.GetContainersAnswer{}}
		noted, err := r.dock.GetContainersPendingUpdates(ctx, dockhand.GetContainersPendingUpdatesOptions{Env: env.ID})
		if err != nil {
			return found, refusal(err)
		}
		found.Noted = noted.Answer.PendingUpdates

		containers, err := r.containersOf(ctx, env)
		if err != nil {
			return found, err
		}
		for i := range containers {
			found.Has[containers[i].Id] = containers[i]
		}

		return found, nil
	}) {
		if host.Err != nil {
			out.Failed = append(out.Failed, envFailure{Env: host.Env.Name, Error: host.Err.Error()})

			continue
		}
		for id := range host.Value.Has {
			if s.wanted(host.Value.Has[id].Labels) {
				out.Checked++
			}
		}

		slices.SortFunc(host.Value.Noted, byNotedName)
		for i := range host.Value.Noted {
			n := &host.Value.Noted[i]
			c, there := host.Value.Has[n.ContainerId]
			if !there {
				stale++

				continue
			}
			if !s.wanted(c.Labels) {
				continue
			}

			finding := auditFinding{Env: host.Env.Name, Kind: kindContainer, Name: n.ContainerName, Stack: c.Labels[labelStack], Image: n.CurrentImage}
			if newer := n.NewerVersion; newer != nil && newer.Tag != "" {
				finding.Check, finding.Detail = "newer_version", fmt.Sprintf("version %s is out, a %s step from the one it runs", newer.Tag, newer.Bump)
				out.found(finding)
			}
			if n.HasImageUpdate {
				finding.Check, finding.Detail = "newer_image", "the registry has another image under the name and tag it runs"
				out.found(finding)
			}
		}
	}
	if stale > 0 {
		out.Note = fmt.Sprintf("%d notes of an update are for containers that are gone, and can be cleared", stale)
	}

	return out
}

// graveImage is how many grave vulnerabilities one image has.
type graveImage struct {
	Critical, High, Fixable int
	Containers              []string
}

// auditVulnerabilities finds the images the scans found something grave in, and the hosts with images no scan has looked at.
func (r *registry) auditVulnerabilities(ctx context.Context, s *survey) auditOut {
	out := auditOut{Findings: []auditFinding{}, ByCheck: map[string]int{}}
	short := 0
	for _, host := range ask(ctx, s.envs, func(ctx context.Context, env environment) (*dockhand.GetVulnerabilitiesAnswer, error) {
		listed, err := r.dock.GetVulnerabilities(ctx, dockhand.GetVulnerabilitiesOptions{Env: env.ID, Severity: string(dockhand.SeverityCritical) + "," + string(dockhand.SeverityHigh), Sort: "severity", Limit: new(graveFindings)})
		if err != nil {
			return nil, refusal(err)
		}

		return listed.Answer, nil
	}) {
		if host.Err != nil {
			out.Failed = append(out.Failed, envFailure{Env: host.Env.Name, Error: host.Err.Error()})

			continue
		}

		if summary := host.Value.Summary; summary != nil {
			out.Checked += summary.TotalImages
			if unscanned := summary.TotalImages - summary.ImagesScanned; unscanned > 0 {
				out.found(auditFinding{Env: host.Env.Name, Kind: kindHost, Name: host.Env.Name, Check: "images_not_scanned", Detail: fmt.Sprintf("%d of its %d images have never been scanned, so nothing is known of them until the host is scanned", unscanned, summary.TotalImages)})
			}
		}
		if host.Value.Total > len(host.Value.Findings) {
			short += host.Value.Total - len(host.Value.Findings)
		}

		images := map[string]*graveImage{}
		for i := range host.Value.Findings {
			f := &host.Value.Findings[i]
			// only the grave ones were asked for; anything else answered is not counted as one
			if f.Severity != dockhand.SeverityCritical && f.Severity != dockhand.SeverityHigh {
				continue
			}

			image, known := images[f.ImageName]
			if !known {
				image = &graveImage{}
				images[f.ImageName] = image
			}
			if f.Severity == dockhand.SeverityCritical {
				image.Critical++
			} else {
				image.High++
			}
			if f.FixedVersion != "" {
				image.Fixable++
			}
			for _, c := range f.Containers {
				if !slices.Contains(image.Containers, c.Name) {
					image.Containers = append(image.Containers, c.Name)
				}
			}
		}

		for _, name := range slices.Sorted(maps.Keys(images)) {
			image := images[name]
			slices.Sort(image.Containers)

			finding := auditFinding{Env: host.Env.Name, Kind: kindImage, Name: name, Check: "high_vulnerabilities", Detail: fmt.Sprintf("%d critical and %d high, %d of them put right by a newer version of the package", image.Critical, image.High, image.Fixable)}
			if image.Critical > 0 {
				finding.Check = "critical_vulnerabilities"
			}
			if len(image.Containers) > 0 {
				finding.Detail += "; used by " + strings.Join(image.Containers, ", ")
			} else {
				finding.Detail += "; no container uses it"
			}
			out.found(finding)
		}
	}
	if short > 0 {
		out.Note = fmt.Sprintf("%d grave vulnerabilities were more than one read takes and are in no image's count", short)
	}

	return out
}

// hostHolds is what one environment holds besides its containers.
type hostHolds struct {
	Images   []dockhand.GetImagesAnswer
	Volumes  []dockhand.GetVolumesAnswer
	Networks hostNetworks
}

// auditUnused finds the images, volumes and networks nothing uses.
func (r *registry) auditUnused(ctx context.Context, s *survey) auditOut {
	out := auditOut{Findings: []auditFinding{}, ByCheck: map[string]int{}}
	bytes := 0
	for _, host := range ask(ctx, s.envs, func(ctx context.Context, env environment) (hostHolds, error) {
		var holds hostHolds
		var err error
		if holds.Images, err = r.imagesOf(ctx, env); err != nil {
			return holds, fmt.Errorf("its images could not be listed: %w", err)
		}
		if holds.Volumes, err = r.volumesOf(ctx, env); err != nil {
			return holds, fmt.Errorf("its volumes could not be listed: %w", err)
		}
		if holds.Networks, err = r.networksOf(ctx, env); err != nil {
			return holds, fmt.Errorf("its networks could not be listed: %w", err)
		}

		return holds, nil
	}) {
		if host.Err != nil {
			out.Failed = append(out.Failed, envFailure{Env: host.Env.Name, Error: host.Err.Error()})

			continue
		}
		holds := host.Value
		out.Checked += len(holds.Images) + len(holds.Volumes) + len(holds.Networks.Networks)

		unused := []auditFinding{}
		for i := range holds.Images {
			row := imageRowOf(host.Env.Name, &holds.Images[i])
			if row.Containers > 0 {
				continue
			}
			name := row.ID
			if len(row.Tags) > 0 {
				name = row.Tags[0]
			}
			bytes += holds.Images[i].Size
			unused = append(unused, auditFinding{Env: host.Env.Name, Kind: kindImage, Name: name, Check: "unused_image", Detail: fmt.Sprintf("no container on the host was made from it; it is %.0f MB, some of which other images may share", row.SizeMB)})
		}
		for i := range holds.Volumes {
			if v := &holds.Volumes[i]; len(v.UsedBy) == 0 {
				unused = append(unused, auditFinding{Env: host.Env.Name, Kind: kindVolume, Name: v.Name, Stack: v.Labels[labelStack], Check: "unused_volume", Detail: "no container mounts it, running or stopped; it may still hold data someone wants"})
			}
		}
		for i := range holds.Networks.Networks {
			n := &holds.Networks.Networks[i]
			if len(holds.Networks.Members[n.Name]) == 0 && !slices.Contains(builtinNetworks, n.Name) {
				unused = append(unused, auditFinding{Env: host.Env.Name, Kind: kindNetwork, Name: n.Name, Check: "unused_network", Detail: "no container is on it, running or stopped"})
			}
		}

		slices.SortStableFunc(unused, byKindAndName)
		for _, finding := range unused {
			out.found(finding)
		}
	}
	if bytes > 0 {
		out.Note = fmt.Sprintf("the unused images add up to %.0f MB, the most removing them could give back", megabytes(bytes))
	}

	return out
}
