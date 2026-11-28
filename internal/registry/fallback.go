package registry

import _ "embed"

// fallbackRegistryYML is an offline snapshot of the default official registry.yml,
// used only when fetching the remote default registry fails and no cache exists.
//
//go:embed fallback_registry.yml
var fallbackRegistryYML []byte
