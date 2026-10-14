# Choosing which tools load

**The default is `core` and `audit`: 21 read-only tools, about 6,000 tokens.**

Every tool's description takes room in a client's context before anyone asks a question. So `--toolsets` / `DOCKHAND_TOOLSETS` loads only the groups a session needs. `core` always comes along, because nothing else can say which hosts there are.

| toolset | what it is for | tools | with core | ~tokens |
|---|---|---|---|---|
| `core` *(default)* | what runs where: hosts, containers, stacks and their logs | 8 | 8 | 2,600 |
| `audit` *(default)* | looking for what is wrong | 13 | 21 | 6,000 |
| `manage` | starting, stopping and restarting | 7 | 15 | 4,100 |
| `containers` | making, changing and looking into a container | 15 | 23 | 8,300 |
| `files` | the files inside a container | 9 | 17 | 5,900 |
| `updates` | keeping containers on current images | 5 | 13 | 4,500 |
| `stacks` | compose stacks: making, changing, deploying, removing | 15 | 23 | 8,700 |
| `resources` | images, volumes and networks | 25 | 33 | 10,900 |
| `setup` | adding a host, its agents and its schedules | 9 | 17 | 6,800 |
| `schedules` | what Dockhand does by itself and how each run went | 6 | 14 | 4,700 |
| `registries` | the registries Dockhand keeps, what is in them, pushing to one | 9 | 17 | 5,500 |
| `notifications` | who Dockhand tells of what | 7 | 15 | 5,800 |
| `vulnerabilities` | scanning images and reading what was found | 9 | 17 | 6,000 |
| `users` | who may sign in: users, the login, single sign-on | 11 | 19 | 6,200 |
| `settings` | Dockhand's own settings and what its web interface remembers | 8 | 16 | 5,800 |
| `secrets` | the secret stores a stack's secrets are loaded from | 6 | 14 | 4,700 |
| `backups` | backing up containers and stacks, and restoring them | 22 | 30 | 11,800 |
| `git` | stacks deployed from git, with their repositories and credentials | 17 | 25 | 9,600 |
| `templates` | app templates and config sets | 10 | 18 | 6,000 |
| `tags` | tags, host labels and icons | 14 | 22 | 7,300 |
| `all` | everything | 225 | 225 | 81,000 |

Tokens are what the model sees: each tool's name, description and inputs, measured over a real `tools/list` at four bytes a token.

`--toolsets` also takes a family, which is every tool whose name starts with it: `container`, `stack`, `image`, `volume`, `network`, `env` and so on.

```sh
DOCKHAND_TOOLSETS=all DOCKHAND_ENABLE_WRITE=true DOCKHAND_ENABLE_DELETE=true   # every tool
DOCKHAND_TOOLSETS=manage DOCKHAND_ENABLE_WRITE=true                            # core, and starting, stopping and restarting
DOCKHAND_TOOLSETS=stacks,git DOCKHAND_ENABLE_WRITE=true                        # core, and compose stacks, from a file or from git
DOCKHAND_TOOLSETS=container                                                    # core, and every container tool that only reads
```

`dockhand-mcp tools` prints what the current flags would load, grouped by toolset, and needs no server:

```sh
dockhand-mcp tools                               # the default set
dockhand-mcp tools --toolsets all --enable-write # every tool but the ones that remove
dockhand-mcp tools -q                            # names only
```

## One tool more, or a few less

`--allow-tools` adds tools by name to whatever the toolsets load. With no toolset given, it loads only the tools it names. `--deny-tools` takes tools away.

Both take names, and patterns with a leading or trailing `*`. `--allow-tools` also takes `essential`: `server_info`, `env_list`, `container_list` and `container_logs`.

Neither gets a tool that changes something past `--enable-write`.

```sh
DOCKHAND_ALLOW_TOOLS=essential                                                             # the four essential tools and nothing else
DOCKHAND_ENABLE_WRITE=true DOCKHAND_TOOLSETS=core DOCKHAND_ALLOW_TOOLS=container_restart   # core, and restarting a container as well
DOCKHAND_ENABLE_WRITE=true DOCKHAND_TOOLSETS=manage DOCKHAND_DENY_TOOLS=*_stop             # start and restart, never stop
```
