package main

import (
 "encoding/json"
 "net/http"
 "github.com/yogasw/wick/pkg/entity"
 "github.com/yogasw/wick/pkg/service"
 "github.com/yogasw/wick/plugins/service/wick_mesh/meshstate"
)
func Module() service.Module {
 return service.Module{
  Meta:service.Meta{Key:"wick_mesh",Name:"Wick Mesh",Description:"Mesh policy foundation (experimental)"},
  Routes:[]service.Route{{Prefix:"/api",Auth:service.Token}},
  Configs:[]entity.Config{{Key:"profile",Value:"mobile",Description:"Mesh profile: mobile, full, worker, backup, custom"}},
  Register:func(mux *http.ServeMux,env *service.Env){
   registerPolicy(mux,func()string{return env.Cfg("profile")})
  },
 }
}

// registerPolicy runs behind the host's token-authenticated service route.
func registerPolicy(mux *http.ServeMux, profile func()string){
 mux.HandleFunc("GET /api/policy",func(w http.ResponseWriter,r *http.Request){
  p,e:=meshstate.Preset(meshstate.Profile(profile()))
  w.Header().Set("Cache-Control","no-store")
  if e!=nil{http.Error(w,"Invalid mesh profile",http.StatusBadRequest);return}
  w.Header().Set("Content-Type","application/json")
  _=json.NewEncoder(w).Encode(p)
 })
}
