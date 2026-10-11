package tools

import (
	"net/http"
	"strings"
	"testing"

	"github.com/katbyte/go-kt/mcp/acctest"
)

// What a 1.0.51 server answers of a host's disk, with invented names: three images of which one is used, one has no name left and two share a layer;
// a running container and a stopped one; a volume in use, one that is not and one Docker cannot size; and build cache, a piece in use, one not, and a
// shared piece that is counted with what it is shared from.
const hostDisk = `{"diskUsage":{"LayersSize":900000000,
	"Images":[
		{"Id":"sha256:aaaaaaaaaaaa1111111111111111111111111111111111111111111111111111","RepoTags":["registry.example/front:1.2","registry.example/front:latest"],"Size":500000000,"SharedSize":100000000,"Containers":2,"Created":1727386302},
		{"Id":"sha256:bbbbbbbbbbbb2222222222222222222222222222222222222222222222222222","RepoTags":["registry.example/tool:old"],"Size":400000000,"SharedSize":100000000,"Containers":0,"Created":1727386302},
		{"Id":"sha256:cccccccccccc3333333333333333333333333333333333333333333333333333","RepoTags":["<none>:<none>"],"Size":100000000,"SharedSize":-1,"Containers":0,"Created":1727386302}],
	"Containers":[
		{"Id":"1111111111112222222222222222222222222222222222222222222222222222","Names":["/web-front-1"],"Image":"registry.example/front:1.2","SizeRw":20000000,"SizeRootFs":520000000,"State":"running","Status":"Up 2 hours"},
		{"Id":"3333333333334444444444444444444444444444444444444444444444444444","Names":["/crashed"],"Image":"registry.example/front:1.2","SizeRw":70000000,"SizeRootFs":570000000,"State":"exited","Status":"Exited (1) 2 hours ago"},
		{"Id":"5555555555556666666666666666666666666666666666666666666666666666","Names":[],"Image":"registry.example/front:1.2","State":"created","Status":"Created"}],
	"Volumes":[
		{"Name":"web_data","UsageData":{"RefCount":1,"Size":300000000}},
		{"Name":"loose-volume","UsageData":{"RefCount":0,"Size":50000000}},
		{"Name":"elsewhere","UsageData":{"RefCount":0,"Size":-1}},
		{"Name":"unmeasured"}],
	"BuildCache":[
		{"ID":"cache-one","Type":"regular","Description":"RUN make all","InUse":true,"Shared":false,"Size":40000000,"LastUsedAt":"2026-10-10T10:00:00Z"},
		{"ID":"cache-two","Type":"source.local","Description":"","InUse":false,"Shared":false,"Size":10000000},
		{"ID":"cache-three","Type":"regular","Description":"shared with another","InUse":false,"Shared":true,"Size":999000000}]}}`

func TestEnvDisk(t *testing.T) {
	t.Parallel()

	const disk = "GET /api/system/disk"
	dock := twoHosts(t)
	dock.AnswerWhen(disk, "env", "1", hostDisk)
	dock.AnswerWhen(disk, "env", "2", `{"diskUsage":{"LayersSize":0,"Images":[],"Containers":[],"Volumes":[],"BuildCache":[]}}`)
	suite := session(t, dock, Options{})

	out := suite.Call(t, "env_disk", map[string]any{"env": "alpha"})
	images, containers, volumes, cache := acctest.Object(t, out["images"], "images"), acctest.Object(t, out["containers"], "containers"), acctest.Object(t, out["volumes"], "volumes"), acctest.Object(t, out["build_cache"], "build_cache")
	// the images take what Docker says they take, a shared layer once, and the unused give back what nothing shares with them
	if acctest.Num(t, images["count"], "count") != 3 || acctest.Decimal(t, images["mb"], "mb") != 900 || acctest.Num(t, images["unused"], "unused") != 2 || acctest.Decimal(t, images["unused_mb"], "unused_mb") != 400 {
		t.Errorf("the images = %v", images)
	}
	if biggest := acctest.Rows(t, images["biggest"], "biggest"); len(biggest) != 3 || biggest[0]["name"] != "registry.example/front:1.2" || acctest.Decimal(t, biggest[0]["mb"], "mb") != 500 || !acctest.IsBool(biggest[0]["in_use"], true) || biggest[1]["name"] != "registry.example/tool:old" || biggest[2]["name"] != "cccccccccccc" || !acctest.IsBool(biggest[2]["in_use"], false) {
		t.Errorf("the biggest images = %v", biggest)
	}
	// a container is what it has written, and one that is not running is unused; one with no name goes by its id
	if acctest.Num(t, containers["count"], "count") != 3 || acctest.Decimal(t, containers["mb"], "mb") != 90 || acctest.Num(t, containers["unused"], "unused") != 2 || acctest.Decimal(t, containers["unused_mb"], "unused_mb") != 70 {
		t.Errorf("the containers = %v", containers)
	}
	if biggest := acctest.Rows(t, containers["biggest"], "biggest"); biggest[0]["name"] != "crashed" || !acctest.IsBool(biggest[0]["in_use"], false) || biggest[1]["name"] != "web-front-1" || !acctest.IsBool(biggest[1]["in_use"], true) || biggest[2]["name"] != "555555555555" {
		t.Errorf("the biggest containers = %v", biggest)
	}
	// a volume Docker cannot size counts as one and takes nothing
	if acctest.Num(t, volumes["count"], "count") != 4 || acctest.Decimal(t, volumes["mb"], "mb") != 350 || acctest.Num(t, volumes["unused"], "unused") != 3 || acctest.Decimal(t, volumes["unused_mb"], "unused_mb") != 50 || acctest.Rows(t, volumes["biggest"], "biggest")[0]["name"] != "web_data" {
		t.Errorf("the volumes = %v", volumes)
	}
	// a shared piece of build cache is not counted twice, and one with nothing said of what made it goes by its kind
	if acctest.Num(t, cache["count"], "count") != 2 || acctest.Decimal(t, cache["mb"], "mb") != 50 || acctest.Decimal(t, cache["unused_mb"], "unused_mb") != 10 || acctest.Rows(t, cache["biggest"], "biggest")[0]["name"] != "RUN make all" || acctest.Rows(t, cache["biggest"], "biggest")[1]["name"] != "source.local" {
		t.Errorf("the build cache = %v", cache)
	}
	if out["env"] != "alpha" || acctest.Decimal(t, out["total_mb"], "total_mb") != 1390 || acctest.Decimal(t, out["unused_mb"], "unused_mb") != 530 || out["note"] != nil || dock.Last(disk).Query.Get("env") != "1" {
		t.Errorf("the whole of it = %v", out)
	}

	// only so many of the biggest are named, and a host with nothing says so
	out = suite.Call(t, "env_disk", map[string]any{"env": 1, "biggest": 1})
	if biggest := acctest.Rows(t, acctest.Object(t, out["volumes"], "volumes")["biggest"], "biggest"); len(biggest) != 1 || acctest.Num(t, acctest.Object(t, out["volumes"], "volumes")["count"], "count") != 4 {
		t.Errorf("the one biggest volume = %v", out["volumes"])
	}
	out = suite.Call(t, "env_disk", map[string]any{"env": "beta"})
	if acctest.Decimal(t, out["total_mb"], "total_mb") != 0 || len(acctest.Rows(t, acctest.Object(t, out["images"], "images")["biggest"], "biggest")) != 0 || !strings.Contains(acctest.Str(out["note"]), "holds nothing") {
		t.Errorf("a host with nothing on it = %v", out)
	}
}

func TestEnvDiskRefused(t *testing.T) {
	t.Parallel()

	const disk = "GET /api/system/disk"
	dock := twoHosts(t)
	suite := session(t, dock, Options{})

	for what, tc := range map[string]struct {
		args map[string]any
		want string
	}{
		"no environment":              {nil, "env is required"},
		"an environment that is none": {map[string]any{"env": "gamma"}, `no environment is named or numbered "gamma"`},
		"more of the biggest than 50": {map[string]any{"env": "alpha", "biggest": 51}, "biggest is 51"},
		"fewer than none":             {map[string]any{"env": "alpha", "biggest": -1}, "biggest is -1"},
	} {
		if msg := suite.CallErr(t, "env_disk", tc.args); !strings.Contains(msg, tc.want) {
			t.Errorf("%s = %q, want %q", what, msg, tc.want)
		}
	}
	if asked := dock.Requests(disk); len(asked) != 0 {
		t.Errorf("the disk was asked for %d times by calls that were refused", len(asked))
	}

	// a host that does not answer in time is given as no account at all, which is said
	dock.Answer(disk, `{"diskUsage":null}`)
	if msg := suite.CallErr(t, "env_disk", map[string]any{"env": "alpha"}); !strings.Contains(msg, "Dockhand gave no account of the disk of alpha") {
		t.Errorf("a host that does not answer = %q", msg)
	}
	dock.Fail(disk, http.StatusForbidden, "Permission denied")
	if msg := suite.CallErr(t, "env_disk", map[string]any{"env": "alpha"}); !strings.Contains(msg, "what the disk of alpha holds could not be read") || !strings.Contains(msg, "Permission denied") {
		t.Errorf("a disk the account may not see = %q", msg)
	}
}
