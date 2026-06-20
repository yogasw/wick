// Package googleworkspace wraps Google Drive, Sheets, Docs, Slides, Gmail,
// Calendar, and Meet REST APIs for LLM consumption.
//
// Purpose: Provides Meta, Configs, 37 Operations across 7 categories, and
// HealthCheck. Each user authenticates their own Google account via OAuth2
// (Connect Account button). Operations are split read-only (list, search,
// info, content, get) vs mutating (upload, create, delete, share, send, RSVP).
// Mutating/destructive ops default to disabled per row.
//
// Caller:   internal/connectors/registry.go::RegisterBuiltins()
// Dependencies: googleworkspace/service.go, googleworkspace/repo.go, googleworkspace/oauth.go
// Side Effects: outbound HTTPS calls on Execute; none at init time.
package googleworkspace

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/yogasw/wick/pkg/connector"
	"github.com/yogasw/wick/pkg/wickdocs"
)

// Meta returns the connector definition metadata.
func Meta() connector.Meta {
	return connector.Meta{
		Key:         "google_workspace",
		Name:        "Google Workspace",
		Description: "Manage Drive files, Sheets data, Docs content, and Slides; read, send, and label Gmail; manage Calendar events with Meet links; and read Meet recordings and transcripts — all with one Google OAuth account.",
		Icon:        "🗂️",
	}
}

// Configs is the per-instance credential set. ClientID and ClientSecret are
// set by the admin; UserToken and RefreshToken are auto-filled by the OAuth flow.
type Configs struct {
	ClientID     string `wick:"desc=OAuth Client ID from Google Cloud Console. Required for Connect Account button."`
	ClientSecret string `wick:"secret;desc=OAuth Client Secret."`
	UserToken    string `wick:"secret;desc=Access token (auto-filled via Connect Account)."`
	RefreshToken string `wick:"secret;desc=Refresh token for auto-renewal (auto-filled via Connect Account)."`
}

// Input types — one per operation.

// ListFilesInput is the argument schema for the list_files operation.
type ListFilesInput struct {
	FolderID string `wick:"desc=Parent folder ID. Leave empty for root (My Drive)."`
	PageSize int    `wick:"desc=Max results (1-1000). Default: 50."`
	OrderBy  string `wick:"dropdown=modifiedTime desc|name|createdTime desc;desc=Sort order. Default: modifiedTime desc."`
}

// SearchFilesInput is the argument schema for the search_files operation.
type SearchFilesInput struct {
	Query    string `wick:"required;desc=Google Drive query string. Example: name contains 'report' and mimeType='application/pdf'"`
	PageSize int    `wick:"desc=Max results (1-1000). Default: 50."`
}

// GetFileInfoInput is the argument schema for the get_file_info operation.
type GetFileInfoInput struct {
	FileID string `wick:"required;desc=Google Drive file or folder ID."`
}

// GetFileContentInput is the argument schema for the get_file_content operation.
type GetFileContentInput struct {
	FileID string `wick:"required;desc=Google Drive file ID."`
}

// UploadFileInput is the argument schema for the upload_file operation.
type UploadFileInput struct {
	Name     string `wick:"required;desc=File name including extension. Example: report.txt"`
	Content  string `wick:"required;textarea;desc=File content as plain text."`
	FolderID string `wick:"desc=Parent folder ID. Leave empty for root."`
	MimeType string `wick:"desc=MIME type. Default: text/plain."`
}

// CreateFolderInput is the argument schema for the create_folder operation.
type CreateFolderInput struct {
	Name           string `wick:"required;desc=Folder name."`
	ParentFolderID string `wick:"desc=Parent folder ID. Leave empty for root."`
}

// DeleteFileInput is the argument schema for the delete_file operation.
type DeleteFileInput struct {
	FileID string `wick:"required;desc=File or folder ID to move to trash."`
}

// ShareFileInput is the argument schema for the share_file operation.
type ShareFileInput struct {
	FileID string `wick:"required;desc=File or folder ID to share."`
	Email  string `wick:"required;email;desc=Email address to grant access to."`
	Role   string `wick:"required;dropdown=reader|writer|commenter;desc=Access level to grant."`
}

// CreateDocInput is the argument schema for the create_doc operation.
type CreateDocInput struct {
	Name     string `wick:"required;desc=Document name. Example: Meeting Notes 2026."`
	FolderID string `wick:"desc=Parent folder ID. Leave empty for My Drive root."`
	Content  string `wick:"textarea;desc=Optional initial plain-text body. Leave empty to create a blank document."`
}

// CreateSheetInput is the argument schema for the create_sheet operation.
type CreateSheetInput struct {
	Name     string `wick:"required;desc=Spreadsheet name. Example: Sales Data Q1."`
	FolderID string `wick:"desc=Parent folder ID. Leave empty for My Drive root."`
	CSVData  string `wick:"textarea;desc=Optional CSV string to pre-populate the first sheet. Leave empty for a blank spreadsheet."`
}

// CreateSlidesInput is the argument schema for the create_slides operation.
type CreateSlidesInput struct {
	Name           string `wick:"required;desc=Presentation name."`
	FolderID       string `wick:"desc=Parent folder ID. Leave empty for My Drive root."`
	FirstSlideText string `wick:"desc=Optional title text for the first slide."`
}

// SheetsReadRangeInput is the argument schema for sheets_read_range.
type SheetsReadRangeInput struct {
	FileID string `wick:"required;desc=Google Spreadsheet file ID."`
	Range  string `wick:"required;desc=A1 notation range. Example: Sheet1!A1:C10 or A:C for a full column."`
}

// SheetsAppendRowsInput is the argument schema for sheets_append_rows.
type SheetsAppendRowsInput struct {
	FileID  string `wick:"required;desc=Google Spreadsheet file ID."`
	Range   string `wick:"required;desc=Range indicating the table start. Example: Sheet1!A:A or Sheet1!A1."`
	CSVData string `wick:"required;textarea;desc=Rows to append as CSV. One row per line. Example: Alice,30,Engineer"`
}

// SheetsUpdateRangeInput is the argument schema for sheets_update_range.
type SheetsUpdateRangeInput struct {
	FileID  string `wick:"required;desc=Google Spreadsheet file ID."`
	Range   string `wick:"required;desc=Target range in A1 notation. Example: Sheet1!A2:C5"`
	CSVData string `wick:"required;textarea;desc=New values as CSV. Overwrites existing cells in the range."`
}

// SheetsClearRangeInput is the argument schema for sheets_clear_range.
type SheetsClearRangeInput struct {
	FileID string `wick:"required;desc=Google Spreadsheet file ID."`
	Range  string `wick:"required;desc=Range to clear in A1 notation. Example: Sheet1!A2:Z100"`
}

// DocsAppendTextInput is the argument schema for docs_append_text.
type DocsAppendTextInput struct {
	FileID string `wick:"required;desc=Google Document file ID."`
	Text   string `wick:"required;textarea;desc=Plain text to append at the end of the document."`
}

// DocsReplaceTextInput is the argument schema for docs_replace_text.
type DocsReplaceTextInput struct {
	FileID    string `wick:"required;desc=Google Document file ID."`
	Find      string `wick:"required;desc=Text to search for throughout the document."`
	Replace   string `wick:"required;desc=Text to substitute in place of every match."`
	MatchCase bool   `wick:"desc=Case-sensitive match. Default: false (case-insensitive)."`
}

// SlidesGetContentInput is the argument schema for slides_get_content.
type SlidesGetContentInput struct {
	FileID string `wick:"required;desc=Google Slides presentation file ID."`
}

// SlidesAddSlideInput is the argument schema for slides_add_slide.
type SlidesAddSlideInput struct {
	FileID        string `wick:"required;desc=Google Slides presentation file ID."`
	Title         string `wick:"desc=Title text for the new slide."`
	Body          string `wick:"textarea;desc=Body text for the new slide."`
	Layout        string `wick:"dropdown=TITLE_AND_BODY|BLANK|TITLE_ONLY;desc=Slide layout. Default: TITLE_AND_BODY"`
	InsertAtIndex int    `wick:"desc=0-based insertion index. Default 0 appends to end (pass -1 to append explicitly)."`
}

// SlidesDuplicateSlideInput is the argument schema for slides_duplicate_slide.
type SlidesDuplicateSlideInput struct {
	FileID     string `wick:"required;desc=Google Slides presentation file ID."`
	SlideIndex int    `wick:"required;desc=0-based index of the slide to duplicate."`
}

// --- Gmail input structs ---

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

// --- Calendar input structs ---

// CalendarListCalendarsInput is the argument schema for calendar_list_calendars.
type CalendarListCalendarsInput struct{}

// CalendarListEventsInput is the argument schema for calendar_list_events.
type CalendarListEventsInput struct {
	CalendarID string `wick:"desc=Calendar ID. Default: primary."`
	TimeMin    string `wick:"desc=Lower bound (RFC3339, inclusive). Example: 2026-06-20T00:00:00Z. Leave empty for no lower bound."`
	TimeMax    string `wick:"desc=Upper bound (RFC3339, exclusive). Leave empty for no upper bound."`
	Query      string `wick:"desc=Free-text search over event fields. Optional."`
	MaxResults int    `wick:"desc=Max events to return (1-250). Default: 50."`
}

// CalendarGetEventInput is the argument schema for calendar_get_event.
type CalendarGetEventInput struct {
	CalendarID string `wick:"desc=Calendar ID. Default: primary."`
	EventID    string `wick:"required;desc=Event ID (from calendar_list_events)."`
}

// CalendarCreateEventInput is the argument schema for calendar_create_event.
type CalendarCreateEventInput struct {
	CalendarID  string `wick:"desc=Calendar ID. Default: primary."`
	Summary     string `wick:"required;desc=Event title."`
	Description string `wick:"textarea;desc=Event description / agenda. Optional."`
	Location    string `wick:"desc=Physical location or address. Optional."`
	Start       string `wick:"required;desc=Start time (RFC3339). Example: 2026-06-21T09:00:00+07:00. For an all-day event pass a date: 2026-06-21."`
	End         string `wick:"required;desc=End time (RFC3339 or date for all-day)."`
	Attendees   string `wick:"desc=Attendee email addresses, comma-separated. Optional."`
	AddMeet     bool   `wick:"desc=Attach a Google Meet video link to the event. Default: false."`
}

// CalendarUpdateEventInput is the argument schema for calendar_update_event.
type CalendarUpdateEventInput struct {
	CalendarID  string `wick:"desc=Calendar ID. Default: primary."`
	EventID     string `wick:"required;desc=Event ID to update."`
	Summary     string `wick:"desc=New title. Leave empty to keep current."`
	Description string `wick:"textarea;desc=New description. Leave empty to keep current."`
	Location    string `wick:"desc=New location. Leave empty to keep current."`
	Start       string `wick:"desc=New start time (RFC3339). Leave empty to keep current."`
	End         string `wick:"desc=New end time (RFC3339). Leave empty to keep current."`
	Attendees   string `wick:"desc=Replacement attendee list, comma-separated. Leave empty to keep current attendees."`
}

// CalendarDeleteEventInput is the argument schema for calendar_delete_event.
type CalendarDeleteEventInput struct {
	CalendarID string `wick:"desc=Calendar ID. Default: primary."`
	EventID    string `wick:"required;desc=Event ID to cancel/delete. Attendees are notified."`
}

// CalendarRespondEventInput is the argument schema for calendar_respond_event.
type CalendarRespondEventInput struct {
	CalendarID string `wick:"desc=Calendar ID. Default: primary."`
	EventID    string `wick:"required;desc=Event ID to respond to."`
	Response   string `wick:"required;dropdown=accepted|declined|tentative;desc=Your RSVP response."`
}

// --- Meet input structs ---

// MeetGetSpaceInput is the argument schema for meet_get_space.
type MeetGetSpaceInput struct {
	Space string `wick:"required;desc=Meet space resource name (spaces/abc), meeting code, or full Meet URL."`
}

// MeetListConferenceRecordsInput is the argument schema for meet_list_conference_records.
type MeetListConferenceRecordsInput struct {
	Filter   string `wick:"desc=Meet filter expression. Example: space.meeting_code=\"abc-defg-hij\" or start_time>=\"2026-06-01T00:00:00Z\". Optional."`
	PageSize int    `wick:"desc=Max records to return (1-100). Default: 25."`
}

// MeetListRecordingsInput is the argument schema for meet_list_recordings.
type MeetListRecordingsInput struct {
	ConferenceRecord string `wick:"required;desc=Conference record name (conferenceRecords/abc) from meet_list_conference_records."`
}

// MeetListTranscriptsInput is the argument schema for meet_list_transcripts.
type MeetListTranscriptsInput struct {
	ConferenceRecord string `wick:"required;desc=Conference record name (conferenceRecords/abc) from meet_list_conference_records."`
}

// Operations returns all 37 operations for the Google Workspace connector.
// Operations groups every Google Workspace action into its product
// section. Group() flattens these into the Module's op list + the section
// metadata the admin detail page renders.
func Operations() []connector.Category {
	return []connector.Category{
		connector.Cat("Drive", "Browse, read, upload, share, and organize files and folders in Google Drive.",
			connector.Op("list_files", "List Files",
				"List files and folders in Google Drive. Returns file ID, name, MIME type, last modified time, size, and web view link. Leave folder_id empty to list root (My Drive).",
				ListFilesInput{}, listFiles, wickdocs.Docs{}),
			connector.Op("search_files", "Search Files",
				"Search Google Drive using Drive query syntax (e.g. name contains 'report'). Returns matched files with metadata. See Google Drive API docs for query syntax.",
				SearchFilesInput{}, searchFiles, wickdocs.Docs{}),
			connector.Op("get_file_info", "Get File Info",
				"Get metadata for a single file or folder: name, MIME type, size, owner email, sharing state, parent folder IDs, and web view link.",
				GetFileInfoInput{}, getFileInfo, wickdocs.Docs{}),
			connector.Op("get_file_content", "Read File Content",
				"Read the text content of a file. Google Docs → plain text, Google Sheets → CSV, Google Slides → plain text, other files → raw bytes (first 100 KB).",
				GetFileContentInput{}, getFileContent, wickdocs.Docs{}),
			connector.OpDestructive("upload_file", "Upload File",
				"Upload a new file to Google Drive. Returns the created file's ID and web view link. Specify folder_id to place in a folder; leave empty for root.",
				UploadFileInput{}, uploadFile, wickdocs.Docs{}),
			connector.OpDestructive("create_folder", "Create Folder",
				"Create a new folder in Google Drive. Returns the folder's ID and web view link. Specify parent_folder_id to nest under an existing folder.",
				CreateFolderInput{}, createFolder, wickdocs.Docs{}),
			connector.OpDestructive("delete_file", "Move to Trash",
				"Move a file or folder to the trash. Reversible: the owner can restore it via Google Drive UI within 30 days. Does not permanently delete.",
				DeleteFileInput{}, deleteFile, wickdocs.Docs{}),
			connector.OpDestructive("share_file", "Share File",
				"Grant access to a file or folder by email address. Role must be reader, writer, or commenter. Returns the created permission ID.",
				ShareFileInput{}, shareFile, wickdocs.Docs{}),
			connector.OpDestructive("create_doc", "Create Google Doc",
				"Create a new Google Document in Drive. Optionally supply initial plain-text content. Returns file ID and web view link.",
				CreateDocInput{}, createDoc, wickdocs.Docs{}),
			connector.OpDestructive("create_sheet", "Create Google Sheet",
				"Create a new Google Spreadsheet in Drive. Optionally supply CSV data to pre-populate the first sheet. Returns file ID and web view link.",
				CreateSheetInput{}, createSheet, wickdocs.Docs{}),
			connector.OpDestructive("create_slides", "Create Google Slides",
				"Create a new Google Slides presentation in Drive. Optionally set the title of the first slide. Returns file ID and web view link.",
				CreateSlidesInput{}, createSlides, wickdocs.Docs{}),
		),
		connector.Cat("Sheets", "Read and write cell ranges in Google Spreadsheets.",
			connector.Op("sheets_read_range", "Read Sheet Range",
				"Read cell values from a Google Spreadsheet range. Returns rows as a JSON array. Use A1 notation for range (e.g. Sheet1!A1:C10).",
				SheetsReadRangeInput{}, sheetsReadRange, wickdocs.Docs{}),
			connector.OpDestructive("sheets_append_rows", "Append Sheet Rows",
				"Append rows to a Google Spreadsheet from a CSV string. Rows are inserted after the last existing row in the table. Does not overwrite existing data.",
				SheetsAppendRowsInput{}, sheetsAppendRows, wickdocs.Docs{}),
			connector.OpDestructive("sheets_update_range", "Update Sheet Range",
				"Overwrite cell values in a specific Google Spreadsheet range with CSV data. Existing values in the range are replaced.",
				SheetsUpdateRangeInput{}, sheetsUpdateRange, wickdocs.Docs{}),
			connector.OpDestructive("sheets_clear_range", "Clear Sheet Range",
				"Clear all values from a Google Spreadsheet range. Cell formatting is preserved; only values are removed.",
				SheetsClearRangeInput{}, sheetsClearRange, wickdocs.Docs{}),
		),
		connector.Cat("Docs", "Append and edit text in Google Documents.",
			connector.OpDestructive("docs_append_text", "Append to Doc",
				"Append plain text to the end of a Google Document. The text is inserted at the last paragraph.",
				DocsAppendTextInput{}, docsAppendText, wickdocs.Docs{}),
			connector.OpDestructive("docs_replace_text", "Replace Text in Doc",
				"Find and replace all occurrences of a string throughout a Google Document. Returns the number of replacements made.",
				DocsReplaceTextInput{}, docsReplaceText, wickdocs.Docs{}),
		),
		connector.Cat("Slides", "Read and build slides in Google Slides presentations.",
			connector.Op("slides_get_content", "Get Slides Content",
				"Get the text content of all slides in a Google Slides presentation. Returns slide index, title, and body text for each slide.",
				SlidesGetContentInput{}, slidesGetContent, wickdocs.Docs{}),
			connector.OpDestructive("slides_add_slide", "Add Slide",
				"Add a new slide to a Google Slides presentation with optional title and body text. Returns the new slide's ID and index.",
				SlidesAddSlideInput{}, slidesAddSlide, wickdocs.Docs{}),
			connector.OpDestructive("slides_duplicate_slide", "Duplicate Slide",
				"Duplicate an existing slide by its 0-based index. The duplicate is inserted immediately after the original.",
				SlidesDuplicateSlideInput{}, slidesDuplicateSlide, wickdocs.Docs{}),
		),
		connector.Cat("Gmail", "Read, search, send, draft, reply to, and label email in Gmail.",
			connector.Op("gmail_list_messages", "List / Search Messages",
				"Search the mailbox using Gmail query syntax (e.g. from:x is:unread). Returns id, thread_id, from, to, subject, date, and snippet for each match. Empty result = empty array.",
				GmailListMessagesInput{}, gmailListMessages, wickdocs.Docs{}),
			connector.Op("gmail_get_message", "Get Message",
				"Read a single message in full: headers (from, to, cc, subject, date), labels, and the plain-text body. Returns the message by ID.",
				GmailGetMessageInput{}, gmailGetMessage, wickdocs.Docs{}),
			connector.OpDestructive("gmail_send", "Send Email",
				"Send a new plain-text email. Specify to, optional cc, subject, and body. Returns the sent message ID and thread ID. This actually delivers mail — not a draft.",
				GmailSendInput{}, gmailSend, wickdocs.Docs{}),
			connector.OpDestructive("gmail_create_draft", "Create Draft",
				"Create a draft email without sending it. Returns the draft ID and message ID. The user can review and send it later from Gmail.",
				GmailCreateDraftInput{}, gmailCreateDraft, wickdocs.Docs{}),
			connector.OpDestructive("gmail_reply", "Reply to Message",
				"Reply to an existing message within its thread. Subject is prefixed with Re:, threading headers are set automatically, and the reply goes to the original sender. Returns the sent message ID.",
				GmailReplyInput{}, gmailReply, wickdocs.Docs{}),
			connector.OpDestructive("gmail_modify_labels", "Modify Labels",
				"Add and/or remove labels on a message. Remove UNREAD to mark as read; remove INBOX to archive; add STARRED to star. Returns the message's current label set.",
				GmailModifyLabelsInput{}, gmailModifyLabels, wickdocs.Docs{}),
		),
		connector.Cat("Calendar", "List, read, create, update, delete, and RSVP to Google Calendar events. Create events with a Google Meet link.",
			connector.Op("calendar_list_calendars", "List Calendars",
				"List the calendars on the account. Returns id, summary, description, primary flag, and access role. Use a calendar id with the other Calendar operations.",
				CalendarListCalendarsInput{}, calendarListCalendars, wickdocs.Docs{}),
			connector.Op("calendar_list_events", "List Events",
				"List events in a calendar within an optional time window. Returns id, summary, start, end, attendees, and meet_link for each. Use RFC3339 for time_min / time_max. Default calendar is primary.",
				CalendarListEventsInput{}, calendarListEvents, wickdocs.Docs{}),
			connector.Op("calendar_get_event", "Get Event",
				"Read a single event in full: summary, description, location, start/end, attendees with RSVP status, and Meet link if any.",
				CalendarGetEventInput{}, calendarGetEvent, wickdocs.Docs{}),
			connector.OpDestructive("calendar_create_event", "Create Event",
				"Create a calendar event. Set add_meet=true to attach a Google Meet video link (returned in meet_link). Attendees are emailed an invite. Returns the created event.",
				CalendarCreateEventInput{}, calendarCreateEvent, wickdocs.Docs{}),
			connector.OpDestructive("calendar_update_event", "Update Event",
				"Patch an existing event — change time, title, location, description, or replace the attendee list. Only non-empty fields are applied. Attendees are notified. Returns the updated event.",
				CalendarUpdateEventInput{}, calendarUpdateEvent, wickdocs.Docs{}),
			connector.OpDestructive("calendar_delete_event", "Delete Event",
				"Cancel and delete an event. Attendees are notified of the cancellation. Returns the deleted event ID. Not reversible via this connector.",
				CalendarDeleteEventInput{}, calendarDeleteEvent, wickdocs.Docs{}),
			connector.OpDestructive("calendar_respond_event", "RSVP to Event",
				"Set your RSVP (accepted / declined / tentative) on an event you were invited to. Returns the event ID and your new response status.",
				CalendarRespondEventInput{}, calendarRespondEvent, wickdocs.Docs{}),
		),
		connector.Cat("Meet", "Read Google Meet conference data — spaces, past meetings, recordings, and transcripts. To create a Meet link, use Calendar → Create Event with add_meet=true.",
			connector.Op("meet_get_space", "Get Meet Space",
				"Get a Meet space's config and active conference by resource name, meeting code, or Meet URL. Returns meeting_uri, meeting_code, access_type, and the active conference record (if a call is live).",
				MeetGetSpaceInput{}, meetGetSpace, wickdocs.Docs{}),
			connector.Op("meet_list_conference_records", "List Past Meetings",
				"List past meetings (conference records) for the account, optionally filtered by Meet filter syntax. Returns name, start_time, end_time, and space for each. Use a record name with the recordings / transcripts operations.",
				MeetListConferenceRecordsInput{}, meetListConferenceRecords, wickdocs.Docs{}),
			connector.Op("meet_list_recordings", "List Recordings",
				"List the recordings produced for a conference record. Returns name, state, start/end time, and the Drive file id of each recording (when available).",
				MeetListRecordingsInput{}, meetListRecordings, wickdocs.Docs{}),
			connector.Op("meet_list_transcripts", "List Transcripts",
				"List the transcripts produced for a conference record. Returns name, state, start/end time, and the Google Docs document id of each transcript (when available).",
				MeetListTranscriptsInput{}, meetListTranscripts, wickdocs.Docs{}),
		),
	}
}

// HealthCheck probes the token's granted scopes via Google tokeninfo and
// reports per-operation availability.
func HealthCheck(c *connector.Ctx) ([]connector.OpHealth, error) {
	scopeStr, err := probeTokenScopes(c)
	if err != nil {
		return nil, fmt.Errorf("probe token scopes: %w", err)
	}
	granted := normalizeGranted(scopeStr)
	out := make([]connector.OpHealth, 0, len(opScopes))
	for op, required := range opScopes {
		ok, missing := evalScopes(required, granted)
		h := connector.OpHealth{Key: op, OK: ok}
		if !ok {
			h.Reason = "needs scope: " + strings.Join(missing, " ")
		}
		out = append(out, h)
	}
	return out, nil
}

func listFiles(c *connector.Ctx) (any, error) {
	pageSize := c.InputInt("page_size")
	if pageSize <= 0 {
		pageSize = 50
	}
	orderBy := c.Input("order_by")
	if orderBy == "" {
		orderBy = "modifiedTime desc"
	}
	params := buildListParams(c.Input("folder_id"), pageSize, orderBy)
	body, err := driveGet(c, "/files", params)
	if err != nil {
		return nil, err
	}
	return parseFileList(body)
}

func searchFiles(c *connector.Ctx) (any, error) {
	query, err := validateString(c.Input("query"), "query")
	if err != nil {
		return nil, err
	}
	pageSize := c.InputInt("page_size")
	if pageSize <= 0 {
		pageSize = 50
	}
	params := buildSearchParams(query, pageSize)
	body, err := driveGet(c, "/files", params)
	if err != nil {
		return nil, err
	}
	return parseFileList(body)
}

func getFileInfo(c *connector.Ctx) (any, error) {
	fileID, err := validateString(c.Input("file_id"), "file_id")
	if err != nil {
		return nil, err
	}
	body, err := driveGet(c, "/files/"+fileID, buildDetailParams())
	if err != nil {
		return nil, err
	}
	var f driveFile
	if err := json.Unmarshal(body, &f); err != nil {
		return nil, fmt.Errorf("parse file info: %w", err)
	}
	return shapeFileDetail(f), nil
}

func getFileContent(c *connector.Ctx) (any, error) {
	fileID, err := validateString(c.Input("file_id"), "file_id")
	if err != nil {
		return nil, err
	}
	return fetchFileContent(c, fileID)
}

func uploadFile(c *connector.Ctx) (any, error) {
	name, err := validateString(c.Input("name"), "name")
	if err != nil {
		return nil, err
	}
	content, err := validateString(c.Input("content"), "content")
	if err != nil {
		return nil, err
	}
	body, err := uploadMultipart(c, name, content, c.Input("folder_id"), c.Input("mime_type"))
	if err != nil {
		return nil, err
	}
	var f driveFile
	if err := json.Unmarshal(body, &f); err != nil {
		return nil, fmt.Errorf("parse upload response: %w", err)
	}
	return shapeFileItem(f), nil
}

func createFolder(c *connector.Ctx) (any, error) {
	name, err := validateString(c.Input("name"), "name")
	if err != nil {
		return nil, err
	}
	return createDriveFolder(c, name, c.Input("parent_folder_id"))
}

func deleteFile(c *connector.Ctx) (any, error) {
	fileID, err := validateString(c.Input("file_id"), "file_id")
	if err != nil {
		return nil, err
	}
	return trashFile(c, fileID)
}

func shareFile(c *connector.Ctx) (any, error) {
	fileID, err := validateString(c.Input("file_id"), "file_id")
	if err != nil {
		return nil, err
	}
	email, err := validateString(c.Input("email"), "email")
	if err != nil {
		return nil, err
	}
	role, err := validateString(c.Input("role"), "role")
	if err != nil {
		return nil, err
	}
	return createPermission(c, fileID, email, role)
}

func createDoc(c *connector.Ctx) (any, error) {
	name, err := validateString(c.Input("name"), "name")
	if err != nil {
		return nil, err
	}
	return createWorkspaceFile(c, name, "application/vnd.google-apps.document",
		c.Input("folder_id"), c.Input("content"), "text/plain")
}

func createSheet(c *connector.Ctx) (any, error) {
	name, err := validateString(c.Input("name"), "name")
	if err != nil {
		return nil, err
	}
	return createWorkspaceFile(c, name, "application/vnd.google-apps.spreadsheet",
		c.Input("folder_id"), c.Input("csv_data"), "text/csv")
}

func createSlides(c *connector.Ctx) (any, error) {
	name, err := validateString(c.Input("name"), "name")
	if err != nil {
		return nil, err
	}
	return createWorkspaceFileAndSetTitle(c, name, c.Input("folder_id"), c.Input("first_slide_text"))
}

func sheetsReadRange(c *connector.Ctx) (any, error) {
	fileID, err := validateString(c.Input("file_id"), "file_id")
	if err != nil {
		return nil, err
	}
	rangeStr, err := validateString(c.Input("range"), "range")
	if err != nil {
		return nil, err
	}
	return readSheetRange(c, fileID, rangeStr)
}

func sheetsAppendRows(c *connector.Ctx) (any, error) {
	fileID, err := validateString(c.Input("file_id"), "file_id")
	if err != nil {
		return nil, err
	}
	rangeStr, err := validateString(c.Input("range"), "range")
	if err != nil {
		return nil, err
	}
	csvData, err := validateString(c.Input("csv_data"), "csv_data")
	if err != nil {
		return nil, err
	}
	return appendSheetRows(c, fileID, rangeStr, csvData)
}

func sheetsUpdateRange(c *connector.Ctx) (any, error) {
	fileID, err := validateString(c.Input("file_id"), "file_id")
	if err != nil {
		return nil, err
	}
	rangeStr, err := validateString(c.Input("range"), "range")
	if err != nil {
		return nil, err
	}
	csvData, err := validateString(c.Input("csv_data"), "csv_data")
	if err != nil {
		return nil, err
	}
	return updateSheetRange(c, fileID, rangeStr, csvData)
}

func sheetsClearRange(c *connector.Ctx) (any, error) {
	fileID, err := validateString(c.Input("file_id"), "file_id")
	if err != nil {
		return nil, err
	}
	rangeStr, err := validateString(c.Input("range"), "range")
	if err != nil {
		return nil, err
	}
	cleared, err := clearSheetRange(c, fileID, rangeStr)
	if err != nil {
		return nil, err
	}
	return map[string]string{"cleared_range": cleared}, nil
}

func docsAppendText(c *connector.Ctx) (any, error) {
	fileID, err := validateString(c.Input("file_id"), "file_id")
	if err != nil {
		return nil, err
	}
	text, err := validateString(c.Input("text"), "text")
	if err != nil {
		return nil, err
	}
	return appendDocText(c, fileID, text)
}

func docsReplaceText(c *connector.Ctx) (any, error) {
	fileID, err := validateString(c.Input("file_id"), "file_id")
	if err != nil {
		return nil, err
	}
	find, err := validateString(c.Input("find"), "find")
	if err != nil {
		return nil, err
	}
	replace, err := validateString(c.Input("replace"), "replace")
	if err != nil {
		return nil, err
	}
	return replaceDocText(c, fileID, find, replace, c.InputBool("match_case"))
}

func slidesGetContent(c *connector.Ctx) (any, error) {
	fileID, err := validateString(c.Input("file_id"), "file_id")
	if err != nil {
		return nil, err
	}
	return getPresentationContent(c, fileID)
}

func slidesAddSlide(c *connector.Ctx) (any, error) {
	fileID, err := validateString(c.Input("file_id"), "file_id")
	if err != nil {
		return nil, err
	}
	layout := c.Input("layout")
	if layout == "" {
		layout = "TITLE_AND_BODY"
	}
	insertAt := c.InputInt("insert_at_index")
	if insertAt == 0 {
		insertAt = -1
	}
	return addSlide(c, fileID, c.Input("title"), c.Input("body"), layout, insertAt)
}

func slidesDuplicateSlide(c *connector.Ctx) (any, error) {
	fileID, err := validateString(c.Input("file_id"), "file_id")
	if err != nil {
		return nil, err
	}
	return duplicateSlide(c, fileID, c.InputInt("slide_index"))
}

// --- Gmail handlers ---

func gmailListMessages(c *connector.Ctx) (any, error) {
	max := c.InputInt("max_results")
	if max <= 0 {
		max = 20
	}
	return listMessages(c, strings.TrimSpace(c.Input("query")), max)
}

func gmailGetMessage(c *connector.Ctx) (any, error) {
	id, err := validateString(c.Input("message_id"), "message_id")
	if err != nil {
		return nil, err
	}
	return getMessage(c, id)
}

func gmailSend(c *connector.Ctx) (any, error) {
	to, err := validateString(c.Input("to"), "to")
	if err != nil {
		return nil, err
	}
	subject, err := validateString(c.Input("subject"), "subject")
	if err != nil {
		return nil, err
	}
	body, err := validateString(c.Input("body"), "body")
	if err != nil {
		return nil, err
	}
	raw := buildRFC822(to, c.Input("cc"), subject, body, "")
	return sendMessage(c, raw, "")
}

func gmailCreateDraft(c *connector.Ctx) (any, error) {
	raw := buildRFC822(c.Input("to"), c.Input("cc"), c.Input("subject"), c.Input("body"), "")
	return createDraft(c, raw)
}

func gmailReply(c *connector.Ctx) (any, error) {
	id, err := validateString(c.Input("message_id"), "message_id")
	if err != nil {
		return nil, err
	}
	body, err := validateString(c.Input("body"), "body")
	if err != nil {
		return nil, err
	}
	return replyMessage(c, id, body)
}

func gmailModifyLabels(c *connector.Ctx) (any, error) {
	id, err := validateString(c.Input("message_id"), "message_id")
	if err != nil {
		return nil, err
	}
	add := splitCSVList(c.Input("add_labels"))
	remove := splitCSVList(c.Input("remove_labels"))
	if len(add) == 0 && len(remove) == 0 {
		return nil, fmt.Errorf("at least one of add_labels or remove_labels is required")
	}
	return modifyLabels(c, id, add, remove)
}

// --- Calendar handlers ---

func calendarListCalendars(c *connector.Ctx) (any, error) {
	return listCalendars(c)
}

func calendarListEvents(c *connector.Ctx) (any, error) {
	max := c.InputInt("max_results")
	if max <= 0 {
		max = 50
	}
	return listEvents(c, calendarIDOrPrimary(c), c.Input("time_min"), c.Input("time_max"), c.Input("query"), max)
}

func calendarGetEvent(c *connector.Ctx) (any, error) {
	eventID, err := validateString(c.Input("event_id"), "event_id")
	if err != nil {
		return nil, err
	}
	return getEvent(c, calendarIDOrPrimary(c), eventID)
}

func calendarCreateEvent(c *connector.Ctx) (any, error) {
	summary, err := validateString(c.Input("summary"), "summary")
	if err != nil {
		return nil, err
	}
	start, err := validateString(c.Input("start"), "start")
	if err != nil {
		return nil, err
	}
	end, err := validateString(c.Input("end"), "end")
	if err != nil {
		return nil, err
	}
	ev := buildEventBody(summary, c.Input("description"), c.Input("location"), start, end, c.Input("attendees"))
	return createEvent(c, calendarIDOrPrimary(c), ev, c.InputBool("add_meet"))
}

func calendarUpdateEvent(c *connector.Ctx) (any, error) {
	eventID, err := validateString(c.Input("event_id"), "event_id")
	if err != nil {
		return nil, err
	}
	patch := buildEventPatch(c.Input("summary"), c.Input("description"), c.Input("location"),
		c.Input("start"), c.Input("end"), c.Input("attendees"))
	if len(patch) == 0 {
		return nil, fmt.Errorf("at least one field to update is required")
	}
	return updateEvent(c, calendarIDOrPrimary(c), eventID, patch)
}

func calendarDeleteEvent(c *connector.Ctx) (any, error) {
	eventID, err := validateString(c.Input("event_id"), "event_id")
	if err != nil {
		return nil, err
	}
	return deleteEvent(c, calendarIDOrPrimary(c), eventID)
}

func calendarRespondEvent(c *connector.Ctx) (any, error) {
	eventID, err := validateString(c.Input("event_id"), "event_id")
	if err != nil {
		return nil, err
	}
	response, err := validateString(c.Input("response"), "response")
	if err != nil {
		return nil, err
	}
	return respondEvent(c, calendarIDOrPrimary(c), eventID, response)
}

// --- Meet handlers ---

func meetGetSpace(c *connector.Ctx) (any, error) {
	space, err := validateString(c.Input("space"), "space")
	if err != nil {
		return nil, err
	}
	return getMeetSpace(c, space)
}

func meetListConferenceRecords(c *connector.Ctx) (any, error) {
	pageSize := c.InputInt("page_size")
	if pageSize <= 0 {
		pageSize = 25
	}
	return listConferenceRecords(c, c.Input("filter"), pageSize)
}

func meetListRecordings(c *connector.Ctx) (any, error) {
	rec, err := validateString(c.Input("conference_record"), "conference_record")
	if err != nil {
		return nil, err
	}
	return listRecordings(c, rec)
}

func meetListTranscripts(c *connector.Ctx) (any, error) {
	rec, err := validateString(c.Input("conference_record"), "conference_record")
	if err != nil {
		return nil, err
	}
	return listTranscripts(c, rec)
}
