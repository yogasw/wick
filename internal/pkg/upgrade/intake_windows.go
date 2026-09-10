//go:build windows

package upgrade

import "context"

// IntakeBaton is a no-op on Windows: without descriptor passing there is
// never a second wick process to coordinate with, so intake needs no lock.
type IntakeBaton struct{}

func acquireIntake(context.Context, string) (*IntakeBaton, error) { return &IntakeBaton{}, nil }

// Release does nothing.
func (b *IntakeBaton) Release() {}
