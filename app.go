package onairlogsync

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"time"

	"cloud.google.com/go/errorreporting"
	"cloud.google.com/go/pubsub"
	"github.com/gocolly/colly"
	"github.com/jinzhu/gorm"

	_ "github.com/jinzhu/gorm/dialects/mysql"
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
	Context      context.Context
	pubsubClient *pubsub.Client
	errorClient  *errorreporting.Client
	db           *gorm.DB
}

// NewApp .
func NewApp(context context.Context) *App {
	return &App{Context: context}
}

// ProjectID .
func (app *App) ProjectID() string {
	return mustGetenv("PROJECT_ID")
}

func (app *App) DB() *gorm.DB {
	if app.db != nil {
		return app.db
	}
	db, err := gorm.Open("mysql", mustGetenv("DATABASE_URI"))
	if err != nil {
		app.LogError(err)
		return nil
	}
	db.LogMode(true)
	if os.Getenv("MIGRATE") == "1" {
		db.AutoMigrate(&Song{})
	}
	app.db = db
	return db
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

func (app *App) LastSong() Song {
	var song Song
	app.DB().Order("time desc").Last(&song)
	return song
}

func (app *App) InsertSong(songTime *time.Time, title, artist string) (*Song, error) {
	db := app.DB()
	var count int
	db.Model(&Song{}).Where(Song{
		Time:   songTime,
		Title:  title,
		Artist: artist,
	}).Limit(1).Count(&count)
	if count > 0 {
		return nil, nil
	}
	song := Song{
		Time:   songTime,
		Title:  title,
		Artist: artist,
	}
	res := db.Create(&song)
	if err := res.Error; err != nil {
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
			if !date.Before(*songTime) {
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
			ServiceName: "alsee-service-log-sync",
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
