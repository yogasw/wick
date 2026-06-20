// Package googleworkspace — service.go: Pure-Go types, validators, URL builders, and response shapers.
//
// Purpose: Provides the stable output types for all 20 operations, scope validation
// logic for HealthCheck, and URL parameter builders for the repo layer.
//
// Caller:   connector.go handlers (validation), repo.go (param builders),
//           connector.go HealthCheck (scope eval)
// Dependencies: standard library only (encoding/csv, encoding/json, fmt, net/url, strings)
// Side Effects: none (pure functions + type definitions)
package googleworkspace

import (
	"encoding/csv"
	"encoding/json"
	"fmt"
	"net/url"
	"strings"
)

// FileItem is the standard response shape for file listing operations.
type FileItem struct {
	ID           string `json:"id"`
	Name         string `json:"name"`
	MimeType     string `json:"mimeType"`
	ModifiedTime string `json:"modifiedTime"`
	WebViewLink  string `json:"webViewLink"`
	Size         string `json:"size"`
}

// FileDetail is the extended response shape for get_file_info.
type FileDetail struct {
	FileItem
	OwnerEmail string   `json:"ownerEmail"`
	Shared     bool     `json:"shared"`
	Parents    []string `json:"parents"`
}

// FileContent is the response shape for get_file_content.
type FileContent struct {
	ID       string `json:"id"`
	Name     string `json:"name"`
	MimeType string `json:"mimeType"`
	Content  string `json:"content"`
}

// WorkspaceFileResult is the response for create_doc, create_sheet, create_slides.
type WorkspaceFileResult struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	WebViewLink string `json:"web_view_link"`
}

// SheetsReadResult is the response for sheets_read_range.
type SheetsReadResult struct {
	Range    string     `json:"range"`
	Rows     [][]string `json:"rows"`
	RowCount int        `json:"row_count"`
}

// SheetsWriteResult is the response for sheets_append_rows and sheets_update_range.
type SheetsWriteResult struct {
	UpdatedRange string `json:"updated_range"`
	UpdatedCells int    `json:"updated_cells"`
}

// DocResult is the response for docs_append_text and docs_replace_text.
type DocResult struct {
	RevisionID         string `json:"revision_id"`
	OccurrencesChanged int    `json:"occurrences_changed,omitempty"`
}

// SlideInfo is one slide's text content within a presentation.
type SlideInfo struct {
	Index    int    `json:"index"`
	SlideID  string `json:"slide_id"`
	Title    string `json:"title"`
	BodyText string `json:"body_text"`
}

// PresentationContent is the response for slides_get_content.
type PresentationContent struct {
	Title      string      `json:"title"`
	SlideCount int         `json:"slide_count"`
	Slides     []SlideInfo `json:"slides"`
}

// SlideAddResult is the response for slides_add_slide and slides_duplicate_slide.
type SlideAddResult struct {
	SlideID    string `json:"slide_id"`
	SlideIndex int    `json:"slide_index"`
}

// --- Gmail response types ---

// MailMessageSummary is the listing shape for gmail_list_messages.
type MailMessageSummary struct {
	ID       string `json:"id"`
	ThreadID string `json:"thread_id"`
	From     string `json:"from"`
	To       string `json:"to"`
	Subject  string `json:"subject"`
	Date     string `json:"date"`
	Snippet  string `json:"snippet"`
}

// MailMessage is the full-detail shape for gmail_get_message.
type MailMessage struct {
	MailMessageSummary
	Cc      string   `json:"cc"`
	Labels  []string `json:"labels"`
	Body    string   `json:"body"`
}

// MailSendResult is the response for gmail_send and gmail_reply.
type MailSendResult struct {
	ID       string `json:"id"`
	ThreadID string `json:"thread_id"`
}

// MailDraftResult is the response for gmail_create_draft.
type MailDraftResult struct {
	DraftID   string `json:"draft_id"`
	MessageID string `json:"message_id"`
}

// MailLabelResult is the response for gmail_modify_labels.
type MailLabelResult struct {
	ID     string   `json:"id"`
	Labels []string `json:"labels"`
}

// --- Calendar response types ---

// CalendarSummary is one calendar in calendar_list_calendars.
type CalendarSummary struct {
	ID          string `json:"id"`
	Summary     string `json:"summary"`
	Description string `json:"description"`
	Primary     bool   `json:"primary"`
	AccessRole  string `json:"access_role"`
}

// EventAttendee is one attendee on a calendar event.
type EventAttendee struct {
	Email          string `json:"email"`
	ResponseStatus string `json:"response_status"`
}

// CalendarEvent is the shape for list_events, get_event, create_event, update_event.
type CalendarEvent struct {
	ID          string          `json:"id"`
	Status      string          `json:"status"`
	Summary     string          `json:"summary"`
	Description string          `json:"description"`
	Location    string          `json:"location"`
	Start       string          `json:"start"`
	End         string          `json:"end"`
	Attendees   []EventAttendee `json:"attendees,omitempty"`
	MeetLink    string          `json:"meet_link,omitempty"`
	HTMLLink    string          `json:"html_link"`
}

// EventDeleteResult is the response for calendar_delete_event.
type EventDeleteResult struct {
	EventID string `json:"event_id"`
	Deleted bool   `json:"deleted"`
}

// EventRespondResult is the response for calendar_respond_event.
type EventRespondResult struct {
	EventID        string `json:"event_id"`
	ResponseStatus string `json:"response_status"`
}

// --- Meet response types ---

// MeetSpace is the response for meet_get_space.
type MeetSpace struct {
	Name             string `json:"name"`
	MeetingURI       string `json:"meeting_uri"`
	MeetingCode      string `json:"meeting_code"`
	AccessType       string `json:"access_type"`
	ActiveConference string `json:"active_conference,omitempty"`
}

// MeetConferenceRecord is one past meeting in meet_list_conference_records.
type MeetConferenceRecord struct {
	Name      string `json:"name"`
	StartTime string `json:"start_time"`
	EndTime   string `json:"end_time"`
	Space     string `json:"space"`
}

// MeetRecording is one recording in meet_list_recordings.
type MeetRecording struct {
	Name       string `json:"name"`
	State      string `json:"state"`
	StartTime  string `json:"start_time"`
	EndTime    string `json:"end_time"`
	DriveFile  string `json:"drive_file,omitempty"`
}

// MeetTranscript is one transcript in meet_list_transcripts.
type MeetTranscript struct {
	Name      string `json:"name"`
	State     string `json:"state"`
	StartTime string `json:"start_time"`
	EndTime   string `json:"end_time"`
	DocID     string `json:"doc_id,omitempty"`
}

// driveFile is the raw structure returned by the Drive API files resource.
type driveFile struct {
	ID           string   `json:"id"`
	Name         string   `json:"name"`
	MimeType     string   `json:"mimeType"`
	ModifiedTime string   `json:"modifiedTime"`
	WebViewLink  string   `json:"webViewLink"`
	Size         string   `json:"size"`
	Shared       bool     `json:"shared"`
	Parents      []string `json:"parents"`
	Owners       []struct {
		EmailAddress string `json:"emailAddress"`
	} `json:"owners"`
}

const driveFileFields = "id,name,mimeType,modifiedTime,webViewLink,size"
const driveDetailFields = "id,name,mimeType,modifiedTime,webViewLink,size,shared,parents,owners"

// opScopes maps each operation key to alternative scope groups (OR of ANDs).
var opScopes = map[string][][]string{
	"list_files": {
		{"https://www.googleapis.com/auth/drive.readonly"},
		{"https://www.googleapis.com/auth/drive"},
	},
	"search_files": {
		{"https://www.googleapis.com/auth/drive.readonly"},
		{"https://www.googleapis.com/auth/drive"},
	},
	"get_file_info": {
		{"https://www.googleapis.com/auth/drive.readonly"},
		{"https://www.googleapis.com/auth/drive"},
	},
	"get_file_content": {
		{"https://www.googleapis.com/auth/drive.readonly"},
		{"https://www.googleapis.com/auth/drive"},
	},
	"upload_file":   {{"https://www.googleapis.com/auth/drive"}},
	"create_folder": {{"https://www.googleapis.com/auth/drive"}},
	"delete_file":   {{"https://www.googleapis.com/auth/drive"}},
	"share_file":    {{"https://www.googleapis.com/auth/drive"}},
	// Workspace file creation uses the Drive scope.
	"create_doc":    {{"https://www.googleapis.com/auth/drive"}},
	"create_sheet":  {{"https://www.googleapis.com/auth/drive"}},
	"create_slides": {{"https://www.googleapis.com/auth/drive"}},
	// Sheets ops.
	"sheets_read_range": {
		{"https://www.googleapis.com/auth/spreadsheets.readonly"},
		{"https://www.googleapis.com/auth/spreadsheets"},
		{"https://www.googleapis.com/auth/drive"},
	},
	"sheets_append_rows":  {{"https://www.googleapis.com/auth/spreadsheets"}},
	"sheets_update_range": {{"https://www.googleapis.com/auth/spreadsheets"}},
	"sheets_clear_range":  {{"https://www.googleapis.com/auth/spreadsheets"}},
	// Docs ops.
	"docs_append_text":  {{"https://www.googleapis.com/auth/documents"}},
	"docs_replace_text": {{"https://www.googleapis.com/auth/documents"}},
	// Slides ops.
	"slides_get_content": {
		{"https://www.googleapis.com/auth/presentations.readonly"},
		{"https://www.googleapis.com/auth/presentations"},
	},
	"slides_add_slide":       {{"https://www.googleapis.com/auth/presentations"}},
	"slides_duplicate_slide": {{"https://www.googleapis.com/auth/presentations"}},
	// Gmail ops.
	"gmail_list_messages": {
		{"https://www.googleapis.com/auth/gmail.readonly"},
		{"https://www.googleapis.com/auth/gmail.modify"},
		{"https://mail.google.com/"},
	},
	"gmail_get_message": {
		{"https://www.googleapis.com/auth/gmail.readonly"},
		{"https://www.googleapis.com/auth/gmail.modify"},
		{"https://mail.google.com/"},
	},
	"gmail_send": {
		{"https://www.googleapis.com/auth/gmail.send"},
		{"https://mail.google.com/"},
	},
	"gmail_reply": {
		{"https://www.googleapis.com/auth/gmail.send"},
		{"https://mail.google.com/"},
	},
	"gmail_create_draft": {
		{"https://www.googleapis.com/auth/gmail.compose"},
		{"https://www.googleapis.com/auth/gmail.modify"},
		{"https://mail.google.com/"},
	},
	"gmail_modify_labels": {
		{"https://www.googleapis.com/auth/gmail.modify"},
		{"https://mail.google.com/"},
	},
	// Calendar ops.
	"calendar_list_calendars": {
		{"https://www.googleapis.com/auth/calendar.readonly"},
		{"https://www.googleapis.com/auth/calendar"},
	},
	"calendar_list_events": {
		{"https://www.googleapis.com/auth/calendar.readonly"},
		{"https://www.googleapis.com/auth/calendar.events.readonly"},
		{"https://www.googleapis.com/auth/calendar"},
		{"https://www.googleapis.com/auth/calendar.events"},
	},
	"calendar_get_event": {
		{"https://www.googleapis.com/auth/calendar.readonly"},
		{"https://www.googleapis.com/auth/calendar.events.readonly"},
		{"https://www.googleapis.com/auth/calendar"},
		{"https://www.googleapis.com/auth/calendar.events"},
	},
	"calendar_create_event": {
		{"https://www.googleapis.com/auth/calendar.events"},
		{"https://www.googleapis.com/auth/calendar"},
	},
	"calendar_update_event": {
		{"https://www.googleapis.com/auth/calendar.events"},
		{"https://www.googleapis.com/auth/calendar"},
	},
	"calendar_delete_event": {
		{"https://www.googleapis.com/auth/calendar.events"},
		{"https://www.googleapis.com/auth/calendar"},
	},
	"calendar_respond_event": {
		{"https://www.googleapis.com/auth/calendar.events"},
		{"https://www.googleapis.com/auth/calendar"},
	},
	// Meet ops (read-only conference data).
	"meet_get_space": {
		{"https://www.googleapis.com/auth/meetings.space.readonly"},
		{"https://www.googleapis.com/auth/meetings.space.created"},
	},
	"meet_list_conference_records": {
		{"https://www.googleapis.com/auth/meetings.space.readonly"},
		{"https://www.googleapis.com/auth/meetings.space.created"},
	},
	"meet_list_recordings": {
		{"https://www.googleapis.com/auth/meetings.space.readonly"},
		{"https://www.googleapis.com/auth/meetings.space.created"},
	},
	"meet_list_transcripts": {
		{"https://www.googleapis.com/auth/meetings.space.readonly"},
		{"https://www.googleapis.com/auth/meetings.space.created"},
	},
}

// normalizeGranted parses a space-separated scope string and applies the
// drive → drive.readonly implication (drive is a superset of drive.readonly).
func normalizeGranted(scopeStr string) map[string]bool {
	m := make(map[string]bool)
	for _, s := range strings.Fields(scopeStr) {
		m[s] = true
	}
	// drive implies drive.readonly
	if m["https://www.googleapis.com/auth/drive"] {
		m["https://www.googleapis.com/auth/drive.readonly"] = true
	}
	// spreadsheets implies spreadsheets.readonly
	if m["https://www.googleapis.com/auth/spreadsheets"] {
		m["https://www.googleapis.com/auth/spreadsheets.readonly"] = true
	}
	// presentations implies presentations.readonly
	if m["https://www.googleapis.com/auth/presentations"] {
		m["https://www.googleapis.com/auth/presentations.readonly"] = true
	}
	// calendar implies calendar.readonly + calendar.events (+ their readonly)
	if m["https://www.googleapis.com/auth/calendar"] {
		m["https://www.googleapis.com/auth/calendar.readonly"] = true
		m["https://www.googleapis.com/auth/calendar.events"] = true
	}
	// calendar.events implies calendar.events.readonly
	if m["https://www.googleapis.com/auth/calendar.events"] {
		m["https://www.googleapis.com/auth/calendar.events.readonly"] = true
	}
	// mail.google.com is full Gmail access — implies every granular gmail scope
	if m["https://mail.google.com/"] {
		m["https://www.googleapis.com/auth/gmail.readonly"] = true
		m["https://www.googleapis.com/auth/gmail.send"] = true
		m["https://www.googleapis.com/auth/gmail.compose"] = true
		m["https://www.googleapis.com/auth/gmail.modify"] = true
	}
	// gmail.modify implies readonly + compose (can read, label, and create drafts)
	if m["https://www.googleapis.com/auth/gmail.modify"] {
		m["https://www.googleapis.com/auth/gmail.readonly"] = true
		m["https://www.googleapis.com/auth/gmail.compose"] = true
	}
	return m
}

// evalScopes checks whether granted satisfies any scope group in required.
func evalScopes(required [][]string, granted map[string]bool) (ok bool, missing []string) {
	for _, group := range required {
		var miss []string
		for _, s := range group {
			if !granted[s] {
				miss = append(miss, s)
			}
		}
		if len(miss) == 0 {
			return true, nil
		}
		if missing == nil || len(miss) < len(missing) {
			missing = miss
		}
	}
	return false, missing
}

// shapeFileItem maps a driveFile to the stable FileItem output type.
func shapeFileItem(f driveFile) FileItem {
	return FileItem{
		ID:           f.ID,
		Name:         f.Name,
		MimeType:     f.MimeType,
		ModifiedTime: f.ModifiedTime,
		WebViewLink:  f.WebViewLink,
		Size:         f.Size,
	}
}

// shapeFileDetail maps a driveFile to the extended FileDetail output type.
func shapeFileDetail(f driveFile) FileDetail {
	ownerEmail := ""
	if len(f.Owners) > 0 {
		ownerEmail = f.Owners[0].EmailAddress
	}
	return FileDetail{
		FileItem:   shapeFileItem(f),
		OwnerEmail: ownerEmail,
		Shared:     f.Shared,
		Parents:    f.Parents,
	}
}

// parseFileList decodes a Drive API files list response into []FileItem.
func parseFileList(body []byte) ([]FileItem, error) {
	var resp struct {
		Files []driveFile `json:"files"`
	}
	if err := json.Unmarshal(body, &resp); err != nil {
		return nil, fmt.Errorf("parse file list: %w", err)
	}
	items := make([]FileItem, len(resp.Files))
	for i, f := range resp.Files {
		items[i] = shapeFileItem(f)
	}
	return items, nil
}

// buildListParams builds query params for the files.list endpoint.
func buildListParams(folderID string, pageSize int, orderBy string) url.Values {
	params := url.Values{}
	q := "trashed=false"
	if folderID != "" {
		q = fmt.Sprintf("'%s' in parents and trashed=false", folderID)
	}
	params.Set("q", q)
	params.Set("pageSize", fmt.Sprintf("%d", pageSize))
	params.Set("orderBy", orderBy)
	params.Set("fields", "files("+driveFileFields+")")
	return params
}

// buildSearchParams builds query params for a Drive full-text search.
func buildSearchParams(query string, pageSize int) url.Values {
	params := url.Values{}
	params.Set("q", query+" and trashed=false")
	params.Set("pageSize", fmt.Sprintf("%d", pageSize))
	params.Set("fields", "files("+driveFileFields+")")
	return params
}

// buildDetailParams builds query params for a single file metadata fetch.
func buildDetailParams() url.Values {
	params := url.Values{}
	params.Set("fields", driveDetailFields)
	return params
}

// validateString trims a string and returns an error if empty.
func validateString(val, name string) (string, error) {
	v := strings.TrimSpace(val)
	if v == "" {
		return "", fmt.Errorf("%s is required", name)
	}
	return v, nil
}

// splitCSVList splits a comma-separated string into trimmed, non-empty parts.
// Used for label IDs and attendee email lists.
func splitCSVList(s string) []string {
	var out []string
	for _, part := range strings.Split(s, ",") {
		if p := strings.TrimSpace(part); p != "" {
			out = append(out, p)
		}
	}
	return out
}

// calendarIDOrPrimary returns the calendar_id input, defaulting to "primary".
func calendarIDOrPrimary(c interface{ Input(string) string }) string {
	id := strings.TrimSpace(c.Input("calendar_id"))
	if id == "" {
		return "primary"
	}
	return id
}

// eventTimeField builds a Calendar event start/end object. A bare date
// (YYYY-MM-DD, length 10, no "T") is treated as an all-day event.
func eventTimeField(value string) map[string]any {
	if len(value) == 10 && !strings.Contains(value, "T") {
		return map[string]any{"date": value}
	}
	return map[string]any{"dateTime": value}
}

// attendeeList maps a comma-separated email string to the Calendar attendees shape.
func attendeeList(emails string) []map[string]any {
	parts := splitCSVList(emails)
	if len(parts) == 0 {
		return nil
	}
	out := make([]map[string]any, len(parts))
	for i, e := range parts {
		out[i] = map[string]any{"email": e}
	}
	return out
}

// buildEventBody assembles a full Calendar event body for create_event.
func buildEventBody(summary, description, location, start, end, attendees string) map[string]any {
	ev := map[string]any{
		"summary": summary,
		"start":   eventTimeField(start),
		"end":     eventTimeField(end),
	}
	if description != "" {
		ev["description"] = description
	}
	if location != "" {
		ev["location"] = location
	}
	if a := attendeeList(attendees); a != nil {
		ev["attendees"] = a
	}
	return ev
}

// buildEventPatch assembles a partial Calendar event body for update_event,
// including only the fields the caller supplied (non-empty).
func buildEventPatch(summary, description, location, start, end, attendees string) map[string]any {
	patch := map[string]any{}
	if summary != "" {
		patch["summary"] = summary
	}
	if description != "" {
		patch["description"] = description
	}
	if location != "" {
		patch["location"] = location
	}
	if start != "" {
		patch["start"] = eventTimeField(start)
	}
	if end != "" {
		patch["end"] = eventTimeField(end)
	}
	if a := attendeeList(attendees); a != nil {
		patch["attendees"] = a
	}
	return patch
}

// parseCSV parses a CSV string into a slice of string rows for the Sheets API.
// Handles quoted fields and embedded newlines.
func parseCSV(data string) ([][]string, error) {
	r := csv.NewReader(strings.NewReader(data))
	r.LazyQuotes = true
	records, err := r.ReadAll()
	if err != nil {
		return nil, fmt.Errorf("parse CSV: %w", err)
	}
	return records, nil
}
