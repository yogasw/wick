package providerstorage

// Config holds runtime-editable knobs for the provider-storage tool.
type Config struct {
	VerboseLogs bool `wick:"desc=Log every file synced or restored (disk path + action). Off by default — enable when debugging sync issues.;checkbox"`
}
