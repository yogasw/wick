package client

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"github.com/yogasw/wick/internal/pkg/sanitizer"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/hashicorp/go-retryablehttp"
	"github.com/rs/zerolog/log"
)

var sanitizerInstance = sanitizer.New()

type Client struct {
	HTTPClient *http.Client
	DebugMode  bool
}

var (
	defaultDebugMode  = false
	defaultHTTPClient = newHTTPClient()
)

func newHTTPClient() *http.Client {
	retryClient := retryablehttp.NewClient()
	retryClient.RetryMax = 3
	retryClient.Logger = nil
	retryClient.HTTPClient.Timeout = 20 * time.Second
	retryClient.ErrorHandler = retryablehttp.PassthroughErrorHandler
	return retryClient.StandardClient()
}

func New() *Client {
	return &Client{
		HTTPClient: defaultHTTPClient,
		DebugMode:  defaultDebugMode,
	}
}

func (c *Client) Call(ctx context.Context, method, url string, body io.Reader, headers map[string]string, response any) error {
	req, err := http.NewRequestWithContext(ctx, strings.ToUpper(method), url, body)
	if err != nil {
		return &Error{
			Message:  fmt.Sprintf("unable to create new request: %s", err.Error()),
			RawError: err,
		}
	}

	var reqBody []byte
	if req.Body != nil {
		reqBody, _ = io.ReadAll(req.Body)
		req.Body = io.NopCloser(bytes.NewBuffer(reqBody))
	}

	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")

	for key, value := range headers {
		req.Header.Set(key, value)
	}

	start := time.Now()

	resp, err := c.HTTPClient.Do(req)
	if err != nil {
		return &Error{
			Message:  fmt.Sprintf("unable to sends an http request: %s", err.Error()),
			RawError: err,
		}
	}

	defer resp.Body.Close()
	latency := time.Since(start)

	responseBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return &Error{
			Message:    fmt.Sprintf("unable to read response body: %s", err.Error()),
			StatusCode: resp.StatusCode,
			RawError:   err,
		}
	}

	if c.DebugMode {
		log.Ctx(ctx).Info().
			Str("method", resp.Request.Method).
			Str("url", resp.Request.URL.String()).
			Str("body", sanitizerInstance.SanitizeJSON(reqBody)).
			Interface("headers", sanitizerInstance.SanitizeHeaders(resp.Request.Header)).
			Int("status_code", resp.StatusCode).
			Float64("latency", float64(latency.Nanoseconds()/1e4)/100.0).
			Msg("outbound request")
	}

	if resp.StatusCode >= 400 {
		rawErr := fmt.Errorf("%s %s returned error %d response: %s",
			resp.Request.Method,
			resp.Request.URL,
			resp.StatusCode,
			string(responseBody),
		)

		return &Error{
			Message:        "http client error",
			StatusCode:     resp.StatusCode,
			RawError:       rawErr,
			RawAPIResponse: responseBody,
		}
	}

	if response != nil {
		if err = json.Unmarshal(responseBody, response); err != nil {
			return &Error{
				Message:        fmt.Sprintf("unable to unmarshaling body response: %s", err.Error()),
				StatusCode:     resp.StatusCode,
				RawError:       err,
				RawAPIResponse: responseBody,
			}
		}
	}

	return nil
}
