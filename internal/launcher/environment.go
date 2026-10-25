package launcher

import "os"

// ApplyEnvironment mirrors the shell launcher's environment handling;
// every Go-spawned child inherits the exported process environment,
// so each variable reaches all child contexts.
func ApplyEnvironment(cfg *Config) {
	if gamescope := os.Getenv("GAMESCOPE_WAYLAND_DISPLAY"); gamescope != "" {
		os.Unsetenv("DISPLAY")
		os.Setenv("WAYLAND_DISPLAY", gamescope)
	}
	if cfg.TitleBar {
		os.Setenv("WLR_NO_DECORATION", "0")
	} else {
		os.Setenv("WLR_NO_DECORATION", "1")
	}
	if cfg.WindowWidth != "" {
		os.Setenv("XTMAPPER_WIDTH", cfg.WindowWidth)
	}
	if cfg.WindowHeight != "" {
		os.Setenv("XTMAPPER_HEIGHT", cfg.WindowHeight)
	}
	if cfg.RefreshMHz != "" {
		os.Setenv("XTMAPPER_REFRESH", cfg.RefreshMHz)
	}
}
