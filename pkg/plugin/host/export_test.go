package host

// SetMaxResponseBytes lowers the response cap for a test and returns a restore
// func.
func SetMaxResponseBytes(n int64) (restore func()) {
	old := maxResponseBytes
	maxResponseBytes = n
	return func() { maxResponseBytes = old }
}
