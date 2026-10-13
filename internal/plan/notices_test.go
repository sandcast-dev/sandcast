package plan

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestNotice(t *testing.T) {
	assert.Equal(t, "lockfile-shadow: gh: base image has 2.93.0", Notice(NoticeLockfileShadow, "gh", "base image has 2.93.0"))
}

func TestSortNotices(t *testing.T) {
	in := []string{
		Notice(NoticeWorkspacePath, "python", "found only under /sandbox"),
		Notice(NoticeLockfileShadow, "node", "b"),
		Notice(NoticeLockfileShadow, "gh", "a"),
		Notice(NoticeHomeRCPath, NoticeSubjectImage, "/sandbox/.bashrc resets PATH"),
		Notice(NoticeUnknownVersion, "codex", "c"),
	}
	SortNotices(in)
	assert.Equal(t, []string{
		"home-rc-path: image: /sandbox/.bashrc resets PATH",
		"lockfile-shadow: gh: a",
		"lockfile-shadow: node: b",
		"unknown-version: codex: c",
		"workspace-path: python: found only under /sandbox",
	}, in)
}
