package main

import (
	"bufio"
	"fmt"
	"io"
	"io/ioutil"
	"net/http"
	"os"
	"os/exec"
	"path"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"unicode"
)

var concurrent = 5
var cacheDir = "/tmp/pkg_zone"

type Package struct {
	String  string
	Name    string
	Version *PackageVersion
	Flavour string
}

func main() {
	indx := packageIndexUrl()
	fmt.Println(indx)
	rpkgs := fetchPackageIndex(packageIndexUrl())
	fmt.Println(len(rpkgs), "remote packages")
	ipkgs := installedPackages()
	fmt.Println(len(ipkgs), "installed packages")
	upgrd := upgradablePackages(ipkgs, rpkgs)
	if upgradePrompt(ipkgs, upgrd) {
		downloadPackages(indx, cacheDir, concurrent, upgrd)
	}
}

func getMirror() string {
	url, err := ioutil.ReadFile("/etc/installurl")
	if err != nil {
		fmt.Println(err)
		os.Exit(1)
	}
	return strings.TrimSpace(string(url))
}

func getRelease() string {
	rel, err := exec.Command("uname", "-r").Output()
	if err != nil {
		fmt.Println("Failed to get OpenBSD release:", err)
		os.Exit(1)
	}
	return strings.TrimSpace(string(rel))
}

func getArch() string {
	arch, err := exec.Command("uname", "-p").Output()
	if err != nil {
		fmt.Println("Failed to get processor architecture:", err)
		os.Exit(1)
	}
	return strings.TrimSpace(string(arch))
}

func packageIndexUrl() string {
	mirror := getMirror()
	release := getRelease()
	arch := getArch()
	return fmt.Sprintf("%s/%s/packages/%s", mirror, release, arch)
}

func fetchPackageIndex(url string) []*Package {
	fmt.Println("Fetching package index")
	resp, err := http.Get(url)
	if err != nil {
		fmt.Println("Failed to fetch package index:", err)
		os.Exit(1)
	}
	defer resp.Body.Close()
	body, err := ioutil.ReadAll(resp.Body)
	if err != nil {
		fmt.Println("Failed to read package index:", err)
		os.Exit(1)
	}

	regex := regexp.MustCompile(`['"](.*?)\.tgz['"]`)
	pkgStrs := regex.FindAllStringSubmatch(string(body), -1)

	var pkgs []*Package
	for _, pkgStr := range pkgStrs {
		pkg := stringToPackage(pkgStr[1])
		pkgs = append(pkgs, pkg)
	}
	return pkgs
}

func stringToPackage(str string) *Package {
	fields := strings.Split(str, "-")
	var verIdx int
	for idx, field := range fields {
		if unicode.IsDigit(rune(field[0])) {
			verIdx = idx
			break
		}
	}

	name := strings.Join(fields[:verIdx], "-")
	version := packageVersion(fields[verIdx])
	flavour := ""
	if len(fields)-1 > verIdx {
		flavour = strings.Join(fields[verIdx+1:], "-")
	}

	return &Package{
		String:  str,
		Name:    name,
		Version: version,
		Flavour: flavour,
	}
}

func installedPackages() []*Package {
	out, err := exec.Command("pkg_info").Output()
	if err != nil {
		fmt.Println("Failed to get installed package list:", err)
		os.Exit(1)
	}
	pkgLines := strings.Split(string(out), "\n")
	var pkgs []*Package
	for _, line := range pkgLines {
		fields := strings.Split(line, " ")
		if len(fields) == 1 {
			break
		}
		pkgs = append(pkgs, stringToPackage(fields[0]))
	}
	return pkgs
}

var suffixes = []string{"alpha", "beta", "rc", "pre", "", "pl"}

func newerPackage(installed, remote *Package) bool {
	// https://man.openbsd.org/man7/packages-specs.7

	// New Scheme number
	if remote.Version.Scheme > installed.Version.Scheme {
		return true
	}

	// New Version number
	iSplit := installed.Version.Version
	rSplit := remote.Version.Version

	for idx, iPart := range iSplit {
		iNum, iLetter := versionLetterSplit(iPart)

		rPart := rSplit[idx]
		rNum, rLetter := versionLetterSplit(rPart)

		if rNum > iNum {
			return true
		}

		if (rNum == iNum) && (rLetter > iLetter) {
			return true
		}
	}

	// New Suffix type
	var iSuffixPos, rSuffixPos int

	for idx, suffix := range suffixes {
		if suffix == installed.Version.Suffix {
			iSuffixPos = idx
		}
		if suffix == remote.Version.Suffix {
			rSuffixPos = idx
		}
	}

	if rSuffixPos > iSuffixPos {
		return true
	}

	// New version for same Suffix type
	rSuffixVersion := remote.Version.SuffixVersion
	iSuffixVersion := installed.Version.SuffixVersion
	if (rSuffixPos == iSuffixPos) && (rSuffixVersion > iSuffixVersion) {
		return true
	}

	// New Revision
	rRev := remote.Version.Revision
	iRev := installed.Version.Revision
	if rRev > iRev {
		return true
	}

	// Same or lower version
	return false
}

var leadingNumbers = regexp.MustCompile(`\d+`)
var trailingLetters = regexp.MustCompile(`\w*`)

func versionLetterSplit(version string) (int, string) {
	numStr := leadingNumbers.FindString(version)
	letters := trailingLetters.FindString(version)

	num, err := strconv.Atoi(numStr)
	if err != nil {
		panic(fmt.Sprintf("Failed to convert version number: %s", err))
	}

	return num, letters
}

type PackageVersion struct {
	String        string
	Version       []string
	Revision      int
	Suffix        string
	SuffixVersion int
	Scheme        int
}

var packageSuffix = regexp.MustCompile(`(rc|alpha|beta|pre|pl)(\d*)`)
var packageRevision = regexp.MustCompile(`p(\d+)`)
var packageScheme = regexp.MustCompile(`[vV](\d+)`)

func packageVersion(ver string) *PackageVersion {
	fields := strings.Split(ver, ".")
	lastIdx := len(fields) - 1
	last := fields[lastIdx]
	pkgVer := &PackageVersion{
		String:   ver,
		Scheme:   -1,
		Revision: -1,
	}
	pkgVer.Version = fields[:lastIdx]

	if suffix := packageSuffix.FindStringSubmatch(last); len(suffix) != 0 {
		pkgVer.Suffix = suffix[1]
		if suffix[2] != "" {
			n, err := strconv.Atoi(suffix[2])
			if err != nil {
				fmt.Println(suffix[0])
				panic(fmt.Sprintf("Failed to convert package suffix version: %s", err))
			}
			pkgVer.SuffixVersion = n
		}
		last = packageSuffix.ReplaceAllString(last, "")
	}

	if rev := packageRevision.FindStringSubmatch(last); len(rev) != 0 {
		n, err := strconv.Atoi(rev[1])
		if err != nil {
			panic(fmt.Sprintf("Failed to convert package revision: %s", err))
		}
		pkgVer.Revision = n
		last = packageRevision.ReplaceAllString(last, "")
	}

	if scheme := packageScheme.FindStringSubmatch(last); len(scheme) != 0 {
		n, err := strconv.Atoi(scheme[1])
		if err != nil {
			panic(fmt.Sprintf("Failed to convert package scheme: %s", err))
		}
		pkgVer.Scheme = n
		last = packageScheme.ReplaceAllString(last, "")
	}

	pkgVer.Version = append(pkgVer.Version, last)

	return pkgVer
}

func packageMapKey(pkg *Package) string {
	return fmt.Sprintf("%s--%s", pkg.Name, pkg.Flavour)
}

func upgradablePackages(installed, remote []*Package) []*Package {
	rMap := make(map[string]*Package)
	for _, pkg := range remote {
		rMap[packageMapKey(pkg)] = pkg
	}

	var upgradable []*Package
	for _, iPkg := range installed {
		rPkg := rMap[packageMapKey(iPkg)]
		if newerPackage(iPkg, rPkg) {
			upgradable = append(upgradable, rPkg)
		}
	}
	return upgradable
}

var bold = "\u001b[1m"
var reset = "\u001b[0m"

func upgradePrompt(installed, upgradable []*Package) bool {
	s := ""
	if len(upgradable) != 1 {
		s = "s"
	}
	fmt.Println()
	upgradableMessage := fmt.Sprintf("%d package%s will upgraded, proceed?", len(upgradable), s)
	fancyLines := strings.Repeat("=", len(upgradableMessage))
	fmt.Println(upgradableMessage)
	fmt.Printf("%s\n\n", fancyLines)
	iMap := make(map[string]*Package)
	for _, pkg := range installed {
		iMap[packageMapKey(pkg)] = pkg
	}

	longestName := 0
	for _, pkg := range upgradable {
		if len(pkg.Name) > longestName {
			longestName = len(pkg.Name)
		}
	}

	for _, uPkg := range upgradable {
		iPkg := iMap[packageMapKey(uPkg)]
		name := iPkg.Name
		space := longestName - len(iPkg.Name) + 2
		spaces := strings.Repeat(" ", space)
		iVer := iPkg.Version.String
		uVer := uPkg.Version.String
		fmt.Printf("%s%s%s%s%s -> %s\n", bold, name, reset, spaces, iVer, uVer)
	}

	fmt.Print("\nContinue? [Y/n]: ")
	reader := bufio.NewReader(os.Stdin)
	resp, _ := reader.ReadString('\n')
	if resp[0] == 'n' || resp[0] == 'N' {
		return false
	}
	return true
}

func downloadPackages(pkgPath, cache string, workers int, upgrades []*Package) {
	var wg sync.WaitGroup

	fmt.Println("Boutta do it")
	err := os.MkdirAll(cache, 0700)
	if err != nil {
		fmt.Println("Error making package cache:", err)
		os.Exit(1)
	}

	in := make(chan *Package)

	go func() {
		for _, pkg := range upgrades {
			fmt.Println("Sending", pkg.Name)
			in <- pkg
		}
		close(in)
	}()

	for i := 0; i < workers; i++ {
		wg.Add(1)

		go func() {
			defer wg.Done()
			fmt.Println("Opening worker")

			for pkg := range in {
				downloadPackage(pkgPath, cache, pkg)
			}
			fmt.Println("Closing worker")
		}()
	}

	wg.Wait()
	fmt.Println("All closed!")
}

func downloadPackage(pkgPath, cache string, pkg *Package) {
	withExt := fmt.Sprintf("%s.tgz", pkg.String)
	pkgCache := path.Join(cache, withExt)
	_, err := os.Stat(pkgCache)
	if os.IsExist(err) {
		fmt.Println(pkg.String, "Already downloaded, skipping")
		return
	}

	pkgUrl := fmt.Sprintf("%s/%s", pkgPath, withExt)
	resp, err := http.Get(pkgUrl)
	if err != nil {
		fmt.Printf("Error downloading %s: %s\n", pkg.String, err)
		return
	}
	defer resp.Body.Close()

	fmt.Println("Downloading", pkg.String)

	out, err := os.Create(pkgCache)
	if err != nil {
		fmt.Printf("Error downloading %s: %s\n", pkg.String, err)
		return
	}
	defer out.Close()

	_, err = io.Copy(out, resp.Body)
	if err != nil {
		fmt.Printf("Error downloading %s: %s\n", withExt, err)
		return
	}
	fmt.Println("Finished downloading", pkg.String)
}
