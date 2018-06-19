package main

import (
	"flag"
	"fmt"
	"io/ioutil"
	"os"
	"os/exec"
	"path/filepath"

	"github.com/pelletier/go-toml"
)

type Project struct {
	Branch   string   `toml:"branch,omitempty"`
	Name     string   `toml:"name"`
	Packages []string `toml:"packages"`
	Revision string   `toml:"revision"`
	Version  string   `toml:"version,omitempty"`
}

type LockFile struct {
	Projects  []*Project `toml:"projects"`
	SolveMeta struct {
		AnalyzerName    string `toml:"analyzer-name"`
		AnalyzerVersion int    `toml:"analyzer-version"`
		InputsDigest    string `toml:"inputs-digest"`
		SolverName      string `toml:"solver-name"`
		SolverVersion   int    `toml:"solver-version"`
	} `toml:"solve-meta"`
}

// readDepLockFile reads a dep lock file using a basic toml parser.
// It does not use the dep tool itself because vendored copies of a dependency
// won't pass the dep validation checks because of pruning.
func readDepLockFile(fpath string) (*LockFile, error) {
	in, err := ioutil.ReadFile(fpath)
	if err != nil {
		return nil, err
	}

	var lockFile LockFile
	if err := toml.Unmarshal(in, &lockFile); err != nil {
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
	fix := flag.Bool("fix", false, "attempts to fix the dependencies to match the specified project")
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

DIFF:
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
		if *fix {
			// If we have been told to fix the dependencies, then modify our project's
			// revision to the one theirs uses.
			myProj.Revision = theirProj.Revision
			return
		}

		if !headerPrinted {
			fmt.Fprintf(os.Stdout, "--- %s\n", packagePath())
			fmt.Fprintf(os.Stdout, "+++ %s\n", proj)
			headerPrinted = true
		}
		fmt.Fprintf(os.Stdout, "- %s %s\n+ %s %s\n", name, myProj.Revision, name, theirProj.Revision)
	}); diff > 0 {
		if *fix {
			// Write out the new lock file we have modified through the diff process.
			// It doesn't need to be the same byte-for-byte, just structure because dep will overwrite it
			// anyway.
			if err := writeTomlFile("Gopkg.lock", *myLock); err != nil {
				fmt.Fprintf(os.Stderr, "Error: Unable to update Gopkg.lock file: %s.\n", err)
				os.Exit(1)
			}

			// Rerun dep ensure so that it ensures the dependencies we switched to actually work and
			// the Gopkg.lock file is formatted correctly. It will rerun the solver and include the new
			// hints we have added to the beginning of the list of revisions to try.
			cmd := exec.Command("dep", "ensure")
			cmd.Stdout = os.Stdout
			cmd.Stderr = os.Stderr
			if err := cmd.Run(); err != nil {
				fmt.Fprintf(os.Stderr, "Error: Unable to execute `dep ensure` with updated Gopkg.lock.\n")
				os.Exit(1)
			}
			*fix = false
			goto DIFF
		}
		os.Exit(1)
	}
}

func writeTomlFile(fpath string, data interface{}) error {
	f, err := os.Create(fpath + ".new")
	if err != nil {
		return err
	}
	defer os.Remove(fpath + ".new")
	defer f.Close()

	fmt.Fprint(f, "# This file is autogenerated, do not edit; changes may be undone by the next 'dep ensure'.\n\n")

	enc := toml.NewEncoder(f).ArraysWithOneElementPerLine(true)
	if err := enc.Encode(data); err != nil {
		return err
	}

	if err := f.Close(); err != nil {
		return err
	}
	return os.Rename(fpath+".new", fpath)
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
