// Pod がホストの名前空間を共有する危険な設定を、描画結果の検査で拒否することを確認します。
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

func TestCRDHostNamespaceSchemaBoundary(t *testing.T) {
	crd := func(properties, extra string) string {
		return "apiVersion: apiextensions.k8s.io/v1\nkind: CustomResourceDefinition\nmetadata: {name: synthetic.example.test}\nspec:\n  versions:\n  - name: v1\n    schema:\n      openAPIV3Schema:\n        type: object\n        properties:\n          spec:\n            type: object\n            properties:\n              template:\n                type: object\n                properties:\n                  spec:\n                    type: object\n                    properties:\n" + properties + extra
	}
	const indent = "                      "
	for _, field := range []string{"hostNetwork", "hostPID", "hostIPC"} {
		for _, tc := range []struct {
			name, definition, extra string
			want                    int
		}{
			{"declaration", "{type: boolean}", "", 0},
			{"false default", "{type: boolean, default: false}", "", 0},
			{"true default", "{type: boolean, default: true}", "", 1},
			{"invalid default", "{type: boolean, default: 'false'}", "", 1},
			{"untyped definition", "{description: synthetic}", "", 1},
			{"actual value in properties", "true", "", 1},
			{"hidden PodSpec", "{type: boolean}", "  syntheticPodSpec: {" + field + ": true}\n", 1},
			{"schema default PodSpec", "{type: boolean}", "        default: {" + field + ": true}\n", 1},
			{"schema example PodSpec", "{type: boolean}", "        example: {" + field + ": true}\n", 1},
			{"unknown extension PodSpec", "{type: boolean}", "        x-synthetic: {" + field + ": true}\n", 1},
			{"extra declaration data", "{type: boolean, " + field + ": true}", "", 1},
		} {
			t.Run(field+"/"+tc.name, func(t *testing.T) {
				data := crd(indent+field+": "+tc.definition+"\n", tc.extra)
				findings, err := inspectYAML([]byte(data), "rendered/crd.yaml")
				if err != nil || len(findings) != tc.want {
					t.Fatalf("want %d host findings, got %+v, error %v", tc.want, findings, err)
				}
				for _, finding := range findings {
					if finding.Rule != "LOCAL-HOST-NAMESPACE" || finding.Decision != "fail" {
						t.Fatal("host setting did not fail")
					}
				}
			})
		}
		// A matching shape in an unknown kind/API is ordinary data, not CRD schema.
		for _, original := range []string{"kind: CustomResourceDefinition", "apiVersion: apiextensions.k8s.io/v1"} {
			data := strings.Replace(crd(indent+field+": {type: boolean}\n", ""), original, original+"Unknown", 1)
			findings, err := inspectYAML([]byte(data), "rendered/unknown.yaml")
			if err != nil || len(findings) != 1 {
				t.Fatal("unknown schema-like workload bypassed host boundary")
			}
		}
	}
}
