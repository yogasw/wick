package client

import "fmt"

type Error struct {
	Message        string
	StatusCode     int
	RawError       error
	RawAPIResponse []byte
}

// Error returns error message.
// To comply client.Error with Go error interface.
func (e *Error) Error() string {
	if e.RawError != nil {
		return fmt.Sprintf("%s: %s", e.Message, e.RawError.Error())
	}

	return e.Message
}

// Unwrap method that returns its contained error
func (e *Error) Unwrap() error {
	return e.RawError
}
