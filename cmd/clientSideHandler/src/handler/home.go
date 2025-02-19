package handlers

import (
	"fmt"
	"net/http"
)

// HomeHandler handles the home route
func HomeHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method == "GET" {
		fmt.Fprintf(w, "This is a GET request")
	} else if r.Method == "POST" {
		fmt.Fprintf(w, "This is a POST request")
	} else {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
	}
}
