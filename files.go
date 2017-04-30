package main

import (
	"crypto/sha1"
	"fmt"
	"github.com/libgit2/git2go"
	"io"
	"io/ioutil"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"
)

/* IsTexDocument reads filename, checks for .tex extension and looks
/* for \begin{document}. */
func IsTexDocument(path string) (bool, error) {
	if bool, _ := regexp.MatchString("\\.tex$", path); !bool {
		return false, nil
	}

	data, err := ioutil.ReadFile(path)
	if err != nil {
		return false, err
	}

	tex := string(data)

	// eliminate comments
	comments, _ := regexp.Compile("%.*")
	tex = comments.ReplaceAllString(tex, "")

	// eliminate whitespace
	whitespace, _ := regexp.Compile("\\s")
	tex = whitespace.ReplaceAllString(tex, "")

	bool, err := regexp.MatchString("\\\\begin{document}", tex)
	if err != nil {
		return false, err
	}

	return bool, nil
}

/* HashObject reads file with name filename and returns a git hash */
func HashObject(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()

	fi, err := f.Stat()
	if err != nil {
		return "", err
	}

	h := sha1.New()
	fmt.Fprintf(h, "blob %d\000", fi.Size())

	if _, err := io.Copy(h, f); err != nil {
		return "", err
	}

	stringHash := fmt.Sprintf("%x", h.Sum(nil))
	return stringHash, nil
}

/* IsClean compares filename to the current commit */
func IsClean(repositoryPath string, filename string) (bool, error) {
	//log.Debug("Checking whether " + filename + " matches what was committed in " + repositoryPath)

	// git seems to prefer (require?) relative paths from the repo root
	filename, err := filepath.Rel(repositoryPath, filename)
	if err != nil {
		return false, err
	}

	// Open the repository directory
	repo, err := git.OpenRepository(repositoryPath)
	if err != nil {
		return false, err
	}

	head, err := repo.Head()
	if err != nil {
		return false, err
	}

	headCommit, err := repo.LookupCommit(head.Target())
	if err != nil {
		return false, err
	}

	tree, err := headCommit.Tree()
	if err != nil {
		return false, err
	}

	entry, err := tree.EntryByPath(filename)
	if err != nil {
		return false, err
	}

	sha := entry.Id.String()

	hash, err := HashObject(filepath.Join(repositoryPath, filename))
	if err != nil {
		return false, err
	}

	return sha == hash, nil
}

/* LatexDependencies reads filename, looks for inputs and includes,
/* and callbacks with a list of normalized paths to dependencies */
func LatexDependencies(filename string) ([]string, error) {
	data, err := ioutil.ReadFile(filename)
	if err != nil {
		return []string{}, err
	}

	// Remove TeX comments
	lines := strings.Split(string(data), "\n")
	comments, _ := regexp.Compile("%.*")
	for i, _ := range lines {
		lines[i] = comments.ReplaceAllString(lines[i], "")
	}
	code := strings.Join(lines, "\n")

	// I have no idea why this is necessary---can we delete this?
	whitespace, _ := regexp.Compile("\\s")
	code = whitespace.ReplaceAllString(code, "")

	// Sometimes things are inside a verbatim environment; let's hackishly remove such things
	verbatim, _ := regexp.Compile("\\\\begin{verbatim}.*\\\\end{verbatim}")
	code = verbatim.ReplaceAllString(code, "")

	// Search for input or similar commands and gather the .tex filenames.
	//
	// Permit space between an input command and the filename in
	// braces
	includers, _ := regexp.Compile("\\\\(input|activity|include|includeonly)\\s*{([^}]+)}")

	matches := includers.FindAllStringSubmatch(code, -1)

	var dependencies []string

	for _, m := range matches {
		resolved, err := filepath.Abs(filepath.Join(filepath.Dir(filename), m[2]))
		if err == nil {
			f, err := os.Open(resolved)
			defer f.Close()

			if err == nil {
				dependencies = append(dependencies, resolved)
			} else {
				f, err := os.Open(resolved + ".tex")
				defer f.Close()

				if err == nil {
					dependencies = append(dependencies, resolved+".tex")
				}
			}
		}
	}

	return dependencies, nil
}

/* IsInRepository checks if filename is committed to the repo */
func IsInRepository(repositoryPath string, filename string) (bool, error) {
	//log.Debug("Checking whether " + filename + " is in the repository at " + repositoryPath)

	// git seems to prefer (require?) relative paths from the repo root
	filename, err := filepath.Rel(repositoryPath, filename)
	if err != nil {
		return false, err
	}

	repo, err := git.OpenRepository(repositoryPath)
	if err != nil {
		return false, err
	}

	head, err := repo.Head()
	if err != nil {
		return false, err
	}

	headCommit, err := repo.LookupCommit(head.Target())
	if err != nil {
		return false, err
	}

	tree, err := headCommit.Tree()
	if err != nil {
		return false, err
	}

	_, err = tree.EntryByPath(filename)
	if err != nil {
		return false, err
	}

	return true, nil
}

func FilesInRepository(directory string, condition func(string) (bool, error)) ([]string, error) {
	var files []string

	var visit = func(path string, f os.FileInfo, err error) error {
		passed, err := condition(path)
		// Ignore errors from the condition test
		if err != nil {
			return nil
		}

		if passed {
			committed, err := IsInRepository(directory, path)
			// Things fail to be in the repository when an error occurs
			if err != nil {
				return nil
			}

			if committed {
				clean, err := IsClean(directory, path)

				if err != nil {
					return err
				}

				if clean {
					files = append(files, path)
				} else {
					log.Warn(path + " is not committed to the repository")
				}
			} else {
				rel, _ := filepath.Rel(directory, path)
				log.Warn(rel + " differs from what has been committed.")
			}
		}

		return nil
	}

	log.Debug("Recursively listing all files in " + directory)
	err := filepath.Walk(directory, visit)
	if err != nil {
		return []string{}, err
	}

	return files, nil
}

func IsUpToDate(inputFilename string, outputFilename string, dependencies []string) (bool, error) {
	inputInfo, err := os.Stat(inputFilename)
	// nonexistent files are viewed as having a very old modification time
	inputTime := time.Unix(0, 0)
	if err == nil {
		inputTime = inputInfo.ModTime()
	}

	outputInfo, err := os.Stat(outputFilename)
	outputTime := time.Unix(0, 0)
	if err == nil {
		outputTime = outputInfo.ModTime()
	}

	if inputTime.After(outputTime) {
		return false, nil
	}

	for _, dependency := range dependencies {
		dependencyInfo, err := os.Stat(dependency)
		dependencyTime := time.Unix(0, 0)
		if err == nil {
			dependencyTime = dependencyInfo.ModTime()
		}

		if dependencyTime.After(outputTime) {
			return false, nil
		}

	}

	return true, nil
}

func TexFilesInRepository(directory string) ([]string, error) {
	return FilesInRepository(directory, IsTexDocument)
}

/* NeedingCompilation examines all the files in the given directory
/* (and its subdirectories) and calls callback with a list of files
/* that require compilation */
func NeedingCompilation(directory string) ([]string, error) {
	filenames, err := TexFilesInRepository(directory)
	var results []string

	if err != nil {
		return []string{}, err
	}

	for _, filename := range filenames {
		dependencies, err := LatexDependencies(filename)
		if err == nil {
			outputFilename := strings.TrimSuffix(filename, filepath.Ext(filename)) + ".html"
			good, err := IsUpToDate(filename, outputFilename, dependencies)

			if err == nil {
				if !good {
					results = append(results, filename)
				}
			}
		}
	}

	return results, nil
}
