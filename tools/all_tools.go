// Package tools defines the MCP tools exposed by dockhand-mcp, split by the resource they act on (server.go, env.go, ...). Tools are named
// resource-first (server_*, env_*, container_*, stack_*, audit_*) so they group by what they act on.
//
// One process works on one Dockhand, and a Dockhand on several Docker hosts, its environments. A tool that is about a host takes it as its env
// argument, by name or number: a read asks every environment unless one is named and sets the answers side by side, and a change is made on one named
// environment at a time.
//
// Every tool is registered through add with a kind: read tools never change what Dockhand or a host holds, write tools do and are only registered
// with --enable-write, and delete tools remove what cannot be put back and are only registered with --enable-delete as well. A session reads until it
// is told it may do more: these are the machines things run on, and a wrong change stops them. --toolsets picks the groups a session needs, and
// --allow-tools / --deny-tools narrow the set further.
package tools

import (
	"errors"
	"time"

	"github.com/katbyte/dockhand-mcp/sdk/dockhand"
	mcpregistry "github.com/katbyte/go-kt/mcp/registry"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// Options controls which tools are registered.
type Options struct {
	// EnableWrite registers the tools that change something. Off unless the operator opts in: with nothing said, a session can only read.
	EnableWrite bool
	// EnableDelete registers the tools that delete. A delete is a change, so it needs EnableWrite too, and is refused without it rather than quietly
	// registering nothing.
	EnableDelete bool
	// Toolsets, when set, restricts registration to the named groups (see Toolsets). "core" is always included, so a set can be asked for on its own.
	// Allow adds to them and Deny takes away.
	Toolsets []string
	// Allow names tools to register: exact names, prefix/suffix globs (container_*, *_delete) or the "essential" preset. Beside Toolsets it adds
	// those tools to the sets, wherever they belong: the sets plus these. With no Toolsets it is the whole choice, and only the tools it names are
	// registered, core not among them unless named. A name or pattern that matches no tool is refused rather than registering nothing.
	Allow []string
	// Deny removes matching tools from whatever the two above chose: it is how a set is narrowed.
	Deny []string

	// poll is how often a job Dockhand runs in the background is asked how it goes, for a test that will not wait; zero is jobPoll.
	poll time.Duration
}

// Toolsets group the tools by the job someone is doing, so a client can load a working subset instead of all of them: every tool's definition (name,
// description, input schema) is spent from a client's context before anyone has asked a question.
//
// "all" is every tool, which is what the library does when no toolset is asked for; the dockhand-mcp binary defaults to core instead (see
// cli.FlagData.ToolOptions).
//
// --toolsets also takes a resource family - server, env, container, stack - which is every tool with that prefix. Those are derived from the
// registered names rather than listed here, so they cannot go stale.
//
// Every tool belongs to exactly one set (TestToolsetsPartition proves it), and core is added to whatever else is asked for, because none of the other
// sets can say which hosts there are on their own.
var Toolsets = map[string][]string{
	// enough to see what the Dockhand is, which hosts it manages and what runs on each: the base every other set assumes. job_get is here because
	// slow work in any set is left running as a job, and whichever set started one has to be able to follow it
	mcpregistry.Core: {
		"server_info", "env_list", "container_list", "container_get", "container_logs", "stack_list", "stack_logs", "job_get",
	},
	// start, stop and restart what is there, and ask work left running to stop: every tool here needs --enable-write
	"manage": {
		"container_start", "container_stop", "container_restart", "stack_start", "stack_stop", "stack_restart", "job_cancel",
	},
	// one container looked into, made and changed: what it is using and takes on disk, what runs in it, how it would be written as compose, and what
	// the containers did and when; with --enable-write make one, change what one was made with or its limits, hold it still, rename it and run a
	// command in it; and with --enable-delete remove it, or every one on a host that is not running, or Dockhand's record of what containers did
	"containers": {
		"container_stats", "container_sizes", "container_top", "container_compose", "container_events",
		"container_create", "container_edit", "container_limits", "container_pause", "container_unpause", "container_rename", "container_exec", "container_delete", "container_prune", "container_events_clear",
	},
	// the files inside a container: list a directory, read a file and fetch a file or a directory out; with --enable-write write a file, send files
	// in, make a directory, move, and set permissions and owner; and with --enable-delete remove
	"files": {
		"container_files", "container_read", "container_file_download",
		"container_file_write", "container_file_upload", "container_file_mkdir", "container_file_move", "container_file_perms", "container_file_delete",
	},
	// keeping containers on current images: which have something newer to be had, and which Dockhand updates by itself; with --enable-write update
	// one, set or stop its schedule, and clear what was noted
	"updates": {
		"container_updates", "container_autoupdate",
		"container_update", "container_autoupdate_set", "container_updates_clear",
	},
	// a compose stack as the file it is: what Dockhand keeps for it, whether the file is sound, and each time it was brought up and what that
	// printed; with --enable-write make one, change its file and variables, bring it up and take it down, and give Dockhand the file of one it only
	// found or move a stack's files elsewhere; and with --enable-delete remove one, or the record of one of its deploys. And where the files lie:
	// Dockhand's own filesystem listed and read, and with --enable-write a folder made on it
	"stacks": {
		"stack_get", "stack_validate", "stack_deploys", "stack_scan", "server_files", "server_read",
		"stack_create", "stack_edit", "stack_deploy", "stack_down", "stack_adopt", "stack_move", "server_mkdir", "stack_delete", "stack_deploys_delete",
	},
	// what a host holds besides its containers, its images, volumes and networks: what is there and what uses it, and what all of it takes of the
	// host's disk; with --enable-write pull, tag, make and connect, and with --enable-delete remove and prune. One kind alone is its family: image,
	// volume or network
	"resources": {
		"env_disk",
		"image_list", "image_get", "image_export", "image_pull", "image_tag", "image_load", "image_delete", "image_prune",
		"volume_list", "volume_get", "volume_files", "volume_read", "volume_export", "volume_create", "volume_clone", "volume_delete", "volume_prune",
		"network_list", "network_get", "network_create", "network_connect", "network_disconnect", "network_delete", "network_prune",
	},
	// the hosts themselves: one in full, the machine, its Docker and what Dockhand does for it by itself, trying a connection, and the agents that
	// call in with their tokens; with --enable-write add a host, change how one is kept and reached, set its schedules and make an agent's token; and
	// with --enable-delete forget a host or revoke a token
	"setup": {
		"env_get", "env_test", "agent_list", "env_create", "env_edit", "env_schedules", "agent_token_create", "env_delete", "agent_token_delete",
	},
	// what Dockhand does by itself: every schedule it keeps and how each run of one went, with what it logged; with --enable-write run one before its
	// time and switch one off or on; and with --enable-delete remove one, or the record of a run
	"schedules": {
		"schedule_list", "schedule_history",
		"schedule_run", "schedule_set", "schedule_delete", "schedule_history_delete",
	},
	// the image registries Dockhand keeps and what is in them: which there are, whether one can be reached and signed in to, the images one holds and
	// their tags; with --enable-write save and change one and push an image to one; and with --enable-delete forget one, or delete a tag from one
	"registries": {
		"registry_list", "registry_test", "registry_images", "registry_tags",
		"registry_create", "registry_edit", "image_push", "registry_delete", "registry_image_delete",
	},
	// how Dockhand tells people what happened: the channels it keeps, where each sends and which hosts' events it is sent; with --enable-write save
	// and change one, tie one to a host, and send a test; and with --enable-delete untie or forget one
	"notifications": {
		"notification_list",
		"notification_test", "notification_create", "notification_edit", "notification_link", "notification_link_delete", "notification_delete",
	},
	// the known vulnerabilities in the images of the hosts: how each host is scanned, what the scans found and one image's own scan; with
	// --enable-write set a host's scanner and scan an image or every one; and with --enable-delete take the scanners or their databases away
	"vulnerabilities": {
		"scanner_get", "vulnerability_list", "vulnerability_export", "image_scan_get",
		"scanner_set", "image_scan", "image_scan_all", "scanner_delete", "scanner_cache_clear",
	},
	// who may sign in to the Dockhand and how: its users, whether its login is on at all, the single sign-on providers it keeps and the API tokens of
	// the user this server signs in as; with --enable-write make and change a user, switch the login on or off, and save and change a provider; and
	// with --enable-delete remove a user or a provider
	"users": {
		"user_list", "login_get", "token_list", "sso_list",
		"user_create", "user_edit", "login_set", "sso_create", "sso_edit", "user_delete", "sso_delete",
	},
	// the settings of the Dockhand itself: what it keeps and for how long, when its own jobs run, where it looks for stacks, and which newer versions
	// of an image it notes; what its web interface remembers, the orders, columns and pages of it and the favourites of its log view; and Dockhand as
	// a product, whether a newer version of it is out, its licence and what it is built from; with --enable-write change the settings
	"settings": {
		"settings_get", "preferences_get", "favorite_list", "server_update_check", "server_about",
		"settings_set", "preferences_set", "favorite_set",
	},
	// the secret providers Dockhand keeps, the stores outside it that a stack's secrets are loaded from when it is deployed: which there are, whether
	// one can be reached, and the names of what one holds at a path; with --enable-write save and change one, and with --enable-delete remove one
	"secrets": {
		"secret_provider_list", "secret_provider_test", "secret_provider_keys",
		"secret_provider_create", "secret_provider_edit", "secret_provider_delete",
	},
	// stacks deployed from git: the credentials Dockhand signs in to a git server with, the repositories it keeps, and the git stacks made from them,
	// each with the commit it runs from and whether its branch has a newer one; with --enable-write save and change a credential and a repository,
	// make a git stack, change how one is kept, fetch its repository and deploy it; and with --enable-delete remove a credential or a repository, or
	// have Dockhand forget that a stack comes from git
	"git": {
		"git_credential_list", "git_repository_list", "git_repository_test", "git_repository_variables", "git_stack_list", "git_stack_get",
		"git_credential_create", "git_credential_edit", "git_repository_create", "git_repository_edit", "git_stack_create", "git_stack_edit", "git_stack_sync", "git_stack_deploy",
		"git_credential_delete", "git_repository_delete", "git_stack_delete",
	},
	// ready-made ways to run something: the templates Dockhand has for well-known applications, one in full with the compose file a stack is made
	// from, and the sources they come from, and the config sets it keeps, bundles of settings to make containers alike; with --enable-write add a
	// source and switch one off or on, and save and change a config set, and with --enable-delete remove either
	"templates": {
		"template_list", "template_get", "template_source_list", "config_set_list",
		"template_source_create", "template_source_edit", "config_set_create", "config_set_edit", "template_source_delete", "config_set_delete",
	},
	// how Dockhand's web interface sorts, filters and draws things: its tags, with the containers and the stacks each is on, the labels of its
	// environments, and the icons that containers, stacks and environments were given; with --enable-write make and change a tag, set the tags of a
	// container or a stack, rename and colour a label and say which environments have it, and give an icon; and with --enable-delete remove a tag, a
	// label or an icon
	"tags": {
		"tag_list", "label_list", "icon_list", "icon_search", "icon_download",
		"tag_create", "tag_edit", "tag_assign", "label_edit", "label_assign", "icon_set", "tag_delete", "label_delete", "icon_delete",
	},
}

// ToolsetOrder is the order a listing shows the sets in: the base first.
var ToolsetOrder = []string{mcpregistry.Core, "manage", "containers", "files", "updates", "stacks", "resources", "setup", "schedules", "registries", "notifications", "vulnerabilities", "users", "settings", "secrets", "git", "templates", "tags"}

// EssentialTools is the curated preset selected by --allow-tools essential: enough to see what the Dockhand is, which hosts it manages, what runs on
// them and what a container has written.
var EssentialTools = []string{
	"server_info",
	"env_list",
	"container_list",
	"container_logs",
}

// The kinds a tool is added with, in go-kt's registry's terms: a read tool never changes what Dockhand or a host holds, a write tool does, and a
// delete tool removes what cannot be put back.
const (
	readTool   = mcpregistry.Read
	writeTool  = mcpregistry.Write
	deleteTool = mcpregistry.Delete
)

// registry is this application's tools and what they share. Which of them a session gets, and what each tells a client about itself, is go-kt's
// registry's work, so the allow and deny patterns are checked against every tool before any is registered.
type registry struct {
	// dock is the client for the one Dockhand this process works on
	dock *dockhand.Client
	opts Options
	// tools holds every tool queued, made when the first is (see queued)
	tools *mcpregistry.Registry
	// errorLog is where a handler's panic is logged; nil is the process's log
	errorLog func(format string, args ...any)
}

// toolHints are what a tool tells a client about itself beyond its kind, in the MCP annotations' terms. A write tool is destructive when it can
// change or take away what was there and additive when it only ever adds; idempotent when calling it again with the same arguments changes nothing
// more.
//
// Every write tool has an entry, each decided on its own rather than taken from the kind. A write tool missing from here is marked destructive, and
// fails TestEveryWriteToolIsHinted.
var toolHints = map[string]mcpregistry.Hints{
	// what was stopped runs again, and nothing that was there is taken away; one that runs is left as it is
	"container_start": {Additive: true, Idempotent: true},
	"stack_start":     {Additive: true, Idempotent: true},
	// what ran is stopped, which is what it is for and takes a service away; one that is stopped is left as it is
	"container_stop": {Idempotent: true},
	"stack_stop":     {Idempotent: true},
	// what ran is stopped and started, each time it is asked
	"container_restart": {},
	"stack_restart":     {},
	// a host is added, and nothing that was there is changed; a second by the same name is refused
	"env_create": {Additive: true},
	// what Dockhand keeps for a host is set over what it kept, and nothing on the host is touched; given again it changes nothing
	"env_edit":      {Idempotent: true},
	"env_schedules": {Idempotent: true},
	// a token is made, and the one the environment had stops working and its agent is cut off: each call makes another
	"agent_token_create": {},
	// how a host is scanned is set over how it was; given again it changes nothing
	"scanner_set": {Idempotent: true},
	// a user is added, and nothing that was there is changed; a second by the same name is refused
	"user_create": {Additive: true},
	// what Dockhand keeps for a user is set over what it kept, which can stop the user signing in; given again it changes nothing
	"user_edit": {Idempotent: true},
	// whether anyone is asked who they are is set over how it was, which shuts people out or lets everyone in; given again it changes nothing
	"login_set": {Idempotent: true},
	// a provider is added, and nothing that was there is changed; a second by the same name is refused
	"sso_create": {Additive: true},
	// what Dockhand keeps for a provider is set over what it kept; given again it changes nothing
	"sso_edit": {Idempotent: true},
	// a source is added, which Dockhand fetches at once from the address given, and nothing that was there is changed; a second by the same name or
	// address is refused
	"template_source_create": {Additive: true},
	// what Dockhand keeps for a source is set over what it kept, which takes its templates out of the list or puts them in; given again it changes
	// nothing
	"template_source_edit": {Idempotent: true},
	// a tag is added, and nothing that was there is changed; a second by the same name is refused
	"tag_create": {Additive: true},
	// what Dockhand keeps for a tag is set over what it kept; given again it changes nothing
	"tag_edit": {Idempotent: true},
	// the tags of one container or stack are set over the ones it had, which is a note Dockhand keeps and nothing on a Docker host; given again it
	// changes nothing
	"tag_assign": {Idempotent: true},
	// a label is renamed on every environment that has it and its colour set over the one it had, which can make two labels one; given again it
	// changes nothing
	"label_edit": {Idempotent: true},
	// a label is given to environments and taken from others, which is a note Dockhand keeps and nothing on a Docker host; given again it changes
	// nothing
	"label_assign": {Idempotent: true},
	// a config set is added, and nothing that was there is changed; a second by the same name is refused
	"config_set_create": {Additive: true},
	// what Dockhand keeps for a config set is set over what it kept, which changes no container; given again it changes nothing
	"config_set_edit": {Idempotent: true},
	// an icon is set over the one a container, a stack or an environment had, which is a note Dockhand keeps and nothing on a Docker host; given
	// again it changes nothing
	"icon_set": {Idempotent: true},
	// each setting named is set over what it was, and a smaller retention has the next clean-up remove more; given again it changes nothing
	"settings_set": {Idempotent: true},
	// a provider is added, and nothing that was there is changed; a second by the same name is refused
	"secret_provider_create": {Additive: true},
	// what Dockhand keeps for a provider is set over what it kept, which the stacks bound to it go by at their next deploy; given again it changes
	// nothing
	"secret_provider_edit": {Idempotent: true},
	// a credential or a repository is added, and nothing that was there is changed; a second by the same name is refused
	"git_credential_create": {Additive: true},
	"git_repository_create": {Additive: true},
	// what Dockhand keeps for a credential or a repository is set over what it kept, which the git stacks that use it go by at their next fetch;
	// given again it changes nothing
	"git_credential_edit": {Idempotent: true},
	"git_repository_edit": {Idempotent: true},
	// a stack is added and brought up, and nothing that was there is changed; a name that is taken is refused
	"git_stack_create": {Additive: true},
	// how Dockhand keeps a git stack is set over how it kept it, and nothing running is touched; given again it changes nothing
	"git_stack_edit": {Idempotent: true},
	// Dockhand's own copy of the repository is brought to the newest commit, and nothing running is touched; fetched again it changes nothing
	"git_stack_sync": {Idempotent: true},
	// the stack's files are replaced with the repository's and containers whose service changed are replaced, each time it is asked
	"git_stack_deploy": {},
	// what the web interface remembers is set over what it remembered, which changes nothing on a Docker host; given again it changes nothing
	"preferences_set": {Idempotent: true},
	// the favourites of one environment's log view are set whole over the ones it had, which is a note Dockhand keeps; given again it changes nothing
	"favorite_set": {Idempotent: true},
	// a scan is added to the ones Dockhand keeps, the newest being what the findings are read from, and the scanner's image and database are added to
	// the host the first time; nothing that was there is changed, and each call adds another
	"image_scan":     {Additive: true},
	"image_scan_all": {Additive: true},
	// a message is sent for real to whoever reads the channel, and nothing Dockhand keeps is changed; each call sends another
	"notification_test": {Additive: true},
	// a channel is added, and nothing that was there is changed; a second by the same name is refused
	"notification_create": {Additive: true},
	// what Dockhand keeps for a channel, or for its tie to a host, is set over what it kept; given again it changes nothing
	"notification_edit": {Idempotent: true},
	"notification_link": {Idempotent: true},
	// a registry is added, and nothing that was there is changed; a second by the same name is refused
	"registry_create": {Additive: true},
	// what Dockhand keeps for a registry is set over what it kept; given again it changes nothing
	"registry_edit": {Idempotent: true},
	// an image is added to a registry, where it replaces what was under the same name and tag; pushed again it changes nothing
	"image_push": {Idempotent: true},
	// what a schedule would do in its time is done now, which for some of them changes a host: each call is another run
	"schedule_run": {},
	// a schedule is switched on or off and keeps everything else about it; one that stands that way is left as it is
	"schedule_set": {Idempotent: true},
	// a directory is added to Dockhand's own filesystem, and nothing that was there is changed; one that is there is left as it is
	"server_mkdir": {Additive: true, Idempotent: true},
	// work under way is asked to stop, and what it had not yet done is not done; one that has ended is left as it is
	"job_cancel": {Idempotent: true},
	// an image is added to the host, and one already there under the name loses the name and nothing else; the newest pulled again changes nothing
	"image_pull": {Additive: true, Idempotent: true},
	// an image gains a name, and keeps the ones it had; given again it changes nothing
	"image_tag": {Additive: true, Idempotent: true},
	// an image is added to the host from an archive, and one already there under the name loses the name and nothing else; loaded again it changes
	// nothing
	"image_load": {Additive: true, Idempotent: true},
	// a volume or a network is added, and nothing that was there is changed; a second by the same name is refused
	"volume_create":  {Additive: true},
	"network_create": {Additive: true},
	// a volume is copied into a new one, and the one copied is untouched; a name that is taken is refused
	"volume_clone": {Additive: true},
	// nothing in the volume is changed: the host is made to run a helper to look inside it, and the first time to pull the helper's image, which is
	// why these two are not reads. Asked again they find the same
	"volume_files": {Additive: true, Idempotent: true},
	"volume_read":  {Additive: true, Idempotent: true},
	// as the two above: a helper is run to read the volume, and what it reads is written to a new file on this machine
	"volume_export": {Additive: true, Idempotent: true},
	// a container gains a network, and keeps the ones it had; one already on it is left as it is
	"network_connect": {Additive: true, Idempotent: true},
	// a container loses a network, and what reached it there no longer does; one that is not on it is left as it is
	"network_disconnect": {Idempotent: true},
	// a container's processes are held still, and nothing is lost; one that is held is left as it is
	"container_pause": {Idempotent: true},
	// what was held goes on, and nothing that was there is taken away; one that is not held is left as it is
	"container_unpause": {Additive: true, Idempotent: true},
	// a container is called something else, and what knew it by its old name no longer finds it; given the name it has it changes nothing
	"container_rename": {Idempotent: true},
	// a command runs inside a container with everything the container can reach, each time it is asked
	"container_exec": {},
	// a container is added, and nothing that was there is changed; a name that is taken is refused
	"container_create": {Additive: true},
	// a container is replaced by one made with the change, and what it wrote inside itself is gone; given again it finds it so and does nothing
	"container_edit": {Idempotent: true},
	// a container's limits are set over the ones it had; given again it changes nothing
	"container_limits": {Idempotent: true},
	// an image is pulled, and when it is newer the container is replaced by one made from it; with recreate it is replaced each time it is asked
	"container_update": {},
	// Dockhand's schedule for a container is set over the one it had, or forgotten; given again it changes nothing
	"container_autoupdate_set": {Idempotent: true},
	// Dockhand forgets what it noted, and nothing on a host is touched; given again there is nothing to forget
	"container_updates_clear": {Idempotent: true},
	// a file is written, over one that was there when overwrite says so; given again it writes the same
	"container_file_write":  {Idempotent: true},
	"container_file_upload": {Idempotent: true},
	// a directory is added, and nothing that was there is changed; one that is there is left as it is
	"container_file_mkdir": {Additive: true, Idempotent: true},
	// a file is taken from where it was and put elsewhere, over one that was there when overwrite says so; asked again there is nothing left to move
	"container_file_move": {},
	// a file's permissions and owner are set over the ones it had; given again it changes nothing
	"container_file_perms": {Idempotent: true},
	// nothing in the container is changed: a file is written on the machine the tool runs on, and one that is there is never written over
	"container_file_download": {WritesHere: true},
	"icon_download":           {WritesHere: true},
	"image_export":            {WritesHere: true},
	// a stack is added, and nothing that was there is changed; a name that is taken is refused
	"stack_create": {Additive: true},
	// what Dockhand keeps for a stack is replaced with what is given; given again it changes nothing more
	"stack_edit": {Idempotent: true},
	// containers whose service changed are replaced, and with recreate every one is, each time it is asked
	"stack_deploy": {},
	// a stack's containers and networks are removed, and with remove_volumes what its volumes held; one that is down is left as it is
	"stack_down": {Idempotent: true},
	// Dockhand gains the file of a stack, and nothing running is touched; a name it has a file for is refused
	"stack_adopt": {Additive: true},
	// a stack's files are moved, and one of the same name where they go is replaced when overwrite says so; moved again to where they are changes
	// nothing
	"stack_move": {Idempotent: true},
}

// queued is every tool queued so far, in go-kt's registry, which is made when the first tool is.
func (r *registry) queued() *mcpregistry.Registry {
	if r.tools == nil {
		r.tools = mcpregistry.New(mcpregistry.Config{
			Toolsets:  Toolsets,
			Essential: EssentialTools,
			Hints:     toolHints,
			LogError:  r.errorLog,
		})
	}

	return r.tools
}

// add queues a typed tool for registration. go-kt's registry sets the MCP annotations from kind and the tool's hints so clients can tell read-only
// from destructive tools without parsing descriptions, turns a panic in the handler into an ordinary tool error, and sends empty collections as []
// rather than null: an AI client reading "containers": null cannot tell "none" from "not fetched", and Go leaves un-appended slices nil.
func add[In, Out any](r *registry, kind mcpregistry.Kind, t *mcp.Tool, h mcp.ToolHandlerFor[In, Out]) {
	mcpregistry.Add(r.queued(), kind, t, h)
}

// RegisterAll adds every tool permitted by opts to the MCP server and returns the names registered. It fails when an allow/deny pattern matches no
// tool, so a typo cannot silently hide one.
func RegisterAll(server *mcp.Server, dock *dockhand.Client, opts Options) ([]string, error) {
	if err := opts.check(); err != nil {
		return nil, err
	}
	r := &registry{dock: dock, opts: opts}
	queueTools(r)

	return r.queued().Register(server, opts.selection())
}

// check refuses options that contradict themselves.
func (o *Options) check() error {
	if o.EnableDelete && !o.EnableWrite {
		return errors.New("--enable-delete needs --enable-write (DOCKHAND_ENABLE_WRITE=true) beside it: a delete is a change, and no change is made without that")
	}

	return nil
}

// selection is the part of the options that chooses tools, as go-kt's registry takes it: its read-only is this application's default, which enabling
// writes lifts.
func (o *Options) selection() mcpregistry.Selection {
	return mcpregistry.Selection{
		ReadOnly:     !o.EnableWrite,
		EnableDelete: o.EnableDelete,
		Toolsets:     o.Toolsets,
		Allow:        o.Allow,
		Deny:         o.Deny,
	}
}

// queueTools queues every tool, before any filtering.
func queueTools(r *registry) {
	registerServerTools(r)
	registerServerFileTools(r)
	registerServerAboutTools(r)
	registerEnvironmentTools(r)
	registerEnvironmentWriteTools(r)
	registerEnvironmentHostTools(r)
	registerEnvironmentDiskTools(r)
	registerAgentTools(r)
	registerScheduleTools(r)
	registerRegistryTools(r)
	registerNotificationTools(r)
	registerVulnerabilityTools(r)
	registerUserTools(r)
	registerSSOTools(r)
	registerSettingsTools(r)
	registerPreferenceTools(r)
	registerSecretProviderTools(r)
	registerGitTools(r)
	registerGitStackTools(r)
	registerExportTools(r)
	registerTemplateTools(r)
	registerTagTools(r)
	registerLabelTools(r)
	registerConfigSetTools(r)
	registerIconTools(r)
	registerContainerTools(r)
	registerContainerGetTool(r)
	registerContainerInsideTools(r)
	registerContainerChangeTools(r)
	registerContainerMakeTools(r)
	registerContainerUpdateTools(r)
	registerContainerFileTools(r)
	registerContainerEventTools(r)
	registerStackTools(r)
	registerStackGetTools(r)
	registerStackWriteTools(r)
	registerLogTools(r)
	registerActionTools(r)
	registerJobTools(r)
	registerImageTools(r)
	registerVolumeTools(r)
	registerNetworkTools(r)
}

// ToolInfo describes a registered tool without a server to register it on.
type ToolInfo = mcpregistry.Info

// described is every tool queued with no Dockhand behind it, for the answers that need none: registration never calls a client, only the handlers do.
func described(opts Options) (*mcpregistry.Registry, error) {
	dock, err := dockhand.NewWithoutLogin("https://describe.invalid")
	if err != nil {
		return nil, err
	}
	r := &registry{dock: dock, opts: opts}
	queueTools(r)

	return r.queued(), nil
}

// Describe lists the tools opts would register, for `dockhand-mcp tools`. It needs no connectivity, and makes the same choice RegisterAll does.
func Describe(opts Options) ([]ToolInfo, error) {
	if err := opts.check(); err != nil {
		return nil, err
	}
	reg, err := described(opts)
	if err != nil {
		return nil, err
	}

	return reg.Describe(opts.selection())
}

// ToolsetNames lists the curated toolsets, for help output.
func ToolsetNames() []string {
	return mcpregistry.New(mcpregistry.Config{Toolsets: Toolsets}).ToolsetNames()
}

// FamilyNames lists the resource prefixes accepted by --toolsets, for help output.
func FamilyNames() []string {
	reg, err := described(Options{})
	if err != nil {
		return nil
	}

	return reg.FamilyNames()
}
