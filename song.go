package onairlogsync

import "time"

type Song struct {
	Time   *time.Time `firestore:"time" json:"time"`
	Artist string     `firestore:"artist" json:"artist"`
	Title  string     `firestore:"title" json:"title"`
}
