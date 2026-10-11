package cli

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"os/signal"
	"syscall"
	"time"

	"github.com/spf13/cobra"

	"github.com/rhuss/sandcast/internal/engine"
	"github.com/rhuss/sandcast/internal/fsutil"
	"github.com/rhuss/sandcast/internal/manifest"
	"github.com/rhuss/sandcast/internal/mise"
)

// App holds the I/O streams and the external dependencies of every command.
// Tests replace the dependencies with fakes.
type App struct {
	Stdout io.Writer
	Stderr io.Writer
	Stdin  io.Reader

	// NewEngine returns the engine for a name (podman or docker).
	NewEngine func(name string) engine.Engine
	// LookPath finds executables (engine detection, host tools).
	LookPath engine.LookPath
	// HTTPClient fetches the remote component registry.
	HTTPClient *http.Client
	// MiseRunner runs the host mise.
	MiseRunner mise.Runner
	// Gateway runs the openshell CLI.
	Gateway GatewayRunner
	// HostPlatform returns the default target platform (linux/<arch>).
	HostPlatform func() string
	// Getenv reads environment variables.
	Getenv func(string) string
	// HostVersion returns the first line of "<bin> --version" for host tool
	// detection; nil runs the binary (capture's default).
	HostVersion func(ctx context.Context, bin string) string

	opts globalOptions
}

type globalOptions struct {
	project string
	engine  string
	verbose bool
	json    bool
}

// NewApp returns an App wired to the real system.
func NewApp(stdout, stderr io.Writer, stdin io.Reader) *App {
	a := &App{
		Stdout:       stdout,
		Stderr:       stderr,
		Stdin:        stdin,
		LookPath:     exec.LookPath,
		HTTPClient:   &http.Client{Timeout: RegistryTimeout},
		MiseRunner:   mise.ExecRunner{},
		Gateway:      execGateway{},
		HostPlatform: mise.HostTarget,
		Getenv:       os.Getenv,
	}
	a.NewEngine = func(name string) engine.Engine {
		e := engine.NewExec(name)
		e.Trace = a.debugf
		e.Notice = func(format string, args ...any) { a.logf("notice: "+format, args...) }
		return e
	}
	return a
}

// RegistryTimeout bounds each component registry request, so a stalled
// server or proxy cannot hang components refresh.
const RegistryTimeout = 60 * time.Second

// ExitInterrupted is the exit code after SIGINT or SIGTERM.
const ExitInterrupted = 130

// Execute runs the command line and returns the process exit code. SIGINT
// and SIGTERM cancel the command's context, so child processes are stopped
// and deferred cleanup runs.
func Execute(args []string) int {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	// After the first signal, say so and restore the default handling, so
	// a second Ctrl-C ends the process at once.
	go func() {
		<-ctx.Done()
		stop()
		// The deferred stop() also cancels ctx, with no cause; only a
		// signal is reported.
		if context.Cause(ctx) == context.Canceled {
			return
		}
		_, _ = fmt.Fprintln(os.Stderr, "sandcast: interrupt received, stopping (press Ctrl-C again to exit at once)")
	}()
	app := NewApp(os.Stdout, os.Stderr, os.Stdin)
	return app.Run(ctx, args)
}

// readLine reads one line from Stdin, or returns the context error when the
// context is cancelled first (a prompt must not swallow Ctrl-C).
func (a *App) readLine(ctx context.Context) (string, error) {
	type line struct {
		text string
		err  error
	}
	ch := make(chan line, 1)
	go func() {
		text, err := bufio.NewReader(a.Stdin).ReadString('\n')
		ch <- line{text, err}
	}()
	select {
	case <-ctx.Done():
		return "", ctx.Err()
	case l := <-ch:
		if l.err != nil && l.text == "" && !errors.Is(l.err, io.EOF) {
			return "", l.err
		}
		return l.text, nil
	}
}

// Run executes args and returns the exit code, printing errors to Stderr.
func (a *App) Run(ctx context.Context, args []string) int {
	root := a.RootCommand()
	root.SetArgs(args)
	err := root.ExecuteContext(ctx)
	if ctx.Err() != nil {
		_, _ = fmt.Fprintln(a.Stderr, "sandcast: interrupted")
		return ExitInterrupted
	}
	if err != nil {
		var se silentErr
		if !errors.As(err, &se) {
			_, _ = fmt.Fprintf(a.Stderr, "sandcast: %v\n", err)
		}
	}
	return CodeOf(err)
}

// RootCommand builds the command tree.
func (a *App) RootCommand() *cobra.Command {
	root := &cobra.Command{
		Use:   "sandcast",
		Short: "Turn a project directory into a sandbox image",
		Long: `sandcast turns a project directory into a sandbox image.

A capture (the sandcast skills in an agent harness) writes .sandcast/manifest.yaml.
plan, build and verify turn the manifest plus a probed base image into a
container image and OpenShell provider profiles adapted to that image.`,
		SilenceUsage:  true,
		SilenceErrors: true,
	}
	root.SetOut(a.Stdout)
	root.SetErr(a.Stderr)
	root.SetIn(a.Stdin)
	root.SetFlagErrorFunc(func(_ *cobra.Command, err error) error {
		return &ExitError{Code: ExitUsage, Err: err}
	})
	pf := root.PersistentFlags()
	pf.StringVar(&a.opts.project, "project", "", "project directory (default: nearest ancestor with .sandcast/, else the current directory)")
	pf.StringVar(&a.opts.engine, "engine", "", "container engine: podman or docker")
	pf.BoolVar(&a.opts.verbose, "verbose", false, "print progress details to stderr")
	pf.BoolVar(&a.opts.json, "json", false, "print one JSON document to stdout (commands that support it)")

	for _, c := range a.commands() {
		root.AddCommand(c)
	}
	return root
}

func (a *App) commands() []*cobra.Command {
	return []*cobra.Command{
		a.versionCommand(),
		a.initCommand(),
		a.manifestCommand(),
		a.baseImagesCommand(),
		a.componentsCommand(),
		a.lockCommand(),
		a.probeCommand(),
		a.planCommand(),
		a.buildCommand(),
		a.verifyCommand(),
		a.profilesCommand(),
		a.harnessCommand(),
		a.skillsCommand(),
		a.fragmentsCommand(),
		a.promoteCommand(),
		a.learningsCommand(),
	}
}

// args wraps a cobra positional-argument validator so violations exit 2.
func args(v cobra.PositionalArgs) cobra.PositionalArgs {
	return func(cmd *cobra.Command, a []string) error {
		if err := v(cmd, a); err != nil {
			return &ExitError{Code: ExitUsage, Err: err}
		}
		return nil
	}
}

// logf prints a diagnostic line to stderr.
func (a *App) logf(format string, args ...any) {
	_, _ = fmt.Fprintf(a.Stderr, format+"\n", args...)
}

// debugf prints only with --verbose.
func (a *App) debugf(format string, args ...any) {
	if a.opts.verbose {
		a.logf(format, args...)
	}
}

// layout resolves the project layout from --project.
func (a *App) layout() (fsutil.Layout, error) {
	return fsutil.NewLayout(a.opts.project)
}

// loadManifest resolves the layout and loads the manifest.
func (a *App) loadManifest() (*manifest.Manifest, fsutil.Layout, error) {
	l, err := a.layout()
	if err != nil {
		return nil, l, err
	}
	m, err := manifest.Load(l.ManifestPath())
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, l, withCode(ExitUsage, err)
		}
		return nil, l, err
	}
	return m, l, nil
}

// selectEngine applies the engine precedence (FR-005) and prints the
// docker fallback notice.
func (a *App) selectEngine(manifestEngine string) (engine.Engine, engine.Selection, error) {
	sel, err := engine.Detect(a.opts.engine, manifestEngine, a.LookPath)
	if err != nil {
		if errors.Is(err, engine.ErrUnsupportedEngine) {
			return nil, sel, withCode(ExitUsage, err)
		}
		return nil, sel, toolErr(err)
	}
	if sel.Notice != "" {
		a.logf("notice: %s", sel.Notice)
	}
	return a.NewEngine(sel.Name), sel, nil
}
