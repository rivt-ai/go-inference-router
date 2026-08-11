# Provider-specific configuration uses maps

Provider Definitions keep routing fields typed but carry provider-specific
settings in `options` and secret references in `secrets`. This finalizes the
unreleased configuration v1 in place: new providers no longer widen a shared
union or require Router-maintained key lists, and no compatibility reader is
needed before the first consumer exists.
