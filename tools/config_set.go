package tools

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"maps"
	"slices"
	"strconv"
	"strings"

	"github.com/katbyte/dockhand-mcp/sdk/dockhand"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// configSetsAre is what every tool about config sets says they are
const configSetsAre = "A config set is a bundle Dockhand keeps for reuse: variables, labels, published ports, mounts, a network mode and a restart policy. Dockhand only keeps them: no call of its API applies one, so the tools that make a container are given a set's values one by one"

// configSetChecks is what the tools that save a config set say of what is checked
const configSetChecks = "Dockhand checks nothing of a set but its name, and saves a port that is no port and a variable with no name as readily as any other, so what is given is checked here first"

// configSetArg names one config set the Dockhand keeps.
type configSetArg struct {
	Set any `json:"set,omitempty" jsonschema:"the config set, by its name or its number as config_set_list gives them"`
}

// configSetRow is one config set the Dockhand keeps, in the words the tools that make a container take.
type configSetRow struct {
	ID            int               `json:"id"`
	Name          string            `json:"name"`
	Description   string            `json:"description,omitempty"`
	EnvVars       map[string]string `json:"env_vars,omitempty"       jsonschema:"the variables it holds, by name; a value hidden as a secret is ***"`
	Labels        map[string]string `json:"labels,omitempty"         jsonschema:"the labels it holds for a container, by name"`
	Ports         []string          `json:"ports,omitempty"          jsonschema:"the ports it publishes, each as host:container/protocol, or container/protocol for a port of the host's choosing"`
	Volumes       []string          `json:"volumes,omitempty"        jsonschema:"what it mounts, each as source:target:mode, the mode being rw or ro"`
	NetworkMode   string            `json:"network_mode,omitempty"   jsonschema:"bridge, host, none or a network's name"`
	RestartPolicy string            `json:"restart_policy,omitempty" jsonschema:"no, always, unless-stopped or on-failure"`
	Hidden        int               `json:"hidden,omitempty"         jsonschema:"how many values above are hidden as secrets; config_set_list shows them when asked to with reveal_secrets"`
	Updated       string            `json:"updated,omitempty"        jsonschema:"when it was last changed, as Dockhand writes it"`
	Note          string            `json:"note,omitempty"`
}

// portOf is a port of a config set as the tools write one.
func portOf(host, container, protocol string) string {
	written := container + "/" + cmp.Or(protocol, "tcp")
	if host != "" {
		written = host + ":" + written
	}

	return written
}

// mountOf is a mount of a config set as the tools write one.
func mountOf(source, target, mode string) string {
	return source + ":" + target + ":" + cmp.Or(mode, "rw")
}

// configSetRowOf is a config set as it is shown: its secrets hidden unless they are asked for, and a note when it holds a name more than once, which
// Dockhand lets it and a list by name cannot show.
func configSetRowOf(s *dockhand.GetConfigSetsResponse, reveal bool) configSetRow {
	row := configSetRow{ID: s.Id, Name: s.Name, Description: s.Description, NetworkMode: s.NetworkMode, RestartPolicy: s.RestartPolicy, Updated: s.UpdatedAt}
	twice := 0
	vars := make(map[string]string, len(s.EnvVars))
	for _, v := range s.EnvVars {
		if _, had := vars[v.Key]; had {
			twice++
		}
		vars[v.Key] = v.Value
	}
	labels := make(map[string]string, len(s.Labels))
	for _, l := range s.Labels {
		if _, had := labels[l.Key]; had {
			twice++
		}
		labels[l.Key] = l.Value
	}
	var hiddenVars, hiddenLabels int
	row.EnvVars, hiddenVars = maskedLabels(vars, reveal)
	row.Labels, hiddenLabels = maskedLabels(labels, reveal)
	row.Hidden = hiddenVars + hiddenLabels
	for _, p := range s.Ports {
		row.Ports = append(row.Ports, portOf(p.HostPort, p.ContainerPort, p.Protocol))
	}
	for _, v := range s.Volumes {
		row.Volumes = append(row.Volumes, mountOf(v.HostPath, v.ContainerPath, v.Mode))
	}
	if twice > 0 {
		row.Note = fmt.Sprintf("the set holds %s more than once, which Dockhand lets it: the last value of each is the one given here", several(twice, "name"))
	}

	return row
}

// configSets is every config set the Dockhand keeps.
func (r *registry) configSets(ctx context.Context) ([]dockhand.GetConfigSetsResponse, error) {
	listed, err := r.dock.GetConfigSets(ctx)
	if err != nil {
		return nil, fmt.Errorf("the config sets could not be listed: %w", refusal(err))
	}

	return listed.Model, nil
}

// configSetIn is the config set of a list a call names, by its number or by its name. Dockhand keeps two whose names differ only by case, so a name
// is matched letter for letter first, and whatever its case only when that is one set alone.
func configSetIn(listed []dockhand.GetConfigSetsResponse, given any) (*dockhand.GetConfigSetsResponse, error) {
	wanted, err := named(given, "set")
	if err != nil {
		return nil, err
	}
	if wanted == "" {
		return nil, errors.New("set is required: the config set's name or number, as config_set_list gives them")
	}
	var near []*dockhand.GetConfigSetsResponse
	names := make([]string, 0, len(listed))
	for i := range listed {
		if strconv.Itoa(listed[i].Id) == wanted || listed[i].Name == wanted {
			return &listed[i], nil
		}
		if strings.EqualFold(listed[i].Name, wanted) {
			near = append(near, &listed[i])
		}
		names = append(names, listed[i].Name)
	}
	switch len(near) {
	case 0:
		return nil, fmt.Errorf("no config set is named or numbered %q: the Dockhand keeps %s", wanted, cmp.Or(strings.Join(names, ", "), "none"))
	case 1:
		return near[0], nil
	default:
		numbers := make([]string, 0, len(near))
		for _, s := range near {
			numbers = append(numbers, s.Name+" ("+strconv.Itoa(s.Id)+")")
		}

		return nil, fmt.Errorf("no config set is named %q letter for letter, and %d are but for their case: give the number of one, %s", wanted, len(near), strings.Join(numbers, " or "))
	}
}

// setPort and setMount are a port and a mount as a config set keeps them.
type (
	setPort  struct{ Host, Container, Protocol string }
	setMount struct{ Source, Target, Mode string }
)

// setPortsOf is the ports a config set is to hold, from the way the tools write them: 8080:80, 53:53/udp, or 80 alone for a port of the host's
// choosing. A config set has no place for an address to publish on.
func setPortsOf(given []string) ([]setPort, error) {
	const form = `a port is written host:container, as 8080:80, with /udp after it for udp; a config set has no place for an address to publish on`

	ports := make([]setPort, 0, len(given))
	for _, entry := range given {
		spec, protocol, _ := strings.Cut(strings.TrimSpace(entry), "/")
		protocol = cmp.Or(strings.ToLower(protocol), "tcp")
		if protocol != "tcp" && protocol != "udp" && protocol != "sctp" {
			return nil, fmt.Errorf("ports has %q, whose protocol is %q: it is tcp, udp or sctp", entry, protocol)
		}
		var host, inside string
		switch parts := strings.Split(spec, ":"); len(parts) {
		case 1:
			inside = parts[0]
		case 2:
			host, inside = parts[0], parts[1]
		default:
			return nil, fmt.Errorf("ports has %q: %s", entry, form)
		}
		if err := isPort(inside, "port of the container"); err != nil {
			return nil, fmt.Errorf("ports has %q: %w; %s", entry, err, form)
		}
		if host != "" {
			if err := isPort(host, "port of the host"); err != nil {
				return nil, fmt.Errorf("ports has %q: %w; %s", entry, err, form)
			}
		}
		ports = append(ports, setPort{Host: host, Container: inside, Protocol: protocol})
	}

	return ports, nil
}

// setMountsOf is the mounts a config set is to hold, from the way the tools write them: source:target, with ro after it for one a container may only
// read.
func setMountsOf(given []string) ([]setMount, error) {
	checked, err := mountedOf(given)
	if err != nil {
		return nil, err
	}
	mounts := make([]setMount, 0, len(checked))
	for _, entry := range checked {
		parts := append(strings.Split(entry, ":"), "rw")
		mounts = append(mounts, setMount{Source: parts[0], Target: parts[1], Mode: parts[2]})
	}

	return mounts, nil
}

// setNamesOf is why the names of a config set's variables or labels are not ones a container takes, or nil: a variable's is letters, digits and
// underscores, and a label's is anything with no space in it.
func setNamesOf(vars, labels map[string]string) error {
	for _, name := range slices.Sorted(maps.Keys(vars)) {
		if !variableNamed.MatchString(name) {
			return fmt.Errorf("env_vars names %q: a variable's name is letters, digits and underscores, and does not start with a digit", name)
		}
	}
	for _, name := range slices.Sorted(maps.Keys(labels)) {
		if name == "" || strings.ContainsAny(name, " \t\n=") {
			return fmt.Errorf("labels names %q: a label's name is not empty and has no space or = in it", name)
		}
	}

	return nil
}

// setRestartOf is a restart policy as a config set keeps one, which is the policy alone: it has no place for how many times a failure is tried again.
func setRestartOf(given string) (string, error) {
	policy, retries, err := restartOf(given)
	if err != nil {
		return "", errors.New(strings.Replace(err.Error(), "restart is", "restart_policy is", 1))
	}
	if retries > 0 {
		return "", fmt.Errorf("restart_policy is %q: a config set keeps the policy alone, and has no place for how many times to try", given)
	}

	return policy, nil
}

// configSetChangeOut is what saving or changing a config set answers.
type configSetChangeOut struct {
	Set     *configSetRow `json:"set,omitempty"     jsonschema:"the config set as Dockhand has it now, read back, with its secrets hidden; absent for a dry run of saving one"`
	Changes []string      `json:"changes"           jsonschema:"what was done, in words; empty when nothing needed doing"`
	DryRun  bool          `json:"dry_run,omitempty" jsonschema:"nothing was done: changes says what would be"`
	Note    string        `json:"note,omitempty"`
}

// configSetBack puts a config set as Dockhand has it once it was saved into the answer of the tool that saved it, or why it could not be read.
func (r *registry) configSetBack(ctx context.Context, id int, out *configSetChangeOut) {
	listed, err := r.configSets(ctx)
	if err != nil {
		out.Set = nil
		out.Note = note(out.Note, "it was saved, and could not be read back: "+err.Error())

		return
	}
	i := slices.IndexFunc(listed, func(s dockhand.GetConfigSetsResponse) bool { return s.Id == id })
	if i < 0 {
		out.Set = nil
		out.Note = note(out.Note, "it was saved, and is not among the config sets read back")

		return
	}
	row := configSetRowOf(&listed[i], false)
	out.Set = &row
}

//nolint:gocyclo // four tools registered in one function, each a handler of its own: the count is theirs added up
func registerConfigSetTools(r *registry) {
	type configSetListIn struct {
		revealArg

		Name string `json:"name,omitempty" jsonschema:"only config sets whose name contains this, whatever its case"`
	}
	type configSetListOut struct {
		Sets   []configSetRow `json:"sets"             jsonschema:"the config sets that matched, by name"`
		Total  int            `json:"total"`
		Hidden int            `json:"hidden,omitempty" jsonschema:"how many values in all are hidden as secrets; reveal_secrets shows them"`
		Note   string         `json:"note,omitempty"`
	}
	add(r, readTool, &mcp.Tool{
		Name:        "config_set_list",
		Description: "List the config sets the Dockhand keeps, each in full: its variables, labels, published ports, mounts, network mode and restart policy, written as the tools that make a container take them. " + configSetsAre + ". The value of a variable or a label whose name says it is a secret - a password, a token, a key - is hidden unless reveal_secrets is given.",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in configSetListIn) (*mcp.CallToolResult, configSetListOut, error) {
		listed, err := r.configSets(ctx)
		if err != nil {
			return nil, configSetListOut{}, err
		}
		out := configSetListOut{Sets: []configSetRow{}}
		want := strings.ToLower(strings.TrimSpace(in.Name))
		for i := range listed {
			if want != "" && !strings.Contains(strings.ToLower(listed[i].Name), want) {
				continue
			}
			row := configSetRowOf(&listed[i], in.RevealSecrets)
			out.Hidden += row.Hidden
			out.Sets = append(out.Sets, row)
		}
		slices.SortStableFunc(out.Sets, func(a, b configSetRow) int {
			return cmp.Or(cmp.Compare(strings.ToLower(a.Name), strings.ToLower(b.Name)), cmp.Compare(a.ID, b.ID))
		})
		out.Total = len(out.Sets)
		switch {
		case len(listed) == 0:
			out.Note = "the Dockhand keeps no config set: config_set_create saves one"
		case out.Total == 0:
			out.Note = fmt.Sprintf("no config set has %q in its name, of the %d the Dockhand keeps", want, len(listed))
		}

		return nil, out, nil
	})

	type configSetCreateIn struct {
		dryRunArg

		Name          string            `json:"name"                     jsonschema:"what to call the config set"`
		Description   string            `json:"description,omitempty"    jsonschema:"what it is for, in a line"`
		EnvVars       map[string]string `json:"env_vars,omitempty"       jsonschema:"the variables it is to hold, by name"`
		Labels        map[string]string `json:"labels,omitempty"         jsonschema:"the labels it is to hold for a container, by name"`
		Ports         []string          `json:"ports,omitempty"          jsonschema:"the ports it is to publish, each as host:container: 8080:80, 53:53/udp, or 80 alone for a port of the host's choosing"`
		Volumes       []string          `json:"volumes,omitempty"        jsonschema:"what it is to mount, each as source:target: data:/var/lib/app for a named volume, /srv/conf:/etc/app:ro for a folder of the host a container may only read"`
		NetworkMode   string            `json:"network_mode,omitempty"   jsonschema:"bridge, host, none or a network's name; bridge unless given"`
		RestartPolicy string            `json:"restart_policy,omitempty" jsonschema:"no, always, unless-stopped or on-failure; no unless given"`
	}
	add(r, writeTool, &mcp.Tool{
		Name:        "config_set_create",
		Description: "Save a config set on the Dockhand: a name, and the variables, labels, published ports, mounts, network mode and restart policy it is to hold. " + configSetsAre + ". " + configSetChecks + ". A name in use is refused, as Dockhand refuses it; one that differs from another's only by case is saved, and said to. Values are kept by Dockhand as they are given, and given back by its API to whoever may read config sets: a secret put in one is no better kept than that.",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in configSetCreateIn) (*mcp.CallToolResult, configSetChangeOut, error) {
		name := strings.TrimSpace(in.Name)
		if name == "" {
			return nil, configSetChangeOut{}, errors.New("name is required: what to call the config set")
		}
		if err := setNamesOf(in.EnvVars, in.Labels); err != nil {
			return nil, configSetChangeOut{}, err
		}
		ports, err := setPortsOf(in.Ports)
		if err != nil {
			return nil, configSetChangeOut{}, err
		}
		mounts, err := setMountsOf(in.Volumes)
		if err != nil {
			return nil, configSetChangeOut{}, err
		}
		restart, err := setRestartOf(in.RestartPolicy)
		if err != nil {
			return nil, configSetChangeOut{}, err
		}
		listed, err := r.configSets(ctx)
		if err != nil {
			return nil, configSetChangeOut{}, err
		}
		out := configSetChangeOut{Changes: []string{}, DryRun: in.DryRun}
		for i := range listed {
			if listed[i].Name == name {
				return nil, out, fmt.Errorf("a config set is already named %s, numbered %d: config_set_edit changes it, and another name is needed for another", name, listed[i].Id)
			}
			if strings.EqualFold(listed[i].Name, name) {
				out.Note = fmt.Sprintf("%s is another config set from %s, numbered %d: Dockhand tells them apart by their case", name, listed[i].Name, listed[i].Id)
			}
		}

		request := dockhand.PostConfigSetsRequest{Name: name, Description: strings.TrimSpace(in.Description), NetworkMode: strings.TrimSpace(in.NetworkMode), RestartPolicy: restart}
		for _, key := range slices.Sorted(maps.Keys(in.EnvVars)) {
			request.EnvVars = append(request.EnvVars, dockhand.PostConfigSetsRequestEnvVars{Key: key, Value: in.EnvVars[key]})
		}
		for _, key := range slices.Sorted(maps.Keys(in.Labels)) {
			request.Labels = append(request.Labels, dockhand.PostConfigSetsRequestLabels{Key: key, Value: in.Labels[key]})
		}
		for _, p := range ports {
			request.Ports = append(request.Ports, dockhand.PostConfigSetsRequestPorts{HostPort: p.Host, ContainerPort: p.Container, Protocol: p.Protocol})
		}
		for _, m := range mounts {
			request.Volumes = append(request.Volumes, dockhand.PostConfigSetsRequestVolumes{HostPath: m.Source, ContainerPath: m.Target, Mode: m.Mode})
		}
		what := fmt.Sprintf("the config set %s, with %s, %s, %s and %s", name, several(len(request.EnvVars), "variable"), several(len(request.Labels), "label"), several(len(request.Ports), "port"), several(len(request.Volumes), "mount"))
		if in.DryRun {
			out.Changes = append(out.Changes, "would save "+what)

			return nil, out, nil
		}

		made, err := r.dock.PostConfigSets(ctx, request)
		if err != nil {
			return nil, out, fmt.Errorf("the config set %s was not saved: %w", name, refusal(err))
		}
		out.Changes = append(out.Changes, "saved "+what)
		r.configSetBack(ctx, made.Model.Id, &out)

		return nil, out, nil
	})

	type configSetEditIn struct {
		configSetArg
		dryRunArg

		Name          string            `json:"name,omitempty"            jsonschema:"another name for the config set"`
		Description   *string           `json:"description,omitempty"     jsonschema:"another description; empty to have it forgotten"`
		EnvVars       map[string]string `json:"env_vars,omitempty"        jsonschema:"variables to set, by name: each is set over the value it had or added, and the others are kept"`
		RemoveEnvVars []string          `json:"remove_env_vars,omitempty" jsonschema:"the names of variables to take out of the set"`
		Labels        map[string]string `json:"labels,omitempty"          jsonschema:"labels to set, by name: each is set over the value it had or added, and the others are kept"`
		RemoveLabels  []string          `json:"remove_labels,omitempty"   jsonschema:"the names of labels to take out of the set"`
		Ports         []string          `json:"ports,omitempty"           jsonschema:"every port it is to publish, each as host:container, in place of the ones it has; an empty list for none"`
		Volumes       []string          `json:"volumes,omitempty"         jsonschema:"everything it is to mount, each as source:target, in place of what it has; an empty list for none"`
		NetworkMode   string            `json:"network_mode,omitempty"    jsonschema:"bridge, host, none or a network's name"`
		RestartPolicy string            `json:"restart_policy,omitempty"  jsonschema:"no, always, unless-stopped or on-failure"`
	}
	add(r, writeTool, &mcp.Tool{
		Name:        "config_set_edit",
		Description: "Change a config set the Dockhand keeps: its name and description, its variables and labels, its published ports, its mounts, its network mode and its restart policy. A variable or a label that is given is set and the others are kept, and the ones named to remove are taken out, so a secret that config_set_list does not show need not be known to be kept; the ports and the mounts are each given whole. " + configSetsAre + ", and no container is changed by changing one. " + configSetChecks + ". What is not given is kept.",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in configSetEditIn) (*mcp.CallToolResult, configSetChangeOut, error) {
		if err := setNamesOf(in.EnvVars, in.Labels); err != nil {
			return nil, configSetChangeOut{}, err
		}
		ports, err := setPortsOf(in.Ports)
		if err != nil {
			return nil, configSetChangeOut{}, err
		}
		mounts, err := setMountsOf(in.Volumes)
		if err != nil {
			return nil, configSetChangeOut{}, err
		}
		restart, err := setRestartOf(in.RestartPolicy)
		if err != nil {
			return nil, configSetChangeOut{}, err
		}
		if i := slices.IndexFunc(in.RemoveEnvVars, func(name string) bool { return has(in.EnvVars, name) }); i >= 0 {
			return nil, configSetChangeOut{}, fmt.Errorf("the variable %s is in both env_vars and remove_env_vars: it is set or taken out, not both", in.RemoveEnvVars[i])
		}
		if i := slices.IndexFunc(in.RemoveLabels, func(name string) bool { return has(in.Labels, name) }); i >= 0 {
			return nil, configSetChangeOut{}, fmt.Errorf("the label %s is in both labels and remove_labels: it is set or taken out, not both", in.RemoveLabels[i])
		}
		listed, err := r.configSets(ctx)
		if err != nil {
			return nil, configSetChangeOut{}, err
		}
		saved, err := configSetIn(listed, in.Set)
		if err != nil {
			return nil, configSetChangeOut{}, err
		}
		row := configSetRowOf(saved, false)
		out := configSetChangeOut{Set: &row, Changes: []string{}, DryRun: in.DryRun}

		done := func(did, would string) string {
			if in.DryRun {
				return would
			}

			return did
		}
		request := dockhand.PutConfigSetsByIdRequest{}
		name := strings.TrimSpace(in.Name)
		if name != "" && name != saved.Name {
			if i := slices.IndexFunc(listed, func(s dockhand.GetConfigSetsResponse) bool { return s.Id != saved.Id && s.Name == name }); i >= 0 {
				return nil, out, fmt.Errorf("another config set is named %s, numbered %d, so %s is not renamed to it", name, listed[i].Id, saved.Name)
			}
			request.Name = name
			out.Changes = append(out.Changes, fmt.Sprintf("%s it from %s to %s", done("renamed", "would rename"), saved.Name, name))
		}
		forget := false
		if in.Description != nil {
			description := strings.TrimSpace(*in.Description)
			if description == "" && saved.Description != "" {
				forget = true
				out.Changes = append(out.Changes, done("forgot", "would forget")+" its description")
			}
			if description != "" && description != saved.Description {
				request.Description = description
				out.Changes = append(out.Changes, done("changed", "would change")+" its description")
			}
		}

		// the variables and the labels: each one given set over what it had or added, the ones named taken out, and the rest kept in the order they
		// were
		vars := make([][2]string, 0, len(saved.EnvVars))
		for _, v := range saved.EnvVars {
			vars = append(vars, [2]string{v.Key, v.Value})
		}
		if kept, set, removed := merged(vars, in.EnvVars, in.RemoveEnvVars); len(set)+len(removed) > 0 {
			request.EnvVars = make([]dockhand.PutConfigSetsByIdRequestEnvVars, 0, len(kept))
			for _, v := range kept {
				request.EnvVars = append(request.EnvVars, dockhand.PutConfigSetsByIdRequestEnvVars{Key: v[0], Value: v[1]})
			}
			out.Changes = append(out.Changes, changedNames(done, "variable", set, removed)...)
		}
		labels := make([][2]string, 0, len(saved.Labels))
		for _, l := range saved.Labels {
			labels = append(labels, [2]string{l.Key, l.Value})
		}
		if kept, set, removed := merged(labels, in.Labels, in.RemoveLabels); len(set)+len(removed) > 0 {
			request.Labels = make([]dockhand.PutConfigSetsByIdRequestLabels, 0, len(kept))
			for _, l := range kept {
				request.Labels = append(request.Labels, dockhand.PutConfigSetsByIdRequestLabels{Key: l[0], Value: l[1]})
			}
			out.Changes = append(out.Changes, changedNames(done, "label", set, removed)...)
		}
		if in.Ports != nil {
			wanted := make([]string, 0, len(ports))
			request.Ports = make([]dockhand.PutConfigSetsByIdRequestPorts, 0, len(ports))
			for _, p := range ports {
				wanted = append(wanted, portOf(p.Host, p.Container, p.Protocol))
				request.Ports = append(request.Ports, dockhand.PutConfigSetsByIdRequestPorts{HostPort: p.Host, ContainerPort: p.Container, Protocol: p.Protocol})
			}
			if slices.Equal(wanted, row.Ports) {
				request.Ports = nil
			} else {
				out.Changes = append(out.Changes, fmt.Sprintf("%s its ports, which were %s, to %s", done("set", "would set"), cmp.Or(strings.Join(row.Ports, ", "), "none"), cmp.Or(strings.Join(wanted, ", "), "none")))
			}
		}
		if in.Volumes != nil {
			wanted := make([]string, 0, len(mounts))
			request.Volumes = make([]dockhand.PutConfigSetsByIdRequestVolumes, 0, len(mounts))
			for _, m := range mounts {
				wanted = append(wanted, mountOf(m.Source, m.Target, m.Mode))
				request.Volumes = append(request.Volumes, dockhand.PutConfigSetsByIdRequestVolumes{HostPath: m.Source, ContainerPath: m.Target, Mode: m.Mode})
			}
			if slices.Equal(wanted, row.Volumes) {
				request.Volumes = nil
			} else {
				out.Changes = append(out.Changes, fmt.Sprintf("%s its mounts, which were %s, to %s", done("set", "would set"), cmp.Or(strings.Join(row.Volumes, ", "), "none"), cmp.Or(strings.Join(wanted, ", "), "none")))
			}
		}
		if mode := strings.TrimSpace(in.NetworkMode); mode != "" && mode != saved.NetworkMode {
			request.NetworkMode = mode
			out.Changes = append(out.Changes, fmt.Sprintf("%s its network mode from %s to %s", done("changed", "would change"), cmp.Or(saved.NetworkMode, "nothing"), mode))
		}
		if restart != "" && restart != saved.RestartPolicy {
			request.RestartPolicy = restart
			out.Changes = append(out.Changes, fmt.Sprintf("%s its restart policy from %s to %s", done("changed", "would change"), cmp.Or(saved.RestartPolicy, "nothing"), restart))
		}
		if len(out.Changes) == 0 {
			if name == "" && in.Description == nil && len(in.EnvVars)+len(in.RemoveEnvVars)+len(in.Labels)+len(in.RemoveLabels) == 0 && in.Ports == nil && in.Volumes == nil && strings.TrimSpace(in.NetworkMode) == "" && restart == "" {
				return nil, out, errors.New("nothing to change was given: name, description, env_vars, remove_env_vars, labels, remove_labels, ports, volumes, network_mode or restart_policy")
			}
			out.Note = "nothing to do: it is that way already"

			return nil, out, nil
		}
		if in.DryRun {
			return nil, out, nil
		}

		if !forget || len(out.Changes) > 1 {
			if _, failed := r.dock.PutConfigSetsById(ctx, saved.Id, request); failed != nil {
				out.Changes = []string{}

				return nil, out, fmt.Errorf("the config set %s was not changed: %w", saved.Name, refusal(failed))
			}
		}
		if forget {
			if _, failed := r.dock.ForgetConfigSetDescription(ctx, saved.Id); failed != nil {
				return nil, out, fmt.Errorf("the description of the config set %s was not forgotten, and anything else asked for was done: %w", saved.Name, refusal(failed))
			}
		}
		r.configSetBack(ctx, saved.Id, &out)

		return nil, out, nil
	})

	type configSetDeleteIn struct {
		configSetArg

		Confirm bool `json:"confirm,omitempty" jsonschema:"remove it: without this the answer says what would go and nothing is removed"`
	}
	type configSetDeleteOut struct {
		Set     string   `json:"set"`
		Removed bool     `json:"removed"            jsonschema:"the config set was removed, and is read back as gone"`
		WouldGo []string `json:"would_go,omitempty" jsonschema:"what removing it takes away, said before anything is removed"`
		Note    string   `json:"note,omitempty"`
	}
	add(r, deleteTool, &mcp.Tool{
		Name:        "config_set_delete",
		Description: "Remove a config set from the Dockhand, with everything it holds. " + configSetsAre + ", and no container is touched by removing one. Without confirm the answer says what the set holds and removes nothing.",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in configSetDeleteIn) (*mcp.CallToolResult, configSetDeleteOut, error) {
		listed, err := r.configSets(ctx)
		if err != nil {
			return nil, configSetDeleteOut{}, err
		}
		saved, err := configSetIn(listed, in.Set)
		if err != nil {
			return nil, configSetDeleteOut{}, err
		}
		out := configSetDeleteOut{Set: saved.Name}
		if !in.Confirm {
			out.WouldGo = []string{fmt.Sprintf("the config set %s, numbered %d, with its %s, %s, %s and %s", saved.Name, saved.Id, several(len(saved.EnvVars), "variable"), several(len(saved.Labels), "label"), several(len(saved.Ports), "port"), several(len(saved.Volumes), "mount"))}
			out.Note = "nothing was removed: call again with confirm to remove it. No container is touched either way"

			return nil, out, nil
		}

		gone, err := r.dock.DeleteConfigSetsById(ctx, saved.Id)
		if err != nil {
			return nil, out, fmt.Errorf("the config set %s was not removed: %w", saved.Name, refusal(err))
		}
		if !yes(gone.Model.Success) {
			return nil, out, fmt.Errorf("the removal of the config set %s was answered without its being said to have succeeded: config_set_list says whether it is still there", saved.Name)
		}
		left, err := r.configSets(ctx)
		if err != nil {
			out.Note = "it was removed, and the config sets could not be listed afterwards: " + err.Error()

			return nil, out, nil //nolint:nilerr // the removal went through: that it could not be read back is said in the answer
		}
		if slices.ContainsFunc(left, func(s dockhand.GetConfigSetsResponse) bool { return s.Id == saved.Id }) {
			return nil, out, fmt.Errorf("the removal of the config set %s was answered as done, and it is still listed", saved.Name)
		}
		out.Removed = true

		return nil, out, nil
	})
}

// has is whether a name is one of those given.
func has(given map[string]string, name string) bool {
	_, is := given[name]

	return is
}

// merged is a list of names and values once some are set and some taken out: each one given is set over the value it had, wherever the name is held,
// or added in order of name, the ones named to remove are dropped, and the rest are kept as they were. It gives the names that were set to something
// they were not and the ones that were there to take out.
func merged(had [][2]string, given map[string]string, remove []string) (kept [][2]string, set, removed []string) {
	kept = make([][2]string, 0, len(had)+len(given))
	seen := map[string]bool{}
	for _, pair := range had {
		name := pair[0]
		if slices.Contains(remove, name) {
			if !slices.Contains(removed, name) {
				removed = append(removed, name)
			}

			continue
		}
		if value, is := given[name]; is {
			if value != pair[1] && !slices.Contains(set, name) {
				set = append(set, name)
			}
			pair[1] = value
		}
		seen[name] = true
		kept = append(kept, pair)
	}
	for _, name := range slices.Sorted(maps.Keys(given)) {
		if !seen[name] {
			kept = append(kept, [2]string{name, given[name]})
			set = append(set, name)
		}
	}
	slices.Sort(set)
	slices.Sort(removed)

	return kept, set, removed
}

// changedNames is what was done to the variables or the labels of a config set, in words, by their names alone: a value may be a secret.
func changedNames(done func(did, would string) string, thing string, set, removed []string) []string {
	the := func(names []string) string {
		if len(names) == 1 {
			return "the " + thing + " " + names[0]
		}

		return "the " + thing + "s " + strings.Join(names, ", ")
	}
	var changes []string
	if len(set) > 0 {
		changes = append(changes, done("set", "would set")+" "+the(set))
	}
	if len(removed) > 0 {
		changes = append(changes, done("took out", "would take out")+" "+the(removed))
	}

	return changes
}
