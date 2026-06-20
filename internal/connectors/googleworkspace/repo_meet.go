// Package googleworkspace — repo_meet.go: Outbound HTTP calls to the Google Meet REST API v2.
//
// Purpose: Read-only network I/O for the Meet operations — space lookup, past
// conference records, and per-conference recordings/transcripts. Reuses the
// shared doWithRefresh lazy-token-refresh helper from repo_drive.go.
//
// Caller:   connector.go Meet handlers
// Dependencies: connector.Ctx, service.go types
// Side Effects: outbound HTTPS calls to meet.googleapis.com
package googleworkspace

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strings"

	"github.com/yogasw/wick/pkg/connector"
)

const meetBaseURL = "https://meet.googleapis.com/v2"

func meetGet(c *connector.Ctx, path string, params url.Values) ([]byte, error) {
	return doWithRefresh(c, func(token string) (*http.Request, error) {
		u := meetBaseURL + path
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

// getMeetSpace fetches a space by its name. The caller may pass a full resource
// name ("spaces/abc"), a meeting code, or a Meet URL — normalized here.
func getMeetSpace(c *connector.Ctx, space string) (MeetSpace, error) {
	name := normalizeSpaceName(space)
	body, err := meetGet(c, "/"+name, nil)
	if err != nil {
		return MeetSpace{}, err
	}
	var r struct {
		Name        string `json:"name"`
		MeetingURI  string `json:"meetingUri"`
		MeetingCode string `json:"meetingCode"`
		Config      struct {
			AccessType string `json:"accessType"`
		} `json:"config"`
		ActiveConference *struct {
			ConferenceRecord string `json:"conferenceRecord"`
		} `json:"activeConference"`
	}
	if err := json.Unmarshal(body, &r); err != nil {
		return MeetSpace{}, fmt.Errorf("parse space: %w", err)
	}
	out := MeetSpace{
		Name:        r.Name,
		MeetingURI:  r.MeetingURI,
		MeetingCode: r.MeetingCode,
		AccessType:  r.Config.AccessType,
	}
	if r.ActiveConference != nil {
		out.ActiveConference = r.ActiveConference.ConferenceRecord
	}
	return out, nil
}

// normalizeSpaceName turns a meeting code or Meet URL into a "spaces/{id}" name.
func normalizeSpaceName(s string) string {
	s = strings.TrimSpace(s)
	if strings.HasPrefix(s, "spaces/") {
		return s
	}
	// strip a Meet URL down to its code
	if i := strings.LastIndex(s, "/"); i >= 0 {
		s = s[i+1:]
	}
	return "spaces/" + s
}

// listConferenceRecords lists past meetings, optionally filtered (Meet filter syntax).
func listConferenceRecords(c *connector.Ctx, filter string, pageSize int) ([]MeetConferenceRecord, error) {
	params := url.Values{}
	if filter != "" {
		params.Set("filter", filter)
	}
	params.Set("pageSize", fmt.Sprintf("%d", pageSize))
	body, err := meetGet(c, "/conferenceRecords", params)
	if err != nil {
		return nil, err
	}
	var resp struct {
		ConferenceRecords []struct {
			Name      string `json:"name"`
			StartTime string `json:"startTime"`
			EndTime   string `json:"endTime"`
			Space     string `json:"space"`
		} `json:"conferenceRecords"`
	}
	if err := json.Unmarshal(body, &resp); err != nil {
		return nil, fmt.Errorf("parse conference records: %w", err)
	}
	out := make([]MeetConferenceRecord, len(resp.ConferenceRecords))
	for i, r := range resp.ConferenceRecords {
		out[i] = MeetConferenceRecord{Name: r.Name, StartTime: r.StartTime, EndTime: r.EndTime, Space: r.Space}
	}
	return out, nil
}

// conferenceChild builds the "{conferenceRecord}/recordings" or ".../transcripts" path.
// conferenceRecord may be passed bare ("abc") or fully-qualified ("conferenceRecords/abc").
func normalizeConferenceName(s string) string {
	s = strings.TrimSpace(s)
	if strings.HasPrefix(s, "conferenceRecords/") {
		return s
	}
	return "conferenceRecords/" + s
}

// listRecordings lists recordings for a conference record.
func listRecordings(c *connector.Ctx, conferenceRecord string) ([]MeetRecording, error) {
	name := normalizeConferenceName(conferenceRecord)
	body, err := meetGet(c, "/"+name+"/recordings", nil)
	if err != nil {
		return nil, err
	}
	var resp struct {
		Recordings []struct {
			Name      string `json:"name"`
			State     string `json:"state"`
			StartTime string `json:"startTime"`
			EndTime   string `json:"endTime"`
			DriveDestination *struct {
				File string `json:"file"`
			} `json:"driveDestination"`
		} `json:"recordings"`
	}
	if err := json.Unmarshal(body, &resp); err != nil {
		return nil, fmt.Errorf("parse recordings: %w", err)
	}
	out := make([]MeetRecording, len(resp.Recordings))
	for i, r := range resp.Recordings {
		rec := MeetRecording{Name: r.Name, State: r.State, StartTime: r.StartTime, EndTime: r.EndTime}
		if r.DriveDestination != nil {
			rec.DriveFile = r.DriveDestination.File
		}
		out[i] = rec
	}
	return out, nil
}

// listTranscripts lists transcripts for a conference record.
func listTranscripts(c *connector.Ctx, conferenceRecord string) ([]MeetTranscript, error) {
	name := normalizeConferenceName(conferenceRecord)
	body, err := meetGet(c, "/"+name+"/transcripts", nil)
	if err != nil {
		return nil, err
	}
	var resp struct {
		Transcripts []struct {
			Name      string `json:"name"`
			State     string `json:"state"`
			StartTime string `json:"startTime"`
			EndTime   string `json:"endTime"`
			DocsDestination *struct {
				Document string `json:"document"`
			} `json:"docsDestination"`
		} `json:"transcripts"`
	}
	if err := json.Unmarshal(body, &resp); err != nil {
		return nil, fmt.Errorf("parse transcripts: %w", err)
	}
	out := make([]MeetTranscript, len(resp.Transcripts))
	for i, t := range resp.Transcripts {
		tr := MeetTranscript{Name: t.Name, State: t.State, StartTime: t.StartTime, EndTime: t.EndTime}
		if t.DocsDestination != nil {
			tr.DocID = t.DocsDestination.Document
		}
		out[i] = tr
	}
	return out, nil
}
