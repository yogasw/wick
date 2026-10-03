package main

import (
	"encoding/json"
	"net/http"

	"github.com/yogasw/wick/pkg/entity"
	"github.com/yogasw/wick/pkg/service"
)

// Module describes the service. Paths are relative to /x/{Key}.
func Module() service.Module {
	return service.Module{
		Meta: service.Meta{Key: "my_service", Name: "My Service", Description: "What this service does.", Icon: "🛰️"},
		Routes: []service.Route{
			{Prefix: "/hook", Auth: service.Public},
			{Prefix: "/api", Auth: service.Token},
			{Prefix: "/", Auth: service.Session},
		},
		Configs: []entity.Config{{Key: "greeting", Value: "hello", Description: "Text the API answers with"}},
		Register: func(mux *http.ServeMux, env *service.Env) {
			mux.HandleFunc("POST /hook", func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusNoContent) })
			mux.HandleFunc("GET /api/hello", func(w http.ResponseWriter, _ *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				_ = json.NewEncoder(w).Encode(map[string]string{"message": env.Cfg("greeting")})
			})
		},
	}
}
