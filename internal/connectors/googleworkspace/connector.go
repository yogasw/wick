// Package googleworkspace wraps Google Drive, Sheets, Docs, and Slides REST APIs
// for LLM consumption.
//
// Purpose: Provides Meta, Configs, 11 Operations, and HealthCheck. Each user
// authenticates their own Google account via OAuth2 (Connect Account button).
// Operations are split read-only (list, search, info, content) vs mutating
// (upload, create_folder, delete, share, create_doc, create_sheet, create_slides).
// Mutating ops default to disabled.
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
		Description: "Manage files in Drive, read and write Sheets data, edit Docs content, and manage Slides presentations — all with one Google OAuth account.",
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

// Operations returns all 20 operations for the Google Workspace connector.
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
