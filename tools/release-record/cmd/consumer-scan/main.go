package main

import (
	release "github.com/syuhei0519/backend-app/tools/release-record"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"time"
)

func check() error {
	if len(os.Args) != 2 || (os.Args[1] != "current" && os.Args[1] != "rollback" && os.Args[1] != "current-inspected" && os.Args[1] != "rollback-inspected") {
		return release.ErrRefused
	}
	b, e := io.ReadAll(io.LimitReader(os.Stdin, 8193))
	if e != nil {
		return release.ErrRefused
	}
	s, e := release.DecodeConsumerSelection(b)
	if e != nil {
		return release.ErrRefused
	}
	a, e := release.NewSourceAPI(os.Getenv("MANIFEST_VERIFY_READ_API_TOKEN"))
	if e != nil {
		return release.ErrRefused
	}
	client, e := release.NewClient(os.Getenv("CI_JOB_TOKEN"))
	if e != nil {
		return release.ErrRefused
	}
	r, e := release.ReadConsumerScan(a, client, s, (os.Args[1] == "rollback" || os.Args[1] == "rollback-inspected"), time.Now().UTC())
	if e != nil {
		return release.ErrRefused
	}
	inspected := os.Args[1] == "current-inspected" || os.Args[1] == "rollback-inspected"
	if inspected && a.ReadConsumerInspection(r, time.Now().UTC()) != nil {
		return release.ErrRefused
	}
	return json.NewEncoder(os.Stdout).Encode(struct {
		Selection               release.ConsumerSelection `json:"selection"`
		PolicyRevision          string                    `json:"policyRevision"`
		ScanBytesVerified       bool                      `json:"scanBytesVerified"`
		NativeRecordMatched     bool                      `json:"nativeRecordMatched"`
		NativeInspectionMatched bool                      `json:"nativeInspectionMatched"`
		AdoptionAuthorized      bool                      `json:"adoptionAuthorized"`
	}{s, r.PolicyRevision, true, true, inspected, false})
}
func main() {
	if check() != nil {
		fmt.Fprintln(os.Stderr, "Current immutable scan or native authority refused")
		os.Exit(1)
	}
}
