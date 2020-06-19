package onairlogsync

import (
	"context"
	"encoding/json"
	"log"
)

func Notify(ctx context.Context, m PubSubMessage) error {
	var songs []Song
	app := NewApp(ctx)
	err := json.Unmarshal(m.Data, &songs)
	if err != nil {
		app.LogError(err)
		return err
	}
	log.Println(songs)
	return nil
}
