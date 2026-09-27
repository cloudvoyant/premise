package core

// PackageMetadata is package-manager-neutral information extracted from one
// project or template package specification. Plugins parse their native files
// and return this shape so release planning does not need format-specific rules.
type PackageMetadata struct {
	Name           string
	Version        string
	Path           string
	PackageManager string
	Ecosystem      string
	Registry       string
	Public         bool
	Publishable    bool
}
