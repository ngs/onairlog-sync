package onairlogsync

import (
	"context"
	"fmt"
	"log"
)

func Sync(ctx context.Context, m PubSubMessage) error {
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
