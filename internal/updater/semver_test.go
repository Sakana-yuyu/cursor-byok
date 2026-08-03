package updater

import "testing"

func TestReleaseVersionIsNewerThanV0062(t *testing.T) {
	if compareVersions("0.0.63", "0.0.62") <= 0 {
		t.Fatal("0.0.63 must be newer than 0.0.62")
	}
}
