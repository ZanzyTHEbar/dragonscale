package cli

// DefaultPalette is the global command registry.
var DefaultPalette Palette

// Register registers command factories into the global palette.
func Register(factories ...CommandFactory) {
	DefaultPalette.Register(factories...)
}
