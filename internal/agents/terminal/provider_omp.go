package terminal

import provider "github.com/yogasw/wick/internal/agents/provider"

func init() {
	Register(provider.TypeOMP, Provider{Commands: func(ins provider.Instance) []Command {
		p := provider.OMPProfile(ins)
		return []Command{
			{Key: "tui", Label: "omp --profile " + p, Args: []string{"--profile", p}},
			{Key: "login", Label: "omp login", Args: []string{"login"}},
			{Key: "usage", Label: "omp usage", Args: []string{"usage"}},
		}
	}})
}
