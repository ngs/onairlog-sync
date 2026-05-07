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

const songsCollection = "songs"

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

func (app *App) LastSong() *Song {
	iter := app.Firestore().Collection(songsCollection).
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
	var song Song
	if err := doc.DataTo(&song); err != nil {
		app.LogError(err)
		return nil
	}
	return &song
}

// SongDocID returns a deterministic document ID for a (time, title, artist) tuple.
func SongDocID(songTime time.Time, title, artist string) string {
	h := sha1.New()
	fmt.Fprintf(h, "%d\x00%s\x00%s", songTime.Unix(), title, artist)
	return hex.EncodeToString(h.Sum(nil))
}

func (app *App) InsertSong(songTime *time.Time, title, artist string) (*Song, error) {
	if songTime == nil {
		return nil, nil
	}
	song := Song{Time: songTime, Artist: artist, Title: title}
	id := SongDocID(*songTime, title, artist)
	_, err := app.Firestore().Collection(songsCollection).Doc(id).Create(app.Context, song)
	if err != nil {
		if status.Code(err) == codes.AlreadyExists {
			return nil, nil
		}
		return nil, err
	}
	return &song, nil
}

func (app *App) Visit(date time.Time) bool {
	if date.After(time.Now()) {
		return true
	}
	c := colly.NewCollector()
	rows := []Song{}
	c.OnHTML(".list_songs", func(e *colly.HTMLElement) {
		e.ForEach(".song", func(index int, e *colly.HTMLElement) {
			songTime := app.ParseTime(e.ChildText(".song_info .time span"))
			if songTime == nil || !date.Before(*songTime) {
				return
			}
			title := e.ChildText("h4")
			artist := e.ChildText(".txt_artist span")
			song, err := app.InsertSong(songTime, title, artist)
			if err != nil {
				app.LogError(err)
			} else if song != nil {
				rows = append(rows, *song)
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
	app.PublishNewSongs(rows)
	return app.Visit(date.Add(2 * time.Hour))
}

func (app *App) PublishNewSongs(songs []Song) {
	if len(songs) == 0 {
		return
	}
	data, err := json.Marshal(songs)
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
