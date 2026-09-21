package params

import (
	"os"
	"testing"
)

// writeFile is a small convenience wrapper for writing a test fixture
// file with the mode Save itself uses.
func writeFile(path, contents string) error {
	return os.WriteFile(path, []byte(contents), 0o644)
}

// chdirTemp changes the process's current working directory to dir
// for the duration of the calling test, restoring the original
// directory when the returned func runs. Resolve's implicit-file
// lookup is relative to os.Getwd(), so exercising it directly (rather
// than just Load, which takes an explicit path) needs this.
func chdirTemp(t *testing.T, dir string) func() {
	t.Helper()
	original, err := os.Getwd()
	if err != nil {
		t.Fatalf("Getwd: %v", err)
	}
	if err := os.Chdir(dir); err != nil {
		t.Fatalf("Chdir(%s): %v", dir, err)
	}
	return func() {
		if err := os.Chdir(original); err != nil {
			t.Fatalf("restoring cwd to %s: %v", original, err)
		}
	}
}
