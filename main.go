package main

import (
	"net/http"
	//	"io"
	"fmt"
	"io/ioutil"
	"os"
	"os/exec"
	"regexp"
	"strings"
	"unicode"
	"strconv"
)

type Package struct {
	Name    string
	Version *PackageVersion
	Flavour string
}

func main() {
	fmt.Println("OK!")
	fmt.Println(getMirror())
	fmt.Println(getRelease())
	fmt.Println(getArch())
	fmt.Println(packageIndexUrl())
	rpkgs := fetchPackageIndex(packageIndexUrl())
	fmt.Println(len(rpkgs), "remote packages")
	ipkgs := installedPackages()
	fmt.Println(len(ipkgs), "installed packages")
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

// https://man.openbsd.org/man7/packages-specs.7

// func newerPackage(installed, remote *Package) Bool {
// 	installedVersion := strings.Split(installed.Version, ".")
// 	remoteVersion := strings.Split(remote.Version, ".")
// 	iV := installedVersion[len(installedVersion)-1]
// 	for idx := range installedVersion {
// 		rVer := remoteVersion[idx]

// 	}
// }

type PackageVersion struct {
	String string
	Version []string
	Revision int
	Suffix string
	SuffixVersion int
	Scheme int
}

var packageSuffix = regexp.MustCompile(`(rc|alpha|beta|pre|pl)(\d*)`)
var packageRevision = regexp.MustCompile(`p(\d+)`)
var packageScheme = regexp.MustCompile(`[vV](\d+)`)

func packageVersion (ver string) *PackageVersion {
	fields := strings.Split(ver, ".")
	lastIdx := len(fields)-1
	last := fields[lastIdx]
	pkgVer := &PackageVersion{
		String: ver,
		Scheme: -1,
		Revision: -1,
	}
	pkgVer.Version = fields[:lastIdx]

	if suffix := packageSuffix.FindStringSubmatch(last); len(suffix) != 0 {
		pkgVer.Suffix = suffix[1]
		n, err := strconv.Atoi(suffix[2]);
		if err != nil {
			fmt.Println(ver)
			fmt.Println("Failed to convert package suffix version:", err)
		}
		pkgVer.SuffixVersion = n
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

	return pkgVer
}
