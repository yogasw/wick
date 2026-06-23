package googleworkspace

// Gmail input structs — one per operation.

// GmailListMessagesInput is the argument schema for gmail_list_messages.
type GmailListMessagesInput struct {
	Query      string `wick:"desc=Gmail search query. Example: from:alice@abc.com is:unread newer_than:7d. Leave empty for the whole mailbox."`
	MaxResults int    `wick:"desc=Max messages to return (1-100). Default: 20."`
}

// GmailGetMessageInput is the argument schema for gmail_get_message.
type GmailGetMessageInput struct {
	MessageID string `wick:"required;desc=Gmail message ID (from gmail_list_messages)."`
}

// GmailSendInput is the argument schema for gmail_send.
type GmailSendInput struct {
	To      string `wick:"required;desc=Recipient email address(es), comma-separated. Example: bob@abc.com"`
	Cc      string `wick:"desc=CC email address(es), comma-separated."`
	Subject string `wick:"required;desc=Email subject line."`
	Body    string `wick:"required;textarea;desc=Plain-text email body."`
}

// GmailCreateDraftInput is the argument schema for gmail_create_draft.
type GmailCreateDraftInput struct {
	To      string `wick:"desc=Recipient email address(es), comma-separated."`
	Cc      string `wick:"desc=CC email address(es), comma-separated."`
	Subject string `wick:"desc=Email subject line."`
	Body    string `wick:"textarea;desc=Plain-text email body."`
}

// GmailReplyInput is the argument schema for gmail_reply.
type GmailReplyInput struct {
	MessageID string `wick:"required;desc=ID of the message to reply to. The reply stays in the same thread."`
	Body      string `wick:"required;textarea;desc=Plain-text reply body."`
}

// GmailModifyLabelsInput is the argument schema for gmail_modify_labels.
type GmailModifyLabelsInput struct {
	MessageID    string `wick:"required;desc=Gmail message ID to modify."`
	AddLabels    string `wick:"desc=Label IDs to add, comma-separated. System labels: UNREAD, STARRED, IMPORTANT, INBOX, SPAM, TRASH."`
	RemoveLabels string `wick:"desc=Label IDs to remove, comma-separated. Remove UNREAD to mark read; remove INBOX to archive."`
}
