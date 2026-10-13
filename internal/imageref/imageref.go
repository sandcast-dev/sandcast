// Package imageref normalizes container image references the way container
// engines resolve them (R-12), so sandcast can report the fully qualified
// reference a gateway pulls and warn about references that resolve to an
// unexpected place.
package imageref

import (
	"errors"
	"fmt"
	"regexp"
	"strings"
)

// DockerHub is the registry of references without a registry part.
const DockerHub = "docker.io"

// DefaultTag is the tag of references without a tag or digest.
const DefaultTag = "latest"

var (
	componentRe = regexp.MustCompile(`^[a-z0-9]+(?:(?:[._]|__|-+)[a-z0-9]+)*$`)
	tagRe       = regexp.MustCompile(`^[A-Za-z0-9_][A-Za-z0-9_.-]{0,127}$`)
	digestRe    = regexp.MustCompile(`^[a-z0-9]+(?:[.+_-][a-z0-9]+)*:[A-Fa-f0-9]{32,}$`)
)

// Ref is a normalized image reference.
type Ref struct {
	// Registry is the registry host, with a port when given (docker.io,
	// localhost:5000, quay.io).
	Registry string
	// Repository is the path below the registry (library/ubuntu, rhuss/demo).
	Repository string
	// Tag is the tag; DefaultTag when the input had neither tag nor digest.
	Tag string
	// Digest is the digest, for example sha256:..., when given.
	Digest string
	// Input is the reference as given.
	Input string
}

// Normalize parses ref and fills in what an engine assumes: the first path
// component is a registry when it contains "." or ":" or is "localhost";
// otherwise docker.io is the registry. A Docker Hub repository without a
// namespace gets "library/". A reference without tag and digest gets the
// tag latest.
func Normalize(ref string) (Ref, error) {
	r := Ref{Input: ref}
	if ref == "" {
		return r, errors.New("empty image reference")
	}
	if strings.ContainsAny(ref, " \t\n\r") {
		return r, fmt.Errorf("image reference %q contains whitespace", ref)
	}
	name := ref
	if i := strings.Index(name, "@"); i >= 0 {
		r.Digest = name[i+1:]
		name = name[:i]
		if !digestRe.MatchString(r.Digest) {
			return r, fmt.Errorf("image reference %q: invalid digest %q", ref, r.Digest)
		}
	}
	if i := strings.LastIndex(name, ":"); i >= 0 && !strings.Contains(name[i+1:], "/") {
		r.Tag = name[i+1:]
		name = name[:i]
		if !tagRe.MatchString(r.Tag) {
			return r, fmt.Errorf("image reference %q: invalid tag %q", ref, r.Tag)
		}
	}
	parts := strings.Split(name, "/")
	if len(parts) > 1 && (strings.ContainsAny(parts[0], ".:") || parts[0] == "localhost") {
		r.Registry = parts[0]
		parts = parts[1:]
	} else {
		r.Registry = DockerHub
	}
	if r.Registry == "index.docker.io" || r.Registry == "registry-1.docker.io" {
		r.Registry = DockerHub
	}
	for _, p := range parts {
		if !componentRe.MatchString(p) {
			return r, fmt.Errorf("image reference %q: invalid path component %q", ref, p)
		}
	}
	if r.Registry == DockerHub && len(parts) == 1 {
		parts = append([]string{"library"}, parts...)
	}
	r.Repository = strings.Join(parts, "/")
	if r.Tag == "" && r.Digest == "" {
		r.Tag = DefaultTag
	}
	return r, nil
}

// Name is registry/repository without tag or digest.
func (r Ref) Name() string { return r.Registry + "/" + r.Repository }

// String is the fully qualified reference: registry/repository, then :tag
// and @digest when set.
func (r Ref) String() string {
	s := r.Name()
	if r.Tag != "" {
		s += ":" + r.Tag
	}
	if r.Digest != "" {
		s += "@" + r.Digest
	}
	return s
}

// DockerHubNoNamespace reports whether the input names a Docker Hub
// repository without a namespace (ubuntu, docker.io/demo), which resolves
// to docker.io/library/<name>, the namespace of the official images.
func (r Ref) DockerHubNoNamespace() bool {
	if r.Registry != DockerHub {
		return false
	}
	name, _, _ := strings.Cut(r.Input, "@")
	if i := strings.LastIndex(name, ":"); i >= 0 && !strings.Contains(name[i+1:], "/") {
		name = name[:i]
	}
	if first, rest, ok := strings.Cut(name, "/"); ok && (strings.ContainsAny(first, ".:") || first == "localhost") {
		name = rest
	}
	return !strings.Contains(name, "/")
}

// IsLocalhost reports whether the reference names the localhost registry,
// which only the engine that built the image can resolve.
func (r Ref) IsLocalhost() bool {
	host, _, _ := strings.Cut(r.Registry, ":")
	return host == "localhost"
}
