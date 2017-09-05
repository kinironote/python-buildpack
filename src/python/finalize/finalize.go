package finalize

import (
	"fmt"
	"io"
	"io/ioutil"
	"os"
	"path/filepath"
	"strings"

	"github.com/cloudfoundry/libbuildpack"
)

type Manifest interface {
	RootDir() string
}

type Stager interface {
	BuildDir() string
	DepDir() string
	DepsIdx() string
	WriteProfileD(string, string) error
}

type Command interface {
	Execute(string, io.Writer, io.Writer, string, ...string) error
	Output(dir string, program string, args ...string) (string, error)
	// Run(cmd *exec.Cmd) error
}

type ManagePyFinder interface {
	FindManagePy(dir string) (string, error)
}

type Finalizer struct {
	Stager         Stager
	Log            *libbuildpack.Logger
	Logfile        *os.File
	Manifest       Manifest
	Command        Command
	ManagePyFinder ManagePyFinder
	// StartScript string
}

func Run(f *Finalizer) error {

	if err := f.HandleCollectstatic(); err != nil {
		f.Log.Error("Error handling collectstatic: %v", err)
		return err
	}

	if err := f.ReplaceDepsDirWithLiteral(); err != nil {
		f.Log.Error("Error replacing depsDir with literal: %v", err)
		return err
	}

	if err := f.ReplaceLiteralWithDepsDirAtRuntime(); err != nil {
		f.Log.Error("Error replacing literal with depsDir: %v", err)
		return err
	}

	return nil
}

func (f *Finalizer) HandleCollectstatic() error {
	if len(os.Getenv("DISABLE_COLLECTSTATIC")) > 0 {
		return nil
	}
	if err := f.Command.Execute(f.Stager.BuildDir(), os.Stdout, os.Stderr, "pip-grep", "-s", "requirements.txt", "django", "Django"); err != nil {
		return nil
	}

	managePyPath, err := f.ManagePyFinder.FindManagePy(f.Stager.BuildDir())
	if err != nil {
		return err
	}

	f.Log.Info("Running python %s collectstatic --noinput --traceback", managePyPath)
	//TODO: should filter out empty lines or those starting with Post-processed --OR-- Copying
	if err = f.Command.Execute(f.Stager.BuildDir(), os.Stdout, os.Stderr, "python", managePyPath, "collectstatic", "--noinput", "--traceback"); err != nil {
		f.Log.Error(fmt.Sprintf(` !     Error while running '$ python %s collectstatic --noinput'.
       See traceback above for details.

       You may need to update application code to resolve this error.
       Or, you can disable collectstatic for this application:

          $ cf set-env <app> DISABLE_COLLECTSTATIC 1

       https://devcenter.heroku.com/articles/django-assets`, managePyPath))
		//TODO: dump environment variables if $DEBUG_COLLECTSTATIC is set???
		return err
	}

	return nil
}

func (f *Finalizer) ReplaceDepsDirWithLiteral() error {
	dirs, err := filepath.Glob(filepath.Join(f.Stager.DepDir(), "python", "lib", "python*"))
	if err != nil {
		return err
	}

	for _, dir := range dirs {
		if err := filepath.Walk(dir, func(path string, _ os.FileInfo, _ error) error {
			if strings.HasSuffix(path, ".pth") {
				fileContents, err := ioutil.ReadFile(path)
				if err != nil {
					return err
				}

				fileContents = []byte(strings.Replace(string(fileContents), f.Stager.DepDir(), "DOLLAR_DEPS_DIR/"+f.Stager.DepsIdx(), -1))

				if err := ioutil.WriteFile(path, fileContents, 0644); err != nil {
					return err
				}
			}
			return nil
		}); err != nil {
			return err
		}
	}

	return nil
}

func (f *Finalizer) ReplaceLiteralWithDepsDirAtRuntime() error {
	scriptContents := `find $DEPS_DIR/%s/python/lib/python*/  -name "*.pth" -print0 2> /dev/null | xargs -r -0 -n 1 sed -i -e "s#DOLLAR_DEPS_DIR#$DEPS_DIR#" &> /dev/null` + "\n"
	return f.Stager.WriteProfileD("python.fixeggs.sh", fmt.Sprintf(scriptContents, f.Stager.DepsIdx()))
}
