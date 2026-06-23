package googleworkspace

// Meet input structs — one per operation.

// MeetCreateSpaceInput is the argument schema for meet_create_space.
type MeetCreateSpaceInput struct {
	AccessType string `wick:"dropdown=TRUSTED|OPEN|RESTRICTED;desc=Who can join. TRUSTED: org + invited (default). OPEN: anyone with the link. RESTRICTED: invited only."`
}

// MeetGetSpaceInput is the argument schema for meet_get_space.
type MeetGetSpaceInput struct {
	Space string `wick:"required;desc=Meet space resource name (spaces/abc), meeting code, or full Meet URL."`
}

// MeetListConferenceRecordsInput is the argument schema for meet_list_conference_records.
type MeetListConferenceRecordsInput struct {
	Filter   string `wick:"desc=Meet filter expression. Example: space.meeting_code=\"abc-defg-hij\" or start_time>=\"2026-06-01T00:00:00Z\". Optional."`
	PageSize int    `wick:"desc=Max records to return (1-100). Default: 25."`
}

// MeetListRecordingsInput is the argument schema for meet_list_recordings.
type MeetListRecordingsInput struct {
	ConferenceRecord string `wick:"required;desc=Conference record name (conferenceRecords/abc) from meet_list_conference_records."`
}

// MeetListTranscriptsInput is the argument schema for meet_list_transcripts.
type MeetListTranscriptsInput struct {
	ConferenceRecord string `wick:"required;desc=Conference record name (conferenceRecords/abc) from meet_list_conference_records."`
}
