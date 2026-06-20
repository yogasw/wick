package loki

import (
	"github.com/yogasw/wick/pkg/connector"
	"github.com/yogasw/wick/pkg/wickdocs"
)

const Key = "loki"

type Configs struct {
	BaseURL       string `wick:"url;required;desc=Grafana base URL. Example: https://loki.domain.com"`
	DatasourceUID string `wick:"required;default=43cBBeg4k;desc=Loki datasource UID in Grafana. Found in the datasource proxy URL segment after /uid/."`
	AuthMode      string `wick:"dropdown=basic|token;required;default=basic;desc=basic = Grafana username + password, token = Bearer API key (Service Account)."`
	Token         string `wick:"secret;desc=Grafana Service Account token. Used when auth_mode = token."`
	Username      string `wick:"required;desc=Grafana username. Used when auth_mode = basic."`
	Password      string `wick:"secret;required;desc=Grafana password. Used when auth_mode = basic."`
	OrgID         string `wick:"required;default=1;desc=Grafana org ID sent as X-Grafana-Org-Id header. Example: 1."`
}

type QueryInput struct {
	Query     string `wick:"required;desc=LogQL query. Example: {app=\"api\"} |= \"error\""`
	Start     string `wick:"desc=Start time. RFC3339 or Unix nanoseconds. Default: 1 hour ago."`
	End       string `wick:"desc=End time. RFC3339 or Unix nanoseconds. Default: now."`
	Limit     int    `wick:"desc=Max log lines to return. Default 100, max 5000."`
	Direction string `wick:"dropdown=backward|forward;desc=backward = newest first (default). forward = oldest first."`
}

type LabelValuesInput struct {
	Label string `wick:"required;desc=Label name to look up. Example: app"`
}

func Meta() connector.Meta {
	return connector.Meta{
		Key:         Key,
		Name:        "Loki",
		Description: "Query logs and discover labels in a Grafana Loki instance via LogQL.",
		Icon:        "🪵",
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
				struct{}{},
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
	}
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
	return fetchStringList(c, u)
}

func labelValues(c *connector.Ctx) (any, error) {
	u, err := validateLabelValues(c)
	if err != nil {
		return nil, err
	}
	return fetchStringList(c, u)
}
