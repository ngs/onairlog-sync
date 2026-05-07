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

	rawTable    = "songs"  // existing raw airplay log
	csongsTable = "csongs" // canonical songs (new)
	playsTable  = "plays"  // airplays with normalized song_id (new)
)

// ---------- normalization (mirrors onairlogsync.* so this module has no
// runtime-package dependency) ----------

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

// ---------- env / connection helpers ----------

func mustGetenv(k string) string {
	v := os.Getenv(k)
	if v == "" {
		log.Fatalf("%s environment variable not set.", k)
	}
	return v
}

func openMySQL() *sql.DB {
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
	db, err := sql.Open("mysql", dsn)
	if err != nil {
		log.Fatalf("mysql open: %v", err)
	}
	if err := db.Ping(); err != nil {
		log.Fatalf("mysql ping: %v", err)
	}
	return db
}

func openFirestore(ctx context.Context) *firestore.Client {
	projectID := mustGetenv("PROJECT_ID")
	dbName := os.Getenv("FIRESTORE_DATABASE")
	if dbName == "" {
		dbName = firestore.DefaultDatabaseID
	}
	fs, err := firestore.NewClientWithDatabase(ctx, projectID, dbName)
	if err != nil {
		log.Fatalf("firestore client: %v", err)
	}
	return fs
}

// ---------- main ----------

func main() {
	var (
		prep        = flag.Bool("prep", false, "Phase 1: build csongs/plays in MySQL from raw songs")
		reset       = flag.Bool("reset", false, "delete all docs in Firestore songs and plays, then exit")
		chunkSize   = flag.Int64("chunk", 1000, "rows per chunk")
		dryRun      = flag.Bool("dry-run", false, "do not write")
		limit       = flag.Int64("limit", 0, "(prep) max source rows (0 = unlimited)")
		startRawID  = flag.Int64("start-id", 0, "(prep) resume from raw songs id")
		startPlayID = flag.String("start-play-id", "", "(import) resume from plays.id (hex)")
		startSongID = flag.String("start-song-id", "", "(import) resume from csongs.id (hex)")
	)
	flag.Parse()

	switch {
	case *reset:
		runReset()
	case *prep:
		runPrep(*startRawID, *limit, *chunkSize, *dryRun)
	default:
		runImport(*startPlayID, *startSongID, *chunkSize, *dryRun)
	}
}

// ---------- Phase 1: prep MySQL tables ----------

const ddlCsongs = `CREATE TABLE IF NOT EXISTS csongs (
  id              VARCHAR(40)  NOT NULL PRIMARY KEY,
  title           VARCHAR(500),
  artist          VARCHAR(500),
  normalized_key  VARCHAR(1000),
  first_aired     DATETIME,
  last_aired      DATETIME,
  play_count      INT NOT NULL DEFAULT 0,
  KEY idx_last_aired (last_aired)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4`

const ddlPlays = `CREATE TABLE IF NOT EXISTS plays (
  id          VARCHAR(40) NOT NULL PRIMARY KEY,
  song_id     VARCHAR(40) NOT NULL,
  time        DATETIME NOT NULL,
  raw_title   VARCHAR(500),
  raw_artist  VARCHAR(500),
  KEY idx_time (time),
  KEY idx_song_id_time (song_id, time)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4`

func runPrep(startID, limit, chunkSize int64, dryRun bool) {
	db := openMySQL()
	defer db.Close()
	ctx := context.Background()

	if !dryRun {
		if _, err := db.ExecContext(ctx, ddlCsongs); err != nil {
			log.Fatalf("create csongs: %v", err)
		}
		if _, err := db.ExecContext(ctx, ddlPlays); err != nil {
			log.Fatalf("create plays: %v", err)
		}
	}

	stmtSong, err := db.PrepareContext(ctx, `INSERT INTO `+csongsTable+`
        (id, title, artist, normalized_key, first_aired, last_aired, play_count)
        VALUES (?, ?, ?, ?, ?, ?, 1)
        ON DUPLICATE KEY UPDATE
          first_aired = LEAST(first_aired, VALUES(first_aired)),
          last_aired  = GREATEST(last_aired, VALUES(last_aired)),
          play_count  = play_count + 1`)
	if err != nil {
		log.Fatalf("prepare song: %v", err)
	}
	defer stmtSong.Close()

	stmtPlay, err := db.PrepareContext(ctx, `INSERT IGNORE INTO `+playsTable+`
        (id, song_id, time, raw_title, raw_artist)
        VALUES (?, ?, ?, ?, ?)`)
	if err != nil {
		log.Fatalf("prepare play: %v", err)
	}
	defer stmtPlay.Close()

	var (
		lastID    = startID
		processed int64
	)
	start := time.Now()
	for {
		batch := chunkSize
		if limit > 0 {
			remaining := limit - processed
			if remaining <= 0 {
				break
			}
			if remaining < batch {
				batch = remaining
			}
		}

		rows, err := db.QueryContext(ctx,
			`SELECT id, time, title, artist FROM `+rawTable+`
			 WHERE id > ? AND deleted_at IS NULL AND time IS NOT NULL
			 ORDER BY id ASC LIMIT ?`, lastID, batch)
		if err != nil {
			log.Fatalf("query raw: %v", err)
		}

		tx, err := db.BeginTx(ctx, nil)
		if err != nil {
			rows.Close()
			log.Fatalf("begin tx: %v", err)
		}
		txStmtSong := tx.StmtContext(ctx, stmtSong)
		txStmtPlay := tx.StmtContext(ctx, stmtPlay)

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
				_ = tx.Rollback()
				log.Fatalf("scan: %v", err)
			}
			lastID = id
			got++
			processed++

			rawTitle := title.String
			rawArtist := artist.String
			sid := songID(rawTitle, rawArtist)
			pid := playID(t, sid)
			nKey := normalize(rawTitle) + "|" + normalize(rawArtist)

			if dryRun {
				continue
			}
			if _, err := txStmtSong.ExecContext(ctx,
				sid, displayClean(rawTitle), displayClean(rawArtist), nKey, t, t); err != nil {
				rows.Close()
				_ = tx.Rollback()
				log.Fatalf("insert csong: %v", err)
			}
			if _, err := txStmtPlay.ExecContext(ctx,
				pid, sid, t, rawTitle, rawArtist); err != nil {
				rows.Close()
				_ = tx.Rollback()
				log.Fatalf("insert play: %v", err)
			}
		}
		if err := rows.Err(); err != nil {
			rows.Close()
			_ = tx.Rollback()
			log.Fatalf("rows.Err: %v", err)
		}
		rows.Close()

		if got == 0 {
			_ = tx.Rollback()
			break
		}

		if dryRun {
			_ = tx.Rollback()
		} else if err := tx.Commit(); err != nil {
			log.Fatalf("commit: %v", err)
		}

		elapsed := time.Since(start).Seconds()
		rate := float64(processed) / elapsed
		log.Printf("prep processed=%d lastID=%d elapsed=%.0fs rate=%.0f/s",
			processed, lastID, elapsed, rate)
	}

	log.Printf("prep done. processed=%d lastID=%d total=%.0fs",
		processed, lastID, time.Since(start).Seconds())
}

// ---------- Phase 2: import to Firestore ----------

func runImport(startPlayID, startSongID string, chunkSize int64, dryRun bool) {
	db := openMySQL()
	defer db.Close()
	ctx := context.Background()
	fs := openFirestore(ctx)
	defer fs.Close()

	importPlays(ctx, db, fs, startPlayID, chunkSize, dryRun)
	importSongs(ctx, db, fs, startSongID, chunkSize, dryRun)
}

func importPlays(ctx context.Context, db *sql.DB, fs *firestore.Client, startID string, chunkSize int64, dryRun bool) {
	bw := fs.BulkWriter(ctx)
	col := fs.Collection(playsCollection)

	var (
		lastID    = startID
		processed int64
	)
	start := time.Now()
	for {
		rows, err := db.QueryContext(ctx,
			`SELECT id, song_id, time, raw_title, raw_artist FROM `+playsTable+`
			 WHERE id > ? ORDER BY id ASC LIMIT ?`, lastID, chunkSize)
		if err != nil {
			log.Fatalf("query plays: %v", err)
		}

		var got int64
		for rows.Next() {
			var (
				id        string
				sid       string
				t         time.Time
				rawTitle  sql.NullString
				rawArtist sql.NullString
			)
			if err := rows.Scan(&id, &sid, &t, &rawTitle, &rawArtist); err != nil {
				rows.Close()
				log.Fatalf("scan play: %v", err)
			}
			lastID = id
			got++
			processed++

			if dryRun {
				continue
			}
			tt := t
			if _, err := bw.Set(col.Doc(id), playDoc{
				SongID:    sid,
				Time:      &tt,
				RawTitle:  rawTitle.String,
				RawArtist: rawArtist.String,
			}); err != nil {
				rows.Close()
				log.Fatalf("bw set play: %v", err)
			}
		}
		if err := rows.Err(); err != nil {
			rows.Close()
			log.Fatalf("rows.Err: %v", err)
		}
		rows.Close()

		if got == 0 {
			break
		}
		if !dryRun {
			bw.Flush()
		}
		elapsed := time.Since(start).Seconds()
		rate := float64(processed) / elapsed
		log.Printf("plays imported=%d lastID=%s elapsed=%.0fs rate=%.0f/s",
			processed, lastID, elapsed, rate)
	}
	if !dryRun {
		bw.End()
	}
	log.Printf("plays import done. processed=%d total=%.0fs", processed, time.Since(start).Seconds())
}

func importSongs(ctx context.Context, db *sql.DB, fs *firestore.Client, startID string, chunkSize int64, dryRun bool) {
	bw := fs.BulkWriter(ctx)
	col := fs.Collection(songsCollection)

	var (
		lastID    = startID
		processed int64
	)
	start := time.Now()
	for {
		rows, err := db.QueryContext(ctx,
			`SELECT id, title, artist, normalized_key, first_aired, last_aired, play_count FROM `+csongsTable+`
			 WHERE id > ? ORDER BY id ASC LIMIT ?`, lastID, chunkSize)
		if err != nil {
			log.Fatalf("query csongs: %v", err)
		}

		var got int64
		for rows.Next() {
			var (
				id        string
				title     sql.NullString
				artist    sql.NullString
				nkey      sql.NullString
				first     sql.NullTime
				last      sql.NullTime
				playCount int
			)
			if err := rows.Scan(&id, &title, &artist, &nkey, &first, &last, &playCount); err != nil {
				rows.Close()
				log.Fatalf("scan song: %v", err)
			}
			lastID = id
			got++
			processed++

			if dryRun {
				continue
			}
			doc := songDoc{
				Title:         title.String,
				Artist:        artist.String,
				NormalizedKey: nkey.String,
				PlayCount:     playCount,
			}
			if first.Valid {
				v := first.Time
				doc.FirstAired = &v
			}
			if last.Valid {
				v := last.Time
				doc.LastAired = &v
			}
			if _, err := bw.Set(col.Doc(id), doc); err != nil {
				rows.Close()
				log.Fatalf("bw set song: %v", err)
			}
		}
		if err := rows.Err(); err != nil {
			rows.Close()
			log.Fatalf("rows.Err: %v", err)
		}
		rows.Close()

		if got == 0 {
			break
		}
		if !dryRun {
			bw.Flush()
		}
		elapsed := time.Since(start).Seconds()
		rate := float64(processed) / elapsed
		log.Printf("songs imported=%d lastID=%s elapsed=%.0fs rate=%.0f/s",
			processed, lastID, elapsed, rate)
	}
	if !dryRun {
		bw.End()
	}
	log.Printf("songs import done. processed=%d total=%.0fs", processed, time.Since(start).Seconds())
}

// ---------- Firestore docs ----------

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

// ---------- Reset ----------

func runReset() {
	ctx := context.Background()
	fs := openFirestore(ctx)
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
