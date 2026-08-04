package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestInstallMergeDriverConfiguresRepositoryIdempotently(t *testing.T) {
	repo := t.TempDir()
	gitRun(t, repo, "init", "-q")
	attributes := "tasks.jsonl merge=tasksjsonl\narchive.jsonl merge=tasksjsonl\n"
	if err := os.WriteFile(filepath.Join(repo, ".gitattributes"), []byte(attributes), 0o644); err != nil {
		t.Fatal(err)
	}

	for range 2 {
		if status := installMergeDriver(nil, []string{repo, "--json"}); status != 0 {
			t.Fatalf("install status = %d", status)
		}
	}
	driver := strings.TrimSpace(gitRun(t, repo, "config", "--get", "merge.tasksjsonl.driver"))
	if !strings.Contains(driver, " merge-driver %O %A %B %P %L %X %Y") {
		t.Fatalf("driver = %q", driver)
	}
	if name := strings.TrimSpace(gitRun(t, repo, "config", "--get", "merge.tasksjsonl.name")); name != "tasks jsonl 3-way record merge" {
		t.Fatalf("name = %q", name)
	}
}

func TestInstallMergeDriverRequiresBothAttributes(t *testing.T) {
	repo := t.TempDir()
	gitRun(t, repo, "init", "-q")
	if err := os.WriteFile(filepath.Join(repo, ".gitattributes"), []byte("tasks.jsonl merge=tasksjsonl\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if status := installMergeDriver(nil, []string{repo}); status == 0 {
		t.Fatal("installer accepted an incomplete attributes file")
	}
}

func gitRun(t *testing.T, repo string, args ...string) string {
	t.Helper()
	command := exec.Command("git", append([]string{"-C", repo}, args...)...)
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("git %s: %v: %s", strings.Join(args, " "), err, output)
	}
	return string(output)
}
