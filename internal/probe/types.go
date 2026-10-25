package probe

import (
	"fmt"
	"slices"
	"strings"

	"github.com/rhuss/sandcast/internal/component"
)

// PackageManagerNone is the package manager name of an image without apt-get,
// dnf, yum or apk.
const PackageManagerNone = "none"

// OS holds the /etc/os-release fields the plan uses.
type OS struct {
	ID      string `json:"id" yaml:"id"`
	IDLike  string `json:"id_like" yaml:"id_like"`
	Name    string `json:"name" yaml:"name"`
	Version string `json:"version" yaml:"version"`
}

// User is the default user of an image.
type User struct {
	Name  string `json:"name" yaml:"name"`
	UID   int    `json:"uid" yaml:"uid"`
	GID   int    `json:"gid" yaml:"gid"`
	Group string `json:"group" yaml:"group"`
	Home  string `json:"home" yaml:"home"`
	Shell string `json:"shell" yaml:"shell"`
}

// Facts are the image facts of one image digest and platform (FR-011).
type Facts struct {
	Digest    string `json:"digest" yaml:"digest"`
	Reference string `json:"reference" yaml:"reference"`
	Platform  string `json:"platform" yaml:"platform"`
	OS        OS     `json:"os" yaml:"os"`
	// PackageManager is apt-get, dnf, yum, apk or none.
	PackageManager string   `json:"package_manager" yaml:"package_manager"`
	User           User     `json:"user" yaml:"user"`
	Shells         []string `json:"shells" yaml:"shells"`
	// HomeRCResetsPath lists the files among .bashrc, .profile and
	// .bash_profile in the default user's home that assign PATH without
	// referencing $PATH (R-05).
	HomeRCResetsPath []string `json:"home_rc_resets_path,omitempty" yaml:"home_rc_resets_path,omitempty"`
	// Path lists the directories of the base image's PATH as the probe
	// container saw it, in order, without empty or duplicate entries. The
	// session environment re-exports the ones beyond the OpenShell default
	// PATH (I-1).
	Path []string `json:"path,omitempty" yaml:"path,omitempty"`
	// Env lists the base image's exported variables other than PATH whose
	// value has at least one absolute colon-separated entry, and the names of
	// LoaderPathEnv and StartupEnv with any value, sorted by name. The plan
	// cleans the ones that resolve in the overmount home for the OpenShell
	// target (#18).
	Env []EnvVar `json:"env,omitempty" yaml:"env,omitempty"`
	// ProbedAt is the RFC 3339 time of the probe run. It is kept out of YAML
	// so that plans stay byte-identical.
	ProbedAt string `json:"probed_at" yaml:"-"`
}

// EnvVar is one variable of the base image environment.
type EnvVar struct {
	Name  string `json:"name" yaml:"name"`
	Value string `json:"value" yaml:"value"`
}

// BinHits are the PATH hits of one binary of a component. Real holds the
// resolved target of each hit (readlink -f), "" where it could not be
// resolved; it is nil or as long as Paths.
type BinHits struct {
	Name  string   `json:"name" yaml:"name"`
	Paths []string `json:"paths,omitempty" yaml:"paths,omitempty"`
	Real  []string `json:"real,omitempty" yaml:"real,omitempty"`
}

// Request is one component check to run in an image.
type Request struct {
	// Key is the component key.
	Key string
	// Command is the check command, run with /bin/sh -c.
	Command string
	// Bins are the binaries of the component, the first one primary: the
	// probe reports the path of the first and the PATH hits of each.
	Bins []string
	// Selector is the effective version selector.
	Selector string
	// ExpectExit is the expected exit code of Command.
	ExpectExit int
	// StdoutContains must appear in the output when set.
	StdoutContains string
	// Hash is component.CheckHash(Selector); it keys the cache entry.
	Hash string
}

// RequestFor builds the probe request of c. A non-empty selectorOverride
// (for example a manifest pin) replaces the component's own selector.
func RequestFor(c *component.Component, selectorOverride string) Request {
	selector := c.VersionSelector(selectorOverride)
	return Request{
		Key:            c.Key,
		Command:        c.Check.Command,
		Bins:           slices.Clone(c.Bins()),
		Selector:       selector,
		ExpectExit:     c.Check.Expect.Exit,
		StdoutContains: c.Check.Expect.StdoutContains,
		Hash:           c.CheckHash(selector),
	}
}

// Status is the outcome of one component check.
type Status string

// Check statuses. StatusTimeout means the check did not finish within
// CheckTimeout; it is never cached, and the plan treats it like missing.
const (
	StatusPresent      Status = "present"
	StatusMissing      Status = "missing"
	StatusIncompatible Status = "incompatible"
	StatusTimeout      Status = "timeout"
)

// Valid reports whether s is one of the known statuses.
func (s Status) Valid() bool {
	switch s {
	case StatusPresent, StatusMissing, StatusIncompatible, StatusTimeout:
		return true
	}
	return false
}

// CheckResult is the outcome of one component check in one image.
type CheckResult struct {
	ComponentKey string `json:"component_key" yaml:"component_key"`
	CheckHash    string `json:"check_hash" yaml:"check_hash"`
	Status       Status `json:"status" yaml:"status"`
	// Path is the location of the component's first binary, if found.
	Path string `json:"path" yaml:"path"`
	// Paths lists every PATH hit of the component's first binary, in PATH
	// order (R-02). Path is Paths[0] unless command -v found a builtin or
	// alias.
	Paths []string `json:"paths,omitempty" yaml:"paths,omitempty"`
	// Bins lists the PATH hits and their resolved targets of every binary of
	// the component, the first one primary (T060).
	Bins []BinHits `json:"bins,omitempty" yaml:"bins,omitempty"`
	// Version is the version parsed from Output.
	Version string `json:"version" yaml:"version"`
	// Output is the combined stdout and stderr of the check, cut to
	// MaxOutput characters.
	Output string `json:"output" yaml:"output"`
	// Exit is the exit code of the check command.
	Exit int `json:"exit" yaml:"exit"`
}

// Evaluate turns the raw outcome of req into a CheckResult. An exit code
// other than the expected one, or a missing expected output, means missing.
// Otherwise the version parsed from the output decides: one that does not
// satisfy the selector (or none at all when a selector is set) means
// incompatible.
func Evaluate(req Request, exit int, path, output string) CheckResult {
	output = strings.TrimRight(output, " \t\r\n")
	r := CheckResult{
		ComponentKey: req.Key,
		CheckHash:    req.Hash,
		Status:       StatusMissing,
		Path:         path,
		Output:       output,
		Exit:         exit,
	}
	if exit != req.ExpectExit || (req.StdoutContains != "" && !strings.Contains(output, req.StdoutContains)) {
		return r
	}
	r.Version = component.ParseVersion(output)
	if component.Satisfies(r.Version, req.Selector) {
		r.Status = StatusPresent
	} else {
		r.Status = StatusIncompatible
	}
	return r
}

// evaluateRaw evaluates one check line. A check that timeout(1) stopped is
// reported as timed out whatever its exit code.
func evaluateRaw(req Request, raw rawCheck) CheckResult {
	var r CheckResult
	if raw.Timeout {
		r = timedOut(req, raw.Exit, raw.Path, raw.Out,
			fmt.Sprintf("timeout: the check did not finish within %s", CheckTimeout))
	} else {
		r = Evaluate(req, raw.Exit, raw.Path, raw.Out)
	}
	if len(raw.Paths) > 0 {
		r.Paths = raw.Paths
	}
	if len(raw.Bins) > 0 {
		r.Bins = raw.Bins
	}
	return r
}

// timedOut is the result of a check that did not finish: StatusTimeout with
// reason as the first output line, followed by the output it printed.
func timedOut(req Request, exit int, path, output, reason string) CheckResult {
	if output = strings.TrimRight(output, " \t\r\n"); output != "" {
		reason += "\n" + output
	}
	return CheckResult{ComponentKey: req.Key, CheckHash: req.Hash, Status: StatusTimeout, Path: path, Output: reason, Exit: exit}
}

// Result is the outcome of Probe.
type Result struct {
	Facts  Facts                  `json:"facts"`
	Checks map[string]CheckResult `json:"checks"`
	// FactsFromCache is true when the facts were not probed in this call.
	FactsFromCache bool `json:"facts_from_cache"`
	// ContainerStarted is true when this call ran a probe container.
	ContainerStarted bool `json:"container_started"`
	// Probed lists the component keys checked by a container in this call,
	// sorted.
	Probed []string `json:"probed"`
	// Notices are human-readable remarks, for example a failed pull that
	// fell back to the local image.
	Notices []string `json:"notices"`
}
