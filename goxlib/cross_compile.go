package goxlib

import (
	"flag"
	"fmt"
	"os"
	"os/exec"
	"runtime"
	"sync"
	"errors"

)

var (
 	buildToolchain bool
 	ldflags string
 	outputTpl string
 	parallel int
 	platformFlag PlatformFlag
 	tags string
 	verbose bool
 	flagGcflags string
 	flagCgo, flagRebuild, flagListOSArch bool
 	flagGoCmd string
	flags = flag.NewFlagSet("gox", flag.ExitOnError)
)

// errors
var (
	ErrNoValidPlatforms = errors.New(`"No valid platforms to build for. If you specified a value
		for the 'os', 'arch', or 'osarch' flags, make sure you're
		using a valid value.`)
)


func init(){

	flags.Usage = func() { PrintUsage() }
	flags.Var(platformFlag.ArchFlagValue(), "arch", "arch to build for or skip")
	flags.Var(platformFlag.OSArchFlagValue(), "osarch", "os/arch pairs to build for or skip")
	flags.Var(platformFlag.OSFlagValue(), "os", "os to build for or skip")
	flags.StringVar(&ldflags, "ldflags", "", "linker flags")
	flags.StringVar(&tags, "tags", "", "go build tags")
	flags.StringVar(&outputTpl, "output", "{{.Dir}}_{{.OS}}_{{.Arch}}", "output path")
	flags.IntVar(&parallel, "parallel", -1, "parallelization factor")
	flags.BoolVar(&buildToolchain, "build-toolchain", false, "build toolchain")
	flags.BoolVar(&verbose, "verbose", false, "verbose")
	flags.BoolVar(&flagCgo, "cgo", false, "")
	flags.BoolVar(&flagRebuild, "rebuild", false, "")
	flags.BoolVar(&flagListOSArch, "osarch-list", false, "")
	flags.StringVar(&flagGcflags, "gcflags", "", "")
	flags.StringVar(&flagGoCmd, "gocmd", "go", "")
}

func CrossCompile() ([]string, error) {

	if err := flags.Parse(os.Args[1:]); err != nil {
		return nil, err
	}

	// Determine what amount of parallelism we want Default to the current
	// number of CPUs-1 is <= 0 is specified.
	if parallel <= 0 {
		cpus := runtime.NumCPU()
		if cpus < 2 {
			parallel = 1
		} else {
			parallel = cpus - 1
		}

		// Joyent containers report 48 cores via runtime.NumCPU(), and a
		// default of 47 parallel builds causes a panic. Default to 3 on
		// Solaris-derived operating systems unless overridden with the
		// -parallel flag.
		if runtime.GOOS == "solaris" {
			parallel = 3
		}
	}

	if buildToolchain {
		return nil, mainBuildToolchain(parallel, platformFlag, verbose)
	}

	if _, err := exec.LookPath(flagGoCmd); err != nil {
		return nil, fmt.Errorf("%s executable must be on the PATH\n", flagGoCmd)
	}

	version, err := GoVersion()
	if err != nil {
		return nil, fmt.Errorf("error reading Go version: %s", err)
	}

	if flagListOSArch {
		return nil, mainListOSArch(version)
	}

	// Determine the packages that we want to compile. Default to the
	// current directory if none are specified.
	packages := flags.Args()
	if len(packages) == 0 {
		packages = []string{"."}
	}

	// Get the packages that are in the given paths
	mainDirs, err := GoMainDirs(packages, flagGoCmd)
	if err != nil {
		return nil, fmt.Errorf("Error reading packages: %s", err.Error())
	}

	// Determine the platforms we're building for
	platforms := platformFlag.Platforms(SupportedPlatforms(version))
	if len(platforms) == 0 {
		return nil, ErrNoValidPlatforms
	}

	// Build in parallel!
	fmt.Printf("Number of parallel builds: %d\n\n", parallel)
	var errorLock sync.Mutex
	var wg sync.WaitGroup
	errors := make([]string, 0)
	semaphore := make(chan int, parallel)
	binPaths := make([]string, 0)
	var mu sync.Mutex
	for _, platform := range platforms {
		for _, path := range mainDirs {
			// Start the goroutine that will do the actual build
			wg.Add(1)
			go func(path string, platform Platform) {
				defer wg.Done()
				semaphore <- 1
				fmt.Printf("--> %15s: %s\n", platform.String(), path)

				opts := &CompileOpts{
					PackagePath: path,
					Platform:    platform,
					OutputTpl:   outputTpl,
					Ldflags:     ldflags,
					Tags:        tags,
					Cgo:         flagCgo,
					Rebuild:     flagRebuild,
					GoCmd:       flagGoCmd,
				}

				// Determine if we have specific CFLAGS or LDFLAGS for this
				// GOOS/GOARCH combo and override the defaults if so.
				envOverride(&opts.Ldflags, platform, "LDFLAGS")
				envOverride(&opts.Gcflags, platform, "GCFLAGS")
				var binName string
				var err error
				if binName, err = GoCrossCompile(opts); err != nil {
					errorLock.Lock()
					defer errorLock.Unlock()
					errors = append(errors,
						fmt.Sprintf("%s error: %s", platform.String(), err))
				}
				mu.Lock()
				binPaths = append(binPaths, path + string(os.PathSeparator) + binName)
				mu.Unlock()
				<-semaphore
			}(path, platform)
		}
	}
	wg.Wait()

	if len(errors) > 0 {
		fmt.Fprintf(os.Stderr, "\n%d errors occurred:\n", len(errors))
		for _, err := range errors {
			fmt.Fprintf(os.Stderr, "--> %s\n", err)
		}
		return nil, err
	}

	return binPaths, nil
}

func PrintUsage() {
	fmt.Fprint(os.Stderr, helpText)
}

const helpText = `Usage: goxlib [options] [packages]

  Gox cross-compiles Go applications in parallel.

  If no specific operating systems or architectures are specified, Gox
  will build for all pairs supported by your version of Go.

Options:

  -arch=""            Space-separated list of architectures to build for
  -build-toolchain    Build cross-compilation toolchain
  -cgo                Sets CGO_ENABLED=1, requires proper C toolchain (advanced)
  -gcflags=""         Additional '-gcflags' value to pass to go build
  -ldflags=""         Additional '-ldflags' value to pass to go build
  -tags=""            Additional '-tags' value to pass to go build
  -os=""              Space-separated list of operating systems to build for
  -osarch=""          Space-separated list of os/arch pairs to build for
  -osarch-list        List supported os/arch pairs for your Go version
  -output="foo"       Output path template. See below for more info
  -parallel=-1        Amount of parallelism, defaults to number of CPUs
  -gocmd="go"         Build command, defaults to Go
  -rebuild            Force rebuilding of package that were up to date
  -verbose            Verbose mode

Output path template:

  The output path for the compiled binaries is specified with the
  "-output" flag. The value is a string that is a Go text template.
  The default value is "{{.Dir}}_{{.OS}}_{{.Arch}}". The variables and
  their values should be self-explanatory.

Platforms (OS/Arch):

  The operating systems and architectures to cross-compile for may be
  specified with the "-arch" and "-os" flags. These are space separated lists
  of valid GOOS/GOARCH values to build for, respectively. You may prefix an
  OS or Arch with "!" to negate and not build for that platform. If the list
  is made up of only negations, then the negations will come from the default
  list.

  Additionally, the "-osarch" flag may be used to specify complete os/arch
  pairs that should be built or ignored. The syntax for this is what you would
  expect: "darwin/amd64" would be a valid osarch value. Multiple can be space
  separated. An os/arch pair can begin with "!" to not build for that platform.

  The "-osarch" flag has the highest precedent when determing whether to
  build for a platform. If it is included in the "-osarch" list, it will be
  built even if the specific os and arch is negated in "-os" and "-arch",
  respectively.

Platform Overrides:

  The "-gcflags" and "-ldflags" options can be overridden per-platform
  by using environment variables. Gox will look for environment variables
  in the following format and use those to override values if they exist:

    GOX_[OS]_[ARCH]_GCFLAGS
    GOX_[OS]_[ARCH]_LDFLAGS

`
