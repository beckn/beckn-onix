package routes

import (
	handlers "beckn-onix/cmd/clientSideReciever/src/handler"
	"net/http"
)


func InitializeRoutes() *http.ServeMux {
	mux := http.NewServeMux()


	mux.HandleFunc("/", handlers.HomeHandler)

	return mux
}
