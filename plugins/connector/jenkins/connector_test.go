package main

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/yogasw/wick/pkg/conntest"
)

func TestListJobsReturnsNestedNames(t *testing.T) {
	srv := conntest.Server(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/json" {
			t.Fatalf("path = %s, want /api/json", r.URL.Path)
		}
		user, pass, ok := r.BasicAuth()
		if !ok || user != "jenkins-user" || pass != "jenkins-pass" {
			t.Fatalf("basic auth = %q/%q/%v", user, pass, ok)
		}
		if r.URL.Query().Get("tree") == "" {
			t.Fatal("tree query is required")
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"jobs": []map[string]any{
				{
					"name": "platform",
					"url":  "http://jenkins.example/job/platform/",
					"jobs": []map[string]any{
						{"name": "payment-service", "url": "http://jenkins.example/job/platform/job/payment-service/"},
					},
				},
				{"name": "IT-es-teler-77", "url": "http://jenkins.example/job/IT-es-teler-77/"},
			},
		})
	})
	c := conntest.Ctx(t, map[string]string{
		"base_url":      srv.URL,
		"username":      "jenkins-user",
		"password":      "jenkins-pass",
		"default_depth": "3",
		"max_depth":     "6",
	}, map[string]string{})

	got, err := listJobs(c)
	if err != nil {
		t.Fatalf("listJobs error: %v", err)
	}
	result := got.(*ListJobsResult)
	if result.Count != 3 {
		t.Fatalf("count = %d, want 3", result.Count)
	}
	want := []string{"platform", "platform/payment-service", "IT-es-teler-77"}
	for i, n := range want {
		if result.Jobs[i] != n {
			t.Fatalf("jobs[%d] = %q, want %q", i, result.Jobs[i], n)
		}
	}
}

func TestGetConfigBuildsURLFromName(t *testing.T) {
	const configXML = `<flow-definition>
  <definition class="org.jenkinsci.plugins.workflow.cps.CpsScmFlowDefinition">
    <scm>
      <userRemoteConfigs>
        <hudson.plugins.git.UserRemoteConfig>
          <url>git@bitbucket.org:abc/payment-service.git</url>
        </hudson.plugins.git.UserRemoteConfig>
      </userRemoteConfigs>
    </scm>
    <scriptPath>deploy/Jenkinsfile</scriptPath>
  </definition>
  <script>pipeline { agent any }</script>
</flow-definition>`

	srv := conntest.Server(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/job/platform/job/payment-service/config.xml" {
			t.Fatalf("path = %s, want /job/platform/job/payment-service/config.xml", r.URL.Path)
		}
		user, pass, ok := r.BasicAuth()
		if !ok || user != "jenkins-user" || pass != "jenkins-pass" {
			t.Fatalf("basic auth = %q/%q/%v", user, pass, ok)
		}
		w.Header().Set("Content-Type", "application/xml")
		_, _ = w.Write([]byte(configXML))
	})
	c := conntest.Ctx(t, map[string]string{
		"base_url": srv.URL,
		"username": "jenkins-user",
		"password": "jenkins-pass",
	}, map[string]string{
		"name": "platform/payment-service",
	})

	got, err := getConfig(c)
	if err != nil {
		t.Fatalf("getConfig error: %v", err)
	}
	result := got.(*ConfigResult)
	if result.PipelineType != "scm" {
		t.Fatalf("pipeline_type = %q", result.PipelineType)
	}
	if len(result.RepositoryURLs) != 1 || result.RepositoryURLs[0] != "git@bitbucket.org:abc/payment-service.git" {
		t.Fatalf("repository_urls = %#v", result.RepositoryURLs)
	}
	if result.ScriptPath != "deploy/Jenkinsfile" {
		t.Fatalf("script_path = %q", result.ScriptPath)
	}
	if !strings.Contains(result.GroovyScript, "pipeline") {
		t.Fatalf("groovy_script = %q", result.GroovyScript)
	}
}

func TestGetConfigRejectsEmptyName(t *testing.T) {
	c := conntest.Ctx(t, map[string]string{
		"base_url": "https://jenkins.example.com",
		"username": "u",
		"password": "p",
	}, map[string]string{
		"name": "  ",
	})
	if _, err := getConfig(c); err == nil {
		t.Fatal("expected error")
	}
}

func TestBuildJobParams(t *testing.T) {
	c := conntest.Ctx(t, map[string]string{"base_url": "http://j.example"}, map[string]string{
		"name": "platform/pay", "parameters": `{"BRANCH":"main"}`,
	})
	p, err := validateBuildJob(c)
	if err != nil {
		t.Fatal(err)
	}
	if p.URL != "http://j.example/job/platform/job/pay/buildWithParameters" || p.Params.Get("BRANCH") != "main" {
		t.Fatalf("got %+v", p)
	}
	c = conntest.Ctx(t, map[string]string{"base_url": "http://j.example"}, map[string]string{"name": "x"})
	p, _ = validateBuildJob(c)
	if !strings.HasSuffix(p.URL, "/job/x/build") {
		t.Fatalf("url = %s", p.URL)
	}
}
