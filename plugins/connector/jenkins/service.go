package main

import (
	"encoding/json"
	"encoding/xml"
	"errors"
	"fmt"
	"net/url"
	"strings"

	"github.com/yogasw/wick/pkg/connector"
)

type listJobsParams struct {
	URL    string
	Search string
	Limit  int
}

type getConfigParams struct {
	URL  string
	Name string
}

type ListJobsResult struct {
	Count int      `json:"count"`
	Jobs  []string `json:"jobs"`
}

type ConfigResult struct {
	Name           string   `json:"name"`
	PipelineType   string   `json:"pipeline_type,omitempty"`
	RepositoryURLs []string `json:"repository_urls"`
	ScriptPath     string   `json:"script_path,omitempty"`
	GroovyScript   string   `json:"groovy_script,omitempty"`
	ConfigXML      string   `json:"config_xml"`
}

func validateListJobs(c *connector.Ctx) (listJobsParams, error) {
	base, err := baseURL(c)
	if err != nil {
		return listJobsParams{}, err
	}
	depth := c.InputInt("depth")
	if depth <= 0 {
		depth = c.CfgInt("default_depth")
	}
	if depth <= 0 {
		depth = 3
	}
	maxDepth := c.CfgInt("max_depth")
	if maxDepth <= 0 {
		maxDepth = 6
	}
	if depth > maxDepth {
		depth = maxDepth
	}
	u, err := url.Parse(base + "/api/json")
	if err != nil {
		return listJobsParams{}, fmt.Errorf("build api url: %w", err)
	}
	q := u.Query()
	q.Set("tree", jobTree(depth))
	u.RawQuery = q.Encode()
	return listJobsParams{
		URL:    u.String(),
		Search: strings.ToLower(strings.TrimSpace(c.Input("search"))),
		Limit:  c.InputInt("limit"),
	}, nil
}

func validateGetConfig(c *connector.Ctx) (getConfigParams, error) {
	base, err := baseURL(c)
	if err != nil {
		return getConfigParams{}, err
	}
	name := strings.Trim(strings.TrimSpace(c.Input("name")), "/")
	if name == "" {
		return getConfigParams{}, errors.New("name is required")
	}
	segments := strings.Split(name, "/")
	var pathParts []string
	for _, seg := range segments {
		seg = strings.TrimSpace(seg)
		if seg == "" {
			return getConfigParams{}, errors.New("name must not contain empty segments")
		}
		pathParts = append(pathParts, "job", url.PathEscape(seg))
	}
	cfgURL := strings.TrimRight(base, "/") + "/" + strings.Join(pathParts, "/") + "/config.xml"
	return getConfigParams{URL: cfgURL, Name: name}, nil
}

type buildJobParams struct {
	URL    string
	Name   string
	Params url.Values
}

type getBuildParams struct {
	URL  string
	Name string
}

type BuildResult struct {
	Name      string `json:"name"`
	Triggered bool   `json:"triggered"`
	QueueURL  string `json:"queue_url,omitempty"`
}

type BuildStatus struct {
	Name      string `json:"name"`
	Number    int    `json:"number"`
	Building  bool   `json:"building"`
	Result    string `json:"result,omitempty"`
	Duration  int64  `json:"duration_ms"`
	Timestamp int64  `json:"timestamp_ms"`
	URL       string `json:"url"`
}

// jobPath turns "folder/job" into "/job/folder/job/job".
func jobPath(name string) (string, error) {
	name = strings.Trim(strings.TrimSpace(name), "/")
	if name == "" {
		return "", errors.New("name is required")
	}
	var parts []string
	for _, seg := range strings.Split(name, "/") {
		seg = strings.TrimSpace(seg)
		if seg == "" {
			return "", errors.New("name must not contain empty segments")
		}
		parts = append(parts, "job", url.PathEscape(seg))
	}
	return "/" + strings.Join(parts, "/"), nil
}

// parseBuildParams accepts a JSON object or KEY=VALUE lines.
func parseBuildParams(raw string) (url.Values, error) {
	raw = strings.TrimSpace(raw)
	out := url.Values{}
	if raw == "" {
		return out, nil
	}
	if strings.HasPrefix(raw, "{") {
		var m map[string]any
		if err := json.Unmarshal([]byte(raw), &m); err != nil {
			return nil, fmt.Errorf("parameters is not valid JSON: %w", err)
		}
		for k, v := range m {
			if s, ok := v.(string); ok {
				out.Set(k, s)
			} else {
				out.Set(k, fmt.Sprint(v))
			}
		}
		return out, nil
	}
	for _, line := range strings.Split(raw, "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		k, v, ok := strings.Cut(line, "=")
		if !ok || strings.TrimSpace(k) == "" {
			return nil, fmt.Errorf("parameters line %q must be KEY=VALUE", line)
		}
		out.Set(strings.TrimSpace(k), v)
	}
	return out, nil
}

func validateBuildJob(c *connector.Ctx) (buildJobParams, error) {
	base, err := baseURL(c)
	if err != nil {
		return buildJobParams{}, err
	}
	name := strings.Trim(strings.TrimSpace(c.Input("name")), "/")
	path, err := jobPath(name)
	if err != nil {
		return buildJobParams{}, err
	}
	params, err := parseBuildParams(c.Input("parameters"))
	if err != nil {
		return buildJobParams{}, err
	}
	endpoint := "/build"
	if len(params) > 0 {
		endpoint = "/buildWithParameters"
	}
	return buildJobParams{URL: base + path + endpoint, Name: name, Params: params}, nil
}

func validateGetBuild(c *connector.Ctx) (getBuildParams, error) {
	base, err := baseURL(c)
	if err != nil {
		return getBuildParams{}, err
	}
	name := strings.Trim(strings.TrimSpace(c.Input("name")), "/")
	path, err := jobPath(name)
	if err != nil {
		return getBuildParams{}, err
	}
	num := strings.TrimSpace(c.Input("number"))
	if num == "" {
		num = "lastBuild"
	}
	return getBuildParams{URL: base + path + "/" + url.PathEscape(num) + "/api/json", Name: name}, nil
}

func baseURL(c *connector.Ctx) (string, error) {
	base := strings.TrimRight(strings.TrimSpace(c.Cfg("base_url")), "/")
	if base == "" {
		return "", errors.New("base_url is not configured")
	}
	return base, nil
}

func jobTree(depth int) string {
	tree := "jobs[name,url"
	for i := 1; i < depth; i++ {
		tree += ",jobs[name,url"
	}
	tree += strings.Repeat("]", depth)
	return tree
}

func flattenJobs(src []jenkinsJob, search string, limit int) []string {
	out := make([]string, 0)
	var walk func(prefix string, jobs []jenkinsJob)
	walk = func(prefix string, jobs []jenkinsJob) {
		for _, job := range jobs {
			if limit > 0 && len(out) >= limit {
				return
			}
			name := job.Name
			if prefix != "" {
				name = prefix + "/" + job.Name
			}
			if matchesName(name, search) {
				out = append(out, name)
			}
			if len(job.Jobs) > 0 {
				walk(name, job.Jobs)
			}
		}
	}
	walk("", src)
	return out
}

func matchesName(name, search string) bool {
	if search == "" {
		return true
	}
	return strings.Contains(strings.ToLower(name), search)
}

func parseConfig(name string, raw []byte) ConfigResult {
	xmlText := string(raw)
	result := ConfigResult{
		Name:           name,
		PipelineType:   pipelineType(xmlText),
		RepositoryURLs: []string{},
		ConfigXML:      xmlText,
	}

	decoder := xml.NewDecoder(strings.NewReader(xmlText))
	seenRepos := map[string]struct{}{}
	for {
		tok, err := decoder.Token()
		if err != nil {
			break
		}
		start, ok := tok.(xml.StartElement)
		if !ok {
			continue
		}
		switch start.Name.Local {
		case "url":
			value := strings.TrimSpace(readElementText(decoder, start.Name))
			if looksLikeRepoURL(value) {
				if _, exists := seenRepos[value]; !exists {
					seenRepos[value] = struct{}{}
					result.RepositoryURLs = append(result.RepositoryURLs, value)
				}
			}
		case "scriptPath":
			if result.ScriptPath == "" {
				result.ScriptPath = strings.TrimSpace(readElementText(decoder, start.Name))
			}
		case "script":
			if result.GroovyScript == "" {
				result.GroovyScript = strings.TrimSpace(readElementText(decoder, start.Name))
			}
		}
	}
	return result
}

func readElementText(decoder *xml.Decoder, name xml.Name) string {
	var b strings.Builder
	depth := 1
	for depth > 0 {
		tok, err := decoder.Token()
		if err != nil {
			break
		}
		switch t := tok.(type) {
		case xml.CharData:
			if depth == 1 {
				b.Write([]byte(t))
			}
		case xml.StartElement:
			depth++
		case xml.EndElement:
			if t.Name.Local == name.Local {
				depth--
			}
		}
	}
	return b.String()
}

func pipelineType(xmlText string) string {
	switch {
	case strings.Contains(xmlText, "org.jenkinsci.plugins.workflow.cps.CpsScmFlowDefinition"):
		return "scm"
	case strings.Contains(xmlText, "org.jenkinsci.plugins.workflow.cps.CpsFlowDefinition"):
		return "inline"
	default:
		return ""
	}
}

func looksLikeRepoURL(value string) bool {
	value = strings.TrimSpace(value)
	if value == "" {
		return false
	}
	lower := strings.ToLower(value)
	return strings.Contains(lower, ".git") ||
		strings.HasPrefix(lower, "git@") ||
		strings.Contains(lower, "bitbucket.org/") ||
		strings.Contains(lower, "github.com/") ||
		strings.Contains(lower, "gitlab")
}
