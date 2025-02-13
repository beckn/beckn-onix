package handlers

import (
	handlers "beckn-onix/cmd/clientSideReciever/src/shared"
	"encoding/json"
	"fmt"
	"net/http"
)

type PublishRequest struct {
	Message string `json:"message"`
}

// HomeHandler handles the home route
func HomeHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method == "GET" {
		fmt.Fprintf(w, "This is a GET request")
	} else if r.Method == "POST" {
		var req PublishRequest

		// Parse JSON body
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, "Invalid JSON", http.StatusBadRequest)
			return
		}

		// Publish message
		fmt.Println("req.Message", req.Message)
		err := handlers.PublishMessage(req.Message)
		if err != nil {
			http.Error(w, "Failed to publish message here", http.StatusInternalServerError)
			return
		}

		w.WriteHeader(http.StatusOK)
		fmt.Fprintf(w, "Message published successfully")
	} else {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
	}
}
