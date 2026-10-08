package main

import (
	"flag"
	"fmt"
	"io"
	"os"
	"time"

	release "core-platform/release-record"
)

// Validation consumes stdin and emits only fixed status text, never input values.
func main() {
	at := flag.String("adopt-at", "", "RFC3339 current time for deployment adoption; empty validates stored evidence")
	flag.Parse()
	fail := func() { fmt.Fprintln(os.Stderr, "release record rejected"); os.Exit(1) }
	if flag.NArg() != 0 {
		fail()
	}
	b, err := io.ReadAll(io.LimitReader(os.Stdin, (1<<20)+1))
	if err != nil {
		fail()
	}
	r, err := release.DecodeRecord(b)
	if err != nil {
		fail()
	}
	if *at != "" {
		now, err := time.Parse(time.RFC3339, *at)
		if err != nil || r.Adopt(now) != nil {
			fail()
		}
	}
	fmt.Println("release record validated")
}
