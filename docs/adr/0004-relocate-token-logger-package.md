# Relocate the Token Logger package with a coordinated consumer migration

Move the reusable Token Logger package from `plugins/library/token-logger`
to `plugins/token-logger`, preserving its package name, exported API, runtime
behavior, database schema, single module, and repository-wide versioning.
Intentionally remove the old import path in the final layout rather than
maintaining a permanent compatibility package: the only confirmed downstream
consumer is the independently released `komposyn-lg`, and its mutable version
variable and linker injection make a simple forwarding package insufficient.

Release core with the new path in a newer version without a bridge release;
migrate the CLI imports, release linker target, and tagged core dependency in
a separate follow-up thread. Existing CLI builds pinned to older core remain
unaffected. Publish explicit migration guidance and preserve compatibility
testing. Accept the expected failure of core's candidate-replacement CLI check
until the CLI default branch migrates, keeping the check enabled and the
failure documented. This does not authorize silently bypassing required-check
restrictions. Public searches cannot exclude private or unindexed consumers.
See the [consumer investigation](../token-logger-package-relocation.md).
