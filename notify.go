package onairlogsync

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/ashwanthkumar/slack-go-webhook"
)

func Notify(ctx context.Context, m PubSubMessage) error {
	var songs []Song
	webhookUrl := mustGetenv("SLACK_WEBHOOK_URL")
	app := NewApp(ctx)
	err := json.Unmarshal(m.Data, &songs)
	if err != nil {
		app.LogError(err)
		return err
	}

	for _, song := range songs {
		timeStr := (*song.Time).Format("2006/01/02 15:04")
		fallback := fmt.Sprintf("%s %s / %s", timeStr, song.Title, song.Artist)
		ts := (*song.Time).Unix()

		attachment1 := slack.Attachment{}
		attachment1.Fallback = &fallback
		attachment1.AuthorName = &song.Artist
		attachment1.Title = &song.Title
		attachment1.Timestamp = &ts
		payload := slack.Payload{
			Attachments: []slack.Attachment{attachment1},
		}
		errors := slack.Send(webhookUrl, "", payload)
		if len(errors) > 0 {
			for _, err := range errors {
				app.LogError(err)
			}
			return errors[0]
		}
	}

	return nil
}
