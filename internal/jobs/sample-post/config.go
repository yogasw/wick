package samplepost

// Config is the runtime-editable config for a sample-post job
// instance. Each exported field with a `wick:"..."` tag becomes one
// row in the `configs` table scoped to this instance's Meta.Key — the
// admin UI picks an input widget from the Go type plus tag flags.
//
// Run() reads live values via job.FromContext(ctx).Cfg(...), so the
// admin can change them without a restart. Static metadata (Key, Name,
// Icon, DefaultCron) is NOT on this struct — it belongs on job.Meta
// declared by the caller of app.RegisterJob.
type Config struct {
	// BaseURL is the root of the JSONPlaceholder-compatible API. The
	// job fetches "{BaseURL}/posts/1" on every run. Required, no
	// default — admins must fill it from the manager UI before the job
	// can run.
	BaseURL string `wick:"url;required;desc=Root URL of the JSONPlaceholder-compatible API. Example: https://jsonplaceholder.typicode.com"`
}
