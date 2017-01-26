package main

import (
	"bufio"
	"bytes"
	"log"
	"os"
	"path/filepath"
	"runtime"
	"strings"
)

// FilePackageMap maps a file path to a list of package names
type FilePackageMap map[string][]string

// filePackageTuple is a tuple consisting of a file name and a package name
// containing the file.
type packageFileList struct {
	Package string
	Files   []string
}

// readPackageFileList reads a list of files belong to a specific package
func readPackageFileList(list string) packageFileList {
	pkg := strings.TrimSuffix(filepath.Base(list), ".list")
	result := packageFileList{Package: pkg}
	file, err := os.Open(list)
	if err != nil {
		return result
	}
	defer file.Close()
	reader := bufio.NewReader(file)
	for {
		lineBytes, err := reader.ReadBytes('\n')
		if err != nil {
			break
		}
		lineBytes = bytes.TrimSpace(lineBytes)
		line := string(lineBytes)

		result.Files = append(result.Files, line)
	}
	return result
}

// goPooled spawns #cpu-1 goroutines of f()
func goPooled(f func()) {
	max := runtime.NumCPU()
	if max > 1 {
		max--
	}
	if max > 256 { // Let's keep the file size limit in mind
		max = 256
	}
	for i := 0; i < max; i++ {
		go f()
	}
}

// NewFileToPackageMap constructs a map from file paths to package names
func NewFileToPackageMap() FilePackageMap {
	match, err := filepath.Glob("/var/lib/dpkg/info/*.list")
	if err != nil {
		log.Fatalf("%s", err)
	}

	// Process in parallel
	out := make(chan packageFileList)
	work := make(chan string)
	done := make(chan FilePackageMap)
	// Start multiple workers and an aggregator that reads their results and
	// creates a map.
	go func() {
		fileToPkg := make(FilePackageMap, 1024*256)
		for _ = range match {
			res := <-out
			for _, file := range res.Files {
				fileToPkg[file] = append(fileToPkg[file], res.Package)
			}
		}
		done <- fileToPkg
		close(done)
		close(out)
	}()
	goPooled(func() {
		for item := range work {
			out <- readPackageFileList(item)
		}
	})
	// Feed the workers with work
	for _, list := range match {
		work <- list
	}
	close(work)
	// Wait for the read worker to construct its data
	return <-done
}
