package main

import (
	"log"
	"os"

	"github.com/GoogleCloudPlatform/functions-framework-go/funcframework"
	app "github.com/ngs/onairlog-sync"
)

func main() {
	funcframework.RegisterEventFunction("/sync", app.Sync)
	funcframework.RegisterEventFunction("/notify", app.Notify)
	port := "8080"
	if envPort := os.Getenv("PORT"); envPort != "" {
		port = envPort
	}

	if err := funcframework.Start(port); err != nil {
		log.Fatalf("funcframework.Start: %v\n", err)
	}
}
