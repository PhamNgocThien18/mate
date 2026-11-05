//go:build !js && !wasip1

package platform_test

import (
	"os"
	"os/exec"
	"strings"
	"testing"
)

// TestEveryBuildCompiles: make check runs on darwin, which compiles neither
// the build for systems without flock nor what the other flock systems'
// syscall packages lack. A call added to one build and not the other, or a
// syscall name one system does not have, would pass there and break a port.
// Vetting the package, its tests included, for linux (WSL2), illumos and
// windows type-checks both builds. js and wasip1 cannot start the go tool,
// hence the constraint above.
func TestEveryBuildCompiles(t *testing.T) {
	// go test caches this result by the files the darwin test binary is
	// built from and the files this process opens, never by what go vet
	// reads. Reading the package directory puts every file of it, those
	// only another build compiles included, in that cache key.
	if _, err := os.ReadDir("."); err != nil {
		t.Fatal(err)
	}
	for _, target := range []string{"linux/amd64", "illumos/amd64", "windows/amd64"} {
		goos, goarch, _ := strings.Cut(target, "/")
		vet := exec.Command("go", "vet", ".")
		vet.Env = append(os.Environ(), "GOOS="+goos, "GOARCH="+goarch, "CGO_ENABLED=0")
		if out, err := vet.CombinedOutput(); err != nil {
			t.Errorf("GOOS=%s GOARCH=%s go vet ./internal/platform: %v\n%s", goos, goarch, err, out)
		}
	}
}
