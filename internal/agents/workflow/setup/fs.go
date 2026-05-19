package setup

import "os"

// removeAll wraps os.RemoveAll so tests can intercept; kept private.
func removeAll(path string) error { return os.RemoveAll(path) }
