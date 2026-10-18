package plan

import (
	"sort"
	"strings"
)

// Notice kinds (R-15). Plan notices come from plan inputs and are written
// into the plan file; runtime notices come from the host, the engine or the
// gateway and appear only on the console and in the build report.
const (
	NoticeLockfileShadow = "lockfile-shadow"
	NoticeUnknownVersion = "unknown-version"
	NoticeWorkspacePath  = "workspace-path"
	NoticeHomeRCPath     = "home-rc-path"
	NoticeReference      = "reference"
	NoticeEmulation      = "emulation"
	NoticeGateway        = "gateway"
	NoticeDockerHub      = "docker-hub"
)

// NoticeSubjectImage is the subject of notices about the image as a whole.
const NoticeSubjectImage = "image"

// Notice formats a notice as "<kind>: <subject>: <message>". The stable
// prefix keeps notices grep-able without a plan schema change.
func Notice(kind, subject, message string) string {
	return kind + ": " + subject + ": " + message
}

// SortNotices orders notices by kind, then subject, then the full text, so
// the plan stays deterministic. Notices without the kind prefix sort by
// their text, after an empty kind.
func SortNotices(notices []string) {
	sort.SliceStable(notices, func(i, j int) bool {
		ki, si := noticeParts(notices[i])
		kj, sj := noticeParts(notices[j])
		if ki != kj {
			return ki < kj
		}
		if si != sj {
			return si < sj
		}
		return notices[i] < notices[j]
	})
}

func noticeParts(n string) (kind, subject string) {
	kind, rest, ok := strings.Cut(n, ": ")
	if !ok {
		return "", n
	}
	subject, _, _ = strings.Cut(rest, ": ")
	return kind, subject
}
