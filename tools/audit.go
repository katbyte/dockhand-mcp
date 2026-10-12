package tools

import (
	"cmp"
	"context"
	"fmt"
	"math"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/katbyte/go-kt/parallel"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

const (
	// defaultAuditFindings is how many findings an audit lists unless told, and maxAuditFindings the most
	defaultAuditFindings = 100
	maxAuditFindings     = 1000
	// inspectAtOnce is how many containers of one host are inspected at a time
	inspectAtOnce = 4
	// defaultStoppedDays is how long a container has to have been stopped for audit_stopped to call it left over
	defaultStoppedDays = 7
	// the kinds of thing a finding is about
	kindContainer = "container"
	kindImage     = "image"
	kindVolume    = "volume"
	kindNetwork   = "network"
	kindStack     = "stack"
	kindHost      = "host"
)

// auditFinding is one thing an audit found wrong.
type auditFinding struct {
	Env    string `json:"env"             jsonschema:"the environment (Docker host) it is on"`
	Kind   string `json:"kind"            jsonschema:"what it is: container, image, volume, network, stack or host"`
	Name   string `json:"name"`
	Stack  string `json:"stack,omitempty" jsonschema:"the compose stack (project) a container belongs to"`
	Image  string `json:"image,omitempty" jsonschema:"the image a container runs"`
	Check  string `json:"check"           jsonschema:"what was found wrong, as one word that by_check counts"`
	Detail string `json:"detail"          jsonschema:"the same in a sentence, with what it means"`
}

// auditOut is what every audit answers with.
type auditOut struct {
	Findings []auditFinding `json:"findings"         jsonschema:"what was found, by environment and then by name; cut at limit"`
	Total    int            `json:"total"            jsonschema:"how many findings there are, on every environment asked; more than are listed when the list was cut at the limit"`
	ByCheck  map[string]int `json:"by_check"         jsonschema:"how many of them are of each check"`
	Checked  int            `json:"checked"          jsonschema:"how many things the audit looked at: a total of 0 beside a checked of 0 says nothing was there to look at, not that all is well"`
	Failed   []envFailure   `json:"failed,omitempty" jsonschema:"the environments that could not be asked, each with why; nothing on them was checked"`
	Note     string         `json:"note,omitempty"`
}

// found adds a finding to what an audit answers with.
func (o *auditOut) found(f auditFinding) {
	o.Findings = append(o.Findings, f)
	o.ByCheck[f.Check]++
	o.Total++
}

// flaw is one thing wrong with a container, before it is said whose it is.
type flaw struct {
	Check  string
	Detail string
}

// surveyed is one container with its inspect document; Doc is nil for one that could not be inspected.
type surveyed struct {
	located

	Doc *inspected
}

// survey is one audit's question: which environments, which stack, and when. It reads every container's inspect document once however many audits
// ask, which is what lets audit_all run them all without asking each host again for each.
type survey struct {
	r     *registry
	envs  []environment
	stack string
	days  int
	now   time.Time

	once  sync.Once
	hosts []asked[[]surveyed]
}

// containers is every container of the environments asked, by name, each with its inspect document.
func (s *survey) containers(ctx context.Context) []asked[[]surveyed] {
	s.once.Do(func() {
		s.hosts = ask(ctx, s.envs, func(ctx context.Context, env environment) ([]surveyed, error) {
			listed, err := s.r.containersOf(ctx, env)
			if err != nil {
				return nil, err
			}
			slices.SortFunc(listed, byContainerName)

			host := make([]surveyed, len(listed))
			// one that cannot be inspected, which is one removed since it was listed, is left without its document and counted as unread
			_ = parallel.Each(ctx, len(listed), inspectAtOnce, func(ctx context.Context, i int) error {
				host[i] = surveyed{Env: env, Container: listed[i]}
				host[i].Doc, _ = s.r.inspect(ctx, &host[i].located)

				return nil
			})

			return host, nil
		})
	})

	return s.hosts
}

// wanted says whether a container is in the stack the audit was narrowed to.
func (s *survey) wanted(labels map[string]string) bool {
	return s.stack == "" || strings.EqualFold(labels[labelStack], s.stack)
}

// ofContainers makes an audit out of one check of a container: the check says whether it is about the container at all, and what is wrong with it.
func ofContainers(check func(c *surveyed, s *survey) (bool, []flaw)) func(ctx context.Context, s *survey) auditOut {
	return func(ctx context.Context, s *survey) auditOut {
		out := auditOut{Findings: []auditFinding{}, ByCheck: map[string]int{}}
		unread := 0
		for _, host := range s.containers(ctx) {
			if host.Err != nil {
				out.Failed = append(out.Failed, envFailure{Env: host.Env.Name, Error: host.Err.Error()})

				continue
			}
			for i := range host.Value {
				c := &host.Value[i]
				if !s.wanted(c.Container.Labels) {
					continue
				}
				if c.Doc == nil {
					unread++

					continue
				}

				about, flaws := check(c, s)
				if !about {
					continue
				}
				out.Checked++
				for _, f := range flaws {
					out.found(auditFinding{Env: host.Env.Name, Kind: kindContainer, Name: c.Container.Name, Stack: c.Container.Labels[labelStack], Image: c.Container.Image, Check: f.Check, Detail: f.Detail})
				}
			}
		}
		if unread > 0 {
			out.Note = fmt.Sprintf("%d containers could not be inspected and were not checked: they were most likely removed while the audit ran", unread)
		}

		return out
	}
}

// audit is one audit: its tool, and how it is run.
type audit struct {
	name        string
	description string
	// byStack says the audit can be narrowed to one compose stack
	byStack bool
	run     func(ctx context.Context, s *survey) auditOut
}

// audits is every audit, in the order audit_all runs and lists them: what is broken now first, then what will break, then what is only untidy.
func (r *registry) audits() []audit {
	return []audit{
		{name: "audit_health", byStack: true, run: ofContainers(ailing), description: "Find the containers that are broken right now, on every Docker host (environment) or on one: unhealthy (its own health check is failing, with what the latest check printed), restarting (Docker keeps starting it again), restart_loop (running, and started again only seconds ago), exited_with_error (it stopped with a code other than 0; 137 is how a container ends when it is stopped and does not stop by itself, so it is said to be that), out_of_memory (the host killed it for memory), start_failed (Docker could not start it, with why) and dead. Every container is inspected, running or not. container_logs says what one wrote before it fell over, and container_get gives one in full."},
		{name: "audit_hosts", run: r.auditHosts, description: "Find the Docker hosts (environments) the Dockhand cannot reach: offline. Nothing on such a host is seen by any other audit, so a clean answer from the others says nothing about it. The setup toolset tries a host's connection and says why it fails."},
		{name: "audit_stacks", byStack: true, run: r.auditStacks, description: "Find the compose stacks that are not wholly up, on every Docker host (environment) or on one: partly_running (some of its containers run and some do not, each named with its state) and not_running (none does). A stack taken down on purpose is found too; nothing tells the two apart. stack_list gives each stack's containers and stack_logs what they wrote; the stacks toolset gives one in full, with what its last deploy printed."},
		{name: "audit_healthcheck", byStack: true, run: ofContainers(unchecked), description: "Find the running containers nothing checks, on every Docker host (environment) or on one: no_health_check (neither its image nor whoever made it gave it one) and health_check_off (it was given one and made with it switched off). Docker calls such a container running for as long as its process lives, whether or not it still answers, so audit_health can never find it unhealthy. Only running containers are looked at."},
		{name: "audit_restart_policy", byStack: true, run: ofContainers(unrestarted), description: "Find the running containers that would stay down, on every Docker host (environment) or on one: no_restart_policy, which is a container Docker does not start again when it exits or when the host reboots. Only running containers are looked at: one that ran once and finished is meant to stay stopped. The containers toolset sets a policy."},
		{name: "audit_updates", byStack: true, run: r.auditUpdates, description: "Find the containers a newer image is to be had for, on every Docker host (environment) or on one, as Dockhand last noted it: newer_image (the registry has another image under the very name and tag it runs) and newer_version (a newer version tag exists, named with how far a step it is). It reads what the last check found and asks no registry itself: the updates toolset asks them afresh, and updates a container."},
		{name: "audit_vulnerabilities", run: r.auditVulnerabilities, description: "Find the images with grave known vulnerabilities, on every Docker host (environment) or on one, as the scans last found them: critical_vulnerabilities and high_vulnerabilities, one finding an image with how many of each, how many a newer version puts right and the containers made from it; and images_not_scanned, one finding a host with how many of its images no scan has looked at, which are in no count. It reads what the scans found and scans nothing: the vulnerabilities toolset scans a host, and lists each vulnerability."},
		{name: "audit_security", byStack: true, run: ofContainers(exposed), description: "Find the containers given more of their host than a container needs, on every Docker host (environment) or on one: privileged (every capability of the host), host_network (the host's own network, with no ports of its own to close), host_pid (it sees and can signal every process of the host), docker_socket (the Docker socket is mounted, which is control of the host; Dockhand's own containers need it and are passed over), host_root (the whole of the host's disk is mounted) and added_capabilities (it was given capabilities that reach outside it, each named). Some software needs these; each finding is something to have decided, not necessarily a fault."},
		{name: "audit_image_tags", byStack: true, run: ofContainers(untagged), description: "Find the containers whose image does not say which version they run, on every Docker host (environment) or on one: latest_tag and no_tag (which is latest), where what runs changes whenever the image is pulled again and nothing records what it was; and image_without_name, a container made from an image by its id, which no update check can follow. An image pinned by its digest is exact and is passed over."},
		{name: "audit_limits", byStack: true, run: ofContainers(unlimited), description: "Find the running containers with no memory limit, on every Docker host (environment) or on one: no_memory_limit, a container that may use all of the host's memory, so one that leaks takes the others down with it. Only running containers are looked at. The containers toolset sets one without making the container again."},
		{name: "audit_stopped", byStack: true, run: ofContainers(leftOver), description: "Find the containers left lying stopped, on every Docker host (environment) or on one: stopped_long (not running for more than days, 7 unless given, with when it stopped and what it exited with) and never_started (made that long ago and never run). They hold their image and their volumes in use, so neither is pruned. The containers toolset removes one, or every stopped one of a host."},
		{name: "audit_unused", run: r.auditUnused, description: "Find what a Docker host (environment) holds that nothing uses, on every host or on one: unused_image (no container was made from it, with its size), unused_volume (no container mounts it) and unused_network (no container is on it; the three every host has are passed over). The note adds up the images' sizes. A volume nothing mounts may still hold data someone wants, so each is to be looked at before it is removed; the resources toolset removes and prunes all three."},
	}
}

// capped is what an audit found, cut to the most a call asked for.
func capped(out auditOut, limit int) auditOut {
	if len(out.Findings) > limit {
		out.Findings = out.Findings[:limit]
		out.Note = noted(out.Note, fmt.Sprintf("the first %d of %d: narrow it to one environment, or raise limit", limit, out.Total))
	}

	return out
}

// surveyOf is the question a call asks: the environments it names, checked, and the most findings it wants.
func (r *registry) surveyOf(ctx context.Context, env envArg, stack string, days, limit int) (*survey, int, error) {
	if limit < 0 || limit > maxAuditFindings {
		return nil, 0, fmt.Errorf("limit is %d: it is the most findings to list, from 1 to %d, or left out for %d", limit, maxAuditFindings, defaultAuditFindings)
	}
	if days < 0 {
		return nil, 0, fmt.Errorf("days is %d: it is how long a container has been stopped, 1 or more, or left out for %d", days, defaultStoppedDays)
	}

	envs, err := r.pick(ctx, env)
	if err != nil {
		return nil, 0, err
	}

	return &survey{r: r, envs: envs, stack: strings.TrimSpace(stack), days: cmp.Or(days, defaultStoppedDays), now: time.Now()}, cmp.Or(limit, defaultAuditFindings), nil
}

// auditRow is one audit as audit_all answers with it.
type auditRow struct {
	Audit    string         `json:"audit"              jsonschema:"the audit's tool: call it for the findings behind the count"`
	Findings int            `json:"findings"`
	ByCheck  map[string]int `json:"by_check,omitempty" jsonschema:"how many of them are of each check"`
	Checked  int            `json:"checked"            jsonschema:"how many things the audit looked at"`
	Unasked  int            `json:"unasked,omitempty"  jsonschema:"how many environments the audit could not ask: its count is short by whatever is on them"`
	Took     float64        `json:"took_s"             jsonschema:"how long the audit took, in seconds"`
	Note     string         `json:"note,omitempty"`
}

func registerAuditTools(r *registry) {
	type auditIn struct {
		envArg

		Limit int `json:"limit,omitempty" jsonschema:"the most findings to list, 100 unless given and 1000 at most; the counts are of every finding"`
	}
	type stackAuditIn struct {
		envArg

		Stack string `json:"stack,omitempty" jsonschema:"only this compose stack (project), by its name"`
		Limit int    `json:"limit,omitempty" jsonschema:"the most findings to list, 100 unless given and 1000 at most; the counts are of every finding"`
	}
	type stoppedAuditIn struct {
		envArg

		Stack string `json:"stack,omitempty" jsonschema:"only the containers of this compose stack (project), by its name"`
		Days  int    `json:"days,omitempty"  jsonschema:"how many days a container has to have been stopped to be found, 7 unless given"`
		Limit int    `json:"limit,omitempty" jsonschema:"the most findings to list, 100 unless given and 1000 at most; the counts are of every finding"`
	}

	all := r.audits()
	for i := range all {
		a := &all[i]
		asked := func(ctx context.Context, env envArg, stack string, days, limit int) (auditOut, error) {
			s, most, err := r.surveyOf(ctx, env, stack, days, limit)
			if err != nil {
				return auditOut{}, err
			}

			return capped(a.run(ctx, s), most), nil
		}

		switch {
		case a.name == "audit_stopped":
			add(r, readTool, &mcp.Tool{Name: a.name, Description: a.description}, func(ctx context.Context, _ *mcp.CallToolRequest, in stoppedAuditIn) (*mcp.CallToolResult, auditOut, error) {
				out, err := asked(ctx, in.envArg, in.Stack, in.Days, in.Limit)

				return nil, out, err
			})
		case a.byStack:
			add(r, readTool, &mcp.Tool{Name: a.name, Description: a.description}, func(ctx context.Context, _ *mcp.CallToolRequest, in stackAuditIn) (*mcp.CallToolResult, auditOut, error) {
				out, err := asked(ctx, in.envArg, in.Stack, 0, in.Limit)

				return nil, out, err
			})
		default:
			add(r, readTool, &mcp.Tool{Name: a.name, Description: a.description}, func(ctx context.Context, _ *mcp.CallToolRequest, in auditIn) (*mcp.CallToolResult, auditOut, error) {
				out, err := asked(ctx, in.envArg, "", 0, in.Limit)

				return nil, out, err
			})
		}
	}

	type auditAllOut struct {
		Audits []auditRow   `json:"audits"           jsonschema:"every audit with what it counted, what is broken now first"`
		Total  int          `json:"total"            jsonschema:"the findings of every audit added up"`
		Failed []envFailure `json:"failed,omitempty" jsonschema:"the environments that could not be asked, each with why; nothing on them is in any count"`
		Note   string       `json:"note,omitempty"`
	}
	add(r, readTool, &mcp.Tool{
		Name:        "audit_all",
		Description: "Run every audit over every Docker host (environment) the Dockhand manages, or over one, and give only the counts, so one call says where things need attention: start here, then call the audit whose count is not zero for its findings. The audits, what is broken now first: audit_health, audit_hosts, audit_stacks, audit_healthcheck, audit_restart_policy, audit_updates, audit_vulnerabilities, audit_security, audit_image_tags, audit_limits, audit_stopped and audit_unused. Every container is inspected once for all of them, which takes a few seconds a host and longer with many containers. It only reads.",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in envArg) (*mcp.CallToolResult, auditAllOut, error) {
		s, _, err := r.surveyOf(ctx, in, "", 0, 0)
		if err != nil {
			return nil, auditAllOut{}, err
		}

		out := auditAllOut{Audits: make([]auditRow, 0, len(all))}
		unasked := map[string]string{}
		for i := range all {
			began := time.Now()
			found := all[i].run(ctx, s)

			out.Audits = append(out.Audits, auditRow{Audit: all[i].name, Findings: found.Total, ByCheck: found.ByCheck, Checked: found.Checked, Unasked: len(found.Failed), Took: math.Round(time.Since(began).Seconds()*100) / 100, Note: found.Note})
			out.Total += found.Total
			for _, failed := range found.Failed {
				unasked[failed.Env] = failed.Error
			}
		}
		for _, env := range s.envs {
			if why, failed := unasked[env.Name]; failed {
				out.Failed = append(out.Failed, envFailure{Env: env.Name, Error: why})
			}
		}
		if len(out.Failed) > 0 {
			out.Note = fmt.Sprintf("%d of the %d environments could not be asked by one audit or more: the counts say nothing about what is on them", len(out.Failed), len(s.envs))
		}

		return nil, out, nil
	})
}
