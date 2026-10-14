package connector

// FileOutputKey is the reserved result key an op uses to hand wick a file
// instead of data for the caller to read (an export, a report). wick takes
// it out of the result after masking, writes it to artifacts/<name> under
// the calling agent session's working directory (creating artifacts/ when
// missing), and puts a FileRef in its place — so the content never reaches
// the agent or the run log, only the path does.
//
// The content is text (CSV, TSV, JSON, NDJSON, …) carried as a plain JSON
// string on purpose: the host's masking sweep covers JSON strings, so every
// value the op passed to c.Mask is masked inside the file too. Binary
// formats are not supported.
const FileOutputKey = "wick_file"

// FileRefKey is where the written file is described in the result wick
// returns.
const FileRefKey = "file"

// FileOutput is the file an op returns under FileOutputKey.
type FileOutput struct {
	// Name is the file name only (no directories); wick sanitizes it and
	// adds a numeric suffix rather than overwrite an existing file.
	Name string `json:"name"`
	// Format is the extension without the dot: csv, tsv, json, ndjson, txt.
	Format  string `json:"format"`
	Content string `json:"content"`
}

// FileRef is what replaces FileOutput in the result the caller sees.
type FileRef struct {
	// Path is relative to the session's working directory, e.g.
	// "artifacts/apps-20261014-1150.csv".
	Path   string `json:"path"`
	Format string `json:"format"`
	Bytes  int    `json:"bytes"`
}

// WithFile attaches f to result under FileOutputKey and returns result.
func WithFile(result map[string]any, f FileOutput) map[string]any {
	if result == nil {
		result = map[string]any{}
	}
	result[FileOutputKey] = f
	return result
}
