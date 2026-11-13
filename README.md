# Kora Control — create and manage FiveM development servers

**Kora Control** is the Windows desktop app for creating and managing native FiveM
servers. The **`kora`** command remains available for terminal users and Linux hosts.
Both interfaces share the same installation, validation and server-management code.

## Windows desktop app

Build with `task build:gui`, then double-click `dist/kora-control.exe`. Tagged
releases built with this workflow include `kora-control.exe` and its SHA-256
checksum, alongside the existing CLI. Older releases may contain only the CLI.
No browser runtime, Go, Docker, WSL or Python is required to run Control.

The **Create a server** wizard walks through five steps:

1. **Check your computer** — prerequisite status, official Git/VC++ download
   links, retry, and an optional Windows Administrator restart.
2. **Name your server** — choose a parent folder, a new directory name and a
   player-facing name. Open the Cfx.re portal and paste your license into a masked
   native input. Select ports or use **Find available ports**.
3. **Database & startup** — create a private MariaDB service (recommended), or
   use an existing MariaDB/MySQL service. Optionally run FiveM as a Windows service.
4. **Optional settings** — keep current defaults, choose available versions, or
   supply a local/web resources.json or supported txAdmin recipe.
5. **Review & install** — see the destination and resolved versions before any
   installation starts. The final choice is whether to start the server afterwards.

Activity runs in the background with progress and a redacted log. Cancellation
retains created files and database objects; it does not roll them back or delete
anything. If installation failed, correct the issue and choose a new directory.
If only startup failed, use **Your servers** to inspect logs and retry Start.
Cancelling readiness monitoring does not stop an already started server.

Control opens **Your servers** automatically when native installations are found.\n**Your servers** discovers native installations and provides Start, Stop, Restart,
logs, folder access and separately staged resource updates. Multiple servers keep
their own folders, ports and databases. Stopping FiveM preserves the database.

The app uses native Windows controls with Kora charcoal/lime branding, keyboard
navigation, standard paste, a folder picker and a DPI-aware manifest. It uses
[Walk](https://github.com/lxn/walk) with Go; no JavaScript frontend or WebView2
installation is needed. Service creation requires Administrator permission.
Prerequisite installation and firewall/router setup remain explicit user actions;
local readiness does not prove that players can connect from the internet.

For source validation on Windows, generate the manifest using `task build:gui`,
then run `go test -race -gcflags="github.com/lxn/...=-d=checkptr=0" -tags gui ./...`.
The package-scoped exception handles Win32 integer resource IDs in the upstream
toolkit; Kora code retains pointer checks and race detection. The native window test uses synthetic
data and creates no services or real servers. CI captures a test-window preview
as the `kora-control-preview` artifact. Visual accessibility, high-DPI behavior,
UAC and real MariaDB/FXServer installation still need target-host acceptance.

## Prerequisites

Use an **x86-64 Windows or Linux host**. Windows installations run FXServer and
MariaDB/MySQL directly as native processes/services: Docker, WSL and Python are
not required. Linux installations continue to use Docker and Compose v2.
Both paths require Git and a Cfx.re username and [license key](https://portal.cfx.re/).

### Native Windows

1. Install [Git for Windows](https://git-scm.com/install/windows) and the
   [Microsoft Visual C++ x64 Redistributable](https://learn.microsoft.com/en-us/cpp/windows/latest-supported-vc-redist).
2. Download `kora_installer.exe` and its `.sha256` file from the
   [releases page](https://github.com/KoraFiveM/kora_installer/releases).
   The native Windows asset is introduced by this change; older Linux-only
   releases do not contain it. Compare `Get-FileHash .\kora_installer.exe -Algorithm SHA256`
   with the published checksum.
3. Open PowerShell **as Administrator** when installing a new database service
   or enabling the optional FXServer service. Run `.\kora_installer.exe`.
4. Choose a parent folder and a new directory name. Existing folders are never
   reused. The wizard installs Windows FXServer artifacts and Git resource clones.
5. Choose **new-service** for a separate local MariaDB service (default), or
   **existing** to connect to your running MariaDB/MySQL service. Both choices
   create a fresh database and account without changing existing ones.
6. Choose whether FXServer should be an automatic Windows service. Without it,
   use the installed `start.cmd` or the Kora dashboard. Finish by choosing whether
   to start the server now.

A new MariaDB installation is kept under the chosen server directory, verified
against MariaDB's official SHA-256 manifest, and bound to `127.0.0.1`. It receives
a unique service name and data directory. Select an unused database port when
installing multiple servers. `--mariadb-version` defaults to `11.4.13`.
An existing service requires an administrative connection with permission to
create databases/users and grant access; those administrator credentials are
used for provisioning and not retained. Newly generated MariaDB administrator
credentials are stored in private `database-admin.json` for recovery.

```powershell
# Guided native installation; choose the directory and service options in the TUI.
.\kora_installer.exe

# Native installation with a dedicated MariaDB and an automatic FXServer service.
.\kora_installer.exe --yes --start --output 'C:\KoraServers' --name city --windows-service --db-port 3307 --cfx-user YOUR_USER --license-key YOUR_CFX_LICENSE_KEY

# Use an existing MySQL service; enter the administrator password in the masked TUI.
.\kora_installer.exe --output 'D:\Servers' --name city --db-setup existing --db-type mysql --db-host 127.0.0.1 --db-port 3306 --db-admin root
```

Each installation contains its own `kora.exe`, `start.cmd`, `native.json`, private
`.env`, `artifacts/`, and editable Git repositories under `server-data/resources/`.
It is registered in Kora's server dashboard. The optional FXServer service runs as
LocalService; the MariaDB service is independent. Stopping FXServer does not stop
or delete the database. Service creation requires elevation and never replaces
an existing service. Failed installs retain files and any newly created services
for inspection. Resource updates are staged separately, as on Linux.

Allow your selected FiveM port through Windows and upstream firewalls for **TCP
and UDP**. Allow the txAdmin TCP port only if needed. Kora does not change firewall
rules. A local `/info.json` response confirms local readiness, not public access.
Adminer and Docker database-port publishing options apply only to Linux/Docker.

### Linux

1. Install Docker Engine using the official instructions for your distribution:
   [Ubuntu](https://docs.docker.com/engine/install/ubuntu/),
   [Debian](https://docs.docker.com/engine/install/debian/),
   [Fedora](https://docs.docker.com/engine/install/fedora/), or
   [other supported distributions](https://docs.docker.com/engine/install/).
   Include the [Docker Compose plugin](https://docs.docker.com/compose/install/linux/).
2. Start Docker and follow Docker's
   [Linux post-installation guide](https://docs.docker.com/engine/install/linux-postinstall/)
   so your normal user can access it. Docker-group membership grants root-level
   privileges; use the access method appropriate for your host.
3. Install [Git for Linux](https://git-scm.com/install/linux), a POSIX shell,
   `curl` or `wget`, CA certificates, and `sha256sum` or `shasum`. For Ubuntu/Debian: `sudo apt install git curl ca-certificates coreutils`.

### Verify before running Kora

For the Linux/Docker path, run these in the **same Linux terminal** you will use for Kora:

```sh
git --version
docker version
docker compose version
docker info --format '{{.OSType}}/{{.Architecture}}'
```

`docker version` must reach the server, Compose must report v2 or newer, and the
last command should report `linux/x86_64` or `linux/amd64`. If Docker is unavailable,
start Docker Desktop or the Docker Engine service; in WSL, check integration for
the selected distribution. Kora also checks these prerequisites before installing.

For private GitHub resources, configure Git credentials in that same environment.
One option is to [install GitHub CLI](https://cli.github.com/) and run
[`gh auth login`](https://cli.github.com/manual/gh_auth_login), then
[`gh auth setup-git`](https://cli.github.com/manual/gh_auth_setup-git).

The published binary does **not** require Go, Python, or a host database server.
Docker provides the runtime and database. Building Kora from source requires
[Go 1.26+](https://go.dev/doc/install); [Task](https://taskfile.dev/docs/installation)
is optional. Python 3 is needed for the development tests, not normal CLI use.

## Install with one command

Linux or WSL x86-64:

```sh
curl -fsSL https://raw.githubusercontent.com/KoraFiveM/kora_installer/HEAD/install.sh | sh -s --
```

The script downloads a **GitHub Actions-built binary**, verifies its SHA-256 checksum,
and installs **`~/.local/bin/kora`**. No Go compiler is needed. It opens the dashboard
when a terminal is available. Add `~/.local/bin` to PATH if instructed, then run:

```sh
kora
```

GitHub Actions builds both Linux and native Windows binaries **inside Docker**,
then publishes binaries, ZIPs and SHA-256 checksums for each `kora-v*` release.
This build container is not a Windows runtime dependency. The shell bootstrap
installs the Linux binary; Windows users download `kora_installer.exe` instead.

Other bootstrap examples:

```sh
# Install the CLI without opening it.
curl -fsSL https://raw.githubusercontent.com/KoraFiveM/kora_installer/HEAD/install.sh | sh -s -- --install-only

# Pin an available release and choose a user bin directory.
curl -fsSL https://raw.githubusercontent.com/KoraFiveM/kora_installer/HEAD/install.sh | KORA_VERSION=kora-v1.0.0 KORA_BIN_DIR="$HOME/bin" sh -s -- --install-only
```

`kora-v1.0.0` above is an example: substitute a published tag. Existing binaries are
never overwritten. To replace an older CLI, move it aside yourself first. The
script needs curl or wget and sha256sum or shasum. Server directories are untouched.

## One-line server installations, including license keys

`--cfx-user` is your **Cfx.re username**. `--license-key` is the server license key.
`--name` is a directory name, not either credential. Replace the placeholders:

```sh
# Install the CLI and a new Kora server, then start it.
curl -fsSL https://raw.githubusercontent.com/KoraFiveM/kora_installer/HEAD/install.sh | sh -s -- --yes --start --name=kora-dev --cfx-user=YOUR_CFX_USERNAME --license-key='YOUR_CFX_LICENSE_KEY' --txadmin --adminer

# With kora already installed: fully unattended defaults.
kora --yes --start --name=kora-dev --cfx-user=YOUR_CFX_USERNAME --license-key='YOUR_CFX_LICENSE_KEY'

# A second server, with distinct ports and automatically generated DB credentials.
kora --yes --start --name=kora-test --cfx-user=YOUR_CFX_USERNAME --license-key='YOUR_CFX_LICENSE_KEY' --cfx-port=30121 --txadmin --tx-port=40121 --adminer --adminer-port=8081 --db-expose-port --db-host-port=3307

# Explicit output parent, MySQL, and redacted report.
kora --yes --name=my-server --output="$HOME/KoraServers" --db-type=mysql --report --cfx-user=YOUR_CFX_USERNAME --license-key='YOUR_CFX_LICENSE_KEY'

# Custom local resources.json.
kora --yes --start --name=custom --resources=./resources.json --cfx-user=YOUR_CFX_USERNAME --license-key='YOUR_CFX_LICENSE_KEY'

# HTTP(S) or GitHub catalog.
kora --yes --name=team --resources=https://example.com/resources.json --cfx-user=YOUR_CFX_USERNAME --license-key='YOUR_CFX_LICENSE_KEY'
kora --yes --name=team-local --resources=http://localhost:8000/resources.json --cfx-user=YOUR_CFX_USERNAME --license-key='YOUR_CFX_LICENSE_KEY'
kora --yes --name=github-catalog --resources=https://github.com/KoraFiveM/kora_docs_next/blob/main/catalog/resources.json --cfx-user=YOUR_CFX_USERNAME --license-key='YOUR_CFX_LICENSE_KEY'

# Explicit txAdmin recipe instead of the catalog; retain the recipe's refs.
kora --yes --name=recipe-server --template=./recipe.yaml --kora-tag=recipe --cfx-user=YOUR_CFX_USERNAME --license-key='YOUR_CFX_LICENSE_KEY'
```

Command-line license keys are supported; they can appear in shell history and
process listings. As an alternative, export `CFX_LICENSE_KEY` privately and omit
`--license-key`. No real license is included in these examples or the video.

`--yes` accepts the resolved plan without a TUI. `--start` starts Docker detached
and waits up to five minutes for FiveM readiness after the build. Without `--yes`, flags prefill the wizard and reviews remain
interactive. Errors return a nonzero status. `--help` lists all options.

To run from a source checkout, use `task run -- <flags>` or `go run . <flags>`
from the repository root. Building from source requires Go 1.26+.

## Startup progress and connection links

Starting a new or existing server keeps the TUI open with a milestone progress
bar, live Docker build output, and recent service logs. Kora checks that the
FiveM container is running and `/info.json` responds with server information;
resource-specific gameplay checks still belong to their resources. A stopped or
unhealthy container produces an error instead of a ready message.

The screen shows the FiveM connect link, server-info URL, and enabled txAdmin and
Adminer URLs. During startup, **Ctrl+O** opens txAdmin for first-time setup. Once
ready, **C** opens FiveM. Links are clickable in terminals supporting OSC 8;
plain URLs remain visible for copying. These are local-host links: from another
computer, replace `127.0.0.1` with the server's reachable address. Adminer is bound
to loopback, so remote access requires a tunnel.

Press **Enter** after success or failure to close the installer. A readable
summary remains in the terminal with the installation path, ports, links, and
management command, or the error and recent logs. Docker runs detached; exiting
the installer does not stop services or remove data. With `--yes --start`, output
is printed directly and the command exits automatically after readiness or failure.

## Walkthrough video

https://github.com/user-attachments/assets/f9fcef93-b42f-4fcc-ba95-dc23fe99fd70

[Download the walkthrough MP4](docs/walkthrough.mp4)

This video uses the actual Bubble Tea view rendering with demo data. Readiness
checks, clone progress and server operations are simulated; it does not depict a
live deployment. It covers the dashboard, server actions, new-server wizard,
catalog input, version selections, masked license entry and final startup choice.

Regenerate on Linux with Pillow, pyte, imageio-ffmpeg and DejaVu Sans Mono installed:

```sh
KORA_WALKTHROUGH_FRAMES=/tmp/kora-walkthrough.json go test -run TestRecordWalkthrough .
python3 tools/render_walkthrough.py /tmp/kora-walkthrough.json docs/walkthrough.mp4
```

## Your servers: Docker in easy mode

Running `kora` opens the server dashboard when installations are found. It shows
Docker Compose state, installation completeness and directory, refreshing every
five seconds while viewing servers. Docker being unavailable is distinct from a
server that has not been started.

| Key | Action |
| --- | --- |
| ↑/↓, Enter | Select a server and open its controls |
| N | Install another server |
| S | Start the selected server |
| X | Stop its services, preserving containers and data |
| B | Restart its services |
| L | View the last 100 log lines |
| I | Inspect resource Git state |
| U | Review and stage resource updates |
| Escape | Go back |

Start, stop, restart and logs require explicit confirmation on the selected server.
Docker output is shown in the terminal, then control returns to the dashboard.
There is no delete-volumes action. Only completed installer-managed servers can
be operated here; original development setups remain visible but use their own
`up.sh` for lifecycle actions. This dashboard manages Kora, not arbitrary containers.

Discovery checks `~/KoraServers` (or `--output`), nearby checkout folders, known
Compose projects, and paths registered by successful installations. Registrations
contain only directory paths in the OS user configuration directory under
`kora/servers/`. Select another existing server explicitly:

```sh
kora --server=/path/to/server
```

Every install creates a new folder. `--name=kora-dev` creates that exact child under
`--output` and fails if it exists; omitting it creates a unique `kora-*` folder.
Each installation gets a unique Compose project and fresh database directory.
Choose different FiveM, txAdmin, Adminer and published database ports for each
server. Port entries check selected-service conflicts and actual availability
before allowing progression, and installation/startup recheck them.

## resources.json is the default source of truth

Default catalog: [docs.koraframe.work/resources.json](https://docs.koraframe.work/resources.json).
It must return a JSON media type (`application/json` or `+json`) and a valid catalog.
Network/status failures, non-JSON headers or invalid catalog content trigger the
[GitHub fallback](https://github.com/KoraFiveM/kora_docs_next/blob/main/catalog/resources.json).
GitHub blob URLs are converted to raw URLs; raw GitHub commonly uses `text/plain`,
so the fallback validates the body without requiring a JSON header.

Use the wizard's catalog field or `--resources=FILE_OR_URL` for a local file, HTTP(S),
or GitHub blob/raw URL. **Explicit custom sources never fall back silently.** Other
HTTP(S) endpoints must return JSON headers. Downloads are bounded to 4 MiB and use
timeouts; malformed catalogs fail before an installation directory is created.

Supported schema (documentation metadata such as title/docsPath is also accepted):

```json
{
  "schemaVersion": 1,
  "resources": [{
    "id": "kora-core",
    "repository": "https://github.com/KoraFiveM/kora_core.git",
    "path": "[pre]/kora_core",
    "latest": "0.1.1",
    "versions": [{
      "version": "0.1.1",
      "tag": "v0.1.1",
      "commit": "REPLACE_WITH_THE_REAL_40_CHARACTER_COMMIT_SHA"
    }]
  }]
}
```

`latest` selects the published release explicitly, regardless of array order.
The exact commit is installed, with its tag displayed in the review. `--kora-tag`
can select a catalog version/tag. If a resource lacks that version, it falls back
to its own published `latest` tag and pinned commit. The review and install progress
identify each fallback; resources that have the requested version keep it. Version
selection lists use kora_core as the representative catalog list; `latest` handles
resources with differing release numbers independently.

An optional `path` controls placement relative to `resources/`. When absent,
known repositories use the bundled recipe's layout; other repositories use
`[kora]/<repository-name>`. Duplicate/unsafe destinations are rejected. Default
catalog installs keep the bundled recipe's non-Kora-resource dependencies, such
as default maps and compatibility groups, then use the catalog for Kora resources.
An explicit custom catalog supplies its complete resource list; include everything
it needs. The bundled recipe still supplies startup/permissions configuration;
review it when creating a very different custom catalog.

Git clones retain history. Catalog pins bypass tag discovery and are checked out
by commit; if necessary the commit is fetched explicitly. Secrets are not catalog
fields. Repository URLs must be credential-free GitHub HTTPS URLs.

## Prerequisites and versions

On Linux, Docker is required. The in-app checks verify Linux x86-64, Git, Docker CLI,
Compose, local context and a running Linux x86-64 engine. They provide official
help links and retry controls. Git is required for recipe mode. Authenticate Git
for private Kora repositories before installing. No host Python or database is
needed when using `kora`; Python runs inside the generated container.

- WSL: [Docker Desktop integration](https://docs.docker.com/desktop/features/wsl/) and
  [Microsoft WSL setup](https://learn.microsoft.com/en-us/windows/wsl/install).
- Linux: [Docker Engine setup](https://docs.docker.com/engine/install/).

Remote Docker contexts are unsupported because this installer relies on local bind
mounts. The Linux/Docker path needs a working local Linux Docker backend. The installer does not install system prerequisites or reboot.

Game builds come from FiveM's documented Legacy build list; recommended Linux
FXServer artifacts come from JG Scripts. These fields have selection lists with
refresh and custom-entry controls. Explicit recipe mode can discover version-sorted
Git tags without GitHub API calls; untagged repositories use their default branch.
Catalog mode instead uses its published `latest` and pinned commit.

## txAdmin recipes and resource updates

Explicit `--recipe` or YAML `--template` opts out of the default catalog unless
`--resources` is also supplied. Recipes may be local YAML, GitHub URLs, or `bundled`.
`--kora-tag=recipe` retains their refs; otherwise Kora resources default to latest
Git version-sorted tags. Resource and setup downloads use Git clones, never ZIPs.

The engine-3 adapter supports repository downloads, top-level cfg/sql moves,
string substitutions, and the supplied create/select-only database bootstrap.
It renders configuration with private credentials supplied by the launcher. Setup
repos remain clean under `recipe-sources/`; delays are skipped and tmp cleanup
retains source checkouts. Owner principals are not invented. Unsupported actions,
unresolved variables and arbitrary SQL stop the install. This is **not a universal
recipe executor**. Nested resource clones must target previously absent paths.

Resource updates are staged separately under `resource-updates/update-*`, with
Git tracking and a review report. Live mounts and databases do not change. Dirty
checkouts, missing Git history or changes that omit local commits block staging.
Review build outputs, ignored local configuration and migrations, then adopt the
new mounts manually; keep previous paths for rollback. Catalog-backed installations retain their catalog source and use its release pins
for updates. Non-catalog repositories continue to resolve their own refs. Updates
do not automatically add new catalog resources to an existing server.

Legacy JSON template overlays remain supported. Press T from the home screen to
export a complete editable base list, or use `--write-template=new-file.json`.
Completed installs also include `base-template.json` and `installation.json`.
Templates never overwrite existing files.

## Optional tools, files and persistence

MariaDB is the default; MySQL is also supported. Generated credentials use random
names and passwords. `--db-expose-port` publishes a configurable host port (3306
by default) on all interfaces; it is off by default.

`--adminer --adminer-port=8080` adds Adminer, preconfigured for `db`, and waits for
database health. It binds to 127.0.0.1 only. The displayed link prefills the database
and username; sign in with `DB_PASSWORD` from the installation's `.env`. No password
is put in the URL. Publishing database port 3306 is not required for Adminer.
`--txadmin --tx-port=40120` adds txAdmin; it defaults to off.

`resources/` is a host bind mount. Engineers can edit resources live, then restart
affected resources in the server console. Each installation keeps its own checkout
and database; source-specific builds still need the resource's documented tools.

`.env` contains private JSON-string values read without shell evaluation. Do not
source it or pass it directly to Compose; use `kora` or optional `start.py` with
host Python 3. Public cfg files reference fresh private runtime credentials.
Optional `--report` and `--secrets` exports have configurable relative JSON paths;
these and `.env` are ignored by generated Git rules. Directories use private Linux
permissions. Protect backups likewise.

Only completed installs receive `install.complete`. Failed directories are retained
and cannot launch; choose a new name on retry. Existing databases and passwords are
never reset. Changing `.env` does not rotate an existing database password. Stop
preserves all data. Runtime sessions and previous resource snapshots are retained;
the installer does not prune them.

## Development and validation

```sh
task run
task build
go test -race ./...
go vet ./...
python3 -m unittest discover -s assets -p 'test_*.py' -v
sh -n install.sh
```

Tests use local Git fixtures, simulated HTTP responses, temporary port bindings,
and generated Compose configuration. The walkthrough renderer is opt-in and makes
no network/installation calls. CI tests Linux and Windows and builds both binaries in Docker; live Windows service,
FXServer/resource compatibility and Docker Desktop still need target-host acceptance.

Copyright © 2026 Kora Framework.

## Build release artifacts in Docker

```sh
task build:docker
# Or without Task:
docker buildx build -f Dockerfile.build --target artifacts --output type=local,dest=dist .
```

Outputs are under `dist/linux-amd64/` and `dist/windows-amd64/`, including
`kora-control.exe` (GUI), `kora_installer.exe` (CLI), ZIP archives, and checksums. This does not start servers or
publish a release. `task build:windows` cross-builds just the Windows executable.
Windows SCM and MariaDB service registration still require live acceptance on a
Windows host; Linux cross-compilation alone does not validate those operations.
