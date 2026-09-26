package plugins

import (
	"sync"

	"github.com/cloudvoyant/premise/core"
)

var registerBuiltinsOnce sync.Once
var registerBuiltinsErr error

// RegisterBuiltins installs the CLI's package managers in detection order.
// Clients may register their own implementations with core before releasing.
func RegisterBuiltins() error {
	registerBuiltinsOnce.Do(func() {
		for _, plugin := range []core.PackageManagerPlugin{Go{}, Cargo{}, Bun{}} {
			if err := core.RegisterPackageManagerPlugin(plugin); err != nil {
				registerBuiltinsErr = err
				return
			}
		}
	})
	return registerBuiltinsErr
}
