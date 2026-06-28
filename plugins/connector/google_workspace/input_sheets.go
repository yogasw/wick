package main

// Sheets input structs — one per operation.

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
