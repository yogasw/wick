package entity

import "time"

// Message kinds.
//
// A reply is its own row rather than a column on the ask, so a tree's
// messages read as one conversation in creation order instead of a set of
// question objects with answers hidden inside them.
const (
	MessageAsk   = "ask"
	MessageTell  = "tell"
	MessageReply = "reply"
)

// Message statuses. "delivered" means handed to the recipient's turn;
// "answered" is reserved for an ask whose reply exists.
const (
	MessageQueued    = "queued"
	MessageDelivered = "delivered"
	MessageAnswered  = "answered"
	MessageDropped   = "dropped"
)

// MessageKinds is the complete kind set, so a UI mapping and any
// validation read the same list.
var MessageKinds = []string{MessageAsk, MessageTell, MessageReply}

// AgentMessage is one message between two agents inside a delegation tree.
//
// RootID scopes addressing: a handle only means something inside its own
// tree, so a message can never reach an agent in someone else's
// conversation — and a leader cannot re-prompt a session it does not own.
//
// FromHandle is written by the SERVER from the calling session, never
// taken from model input. A model that could name its own sender could
// claim to be the leader and inherit the authority that comes with it.
type AgentMessage struct {
	ID         string `gorm:"primaryKey;type:varchar(64)" json:"id"`
	RootID     string `gorm:"type:varchar(64);not null;index:idx_agent_messages_inbox,priority:1" json:"root_id"`
	FromHandle string `gorm:"type:varchar(64);not null" json:"from_handle"`
	ToHandle   string `gorm:"type:varchar(64);not null;index:idx_agent_messages_inbox,priority:2" json:"to_handle"`
	Body       string `gorm:"type:text;not null" json:"body"`
	Kind       string `gorm:"type:varchar(16);not null" json:"kind"`
	// ReplyTo points a reply at the ask it answers.
	ReplyTo string `gorm:"type:varchar(64);index" json:"reply_to,omitempty"`
	// AutoReply marks a reply wick synthesised from the recipient's final
	// turn because it never called reply explicitly. Surfaced so a reader
	// can tell a deliberate answer from a salvaged one — the salvaged kind
	// often does not actually answer the question.
	AutoReply bool   `gorm:"not null;default:false" json:"auto_reply,omitempty"`
	Status    string `gorm:"type:varchar(16);not null;index:idx_agent_messages_inbox,priority:3" json:"status"`
	// Hop records the tree's hop counter when this message was sent. Kept
	// for audit; the live counter lives on the root delegation row.
	Hop         int        `gorm:"not null;default:0" json:"hop"`
	CreatedAt   time.Time  `json:"created_at"`
	DeliveredAt *time.Time `json:"delivered_at,omitempty"`
}

func (AgentMessage) TableName() string { return "agent_messages" }
