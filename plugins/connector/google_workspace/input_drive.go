package main

// Drive input structs — one per operation.

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
