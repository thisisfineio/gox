package goxlib

import (
	"bytes"
	"fmt"
	"github.com/mitchellh/iochan"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sync"
)

// The "main" method for when the toolchain build is requested.
func mainBuildToolchain(parallel int, platformFlag PlatformFlag, verbose bool) error {
	if _, err := exec.LookPath("go"); err != nil {
		fmt.Fprint(os.Stderr, "You must have Go already built for your native platform\n")
		fmt.Fprint(os.Stderr, "and the `go` binary on the PATH to build toolchains.\n")
		return err
	}

	// If we're version 1.5 or greater, then we don't need to do this anymore!
	versionParts, err := GoVersionParts()
	if err != nil {
		fmt.Fprintf(os.Stderr, "error reading Go version: %s", err)
		return err
	}
	if versionParts[0] >= 1 && versionParts[1] >= 5 {
		fmt.Fprint(
			os.Stderr,
			"-build-toolchain is no longer required for Go 1.5 or later.\n"+
				"You can start using Gox immediately!\n")
		return err
	}

	version, err := GoVersion()
	if err != nil {
		fmt.Fprintf(os.Stderr, "error reading Go version: %s", err)
		return err
	}

	root, err := GoRoot()
	if err != nil {
		fmt.Fprintf(os.Stderr, "error finding GOROOT: %s\n", err)
		return err
	}

	if verbose {
		fmt.Println("Verbose mode enabled. Output from building each toolchain will be")
		fmt.Println("outputted to stdout as they are built.\n")
	}

	// Determine the platforms we're building the toolchain for.
	platforms := platformFlag.Platforms(SupportedPlatforms(version))

	// The toolchain build can't be parallelized.
	if parallel > 1 {
		fmt.Println("The toolchain build can't be parallelized because compiling a single")
		fmt.Println("Go source directory can only be done for one platform at a time. Therefore,")
		fmt.Println("the toolchain for each platform will be built one at a time.\n")
	}
	parallel = 1

	var errorLock sync.Mutex
	var wg sync.WaitGroup
	errs := make([]error, 0)
	semaphore := make(chan int, parallel)
	for _, platform := range platforms {
		wg.Add(1)
		go func(platform Platform) {
			err := BuildToolchain(&wg, semaphore, root, platform, verbose)
			if err != nil {
				errorLock.Lock()
				defer errorLock.Unlock()
				errs = append(errs, fmt.Errorf("%s: %s", platform.String(), err))
			}
		}(platform)
	}
	wg.Wait()

	if len(errs) > 0 {
		fmt.Fprintf(os.Stderr, "\n%d errors occurred:\n", len(errs))
		for _, err := range errs {
			fmt.Fprintf(os.Stderr, "%s\n", err)
		}
		return err
	}

	return nil
}

func BuildToolchain(wg *sync.WaitGroup, semaphore chan int, root string, platform Platform, verbose bool) error {
	defer wg.Done()
	semaphore <- 1
	defer func() { <-semaphore }()
	fmt.Printf("--> Toolchain: %s\n", platform.String())

	scriptName := "make.bash"
	if runtime.GOOS == "windows" {
		scriptName = "make.bat"
	}

	var stderr bytes.Buffer
	var stdout bytes.Buffer
	scriptDir := filepath.Join(root, "src")
	scriptPath := filepath.Join(scriptDir, scriptName)
	cmd := exec.Command(scriptPath, "--no-clean")
	cmd.Dir = scriptDir
	cmd.Env = append(os.Environ(),
		"GOARCH="+platform.Arch,
		"GOOS="+platform.OS)
	cmd.Stderr = &stderr
	cmd.Stdout = &stdout

	if verbose {
		// In verbose mode, we output all stdout to the console.
		r, w := io.Pipe()
		cmd.Stdout = w
		cmd.Stderr = io.MultiWriter(cmd.Stderr, w)

		// Send all the output to stdout, and also make a done channel
		// so that this compilation isn't done until we receive all output
		doneCh := make(chan struct{})
		go func() {
			defer close(doneCh)
			for line := range iochan.DelimReader(r, '\n') {
				fmt.Printf("%s: %s", platform.String(), line)
			}
		}()
		defer func() {
			w.Close()
			<-doneCh
		}()
	}

	if err := cmd.Start(); err != nil {
		return fmt.Errorf("Error building '%s': %s", platform.String(), err)
	}

	if err := cmd.Wait(); err != nil {
		return fmt.Errorf("Error building '%s'.\n\nStdout: %s\n\nStderr: %s\n",
			platform.String(), stdout.String(), stderr.String())
	}

	return nil
}
