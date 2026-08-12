package backend

import "testing"

func TestSelectKiroCLIWindowsPackageOnlySelectsWindowsX64MSI(t *testing.T) {
	packages := []kiroCLIPackage{
		{OS: "windows", Architecture: "arm64", Kind: "msi", Download: "2.17.0/arm64.msi"},
		{OS: "linux", Architecture: "x86_64", Kind: "deb", Download: "2.17.0/linux.deb"},
		{OS: "windows", Architecture: "x86_64", Kind: "msi", Download: "2.17.0/kiro-cli.msi"},
	}

	packageInfo, ok := selectKiroCLIWindowsPackage(packages)
	if !ok || packageInfo.Download != "2.17.0/kiro-cli.msi" {
		t.Fatalf("selectKiroCLIWindowsPackage() = %#v, %t", packageInfo, ok)
	}
}

func TestIsSafeKiroCLIPackageRejectsUnsafeManifestEntries(t *testing.T) {
	valid := kiroCLIPackage{
		Download: "2.17.0/kiro-cli-x86_64-pc-windows-msvc.msi",
		SHA256:   "16d3c900184ccdf9aed4fecebad920df0e8e91ef0ae8ff1a8eb74f9710fc2e18",
		Size:     250822656,
	}
	if !isSafeKiroCLIPackage(valid) {
		t.Fatal("isSafeKiroCLIPackage(valid) = false")
	}
	for _, packageInfo := range []kiroCLIPackage{
		{Download: "../kiro-cli.msi", SHA256: valid.SHA256, Size: valid.Size},
		{Download: "2.17.0/kiro-cli.msi?redirect=1", SHA256: valid.SHA256, Size: valid.Size},
		{Download: valid.Download, SHA256: "not-a-hash", Size: valid.Size},
		{Download: valid.Download, SHA256: valid.SHA256, Size: 0},
		{Download: valid.Download, SHA256: valid.SHA256, Size: kiroCLIMaxInstallerSize + 1},
	} {
		if isSafeKiroCLIPackage(packageInfo) {
			t.Fatalf("isSafeKiroCLIPackage(%#v) = true", packageInfo)
		}
	}
}
