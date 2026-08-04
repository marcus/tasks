package main

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/marcus/tasks/internal/config"
	"github.com/marcus/tasks/internal/runtimepath"
)

func init() {
	register("install-merge-driver", installMergeDriver)
}

func installMergeDriver(_ *surfaceContext, args []string) int {
	flags, rest, flagErr := takeFlags(args, "--json")
	if flagErr != nil {
		return abort(flagErr.Error())
	}
	args = rest
	asJSON := flags["--json"]
	if len(args) > 1 {
		return abort("usage: tasks install-merge-driver [DATA_REPO] [--json]")
	}

	repo := ""
	if len(args) == 1 {
		repo = args[0]
	} else {
		paths := config.Resolve("", env, nil)
		if paths.Configured {
			repo = filepath.Dir(paths.Org)
		} else {
			cwd, err := os.Getwd()
			if err != nil {
				return abort("install-merge-driver: cannot resolve current directory")
			}
			repo = cwd
		}
	}

	top, err := gitOutput(repo, "rev-parse", "--show-toplevel")
	if err != nil {
		return abort(fmt.Sprintf("not a git repository: %s (%v)", repo, err))
	}
	repo = strings.TrimSpace(top)
	attributes := filepath.Join(repo, ".gitattributes")
	attributeBytes, err := os.ReadFile(attributes)
	if err != nil {
		return abort(fmt.Sprintf("%s must select merge=tasksjsonl for tasks.jsonl and archive.jsonl", attributes))
	}
	missing := missingAttributes(string(attributeBytes))
	if len(missing) > 0 {
		return abort(fmt.Sprintf("%s must select merge=tasksjsonl for tasks.jsonl and archive.jsonl (missing: %s)",
			attributes, strings.Join(missing, ", ")))
	}

	driver := driverShellQuote(runtimepath.Executable()) + " merge-driver %O %A %B %P %L %X %Y"
	if _, err := gitOutput(repo, "config", "merge.tasksjsonl.name", "tasks jsonl 3-way record merge"); err != nil {
		return abort("install-merge-driver: " + err.Error())
	}
	if _, err := gitOutput(repo, "config", "merge.tasksjsonl.driver", driver); err != nil {
		return abort("install-merge-driver: " + err.Error())
	}

	if asJSON {
		w := jsonWriter()
		w.BeginObject()
		w.KeyBool("installed", true)
		w.KeyStr("repository", repo)
		w.KeyStr("driver", driver)
		w.EndObject()
		out(w.String())
		return 0
	}
	out("installed tasksjsonl merge driver in " + repo)
	out("  " + driver)
	return 0
}

func missingAttributes(text string) []string {
	found := map[string]bool{}
	for _, line := range strings.Split(text, "\n") {
		fields := strings.Fields(line)
		if len(fields) < 2 {
			continue
		}
		if (fields[0] == "tasks.jsonl" || fields[0] == "archive.jsonl") && fields[1] == "merge=tasksjsonl" {
			found[fields[0]] = true
		}
	}
	missing := []string{}
	for _, name := range []string{"tasks.jsonl", "archive.jsonl"} {
		if !found[name] {
			missing = append(missing, name)
		}
	}
	return missing
}

func gitOutput(repo string, args ...string) (string, error) {
	command := exec.Command("git", append([]string{"-C", repo}, args...)...)
	output, err := command.CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("git %s failed: %s", strings.Join(args, " "), strings.TrimSpace(string(output)))
	}
	return string(output), nil
}

func driverShellQuote(value string) string {
	return "'" + strings.ReplaceAll(value, "'", "'\"'\"'") + "'"
}
