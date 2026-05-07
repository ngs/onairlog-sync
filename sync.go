package onairlogsync

import (
	"context"
	"fmt"
	"log"

	"github.com/GoogleCloudPlatform/functions-framework-go/functions"
	"github.com/cloudevents/sdk-go/v2/event"
)

func init() {
	functions.CloudEvent("Sync", Sync)
}

func Sync(ctx context.Context, e event.Event) error {
	app := NewApp(ctx)
	defer app.Close()
	last := app.LastSong()
	if last == nil || last.Time == nil {
		log.Println("No previous song found in Firestore; skipping sync")
		return nil
	}
	fmt.Println(*last.Time)
	app.Visit(*last.Time)
	return nil
}
