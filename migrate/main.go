package main

import (
	"context"
	"crypto/sha1"
	"database/sql"
	"encoding/hex"
	"flag"
	"fmt"
	"log"
	"os"
	"regexp"
	"strings"
	"time"

	"cloud.google.com/go/firestore"
	_ "github.com/go-sql-driver/mysql"
	"google.golang.org/api/iterator"
)

const (
	songsCollection = "songs"
	playsCollection = "plays"
)

func mustGetenv(k string) string {
	v := os.Getenv(k)
	if v == "" {
		log.Fatalf("%s environment variable not set.", k)
	}
	return v
}

// Mirror of onairlogsync.Normalize / DisplayClean. Kept inlined so the
// migrate module does not have to depend on the runtime package.
var (
	parenRE = regexp.MustCompile(`[\(（\[［][^\)）\]］]*[\)）\]］]`)
	featRE  = regexp.MustCompile(`(?i)\b(?:featuring|feat|ft)(?:\.|\b)`)
	wsRE    = regexp.MustCompile(`\s+`)
)

func displayClean(s string) string {
	s = parenRE.ReplaceAllString(s, " ")
	s = wsRE.ReplaceAllString(s, " ")
	return strings.TrimSpace(s)
}

func normalize(s string) string {
	s = parenRE.ReplaceAllString(s, " ")
	s = strings.ToLower(s)
	s = featRE.ReplaceAllString(s, "feat.")
	s = wsRE.ReplaceAllString(s, " ")
	return strings.TrimSpace(s)
}

func hashKey(s string) string {
	h := sha1.New()
	fmt.Fprint(h, s)
	return hex.EncodeToString(h.Sum(nil))
}

func songID(title, artist string) string {
	return hashKey(normalize(title) + "\x00" + normalize(artist))
}

func playID(airTime time.Time, sid string) string {
	return hashKey(fmt.Sprintf("%d\x00%s", airTime.Unix(), sid))
}

type songDoc struct {
	Title         string     `firestore:"title"`
	Artist        string     `firestore:"artist"`
	NormalizedKey string     `firestore:"normalizedKey"`
	FirstAired    *time.Time `firestore:"firstAired"`
	LastAired     *time.Time `firestore:"lastAired"`
	PlayCount     int        `firestore:"playCount"`
}

type playDoc struct {
	SongID    string     `firestore:"songId"`
	Time      *time.Time `firestore:"time"`
	RawTitle  string     `firestore:"rawTitle"`
	RawArtist string     `firestore:"rawArtist"`
}

func main() {
	startID := flag.Int64("start-id", 0, "resume from this MySQL id (exclusive)")
	limit := flag.Int64("limit", 0, "max rows to migrate (0 = unlimited)")
	chunkSize := flag.Int64("chunk", 1000, "rows per chunk")
	dryRun := flag.Bool("dry-run", false, "do not write to Firestore")
	reset := flag.Bool("reset", false, "delete all docs in songs and plays, then exit")
	flag.Parse()

	if *reset {
		runReset()
		return
	}

	dsn := mustGetenv("DATABASE_URI")
	if !strings.Contains(dsn, "parseTime=") {
		if strings.Contains(dsn, "?") {
			dsn += "&parseTime=true"
		} else {
			dsn += "?parseTime=true"
		}
	}
	if !strings.Contains(dsn, "loc=") {
		dsn += "&loc=Asia%2FTokyo"
	}
	projectID := mustGetenv("PROJECT_ID")

	db, err := sql.Open("mysql", dsn)
	if err != nil {
		log.Fatalf("mysql open: %v", err)
	}
	defer db.Close()
	if err := db.Ping(); err != nil {
		log.Fatalf("mysql ping: %v", err)
	}

	ctx := context.Background()
	dbName := os.Getenv("FIRESTORE_DATABASE")
	if dbName == "" {
		dbName = firestore.DefaultDatabaseID
	}
	fs, err := firestore.NewClientWithDatabase(ctx, projectID, dbName)
	if err != nil {
		log.Fatalf("firestore client: %v", err)
	}
	defer fs.Close()

	bw := fs.BulkWriter(ctx)

	songMeta := make(map[string]songDoc)
	seenPlay := make(map[string]struct{})

	var (
		lastID    = *startID
		processed int64
		queued    int64
		duplicate int64
	)

	start := time.Now()
	for {
		batch := *chunkSize
		if *limit > 0 {
			remaining := *limit - processed
			if remaining <= 0 {
				break
			}
			if remaining < batch {
				batch = remaining
			}
		}

		rows, err := db.QueryContext(ctx,
			`SELECT id, time, title, artist FROM songs
			 WHERE id > ? AND deleted_at IS NULL AND time IS NOT NULL
			 ORDER BY id ASC
			 LIMIT ?`, lastID, batch)
		if err != nil {
			log.Fatalf("query: %v", err)
		}

		var got int64
		for rows.Next() {
			var (
				id     int64
				t      time.Time
				title  sql.NullString
				artist sql.NullString
			)
			if err := rows.Scan(&id, &t, &title, &artist); err != nil {
				rows.Close()
				log.Fatalf("scan: %v", err)
			}
			lastID = id
			got++
			processed++

			rawTitle := title.String
			rawArtist := artist.String
			sid := songID(rawTitle, rawArtist)
			pid := playID(t, sid)

			if _, ok := seenPlay[pid]; ok {
				duplicate++
				continue
			}
			seenPlay[pid] = struct{}{}

			tt := t

			meta, exists := songMeta[sid]
			if !exists {
				meta = songDoc{
					Title:         displayClean(rawTitle),
					Artist:        displayClean(rawArtist),
					NormalizedKey: normalize(rawTitle) + "|" + normalize(rawArtist),
					FirstAired:    &tt,
					LastAired:     &tt,
				}
			} else {
				if meta.FirstAired == nil || tt.Before(*meta.FirstAired) {
					meta.FirstAired = &tt
				}
				if meta.LastAired == nil || tt.After(*meta.LastAired) {
					meta.LastAired = &tt
				}
			}
			meta.PlayCount++
			songMeta[sid] = meta

			if *dryRun {
				continue
			}

			doc := fs.Collection(playsCollection).Doc(pid)
			if _, err := bw.Set(doc, playDoc{
				SongID:    sid,
				Time:      &tt,
				RawTitle:  rawTitle,
				RawArtist: rawArtist,
			}); err != nil {
				rows.Close()
				log.Fatalf("bulkwriter set play: %v", err)
			}
			queued++
		}
		if err := rows.Err(); err != nil {
			rows.Close()
			log.Fatalf("rows.Err: %v", err)
		}
		rows.Close()

		if got == 0 {
			break
		}

		if !*dryRun {
			bw.Flush()
		}
		elapsed := time.Since(start).Seconds()
		rate := float64(processed) / elapsed
		log.Printf("plays processed=%d queued=%d duplicate=%d unique-songs=%d lastID=%d elapsed=%.0fs rate=%.0f/s",
			processed, queued, duplicate, len(songMeta), lastID, elapsed, rate)
	}

	log.Printf("plays import done. processed=%d queued=%d duplicate=%d unique-songs=%d",
		processed, queued, duplicate, len(songMeta))

	if !*dryRun {
		var songsWritten int64
		for sid, meta := range songMeta {
			doc := fs.Collection(songsCollection).Doc(sid)
			if _, err := bw.Set(doc, meta); err != nil {
				log.Fatalf("bulkwriter set song: %v", err)
			}
			songsWritten++
			if songsWritten%5000 == 0 {
				bw.Flush()
				log.Printf("songs queued=%d / %d", songsWritten, len(songMeta))
			}
		}
		bw.End()
		log.Printf("songs import done. wrote=%d", songsWritten)
	}

	log.Printf("done. total=%.0fs", time.Since(start).Seconds())
}

func runReset() {
	projectID := mustGetenv("PROJECT_ID")
	ctx := context.Background()
	dbName := os.Getenv("FIRESTORE_DATABASE")
	if dbName == "" {
		dbName = firestore.DefaultDatabaseID
	}
	fs, err := firestore.NewClientWithDatabase(ctx, projectID, dbName)
	if err != nil {
		log.Fatalf("firestore client: %v", err)
	}
	defer fs.Close()

	for _, name := range []string{playsCollection, songsCollection} {
		log.Printf("resetting collection %q...", name)
		deleteCollection(ctx, fs, name)
	}
}

func deleteCollection(ctx context.Context, fs *firestore.Client, name string) {
	bw := fs.BulkWriter(ctx)
	col := fs.Collection(name)
	const pageSize = 500

	var deleted int64
	start := time.Now()
	for {
		iter := col.Limit(pageSize).Documents(ctx)
		var got int
		for {
			doc, err := iter.Next()
			if err == iterator.Done {
				break
			}
			if err != nil {
				iter.Stop()
				log.Fatalf("iter: %v", err)
			}
			if _, err := bw.Delete(doc.Ref); err != nil {
				iter.Stop()
				log.Fatalf("bulkwriter delete: %v", err)
			}
			got++
			deleted++
		}
		iter.Stop()
		if got == 0 {
			break
		}
		bw.Flush()
		elapsed := time.Since(start).Seconds()
		log.Printf("[%s] deleted=%d elapsed=%.0fs rate=%.0f/s",
			name, deleted, elapsed, float64(deleted)/elapsed)
	}
	bw.End()
	log.Printf("[%s] reset done. deleted=%d total=%.0fs", name, deleted, time.Since(start).Seconds())
}
