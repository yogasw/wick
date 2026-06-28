// Command google_workspace runs the Google Workspace connector as an external
// wick plugin: a standalone binary the host downloads and runs over gRPC. It
// wraps Google Drive, Sheets, Docs, Slides, Gmail, Calendar, and Meet REST APIs
// for LLM consumption.
//
// Purpose: Provides Meta, Configs, 38 Operations across 7 categories, and
// HealthCheck. Each user authenticates their own Google account via OAuth2
// (Connect Account button). Operations are split read-only (list, search,
// info, content, get) vs mutating (upload, create, delete, share, send, RSVP).
// Mutating ops are marked destructive so the LLM confirms before executing;
// they are enabled by default like every other op.
//
// Caller:   main.go → wickplugin.Serve(Module())
// Dependencies: service.go, repo_*.go, oauth.go
// Side Effects: outbound HTTPS calls on Execute; none at init time.
package main

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/yogasw/wick/pkg/connector"
	"github.com/yogasw/wick/pkg/entity"
	"github.com/yogasw/wick/pkg/wickdocs"
	"github.com/yogasw/wick/plugins/tags"
)

// Module assembles the full connector definition served by main.go. It mirrors
// the in-tree registry record this connector used to have: Meta + Configs +
// Operations + HealthCheck, plus the OAuth descriptor (GetUserIdentity runs in
// this subprocess, invoked by the host over gRPC's ResolveIdentity) and the
// SSO-on access defaults. DefaultTags come from the shared plugins/tags catalog
// so the plugin lands in the same connector-list section (API) as the built-ins.
func Module() connector.Module {
	m := Meta()
	m.DefaultTags = []entity.DefaultTag{tags.Connector, tags.API}
	return connector.Module{
		Meta:        m,
		Configs:     entity.StructToConfigs(Configs{}),
		Operations:  Operations(),
		HealthCheck: HealthCheck,
		OAuth:       OAuthMeta(),
		// OAuth-only — no bot-token path. New rows start SSO-on and let
		// tag-scoped users connect their own account, so there's no extra
		// Enable-SSO step before the first Connect.
		DefaultAccess: connector.AccessDefaults{EnableSSO: true, AllowOthersConnectSSO: true},
	}
}

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

// Input types live in input_<category>.go (input_drive.go, input_sheets.go,
// input_docs.go, input_slides.go, input_gmail.go, input_calendar.go,
// input_meet.go) — one struct per operation, grouped to match the repo_*.go split.

// Operations returns all 38 operations for the Google Workspace connector.
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
		connector.Cat("Meet", "Create a standalone Google Meet link, and read Meet conference data — spaces, past meetings, recordings, and transcripts. To attach a Meet link to a calendar invite instead, use Calendar → Create Event with add_meet=true.",
			connector.OpDestructive("meet_create_space", "Create Meet Link",
				"Create a new standalone Google Meet space (not tied to a calendar event). Returns meeting_uri (the shareable link) and meeting_code. Use access_type to control who can join. For a meeting with a scheduled time and invitees, use calendar_create_event with add_meet=true instead.",
				MeetCreateSpaceInput{}, meetCreateSpace, wickdocs.Docs{}),
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

func meetCreateSpace(c *connector.Ctx) (any, error) {
	return createMeetSpace(c, c.Input("access_type"))
}

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
