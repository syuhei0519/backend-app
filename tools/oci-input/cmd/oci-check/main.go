package main

import (
	oci "core-platform/oci-input"
	"encoding/json"
	"fmt"
	"os"
)

func main() {
	if len(os.Args) != 5 {
		fmt.Fprintln(os.Stderr, "OCI input refused")
		os.Exit(1)
	}
	p, e := oci.Validate(os.Args[1], os.Args[2], os.Args[3], os.Args[4])
	if e != nil {
		fmt.Fprintln(os.Stderr, "OCI input refused")
		os.Exit(1)
	}
	if json.NewEncoder(os.Stdout).Encode(p) != nil {
		os.Exit(1)
	}
}
