package provider

import "os"

// removeAll is a test helper alias so test files in this package can
// clean up tempdirs created by userconfig without importing os in
// every test file. Kept here rather than inline to avoid pulling os
// into the production status_cache_capability_test.go file.
func removeAll(path string) error { return os.RemoveAll(path) }
