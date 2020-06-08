package upgraderesponder

import (
	"github.com/gorilla/mux"
)

func NewRouter(s *Server) *mux.Router {
	r := mux.NewRouter().StrictSlash(true)

	r.Methods("POST").Path("/v1/checkupgrade").HandlerFunc(s.CheckUpgrade)
	r.Methods("GET").Path("/v1/healthcheck").HandlerFunc(s.HealthCheck)

	return r
}
