package updater

import (
	"errors"
	"strings"
	"testing"
)

func TestUpdateInfoFromManifestIgnoresMissingLinuxAsset(t *testing.T) {
	t.Parallel()

	info, err := updateInfoFromManifest(manifest{Version: "0.0.46", Platforms: map[string]manifestPlatform{}}, "linux-x64")
	if err != nil {
		t.Fatal(err)
	}
	if info != nil {
		t.Fatalf("info = %#v, want nil", info)
	}
}

func TestUpdateInfoFromManifestRejectsInvalidCurrentAsset(t *testing.T) {
	t.Parallel()

	_, err := updateInfoFromManifest(manifest{
		Version: "0.0.46",
		Platforms: map[string]manifestPlatform{
			"linux-x64": {URL: "https://example.invalid/update.tar.gz", Size: 1, Checksum: "sha256:not-a-checksum"},
		},
	}, "linux-x64")
	if err == nil || !strings.Contains(err.Error(), "checksum") {
		t.Fatalf("error = %v, want checksum validation error", err)
	}
}

func TestUpdateInfoFromManifestRejectsMissingNonLinuxAsset(t *testing.T) {
	t.Parallel()

	_, err := updateInfoFromManifest(manifest{Platforms: map[string]manifestPlatform{}}, "windows-x64")
	if !errors.Is(err, errNoSupportedAsset) {
		t.Fatalf("error = %v, want %v", err, errNoSupportedAsset)
	}
}

