package main

import (
	"bufio"
	"fmt"
	"io/ioutil"
	"log"
	"os"
	"path/filepath"
	"regexp"
	"runtime/pprof"
	"sort"
	"strconv"
	"strings"
	"text/tabwriter"

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
	reg, _ := regexp.Compile("\\s+")
	file, err := os.Open(filepath.Join("/proc", pid, "smaps"))
	if err != nil {
		fmt.Println("Error in", pid, err)
		return 0
	}
	defer file.Close()
	reader := bufio.NewReader(file)
	var sum uint64
	for {
		line, err := reader.ReadString('\n')
		if err != nil {
			break
		}
		if !strings.HasPrefix(line, "Pss:") {
			continue
		}

		fields := reg.Split(line, -1)

		i, _ := strconv.ParseUint(fields[1], 10, 64)
		sum += i * 1024
	}
	return sum
}

func process(list string) []FilePackageTuple {
	result := make([]FilePackageTuple, 0, 16)
	file, err := os.Open(list)
	if err != nil {

	}
	defer file.Close()
	reader := bufio.NewReader(file)
	for {
		line, err := reader.ReadString('\n')
		if err != nil {
			break
		}
		line = strings.TrimSpace(line)
		pkg := filepath.Base(list)

		result = append(result, FilePackageTuple{File: line, Package: pkg})
		// Add support for usrmerge
		if !strings.HasPrefix(line, "/usr") {
			result = append(result, FilePackageTuple{File: "/usr" + line, Package: pkg})
		}
	}
	return result
}

type PackageMap map[string][]string

// NewFileToPackageMap reads a map file -> [pkg] from dpkg
func NewFileToPackageMap() PackageMap {
	match, err := filepath.Glob("/var/lib/dpkg/info/*.list")
	if err != nil {
		log.Fatalf("%s", err)
	}

	fileToPkg := make(map[string][]string, 1024*16)

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
	// Workers
	for i := 0; i < 4; i++ {
		go func() {
			for item := range work {
				out <- process(item)
			}
		}()
	}

	// Inform workers
	for _, list := range match {
		work <- list
	}
	close(work)
	// Wait for fileToPkg to be filled.
	<-done

	return fileToPkg
}

type ProcInfo struct {
	Exe  string
	Pid  string
	Pss  uint64
	Pkgs []string
}

// ProcInfoSlice is a sortable slice of ProcInfo pointers
type ProcInfoSlice []*ProcInfo

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
func (m PackageMap) NewProcInfo(pid string) *ProcInfo {
	info := ProcInfo{Pss: MemUsage(pid), Pid: pid}
	info.Exe = filepath.Join("/proc", pid, "exe")

	if info.Exe, _ = os.Readlink(info.Exe); info.Exe != "" {
	}

	if strings.Contains(info.Exe, "android-studio") {
		info.Pkgs = append(info.Pkgs, "android-studio")
		return &info
	}
	pkgs := make([]string, 0, 8)
	for _, pkg := range m[info.Exe] {
		pkgs = append(pkgs, pkg)
	}
	for _, pkg := range m["/usr"+info.Exe] {
		pkgs = append(pkgs, pkg)
	}

	for _, pkg := range pkgs {
		if pkg != "" {
			info.Pkgs = append(info.Pkgs, pkg)
		}
	}
	if len(info.Pkgs) == 0 {
		info.Pkgs = append(info.Pkgs, "<other>")
	}

	return &info
}

func main() {

	f, err := os.Create("cpu")
	if err != nil {
		log.Fatal(err)
	}
	pprof.StartCPUProfile(f)
	defer pprof.StopCPUProfile()

	packageMap := NewFileToPackageMap()
	/*for file, pkgs := range fileToPkg {
		fmt.Printf("%s => %v\n", file, pkgs)
	}*/
	var procs = make(chan *ProcInfo)
	packageToInfo := make(map[string]map[string]*ProcInfo)
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
		if res != nil && res.Pss != 0 {
			set := make(map[string]bool)
			for _, pkg := range res.Pkgs {
				// Why can we have the same pid multiple times?
				if packageToInfo[pkg] == nil {
					packageToInfo[pkg] = make(map[string]*ProcInfo)

				}
				set[pkg] = true
				packageToInfo[pkg][res.Pid] = res
			}
			// Split the Pss over the packages that share the file...
			res.Pss /= uint64(len(set))
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
	for _, pkgInfo := range pkgInfos {
		total += pkgInfo.pss
		fmt.Fprintf(w, "%s\t%v\t\n", pkgInfo.pkg, humanize.Bytes(pkgInfo.pss))

		/*for _, in := range pkgInfo.procs {
			fmt.Fprintf(w, "\t-[%v]%s\t%v\t\n", in.Pid, in.Exe, humanize.Bytes(in.Pss))
		}*/

	}
	fmt.Fprintf(w, "total\t%v\t\n", humanize.Bytes(total))
	w.Flush()

	print(MemUsage("self"))

}