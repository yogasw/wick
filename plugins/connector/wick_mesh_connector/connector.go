package main

import (
 "github.com/yogasw/wick/pkg/connector"
 "github.com/yogasw/wick/pkg/entity"
 "github.com/yogasw/wick/pkg/wickdocs"
 "github.com/yogasw/wick/plugins/service/wick_mesh/meshclient"
)

type Config struct{
 BaseURL string `wick:"required;url;desc=HTTPS Mesh service URL ending in /x/wick_mesh"`
 Token string `wick:"required;secret;desc=Mesh service bearer token"`
}
type emptyInput struct{}
func Module() connector.Module {
 return connector.Module{
 Meta:connector.Meta{Key:"wick_mesh_connector",Name:"Wick Mesh Connector",Description:"Read Mesh policy from a configured service (experimental)",Icon:"🕸️"},
 Configs:entity.StructToConfigs(Config{}),
 Operations:[]connector.Category{
 connector.Cat("Mesh","Inspect the configured Mesh service",
 connector.Op("policy","Get effective preset","Read the Mesh policy preset. Does not claim node health or distributed readiness.",emptyInput{},getPolicy,wickdocs.Docs{})),
 },
 }
}
func getPolicy(c *connector.Ctx)(any,error){return meshclient.Policy(c.Context(),c.Cfg("base_url"),c.Cfg("token"))}
