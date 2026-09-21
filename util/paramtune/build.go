package main

// Locating the project root and building the three binaries a sweep
// needs once, up front: the Go and Rust vibe_sat binaries (handed to
// every benchcompare invocation via --go-bin/--rust-bin, so a sweep of
// many candidates never pays a rebuild per candidate) and
// util/benchcompare itself. This deliberately duplicates a small slice
// of benchcompare's own main.go (findProjectRoot/buildGoBinary/
// buildRustBinary) rather than importing it -- paramtune is a
// separate Go module (see go.mod) and benchcompare is "package main",
// so it isn't importable regardless; see this project's existing
// precedent (benchcompare's own cnf.go/verify.go) for independently
// re-deriving a small amount of logic rather than reaching for
// cross-module machinery to avoid it.

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
)

// findProjectRoot walks upward from start looking for a directory
// containing go_src/, rust_src/, and benchmark/ -- see benchcompare's
// own findProjectRoot for the identical reasoning.
func findProjectRoot(start string) (string, error) {
	abs, err := filepath.Abs(start)
	if err != nil {
		return "", err
	}
	dir := abs
	for range 32 {
		if looksLikeProjectRoot(dir) {
			return dir, nil
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			break
		}
		dir = parent
	}
	return "", fmt.Errorf("could not auto-detect the vibe_sat project root above %s; pass --project-root explicitly", abs)
}

func looksLikeProjectRoot(dir string) bool {
	for _, sub := range []string{"go_src", "rust_src", "benchmark"} {
		info, err := os.Stat(filepath.Join(dir, sub))
		if err != nil || !info.IsDir() {
			return false
		}
	}
	return true
}

// buildGoBinary builds go_src's vibe_sat binary into destDir, exactly
// as benchcompare's own buildGoBinary does.
func buildGoBinary(projectRoot, destDir string) (string, error) {
	dest := filepath.Join(destDir, "vibe_sat_go")
	cmd := exec.Command("go", "build", "-o", dest, "./cmd/vibe_sat")
	cmd.Dir = filepath.Join(projectRoot, "go_src")
	out, err := cmd.CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("building go binary: %w\n%s", err, out)
	}
	return dest, nil
}

// buildRustBinary builds rust_src's vibe_sat binary in release mode,
// exactly as benchcompare's own buildRustBinary does.
func buildRustBinary(projectRoot string) (string, error) {
	manifest := filepath.Join(projectRoot, "rust_src", "Cargo.toml")
	cmd := exec.Command("cargo", "build", "--release", "--quiet", "--manifest-path", manifest)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("building rust binary: %w\n%s", err, out)
	}
	return filepath.Join(projectRoot, "rust_src", "target", "release", "vibe_sat"), nil
}

// buildBenchcompareBinary builds util/benchcompare itself into
// destDir, once, so a sweep of many candidates invokes a single
// pre-built binary rather than paying "go run"'s build step on every
// candidate.
func buildBenchcompareBinary(projectRoot, destDir string) (string, error) {
	dest := filepath.Join(destDir, "benchcompare")
	cmd := exec.Command("go", "build", "-o", dest, ".")
	cmd.Dir = filepath.Join(projectRoot, "util", "benchcompare")
	out, err := cmd.CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("building benchcompare: %w\n%s", err, out)
	}
	return dest, nil
}
