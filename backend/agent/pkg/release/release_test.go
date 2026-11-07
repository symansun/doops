package release

import "testing"

func TestValidCommitRequiresFullLowercaseSHA(t *testing.T) {
	if !ValidCommit("0123456789abcdef0123456789abcdef01234567") {
		t.Fatal("full lowercase commit should be valid")
	}
	for _, value := range []string{
		"cc4a56843",
		"0123456789abcdef0123456789abcdef0123456G",
		"0123456789abcdef0123456789abcdef012345678",
	} {
		if ValidCommit(value) {
			t.Fatalf("commit %q should be invalid", value)
		}
	}
}
