package plan

import (
	"strconv"

	"github.com/rhuss/sandcast/internal/manifest"
	"github.com/rhuss/sandcast/internal/probe"
)

// Created user defaults when the base image runs as root (R-10).
const (
	CreatedUserName = "sandbox"
	CreatedUserID   = 1000
	CreatedUserHome = "/home/sandbox"
)

// FinalUserFor keeps a non-root probed user and, for the OpenShell target,
// creates the sandbox user (uid 1000) when the base image runs as root. The
// container target keeps the base image's user as it is, root included,
// because it applies no sandbox user convention (FR-040).
func FinalUserFor(f probe.Facts, target string) FinalUser {
	u := f.User
	if target == manifest.TargetContainer {
		return keptUser(u)
	}
	if u.UID == 0 || u.Name == "root" || u.Name == "" {
		return FinalUser{
			Name:    CreatedUserName,
			UID:     CreatedUserID,
			GID:     CreatedUserID,
			Group:   CreatedUserName,
			Home:    CreatedUserHome,
			Created: true,
		}
	}
	return keptUser(u)
}

func keptUser(u probe.User) FinalUser {
	name := u.Name
	if name == "" && u.UID > 0 {
		// A numeric USER without a passwd entry (common in hardened images)
		// stays numeric; it must not turn into root.
		home := u.Home
		if home == "" {
			home = "/"
		}
		return FinalUser{Name: strconv.Itoa(u.UID), UID: u.UID, GID: u.GID, Group: strconv.Itoa(u.GID), Home: home}
	}
	if name == "" {
		name = "root"
	}
	group := u.Group
	if group == "" {
		group = name
	}
	home := u.Home
	if home == "" {
		if name == "root" {
			home = "/root"
		} else {
			home = "/home/" + name
		}
	}
	return FinalUser{Name: name, UID: u.UID, GID: u.GID, Group: group, Home: home}
}

// WorkspacePath is the directory OpenShell may mount the workspace volume
// over at runtime (spec "Workspace path"): the final user's home for the
// OpenShell target, which OpenShell v0.1.3 uses as the session HOME and
// workspace root. It comes from the image facts, never a fixed /sandbox.
// The container target has no workspace volume, so it returns "" and the
// workspace rule is off (FR-003).
func WorkspacePath(facts probe.Facts, target string) string {
	if target != manifest.TargetOpenShell {
		return ""
	}
	home := FinalUserFor(facts, target).Home
	if home == "" || home == "/" {
		return ""
	}
	return home
}
