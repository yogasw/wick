package resp

import (
	"encoding/json"
	"errors"
	"io"
	"math"
	"net/http"
	"strconv"

	"github.com/go-playground/validator/v10"
)

type Meta struct {
	Page      int `json:"page"`
	PageTotal int `json:"page_total"`
	Total     int `json:"total"`
}

type DataPaginate struct {
	Data any  `json:"data"`
	Meta Meta `json:"meta"`
}

type HTTPError struct {
	Message   string `json:"message"`
	RequestID string `json:"request_id"`
}

type Empty struct{}

func WriteJSON(w http.ResponseWriter, code int, data any) {
	w.Header().Add("Content-Type", "application/json")
	w.WriteHeader(code)
	jsonData, _ := json.Marshal(data)
	w.Write(jsonData)
}

func WriteJSONWithPaginate(w http.ResponseWriter, code int, data any, total int, page int, limit int) {
	w.Header().Add("Content-Type", "application/json")
	w.WriteHeader(code)

	totalPages := int(math.Ceil(float64(total) / float64(limit)))
	meta := Meta{
		Page:      page,
		PageTotal: totalPages,
		Total:     total,
	}

	jsonData, _ := json.Marshal(DataPaginate{
		Data: data,
		Meta: meta,
	})

	w.Write(jsonData)
}

func WriteJSONFromError(w http.ResponseWriter, err error) {
	code := http.StatusInternalServerError
	msg := "Something went wrong"

	var httpErr interface{ HTTPStatusCode() int }
	var validationErrs validator.ValidationErrors

	switch {
	case errors.As(err, &httpErr):
		code = httpErr.HTTPStatusCode()
		msg = err.Error()
	case errors.As(err, new(*json.UnmarshalTypeError)),
		errors.As(err, new(*json.SyntaxError)),
		errors.As(err, &validationErrs),
		errors.Is(err, io.EOF),
		errors.Is(err, io.ErrUnexpectedEOF),
		errors.Is(err, strconv.ErrSyntax),
		errors.Is(err, strconv.ErrRange):
		code = http.StatusBadRequest
		msg = err.Error()
	}

	errResp := HTTPError{
		Message:   msg,
		RequestID: w.Header().Get("X-Request-Id"),
	}

	resp, _ := json.Marshal(errResp)
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	w.Write(resp)
}
