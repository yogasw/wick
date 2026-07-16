package main

import (
	"github.com/yogasw/wick/pkg/connector"
	"github.com/yogasw/wick/pkg/entity"
	"github.com/yogasw/wick/pkg/wickdocs"
	"github.com/yogasw/wick/plugins/tags"
)

const Key = "loki"

type Configs struct {
	BaseURL string `wick:"url;required;group=Connection;desc=Grafana base URL. Example: https://loki.domain.com"`
	// Status is a read-only widget: it shows the Grafana version + reachability
	// probed live from /api/health. Not a stored value — the html op renders it.
	Status string `wick:"html=connection_status;group=Connection;desc=Live connection status and Grafana version. Fill the base URL first."`

	AuthMode string `wick:"dropdown=basic|token;required;default=basic;group=Authentication;desc=basic = Grafana username + password, token = Bearer API key (Service Account)."`
	Username string `wick:"required;visible_when=auth_mode:basic;group=Authentication;desc=Grafana username. Used when auth_mode = basic."`
	Password string `wick:"secret;required;visible_when=auth_mode:basic;group=Authentication;desc=Grafana password. Used when auth_mode = basic."`
	Token    string `wick:"secret;required;visible_when=auth_mode:token;group=Authentication;desc=Grafana Service Account token. Used when auth_mode = token."`

	OrgID         string `wick:"html=list_orgs;required;group=Datasource;desc=Pick the Grafana org. Sent as the X-Grafana-Org-Id header. Fill Connection + Authentication first."`
	DatasourceUID string `wick:"html=list_datasources;required;group=Datasource;desc=Pick a Loki datasource (scoped to the org above). Pick the org first, then reopen this list."`
}

// pickerInput drives the html picker ops (list_orgs, list_datasources). The
// manager's html widget always calls the backing op with the currently-selected
// value in an arg named "browser" (a fixed convention in HtmlField.svelte), so
// the op can highlight the chosen row. No other input is needed.
type pickerInput struct {
	Browser string `wick:"desc=Currently selected value, used only to highlight it in the list."`
}

type QueryInput struct {
	Query     string `wick:"required;desc=LogQL query. Example: {app=\"api\"} |= \"error\""`
	Start     string `wick:"desc=Start time. RFC3339 or Unix nanoseconds. Default: 1 hour ago."`
	End       string `wick:"desc=End time. RFC3339 or Unix nanoseconds. Default: now."`
	Limit     int    `wick:"desc=Max log lines to return. Default 100, max 5000."`
	Direction string `wick:"dropdown=backward|forward;desc=backward = newest first (default). forward = oldest first."`
}

// LabelsInput drives the labels op. Start/End are optional — a lookback
// window for label discovery. Grafana proxies label queries to Loki, and some
// Loki versions reject a range-less query (500 downstreamError); newer ones
// default to a window. Leave both blank on a recent Grafana; set them to widen
// the window or to inspect a past period. Default: last 6h → now.
type LabelsInput struct {
	Start string `wick:"desc=Start time for label discovery. RFC3339 or Unix nanoseconds. Optional. Default: 6 hours ago."`
	End   string `wick:"desc=End time. RFC3339 or Unix nanoseconds. Optional. Default: now."`
}

type LabelValuesInput struct {
	Label string `wick:"required;desc=Label name to look up. Example: app"`
	Start string `wick:"desc=Start time for value discovery. RFC3339 or Unix nanoseconds. Optional. Default: 6 hours ago."`
	End   string `wick:"desc=End time. RFC3339 or Unix nanoseconds. Optional. Default: now."`
}

// Module is the connector definition. DefaultTags match the built-in it
// replaced (Connector + Observability) so it lands in the same connector-list
// section; the app reads these from the manifest and categorizes the plugin
// identically to a built-in.
func Module() connector.Module {
	return connector.Module{
		Meta: connector.Meta{
			Key:         Key,
			Name:        "Loki",
			Description: "Query logs and discover labels in a Grafana Loki instance via LogQL.",
			Icon:        "🪵",
			DefaultTags: []entity.DefaultTag{tags.Connector, tags.Observability},
		},
		Configs:    entity.StructToConfigs(Configs{}),
		Operations: Operations(),
	}
}

func Operations() []connector.Category {
	return []connector.Category{
		connector.Cat(
			"Loki",
			"Query logs and discover labels in Grafana Loki using LogQL.",
			connector.Op(
				"query",
				"Query Logs",
				"Search logs using LogQL over a time range. Returns a count and flat list of log entries, each with an RFC3339 timestamp, stream labels map, and line text. Empty entries array means no matches found.",
				QueryInput{},
				query,
				wickdocs.Docs{},
			),
			connector.Op(
				"labels",
				"List Labels",
				"List all label names currently indexed by Loki. Use this to discover available labels before constructing a LogQL stream selector.",
				LabelsInput{},
				labels,
				wickdocs.Docs{},
			),
			connector.Op(
				"label_values",
				"List Label Values",
				"List all values for a given label name. Combine with the labels operation to build precise LogQL stream selectors like {app=\"api\", env=\"prod\"}.",
				LabelValuesInput{},
				labelValues,
				wickdocs.Docs{},
			),
		),
		connector.Cat(
			"Maintenance",
			"Backs the manager's config widgets (status + org + datasource pickers); not meant for agent use.",
			connector.OpConfigOnly(
				"connection_status",
				"Connection Status",
				"Probe GET /api/health and report the Grafana version + reachability. Read-only; used by the manager UI's connection-status widget.",
				pickerInput{},
				connectionStatus,
				wickdocs.Docs{},
			),
			connector.OpConfigOnly(
				"list_orgs",
				"List Orgs",
				"List the Grafana orgs the configured auth can access. Read-only; used by the manager UI's org picker to fill org_id.",
				pickerInput{},
				listOrgs,
				wickdocs.Docs{},
			),
			connector.OpConfigOnly(
				"list_datasources",
				"List Datasources",
				"List Loki datasources in Grafana. Read-only; used by the manager UI's datasource picker to fill datasource_uid.",
				pickerInput{},
				listDatasources,
				wickdocs.Docs{},
			),
		),
	}
}

func connectionStatus(c *connector.Ctx) (any, error) {
	return fetchStatusHTML(c)
}

func listOrgs(c *connector.Ctx) (any, error) {
	return fetchOrgsHTML(c)
}

func listDatasources(c *connector.Ctx) (any, error) {
	return fetchDatasourcesHTML(c)
}

func query(c *connector.Ctx) (any, error) {
	p, err := validateQuery(c)
	if err != nil {
		return nil, err
	}
	return fetchQueryRange(c, p)
}

func labels(c *connector.Ctx) (any, error) {
	u, err := resourceURL(c, "labels")
	if err != nil {
		return nil, err
	}
	return fetchStringList(c, withLabelWindow(c, u))
}

func labelValues(c *connector.Ctx) (any, error) {
	u, err := validateLabelValues(c)
	if err != nil {
		return nil, err
	}
	return fetchStringList(c, u)
}
