package tools

import (
	"net/http"
	"strings"
	"testing"

	"github.com/katbyte/dockhand-mcp/sdk/dockhandtest"
	"github.com/katbyte/go-kt/mcp/acctest"
)

// The networks of the two canned hosts, beside the containers of container_test.go, which say who is on each: the bridge every container started on
// its own is on, the web stack's network with its two running containers, one nothing is on, and Docker's other two. On beta, a bridge with its one
// container.
const (
	bridgeNet = `{"id":"b41d9e00000000000000000000000000000000000000000000000000000000aa","name":"bridge","driver":"bridge","scope":"local","internal":false,"ipam":{"driver":"default","config":[{"subnet":"172.17.0.0/16","gateway":"172.17.0.1"}]},"containers":{}}`
	webNet    = `{"id":"3eb0000000000000000000000000000000000000000000000000000000000bb0","name":"web_default","driver":"bridge","scope":"local","internal":false,"ipam":{"driver":"default","config":[{"subnet":"172.18.0.0/16","gateway":"172.18.0.1"}]},"containers":{}}`
	spareNet  = `{"id":"59a4e000000000000000000000000000000000000000000000000000000000cc","name":"spare","driver":"bridge","scope":"local","internal":true,"ipam":{"driver":"default","config":[{"subnet":"172.30.0.0/24","gateway":"172.30.0.1"},{"subnet":"fd00:30::/64"}]},"containers":{}}`
	hostNet   = `{"id":"4057000000000000000000000000000000000000000000000000000000000dd0","name":"host","driver":"host","scope":"local","internal":false,"ipam":{"driver":"default","config":[]},"containers":{}}`
	noneNet   = `{"id":"404e000000000000000000000000000000000000000000000000000000000ee0","name":"none","driver":"null","scope":"local","internal":false,"ipam":{"driver":"default","config":[]},"containers":{}}`

	alphaNetworks = `[` + bridgeNet + `,` + webNet + `,` + spareNet + `,` + hostNet + `,` + noneNet + `]`
	betaNetworks  = `[` + bridgeNet + `]`

	spareID  = "59a4e000000000000000000000000000000000000000000000000000000000cc"
	webNetID = "3eb0000000000000000000000000000000000000000000000000000000000bb0"
)

// withNetworks is the two canned hosts with their networks as well.
func withNetworks(t *testing.T) *dockhandtest.Server {
	t.Helper()

	dock := twoHosts(t)
	dock.AnswerWhen("GET /api/networks", "env", "1", alphaNetworks)
	dock.AnswerWhen("GET /api/networks", "env", "2", betaNetworks)

	return dock
}

// onNetwork has the canned alpha list a container of its own as on a network as well as the ones it is on, or no longer on it, from when a call is
// made: how connecting and disconnecting come to be read back.
func onNetwork(dock *dockhandtest.Server, call, container, network string, on bool) {
	without := `"name":"` + container + `","image":"registry.example/tool:latest","imageId":"sha256:cafe","command":"sleep 100000","created":1791615492,"state":"running","status":"Up 12 seconds","health":null,"restartCount":0,"labels":{},"mounts":[],"networks":{"bridge":{"ipAddress":"172.17.0.2"}}`
	with := strings.Replace(without, `"networks":{`, `"networks":{"`+network+`":{"ipAddress":"172.30.0.2"},`, 1)
	dock.Then(call, func(dockhandtest.Request) {
		if on {
			dock.AnswerWhen("GET /api/containers", "env", "1", strings.Replace(alphaContainers, without, with, 1))
		} else {
			dock.AnswerWhen("GET /api/containers", "env", "1", alphaContainers)
		}
	})
}

func TestNetworkList(t *testing.T) {
	t.Parallel()

	suite := session(t, withNetworks(t), Options{})

	out := suite.Call(t, "network_list", nil)
	rows := acctest.Rows(t, out["networks"], "networks")
	order := make([]string, 0, len(rows))
	for _, row := range rows {
		order = append(order, acctest.Str(row["env"])+" "+acctest.Str(row["name"]))
	}
	if got := strings.Join(order, "|"); got != "alpha bridge|alpha host|alpha none|alpha spare|alpha web_default|beta bridge" {
		t.Errorf("the networks, in order = %s", got)
	}
	// one network nothing is on; Docker's own three are not counted as unused though nothing is on two of them
	if acctest.Num(t, out["total"], "total") != 6 || acctest.Num(t, out["unused"], "unused") != 1 || out["failed"] != nil || out["note"] != nil {
		t.Errorf("the counts = %v", out)
	}

	// who is on a network is read from the containers, stopped ones too, which the network's own record does not name
	bridge := rows[0]
	if !acctest.IsBool(bridge["builtin"], true) || bridge["id"] != "b41d9e000000" || bridge["driver"] != "bridge" || strings.Join(acctest.Strs(t, bridge["subnets"], "subnets"), "|") != "172.17.0.0/16" || strings.Join(acctest.Strs(t, bridge["containers"], "containers"), "|") != "crashed|done|flapping|looping|loose|steady|stopped" {
		t.Errorf("the bridge = %v", bridge)
	}
	if spare := rows[3]; spare["builtin"] != nil || !acctest.IsBool(spare["internal"], true) || strings.Join(acctest.Strs(t, spare["subnets"], "subnets"), "|") != "172.30.0.0/24|fd00:30::/64" || len(acctest.Strs(t, spare["containers"], "containers")) != 0 {
		t.Errorf("a network nothing is on = %v", spare)
	}
	if web := rows[4]; strings.Join(acctest.Strs(t, web["containers"], "containers"), "|") != "web-back-1|web-front-1" || web["internal"] != nil {
		t.Errorf("a stack's network = %v", web)
	}

	names := func(args map[string]any) string {
		listed := acctest.Rows(t, suite.Call(t, "network_list", args)["networks"], "networks")
		got := make([]string, 0, len(listed))
		for _, row := range listed {
			got = append(got, acctest.Str(row["env"])+" "+acctest.Str(row["name"]))
		}

		return strings.Join(got, "|")
	}
	for name, tc := range map[string]struct {
		args map[string]any
		want string
	}{
		"one host":              {map[string]any{"env": "beta"}, "beta bridge"},
		"part of a name":        {map[string]any{"name": "BRIDGE"}, "alpha bridge|beta bridge"},
		"what nothing is on":    {map[string]any{"unused": true}, "alpha spare"},
		"no more than a number": {map[string]any{"limit": 2}, "alpha bridge|alpha host"},
	} {
		if got := names(tc.args); got != tc.want {
			t.Errorf("%s = %q, want %q", name, got, tc.want)
		}
	}
	if cut := suite.Call(t, "network_list", map[string]any{"limit": 2}); acctest.Num(t, cut["total"], "total") != 6 || !strings.Contains(acctest.Str(cut["note"]), "6 networks matched and the first 2 are listed") {
		t.Errorf("cut at a limit = %v", cut)
	}
	if msg := suite.CallErr(t, "network_list", map[string]any{"limit": -1}); !strings.Contains(msg, "limit is -1") {
		t.Errorf("a limit below none = %q", msg)
	}
}

func TestNetworkListWithAHostDown(t *testing.T) {
	t.Parallel()

	// a host whose containers cannot be read cannot say who is on its networks, and is said to have failed rather than listed as though nothing were
	dock := withNetworks(t)
	dock.FailWhen("GET /api/containers", "env", "2", http.StatusInternalServerError, "Failed to list containers")
	suite := session(t, dock, Options{})
	out := suite.Call(t, "network_list", nil)
	if failed := acctest.Rows(t, out["failed"], "failed"); len(failed) != 1 || failed[0]["env"] != "beta" || !strings.Contains(acctest.Str(failed[0]["error"]), "its containers, which say who is on them, could not be") || acctest.Num(t, out["total"], "total") != 5 {
		t.Errorf("with one host's containers unreadable = %v", out)
	}

	dock.FailWhen("GET /api/networks", "env", "1", http.StatusInternalServerError, "Failed to list networks")
	if msg := suite.CallErr(t, "network_list", nil); !strings.Contains(msg, "no environment could be asked for its networks") {
		t.Errorf("with both down = %q", msg)
	}
}

func TestNetworkGet(t *testing.T) {
	t.Parallel()

	dock := withNetworks(t)
	dock.Answer("GET /api/networks/"+webNetID+"/inspect", `{"Name":"web_default","Id":"`+webNetID+`","Created":"2026-10-03T04:00:00.148768134Z","Scope":"local","Driver":"bridge","EnableIPv4":true,"EnableIPv6":false,"IPAM":{"Driver":"default","Options":null,"Config":[{"Subnet":"172.18.0.0/16","Gateway":"172.18.0.1"}]},"Internal":false,"Attachable":true,"Ingress":false,"Options":{"com.docker.network.bridge.name":"br-web"},"Labels":{"com.docker.compose.project":"web","deploy.token":"not-a-real-token"},"Containers":{"aaaaaaaaaaaa1111111111111111111111111111111111111111111111111111":{"Name":"web-front-1","EndpointID":"e1","MacAddress":"02:42:ac:12:00:02","IPv4Address":"172.18.0.2/16","IPv6Address":""}}}`)
	suite := session(t, dock, Options{})

	out := suite.Call(t, "network_get", map[string]any{"network": "web_default"})
	if out["env"] != "alpha" || out["full_id"] != webNetID || out["id"] != "3eb000000000" || out["stack"] != "web" || out["created"] != "2026-10-03T04:00:00Z" || !acctest.IsBool(out["attachable"], true) || out["ipv6"] != nil || out["builtin"] != nil {
		t.Errorf("network_get = %v", out)
	}
	if ranges := acctest.Rows(t, out["ranges"], "ranges"); len(ranges) != 1 || ranges[0]["subnet"] != "172.18.0.0/16" || ranges[0]["gateway"] != "172.18.0.1" {
		t.Errorf("its ranges = %v", out["ranges"])
	}
	if acctest.Object(t, out["options"], "options")["com.docker.network.bridge.name"] != "br-web" {
		t.Errorf("its options = %v", out["options"])
	}
	// each container with its state and its address there, and the hardware address of the one whose end Docker's record has
	members := acctest.Rows(t, out["members"], "members")
	if len(members) != 2 || members[0]["container"] != "web-back-1" || members[0]["state"] != "running" || members[0]["mac"] != nil || members[1]["container"] != "web-front-1" || members[1]["address"] != "172.18.0.2" || members[1]["mac"] != "02:42:ac:12:00:02" {
		t.Errorf("its members = %v", members)
	}
	// a label named as a secret is hidden until it is asked for
	if acctest.Object(t, out["labels"], "labels")["deploy.token"] != masked || !strings.Contains(acctest.Str(out["note"]), "1 secret is hidden") || strings.Contains(flat(out), "not-a-real") {
		t.Errorf("its labels = %v, note %v", out["labels"], out["note"])
	}
	if shown := suite.Call(t, "network_get", map[string]any{"network": "web_default", "reveal_secrets": true}); acctest.Object(t, shown["labels"], "labels")["deploy.token"] != "not-a-real-token" || shown["note"] != nil {
		t.Errorf("its labels, revealed = %v", shown)
	}
	if made := dock.Last("GET /api/networks/" + webNetID + "/inspect"); made.Query.Get("env") != "1" {
		t.Errorf("its record was asked on environment %q", made.Query.Get("env"))
	}

	// the rest of its record failing is said, and what the list and the containers gave is still given
	dock.Fail("GET /api/networks/"+webNetID+"/inspect", http.StatusInternalServerError, "Failed to inspect network")
	if out = suite.Call(t, "network_get", map[string]any{"network": "3eb0"}); out["name"] != "web_default" || len(acctest.Rows(t, out["members"], "members")) != 2 || !strings.Contains(acctest.Str(out["note"]), "the rest of its record could not be read") {
		t.Errorf("with its record unreadable = %v", out)
	}
}

// A network is found by its name before the start of an id, and one that is on several hosts, as the bridge always is, is refused until the host is
// said.
func TestNetworkFinding(t *testing.T) {
	t.Parallel()

	dock := withNetworks(t)
	suite := session(t, dock, everything)

	for name, tc := range map[string]struct {
		args map[string]any
		want []string
	}{
		"none":                    {map[string]any{"network": " "}, []string{"network is required"}},
		"one name on two hosts":   {map[string]any{"network": "bridge"}, []string{`"bridge" is more than one network (bridge on alpha, bridge on beta)`, "say which with env"}},
		"an id on two hosts":      {map[string]any{"network": "b41d9e"}, []string{"is more than one network"}},
		"an id too short to mean": {map[string]any{"network": "59a"}, []string{`no network is named "59a"`, "on alpha, beta", "network_list"}},
		"one that is not there":   {map[string]any{"network": "nope", "env": "alpha"}, []string{`no network is named "nope"`, "on alpha:"}},
		"a host there is none of": {map[string]any{"network": "spare", "env": "gamma"}, []string{`no environment is named or numbered "gamma"`}},
	} {
		// asked of the tool that removes, without confirm: finding comes first whatever is then done
		msg := suite.CallErr(t, "network_delete", tc.args)
		for _, want := range tc.want {
			if !strings.Contains(msg, want) {
				t.Errorf("%s = %q, want %q in it", name, msg, want)
			}
		}
	}
	if out := suite.Call(t, "network_delete", map[string]any{"network": "59a4e0"}); out["name"] != "spare" || out["env"] != "alpha" {
		t.Errorf("found by the start of its id = %v", out)
	}

	dock.FailWhen("GET /api/networks", "env", "2", http.StatusInternalServerError, "Failed to connect to the agent")
	if msg := suite.CallErr(t, "network_delete", map[string]any{"network": "nope"}); !strings.Contains(msg, "not every environment could be asked") || !strings.Contains(msg, "beta: ") {
		t.Errorf("not found with a host down = %q", msg)
	}
}

func TestNetworkCreate(t *testing.T) {
	t.Parallel()

	const call = "POST /api/networks"
	made := `{"id":"fe5400000000000000000000000000000000000000000000000000000000ff00","name":"fresh","driver":"bridge","scope":"local","internal":true,"ipam":{"driver":"default","config":[{"subnet":"172.31.0.0/24","gateway":"172.31.0.1"}]},"containers":{}}`

	dock := withNetworks(t)
	dock.Answer(call, `{"success":true,"id":"fe5400000000000000000000000000000000000000000000000000000000ff00"}`)
	dock.Then(call, func(dockhandtest.Request) {
		dock.AnswerWhen("GET /api/networks", "env", "1", `[`+bridgeNet+`,`+made+`]`)
	})
	suite := session(t, dock, everything)

	dry := suite.Call(t, "network_create", map[string]any{"env": "alpha", "name": "fresh", "dry_run": true})
	if !acctest.IsBool(dry["dry_run"], true) || !acctest.IsBool(dry["created"], false) || dry["network"] != nil || !strings.Contains(acctest.Str(dry["note"]), "a bridge network named fresh is made on alpha") || len(dock.Requests(call)) != 0 {
		t.Errorf("a dry run = %v", dry)
	}

	// everything that can be said of it, each sent as Dockhand takes it
	out := suite.Call(t, "network_create", map[string]any{"env": "alpha", "name": " fresh ", "subnet": "172.31.0.0/24", "gateway": "172.31.0.1", "internal": true, "attachable": true, "ipv6": true, "labels": map[string]any{"team": "web"}})
	row := acctest.Object(t, out["network"], "network")
	if !acctest.IsBool(out["created"], true) || row["name"] != "fresh" || !acctest.IsBool(row["internal"], true) || strings.Join(acctest.Strs(t, row["subnets"], "subnets"), "|") != "172.31.0.0/24" || out["note"] != nil {
		t.Errorf("network_create = %v", out)
	}
	if sent := dock.Last(call); sent.Query.Get("env") != "1" || sent.Body != `{"attachable":true,"driver":"bridge","enableIPv6":true,"internal":true,"ipam":{"config":[{"gateway":"172.31.0.1","subnet":"172.31.0.0/24"}]},"labels":{"team":"web"},"name":"fresh"}` {
		t.Errorf("the network was asked for on environment %q with %s", sent.Query.Get("env"), sent.Body)
	}

	// with nothing said of it but its name, nothing else is sent but the kind
	dock.AnswerWhen("GET /api/networks", "env", "1", alphaNetworks)
	suite.Call(t, "network_create", map[string]any{"env": "alpha", "name": "fresh", "driver": "macvlan", "subnet": "10.9.0.0/24"})
	if sent := dock.Last(call); sent.Body != `{"driver":"macvlan","ipam":{"config":[{"subnet":"10.9.0.0/24"}]},"name":"fresh"}` {
		t.Errorf("a network of another kind was sent as %s", sent.Body)
	}

	dock.AnswerWhen("GET /api/networks", "env", "1", alphaNetworks)
	asked := len(dock.Requests(call))
	for name, tc := range map[string]struct {
		args map[string]any
		want string
	}{
		"a name that is taken":     {map[string]any{"env": "alpha", "name": "spare"}, "alpha already has a network named spare"},
		"no name":                  {map[string]any{"env": "alpha", "name": " "}, `name is " "`},
		"a name of two words":      {map[string]any{"env": "alpha", "name": "my net"}, `name is "my net"`},
		"a gateway with no subnet": {map[string]any{"env": "alpha", "name": "fresh2", "gateway": "172.31.0.1"}, "gateway is given with no subnet"},
		"no host":                  {map[string]any{"name": "fresh2"}, "env is required"},
	} {
		if msg := suite.CallErr(t, "network_create", tc.args); !strings.Contains(msg, tc.want) {
			t.Errorf("%s = %q, want %q", name, msg, tc.want)
		}
	}
	if len(dock.Requests(call)) != asked {
		t.Error("a network that was refused was asked of Dockhand all the same")
	}
}

func TestNetworkCreateThatGoesWrong(t *testing.T) {
	t.Parallel()

	const call = "POST /api/networks"
	dock := withNetworks(t)
	suite := session(t, dock, everything)
	args := map[string]any{"env": "alpha", "name": "fresh"}

	dock.Fail(call, http.StatusInternalServerError, "Failed to create network")
	if msg := suite.CallErr(t, "network_create", args); !strings.Contains(msg, "the network fresh was not made on alpha") || !strings.Contains(msg, "Failed to create network") {
		t.Errorf("a network Dockhand fails to make = %q", msg)
	}
	dock.Answer(call, `{"success":false}`)
	if msg := suite.CallErr(t, "network_create", args); !strings.Contains(msg, "without its being said to have succeeded") {
		t.Errorf("answered without success = %q", msg)
	}
	dock.Answer(call, `{"success":true,"id":"fe54"}`)
	if msg := suite.CallErr(t, "network_create", args); !strings.Contains(msg, "answered as done, and alpha does not list it") {
		t.Errorf("made and not listed = %q", msg)
	}
	dock.Then(call, func(dockhandtest.Request) {
		dock.FailWhen("GET /api/networks", "env", "1", http.StatusInternalServerError, "Failed to list networks")
	})
	if out := suite.Call(t, "network_create", args); !acctest.IsBool(out["created"], true) || !strings.Contains(acctest.Str(out["note"]), "it was made, and the networks could not be listed afterwards") {
		t.Errorf("made and not read back = %v", out)
	}
	dock.Then(call, nil)
	if msg := suite.CallErr(t, "network_create", args); !strings.Contains(msg, "could not be listed, so nothing was made") {
		t.Errorf("a host that cannot be read = %q", msg)
	}
}

func TestNetworkConnectAndDisconnect(t *testing.T) {
	t.Parallel()

	connect, disconnect := "POST /api/networks/"+spareID+"/connect", "POST /api/networks/"+spareID+"/disconnect"

	// the container says which host; the network is looked for there, where bridge would otherwise be two
	dock := withNetworks(t)
	dock.Answer(connect, `{"success":true}`)
	onNetwork(dock, connect, "loose", "spare", true)
	suite := session(t, dock, everything)

	dry := suite.Call(t, "network_connect", map[string]any{"network": "spare", "container": "loose", "dry_run": true})
	if !acctest.IsBool(dry["dry_run"], true) || !acctest.IsBool(dry["was"], false) || !acctest.IsBool(dry["is"], false) || !strings.Contains(acctest.Str(dry["note"]), "without dry_run loose is connected to spare") || len(dock.Requests(connect)) != 0 {
		t.Errorf("a dry run = %v", dry)
	}
	out := suite.Call(t, "network_connect", map[string]any{"network": "spare", "container": "loose"})
	if out["env"] != "alpha" || out["network"] != "spare" || out["container"] != "loose" || !acctest.IsBool(out["was"], false) || !acctest.IsBool(out["is"], true) || out["address"] != "172.30.0.2" || out["note"] != nil {
		t.Errorf("network_connect = %v", out)
	}
	if sent := dock.Last(connect); sent.Query.Get("env") != "1" || sent.Body != `{"containerId":"`+looseID+`","containerName":"loose"}` {
		t.Errorf("the connection was asked on environment %q with %s", sent.Query.Get("env"), sent.Body)
	}
	// already on it: left as it is, and nothing is asked of Dockhand
	asked := len(dock.Requests(connect))
	if again := suite.Call(t, "network_connect", map[string]any{"network": "spare", "container": "loose"}); !acctest.IsBool(again["was"], true) || !acctest.IsBool(again["is"], true) || !strings.Contains(acctest.Str(again["note"]), "loose is already on spare: nothing was done") || len(dock.Requests(connect)) != asked {
		t.Errorf("connected a second time = %v", again)
	}

	// and off again
	dock.Answer(disconnect, `{"success":true}`)
	onNetwork(dock, disconnect, "loose", "spare", false)
	out = suite.Call(t, "network_disconnect", map[string]any{"network": "spare", "container": "loose"})
	if !acctest.IsBool(out["was"], true) || !acctest.IsBool(out["is"], false) || out["address"] != nil || out["note"] != nil {
		t.Errorf("network_disconnect = %v", out)
	}
	if sent := dock.Last(disconnect); sent.Body != `{"containerId":"`+looseID+`","containerName":"loose"}` {
		t.Errorf("the disconnection was sent as %s", sent.Body)
	}
	asked = len(dock.Requests(disconnect))
	if again := suite.Call(t, "network_disconnect", map[string]any{"network": "spare", "container": "loose"}); !acctest.IsBool(again["is"], false) || !strings.Contains(acctest.Str(again["note"]), "loose is not on spare: nothing was done") || len(dock.Requests(disconnect)) != asked {
		t.Errorf("disconnected a second time = %v", again)
	}

	// a container that is not running is put on the network and has no address there yet, which is said
	stoppedConnect := "POST /api/networks/" + spareID + "/connect"
	dock.Then(stoppedConnect, func(dockhandtest.Request) {
		dock.AnswerWhen("GET /api/containers", "env", "1", strings.Replace(alphaContainers, `"name":"stopped","image":"registry.example/tool:latest","imageId":"sha256:cafe","command":"sleep 100000","created":1791615492,"state":"exited","status":"Exited (137) 10 seconds ago","health":null,"restartCount":0,"labels":{},"mounts":[],"networks":{"bridge":{"ipAddress":""}}`, `"name":"stopped","image":"registry.example/tool:latest","imageId":"sha256:cafe","command":"sleep 100000","created":1791615492,"state":"exited","status":"Exited (137) 10 seconds ago","health":null,"restartCount":0,"labels":{},"mounts":[],"networks":{"bridge":{"ipAddress":""},"spare":{"ipAddress":""}}`, 1))
	})
	if out = suite.Call(t, "network_connect", map[string]any{"network": "spare", "container": "stopped"}); !acctest.IsBool(out["is"], true) || out["address"] != nil || !strings.Contains(acctest.Str(out["note"]), "stopped is not running, so it has no address on spare until it is started") {
		t.Errorf("a stopped container connected = %v", out)
	}
}

func TestNetworkConnectThatGoesWrong(t *testing.T) {
	t.Parallel()

	connect := "POST /api/networks/" + spareID + "/connect"
	dock := withNetworks(t)
	suite := session(t, dock, everything)
	args := map[string]any{"network": "spare", "container": "loose"}

	dock.Fail(connect, http.StatusInternalServerError, "Failed to connect container to network")
	if msg := suite.CallErr(t, "network_connect", args); !strings.Contains(msg, "loose was not connected to spare") || !strings.Contains(msg, "Failed to connect container to network") {
		t.Errorf("a connection Dockhand fails = %q", msg)
	}
	dock.Answer(connect, `{"success":false}`)
	if msg := suite.CallErr(t, "network_connect", args); !strings.Contains(msg, "without its being said to have succeeded") {
		t.Errorf("answered without success = %q", msg)
	}
	dock.Answer(connect, `{"success":true}`)
	if msg := suite.CallErr(t, "network_connect", args); !strings.Contains(msg, "loose being connected to spare was answered as done, and the host says otherwise") {
		t.Errorf("a connection that did not take = %q", msg)
	}
	dock.Then(connect, func(dockhandtest.Request) {
		dock.FailWhen("GET /api/networks", "env", "1", http.StatusInternalServerError, "Failed to list networks")
	})
	if out := suite.Call(t, "network_connect", args); !acctest.IsBool(out["is"], true) || !strings.Contains(acctest.Str(out["note"]), "it was done, and the network could not be read back afterwards") {
		t.Errorf("connected and not read back = %v", out)
	}
	dock.Then(connect, nil)
	dock.AnswerWhen("GET /api/networks", "env", "1", alphaNetworks)

	for name, tc := range map[string]struct {
		args map[string]any
		want string
	}{
		"a container there is none of": {map[string]any{"network": "spare", "container": "nope"}, `no container is named "nope"`},
		"a network there is none of":   {map[string]any{"network": "nope", "container": "loose"}, `no network is named "nope"`},
		"a network of another host":    {map[string]any{"network": "spare", "container": "solo"}, `no network is named "spare", or has an id that starts with it, on beta`},
		"no network":                   {map[string]any{"network": " ", "container": "loose"}, "network is required"},
	} {
		if msg := suite.CallErr(t, "network_disconnect", tc.args); !strings.Contains(msg, tc.want) {
			t.Errorf("%s = %q, want %q", name, msg, tc.want)
		}
	}
}

func TestNetworkDelete(t *testing.T) {
	t.Parallel()

	call := "DELETE /api/networks/" + spareID
	dock := withNetworks(t)
	suite := session(t, dock, everything)

	out := suite.Call(t, "network_delete", map[string]any{"network": "spare"})
	if !acctest.IsBool(out["removed"], false) || out["env"] != "alpha" || out["name"] != "spare" || strings.Join(acctest.Strs(t, out["would_go"], "would_go"), "|") != "the network spare on alpha" || len(acctest.Strs(t, out["containers"], "containers")) != 0 || len(dock.Requests(call)) != 0 {
		t.Errorf("without confirm = %v", out)
	}

	dock.Answer(call, `{"success":true}`)
	dock.Then(call, func(dockhandtest.Request) {
		dock.AnswerWhen("GET /api/networks", "env", "1", `[`+bridgeNet+`,`+webNet+`,`+hostNet+`,`+noneNet+`]`)
	})
	if out = suite.Call(t, "network_delete", map[string]any{"network": "spare", "confirm": true}); !acctest.IsBool(out["removed"], true) || out["would_go"] != nil || out["note"] != nil || dock.Last(call).Query.Get("env") != "1" {
		t.Errorf("with confirm = %v", out)
	}

	// one a container is on is refused with the containers and their states, and Docker's own are refused outright
	if msg := suite.CallErr(t, "network_delete", map[string]any{"network": "web_default", "confirm": true}); !strings.Contains(msg, "the network web_default on alpha has 2 containers on it (web-back-1 (running), web-front-1 (running))") || !strings.Contains(msg, "disconnect or remove them first") {
		t.Errorf("a network in use = %q", msg)
	}
	for _, builtin := range []string{"bridge", "host", "none"} {
		if msg := suite.CallErr(t, "network_delete", map[string]any{"network": builtin, "env": "alpha", "confirm": true}); !strings.Contains(msg, builtin+" is one of the three networks every Docker host has") {
			t.Errorf("removing %s = %q", builtin, msg)
		}
	}
}

func TestNetworkDeleteThatGoesWrong(t *testing.T) {
	t.Parallel()

	call := "DELETE /api/networks/" + spareID
	dock := withNetworks(t)
	suite := session(t, dock, everything)
	args := map[string]any{"network": "spare", "confirm": true}

	dock.Fail(call, http.StatusInternalServerError, "Failed to remove network")
	if msg := suite.CallErr(t, "network_delete", args); !strings.Contains(msg, "the network spare was not removed") || !strings.Contains(msg, "Failed to remove network") {
		t.Errorf("a removal Dockhand fails = %q", msg)
	}
	dock.Answer(call, `{"success":false}`)
	if msg := suite.CallErr(t, "network_delete", args); !strings.Contains(msg, "without its being said to have succeeded") {
		t.Errorf("answered without success = %q", msg)
	}
	dock.Answer(call, `{"success":true}`)
	if msg := suite.CallErr(t, "network_delete", args); !strings.Contains(msg, "answered as done, and it is still listed") {
		t.Errorf("a removal that did not take = %q", msg)
	}
	dock.Then(call, func(dockhandtest.Request) {
		dock.FailWhen("GET /api/networks", "env", "1", http.StatusInternalServerError, "Failed to list networks")
	})
	if out := suite.Call(t, "network_delete", args); !acctest.IsBool(out["removed"], false) || !strings.Contains(acctest.Str(out["note"]), "it was removed, and the networks could not be listed afterwards") {
		t.Errorf("removed and not read back = %v", out)
	}
}

// stoppedStack is the canned alpha with the web stack's two containers stopped: who is then on its network, and who a prune would strand.
func stoppedStack(dock *dockhandtest.Server) {
	stopped := strings.Replace(alphaContainers, `"state":"running","status":"Up 2 hours (healthy)","health":"healthy"`, `"state":"exited","status":"Exited (0) 5 minutes ago","health":"healthy"`, 1)
	stopped = strings.Replace(stopped, `"state":"running","status":"Up 2 hours (unhealthy)","health":"unhealthy"`, `"state":"exited","status":"Exited (0) 5 minutes ago","health":"unhealthy"`, 1)
	dock.AnswerWhen("GET /api/containers", "env", "1", stopped)
}

func TestNetworkPrune(t *testing.T) {
	t.Parallel()

	const call = "POST /api/prune/networks"
	dock := withNetworks(t)
	suite := session(t, dock, everything)

	// without confirm: the one network no running container is on, and nothing is asked of Dockhand
	out := suite.Call(t, "network_prune", map[string]any{"env": "alpha"})
	if strings.Join(acctest.Strs(t, out["would_go"], "would_go"), "|") != "spare" || out["would_strand"] != nil || len(acctest.Strs(t, out["removed"], "removed")) != 0 || !strings.Contains(acctest.Str(out["note"]), "call again with confirm") || len(dock.Requests(call)) != 0 {
		t.Errorf("without confirm = %v", out)
	}

	dock.Answer(call, `{"success":true,"result":{"NetworksDeleted":["spare"]}}`)
	out = suite.Call(t, "network_prune", map[string]any{"env": "alpha", "confirm": true})
	if strings.Join(acctest.Strs(t, out["removed"], "removed"), "|") != "spare" || out["would_go"] != nil || out["stranded"] != nil || out["note"] != nil || dock.Last(call).Query.Get("env") != "1" {
		t.Errorf("with confirm = %v", out)
	}
	dock.Answer(call, `{"success":true,"result":{"NetworksDeleted":null}}`)
	if out = suite.Call(t, "network_prune", map[string]any{"env": "alpha", "confirm": true}); len(acctest.Strs(t, out["removed"], "removed")) != 0 || !strings.Contains(acctest.Str(out["note"]), "nothing was removed") {
		t.Errorf("a pruning that took nothing = %v", out)
	}

	// a host where every network has a running container on it, or is Docker's own, is not asked to prune
	asked := len(dock.Requests(call))
	if out = suite.Call(t, "network_prune", map[string]any{"env": "beta", "confirm": true}); !strings.Contains(acctest.Str(out["note"]), "there is nothing to remove") || len(dock.Requests(call)) != asked {
		t.Errorf("nothing unused = %v", out)
	}
}

// Docker prunes a network that only stopped containers are on, and they then fail to start. So a prune that would do that is refused, with the
// containers named, until it is told to go on; and when it is, it says who was left stranded.
func TestNetworkPruneThatWouldStrand(t *testing.T) {
	t.Parallel()

	const call = "POST /api/prune/networks"
	dock := withNetworks(t)
	stoppedStack(dock)
	suite := session(t, dock, everything)

	out := suite.Call(t, "network_prune", map[string]any{"env": "alpha"})
	if strings.Join(acctest.Strs(t, out["would_go"], "would_go"), "|") != "spare|web_default" || strings.Join(acctest.Strs(t, out["would_strand"], "would_strand"), "|") != "web-back-1 (exited), on web_default|web-front-1 (exited), on web_default" || !strings.Contains(acctest.Str(out["note"]), "2 stopped containers are on networks this would take") {
		t.Errorf("without confirm = %v", out)
	}
	// confirmed, and not told to strand: refused, naming them, and Dockhand is not asked
	if msg := suite.CallErr(t, "network_prune", map[string]any{"env": "alpha", "confirm": true}); !strings.Contains(msg, "nothing was removed") || !strings.Contains(msg, "web-front-1 (exited), on web_default") || !strings.Contains(msg, "network_delete") || len(dock.Requests(call)) != 0 {
		t.Errorf("confirmed, and not told to strand = %q", msg)
	}
	// told to strand and not confirmed: still only said
	if out = suite.Call(t, "network_prune", map[string]any{"env": "alpha", "stranding": true}); len(dock.Requests(call)) != 0 || !strings.Contains(acctest.Str(out["note"]), "call again with confirm") || len(acctest.Strs(t, out["would_strand"], "would_strand")) != 2 {
		t.Errorf("told to strand, and not confirmed = %v", out)
	}

	dock.Answer(call, `{"success":true,"result":{"NetworksDeleted":["web_default","spare"]}}`)
	out = suite.Call(t, "network_prune", map[string]any{"env": "alpha", "stranding": true, "confirm": true})
	if strings.Join(acctest.Strs(t, out["removed"], "removed"), "|") != "spare|web_default" || strings.Join(acctest.Strs(t, out["stranded"], "stranded"), "|") != "web-back-1 (exited), on web_default|web-front-1 (exited), on web_default" || out["would_strand"] != nil || !strings.Contains(acctest.Str(out["note"]), "will not start as they are") {
		t.Errorf("told to strand, and confirmed = %v", out)
	}
}

func TestNetworkPruneThatGoesWrong(t *testing.T) {
	t.Parallel()

	const call = "POST /api/prune/networks"
	dock := withNetworks(t)
	suite := session(t, dock, everything)
	args := map[string]any{"env": "alpha", "confirm": true}

	if msg := suite.CallErr(t, "network_prune", map[string]any{"confirm": true}); !strings.Contains(msg, "env is required") {
		t.Errorf("no host = %q", msg)
	}
	dock.Fail(call, http.StatusInternalServerError, "Failed to prune networks")
	if msg := suite.CallErr(t, "network_prune", args); !strings.Contains(msg, "the networks of alpha were not pruned") {
		t.Errorf("a pruning Dockhand fails = %q", msg)
	}
	dock.Answer(call, `{"success":true}`)
	if msg := suite.CallErr(t, "network_prune", args); !strings.Contains(msg, "without its being said to have succeeded") {
		t.Errorf("answered with no report = %q", msg)
	}
	// who is on the networks cannot be said, so nothing is removed
	pruned := len(dock.Requests(call))
	dock.FailWhen("GET /api/containers", "env", "1", http.StatusInternalServerError, "Failed to list containers")
	if msg := suite.CallErr(t, "network_prune", args); !strings.Contains(msg, "the networks of alpha could not be read, so nothing was removed") || len(dock.Requests(call)) != pruned {
		t.Errorf("a host whose containers cannot be read = %q", msg)
	}
}
