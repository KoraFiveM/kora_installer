# Kora Control development

Read README.md and CONTRIBUTING.md before changing this repository.
This is the independent source repository for the `kora` development-server CLI.

- Preserve existing server files, resource checkouts, databases and Docker volumes.
- Never start real servers, pull resource repositories or update installed resources just to test changes.
- Use temporary Git repositories, fake commands and local HTTP fixtures for tests.
- Never commit real credentials, `.env` files, runtime data, compiled binaries or dependencies.
- Run Go tests and vet, Python launcher/bootstrap tests and shell syntax checks.
  Build Linux and Windows x86-64 when changing startup or platform behavior.
  Never register real Windows services just for validation; use offline fixtures.
- Retain Git tracking when installing resources. Unsupported recipes must fail explicitly.
- Use Commitizen conventional commits with focused scopes; commit/push only when requested.
- Creating release tags publishes binaries through GitHub Actions. Only tag or publish when requested.
- The checked-in walkthrough uses simulated installation data, never a real server.
