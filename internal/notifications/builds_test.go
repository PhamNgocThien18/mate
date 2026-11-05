//go:build !js && !wasip1

package notifications

import (
	"os"
	"os/exec"
	"strings"
	"testing"
)

// TestEveryBuildCompiles: make check runs on macOS, which compiles neither
// native_linux.go and its test nor native_other.go. Vetting the package,
// its tests included, for linux and windows type-checks them, as
// internal/platform's test of the same name does for its builds. It does
// not run the Linux tests.
func TestEveryBuildCompiles(t *testing.T) {
	// go test caches this result by the files the test binary is built
	// from and the files this process opens, never by what go vet reads.
	// Reading the package directory puts every file of it, those only
	// another build compiles included, in that cache key.
	if _, err := os.ReadDir("."); err != nil {
		t.Fatal(err)
	}
	for _, target := range []string{"linux/amd64", "windows/amd64"} {
		goos, goarch, _ := strings.Cut(target, "/")
		vet := exec.Command("go", "vet", ".")
		vet.Env = append(os.Environ(), "GOOS="+goos, "GOARCH="+goarch, "CGO_ENABLED=0")
		if out, err := vet.CombinedOutput(); err != nil {
			t.Errorf("GOOS=%s GOARCH=%s go vet ./internal/notifications: %v\n%s", goos, goarch, err, out)
		}
	}
}
