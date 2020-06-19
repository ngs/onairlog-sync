package onairlogsync

import (
	"time"

	"github.com/jinzhu/gorm"
)

type Song struct {
	gorm.Model
	Time   *time.Time
	Artist string
	Title  string
}
