package main

import (
	"github.com/yogasw/wick/pkg/connector"
	"github.com/yogasw/wick/pkg/wickdocs"
)

const Key = "jenkins"

type Configs struct {
	BaseURL      string `wick:"url;required;desc=Jenkins base URL. Example: https://jenkins.example.com"`
	Username     string `wick:"secret;required;desc=Jenkins username for Basic Auth."`
	Password     string `wick:"secret;required;desc=Jenkins password for Basic Auth."`
	DefaultDepth int    `wick:"default=3;desc=Default nested folder depth for list_jobs."`
	MaxDepth     int    `wick:"default=6;desc=Maximum nested folder depth allowed for list_jobs."`
}

type ListJobsInput struct {
	Search string `wick:"desc=Optional case-insensitive contains filter against job name. Empty returns all jobs."`
	Depth  int    `wick:"desc=Nested folder depth. Defaults to connector default_depth."`
	Limit  int    `wick:"desc=Optional max jobs returned. 0 = no limit."`
}

type BuildJobInput struct {
	Name       string `wick:"required;desc=Job name from list_jobs. For nested jobs use slash-separated path. Example: platform/payment-service"`
	Parameters string `wick:"textarea;desc=Optional build parameters. A JSON object ({\"BRANCH\":\"main\"}) or one KEY=VALUE per line. Empty triggers a build without parameters."`
}

type GetBuildInput struct {
	Name   string `wick:"required;desc=Job name from list_jobs. For nested jobs use slash-separated path."`
	Number string `wick:"desc=Build number, or lastBuild / lastSuccessfulBuild / lastFailedBuild. Default lastBuild."`
}

type GetConfigInput struct {
	Name string `wick:"required;desc=Job name from list_jobs. For nested jobs use slash-separated path. Example: platform/payment-service"`
}

func Meta() connector.Meta {
	return connector.Meta{
		Key:         Key,
		Name:        "Jenkins",
		Description: "List Jenkins jobs and read job config.xml to discover SCM repository URLs and inline Groovy pipeline scripts.",
		Icon:        "J",
	}
}

func Operations() []connector.Category {
	return []connector.Category{connector.Cat(
		"Jobs",
		"List Jenkins jobs and fetch their config.",
		connector.Op(
			"list_jobs",
			"List Jobs",
			"List Jenkins job names visible to the configured account. Returns names only (slash-separated path for nested jobs). Use get_config with the returned name to fetch pipeline detail.",
			ListJobsInput{},
			listJobs,
			wickdocs.Docs{},
		),
		connector.Op(
			"get_config",
			"Get Job Config",
			"Fetch a Jenkins job's config.xml by name. URL is built from the configured base_url and the job name path, so host mismatches between Jenkins's internal URL and the access URL are ignored.",
			GetConfigInput{},
			getConfig,
			wickdocs.Docs{},
		),
		connector.OpDestructive(
			"build_job",
			"Trigger Build",
			"Trigger a build of a Jenkins job, with optional parameters. Returns the queue item URL; the build number appears in get_build once the build leaves the queue. Starts a real build.",
			BuildJobInput{},
			buildJob,
			wickdocs.Docs{},
		),
		connector.Op(
			"get_build",
			"Get Build",
			"Fetch a build's status (building, result, duration, timestamp, url). Number defaults to lastBuild.",
			GetBuildInput{},
			getBuild,
			wickdocs.Docs{},
		),
	)}
}

func listJobs(c *connector.Ctx) (any, error) {
	p, err := validateListJobs(c)
	if err != nil {
		return nil, err
	}
	return fetchJobs(c, p)
}

func getConfig(c *connector.Ctx) (any, error) {
	p, err := validateGetConfig(c)
	if err != nil {
		return nil, err
	}
	return fetchConfig(c, p)
}

func buildJob(c *connector.Ctx) (any, error) {
	p, err := validateBuildJob(c)
	if err != nil {
		return nil, err
	}
	return triggerBuild(c, p)
}

func getBuild(c *connector.Ctx) (any, error) {
	p, err := validateGetBuild(c)
	if err != nil {
		return nil, err
	}
	return fetchBuild(c, p)
}
