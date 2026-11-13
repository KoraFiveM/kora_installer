# Contributing

Keep changes focused on the installer and development-server management. Preserve
existing installations and databases. Test using temporary fixtures, without
starting real servers or modifying user resources.

## Local checks

Use Go 1.26+, Python 3 and optionally Task:

```sh
task test
task vet
task check:fmt
task build:linux
sh -n install.sh
```

Without Task, run `go test ./...`, `go vet ./...` and
`python3 -m unittest discover -s assets -p 'test_*.py' -v`.
Go network tests bind loopback sockets; they do not need Docker or GitHub access.

## Commits

Install Commitizen (`pipx install commitizen`), stage only the relevant files,
and run `cz commit`. The `.cz.toml` configuration uses Conventional Commits.
Keep commits small and focused, for example `fix(tui): align license input borders`.
Use `cz check --rev-range <base>..HEAD` to validate a branch's messages.

Explain the behavior change and relevant validation in reviews. Do not include
credentials, installed resource code, runtime data or generated binaries.
Release publication is separate: a `kora-v*` tag triggers the release workflow.
