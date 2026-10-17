// Package paymentmigrations owns Payment SQL; Account migration history is independent.
package paymentmigrations

import "embed"

//go:embed *.sql
var Files embed.FS
