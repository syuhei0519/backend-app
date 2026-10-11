// Package ordermigrations owns Order SQL; Account migration history is independent.
package ordermigrations

import "embed"

//go:embed *.sql
var Files embed.FS
