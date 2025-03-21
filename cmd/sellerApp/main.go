package main

import (
	"flag"
	"log"
	"net/http"
)

// var products []protocol.Provider

func loadProducts(filename string) {
	// data, err := os.ReadFile(filename)
	// if err != nil {
	// 	log.Fatalf("Failed to read YAML file: %v", err)
	// }
	// if err := yaml.Unmarshal(data, &products); err != nil {
	// 	log.Fatalf("Failed to parse YAML data: %v", err)
	// }
}

func searchHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Invalid request method", http.StatusMethodNotAllowed)
		return
	}

	// var req model.BPPSearchRequest
	// if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
	// 	http.Error(w, "Bad request", http.StatusBadRequest)
	// 	return
	// }

	// w.Header().Set("Content-Type", "application/json")
	// json.NewEncoder(w).Encode(model.BPPSearchResponse{Products: products})
}

var dataPath string

func main() {
	flag.StringVar(&dataPath, "config", "../../config/sellerData.yaml", "../../config/sellerData.yaml")
	flag.Parse()
	loadProducts(dataPath)
	http.HandleFunc("/search", searchHandler)
	log.Println("Seller app running on port 8080...")
	log.Fatal(http.ListenAndServe(":8080", nil))
}
