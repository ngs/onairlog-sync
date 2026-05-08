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

// jstLocation returns Asia/Tokyo, falling back to UTC if the zone
// database is unavailable (e.g. in a stripped-down container).
func jstLocation() *time.Location {
	loc, err := time.LoadLocation("Asia/Tokyo")
	if err != nil {
		return time.UTC
	}
	return loc
}

// BuildSlackAttachment renders a Slack attachment for a single
// PublishedPlay. Pure function — no side effects, suitable for tests.
//
// Title / artist prefer the canonical (enriched) values, falling back
// to the raw display fields, then to the play's raw title/artist.
// The footer always shows the JST-formatted airplay time so Slack's
// own ts collapse cannot hide the time of day.
func BuildSlackAttachment(item PublishedPlay, loc *time.Location) (slack.Attachment, bool) {
	if item.Play.Time == nil {
		return slack.Attachment{}, false
	}
	if loc == nil {
		loc = time.UTC
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

	t := item.Play.Time.In(loc)
	timeStr := t.Format("2006/01/02 15:04")
	fallback := fmt.Sprintf("%s %s / %s", timeStr, title, artist)
	ts := item.Play.Time.Unix()

	a := slack.Attachment{}
	a.Fallback = &fallback
	a.AuthorName = &artist
	a.Title = &title
	a.Footer = &timeStr
	a.Timestamp = &ts
	if song.ITunesURL != "" {
		link := song.ITunesURL
		a.TitleLink = &link
	}
	if song.ArtworkURL != "" {
		art := song.ArtworkURL
		a.ImageUrl = &art
	}
	return a, true
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

	jst := jstLocation()
	for _, item := range plays {
		attachment, ok := BuildSlackAttachment(item, jst)
		if !ok {
			continue
		}
		payload := slack.Payload{
			Attachments: []slack.Attachment{attachment},
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
