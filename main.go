package main

import (
	"flag"
	"fmt"
	"os"
	"path/filepath"

	"github.com/BurntSushi/toml"
)

type Project struct {
	Name     string   `toml:"name"`
	Packages []string `toml:"packages"`
	Branch   string   `toml:"branch,omitempty"`
	Revision string   `toml:"revision"`
	Version  string   `toml:"version,omitempty"`
}

type LockFile struct {
	Projects  []*Project             `toml:"projects"`
	SolveMeta map[string]interface{} `toml:"solve-meta"`
}

// readDepLockFile reads a dep lock file using a basic toml parser.
// It does not use the dep tool itself because vendored copies of a dependency
// won't pass the dep validation checks because of pruning.
func readDepLockFile(fpath string) (*LockFile, error) {
	var lockFile LockFile
	if _, err := toml.DecodeFile(fpath, &lockFile); err != nil {
		return nil, err
	}
	return &lockFile, nil
}

// diffProjectDeps compares the dependencies from project a to project b.
// If any of the dependencies are present in both but different, ondiff is called with
// the project name as the first argument and the project specification for each of the
// two arguments as the second and third argument. The function may feel free to modify
// these structures. If ondiff is nil, then it will not be called.
// The number of differences is returned.
func diffProjectDeps(a, b *LockFile, ondiff func(name string, a, b *Project)) int {
	adeps := make(map[string]string, len(a.Projects))
	for _, proj := range a.Projects {
		adeps[proj.Name] = proj.Revision
	}
	bdeps := make(map[string]string, len(b.Projects))
	for _, proj := range b.Projects {
		bdeps[proj.Name] = proj.Revision
	}

	var numDifferences int
	for name, arev := range adeps {
		brev, ok := bdeps[name]
		if !ok {
			continue
		} else if arev != brev {
			if ondiff != nil {
				// Find the project struct in each of the lock files so we can pass it
				// to ondiff.
				var aproj, bproj *Project
				for _, proj := range a.Projects {
					if proj.Name == name {
						aproj = proj
						break
					}
				}
				for _, proj := range b.Projects {
					if proj.Name == name {
						bproj = proj
						break
					}
				}
				ondiff(name, aproj, bproj)
			}
			numDifferences++
		}
	}
	return numDifferences
}

func main() {
	flag.Parse()

	args := flag.Args()
	if len(args) != 1 {
		fmt.Fprintf(os.Stderr, "Error: Exactly one project must be specified.\n")
		os.Exit(1)
	}

	// Verify the project exists and is in the vendor folder.
	proj := args[0]
	if st, err := os.Stat(filepath.Join("vendor", proj)); err != nil {
		if os.IsNotExist(err) {
			fmt.Fprintf(os.Stderr, "Error: Project %s is missing from the vendor directory. Please run `dep ensure` and verify it is a dependency of the current project.\n", proj)
			os.Exit(1)
		} else {
			fmt.Fprintf(os.Stderr, "Error: %s\n", err)
			os.Exit(1)
		}
	} else {
		// Verify this is a directory.
		if !st.IsDir() {
			fmt.Fprintf(os.Stderr, "Error: Project %s is in vendor, but it is not a directory.\n", proj)
			os.Exit(1)
		}
	}

	// The project should have a Gopkg.lock. We need to read it directly with a toml parser.
	projLock, err := readDepLockFile(filepath.Join("vendor", proj, "Gopkg.lock"))
	if err != nil {
		if os.IsNotExist(err) {
			fmt.Fprintf(os.Stderr, "Error: Project %s does not have a Gopkg.lock file.\n", proj)
		} else {
			fmt.Fprintf(os.Stderr, "Error: Unable to read the Gopkg.lock file for project %s: %s.\n", proj, err)
		}
		os.Exit(1)
	}

	// Read the current project's lock file.
	myLock, err := readDepLockFile("Gopkg.lock")
	if err != nil {
		if os.IsNotExist(err) {
			fmt.Fprintf(os.Stderr, "Error: No Gopkg.lock file in the current directory.\n")
		} else {
			fmt.Fprintf(os.Stderr, "Error: Unable to read the Gopkg.lock file for the current directory: %s.\n", err)
		}
		os.Exit(1)
	}

	// Diff the lock files and output a helper message for each one of these if there is a difference.
	var headerPrinted bool
	if diff := diffProjectDeps(myLock, projLock, func(name string, myProj, theirProj *Project) {
		if !headerPrinted {
			fmt.Fprintf(os.Stdout, "--- %s\n", packagePath())
			fmt.Fprintf(os.Stdout, "+++ %s\n", proj)
			headerPrinted = true
		}
		fmt.Fprintf(os.Stdout, "- %s %s\n+ %s %s\n", name, myProj.Revision, name, theirProj.Revision)
	}); diff > 0 {
		os.Exit(1)
	}
}

// packagePath returns the current package path by using the current directory and trimming the GOPATH.
// If it can't do this, it returns "." or an absolute path to the current directory.
func packagePath() string {
	cwd, err := os.Getwd()
	if err != nil {
		return "."
	}

	gopath := filepath.SplitList(os.Getenv("GOPATH"))
	if len(gopath) == 0 {
		return cwd
	}

	relpath, err := filepath.Rel(filepath.Join(gopath[0], "src"), cwd)
	if err != nil {
		return cwd
	}
	return relpath
}
