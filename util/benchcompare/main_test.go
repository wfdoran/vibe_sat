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
	files, err := selectFiles(root, []string{"uf20-91"}, 0, 1)
	if err != nil {
		t.Fatalf("selectFiles: %v", err)
	}
	if len(files) != 3 {
		t.Errorf("selectFiles returned %d files, want 3", len(files))
	}
}

func TestSelectFilesSamplingIsDeterministicForAFixedSeed(t *testing.T) {
	root := makeFakeProjectRoot(t, "uf20-91", "a.cnf", "b.cnf", "c.cnf", "d.cnf", "e.cnf")
	first, err := selectFiles(root, []string{"uf20-91"}, 2, 42)
	if err != nil {
		t.Fatalf("selectFiles: %v", err)
	}
	second, err := selectFiles(root, []string{"uf20-91"}, 2, 42)
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
	if _, err := selectFiles(root, []string{"does-not-exist"}, 0, 1); err == nil {
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
	files, err := selectFiles(root, []string{"uuf100-430"}, 0, 1)
	if err != nil {
		t.Fatalf("selectFiles: %v", err)
	}
	if len(files) != 2 {
		t.Fatalf("selectFiles found %d nested files, want 2 (got %v)", len(files), files)
	}
}
