# BugBarn Infrastructure

This directory contains the project-specific homelab runner scaffold for BugBarn.

It intentionally mirrors the adjacent `../rapid-root/infra` runner conventions:

- Linux GitHub runners are configured in `github-runner.yml`.
- macOS GitHub runners are configured through `host_vars/mac-mini.yml` and `host_vars/mac-laptop.yml`.
- The shared Ansible roles are expected at `../rapid-root/infra/roles` for now.

This keeps this new repository small while still allowing the project to maintain its own runner definitions.

## Commands

```bash
make setup-runners
make setup-runners-macos
make ping
```

Required environment:

- `GITHUB_TOKEN`: token with permission to register repository runners.

## Open Question

Before this becomes fully standalone, decide whether to vendor the shared runner roles into this repository, publish them as an Ansible collection, or keep them as a local homelab dependency.
