package schemahistory

import "testing"

func TestClassifyMatrix(t *testing.T) {
	tests := []struct {
		name      string
		available bool
		exists    bool
		versions  []int
		want      State
	}{
		{"unavailable", false, false, nil, Unavailable},
		{"no table", true, false, nil, NoTable},
		{"empty", true, true, nil, Empty},
		{"c0", true, true, []int{1}, C0},
		{"expand", true, true, []int{1, 2}, Expand},
		{"missing base", true, true, []int{2}, MissingBase},
		{"gap", true, true, []int{1, 3}, Gap},
		{"unknown future", true, true, []int{1, 2, 3}, Future},
		{"duplicate", true, true, []int{1, 1}, Gap},
		{"order independent", true, true, []int{2, 1}, Expand},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := Classify(tt.available, tt.exists, tt.versions); got != tt.want {
				t.Fatalf("Classify()=%q want %q", got, tt.want)
			}
		})
	}
}

func TestC0GatesAreExact(t *testing.T) {
	for _, state := range []State{Unavailable, NoTable, Empty, Expand, MissingBase, Gap, Future} {
		if C0Ready(state) {
			t.Fatalf("C0 readiness accepted %q", state)
		}
	}
	if !C0Ready(C0) {
		t.Fatal("exact C0 was not ready")
	}
	for _, state := range []State{Unavailable, NoTable, Expand, MissingBase, Gap, Future} {
		if C0MigrationAllowed(state) {
			t.Fatalf("C0 migration accepted %q", state)
		}
	}
	if !C0MigrationAllowed(Empty) || !C0MigrationAllowed(C0) {
		t.Fatal("empty/C0 migration was rejected")
	}
}
