package server

import (
	"strings"
	"testing"
)

// Adopting an object that has no Helm ownership metadata is the caller's
// explicit choice. Only that choice adds the flag to the Helm command.
func TestBuildHelmCommandTakeOwnershipIsExplicit(t *testing.T) {
	cases := []struct {
		name          string
		takeOwnership bool
		want          bool
	}{
		{name: "default leaves existing objects alone", takeOwnership: false, want: false},
		{name: "explicit adoption adds the flag", takeOwnership: true, want: true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, _ = writeFakeHelm(t, "", "", "")
			params := testHelmParams()
			params.TakeOwnership = tc.takeOwnership
			command, err := buildHelmCommand(params)
			if err != nil {
				t.Fatal(err)
			}
			got := strings.Contains(strings.Join(command.args, " "), "--take-ownership")
			if got != tc.want {
				t.Fatalf("--take-ownership present = %t, want %t: %q", got, tc.want, command.args)
			}
		})
	}
}

// Rollback restores an earlier revision of this release; adoption has no
// meaning there, so the flag never reaches it.
func TestBuildHelmRollbackNeverTakesOwnership(t *testing.T) {
	_, _ = writeFakeHelm(t, "", "", "")
	params := testHelmParams()
	params.Operation = "rollback"
	params.Chart = ""
	params.Revision = 3
	params.TakeOwnership = true
	command, err := buildHelmCommand(params)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(strings.Join(command.args, " "), "--take-ownership") {
		t.Fatalf("rollback must not adopt objects: %q", command.args)
	}
}
