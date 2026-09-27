package cmd

import (
	"sync"

	"github.com/cloudvoyant/premise/core"
	"github.com/cloudvoyant/premise/core/backends"
)

var registerPackageManagersOnce sync.Once
var registerPackageManagersErr error

// registerPackageManagerBackends installs the CLI's built-in implementations.
// Core owns the registry contract but cannot import implementations that depend
// on core, so the executable composition root performs the wiring explicitly.
func registerPackageManagerBackends() error {
	registerPackageManagersOnce.Do(func() {
		for _, plugin := range []core.PackageManagerBackend{backends.Go{}, backends.Cargo{}, backends.Bun{}} {
			if err := core.RegisterPackageManagerBackend(plugin); err != nil {
				registerPackageManagersErr = err
				return
			}
		}
	})
	return registerPackageManagersErr
}
