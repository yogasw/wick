package resp

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// MockHTTPError implements the HTTPStatusCode interface for testing
type MockHTTPError struct {
	message string
	code    int
}

func (e MockHTTPError) Error() string {
	return e.message
}

func (e MockHTTPError) HTTPStatusCode() int {
	return e.code
}

func TestWriteJSON(t *testing.T) {
	tests := []struct {
		name         string
		code         int
		data         any
		expectedCode int
		expectedBody string
	}{
		{
			name:         "SUCCESS-WriteSimpleJSON_CorrectResponse",
			code:         http.StatusOK,
			data:         map[string]string{"message": "success"},
			expectedCode: http.StatusOK,
			expectedBody: `{"message":"success"}`,
		},
		{
			name:         "SUCCESS-WriteJSONWithStatusCreated_CorrectResponse",
			code:         http.StatusCreated,
			data:         map[string]any{"id": 123, "name": "test"},
			expectedCode: http.StatusCreated,
			expectedBody: `{"id":123,"name":"test"}`,
		},
		{
			name:         "SUCCESS-WriteEmptyJSON_CorrectResponse",
			code:         http.StatusNoContent,
			data:         Empty{},
			expectedCode: http.StatusNoContent,
			expectedBody: `{}`,
		},
		{
			name:         "SUCCESS-WriteNilJSON_NullResponse",
			code:         http.StatusOK,
			data:         nil,
			expectedCode: http.StatusOK,
			expectedBody: `null`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			recorder := httptest.NewRecorder()

			WriteJSON(recorder, tt.code, tt.data)

			assert.Equal(t, tt.expectedCode, recorder.Code)
			assert.Equal(t, "application/json", recorder.Header().Get("Content-Type"))
			assert.JSONEq(t, tt.expectedBody, recorder.Body.String())
		})
	}
}

func TestWriteJSONWithPaginate(t *testing.T) {
	tests := []struct {
		name         string
		code         int
		data         any
		total        int
		page         int
		limit        int
		expectedCode int
		expectedMeta Meta
	}{
		{
			name:         "SUCCESS-WritePaginatedJSON_CorrectMetadata",
			code:         http.StatusOK,
			data:         []string{"item1", "item2"},
			total:        100,
			page:         1,
			limit:        10,
			expectedCode: http.StatusOK,
			expectedMeta: Meta{Page: 1, PageTotal: 10, Total: 100},
		},
		{
			name:         "SUCCESS-WritePaginatedJSONLastPage_CorrectMetadata",
			code:         http.StatusOK,
			data:         []string{"item1"},
			total:        21,
			page:         3,
			limit:        10,
			expectedCode: http.StatusOK,
			expectedMeta: Meta{Page: 3, PageTotal: 3, Total: 21},
		},
		{
			name:         "SUCCESS-WritePaginatedJSONSingleItem_CorrectMetadata",
			code:         http.StatusOK,
			data:         []string{"item1"},
			total:        1,
			page:         1,
			limit:        10,
			expectedCode: http.StatusOK,
			expectedMeta: Meta{Page: 1, PageTotal: 1, Total: 1},
		},
		{
			name:         "SUCCESS-WritePaginatedJSONExactDivision_CorrectMetadata",
			code:         http.StatusOK,
			data:         []string{"item1", "item2"},
			total:        20,
			page:         2,
			limit:        10,
			expectedCode: http.StatusOK,
			expectedMeta: Meta{Page: 2, PageTotal: 2, Total: 20},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			recorder := httptest.NewRecorder()

			WriteJSONWithPaginate(recorder, tt.code, tt.data, tt.total, tt.page, tt.limit)

			assert.Equal(t, tt.expectedCode, recorder.Code)
			assert.Equal(t, "application/json", recorder.Header().Get("Content-Type"))

			var response DataPaginate
			err := json.Unmarshal(recorder.Body.Bytes(), &response)
			require.NoError(t, err)

			assert.Equal(t, tt.expectedMeta, response.Meta)
			assert.NotNil(t, response.Data)
		})
	}
}

func TestWriteJSONFromError(t *testing.T) {
	tests := []struct {
		name            string
		err             error
		requestID       string
		expectedCode    int
		expectedMessage string
		expectedReqID   string
	}{
		{
			name:            "SUCCESS-HTTPError_CorrectStatusAndMessage",
			err:             MockHTTPError{message: "validation failed", code: http.StatusBadRequest},
			requestID:       "req-123",
			expectedCode:    http.StatusBadRequest,
			expectedMessage: "validation failed",
			expectedReqID:   "req-123",
		},
		{
			name:            "SUCCESS-EOFError_BadRequest",
			err:             io.EOF,
			requestID:       "req-101",
			expectedCode:    http.StatusBadRequest,
			expectedMessage: "EOF",
			expectedReqID:   "req-101",
		},
		{
			name:            "SUCCESS-UnexpectedEOFError_BadRequest",
			err:             io.ErrUnexpectedEOF,
			requestID:       "req-102",
			expectedCode:    http.StatusBadRequest,
			expectedMessage: "unexpected EOF",
			expectedReqID:   "req-102",
		},
		{
			name:            "SUCCESS-StrconvSyntaxError_BadRequest",
			err:             strconv.ErrSyntax,
			requestID:       "req-103",
			expectedCode:    http.StatusBadRequest,
			expectedMessage: "invalid syntax",
			expectedReqID:   "req-103",
		},
		{
			name:            "SUCCESS-StrconvRangeError_BadRequest",
			err:             strconv.ErrRange,
			requestID:       "req-104",
			expectedCode:    http.StatusBadRequest,
			expectedMessage: "value out of range",
			expectedReqID:   "req-104",
		},
		{
			name:            "SUCCESS-GenericError_InternalServerError",
			err:             errors.New("database connection failed"),
			requestID:       "req-500",
			expectedCode:    http.StatusInternalServerError,
			expectedMessage: "Something went wrong",
			expectedReqID:   "req-500",
		},
		{
			name:            "SUCCESS-NoRequestID_EmptyRequestID",
			err:             errors.New("some error"),
			requestID:       "",
			expectedCode:    http.StatusInternalServerError,
			expectedMessage: "Something went wrong",
			expectedReqID:   "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			recorder := httptest.NewRecorder()
			if tt.requestID != "" {
				recorder.Header().Set("X-Request-Id", tt.requestID)
			}

			WriteJSONFromError(recorder, tt.err)

			assert.Equal(t, tt.expectedCode, recorder.Code)
			assert.Equal(t, "application/json", recorder.Header().Get("Content-Type"))

			var response HTTPError
			err := json.Unmarshal(recorder.Body.Bytes(), &response)
			require.NoError(t, err)

			assert.Equal(t, tt.expectedMessage, response.Message)
			assert.Equal(t, tt.expectedReqID, response.RequestID)
		})
	}
}

func TestMeta(t *testing.T) {
	tests := []struct {
		name     string
		meta     Meta
		expected string
	}{
		{
			name:     "SUCCESS-MetaJSON_CorrectSerialization",
			meta:     Meta{Page: 1, PageTotal: 10, Total: 100},
			expected: `{"page":1,"page_total":10,"total":100}`,
		},
		{
			name:     "SUCCESS-MetaZeroValues_CorrectSerialization",
			meta:     Meta{Page: 0, PageTotal: 0, Total: 0},
			expected: `{"page":0,"page_total":0,"total":0}`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			jsonData, err := json.Marshal(tt.meta)
			require.NoError(t, err)
			assert.JSONEq(t, tt.expected, string(jsonData))
		})
	}
}

func TestDataPaginate(t *testing.T) {
	tests := []struct {
		name     string
		data     DataPaginate
		expected string
	}{
		{
			name: "SUCCESS-DataPaginateJSON_CorrectSerialization",
			data: DataPaginate{
				Data: []string{"item1", "item2"},
				Meta: Meta{Page: 1, PageTotal: 5, Total: 50},
			},
			expected: `{"data":["item1","item2"],"meta":{"page":1,"page_total":5,"total":50}}`,
		},
		{
			name: "SUCCESS-DataPaginateEmptyData_CorrectSerialization",
			data: DataPaginate{
				Data: []string{},
				Meta: Meta{Page: 1, PageTotal: 0, Total: 0},
			},
			expected: `{"data":[],"meta":{"page":1,"page_total":0,"total":0}}`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			jsonData, err := json.Marshal(tt.data)
			require.NoError(t, err)
			assert.JSONEq(t, tt.expected, string(jsonData))
		})
	}
}

func TestHTTPError(t *testing.T) {
	tests := []struct {
		name     string
		httpErr  HTTPError
		expected string
	}{
		{
			name:     "SUCCESS-HTTPErrorJSON_CorrectSerialization",
			httpErr:  HTTPError{Message: "validation failed", RequestID: "req-123"},
			expected: `{"message":"validation failed","request_id":"req-123"}`,
		},
		{
			name:     "SUCCESS-HTTPErrorEmptyRequestID_CorrectSerialization",
			httpErr:  HTTPError{Message: "error occurred", RequestID: ""},
			expected: `{"message":"error occurred","request_id":""}`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			jsonData, err := json.Marshal(tt.httpErr)
			require.NoError(t, err)
			assert.JSONEq(t, tt.expected, string(jsonData))
		})
	}
}

func TestEmpty(t *testing.T) {
	t.Run("SUCCESS-EmptyJSON_CorrectSerialization", func(t *testing.T) {
		empty := Empty{}
		jsonData, err := json.Marshal(empty)
		require.NoError(t, err)
		assert.JSONEq(t, `{}`, string(jsonData))
	})
}
