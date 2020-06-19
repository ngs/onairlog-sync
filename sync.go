package onairlogsync

import (
	"context"
	"fmt"
	"log"
)

func Sync(ctx context.Context, m PubSubMessage) error {
	app := NewApp(ctx)
	defer app.DB().Close()
	lastTime := app.LastSong().Time
	if lastTime == nil {
		log.Fatal("Failed to fetch last song")
		return nil
	}
	fmt.Println(lastTime)
	app.Visit(*lastTime)
	return nil
}
