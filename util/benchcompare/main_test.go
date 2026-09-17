package main

import (
	"os"
	"path/filepath"
	"testing"
)

// makeFakeProjectRoot builds a minimal directory tree under t's temp
// directory that looksLikeProjectRoot/findProjectRoot should recognize:
// go_src/, rust_src/, and benchmark/<dirName>/ containing .cnf files.
func makeFakeProjectRoot(t *testing.T, benchmarkDirName string, cnfFiles ...string) string {
	t.Helper()
	root := t.TempDir()
	for _, d := range []string{"go_src", "rust_src", filepath.Join("benchmark", benchmarkDirName)} {
		if err := os.MkdirAll(filepath.Join(root, d), 0o755); err != nil {
			t.Fatalf("MkdirAll: %v", err)
		}
	}
	for _, name := range cnfFiles {
		path := filepath.Join(root, "benchmark", benchmarkDirName, name)
		if err := os.WriteFile(path, []byte("p cnf 1 1\n1 0\n"), 0o644); err != nil {
			t.Fatalf("WriteFile: %v", err)
		}
	}
	return root
}

func TestFindProjectRootFromNestedDirectory(t *testing.T) {
	root := makeFakeProjectRoot(t, "uf20-91", "a.cnf")
	nested := filepath.Join(root, "util", "benchcompare")
	if err := os.MkdirAll(nested, 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	got, err := findProjectRoot(nested)
	if err != nil {
		t.Fatalf("findProjectRoot: %v", err)
	}
	if got != root {
		t.Errorf("findProjectRoot = %q, want %q", got, root)
	}
}

func TestFindProjectRootFailsOutsideAnyProject(t *testing.T) {
	// t.TempDir() alone has none of go_src/rust_src/benchmark, and
	// (being freshly created under the OS temp directory) is not
	// nested inside this repository either.
	if _, err := findProjectRoot(t.TempDir()); err == nil {
		t.Fatal("findProjectRoot succeeded outside any project root, want an error")
	}
}

func TestSelectFilesReturnsAllWhenNoSampleRequested(t *testing.T) {
	root := makeFakeProjectRoot(t, "uf20-91", "a.cnf", "b.cnf", "c.cnf")
	files, err := selectFiles(root, []string{"uf20-91"}, 0, 1, 0)
	if err != nil {
		t.Fatalf("selectFiles: %v", err)
	}
	if len(files) != 3 {
		t.Errorf("selectFiles returned %d files, want 3", len(files))
	}
}

func TestSelectFilesSamplingIsDeterministicForAFixedSeed(t *testing.T) {
	root := makeFakeProjectRoot(t, "uf20-91", "a.cnf", "b.cnf", "c.cnf", "d.cnf", "e.cnf")
	first, err := selectFiles(root, []string{"uf20-91"}, 2, 42, 0)
	if err != nil {
		t.Fatalf("selectFiles: %v", err)
	}
	second, err := selectFiles(root, []string{"uf20-91"}, 2, 42, 0)
	if err != nil {
		t.Fatalf("selectFiles: %v", err)
	}
	if len(first) != 2 {
		t.Fatalf("selectFiles returned %d files, want 2", len(first))
	}
	if !stringSliceEqual(first, second) {
		t.Errorf("same seed produced different samples: %v vs %v", first, second)
	}
}

func TestSelectFilesUnknownDirectory(t *testing.T) {
	root := makeFakeProjectRoot(t, "uf20-91", "a.cnf")
	if _, err := selectFiles(root, []string{"does-not-exist"}, 0, 1, 0); err == nil {
		t.Fatal("selectFiles on a missing directory succeeded, want an error")
	}
}

// TestSelectFilesFindsNestedFiles confirms selectFiles finds .cnf
// files that are not directly inside benchmark/<dir> but one level
// deeper -- a real quirk of a few SATLIB directories in this
// project's actual benchmark/ tree (e.g. uuf100-430, uf75-325,
// uuf50-218, uuf75-325 all hold their files inside one extra nested
// folder, a leftover of how their original tarball extracted), first
// discovered when a real sweep across uuf100-430 silently returned 0
// of its files instead of erroring.
func TestSelectFilesFindsNestedFiles(t *testing.T) {
	root := makeFakeProjectRoot(t, "uuf100-430")
	nested := filepath.Join(root, "benchmark", "uuf100-430", "UUF100.430.1000")
	if err := os.MkdirAll(nested, 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	for _, name := range []string{"a.cnf", "b.cnf"} {
		if err := os.WriteFile(filepath.Join(nested, name), []byte("p cnf 1 1\n1 0\n"), 0o644); err != nil {
			t.Fatalf("WriteFile: %v", err)
		}
	}
	files, err := selectFiles(root, []string{"uuf100-430"}, 0, 1, 0)
	if err != nil {
		t.Fatalf("selectFiles: %v", err)
	}
	if len(files) != 2 {
		t.Fatalf("selectFiles found %d nested files, want 2 (got %v)", len(files), files)
	}
}

// TestSelectFilesExcludesFilesAboveMaxSize confirms --max-size-mb
// (STAGE29.md/REPORT29.md: sat_comp/2018 contains individual .cnf
// files large enough to OOM-kill a solving algorithm before any
// timeout would even fire) actually drops oversized files rather than
// merely reordering or sampling around them.
func TestSelectFilesExcludesFilesAboveMaxSize(t *testing.T) {
	root := makeFakeProjectRoot(t, "uf20-91")
	small := filepath.Join(root, "benchmark", "uf20-91", "small.cnf")
	big := filepath.Join(root, "benchmark", "uf20-91", "big.cnf")
	if err := os.WriteFile(small, []byte("p cnf 1 1\n1 0\n"), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	// Pad well past a 1-byte cap without needing a genuinely huge file.
	if err := os.WriteFile(big, []byte("p cnf 1 1\n1 0\nc padding to exceed the size cap\n"), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	all, err := selectFiles(root, []string{"uf20-91"}, 0, 1, 0)
	if err != nil {
		t.Fatalf("selectFiles (no cap): %v", err)
	}
	if len(all) != 2 {
		t.Fatalf("selectFiles (no cap) found %d files, want 2", len(all))
	}

	capped, err := selectFiles(root, []string{"uf20-91"}, 0, 1, int64(len("p cnf 1 1\n1 0\n")))
	if err != nil {
		t.Fatalf("selectFiles (capped): %v", err)
	}
	if len(capped) != 1 || filepath.Base(capped[0]) != "small.cnf" {
		t.Fatalf("selectFiles (capped) = %v, want just [small.cnf]", capped)
	}
}

// TestSelectPathsFindsFilesOutsideBenchmarkDir confirms selectPaths --
// added for sat_comp/2018, which is deliberately kept outside
// benchmark/ and out of the repository entirely (STAGE29.md) -- works
// against an arbitrary literal directory the same way selectFiles
// works against a benchmark/-relative name.
func TestSelectPathsFindsFilesOutsideBenchmarkDir(t *testing.T) {
	dir := t.TempDir()
	for _, name := range []string{"a.cnf", "b.cnf"} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte("p cnf 1 1\n1 0\n"), 0o644); err != nil {
			t.Fatalf("WriteFile: %v", err)
		}
	}
	// A non-.cnf file in the same directory must be ignored.
	if err := os.WriteFile(filepath.Join(dir, "notes.txt"), []byte("hi"), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	files, err := selectPaths([]string{dir}, 0, 1, 0)
	if err != nil {
		t.Fatalf("selectPaths: %v", err)
	}
	if len(files) != 2 {
		t.Fatalf("selectPaths found %d files, want 2 (got %v)", len(files), files)
	}
}

func TestSelectPathsUnknownDirectory(t *testing.T) {
	if _, err := selectPaths([]string{filepath.Join(t.TempDir(), "does-not-exist")}, 0, 1, 0); err == nil {
		t.Fatal("selectPaths on a missing directory succeeded, want an error")
	}
}
