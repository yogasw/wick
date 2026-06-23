package googleworkspace

// Calendar input structs — one per operation.

// CalendarListCalendarsInput is the argument schema for calendar_list_calendars.
type CalendarListCalendarsInput struct{}

// CalendarListEventsInput is the argument schema for calendar_list_events.
type CalendarListEventsInput struct {
	CalendarID string `wick:"desc=Calendar ID. Default: primary."`
	TimeMin    string `wick:"desc=Lower bound (RFC3339, inclusive). Example: 2026-06-20T00:00:00Z. Leave empty for no lower bound."`
	TimeMax    string `wick:"desc=Upper bound (RFC3339, exclusive). Leave empty for no upper bound."`
	Query      string `wick:"desc=Free-text search over event fields. Optional."`
	MaxResults int    `wick:"desc=Max events to return (1-250). Default: 50."`
}

// CalendarGetEventInput is the argument schema for calendar_get_event.
type CalendarGetEventInput struct {
	CalendarID string `wick:"desc=Calendar ID. Default: primary."`
	EventID    string `wick:"required;desc=Event ID (from calendar_list_events)."`
}

// CalendarCreateEventInput is the argument schema for calendar_create_event.
type CalendarCreateEventInput struct {
	CalendarID  string `wick:"desc=Calendar ID. Default: primary."`
	Summary     string `wick:"required;desc=Event title."`
	Description string `wick:"textarea;desc=Event description / agenda. Optional."`
	Location    string `wick:"desc=Physical location or address. Optional."`
	Start       string `wick:"required;desc=Start time (RFC3339). Example: 2026-06-21T09:00:00+07:00. For an all-day event pass a date: 2026-06-21."`
	End         string `wick:"required;desc=End time (RFC3339 or date for all-day)."`
	Attendees   string `wick:"desc=Attendee email addresses, comma-separated. Optional."`
	AddMeet     bool   `wick:"desc=Attach a Google Meet video link to the event. Default: false."`
}

// CalendarUpdateEventInput is the argument schema for calendar_update_event.
type CalendarUpdateEventInput struct {
	CalendarID  string `wick:"desc=Calendar ID. Default: primary."`
	EventID     string `wick:"required;desc=Event ID to update."`
	Summary     string `wick:"desc=New title. Leave empty to keep current."`
	Description string `wick:"textarea;desc=New description. Leave empty to keep current."`
	Location    string `wick:"desc=New location. Leave empty to keep current."`
	Start       string `wick:"desc=New start time (RFC3339). Leave empty to keep current."`
	End         string `wick:"desc=New end time (RFC3339). Leave empty to keep current."`
	Attendees   string `wick:"desc=Replacement attendee list, comma-separated. Leave empty to keep current attendees."`
}

// CalendarDeleteEventInput is the argument schema for calendar_delete_event.
type CalendarDeleteEventInput struct {
	CalendarID string `wick:"desc=Calendar ID. Default: primary."`
	EventID    string `wick:"required;desc=Event ID to cancel/delete. Attendees are notified."`
}

// CalendarRespondEventInput is the argument schema for calendar_respond_event.
type CalendarRespondEventInput struct {
	CalendarID string `wick:"desc=Calendar ID. Default: primary."`
	EventID    string `wick:"required;desc=Event ID to respond to."`
	Response   string `wick:"required;dropdown=accepted|declined|tentative;desc=Your RSVP response."`
}
