// Order の migration SQL を実行ファイルに埋め込み、配布時に SQL ファイルの取り忘れを防ぎます。
// Package ordermigrations owns Order SQL; Account migration history is independent.
package ordermigrations

import "embed"

//go:embed *.sql
var Files embed.FS
