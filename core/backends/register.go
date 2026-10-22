package backends

import (
	"sync"

	"github.com/cloudvoyant/premise/core"
)

// Public API -----------------------------------------------------------------

// RegisterPackageManagers installs the CLI's built-in implementations.
func RegisterPackageManagers() error {
	registerPackageManagersOnce.Do(func() {
		for _, plugin := range []core.PackageManagerBackend{Go{}, Cargo{}, Bun{}} {
			if err := core.RegisterPackageManagerBackend(plugin); err != nil {
				registerPackageManagersErr = err
				return
			}
		}
	})
	return registerPackageManagersErr
}

// Private helpers ------------------------------------------------------------

var registerPackageManagersOnce sync.Once

var registerPackageManagersErr error
