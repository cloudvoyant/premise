package cmd

import (
	"sync"

	"github.com/cloudvoyant/premise/core"
	"github.com/cloudvoyant/premise/core/plugins"
)

var registerPackageManagersOnce sync.Once
var registerPackageManagersErr error

// registerPackageManagerPlugins installs the CLI's built-in implementations.
// Core owns the registry contract but cannot import implementations that depend
// on core, so the executable composition root performs the wiring explicitly.
func registerPackageManagerPlugins() error {
	registerPackageManagersOnce.Do(func() {
		for _, plugin := range []core.PackageManagerPlugin{plugins.Go{}, plugins.Cargo{}, plugins.Bun{}} {
			if err := core.RegisterPackageManagerPlugin(plugin); err != nil {
				registerPackageManagersErr = err
				return
			}
		}
	})
	return registerPackageManagersErr
}
