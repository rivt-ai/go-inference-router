# Provider Process threat model

Provider Processes run unsandboxed with the invoking user's operating-system
privileges. The security boundary provides process isolation, a protocol-only
stdout channel, an allowlisted environment, and credentials scoped to the
selected Provider Definition; it does not provide confinement. Installed
binaries are signature-verified at download and hash-verified before execution,
while explicitly configured paths and opted-in `PATH` binaries are user-trusted.
A same-UID attacker who can replace both binaries and integrity records is out
of scope.
