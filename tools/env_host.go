package tools

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"math"
	"net"
	"slices"
	"strconv"
	"strings"

	"github.com/katbyte/dockhand-mcp/sdk/dockhand"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// What an image prune on a schedule takes: images with no name left, or every image no container uses.
var pruneModes = []string{"dangling", "all"}

// What a disk warning goes by: how full the disk Docker keeps its data on is, or how much Docker's data comes to.
var warningModes = []string{"percentage", "absolute"}

// word reads a text out of an object the API sends with no declared shape; one that is not there is empty.
func word(from map[string]any, key string) string {
	text, ok := from[key].(string)
	if !ok {
		return ""
	}

	return text
}

// hostMachine is the machine behind an environment and the Docker on it.
type hostMachine struct {
	Hostname      string  `json:"hostname"                 jsonschema:"what the machine calls itself"`
	Address       string  `json:"address,omitempty"        jsonschema:"where Dockhand reaches it"`
	OS            string  `json:"os"`
	Arch          string  `json:"arch"`
	Kernel        string  `json:"kernel,omitempty"`
	CPUs          int     `json:"cpus"`
	MemoryMB      float64 `json:"memory_mb"`
	UptimeHours   float64 `json:"uptime_hours,omitempty"   jsonschema:"how long the machine has been up; absent for a host reached at an address with no agent, which does not say"`
	DockerVersion string  `json:"docker_version"`
	StorageDriver string  `json:"storage_driver,omitempty" jsonschema:"how Docker keeps images and containers on disk"`
	Containers    int     `json:"containers"`
	Running       int     `json:"running"                  jsonschema:"how many of the containers are running"`
	Images        int     `json:"images"`
	Volumes       int     `json:"volumes"`
	Networks      int     `json:"networks"`
	Stacks        int     `json:"stacks"`
}

// updateCheck is when Dockhand asks the registries whether a host's containers are behind, and what it does about one that is.
type updateCheck struct {
	Enabled    bool   `json:"enabled"`
	Cron       string `json:"cron"        jsonschema:"when, as five cron fields"`
	AutoUpdate bool   `json:"auto_update" jsonschema:"whether a container found behind is updated then and there, with nobody asked, or only noted"`
	HoldBack   string `json:"hold_back"   jsonschema:"when a scan of the newer image holds such an update back: never, any, critical_high, critical or more_than_current"`
}

// pruneSchedule is when Dockhand prunes a host's images by itself.
type pruneSchedule struct {
	Enabled bool   `json:"enabled"`
	Cron    string `json:"cron"    jsonschema:"when, as five cron fields"`
	Mode    string `json:"mode"    jsonschema:"dangling, for images with no name left, or all, for every image no container uses"`
}

// diskWarning is when Dockhand warns that a host's disk is filling.
type diskWarning struct {
	Enabled bool   `json:"enabled"`
	Mode    string `json:"mode"    jsonschema:"percentage, by how full the disk is, or absolute, by how much Docker's data comes to"`
	Percent int    `json:"percent" jsonschema:"for percentage: how full, in percent, before it warns"`
	GB      int    `json:"gb"      jsonschema:"for absolute: how many gigabytes before it warns"`
}

// hostSettings is what Dockhand keeps for an environment beyond how to reach it. Each is read on its own, and one that cannot be read is left out.
type hostSettings struct {
	Timezone        string         `json:"timezone,omitempty"          jsonschema:"the timezone its schedules run in"`
	UpdateCheck     *updateCheck   `json:"update_check,omitempty"`
	ImagePrune      *pruneSchedule `json:"prune_images,omitempty"`
	DiskWarning     *diskWarning   `json:"disk_warning,omitempty"`
	RemoteStacksDir string         `json:"remote_stacks_dir,omitempty" jsonschema:"for a host reached at an address with no agent: the folder on the host that stack files are put in before a deploy"`
}

// settingsOf is the settings Dockhand keeps for an environment, and what there is to say of the ones that could not be read.
func (r *registry) settingsOf(ctx context.Context, env environment) (kept hostSettings, unreadable string) {
	var unread []string
	if zone, err := r.dock.GetEnvironmentsByIdTimezone(ctx, env.ID); err != nil {
		unread = append(unread, "its timezone")
	} else {
		kept.Timezone = zone.Answer.Timezone
	}
	if check, err := r.dock.GetEnvironmentsByIdUpdateCheck(ctx, env.ID); err != nil || check.Answer.Settings == nil {
		unread = append(unread, "its update check")
	} else {
		kept.UpdateCheck = &updateCheck{Enabled: check.Answer.Settings.Enabled, Cron: check.Answer.Settings.Cron, AutoUpdate: check.Answer.Settings.AutoUpdate, HoldBack: check.Answer.Settings.VulnerabilityCriteria}
	}
	if prune, err := r.dock.GetEnvironmentsByIdImagePrune(ctx, env.ID); err != nil || prune.Answer.Settings == nil {
		unread = append(unread, "its image prune")
	} else {
		kept.ImagePrune = &pruneSchedule{Enabled: prune.Answer.Settings.Enabled, Cron: prune.Answer.Settings.CronExpression, Mode: prune.Answer.Settings.PruneMode}
	}
	if warning, err := r.dock.GetEnvironmentsByIdDiskWarning(ctx, env.ID); err != nil {
		unread = append(unread, "its disk warning")
	} else {
		kept.DiskWarning = &diskWarning{Enabled: warning.Answer.Enabled, Mode: string(warning.Answer.Mode), Percent: warning.Answer.Threshold, GB: warning.Answer.ThresholdGb}
	}
	if dir, err := r.dock.GetEnvironmentsByIdRemoteStacksDir(ctx, env.ID); err != nil {
		unread = append(unread, "its remote stacks folder")
	} else {
		kept.RemoteStacksDir = dir.Answer.RemoteStacksDir
	}
	if len(unread) > 0 {
		return kept, strings.Join(unread, ", ") + " could not be read"
	}

	return kept, ""
}

//nolint:gocyclo,maintidx // four tools registered in one function, each a handler of its own: the count is theirs added up
func registerEnvironmentHostTools(r *registry) {
	type envGetOut struct {
		Env             string   `json:"env"                       jsonschema:"the environment's name"`
		ID              int      `json:"id"                        jsonschema:"the environment's number, which Dockhand's own API takes"`
		Connection      string   `json:"connection"                jsonschema:"how Dockhand reaches the host: socket, direct, hawser-standard or hawser-edge"`
		Address         string   `json:"address,omitempty"         jsonschema:"where the host, or the agent in front of it, is reached; absent for a socket"`
		SocketPath      string   `json:"socket_path,omitempty"     jsonschema:"the Docker socket's path, on the machine Dockhand runs on for a socket and on the host for an agent"`
		Labels          []string `json:"labels"                    jsonschema:"the labels Dockhand groups its environments by"`
		PublicIP        string   `json:"public_ip,omitempty"       jsonschema:"the address the host's published ports are reached at from outside, which Dockhand uses to make links"`
		CollectActivity bool     `json:"collect_activity"          jsonschema:"whether Dockhand keeps what the host's containers do: starts, stops, deaths"`
		CollectMetrics  bool     `json:"collect_metrics"           jsonschema:"whether Dockhand keeps the host's processor and memory use over time"`
		Agent           string   `json:"agent,omitempty"           jsonschema:"the name of the Hawser agent in front of the host, when there is one and it has been heard from"`
		AgentVersion    string   `json:"agent_version,omitempty"`
		AgentLastSeen   string   `json:"agent_last_seen,omitempty" jsonschema:"when an agent that calls Dockhand last did"`
		HasAgentToken   bool     `json:"has_agent_token,omitempty" jsonschema:"Dockhand keeps a token for the agent, which it never gives back"`
		Created         string   `json:"created,omitempty"`

		Machine *hostMachine `json:"machine,omitempty" jsonschema:"the machine and the Docker on it, as they answer now; absent when the host could not be reached"`

		hostSettings

		Note string `json:"note,omitempty"`
	}
	add(r, readTool, &mcp.Tool{
		Name:        "env_get",
		Description: "Describe one Docker host (environment) in full: how Dockhand reaches it and the agent in front of it, the machine itself as it answers now, its name, system, processors, memory, how long it has been up and its Docker's version and storage driver, what it holds by kind, and what Dockhand does for it by itself: the timezone its schedules run in, when it checks the host's containers for newer images and whether it then updates them unasked, when it prunes the host's images, and when it warns that the disk is filling. A host that cannot be reached is still described by what Dockhand keeps for it.",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in envArg) (*mcp.CallToolResult, envGetOut, error) {
		env, err := r.one(ctx, in)
		if err != nil {
			return nil, envGetOut{}, err
		}
		kept, err := r.dock.GetEnvironmentsById(ctx, env.ID)
		if err != nil {
			return nil, envGetOut{}, fmt.Errorf("%s could not be read: %w", env.Name, refusal(err))
		}
		e := kept.Answer
		out := envGetOut{Env: e.Name, ID: int(e.Id), Connection: string(e.ConnectionType), SocketPath: e.SocketPath, Labels: orNone(e.Labels), PublicIP: e.PublicIp, CollectActivity: e.CollectActivity, CollectMetrics: e.CollectMetrics, Agent: e.HawserAgentName, AgentVersion: e.HawserVersion, AgentLastSeen: when(e.HawserLastSeen), HasAgentToken: e.HasHawserToken, Created: when(e.CreatedAt)}
		if e.ConnectionType != connectionSocket && e.Host != "" {
			out.Address = e.Host
			if e.Port != 0 {
				out.Address = net.JoinHostPort(e.Host, strconv.Itoa(e.Port))
			}
		}

		// the machine answers for itself, and one that does not is said not to
		host, err := r.dock.GetHost(ctx, dockhand.GetHostOptions{Env: env.ID})
		if err != nil {
			out.Note = "the host did not answer, so nothing is said of the machine: " + refusal(err).Error()
		} else {
			h := host.Answer
			out.Machine = &hostMachine{Hostname: h.Hostname, Address: h.IpAddress, OS: h.Platform, Arch: h.Arch, CPUs: h.Cpus, MemoryMB: megabytes(h.TotalMemory), UptimeHours: math.Round(h.Uptime/360) / 10, DockerVersion: h.DockerVersion, Containers: h.DockerContainers, Running: h.DockerContainersRunning, Images: h.DockerImages}
			out.AgentVersion = cmp.Or(out.AgentVersion, word(h.Environment, "hawserVersion"))
			// what Docker says of itself beyond that is another call, and the description stands without it
			if system, failed := r.dock.GetSystem(ctx, dockhand.GetSystemOptions{Env: env.ID}); failed == nil {
				out.Machine.Kernel, out.Machine.StorageDriver = word(system.Answer.Docker, "kernelVersion"), word(system.Answer.Host, "storageDriver")
				out.Machine.Volumes, out.Machine.Networks, out.Machine.Stacks = count(system.Answer.Stats, "volumes"), count(system.Answer.Stats, "networks"), count(system.Answer.Stats, "stacks")
			}
		}

		var unread string
		out.hostSettings, unread = r.settingsOf(ctx, env)
		out.Note = noted(out.Note, unread)

		return nil, out, nil
	})

	type envEditIn struct {
		envArg
		dryRunArg

		Name            string   `json:"name,omitempty"             jsonschema:"another name for the environment; Dockhand renames the folder it keeps the environment's stacks in to match"`
		Labels          []string `json:"labels,omitempty"           jsonschema:"every label it is to have, in place of the ones it has; an empty list for none"`
		PublicIP        *string  `json:"public_ip,omitempty"        jsonschema:"the address the host's published ports are reached at from outside; empty to forget it"`
		CollectActivity *bool    `json:"collect_activity,omitempty" jsonschema:"whether Dockhand keeps what the host's containers do"`
		CollectMetrics  *bool    `json:"collect_metrics,omitempty"  jsonschema:"whether Dockhand keeps the host's processor and memory use over time"`
		Host            string   `json:"host,omitempty"             jsonschema:"for a host reached at an address or through an agent: the name or address Dockhand is to reach it at from now on"`
		Port            int      `json:"port,omitempty"             jsonschema:"the port to reach it on from now on"`
		Protocol        string   `json:"protocol,omitempty"         jsonschema:"http or https"`
		SocketPath      string   `json:"socket_path,omitempty"      jsonschema:"for a socket: the socket's path as Dockhand sees it"`
		AgentToken      string   `json:"agent_token,omitempty"      jsonschema:"for a Hawser agent: the token the agent is started with now. It is sent to Dockhand and kept there, and is never given back"`
	}
	type envEditOut struct {
		Env       string   `json:"env"                 jsonschema:"the environment's name, read back: the new one once it was renamed"`
		ID        int      `json:"id"`
		Changes   []string `json:"changes"             jsonschema:"what was changed, in words; a token is said to have changed and never shown"`
		Reachable *bool    `json:"reachable,omitempty" jsonschema:"whether Dockhand reaches the host the new way, tried after how it is reached was changed"`
		DryRun    bool     `json:"dry_run,omitempty"   jsonschema:"nothing was done: changes says what would be"`
		Note      string   `json:"note,omitempty"`
	}
	add(r, writeTool, &mcp.Tool{
		Name:        "env_edit",
		Description: "Change what Dockhand keeps for one Docker host (environment): its name, its labels, the address its ports are reached at from outside, whether its containers' activity and its metrics are collected, and how the host is reached, its address, port, protocol, socket or agent token. What is not given is kept. Nothing on the host is touched. After a change to how the host is reached Dockhand is made to try it, and the answer says whether it got through: a host that can no longer be reached is one whose containers can no longer be seen or managed until it is put right. A name another environment has is refused.",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in envEditIn) (*mcp.CallToolResult, envEditOut, error) {
		name, protocol := strings.TrimSpace(in.Name), strings.ToLower(strings.TrimSpace(in.Protocol))
		switch {
		case protocol != "" && protocol != "http" && protocol != "https":
			return nil, envEditOut{}, fmt.Errorf("protocol is %q: it is http or https", in.Protocol)
		case in.Port < 0 || in.Port > 65535:
			return nil, envEditOut{}, fmt.Errorf("port is %d: a port is a number from 1 to 65535", in.Port)
		default:
		}
		env, err := r.one(ctx, in.envArg)
		if err != nil {
			return nil, envEditOut{}, err
		}
		out := envEditOut{Env: env.Name, ID: int(env.ID), Changes: []string{}, DryRun: in.DryRun}
		kept, err := r.dock.GetEnvironmentsById(ctx, env.ID)
		if err != nil {
			return nil, out, fmt.Errorf("%s could not be read, so nothing was changed: %w", env.Name, refusal(err))
		}
		was := kept.Answer

		request := dockhand.PutEnvironmentsByIdRequest{}
		reached := false
		if name != "" && name != was.Name {
			all, failed := r.environments(ctx)
			if failed != nil {
				return nil, out, fmt.Errorf("the environments could not be listed, so nothing was changed: %w", failed)
			}
			if slices.ContainsFunc(all, func(other environment) bool { return other.ID != env.ID && strings.EqualFold(other.Name, name) }) {
				return nil, out, fmt.Errorf("another environment is named %s already", name)
			}
			request.Name = name
			out.Changes = append(out.Changes, "its name, which was "+was.Name+", is "+name)
		}
		if in.Labels != nil && !slices.Equal(in.Labels, was.Labels) {
			request.Labels = in.Labels
			out.Changes = append(out.Changes, fmt.Sprintf("its labels, which were %v, are %v", orNone(was.Labels), in.Labels))
		}
		// an address forgotten is a call of its own: the one that saves the rest leaves an empty text out, and Dockhand keeps what it is not sent
		forget := false
		if in.PublicIP != nil && strings.TrimSpace(*in.PublicIP) != was.PublicIp {
			request.PublicIp = strings.TrimSpace(*in.PublicIP)
			forget = request.PublicIp == ""
			out.Changes = append(out.Changes, fmt.Sprintf("its public address, which was %q, is %q", was.PublicIp, request.PublicIp))
		}
		if in.CollectActivity != nil && *in.CollectActivity != was.CollectActivity {
			request.CollectActivity = in.CollectActivity
			out.Changes = append(out.Changes, fmt.Sprintf("its containers' activity is collected: %t", *in.CollectActivity))
		}
		if in.CollectMetrics != nil && *in.CollectMetrics != was.CollectMetrics {
			request.CollectMetrics = in.CollectMetrics
			out.Changes = append(out.Changes, fmt.Sprintf("its metrics are collected: %t", *in.CollectMetrics))
		}
		if host := strings.TrimSpace(in.Host); host != "" && host != was.Host {
			request.Host, reached = host, true
			out.Changes = append(out.Changes, "it is reached at "+host+", where it was reached at "+was.Host)
		}
		if in.Port != 0 && in.Port != was.Port {
			request.Port, reached = in.Port, true
			out.Changes = append(out.Changes, fmt.Sprintf("it is reached on port %d, where it was %d", in.Port, was.Port))
		}
		if protocol != "" && protocol != string(was.Protocol) {
			request.Protocol, reached = dockhand.EnvironmentProtocol(protocol), true
			out.Changes = append(out.Changes, "it is reached over "+protocol+", where it was "+string(was.Protocol))
		}
		if socket := strings.TrimSpace(in.SocketPath); socket != "" && socket != was.SocketPath {
			request.SocketPath, reached = socket, true
			out.Changes = append(out.Changes, "its socket is "+socket+", where it was "+was.SocketPath)
		}
		if in.AgentToken != "" {
			request.HawserToken, reached = in.AgentToken, true
			out.Changes = append(out.Changes, "its agent token is the one given")
		}

		switch {
		case len(out.Changes) == 0 && name == "" && in.Labels == nil && in.PublicIP == nil && in.CollectActivity == nil && in.CollectMetrics == nil && in.Host == "" && in.Port == 0 && protocol == "" && in.SocketPath == "":
			return nil, out, errors.New("nothing to change was given: name, labels, public_ip, collect_activity, collect_metrics, host, port, protocol, socket_path or agent_token")
		case len(out.Changes) == 0:
			out.Note = "nothing was done: it is so already"

			return nil, out, nil
		case in.DryRun:
			return nil, out, nil
		default:
		}

		saved, err := r.dock.PutEnvironmentsById(ctx, env.ID, request)
		if err != nil {
			out.Changes = []string{}

			return nil, out, fmt.Errorf("%s was not changed: %w", env.Name, refusal(err))
		}
		out.Env = saved.Answer.Name
		if forget {
			if _, err := r.dock.ForgetEnvironmentPublicIP(ctx, int(env.ID)); err != nil {
				return nil, out, fmt.Errorf("%s was changed, and its public address was not forgotten: %w", env.Name, refusal(err))
			}
		}

		if reached {
			tried, failed := r.dock.PostEnvironmentsByIdTest(ctx, env.ID)
			switch {
			case failed != nil:
				out.Note = "whether Dockhand reaches the host the new way could not be tried: " + refusal(failed).Error()
			case !tried.Answer.Success:
				out.Reachable = new(false)
				out.Note = "Dockhand does not reach the host the new way: " + cmp.Or(tried.Answer.Error, "it gives no reason") + ". Its containers cannot be seen or managed until how it is reached is put right"
			default:
				out.Reachable = new(true)
			}
		}

		return nil, out, nil
	})

	type envSchedulesIn struct {
		envArg
		dryRunArg

		Timezone           string  `json:"timezone,omitempty"             jsonschema:"the timezone the environment's schedules run in, by its name, as Europe/Berlin, America/Vancouver or UTC"`
		UpdateCheck        *bool   `json:"update_check,omitempty"         jsonschema:"whether Dockhand asks the registries on a schedule whether the host's containers are behind"`
		UpdateCheckCron    string  `json:"update_check_cron,omitempty"    jsonschema:"when it asks, as five cron fields: \"0 4 * * *\" for four every morning"`
		AutoUpdate         *bool   `json:"auto_update,omitempty"          jsonschema:"whether a container the check finds behind is updated then and there, pulled and made again with nobody asked, or only noted"`
		UpdateHoldBack     string  `json:"update_hold_back,omitempty"     jsonschema:"when a vulnerability scan of the newer image holds such an update back: never, any, critical_high, critical or more_than_current"`
		ImagePrune         *bool   `json:"prune_images,omitempty"         jsonschema:"whether Dockhand prunes the host's images on a schedule"`
		ImagePruneCron     string  `json:"prune_images_cron,omitempty"    jsonschema:"when it prunes, as five cron fields"`
		ImagePruneMode     string  `json:"prune_images_mode,omitempty"    jsonschema:"what it prunes: dangling, the images with no name left, or all, every image no container uses"`
		DiskWarning        *bool   `json:"disk_warning,omitempty"         jsonschema:"whether Dockhand warns when the host's disk is filling"`
		DiskWarningMode    string  `json:"disk_warning_mode,omitempty"    jsonschema:"what it goes by: percentage, how full the disk is, or absolute, how much Docker's data comes to"`
		DiskWarningPercent int     `json:"disk_warning_percent,omitempty" jsonschema:"for percentage: how full, from 1 to 100, before it warns"`
		DiskWarningGB      int     `json:"disk_warning_gb,omitempty"      jsonschema:"for absolute: how many gigabytes before it warns"`
		RemoteStacksDir    *string `json:"remote_stacks_dir,omitempty"    jsonschema:"for a host reached at an address with no agent: the folder on the host to put stack files in before a deploy; empty to have none"`
	}
	type envSchedulesOut struct {
		Env string `json:"env" jsonschema:"the environment whose settings these are"`

		hostSettings

		Changes []string `json:"changes"           jsonschema:"what was changed, in words"`
		DryRun  bool     `json:"dry_run,omitempty" jsonschema:"nothing was done: changes says what would be, and the settings are as they stand"`
		Note    string   `json:"note,omitempty"`
	}
	add(r, writeTool, &mcp.Tool{
		Name:        "env_schedules",
		Description: "Set what Dockhand does for one Docker host (environment) by itself: the timezone its schedules run in; whether and when it asks the registries if the host's containers are behind, and whether it then updates them unasked or only notes it; whether and when it prunes the host's images, and whether only the nameless ones or every image no container uses; when it warns that the disk is filling; and, for a host reached with no agent, the folder stack files are staged in. What is not given is kept. auto_update has Dockhand pull newer images and make containers again on its schedule with nobody asked, across the whole host, and a scheduled prune of all removes every image nothing is using at that moment, a stopped stack's among them. A cron expression no scheduler reads is refused here, where Dockhand left to itself saves it and never runs it. Read back from Dockhand.",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in envSchedulesIn) (*mcp.CallToolResult, envSchedulesOut, error) {
		zone, hold, pruneMode, warnMode := strings.TrimSpace(in.Timezone), strings.ToLower(strings.TrimSpace(in.UpdateHoldBack)), strings.ToLower(strings.TrimSpace(in.ImagePruneMode)), strings.ToLower(strings.TrimSpace(in.DiskWarningMode))
		checkCron, pruneCron := strings.TrimSpace(in.UpdateCheckCron), strings.TrimSpace(in.ImagePruneCron)
		var err error
		if checkCron != "" {
			if checkCron, err = cronOf(checkCron); err != nil {
				return nil, envSchedulesOut{}, fmt.Errorf("update_check_cron: %w", err)
			}
		}
		if pruneCron != "" {
			if pruneCron, err = cronOf(pruneCron); err != nil {
				return nil, envSchedulesOut{}, fmt.Errorf("prune_images_cron: %w", err)
			}
		}
		switch {
		case hold != "" && !slices.Contains(holdBacks, hold):
			return nil, envSchedulesOut{}, fmt.Errorf("update_hold_back is %q: it is one of %s", in.UpdateHoldBack, strings.Join(holdBacks, ", "))
		case pruneMode != "" && !slices.Contains(pruneModes, pruneMode):
			return nil, envSchedulesOut{}, fmt.Errorf("prune_images_mode is %q: it is dangling or all", in.ImagePruneMode)
		case warnMode != "" && !slices.Contains(warningModes, warnMode):
			return nil, envSchedulesOut{}, fmt.Errorf("disk_warning_mode is %q: it is percentage or absolute", in.DiskWarningMode)
		case in.DiskWarningPercent < 0 || in.DiskWarningPercent > 100:
			return nil, envSchedulesOut{}, fmt.Errorf("disk_warning_percent is %d: it is how full the disk is before a warning, from 1 to 100", in.DiskWarningPercent)
		case in.DiskWarningGB < 0:
			return nil, envSchedulesOut{}, fmt.Errorf("disk_warning_gb is %d: it is how many gigabytes before a warning, a number above 0", in.DiskWarningGB)
		default:
		}

		env, err := r.one(ctx, in.envArg)
		if err != nil {
			return nil, envSchedulesOut{}, err
		}
		out := envSchedulesOut{Env: env.Name, Changes: []string{}, DryRun: in.DryRun}
		var unread string
		if out.hostSettings, unread = r.settingsOf(ctx, env); unread != "" {
			return nil, out, fmt.Errorf("%s of %s, so nothing was changed: what is not given has to be read to be kept", unread, env.Name)
		}
		was := out.hostSettings

		// each setting is its own call: what is asked for is worked out first, and nothing is sent for what is so already. The timezone goes first,
		// since Dockhand knows which there are and refuses one there is none of before anything else is saved
		var todo []func() error
		if zone != "" && zone != was.Timezone {
			out.Changes = append(out.Changes, "its timezone, which was "+was.Timezone+", is "+zone)
			todo = append(todo, func() error {
				_, failed := r.dock.PostEnvironmentsByIdTimezone(ctx, env.ID, dockhand.PostEnvironmentsByIdTimezoneRequest{Timezone: zone})

				return failed
			})
		}

		check := *was.UpdateCheck
		if in.UpdateCheck != nil {
			check.Enabled = *in.UpdateCheck
		}
		if in.AutoUpdate != nil {
			check.AutoUpdate = *in.AutoUpdate
		}
		check.Cron, check.HoldBack = cmp.Or(checkCron, check.Cron), cmp.Or(hold, check.HoldBack)
		if check != *was.UpdateCheck {
			out.Changes = append(out.Changes, fmt.Sprintf("its update check, which was %s, is %s", saidOfCheck(was.UpdateCheck), saidOfCheck(&check)))
			todo = append(todo, func() error {
				_, failed := r.dock.PostEnvironmentsByIdUpdateCheck(ctx, env.ID, dockhand.PostEnvironmentsByIdUpdateCheckRequest{Enabled: new(check.Enabled), Cron: check.Cron, AutoUpdate: new(check.AutoUpdate), VulnerabilityCriteria: check.HoldBack})

				return failed
			})
		}

		prune := *was.ImagePrune
		if in.ImagePrune != nil {
			prune.Enabled = *in.ImagePrune
		}
		prune.Cron, prune.Mode = cmp.Or(pruneCron, prune.Cron), cmp.Or(pruneMode, prune.Mode)
		if prune != *was.ImagePrune {
			out.Changes = append(out.Changes, fmt.Sprintf("its image prune, which was %s, is %s", saidOfPrune(was.ImagePrune), saidOfPrune(&prune)))
			todo = append(todo, func() error {
				_, failed := r.dock.PostEnvironmentsByIdImagePrune(ctx, env.ID, dockhand.PostEnvironmentsByIdImagePruneRequest{Enabled: new(prune.Enabled), CronExpression: prune.Cron, PruneMode: prune.Mode})

				return failed
			})
		}

		warning := *was.DiskWarning
		if in.DiskWarning != nil {
			warning.Enabled = *in.DiskWarning
		}
		warning.Mode, warning.Percent, warning.GB = cmp.Or(warnMode, warning.Mode), cmp.Or(in.DiskWarningPercent, warning.Percent), cmp.Or(in.DiskWarningGB, warning.GB)
		if warning != *was.DiskWarning {
			out.Changes = append(out.Changes, fmt.Sprintf("its disk warning, which was %s, is %s", saidOfWarning(was.DiskWarning), saidOfWarning(&warning)))
			todo = append(todo, func() error {
				_, failed := r.dock.PostEnvironmentsByIdDiskWarning(ctx, env.ID, dockhand.PostEnvironmentsByIdDiskWarningRequest{Enabled: new(warning.Enabled), Mode: dockhand.DiskWarningMode(warning.Mode), Threshold: warning.Percent, ThresholdGb: warning.GB})

				return failed
			})
		}

		if in.RemoteStacksDir != nil && strings.TrimSpace(*in.RemoteStacksDir) != was.RemoteStacksDir {
			dir := strings.TrimSpace(*in.RemoteStacksDir)
			out.Changes = append(out.Changes, fmt.Sprintf("its remote stacks folder, which was %q, is %q", was.RemoteStacksDir, dir))
			todo = append(todo, func() error {
				_, failed := r.dock.PostEnvironmentsByIdRemoteStacksDir(ctx, env.ID, dockhand.PostEnvironmentsByIdRemoteStacksDirRequest{RemoteStacksDir: dir})

				return failed
			})
		}

		switch {
		case len(out.Changes) == 0 && zone == "" && in.UpdateCheck == nil && checkCron == "" && in.AutoUpdate == nil && hold == "" && in.ImagePrune == nil && pruneCron == "" && pruneMode == "" && in.DiskWarning == nil && warnMode == "" && in.DiskWarningPercent == 0 && in.DiskWarningGB == 0 && in.RemoteStacksDir == nil:
			return nil, out, errors.New("nothing to set was given: timezone, the update check, the image prune, the disk warning or remote_stacks_dir")
		case len(out.Changes) == 0:
			out.Note = "nothing was done: it is so already"

			return nil, out, nil
		case in.DryRun:
			return nil, out, nil
		default:
		}

		for done, set := range todo {
			if err := set(); err != nil {
				out.Changes = out.Changes[:done]
				out.hostSettings, _ = r.settingsOf(ctx, env)

				return nil, out, fmt.Errorf("the settings of %s were not all saved, and what came after the one that failed was not tried: %w. Saved before it: %s", env.Name, refusal(err), cmp.Or(strings.Join(out.Changes, "; "), "nothing"))
			}
		}
		if out.hostSettings, unread = r.settingsOf(ctx, env); unread != "" {
			out.Note = unread + " back"
		}

		return nil, out, nil
	})
}

// saidOfCheck is an update check in words.
func saidOfCheck(c *updateCheck) string {
	if !c.Enabled {
		return "off"
	}
	what := "noting what is behind"
	if c.AutoUpdate {
		what = "updating what is behind, held back by a scan: " + c.HoldBack
	}

	return "at " + c.Cron + ", " + what
}

// saidOfPrune is a scheduled image prune in words.
func saidOfPrune(p *pruneSchedule) string {
	if !p.Enabled {
		return "off"
	}

	return "at " + p.Cron + ", of " + p.Mode
}

// saidOfWarning is a disk warning in words.
func saidOfWarning(w *diskWarning) string {
	switch {
	case !w.Enabled:
		return "off"
	case w.Mode == "absolute":
		return fmt.Sprintf("at %d GB", w.GB)
	default:
		return fmt.Sprintf("at %d%% full", w.Percent)
	}
}
