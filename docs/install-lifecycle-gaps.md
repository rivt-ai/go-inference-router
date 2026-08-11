# Provider install lifecycle

Provider Process artifacts live at `<cache>/<provider>/<version>/<os>-<arch>/`. Versions coexist so
hosts can update an unpinned Provider Definition or roll back immediately by pinning an older cached
version.

## Version selection

An omitted version means the newest stable semantic version in both the signed registry and the
local cache. Semantic comparison accepts identifiers with or without a leading `v`. Prereleases and
legacy non-semantic identifiers never become the implicit default, but remain available through an
exact version request.

`Locate` re-verifies the selected binary against its `.sha256` sidecar immediately before execution.
Availability polling inventories cache entries without hashing every binary; the execution path
remains the integrity gate.

## Update discovery

`llm.v1.install.available` accepts a provider artifact type and returns:

- every cached version, newest first;
- the stable version implicit lookup would currently select;
- the newest compatible stable version in the verified registry; and
- whether the registry version is newer.

The query verifies the same signed manifest as `install.plan` but does not create a pending approval
plan. A provider the registry no longer offers reports an empty available version and no update
rather than failing, so its cached versions stay enumerable and reclaimable.

## Removal and retention

`llm.v1.install.remove` removes one exact provider type and version. A missing cache entry is an
idempotent no-op. The Router refuses removal while a Provider Definition pins that exact version,
temporarily blocks new opens for the provider type, stops matching unpinned processes, and only then
deletes the version directory and its digest sidecar.

Retention count belongs to the host. It can use the availability inventory to keep its preferred N
versions and explicitly remove the rest; installation approval never deletes rollback candidates as
a side effect.

## Not covered here

Module tagging remains open — `git tag` is still empty and the submodule `replace` directives are
intact, so external hosts cannot resolve these modules. That is a release task, tracked separately.
