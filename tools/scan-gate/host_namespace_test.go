package main

import (
	"strings"
	"testing"
)

func TestHostNamespaceStructuralBoundary(t *testing.T) {
	for _, kind := range []string{"Pod", "Deployment", "StatefulSet", "DaemonSet", "ReplicaSet", "ReplicationController", "Job", "CronJob", "UnknownWorkload", "ConfigMap"} {
		for _, field := range []string{"hostNetwork", "hostPID", "hostIPC"} {
			for _, value := range []string{"", "false", "False", "FALSE", "true", "null", "'false'", "'true'", "0", "[]", "{}"} {
				t.Run(kind+"/"+field+"/"+value, func(t *testing.T) {
					spec := "containers: [{name: app, image: synthetic}]\n"
					if value != "" {
						spec = field + ": " + value + "\n" + spec
					}
					prefix := "spec:\n"
					indent := "  "
					if kind == "CronJob" {
						prefix = "spec:\n  jobTemplate:\n    spec:\n      template:\n        spec:\n"
						indent = "          "
					} else if kind != "Pod" {
						prefix = "spec:\n  template:\n    spec:\n"
						indent = "      "
					}
					data := "apiVersion: v1\nkind: " + kind + "\nmetadata: {name: synthetic}\n" + prefix + indent + strings.ReplaceAll(strings.TrimSuffix(spec, "\n"), "\n", "\n"+indent) + "\n"
					findings, err := inspectYAML([]byte(data), "rendered/synthetic.yaml")
					if err != nil {
						t.Fatal(err)
					}
					want := 0
					if value != "" && !strings.EqualFold(value, "false") {
						want = 1
					}
					if len(findings) != want {
						t.Fatalf("expected %d findings, got %+v", want, findings)
					}
					if want == 1 && (findings[0].Rule != "LOCAL-HOST-NAMESPACE" || findings[0].Decision != "fail") {
						t.Fatal("host finding did not fail")
					}
				})
			}
		}
	}
}
