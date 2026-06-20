// Package googleworkspace — repo_calendar.go: Outbound HTTP calls to the Google Calendar REST API v3.
//
// Purpose: All network I/O for the Calendar operations. Reuses the shared
// doWithRefresh lazy-token-refresh helper from repo_drive.go. create_event can
// attach a Google Meet link via conferenceData (covers the "Meet via Calendar" path).
//
// Caller:   connector.go Calendar handlers
// Dependencies: connector.Ctx, service.go types
// Side Effects: outbound HTTPS calls to www.googleapis.com/calendar
package googleworkspace

import (
	"crypto/sha1"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"

	"github.com/yogasw/wick/pkg/connector"
)

// meetRequestID derives a stable conference requestId from the event payload.
// Google only requires it to be unique per create call; a hash of the start
// time + summary is unique enough and keeps retries idempotent.
func meetRequestID(ev map[string]any) string {
	b, _ := json.Marshal(ev)
	sum := sha1.Sum(b)
	return "wick-meet-" + hex.EncodeToString(sum[:8])
}

const calendarBaseURL = "https://www.googleapis.com/calendar/v3"

func calendarGet(c *connector.Ctx, path string, params url.Values) ([]byte, error) {
	return doWithRefresh(c, func(token string) (*http.Request, error) {
		u := calendarBaseURL + path
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

func calendarPost(c *connector.Ctx, path string, params url.Values, body any) ([]byte, error) {
	u := calendarBaseURL + path
	if len(params) > 0 {
		u += "?" + params.Encode()
	}
	return doWithRefresh(c, func(token string) (*http.Request, error) {
		return buildJSONRequest(c, http.MethodPost, u, token, body)
	})
}

func calendarPatch(c *connector.Ctx, path string, params url.Values, body any) ([]byte, error) {
	u := calendarBaseURL + path
	if len(params) > 0 {
		u += "?" + params.Encode()
	}
	return doWithRefresh(c, func(token string) (*http.Request, error) {
		return buildJSONRequest(c, http.MethodPatch, u, token, body)
	})
}

func calendarDelete(c *connector.Ctx, path string) ([]byte, error) {
	return doWithRefresh(c, func(token string) (*http.Request, error) {
		req, err := http.NewRequestWithContext(c.Context(), http.MethodDelete, calendarBaseURL+path, nil)
		if err != nil {
			return nil, err
		}
		req.Header.Set("Authorization", "Bearer "+token)
		return req, nil
	})
}

// rawEvent is the subset of the Calendar event resource we surface.
type rawEvent struct {
	ID          string `json:"id"`
	Status      string `json:"status"`
	Summary     string `json:"summary"`
	Description string `json:"description"`
	Location    string `json:"location"`
	HTMLLink    string `json:"htmlLink"`
	HangoutLink string `json:"hangoutLink"`
	Start       rawEventTime `json:"start"`
	End         rawEventTime `json:"end"`
	Attendees   []struct {
		Email          string `json:"email"`
		ResponseStatus string `json:"responseStatus"`
	} `json:"attendees"`
	ConferenceData *struct {
		EntryPoints []struct {
			EntryPointType string `json:"entryPointType"`
			URI            string `json:"uri"`
		} `json:"entryPoints"`
	} `json:"conferenceData"`
}

type rawEventTime struct {
	DateTime string `json:"dateTime"`
	Date     string `json:"date"`
}

func (t rawEventTime) value() string {
	if t.DateTime != "" {
		return t.DateTime
	}
	return t.Date
}

func shapeEvent(r rawEvent) CalendarEvent {
	ev := CalendarEvent{
		ID:          r.ID,
		Status:      r.Status,
		Summary:     r.Summary,
		Description: r.Description,
		Location:    r.Location,
		Start:       r.Start.value(),
		End:         r.End.value(),
		HTMLLink:    r.HTMLLink,
		MeetLink:    r.HangoutLink,
	}
	for _, a := range r.Attendees {
		ev.Attendees = append(ev.Attendees, EventAttendee{Email: a.Email, ResponseStatus: a.ResponseStatus})
	}
	if ev.MeetLink == "" && r.ConferenceData != nil {
		for _, ep := range r.ConferenceData.EntryPoints {
			if ep.EntryPointType == "video" {
				ev.MeetLink = ep.URI
				break
			}
		}
	}
	return ev
}

// listCalendars returns the user's calendar list.
func listCalendars(c *connector.Ctx) ([]CalendarSummary, error) {
	body, err := calendarGet(c, "/users/me/calendarList", nil)
	if err != nil {
		return nil, err
	}
	var resp struct {
		Items []struct {
			ID          string `json:"id"`
			Summary     string `json:"summary"`
			Description string `json:"description"`
			Primary     bool   `json:"primary"`
			AccessRole  string `json:"accessRole"`
		} `json:"items"`
	}
	if err := json.Unmarshal(body, &resp); err != nil {
		return nil, fmt.Errorf("parse calendar list: %w", err)
	}
	out := make([]CalendarSummary, len(resp.Items))
	for i, it := range resp.Items {
		out[i] = CalendarSummary{
			ID: it.ID, Summary: it.Summary, Description: it.Description,
			Primary: it.Primary, AccessRole: it.AccessRole,
		}
	}
	return out, nil
}

// listEvents lists events in a calendar within an optional time window.
func listEvents(c *connector.Ctx, calendarID, timeMin, timeMax, query string, maxResults int) ([]CalendarEvent, error) {
	params := url.Values{}
	params.Set("singleEvents", "true")
	params.Set("orderBy", "startTime")
	if timeMin != "" {
		params.Set("timeMin", timeMin)
	}
	if timeMax != "" {
		params.Set("timeMax", timeMax)
	}
	if query != "" {
		params.Set("q", query)
	}
	params.Set("maxResults", fmt.Sprintf("%d", maxResults))
	body, err := calendarGet(c, "/calendars/"+url.PathEscape(calendarID)+"/events", params)
	if err != nil {
		return nil, err
	}
	var resp struct {
		Items []rawEvent `json:"items"`
	}
	if err := json.Unmarshal(body, &resp); err != nil {
		return nil, fmt.Errorf("parse events: %w", err)
	}
	out := make([]CalendarEvent, len(resp.Items))
	for i, r := range resp.Items {
		out[i] = shapeEvent(r)
	}
	return out, nil
}

// getEvent fetches a single event.
func getEvent(c *connector.Ctx, calendarID, eventID string) (CalendarEvent, error) {
	body, err := calendarGet(c, "/calendars/"+url.PathEscape(calendarID)+"/events/"+url.PathEscape(eventID), nil)
	if err != nil {
		return CalendarEvent{}, err
	}
	var r rawEvent
	if err := json.Unmarshal(body, &r); err != nil {
		return CalendarEvent{}, fmt.Errorf("parse event: %w", err)
	}
	return shapeEvent(r), nil
}

// createEvent inserts an event. When addMeet is true, a Meet conference is
// requested via conferenceData and the resulting link is returned in MeetLink.
func createEvent(c *connector.Ctx, calendarID string, ev map[string]any, addMeet bool) (CalendarEvent, error) {
	params := url.Values{}
	params.Set("sendUpdates", "all")
	if addMeet {
		params.Set("conferenceDataVersion", "1")
		ev["conferenceData"] = map[string]any{
			"createRequest": map[string]any{
				"requestId":             meetRequestID(ev),
				"conferenceSolutionKey": map[string]any{"type": "hangoutsMeet"},
			},
		}
	}
	body, err := calendarPost(c, "/calendars/"+url.PathEscape(calendarID)+"/events", params, ev)
	if err != nil {
		return CalendarEvent{}, err
	}
	var r rawEvent
	if err := json.Unmarshal(body, &r); err != nil {
		return CalendarEvent{}, fmt.Errorf("parse created event: %w", err)
	}
	return shapeEvent(r), nil
}

// updateEvent patches an existing event with the supplied fields.
func updateEvent(c *connector.Ctx, calendarID, eventID string, patch map[string]any) (CalendarEvent, error) {
	params := url.Values{}
	params.Set("sendUpdates", "all")
	body, err := calendarPatch(c, "/calendars/"+url.PathEscape(calendarID)+"/events/"+url.PathEscape(eventID), params, patch)
	if err != nil {
		return CalendarEvent{}, err
	}
	var r rawEvent
	if err := json.Unmarshal(body, &r); err != nil {
		return CalendarEvent{}, fmt.Errorf("parse updated event: %w", err)
	}
	return shapeEvent(r), nil
}

// deleteEvent cancels/deletes an event. A 204 No Content body is empty.
func deleteEvent(c *connector.Ctx, calendarID, eventID string) (EventDeleteResult, error) {
	_, err := calendarDelete(c, "/calendars/"+url.PathEscape(calendarID)+"/events/"+url.PathEscape(eventID)+"?sendUpdates=all")
	if err != nil {
		return EventDeleteResult{}, err
	}
	return EventDeleteResult{EventID: eventID, Deleted: true}, nil
}

// respondEvent sets the current user's responseStatus on an event by patching
// the matching attendee entry.
func respondEvent(c *connector.Ctx, calendarID, eventID, response string) (EventRespondResult, error) {
	// fetch event to find the self attendee
	body, err := calendarGet(c, "/calendars/"+url.PathEscape(calendarID)+"/events/"+url.PathEscape(eventID), nil)
	if err != nil {
		return EventRespondResult{}, err
	}
	var r struct {
		Attendees []struct {
			Email string `json:"email"`
			Self  bool   `json:"self"`
		} `json:"attendees"`
	}
	if err := json.Unmarshal(body, &r); err != nil {
		return EventRespondResult{}, fmt.Errorf("parse event attendees: %w", err)
	}
	selfEmail := ""
	for _, a := range r.Attendees {
		if a.Self {
			selfEmail = a.Email
			break
		}
	}
	if selfEmail == "" {
		return EventRespondResult{}, fmt.Errorf("you are not an attendee on this event")
	}
	patch := map[string]any{
		"attendees": []map[string]any{
			{"email": selfEmail, "responseStatus": response},
		},
	}
	params := url.Values{}
	params.Set("sendUpdates", "all")
	if _, err := calendarPatch(c, "/calendars/"+url.PathEscape(calendarID)+"/events/"+url.PathEscape(eventID), params, patch); err != nil {
		return EventRespondResult{}, err
	}
	return EventRespondResult{EventID: eventID, ResponseStatus: response}, nil
}
