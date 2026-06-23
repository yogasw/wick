package googleworkspace

// Slides input structs — one per operation.

// SlidesGetContentInput is the argument schema for slides_get_content.
type SlidesGetContentInput struct {
	FileID string `wick:"required;desc=Google Slides presentation file ID."`
}

// SlidesAddSlideInput is the argument schema for slides_add_slide.
type SlidesAddSlideInput struct {
	FileID        string `wick:"required;desc=Google Slides presentation file ID."`
	Title         string `wick:"desc=Title text for the new slide."`
	Body          string `wick:"textarea;desc=Body text for the new slide."`
	Layout        string `wick:"dropdown=TITLE_AND_BODY|BLANK|TITLE_ONLY;desc=Slide layout. Default: TITLE_AND_BODY"`
	InsertAtIndex int    `wick:"desc=0-based insertion index. Default 0 appends to end (pass -1 to append explicitly)."`
}

// SlidesDuplicateSlideInput is the argument schema for slides_duplicate_slide.
type SlidesDuplicateSlideInput struct {
	FileID     string `wick:"required;desc=Google Slides presentation file ID."`
	SlideIndex int    `wick:"required;desc=0-based index of the slide to duplicate."`
}
