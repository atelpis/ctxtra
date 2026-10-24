# Changelog

Each release says under **Upgrade** whether you need to run `ctxtra setup` after you upgrade. Without
that line, the hooks that an earlier release set up keep working as they are
([setup compatibility](docs/spec.md#setup-compatibility)).

## Unreleased

## v0.1.0

The first public release.

Upgrade: setups written before 0.1 aren't supported. `ctxtra doctor` says what to delete or set up again.
