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

// defaultNetworks is how many networks network_list lists unless told.
const defaultNetworks = 200

// builtinNetworks are the three networks every Docker host has and none can be removed: the default bridge, the host's own, and none at all.
var builtinNetworks = []string{"bridge", "host", "none"}

// networkRow is one network of a host as network_list answers with it.
type networkRow struct {
	Env        string   `json:"env"                jsonschema:"the environment (Docker host) the network is on"`
	Name       string   `json:"name"`
	ID         string   `json:"id"                 jsonschema:"the start of the network's id, as docker shows it"`
	Driver     string   `json:"driver"             jsonschema:"bridge for a network of the host's own, host and null for two of the three Docker always has, overlay for one a swarm shares"`
	Subnets    []string `json:"subnets,omitempty"  jsonschema:"the address ranges of the network"`
	Internal   bool     `json:"internal,omitempty" jsonschema:"the network is cut off from everything outside the host"`
	Builtin    bool     `json:"builtin,omitempty"  jsonschema:"one of the three networks every Docker host has, which cannot be removed: bridge, host and none"`
	Containers []string `json:"containers"         jsonschema:"the containers on the network, running or not, by name; none for a network nothing is on"`
}

// netMember is a container on a network.
type netMember struct {
	ID      string
	Name    string
	State   string
	Address string
}

// active reports whether a member holds the network open: Docker keeps an end of a network only for a container that is running, so a stopped one is
// on it by its own record alone.
func (m netMember) active() bool {
	return running(m.State)
}

// membersOf is the containers of an environment by the name of each network they are on, running or not. It is read from the containers: a network's
// own record names only the ones that are running, and one put on a network while it was stopped is in nobody's list but its own.
func membersOf(containers []dockhand.GetContainersResponse) map[string][]netMember {
	members := map[string][]netMember{}
	for i := range containers {
		c := &containers[i]
		for network := range c.Networks {
			members[network] = append(members[network], netMember{ID: c.Id, Name: c.Name, State: c.State, Address: c.Networks[network].IpAddress})
		}
	}
	for network := range members {
		slices.SortFunc(members[network], func(a, b netMember) int { return cmp.Compare(a.Name, b.Name) })
	}

	return members
}

// networkRowOf is a network of an environment as the tools give one, with the containers that are on it.
func networkRowOf(env string, n *dockhand.GetNetworksResponse, members []netMember) networkRow {
	row := networkRow{Env: env, Name: n.Name, ID: n.Id, Driver: n.Driver, Internal: yes(n.Internal), Builtin: slices.Contains(builtinNetworks, n.Name), Containers: make([]string, 0, len(members))}
	if len(row.ID) > shortID {
		row.ID = row.ID[:shortID]
	}
	if n.Ipam != nil {
		for i := range n.Ipam.Config {
			if n.Ipam.Config[i].Subnet != "" {
				row.Subnets = append(row.Subnets, n.Ipam.Config[i].Subnet)
			}
		}
	}
	for i := range members {
		row.Containers = append(row.Containers, members[i].Name)
	}

	return row
}

// hostNetworks is the networks of an environment and who is on each.
type hostNetworks struct {
	Networks []dockhand.GetNetworksResponse
	Members  map[string][]netMember
}

// networksOf is every network on an environment, and the containers on each. Both are needed to say anything of a network that can be relied on, so
// one that cannot be read fails the two.
func (r *registry) networksOf(ctx context.Context, env environment) (hostNetworks, error) {
	listed, err := r.dock.GetNetworks(ctx, dockhand.GetNetworksOperationOptions{Env: &env.ID})
	if err != nil {
		return hostNetworks{}, refusal(err)
	}
	containers, err := r.containersOf(ctx, env)
	if err != nil {
		return hostNetworks{}, fmt.Errorf("its networks were listed and its containers, which say who is on them, could not be: %w", err)
	}

	return hostNetworks{Networks: listed.Model, Members: membersOf(containers)}, nil
}

// networkArg is the arguments of a tool about one network: which, and where to look for it.
type networkArg struct {
	envArg

	Network string `json:"network" jsonschema:"the network, by its name or by the start of its id, as network_list gives them"`
}

// locatedNetwork is one network, the environment it is on and the containers on it.
type locatedNetwork struct {
	Env     environment
	Network dockhand.GetNetworksResponse
	Members []netMember
}

// row is the network as the tools give one.
func (l *locatedNetwork) row() networkRow {
	return networkRowOf(l.Env.Name, &l.Network, l.Members)
}

// has reports whether a container is on the network, and gives it as it is there.
func (l *locatedNetwork) has(containerID string) (netMember, bool) {
	for i := range l.Members {
		if l.Members[i].ID == containerID {
			return l.Members[i], true
		}
	}

	return netMember{}, false
}

// described is the containers on the network, each with its state.
func (l *locatedNetwork) described() []string {
	said := make([]string, 0, len(l.Members))
	for i := range l.Members {
		said = append(said, l.Members[i].Name+" ("+l.Members[i].State+")")
	}

	return said
}

// findNetwork is the one network a call names, looked for on the environment given or on every one: by its name, and failing that by the start of its
// id. Every host has a bridge, and a stack brought up on two hosts has its network on both, so one found on several is not guessed at: the call is
// refused, naming them, until it says which.
func (r *registry) findNetwork(ctx context.Context, in networkArg) (locatedNetwork, error) {
	want := strings.TrimSpace(in.Network)
	if want == "" {
		return locatedNetwork{}, errors.New("network is required: the network's name, or the start of its id, as network_list gives them")
	}

	envs, err := r.pick(ctx, in.envArg)
	if err != nil {
		return locatedNetwork{}, err
	}

	var named, byID []locatedNetwork
	var failed []string
	for _, answer := range ask(ctx, envs, r.networksOf) {
		if answer.Err != nil {
			failed = append(failed, answer.Env.Name+": "+answer.Err.Error())

			continue
		}
		for i := range answer.Value.Networks {
			switch n := answer.Value.Networks[i]; {
			case n.Name == want:
				named = append(named, locatedNetwork{Env: answer.Env, Network: n, Members: answer.Value.Members[n.Name]})
			case len(want) >= minIDPrefix && strings.HasPrefix(n.Id, want):
				byID = append(byID, locatedNetwork{Env: answer.Env, Network: n, Members: answer.Value.Members[n.Name]})
			default:
			}
		}
	}

	matches := named
	if len(matches) == 0 {
		matches = byID
	}
	switch {
	case len(matches) == 1:
		return matches[0], nil
	case len(matches) > 1:
		at := make([]string, 0, len(matches))
		for i := range matches {
			at = append(at, matches[i].Network.Name+" on "+matches[i].Env.Name)
		}
		slices.Sort(at)

		return locatedNetwork{}, fmt.Errorf("%q is more than one network (%s): say which with env, or with more of its id", want, strings.Join(at, ", "))
	case len(failed) > 0:
		return locatedNetwork{}, fmt.Errorf("no network named %q was found, and not every environment could be asked: %s", want, strings.Join(failed, "; "))
	default:
		names := make([]string, 0, len(envs))
		for _, e := range envs {
			names = append(names, e.Name)
		}

		return locatedNetwork{}, fmt.Errorf("no network is named %q, or has an id that starts with it, on %s: network_list gives the ones there are", want, strings.Join(names, ", "))
	}
}

// reread is a network as its environment has it now, and whether it still does.
func (r *registry) reread(ctx context.Context, at *locatedNetwork) (locatedNetwork, bool, error) {
	now, err := r.networksOf(ctx, at.Env)
	if err != nil {
		return locatedNetwork{}, false, err
	}
	for i := range now.Networks {
		if now.Networks[i].Id == at.Network.Id {
			return locatedNetwork{Env: at.Env, Network: now.Networks[i], Members: now.Members[now.Networks[i].Name]}, true, nil
		}
	}

	return locatedNetwork{}, false, nil
}

//nolint:gocyclo,maintidx // the network tools registered in one function, each a handler of its own: the count is theirs added up
func registerNetworkTools(r *registry) {
	type networkListIn struct {
		envArg

		Name   string `json:"name,omitempty"   jsonschema:"only networks whose name contains this, whatever its case"`
		Unused bool   `json:"unused,omitempty" jsonschema:"only networks no container is on, running or stopped, the three Docker always has left out: the ones that can be removed with nothing left stranded"`
		Limit  int    `json:"limit,omitempty"  jsonschema:"the most networks to list, 200 unless given; the counts are of every network that matched"`
	}
	type networkListOut struct {
		Networks []networkRow `json:"networks"         jsonschema:"the networks that matched, by environment and then by name"`
		Total    int          `json:"total"            jsonschema:"how many networks matched, on every environment asked; more than are listed when the list was cut at the limit"`
		Unused   int          `json:"unused"           jsonschema:"how many of those no container is on, the three Docker always has not counted"`
		Failed   []envFailure `json:"failed,omitempty" jsonschema:"the environments that could not be asked, each with why; their networks are in no count"`
		Note     string       `json:"note,omitempty"`
	}
	add(r, readTool, &mcp.Tool{
		Name:        "network_list",
		Description: "List the Docker networks on every Docker host (environment) the Dockhand manages, or on one: each with its host, name, kind, address ranges, whether it is cut off from outside, and the containers on it, running or not. Every host has three of Docker's own, bridge, host and none, which are marked builtin. Narrow it to one environment or a part of a name, or ask for unused alone, which is every network no container is on, running or stopped. A host that cannot be asked is named with the reason beside what the others answered. network_get gives each container's address on one.",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in networkListIn) (*mcp.CallToolResult, networkListOut, error) {
		if in.Limit < 0 {
			return nil, networkListOut{}, fmt.Errorf("limit is %d: it is how many networks to list at most, a number above 0, or left out for %d", in.Limit, defaultNetworks)
		}
		limit := cmp.Or(in.Limit, defaultNetworks)

		envs, err := r.pick(ctx, in.envArg)
		if err != nil {
			return nil, networkListOut{}, err
		}

		out := networkListOut{Networks: []networkRow{}}
		for _, answer := range ask(ctx, envs, r.networksOf) {
			if answer.Err != nil {
				out.Failed = append(out.Failed, envFailure{Env: answer.Env.Name, Error: answer.Err.Error()})

				continue
			}

			rows := make([]networkRow, 0, len(answer.Value.Networks))
			for i := range answer.Value.Networks {
				row := networkRowOf(answer.Env.Name, &answer.Value.Networks[i], answer.Value.Members[answer.Value.Networks[i].Name])
				unused := len(row.Containers) == 0 && !row.Builtin
				if (in.Unused && !unused) || (in.Name != "" && !strings.Contains(strings.ToLower(row.Name), strings.ToLower(in.Name))) {
					continue
				}

				out.Total++
				if unused {
					out.Unused++
				}
				rows = append(rows, row)
			}
			slices.SortFunc(rows, func(a, b networkRow) int { return cmp.Compare(a.Name, b.Name) })

			out.Networks = append(out.Networks, rows[:min(len(rows), max(0, limit-len(out.Networks)))]...)
		}

		if len(out.Failed) == len(envs) {
			return nil, out, fmt.Errorf("no environment could be asked for its networks: %s: %s", out.Failed[0].Env, out.Failed[0].Error)
		}
		if len(out.Failed) > 0 {
			out.Note = fmt.Sprintf("%d of the %d environments could not be asked, and their networks are missing from this", len(out.Failed), len(envs))
		}
		if out.Total > len(out.Networks) {
			out.Note = note(out.Note, fmt.Sprintf("%d networks matched and the first %d are listed: narrow it by env or name, or raise limit", out.Total, len(out.Networks)))
		}

		return nil, out, nil
	})

	type networkMember struct {
		Container string `json:"container"         jsonschema:"the container's name"`
		State     string `json:"state"             jsonschema:"running, exited, restarting, paused, created or dead"`
		Address   string `json:"address,omitempty" jsonschema:"the container's address on the network; absent while it is not running"`
		MAC       string `json:"mac,omitempty"     jsonschema:"the hardware address of the container's end of the network; absent while it is not running"`
	}
	type networkRange struct {
		Subnet  string `json:"subnet"`
		Gateway string `json:"gateway,omitempty"`
	}
	type networkGetIn struct {
		networkArg
		revealArg
	}
	type networkGetOut struct {
		networkRow

		FullID     string            `json:"full_id"              jsonschema:"the network's whole id"`
		Stack      string            `json:"stack,omitempty"      jsonschema:"the compose stack (project) that made the network; absent for one made on its own"`
		Created    string            `json:"created,omitempty"    jsonschema:"when the network was made, in UTC"`
		Attachable bool              `json:"attachable,omitempty" jsonschema:"a container started on its own may join the network, which matters for a swarm's"`
		IPv6       bool              `json:"ipv6,omitempty"       jsonschema:"the network carries IPv6 as well"`
		Ranges     []networkRange    `json:"ranges,omitempty"     jsonschema:"the network's address ranges, each with the address its gateway has"`
		Labels     map[string]string `json:"labels,omitempty"     jsonschema:"the labels the network was made with"`
		Options    map[string]string `json:"options,omitempty"    jsonschema:"what the network's driver was told when it was made"`
		Members    []networkMember   `json:"members"              jsonschema:"the containers on the network, running or not, each with its address there when it has one"`
		Note       string            `json:"note,omitempty"`
	}
	add(r, readTool, &mcp.Tool{
		Name:        "network_get",
		Description: "Describe one Docker network on a host in full: its kind, when it was made and by which compose stack, its address ranges and gateways, whether it is cut off from outside, its labels and what its driver was told, and the containers on it, each with its address and hardware address there. Name it by its name or the start of its id; say which host with env, since most names are on every host. A container that is not running is listed with no address. A password or token among its labels is hidden unless reveal_secrets asks for it.",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in networkGetIn) (*mcp.CallToolResult, networkGetOut, error) {
		at, err := r.findNetwork(ctx, in.networkArg)
		if err != nil {
			return nil, networkGetOut{}, err
		}

		out := networkGetOut{networkRow: at.row(), FullID: at.Network.Id, Members: make([]networkMember, 0, len(at.Members))}
		if at.Network.Ipam != nil {
			for i := range at.Network.Ipam.Config {
				out.Ranges = append(out.Ranges, networkRange{Subnet: at.Network.Ipam.Config[i].Subnet, Gateway: at.Network.Ipam.Config[i].Gateway})
			}
		}

		// Docker's own record of the network has what the list leaves out, and the running containers' ends of it
		ends := map[string]dockhand.GetNetworksByIdInspectResponseContainers{}
		if one, err := r.dock.GetNetworksByIdInspect(ctx, at.Network.Id, dockhand.GetNetworksByIdInspectOperationOptions{Env: &at.Env.ID}); err != nil {
			out.Note = "the rest of its record could not be read: " + refusal(err).Error()
		} else {
			labels, hidden := maskedLabels(one.Model.Labels, in.RevealSecrets)
			out.Labels, out.Options, out.Stack = labels, one.Model.Options, one.Model.Labels[labelStack]
			out.Created, out.Attachable, out.IPv6 = dockerTime(one.Model.Created), yes(one.Model.Attachable), yes(one.Model.EnableIPv6)
			if hidden > 0 {
				out.Note = hiddenNote(hidden, "told by the names beside them")
			}
			ends = one.Model.Containers
		}

		for i := range at.Members {
			out.Members = append(out.Members, networkMember{Container: at.Members[i].Name, State: at.Members[i].State, Address: at.Members[i].Address, MAC: ends[at.Members[i].ID].MacAddress})
		}

		return nil, out, nil
	})

	type networkCreateIn struct {
		envArg
		dryRunArg

		Name       string            `json:"name"                 jsonschema:"the new network's name"`
		Driver     string            `json:"driver,omitempty"     jsonschema:"the kind of network; bridge, a network of the host's own, unless said"`
		Subnet     string            `json:"subnet,omitempty"     jsonschema:"the address range to give it, such as 172.30.0.0/24; left out, Docker picks one that is free"`
		Gateway    string            `json:"gateway,omitempty"    jsonschema:"the gateway's address within subnet; left out, the range's first address"`
		Internal   bool              `json:"internal,omitempty"   jsonschema:"cut the network off from everything outside the host: its containers reach each other and nothing else"`
		Attachable bool              `json:"attachable,omitempty" jsonschema:"let a container started on its own join it, for a swarm's network"`
		IPv6       bool              `json:"ipv6,omitempty"       jsonschema:"carry IPv6 as well"`
		Labels     map[string]string `json:"labels,omitempty"     jsonschema:"labels to make the network with, by name"`
	}
	type networkCreateOut struct {
		Env     string      `json:"env"`
		Created bool        `json:"created"           jsonschema:"the network was made, and is read back from the host"`
		Network *networkRow `json:"network,omitempty" jsonschema:"the network as the host now lists it; absent with dry_run"`
		DryRun  bool        `json:"dry_run,omitempty" jsonschema:"nothing was made"`
		Note    string      `json:"note,omitempty"`
	}
	add(r, writeTool, &mcp.Tool{
		Name:        "network_create",
		Description: "Make a Docker network on one Docker host (environment), as docker network create does: a bridge of the host's own unless another kind is said, with an address range Docker picks unless one is given. It names one environment. A name the host already has a network under is refused. Nothing is on the network until a container is connected to it or started on it. Read back from the host.",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in networkCreateIn) (*mcp.CallToolResult, networkCreateOut, error) {
		name := strings.TrimSpace(in.Name)
		if name == "" || strings.ContainsAny(name, " \t\n/") {
			return nil, networkCreateOut{}, fmt.Errorf("name is %q: it is the new network's name, with no spaces or slashes in it", in.Name)
		}
		subnet, gateway := strings.TrimSpace(in.Subnet), strings.TrimSpace(in.Gateway)
		if gateway != "" && subnet == "" {
			return nil, networkCreateOut{}, errors.New("gateway is given with no subnet: a gateway is an address within the range the network is given")
		}
		env, err := r.one(ctx, in.envArg)
		if err != nil {
			return nil, networkCreateOut{}, err
		}

		out := networkCreateOut{Env: env.Name, DryRun: in.DryRun}
		before, err := r.networksOf(ctx, env)
		if err != nil {
			return nil, out, fmt.Errorf("the networks of %s could not be listed, so nothing was made: %w", env.Name, err)
		}
		if slices.ContainsFunc(before.Networks, func(n dockhand.GetNetworksResponse) bool { return n.Name == name }) {
			return nil, out, fmt.Errorf("%s already has a network named %s, which was left as it is: network_get describes it", env.Name, name)
		}
		if in.DryRun {
			out.Note = fmt.Sprintf("nothing was made: without dry_run a %s network named %s is made on %s", cmp.Or(strings.TrimSpace(in.Driver), "bridge"), name, env.Name)

			return nil, out, nil
		}

		body := dockhand.PostNetworksRequest{Name: name, Driver: cmp.Or(strings.TrimSpace(in.Driver), "bridge")}
		// a flag is sent only when it is asked for, so that what Docker does unasked stays Docker's to choose
		if in.Internal {
			body.Internal = new(true)
		}
		if in.Attachable {
			body.Attachable = new(true)
		}
		if in.IPv6 {
			body.EnableIPv6 = new(true)
		}
		if subnet != "" {
			addresses := map[string]any{"subnet": subnet}
			if gateway != "" {
				addresses["gateway"] = gateway
			}
			body.Ipam = &dockhand.PostNetworksRequestIpam{Config: []map[string]any{addresses}}
		}
		if len(in.Labels) > 0 {
			body.Labels = make(map[string]any, len(in.Labels))
			for label, value := range in.Labels {
				body.Labels[label] = value
			}
		}

		made, err := r.dock.PostNetworks(ctx, body, dockhand.PostNetworksOperationOptions{Env: &env.ID})
		if err != nil {
			return nil, out, fmt.Errorf("the network %s was not made on %s: %w", name, env.Name, refusal(err))
		}
		if !yes(made.Model.Success) {
			return nil, out, fmt.Errorf("making the network %s was answered without its being said to have succeeded: network_list says whether it is there", name)
		}

		after, err := r.networksOf(ctx, env)
		if err != nil {
			out.Created = true
			out.Note = "it was made, and the networks could not be listed afterwards: " + err.Error()

			return nil, out, nil //nolint:nilerr // the network was made: that it could not be read back is said in the answer
		}
		for i := range after.Networks {
			if after.Networks[i].Name == name {
				row := networkRowOf(env.Name, &after.Networks[i], after.Members[name])
				out.Created, out.Network = true, &row
			}
		}
		if !out.Created {
			return nil, out, fmt.Errorf("making the network %s was answered as done, and %s does not list it", name, env.Name)
		}

		return nil, out, nil
	})

	type networkJoinIn struct {
		networkArg
		dryRunArg

		Container string `json:"container" jsonschema:"the container, by its name or by the start of its id, as container_list gives them"`
	}
	type networkJoinOut struct {
		Env       string `json:"env"`
		Network   string `json:"network"`
		Container string `json:"container"`
		Was       bool   `json:"was"               jsonschema:"whether the container was on the network before"`
		Is        bool   `json:"is"                jsonschema:"whether it is on the network now, read back from the host; with dry_run, as it stands"`
		Address   string `json:"address,omitempty" jsonschema:"the container's address on the network now, when it is on it and running"`
		DryRun    bool   `json:"dry_run,omitempty" jsonschema:"nothing was done"`
		Note      string `json:"note,omitempty"`
	}

	// join puts a container on a network or takes it off, and reads the network back: what both tools do, the one the other's opposite.
	join := func(ctx context.Context, in networkJoinIn, on bool) (networkJoinOut, error) {
		// the container says which host, and the network is looked for there
		container, err := r.find(ctx, containerArg{envArg: in.envArg, Container: in.Container})
		if err != nil {
			return networkJoinOut{}, err
		}
		at, err := r.findNetwork(ctx, networkArg{Env: container.Env.Name, Network: in.Network})
		if err != nil {
			return networkJoinOut{}, err
		}

		member, was := at.has(container.Container.Id)
		out := networkJoinOut{Env: at.Env.Name, Network: at.Network.Name, Container: container.Container.Name, Was: was, Is: was, Address: member.Address, DryRun: in.DryRun}
		verb, done := "connected to", "is already on"
		if !on {
			verb, done = "disconnected from", "is not on"
		}
		if was == on {
			out.Note = fmt.Sprintf("%s %s %s: nothing was done", out.Container, done, out.Network)

			return out, nil
		}
		if in.DryRun {
			out.Note = fmt.Sprintf("nothing was done: without dry_run %s is %s %s", out.Container, verb, out.Network)

			return out, nil
		}

		var succeeded *bool
		if on {
			answer, failed := r.dock.PostNetworksByIdConnect(ctx, at.Network.Id, dockhand.PostNetworksByIdConnectRequest{ContainerId: container.Container.Id, ContainerName: container.Container.Name}, dockhand.PostNetworksByIdConnectOperationOptions{Env: &at.Env.ID})
			if err = failed; err == nil {
				succeeded = answer.Model.Success
			}
		} else {
			answer, failed := r.dock.PostNetworksByIdDisconnect(ctx, at.Network.Id, dockhand.PostNetworksByIdDisconnectRequest{ContainerId: container.Container.Id, ContainerName: container.Container.Name}, dockhand.PostNetworksByIdDisconnectOperationOptions{Env: &at.Env.ID})
			if err = failed; err == nil {
				succeeded = answer.Model.Success
			}
		}
		if err != nil {
			return out, fmt.Errorf("%s was not %s %s: %w", out.Container, verb, out.Network, refusal(err))
		}
		if !yes(succeeded) {
			return out, fmt.Errorf("%s being %s %s was answered without its being said to have succeeded: network_get says what is on it", out.Container, verb, out.Network)
		}

		now, there, err := r.reread(ctx, &at)
		if err != nil || !there {
			out.Is, out.Address = on, ""
			out.Note = "it was done, and the network could not be read back afterwards"

			return out, nil //nolint:nilerr // the change went through: that it could not be read back is said in the answer
		}
		member, out.Is = now.has(container.Container.Id)
		if out.Address = member.Address; out.Is != on {
			return out, fmt.Errorf("%s being %s %s was answered as done, and the host says otherwise", out.Container, verb, out.Network)
		}
		if on && !member.active() {
			out.Note = fmt.Sprintf("%s is not running, so it has no address on %s until it is started", out.Container, out.Network)
		}

		return out, nil
	}

	add(r, writeTool, &mcp.Tool{
		Name:        "network_connect",
		Description: "Put a container on a Docker network as well as the ones it is on, as docker network connect does: it gets an address there and can reach, and be reached by name from, whatever else is on it. The container says which host, and the network is looked for there. One already on the network is left as it is. A container that a compose stack made goes back to the networks its compose file gives it the next time the stack is brought up. Read back from the host.",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in networkJoinIn) (*mcp.CallToolResult, networkJoinOut, error) {
		out, err := join(ctx, in, true)

		return nil, out, err
	})
	add(r, writeTool, &mcp.Tool{
		Name:        "network_disconnect",
		Description: "Take a container off a Docker network, as docker network disconnect does: it loses its address there and whatever reached it over that network no longer does, at once, while it goes on running. The container says which host, and the network is looked for there. One that is not on the network is left as it is. Taking a container off its only network leaves it reaching nothing. Read back from the host.",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in networkJoinIn) (*mcp.CallToolResult, networkJoinOut, error) {
		out, err := join(ctx, in, false)

		return nil, out, err
	})

	type networkDeleteIn struct {
		networkArg

		Confirm bool `json:"confirm,omitempty" jsonschema:"remove it: without this the answer says what would go and nothing is removed"`
	}
	type networkDeleteOut struct {
		Env        string   `json:"env"`
		Name       string   `json:"name"`
		Removed    bool     `json:"removed"            jsonschema:"the network was removed, and is read back as gone"`
		WouldGo    []string `json:"would_go,omitempty" jsonschema:"what removing it takes away, said before anything is removed"`
		Containers []string `json:"containers"         jsonschema:"the containers on it, running or not, each with its state"`
		Note       string   `json:"note,omitempty"`
	}
	add(r, deleteTool, &mcp.Tool{
		Name:        "network_delete",
		Description: "Remove one Docker network from a host. Without confirm the answer says what would go and removes nothing. A network a container is on is refused, with the containers named: Docker refuses one a running container is on, and would remove one that only stopped containers are on and leave them unable to start, so this refuses both until they are disconnected or removed. The three networks every host has, bridge, host and none, cannot be removed. A network a compose stack made is made again the next time the stack is brought up. Read back as gone.",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in networkDeleteIn) (*mcp.CallToolResult, networkDeleteOut, error) {
		at, err := r.findNetwork(ctx, in.networkArg)
		if err != nil {
			return nil, networkDeleteOut{}, err
		}

		row := at.row()
		out := networkDeleteOut{Env: row.Env, Name: row.Name, Containers: at.described()}
		if row.Builtin {
			return nil, out, fmt.Errorf("%s is one of the three networks every Docker host has, and cannot be removed", row.Name)
		}
		// Docker itself refuses only for a running container: one that is stopped would be left unable to start, so it is refused here as well
		if len(at.Members) > 0 {
			return nil, out, fmt.Errorf("the network %s on %s has %d containers on it (%s): a running one keeps Docker from removing it, and a stopped one could not be started without it, so disconnect or remove them first", row.Name, row.Env, len(at.Members), strings.Join(out.Containers, ", "))
		}
		if !in.Confirm {
			out.WouldGo = []string{fmt.Sprintf("the network %s on %s", row.Name, row.Env)}
			out.Note = "nothing was removed: call again with confirm to remove it"

			return nil, out, nil
		}

		gone, err := r.dock.DeleteNetworksById(ctx, at.Network.Id, dockhand.DeleteNetworksByIdOperationOptions{Env: &at.Env.ID})
		if err != nil {
			return nil, out, fmt.Errorf("the network %s was not removed: %w", row.Name, refusal(err))
		}
		if !yes(gone.Model.Success) {
			return nil, out, fmt.Errorf("the removal of the network %s was answered without its being said to have succeeded: network_list says whether it is still there", row.Name)
		}

		_, there, err := r.reread(ctx, &at)
		if err != nil {
			out.Note = "it was removed, and the networks could not be listed afterwards: " + err.Error()

			return nil, out, nil //nolint:nilerr // the removal went through: that it could not be read back is said in the answer
		}
		if there {
			return nil, out, fmt.Errorf("the removal of the network %s was answered as done, and it is still listed", row.Name)
		}
		out.Removed = true

		return nil, out, nil
	})

	type networkPruneIn struct {
		envArg

		Stranding bool `json:"stranding,omitempty" jsonschema:"prune although it would take a network that stopped containers are on, which leaves them unable to start: would_strand names them"`
		Confirm   bool `json:"confirm,omitempty"   jsonschema:"remove them: without this the answer lists what would go and nothing is removed"`
	}
	type networkPruneOut struct {
		Env         string   `json:"env"`
		Removed     []string `json:"removed"                jsonschema:"the networks that were removed, by name; with confirm left out, empty"`
		WouldGo     []string `json:"would_go,omitempty"     jsonschema:"the networks that would be removed, said before anything is: every one no running container is on"`
		WouldStrand []string `json:"would_strand,omitempty" jsonschema:"the stopped containers on a network that would go, each with the network: Docker removes a network that only stopped containers are on, and they cannot be started again until they are put on a network that exists"`
		Stranded    []string `json:"stranded,omitempty"     jsonschema:"the stopped containers that were on a network that was removed, and so will not start as they are"`
		Note        string   `json:"note,omitempty"`
	}
	add(r, deleteTool, &mcp.Tool{
		Name:        "network_prune",
		Description: "Remove every Docker network on one host that no running container is on, as docker network prune does. It names one environment. Without confirm the answer lists what would go and removes nothing. Docker counts only running containers: a network that only stopped containers are on is removed with the rest, and those containers then fail to start, with network not found, until their stack is brought up again or they are made again. So when a prune would take such a network this refuses, naming the containers, unless stranding is given; network_delete removes one unused network at a time without that risk. The three networks every host has are never removed. A network a compose stack made is made again the next time the stack is brought up; one made by hand is not.",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in networkPruneIn) (*mcp.CallToolResult, networkPruneOut, error) {
		env, err := r.one(ctx, in.envArg)
		if err != nil {
			return nil, networkPruneOut{}, err
		}

		out := networkPruneOut{Env: env.Name, Removed: []string{}}
		before, err := r.networksOf(ctx, env)
		if err != nil {
			return nil, out, fmt.Errorf("the networks of %s could not be read, so nothing was removed: %w", env.Name, err)
		}
		// what Docker would take: every network of the host's own making with no running container on it. The stopped ones on those are who it would
		// strand
		stranded := map[string][]string{}
		stopped := map[string]bool{}
		for i := range before.Networks {
			name := before.Networks[i].Name
			members := before.Members[name]
			if slices.Contains(builtinNetworks, name) || slices.ContainsFunc(members, netMember.active) {
				continue
			}
			out.WouldGo = append(out.WouldGo, name)
			for j := range members {
				stopped[members[j].Name] = true
				stranded[name] = append(stranded[name], fmt.Sprintf("%s (%s), on %s", members[j].Name, members[j].State, name))
				out.WouldStrand = append(out.WouldStrand, stranded[name][j])
			}
		}
		slices.Sort(out.WouldGo)
		slices.Sort(out.WouldStrand)

		switch {
		case len(out.WouldGo) == 0:
			out.Note = "every network on the host has a running container on it or is one of Docker's own, so there is nothing to remove"

			return nil, out, nil
		case len(out.WouldStrand) > 0 && !in.Stranding:
			out.Note = fmt.Sprintf("nothing was removed: %d stopped containers are on networks this would take, and could not be started again afterwards. Start or remove them first, remove the unused networks one at a time with network_delete, or give stranding to prune all the same", len(stopped))
			if in.Confirm {
				return nil, out, errors.New(out.Note + ": " + strings.Join(out.WouldStrand, "; "))
			}

			return nil, out, nil
		case !in.Confirm:
			out.Note = "nothing was removed: call again with confirm to remove them"

			return nil, out, nil
		default:
		}

		pruned, err := r.dock.PostPruneNetworks(ctx, dockhand.PostPruneNetworksOperationOptions{Env: &env.ID})
		if err != nil {
			return nil, out, fmt.Errorf("the networks of %s were not pruned: %w", env.Name, refusal(err))
		}
		if !yes(pruned.Model.Success) || pruned.Model.Result == nil {
			return nil, out, fmt.Errorf("the pruning of %s was answered without its being said to have succeeded: network_list says what is left", env.Name)
		}

		out.WouldGo, out.WouldStrand = nil, nil
		out.Removed = append(out.Removed, pruned.Model.Result.NetworksDeleted...)
		slices.Sort(out.Removed)
		for _, name := range out.Removed {
			out.Stranded = append(out.Stranded, stranded[name]...)
		}
		slices.Sort(out.Stranded)
		switch {
		case len(out.Removed) == 0:
			out.Note = "nothing was removed: a container came onto what was unused, or it had gone already"
		case len(out.Stranded) > 0:
			out.Note = "the stopped containers that were on networks that are now gone will not start as they are: bring their stack up again, or make them again"
		default:
		}

		return nil, out, nil
	})
}
