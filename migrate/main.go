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
	"strings"
	"time"

	"cloud.google.com/go/firestore"
	_ "github.com/go-sql-driver/mysql"
)

const collection = "songs"

func mustGetenv(k string) string {
	v := os.Getenv(k)
	if v == "" {
		log.Fatalf("%s environment variable not set.", k)
	}
	return v
}

func songDocID(songTime time.Time, title, artist string) string {
	h := sha1.New()
	fmt.Fprintf(h, "%d\x00%s\x00%s", songTime.Unix(), title, artist)
	return hex.EncodeToString(h.Sum(nil))
}

type songDoc struct {
	Time   *time.Time `firestore:"time"`
	Artist string     `firestore:"artist"`
	Title  string     `firestore:"title"`
}

func main() {
	startID := flag.Int64("start-id", 0, "resume from this MySQL id (exclusive)")
	limit := flag.Int64("limit", 0, "max rows to migrate (0 = unlimited)")
	chunkSize := flag.Int64("chunk", 1000, "rows per chunk")
	dryRun := flag.Bool("dry-run", false, "do not write to Firestore")
	flag.Parse()

	dsn := mustGetenv("DATABASE_URI")
	if !strings.Contains(dsn, "parseTime=") {
		if strings.Contains(dsn, "?") {
			dsn += "&parseTime=true"
		} else {
			dsn += "?parseTime=true"
		}
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

	// BulkWriter rejects multiple writes against the same document path.
	// MySQL has duplicate (time, title, artist) rows, so dedupe by docID
	// in-process before enqueueing.
	seen := make(map[string]struct{})

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

			tt := t
			docID := songDocID(tt, title.String, artist.String)
			if _, ok := seen[docID]; ok {
				duplicate++
				continue
			}
			seen[docID] = struct{}{}

			if *dryRun {
				continue
			}
			doc := fs.Collection(collection).Doc(docID)
			if _, err := bw.Set(doc, songDoc{Time: &tt, Artist: artist.String, Title: title.String}); err != nil {
				rows.Close()
				log.Fatalf("bulkwriter set: %v", err)
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
		log.Printf("processed=%d queued=%d duplicate=%d lastID=%d elapsed=%.0fs rate=%.0f/s",
			processed, queued, duplicate, lastID, elapsed, rate)
	}

	if !*dryRun {
		bw.End()
	}
	log.Printf("done. processed=%d queued=%d duplicate=%d lastID=%d total=%.0fs",
		processed, queued, duplicate, lastID, time.Since(start).Seconds())
}
