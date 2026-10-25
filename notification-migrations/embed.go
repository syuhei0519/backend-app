// Notification の migration SQL を実行ファイルに埋め込み、配布時に SQL ファイルの取り忘れを防ぎます。
// Package notificationmigrations owns Notification SQL; Account migration history is independent.
package notificationmigrations

import "embed"

//go:embed *.sql
var Files embed.FS
