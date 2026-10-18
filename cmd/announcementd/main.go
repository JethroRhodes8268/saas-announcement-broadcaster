package main

import (
	"log"
	"net/http"
	"os"
	"time"

	"example.com/saas-announcement-broadcaster/announcement"
)

func main() {
	apiKey := os.Getenv("INFRAI_API_KEY")
	if apiKey == "" {
		log.Fatal("INFRAI_API_KEY is required")
	}
	addr := os.Getenv("LISTEN_ADDR")
	if addr == "" {
		addr = ":8080"
	}

	client := announcement.NewInfraiClient(apiKey)
	handler := announcement.NewHTTPHandler(announcement.NewService(client))
	server := &http.Server{Addr: addr, Handler: handler, ReadHeaderTimeout: 5 * time.Second}
	log.Printf("announcement service listening on %s", addr)
	log.Fatal(server.ListenAndServe())
}
