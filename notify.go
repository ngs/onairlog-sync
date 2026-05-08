package onairlogsync

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/GoogleCloudPlatform/functions-framework-go/functions"
	"github.com/ashwanthkumar/slack-go-webhook"
	"github.com/cloudevents/sdk-go/v2/event"
)

func init() {
	functions.CloudEvent("Notify", Notify)
}

func Notify(ctx context.Context, e event.Event) error {
	webhookUrl := mustGetenv("SLACK_WEBHOOK_URL")
	app := NewApp(ctx)

	var msg PubSubMessage
	if err := e.DataAs(&msg); err != nil {
		app.LogError(err)
		return err
	}
	var plays []PublishedPlay
	if err := json.Unmarshal(msg.Message.Data, &plays); err != nil {
		app.LogError(err)
		return err
	}

	for _, item := range plays {
		t := item.Play.Time
		if t == nil {
			continue
		}
		song := item.Song
		title := song.DisplayTitle()
		artist := song.DisplayArtist()
		if title == "" {
			title = item.Play.RawTitle
		}
		if artist == "" {
			artist = item.Play.RawArtist
		}
		jst, err := time.LoadLocation("Asia/Tokyo")
		if err != nil {
			app.LogError(err)
			jst = time.UTC
		}
		timeStr := t.In(jst).Format("2006/01/02 15:04")
		fallback := fmt.Sprintf("%s %s / %s", timeStr, title, artist)
		ts := t.Unix()

		attachment1 := slack.Attachment{}
		attachment1.Fallback = &fallback
		attachment1.AuthorName = &artist
		attachment1.Title = &title
		attachment1.Footer = &timeStr
		attachment1.Timestamp = &ts
		if link := song.ITunesURL(); link != "" {
			attachment1.TitleLink = &link
		}
		if art := song.ArtworkURL(); art != "" {
			attachment1.ImageUrl = &art
		}
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
