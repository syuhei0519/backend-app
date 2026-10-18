// Package notificationmigrations owns Notification SQL; Account migration history is independent.
package notificationmigrations

import "embed"

//go:embed *.sql
var Files embed.FS
