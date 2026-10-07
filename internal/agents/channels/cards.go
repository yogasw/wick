package channels

import "context"

// PostbackFn sends a click on a card button (cardID, value) in sessionID
// to the agent, as the web UI's postback endpoint does, and returns the
// button's label. source names the channel ("slack").
type PostbackFn func(ctx context.Context, sessionID, source, cardID, value string) (label string, err error)

// NumberPostbackFn maps a bare-number reply ("2") on a channel without
// buttons to the open card's n-th button. ok=false: not a number, or no
// open card — send the text as an ordinary message.
type NumberPostbackFn func(ctx context.Context, sessionID, source, text string) (label string, ok bool, err error)

// CardPostback and CardNumberPostback are wired by the server at boot
// (the tools package owns the conversation and the pool); nil leaves
// cards as text and numbers as plain messages.
var (
	CardPostback       PostbackFn
	CardNumberPostback NumberPostbackFn
)
