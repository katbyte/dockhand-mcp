package tools

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"slices"
	"strings"
	"sync"
	"testing"

	"github.com/katbyte/dockhand-mcp/sdk/dockhand"
	"github.com/katbyte/dockhand-mcp/sdk/dockhandtest"
	mcpregistry "github.com/katbyte/go-kt/mcp/registry"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// kindsOf is every tool's kind by name: read, write or delete.
func kindsOf(t *testing.T) map[string]string {
	t.Helper()

	described, err := Describe(Options{Toolsets: []string{mcpregistry.All}, EnableWrite: true, EnableDelete: true})
	if err != nil {
		t.Fatal(err)
	}
	kinds := map[string]string{}
	for _, ti := range described {
		kinds[ti.Name] = ti.Kind
	}

	return kinds
}

// A session gets the read tools alone until writes are enabled, the delete tools are held back until they are asked for as well, and what is
// registered is told sorted.
func TestRegisterAllKinds(t *testing.T) {
	t.Parallel()

	kinds := kindsOf(t)
	all := register(t, everything)
	writes := register(t, Options{EnableWrite: true})
	dflt := register(t, Options{})

	if len(all) != len(kinds) || len(dflt) == 0 {
		t.Fatalf("counts all=%d of %d, default=%d", len(all), len(kinds), len(dflt))
	}
	for _, name := range all {
		wantDefault, wantWrites := kinds[name] == "read", kinds[name] != "delete"
		if slices.Contains(dflt, name) != wantDefault {
			t.Errorf("%s (%s) registered with nothing enabled: %v", name, kinds[name], !wantDefault)
		}
		if slices.Contains(writes, name) != wantWrites {
			t.Errorf("%s (%s) registered with --enable-write alone: %v", name, kinds[name], !wantWrites)
		}
	}
	for _, name := range EssentialTools {
		if !slices.Contains(dflt, name) {
			t.Errorf("essential tool %s does not exist, or is no read", name)
		}
	}
	if !slices.IsSorted(dflt) {
		t.Error("registered names not sorted")
	}

	// a delete is a change: enabled without writes, it is refused rather than registering nothing without a word
	if msg := refused(t, Options{EnableDelete: true}); !strings.Contains(msg, "--enable-delete needs --enable-write") {
		t.Errorf("deletes enabled without writes = %q", msg)
	}
	if _, err := Describe(Options{EnableDelete: true}); err == nil {
		t.Error("Describe took deletes enabled without writes")
	}
}

// The surface is 16 tools: 8 that read, 7 that write and 1 that deletes. A tool added, merged, removed or moved between kinds changes these on
// purpose, and this is where that is said.
func TestSurfaceSize(t *testing.T) {
	t.Parallel()

	counts := map[string]int{}
	kinds := kindsOf(t)
	for _, kind := range kinds {
		counts[kind]++
	}
	if len(kinds) != 190 || counts["read"] != 73 || counts["write"] != 84 || counts["delete"] != 33 {
		t.Errorf("surface = %d tools: %v, want 190 with 73 read, 84 write, 33 delete", len(kinds), counts)
	}
}

// Every tool tells a client what it changes, without the client reading the text: a read tool says it changes nothing (unless it writes a file on
// this machine, which it does not deny), every write tool says whether it can take away what was there and whether a repeat changes more, from its
// own entry rather than its kind, and every delete is destructive.
func TestEveryWriteToolIsHinted(t *testing.T) {
	t.Parallel()

	kinds := kindsOf(t)
	seen := map[string]bool{}
	for _, tool := range listed(t, session(t, dockhandtest.New(t), everything)) {
		a := tool.Annotations
		if a == nil || a.DestructiveHint == nil {
			t.Errorf("%s has no annotations", tool.Name)

			continue
		}
		switch kinds[tool.Name] {
		case "read":
			if a.ReadOnlyHint == toolHints[tool.Name].WritesHere || *a.DestructiveHint {
				t.Errorf("%s: read-only %v, destructive %v", tool.Name, a.ReadOnlyHint, *a.DestructiveHint)
			}
		case "write":
			seen[tool.Name] = true
			h, ok := toolHints[tool.Name]
			if !ok {
				t.Errorf("%s is a write tool with no hints of its own", tool.Name)

				continue
			}
			if a.ReadOnlyHint || *a.DestructiveHint == h.Additive || a.IdempotentHint != h.Idempotent || h.WritesHere {
				t.Errorf("%s: read-only %v, destructive %v, idempotent %v; want %+v", tool.Name, a.ReadOnlyHint, *a.DestructiveHint, a.IdempotentHint, h)
			}
		case "delete":
			if a.ReadOnlyHint || !*a.DestructiveHint {
				t.Errorf("%s deletes: read-only %v, destructive %v", tool.Name, a.ReadOnlyHint, *a.DestructiveHint)
			}
		default:
			t.Errorf("%s has no kind", tool.Name)
		}
	}
	for name, h := range toolHints {
		switch {
		case h.WritesHere:
			if kinds[name] != "read" {
				t.Errorf("%s is a %s tool; it changes nothing on a server", name, kinds[name])
			}
		case !seen[name]:
			t.Errorf("toolHints names %s, which is no write tool", name)
		default:
		}
	}
}

func TestRegisterAllFilters(t *testing.T) {
	t.Parallel()

	got := register(t, Options{Allow: []string{mcpregistry.Essential}})
	want := slices.Clone(EssentialTools)
	slices.Sort(want)
	if !slices.Equal(got, want) {
		t.Errorf("essential = %v", got)
	}

	got = register(t, Options{Allow: []string{"env_*,server_info"}, Deny: []string{"*_info"}})
	if !slices.Equal(got, []string{"env_disk", "env_get", "env_list", "env_test"}) {
		t.Errorf("env_* and server_info without *_info = %v", got)
	}

	// beside toolsets an allow list adds its tools to them, from whatever set they are in, and a deny list is what narrows
	got = register(t, Options{EnableWrite: true, Toolsets: []string{"env"}, Allow: []string{"stack_restart"}, Deny: []string{"container_logs"}})
	if !slices.Contains(got, "stack_restart") || !slices.Contains(got, "env_list") || !slices.Contains(got, "server_info") || slices.Contains(got, "container_logs") || slices.Contains(got, "stack_stop") {
		t.Errorf("the env family with stack_restart and without container_logs = %v, want the family, core and the one tool named", got)
	}
	// an allow list alone is the whole choice: core does not come with it
	if got = register(t, Options{Allow: []string{"env_list"}}); !slices.Equal(got, []string{"env_list"}) {
		t.Errorf("env_list alone = %v", got)
	}
	// and it lets nothing past the switch that enables writes
	if got = register(t, Options{Allow: []string{"container_*"}}); slices.Contains(got, "container_restart") || !slices.Contains(got, "container_list") {
		t.Errorf("container_* with writes not enabled = %v", got)
	}

	if msg := refused(t, Options{Allow: []string{"bogus_*"}}); !strings.Contains(msg, `"bogus_*" matches no tool`) {
		t.Errorf("an allow pattern that matches nothing = %q", msg)
	}
	if msg := refused(t, Options{Deny: []string{"nope"}}); !strings.Contains(msg, `"nope" matches no tool`) {
		t.Errorf("a deny pattern that matches nothing = %q", msg)
	}
}

// Every tool belongs to exactly one toolset, every toolset names only real tools and has its place in the order a listing shows them, and core comes
// along with whatever else is asked for.
func TestToolsetsPartition(t *testing.T) {
	t.Parallel()

	all := register(t, everything)
	seen := map[string]string{}
	for set, members := range Toolsets {
		if !slices.Contains(ToolsetOrder, set) {
			t.Errorf("toolset %s has no place in ToolsetOrder, so a listing leaves its tools out", set)
		}
		for _, m := range members {
			if !slices.Contains(all, m) {
				t.Errorf("toolset %s names %s, which is not a tool", set, m)
			}
			if prev, dup := seen[m]; dup {
				t.Errorf("%s is in both %s and %s", m, prev, set)
			}
			seen[m] = set
		}
	}
	for _, name := range all {
		if seen[name] == "" {
			t.Errorf("%s belongs to no toolset", name)
		}
	}
	for _, set := range ToolsetOrder {
		if _, ok := Toolsets[set]; !ok {
			t.Errorf("ToolsetOrder names %s, which is no toolset", set)
		}
	}

	// a resource family is every tool with that prefix, plus core
	got := register(t, Options{Toolsets: []string{"env"}})
	for _, name := range got {
		if !strings.HasPrefix(name, "env_") && seen[name] != mcpregistry.Core {
			t.Errorf("%s registered for the env family", name)
		}
	}
	if !slices.Contains(got, "env_list") || !slices.Contains(got, "server_info") {
		t.Errorf("the env family = %v, want its tools and core's", got)
	}

	// all is everything the kind gates allow
	if got = register(t, Options{Toolsets: []string{mcpregistry.All}, EnableWrite: true}); len(got) != len(register(t, Options{EnableWrite: true})) {
		t.Errorf("all registered %d tools, want %d", len(got), len(register(t, Options{EnableWrite: true})))
	}

	if msg := refused(t, Options{Toolsets: []string{"nope"}}); !strings.Contains(msg, mcpregistry.Core) || !strings.Contains(msg, "env") {
		t.Errorf("the error should name the sets and families: %v", msg)
	}
}

// A session reads the tools it loaded and follows where they point, so a tool names only tools that load with it: its own set's, or core's, which
// comes with every set. A core tool loads on its own by default, so it names only core tools.
func TestToolsetsNameOnlyWhatTheyLoad(t *testing.T) {
	t.Parallel()

	all := register(t, everything)
	set := map[string]string{}
	for name, members := range Toolsets {
		for _, m := range members {
			set[m] = name
		}
	}
	word := regexp.MustCompile(`[a-z]+(?:_[a-z]+)+`)
	for _, tool := range listed(t, session(t, dockhandtest.New(t), everything)) {
		// the description and the input and output schemas' own descriptions
		raw, err := json.Marshal(tool)
		if err != nil {
			t.Fatal(err)
		}
		for _, ref := range word.FindAllString(string(raw), -1) {
			if ref == tool.Name || !slices.Contains(all, ref) {
				continue
			}
			if set[ref] != mcpregistry.Core && set[ref] != set[tool.Name] {
				t.Errorf("%s (%s) names %s, which only %s loads", tool.Name, set[tool.Name], ref, set[ref])
			}
		}
	}
}

// Describe reports the same selection RegisterAll makes, with its kinds and sets, and needs no server.
func TestDescribe(t *testing.T) {
	t.Parallel()

	list, err := Describe(Options{Toolsets: []string{mcpregistry.Core}, EnableWrite: true, EnableDelete: true})
	if err != nil {
		t.Fatal(err)
	}
	names := make([]string, 0, len(list))
	for _, ti := range list {
		names = append(names, ti.Name)
		if ti.Toolset != mcpregistry.Core {
			t.Errorf("%s reported in %s", ti.Name, ti.Toolset)
		}
		if ti.Description == "" {
			t.Errorf("%s has no description", ti.Name)
		}
		if ti.Kind != "read" {
			t.Errorf("%s is a %s tool in core, which a session loads without asking", ti.Name, ti.Kind)
		}
	}
	if want := register(t, Options{Toolsets: []string{mcpregistry.Core}, EnableWrite: true, EnableDelete: true}); !slices.Equal(names, want) {
		t.Errorf("Describe = %v\nRegisterAll = %v", names, want)
	}
	if _, err := Describe(Options{Allow: []string{"nope"}}); err == nil {
		t.Error("Describe accepted a pattern that matches nothing")
	}

	if fam := FamilyNames(); !slices.Contains(fam, "server") || !slices.Contains(fam, "env") {
		t.Errorf("families = %v", fam)
	}
	if sets := ToolsetNames(); !slices.Contains(sets, mcpregistry.Core) || !slices.IsSorted(sets) {
		t.Errorf("toolset names = %v", sets)
	}
}

// A handler that panics answers its call with an error naming the tool and logs the stack, rather than ending the session: the next call is served.
func TestAPanicInAToolIsAnErrorNotACrash(t *testing.T) {
	t.Parallel()

	var (
		mu     sync.Mutex
		logged strings.Builder
	)
	r := &registry{dock: unreachable(t), errorLog: func(format string, args ...any) {
		mu.Lock()
		defer mu.Unlock()
		_, _ = fmt.Fprintf(&logged, format, args...)
	}}
	type none struct{}
	add(r, writeTool, &mcp.Tool{Name: "zzyzx_panic"}, func(context.Context, *mcp.CallToolRequest, none) (*mcp.CallToolResult, none, error) {
		var found map[string]*dockhand.GetEnvironmentsResponse

		return nil, none{}, errors.New(found["9"].Name) // a nil dereference
	})
	add(r, deleteTool, &mcp.Tool{Name: "zzyzx_fine"}, func(context.Context, *mcp.CallToolRequest, none) (*mcp.CallToolResult, none, error) {
		return nil, none{}, nil
	})
	suite := hostRegistry(t, r)

	msg := suite.CallErr(t, "zzyzx_panic", nil)
	if !strings.Contains(msg, "internal error in zzyzx_panic") || !strings.Contains(msg, "nil pointer") {
		t.Errorf("the refusal = %q, want the tool and the panic named", msg)
	}
	mu.Lock()
	log := logged.String()
	mu.Unlock()
	if !strings.Contains(log, "internal error in zzyzx_panic") || !strings.Contains(log, "goroutine") {
		t.Errorf("the log = %q, want the panic and its stack", log)
	}
	suite.Call(t, "zzyzx_fine", nil)
}
