// Package googleworkspace — repo_gmail.go: Outbound HTTP calls to the Gmail REST API v1.
//
// Purpose: All network I/O for the Gmail operations. Reuses the shared
// doWithRefresh lazy-token-refresh helper from repo_drive.go.
//
// Caller:   connector.go Gmail handlers
// Dependencies: connector.Ctx, service.go types
// Side Effects: outbound HTTPS calls to gmail.googleapis.com
package googleworkspace

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strings"

	"github.com/yogasw/wick/pkg/connector"
)

const gmailBaseURL = "https://gmail.googleapis.com/gmail/v1/users/me"

func gmailGet(c *connector.Ctx, path string, params url.Values) ([]byte, error) {
	return doWithRefresh(c, func(token string) (*http.Request, error) {
		u := gmailBaseURL + path
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

func gmailPost(c *connector.Ctx, path string, body any) ([]byte, error) {
	return doWithRefresh(c, func(token string) (*http.Request, error) {
		return buildJSONRequest(c, http.MethodPost, gmailBaseURL+path, token, body)
	})
}

// gmailRawMessage is the raw Gmail API message resource we care about.
type gmailRawMessage struct {
	ID           string   `json:"id"`
	ThreadID     string   `json:"threadId"`
	Snippet      string   `json:"snippet"`
	LabelIDs     []string `json:"labelIds"`
	InternalDate string   `json:"internalDate"`
	Payload      *gmailPayload `json:"payload"`
}

type gmailPayload struct {
	Headers []struct {
		Name  string `json:"name"`
		Value string `json:"value"`
	} `json:"headers"`
	MimeType string         `json:"mimeType"`
	Body     *gmailBodyPart `json:"body"`
	Parts    []gmailPayload `json:"parts"`
}

type gmailBodyPart struct {
	Data string `json:"data"`
	Size int    `json:"size"`
}

func (p *gmailPayload) header(name string) string {
	if p == nil {
		return ""
	}
	for _, h := range p.Headers {
		if strings.EqualFold(h.Name, name) {
			return h.Value
		}
	}
	return ""
}

// extractBody walks the MIME tree and returns the first text/plain body it finds,
// falling back to text/html stripped of nothing (raw) when no plain part exists.
func extractBody(p *gmailPayload) string {
	if p == nil {
		return ""
	}
	if strings.HasPrefix(p.MimeType, "text/plain") && p.Body != nil && p.Body.Data != "" {
		return decodeB64URL(p.Body.Data)
	}
	for i := range p.Parts {
		if b := extractBody(&p.Parts[i]); b != "" {
			return b
		}
	}
	// fall back to whatever single body exists (e.g. text/html only)
	if p.Body != nil && p.Body.Data != "" {
		return decodeB64URL(p.Body.Data)
	}
	return ""
}

func decodeB64URL(s string) string {
	b, err := base64.URLEncoding.DecodeString(s)
	if err != nil {
		// Gmail sometimes omits padding; retry with raw decoder.
		if b2, err2 := base64.RawURLEncoding.DecodeString(s); err2 == nil {
			return string(b2)
		}
		return ""
	}
	return string(b)
}

// listMessages searches the mailbox and resolves metadata for each hit.
func listMessages(c *connector.Ctx, query string, maxResults int) ([]MailMessageSummary, error) {
	params := url.Values{}
	if query != "" {
		params.Set("q", query)
	}
	params.Set("maxResults", fmt.Sprintf("%d", maxResults))
	body, err := gmailGet(c, "/messages", params)
	if err != nil {
		return nil, err
	}
	var list struct {
		Messages []struct {
			ID       string `json:"id"`
			ThreadID string `json:"threadId"`
		} `json:"messages"`
	}
	if err := json.Unmarshal(body, &list); err != nil {
		return nil, fmt.Errorf("parse message list: %w", err)
	}
	out := make([]MailMessageSummary, 0, len(list.Messages))
	for _, m := range list.Messages {
		// metadata format: headers only, no body — cheap.
		p := url.Values{}
		p.Set("format", "metadata")
		p.Add("metadataHeaders", "From")
		p.Add("metadataHeaders", "To")
		p.Add("metadataHeaders", "Subject")
		p.Add("metadataHeaders", "Date")
		mb, err := gmailGet(c, "/messages/"+m.ID, p)
		if err != nil {
			return nil, err
		}
		var raw gmailRawMessage
		if err := json.Unmarshal(mb, &raw); err != nil {
			return nil, fmt.Errorf("parse message %s: %w", m.ID, err)
		}
		out = append(out, shapeSummary(raw))
	}
	return out, nil
}

func shapeSummary(raw gmailRawMessage) MailMessageSummary {
	return MailMessageSummary{
		ID:       raw.ID,
		ThreadID: raw.ThreadID,
		From:     raw.Payload.header("From"),
		To:       raw.Payload.header("To"),
		Subject:  raw.Payload.header("Subject"),
		Date:     raw.Payload.header("Date"),
		Snippet:  raw.Snippet,
	}
}

// getMessage fetches one message in full and returns its body + headers.
func getMessage(c *connector.Ctx, id string) (MailMessage, error) {
	params := url.Values{}
	params.Set("format", "full")
	body, err := gmailGet(c, "/messages/"+id, params)
	if err != nil {
		return MailMessage{}, err
	}
	var raw gmailRawMessage
	if err := json.Unmarshal(body, &raw); err != nil {
		return MailMessage{}, fmt.Errorf("parse message: %w", err)
	}
	return MailMessage{
		MailMessageSummary: shapeSummary(raw),
		Cc:                 raw.Payload.header("Cc"),
		Labels:             raw.LabelIDs,
		Body:               extractBody(raw.Payload),
	}, nil
}

// buildRFC822 assembles a raw RFC 2822 message and base64url-encodes it for the
// Gmail send/draft endpoints. threadRefs (In-Reply-To / References) are added
// only when replying.
func buildRFC822(to, cc, subject, body, inReplyTo string) string {
	var b strings.Builder
	b.WriteString("To: " + to + "\r\n")
	if cc != "" {
		b.WriteString("Cc: " + cc + "\r\n")
	}
	b.WriteString("Subject: " + subject + "\r\n")
	if inReplyTo != "" {
		b.WriteString("In-Reply-To: " + inReplyTo + "\r\n")
		b.WriteString("References: " + inReplyTo + "\r\n")
	}
	b.WriteString("Content-Type: text/plain; charset=UTF-8\r\n")
	b.WriteString("\r\n")
	b.WriteString(body)
	return base64.URLEncoding.EncodeToString([]byte(b.String()))
}

// sendMessage sends a new email. threadID/inReplyTo are empty for a fresh send.
func sendMessage(c *connector.Ctx, raw, threadID string) (MailSendResult, error) {
	payload := map[string]any{"raw": raw}
	if threadID != "" {
		payload["threadId"] = threadID
	}
	body, err := gmailPost(c, "/messages/send", payload)
	if err != nil {
		return MailSendResult{}, err
	}
	var resp struct {
		ID       string `json:"id"`
		ThreadID string `json:"threadId"`
	}
	if err := json.Unmarshal(body, &resp); err != nil {
		return MailSendResult{}, fmt.Errorf("parse send response: %w", err)
	}
	return MailSendResult{ID: resp.ID, ThreadID: resp.ThreadID}, nil
}

// createDraft creates a draft message.
func createDraft(c *connector.Ctx, raw string) (MailDraftResult, error) {
	payload := map[string]any{"message": map[string]any{"raw": raw}}
	body, err := gmailPost(c, "/drafts", payload)
	if err != nil {
		return MailDraftResult{}, err
	}
	var resp struct {
		ID      string `json:"id"`
		Message struct {
			ID string `json:"id"`
		} `json:"message"`
	}
	if err := json.Unmarshal(body, &resp); err != nil {
		return MailDraftResult{}, fmt.Errorf("parse draft response: %w", err)
	}
	return MailDraftResult{DraftID: resp.ID, MessageID: resp.Message.ID}, nil
}

// replyMessage resolves a thread's last message headers, then sends a reply that
// stays in the same thread (Subject Re:, In-Reply-To, References set).
func replyMessage(c *connector.Ctx, messageID, body string) (MailSendResult, error) {
	src, err := getMessage(c, messageID)
	if err != nil {
		return MailSendResult{}, fmt.Errorf("fetch message to reply: %w", err)
	}
	subject := src.Subject
	if !strings.HasPrefix(strings.ToLower(subject), "re:") {
		subject = "Re: " + subject
	}
	// reply to the original sender. The Message-ID header threads the reply.
	msgID := messageHeaderID(c, messageID)
	raw := buildRFC822(src.From, "", subject, body, msgID)
	return sendMessage(c, raw, src.ThreadID)
}

// messageHeaderID fetches the RFC822 Message-ID header used for threading.
func messageHeaderID(c *connector.Ctx, id string) string {
	p := url.Values{}
	p.Set("format", "metadata")
	p.Add("metadataHeaders", "Message-ID")
	mb, err := gmailGet(c, "/messages/"+id, p)
	if err != nil {
		return ""
	}
	var raw gmailRawMessage
	if json.Unmarshal(mb, &raw) != nil {
		return ""
	}
	return raw.Payload.header("Message-ID")
}

// modifyLabels adds and/or removes label IDs on a message.
func modifyLabels(c *connector.Ctx, messageID string, add, remove []string) (MailLabelResult, error) {
	payload := map[string]any{}
	if len(add) > 0 {
		payload["addLabelIds"] = add
	}
	if len(remove) > 0 {
		payload["removeLabelIds"] = remove
	}
	body, err := gmailPost(c, "/messages/"+messageID+"/modify", payload)
	if err != nil {
		return MailLabelResult{}, err
	}
	var raw gmailRawMessage
	if err := json.Unmarshal(body, &raw); err != nil {
		return MailLabelResult{}, fmt.Errorf("parse modify response: %w", err)
	}
	return MailLabelResult{ID: raw.ID, Labels: raw.LabelIDs}, nil
}
