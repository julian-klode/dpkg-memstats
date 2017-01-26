// Package main prints the memory usage of the system grouped
// by Debian package.
package main

import (
	"bufio"
	"bytes"
	"flag"
	"fmt"
	"io/ioutil"
	"log"
	"os"
	"path/filepath"
	"runtime"
	"runtime/pprof"
	"sort"
	"strconv"
	"strings"
	"text/tabwriter"
	"unsafe"

	"github.com/dustin/go-humanize"
)

// FilePackageTuple is a tuple consisting of a file name and a package name
// containing the file.
type FilePackageTuple struct {
	File    string
	Package string
}

// MemUsage returns the memory usage of the given process, in bytes
func MemUsage(pid string) uint64 {
	file, err := os.Open(filepath.Join("/proc", pid, "smaps"))
	if err != nil {
		fmt.Println("Error in", pid, err)
		return 0
	}
	defer file.Close()
	reader := bufio.NewReader(file)
	var sum uint64
	for {
		line, err := reader.ReadBytes('\n')
		if err != nil {
			break
		}
		if !bytes.HasPrefix(line, []byte("Pss:")) {
			continue
		}
		// Trim off Pss: on the left side
		line = line[4:]
		// Look until the first number
		for line[0] < '0' || line[0] > '9' {
			line = line[1:]
		}
		// Trim non-digits on the right side
		for line[len(line)-1] < '0' || line[len(line)-1] > '9' {
			line = line[:len(line)-1]
		}

		i, _ := strconv.ParseUint(*(*string)(unsafe.Pointer(&line)), 10, 64)
		sum += i * 1024
	}
	return sum
}

// ReadPackageFileList returns a slice of tuples (filename, packagename)
func ReadPackageFileList(list string) []FilePackageTuple {
	pkg := filepath.Base(list)
	result := make([]FilePackageTuple, 0, 16)
	file, err := os.Open(list)
	if err != nil {

	}
	defer file.Close()
	reader := bufio.NewReader(file)
	for {
		lineBytes, err := reader.ReadBytes('\n')
		if err != nil {
			break
		}
		line := strings.TrimSpace(*(*string)(unsafe.Pointer(&lineBytes)))

		result = append(result, FilePackageTuple{File: line, Package: pkg})

	}
	return result
}

type PackageMap map[string][]string

// GoPooled spawns #cpu-1 goroutines of f()
func GoPooled(f func()) {
	max := runtime.NumCPU()
	if max > 1 {
		max--
	}
	if max > 256 {
		max = 256
	}
	for i := 0; i < max; i++ {
		go f()
	}
}

// NewFileToPackageMap reads a map file -> [pkg] from dpkg
func NewFileToPackageMap() PackageMap {
	match, err := filepath.Glob("/var/lib/dpkg/info/*.list")
	if err != nil {
		log.Fatalf("%s", err)
	}

	fileToPkg := make(map[string][]string, 1024*256)

	// Process this bastard in parallel

	out := make(chan []FilePackageTuple, 16)
	work := make(chan string, 16)
	done := make(chan interface{})
	// Reader
	go func() {
		for _, _ = range match {
			res := <-out
			for _, t := range res {
				fileToPkg[t.File] = append(fileToPkg[t.File], t.Package)
			}
		}
		close(done)
		close(out)
	}()
	GoPooled(func() {
		for item := range work {
			out <- ReadPackageFileList(item)
		}
	})
	// Feed the system
	for _, list := range match {
		work <- list
	}
	// Wait for the system to finish
	close(work)
	<-done

	return fileToPkg
}

type ProcInfo struct {
	Exe  string
	Cmd  string
	Pid  string
	Pss  uint64
	Pkgs []string
}

// ProcInfoSlice is a sortable slice of ProcInfo pointers
type ProcInfoSlice []ProcInfo

func (s ProcInfoSlice) Len() int {
	return len(s)
}
func (s ProcInfoSlice) Swap(i, j int) {
	s[i], s[j] = s[j], s[i]
}
func (s ProcInfoSlice) Less(i, j int) bool {
	return s[i].Pss < s[j].Pss
}

// PackageInfo contains information about a package
type PackageInfo struct {
	pkg   string
	pss   uint64
	procs ProcInfoSlice
}

// PackageInfoSlice is a sortable slice of packages
type PackageInfoSlice []PackageInfo

func (s PackageInfoSlice) Len() int {
	return len(s)
}
func (s PackageInfoSlice) Swap(i, j int) {
	s[i], s[j] = s[j], s[i]
}
func (s PackageInfoSlice) Less(i, j int) bool {
	return s[i].pss < s[j].pss
}

// NewProcInfo generates a new process info for the given pid
func (m PackageMap) NewProcInfo(pid string) ProcInfo {
	info := ProcInfo{Pss: MemUsage(pid), Pid: pid}
	info.Exe = filepath.Join("/proc", pid, "exe")

	if info.Exe, _ = os.Readlink(info.Exe); info.Exe != "" {
	}

	if cmdline, err := ioutil.ReadFile(filepath.Join("/proc", pid, "cmdline")); err == nil {
		if len(cmdline) > 60 {
			cmdline = cmdline[:60]
		}
		cmdline = bytes.TrimSpace(cmdline)
		info.Cmd = string(cmdline)
	}

	if strings.Contains(info.Exe, "android-studio") {
		info.Pkgs = append(info.Pkgs, "android-studio")
		return info
	}

	for _, pkg := range m[info.Exe] {
		info.Pkgs = append(info.Pkgs, pkg)
	}

	// A file in /usr may be registered without /usr due to usrmerge, so look
	// there as well.
	if len(info.Pkgs) == 0 && strings.HasPrefix(info.Exe, "/usr") {
		for _, pkg := range m[info.Exe[4:]] {
			info.Pkgs = append(info.Pkgs, pkg)
		}
	}

	if len(info.Pkgs) == 0 {
		info.Pkgs = append(info.Pkgs, "<other>")
	}

	// Split the Pss over the packages that share the file
	info.Pss /= uint64(len(info.Pkgs))
	return info
}

func main() {
	var cpuprof = flag.String("cpu", "", "Path to store CPU profile")
	var memprof = flag.String("mem", "", "Path to store memory profile")
	var verbose = flag.Bool("v", false, "Show per process memory use")
	flag.Parse()

	if cpuprof != nil && *cpuprof != "" {
		f, err := os.Create(*cpuprof)
		if err != nil {
			log.Fatal(err)
		}
		pprof.StartCPUProfile(f)
		defer pprof.StopCPUProfile()
	}

	packageMap := NewFileToPackageMap()
	/*for file, pkgs := range fileToPkg {
		fmt.Printf("%s => %v\n", file, pkgs)
	}*/
	var procs = make(chan ProcInfo)
	packageToInfo := make(map[string][]ProcInfo)
	var count int
	files, _ := ioutil.ReadDir("/proc")
	for _, f := range files {
		if _, err := strconv.ParseUint(f.Name(), 10, 64); err != nil {
			continue
		}

		count++
		go func(pid string) {
			procs <- packageMap.NewProcInfo(pid)
		}(f.Name())
	}

	for i := 0; i < count; i++ {
		res := <-procs
		if res.Pss != 0 {
			for _, pkg := range res.Pkgs {
				packageToInfo[pkg] = append(packageToInfo[pkg], res)
			}
		}
	}

	var pkgInfos PackageInfoSlice
	for pkg, infoSet := range packageToInfo {
		sum := uint64(0)
		infos := make(ProcInfoSlice, 0, len(infoSet))
		for _, in := range infoSet {
			infos = append(infos, in)
		}
		sort.Sort(infos)
		for _, in := range infos {
			sum += in.Pss
		}

		pkgInfos = append(pkgInfos, PackageInfo{pkg: pkg, pss: sum, procs: infos})

	}
	sort.Sort(pkgInfos)

	w := tabwriter.NewWriter(os.Stdout, 12, 8, 4, ' ', 0)
	var total uint64
	fmt.Fprintf(w, "%s\t%s\t\n", "Package/Proc", "Memory (PSS)")
	fmt.Fprintf(w, "%s\t%s\t\n", "------------", "------------")
	for _, pkgInfo := range pkgInfos {
		total += pkgInfo.pss
		fmt.Fprintf(w, "%s\t%v\t\n", pkgInfo.pkg, humanize.Bytes(pkgInfo.pss))

		if *verbose {
			for _, in := range pkgInfo.procs {
				fmt.Fprintf(w, "- [%v] %s\t%v\t\n", in.Pid, in.Cmd, humanize.Bytes(in.Pss))
			}
		}

	}
	fmt.Fprintf(w, "%s\t%s\t\n", "------------", "------------")
	fmt.Fprintf(w, "total\t%v\t\n", humanize.Bytes(total))
	w.Flush()

	if memprof != nil && *memprof != "" {
		f, err := os.Create(*memprof)
		if err != nil {
			log.Fatal(err)
		}
		pprof.WriteHeapProfile(f)
	}
}
