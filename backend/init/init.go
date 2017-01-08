// Package backendinit is used to initialize backends.
//
// This package keeps track of the available backends and given a backend
// configuration will initialize the correct backend to use. This functionality
// is not in the "backend" package to avoid circular dependencies from backend
// implementations that need to use structs from the "backend" package.
//
// The returned backends from this package are ready to be used (State,
// Operation, etc.). All initialization functions such as Input, Validate,
// Configure will be called.
package backendinit

import (
	"github.com/hashicorp/terraform/backend"
	"github.com/hashicorp/terraform/config"
)

// Backends is the list of available backends.
//
// This can be modified before any initialization to add or remove the
// list of available backends. This will be empty by default, use the
// backend/builtin package to get the built-in list of backends.
var Backends map[string]func() backend.Backend

// FromConfig initializes a backend with the given configuration.
func FromConfig(b *config.Backend, opts *backend.CLIOpts) (backend.Backend, error) {
	return nil, nil
}
