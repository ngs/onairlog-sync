package onairlogsync

import (
	"context"
	"crypto/sha1"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"time"

	"cloud.google.com/go/errorreporting"
	"cloud.google.com/go/firestore"
	"cloud.google.com/go/pubsub"
	"github.com/gocolly/colly"
	"google.golang.org/api/iterator"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

const (
	songsCollection = "songs"
	playsCollection = "plays"
)

func mustGetenv(k string) string {
	v := os.Getenv(k)
	if v == "" {
		log.Panicf("%s environment variable not set.", k)
	}
	return v
}

// App .
type App struct {
	Context         context.Context
	pubsubClient    *pubsub.Client
	errorClient     *errorreporting.Client
	firestoreClient *firestore.Client
}

// NewApp .
func NewApp(ctx context.Context) *App {
	return &App{Context: ctx}
}

// ProjectID .
func (app *App) ProjectID() string {
	return mustGetenv("PROJECT_ID")
}

// Firestore returns a lazily-initialized Firestore client.
func (app *App) Firestore() *firestore.Client {
	if app.firestoreClient != nil {
		return app.firestoreClient
	}
	db := os.Getenv("FIRESTORE_DATABASE")
	if db == "" {
		db = firestore.DefaultDatabaseID
	}
	client, err := firestore.NewClientWithDatabase(app.Context, app.ProjectID(), db)
	if err != nil {
		app.LogError(err)
		log.Fatal(err)
	}
	app.firestoreClient = client
	return client
}

// Close releases held clients.
func (app *App) Close() {
	if app.firestoreClient != nil {
		_ = app.firestoreClient.Close()
	}
}

func (app *App) ParseTime(str string) *time.Time {
	jst, err := time.LoadLocation("Asia/Tokyo")
	if err != nil {
		app.LogError(err)
		return nil
	}
	t, err := time.ParseInLocation("2006-01-02 15:04:05", str, jst)
	if err != nil {
		app.LogError(err)
		return nil
	}
	return &t
}

// LastPlay returns the most recently aired Play, or nil if none exists.
func (app *App) LastPlay() *Play {
	iter := app.Firestore().Collection(playsCollection).
		OrderBy("time", firestore.Desc).
		Limit(1).
		Documents(app.Context)
	defer iter.Stop()
	doc, err := iter.Next()
	if err == iterator.Done {
		return nil
	}
	if err != nil {
		app.LogError(err)
		return nil
	}
	var play Play
	if err := doc.DataTo(&play); err != nil {
		app.LogError(err)
		return nil
	}
	return &play
}

// PlayDocID returns the deterministic Firestore ID for a Play given its
// air time and the canonical SongID it belongs to.
func PlayDocID(airTime time.Time, songID string) string {
	h := sha1.New()
	fmt.Fprintf(h, "%d\x00%s", airTime.Unix(), songID)
	return hex.EncodeToString(h.Sum(nil))
}

// InsertPlay creates a Play and upserts the canonical Song it points to.
// Returns (play, song, error). When the play already exists (same airtime
// and song), play is nil to indicate "nothing new".
func (app *App) InsertPlay(airTime *time.Time, rawTitle, rawArtist string) (*Play, *Song, error) {
	if airTime == nil {
		return nil, nil, nil
	}

	songID := SongID(rawTitle, rawArtist)
	playID := PlayDocID(*airTime, songID)

	playRef := app.Firestore().Collection(playsCollection).Doc(playID)
	songRef := app.Firestore().Collection(songsCollection).Doc(songID)

	play := Play{
		SongID:    songID,
		Time:      airTime,
		RawTitle:  rawTitle,
		RawArtist: rawArtist,
	}

	if _, err := playRef.Create(app.Context, play); err != nil {
		if status.Code(err) == codes.AlreadyExists {
			return nil, nil, nil
		}
		return nil, nil, err
	}

	var resultSong Song
	err := app.Firestore().RunTransaction(app.Context, func(ctx context.Context, tx *firestore.Transaction) error {
		snap, err := tx.Get(songRef)
		if status.Code(err) == codes.NotFound {
			resultSong = Song{
				Title:         DisplayClean(rawTitle),
				Artist:        DisplayClean(rawArtist),
				NormalizedKey: NormalizedKey(rawTitle, rawArtist),
				FirstAired:    airTime,
				LastAired:     airTime,
				PlayCount:     1,
			}
			return tx.Create(songRef, resultSong)
		}
		if err != nil {
			return err
		}
		if err := snap.DataTo(&resultSong); err != nil {
			return err
		}
		if resultSong.FirstAired == nil || airTime.Before(*resultSong.FirstAired) {
			resultSong.FirstAired = airTime
		}
		if resultSong.LastAired == nil || airTime.After(*resultSong.LastAired) {
			resultSong.LastAired = airTime
		}
		resultSong.PlayCount++
		return tx.Set(songRef, resultSong)
	})
	if err != nil {
		return nil, nil, err
	}
	return &play, &resultSong, nil
}

func (app *App) Visit(date time.Time) bool {
	if date.After(time.Now()) {
		return true
	}
	// J-WAVE's URL expects JST date/time fields. time.Time loaded from
	// Firestore carries UTC location, so format in JST explicitly.
	jst, err := time.LoadLocation("Asia/Tokyo")
	if err != nil {
		app.LogError(err)
		return true
	}
	date = date.In(jst)
	c := colly.NewCollector()
	var rows []PublishedPlay
	c.OnHTML(".list_songs", func(e *colly.HTMLElement) {
		e.ForEach(".song", func(index int, e *colly.HTMLElement) {
			airTime := app.ParseTime(e.ChildText(".song_info .time span"))
			if airTime == nil || !date.Before(*airTime) {
				return
			}
			title := e.ChildText("h4")
			artist := e.ChildText(".txt_artist span")
			play, song, err := app.InsertPlay(airTime, title, artist)
			if err != nil {
				app.LogError(err)
				return
			}
			if play != nil && song != nil {
				rows = append(rows, PublishedPlay{Play: *play, Song: *song})
			}
		})
	})
	c.OnRequest(func(r *colly.Request) {
		fmt.Println("Visiting", r.URL)
	})
	c.OnError(func(r *colly.Response, err error) {
		fmt.Println("Request URL:", r.Request.URL, "failed with response:", r, "\nError:", err)
	})

	c.Visit(date.Format("https://www.j-wave.co.jp/cgi-bin/soundsearch_result.cgi?year=2006&month=01&day=02&hour=15&minute=04"))
	app.PublishNewPlays(rows)
	return app.Visit(date.Add(2 * time.Hour))
}

// PublishedPlay is the message body delivered to the notify topic.
type PublishedPlay struct {
	Play Play `json:"play"`
	Song Song `json:"song"`
}

func (app *App) PublishNewPlays(rows []PublishedPlay) {
	if len(rows) == 0 {
		return
	}
	data, err := json.Marshal(rows)
	if err != nil {
		app.LogError(err)
		return
	}
	log.Println(string(data))
	msg := &pubsub.Message{Data: data}
	topic := app.PubSubClient().Topic("notify")
	res, err := topic.Publish(app.Context, msg).Get(app.Context)
	if err != nil {
		app.LogError(err)
		return
	}
	log.Printf("Topic published: %v\n", res)
}

// LogError .
func (app *App) LogError(err error) {
	log.Println(err)
	if app.errorClient == nil {
		ctx := context.Background()
		errorClient, err := errorreporting.NewClient(ctx, app.ProjectID(), errorreporting.Config{
			ServiceName: "onairlog-sync",
			OnError: func(err error) {
				log.Printf("Could not log error: %v", err)
			},
		})
		if err != nil {
			log.Fatal(err)
		}
		app.errorClient = errorClient
	}
	app.errorClient.Report(errorreporting.Entry{
		Error: err,
	})
}

// PubSubClient .
func (app *App) PubSubClient() *pubsub.Client {
	if app.pubsubClient != nil {
		return app.pubsubClient
	}
	client, err := pubsub.NewClient(app.Context, app.ProjectID())
	if err != nil {
		app.LogError(err)
		log.Fatal(err)
	}
	app.pubsubClient = client
	return client
}
