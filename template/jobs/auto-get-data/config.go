package autogetdata

// Config is the runtime-editable config for this job. Each exported
// field with a `wick:"..."` tag becomes one row in the `configs` table
// scoped to this instance's Meta.Key — admins edit them from
// /manager/jobs/{key} without a redeploy. Run() reads live values via
// job.FromContext(ctx).Cfg(...).
//
// Static metadata (Key, Name, Icon, DefaultCron) is NOT here — it
// belongs on job.Meta at the app.RegisterJob call site.
type Config struct {
	// URL is the endpoint the job hits every tick. Required — the job
	// errors out until an admin fills it in from /manager/jobs.
	URL string `wick:"url;required;desc=Endpoint fetched on every tick. Example: https://jsonplaceholder.typicode.com/posts/1"`
}
