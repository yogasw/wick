package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"

	"github.com/yogasw/wick/pkg/connector"
)

const sheetsBaseURL = "https://sheets.googleapis.com/v4/spreadsheets"

func sheetsGet(c *connector.Ctx, path string, params url.Values) ([]byte, error) {
	return doWithRefresh(c, func(token string) (*http.Request, error) {
		u := sheetsBaseURL + path
		if len(params) > 0 {
			u += "?" + params.Encode()
		}
		req, err := http.NewRequestWithContext(c.Context(), http.MethodGet, u, nil)
		if err != nil {
			return nil, err
		}
		req.Header.Set("Authorization", "Bearer "+token)
		return req, nil
	})
}

func sheetsPost(c *connector.Ctx, path string, body any) ([]byte, error) {
	return doWithRefresh(c, func(token string) (*http.Request, error) {
		return buildJSONRequest(c, http.MethodPost, sheetsBaseURL+path, token, body)
	})
}

func sheetsPut(c *connector.Ctx, path string, body any) ([]byte, error) {
	return doWithRefresh(c, func(token string) (*http.Request, error) {
		return buildJSONRequest(c, http.MethodPut, sheetsBaseURL+path, token, body)
	})
}

// readSheetRange reads cell values from a spreadsheet range.
func readSheetRange(c *connector.Ctx, fileID, rangeStr string) (SheetsReadResult, error) {
	rangeEnc := url.PathEscape(rangeStr)
	body, err := sheetsGet(c, "/"+fileID+"/values/"+rangeEnc, nil)
	if err != nil {
		return SheetsReadResult{}, fmt.Errorf("sheets read range: %w", err)
	}
	var resp struct {
		Range  string     `json:"range"`
		Values [][]string `json:"values"`
	}
	if err := json.Unmarshal(body, &resp); err != nil {
		return SheetsReadResult{}, fmt.Errorf("parse sheets range: %w", err)
	}
	return SheetsReadResult{
		Range:    resp.Range,
		Rows:     resp.Values,
		RowCount: len(resp.Values),
	}, nil
}

// appendSheetRows appends rows parsed from csvData to the spreadsheet.
func appendSheetRows(c *connector.Ctx, fileID, rangeStr, csvData string) (SheetsWriteResult, error) {
	rows, err := parseCSV(csvData)
	if err != nil {
		return SheetsWriteResult{}, err
	}
	values := make([][]any, len(rows))
	for i, row := range rows {
		vals := make([]any, len(row))
		for j, v := range row {
			vals[j] = v
		}
		values[i] = vals
	}
	payload := map[string]any{"values": values}
	rangeEnc := url.PathEscape(rangeStr)
	path := fmt.Sprintf("/%s/values/%s:append?valueInputOption=USER_ENTERED&insertDataOption=INSERT_ROWS", fileID, rangeEnc)
	body, err := sheetsPost(c, path, payload)
	if err != nil {
		return SheetsWriteResult{}, fmt.Errorf("sheets append: %w", err)
	}
	var resp struct {
		Updates struct {
			UpdatedRange string `json:"updatedRange"`
			UpdatedCells int    `json:"updatedCells"`
		} `json:"updates"`
	}
	if err := json.Unmarshal(body, &resp); err != nil {
		return SheetsWriteResult{}, fmt.Errorf("parse append response: %w", err)
	}
	return SheetsWriteResult{
		UpdatedRange: resp.Updates.UpdatedRange,
		UpdatedCells: resp.Updates.UpdatedCells,
	}, nil
}

// updateSheetRange overwrites cells in a range with rows from csvData.
func updateSheetRange(c *connector.Ctx, fileID, rangeStr, csvData string) (SheetsWriteResult, error) {
	rows, err := parseCSV(csvData)
	if err != nil {
		return SheetsWriteResult{}, err
	}
	values := make([][]any, len(rows))
	for i, row := range rows {
		vals := make([]any, len(row))
		for j, v := range row {
			vals[j] = v
		}
		values[i] = vals
	}
	payload := map[string]any{"range": rangeStr, "values": values}
	rangeEnc := url.PathEscape(rangeStr)
	path := fmt.Sprintf("/%s/values/%s?valueInputOption=USER_ENTERED", fileID, rangeEnc)
	body, err := sheetsPut(c, path, payload)
	if err != nil {
		return SheetsWriteResult{}, fmt.Errorf("sheets update: %w", err)
	}
	var resp struct {
		UpdatedRange string `json:"updatedRange"`
		UpdatedCells int    `json:"updatedCells"`
	}
	if err := json.Unmarshal(body, &resp); err != nil {
		return SheetsWriteResult{}, fmt.Errorf("parse update response: %w", err)
	}
	return SheetsWriteResult{UpdatedRange: resp.UpdatedRange, UpdatedCells: resp.UpdatedCells}, nil
}

// clearSheetRange clears all values from a spreadsheet range.
func clearSheetRange(c *connector.Ctx, fileID, rangeStr string) (string, error) {
	rangeEnc := url.PathEscape(rangeStr)
	path := fmt.Sprintf("/%s/values/%s:clear", fileID, rangeEnc)
	body, err := sheetsPost(c, path, map[string]any{})
	if err != nil {
		return "", fmt.Errorf("sheets clear: %w", err)
	}
	var resp struct {
		ClearedRange string `json:"clearedRange"`
	}
	if err := json.Unmarshal(body, &resp); err != nil {
		return "", fmt.Errorf("parse clear response: %w", err)
	}
	return resp.ClearedRange, nil
}
