package webtty

// Config holds the runtime-editable knobs for the web terminal tool.
type Config struct {
	// Enabled controls whether the terminal is accessible. When false,
	// the tool page shows a disabled notice instead of the terminal.
	Enabled bool `wick:"desc=Enable or disable the web terminal. When disabled, the page shows a notice instead of the terminal.;checkbox"`
}
