package component

import (
	"regexp"
	"strconv"
	"strings"
)

var versionPattern = regexp.MustCompile(`\d+(?:\.\d+)+(?:[-+][0-9A-Za-z.]+)?|\d+`)

// ParseVersion extracts the first version-like token (1.2.3, 1.22, go1.25.1)
// from check output. It returns "" when none is found.
func ParseVersion(output string) string {
	for line := range strings.SplitSeq(output, "\n") {
		if m := versionPattern.FindString(line); m != "" && strings.Contains(m, ".") {
			return m
		}
	}
	return ""
}

// Satisfies reports whether installed matches selector. Supported selectors:
// empty or "latest" (anything), a prefix such as "1.25" (1.25 or 1.25.x),
// an exact version, and comparisons ">=1.2", ">1.2", "<=1.2", "<1.2",
// "^1.2" (same major) and "~1.2" (same major and minor). An unknown
// selector such as "lts" accepts any installed version.
func Satisfies(installed, selector string) bool {
	selector = strings.TrimSpace(selector)
	if selector == "" || selector == "latest" {
		return true
	}
	if installed == "" {
		return false
	}
	installed = strings.TrimPrefix(installed, "v")
	for _, op := range []string{">=", "<=", ">", "<", "^", "~", "="} {
		if !strings.HasPrefix(selector, op) {
			continue
		}
		want := strings.TrimPrefix(strings.TrimSpace(strings.TrimPrefix(selector, op)), "v")
		c := Compare(installed, want)
		switch op {
		case ">=":
			return c >= 0
		case "<=":
			return c <= 0
		case ">":
			return c > 0
		case "<":
			return c < 0
		case "=":
			return c == 0
		case "^":
			return c >= 0 && part(installed, 0) == part(want, 0)
		case "~":
			return c >= 0 && part(installed, 0) == part(want, 0) && part(installed, 1) == part(want, 1)
		}
	}
	sel := strings.TrimPrefix(selector, "v")
	if !versionPattern.MatchString(sel) || strings.ContainsAny(sel, " |") {
		return true
	}
	return installed == sel || strings.HasPrefix(installed, sel+".") || strings.HasPrefix(installed, sel+"-")
}

// Compare compares two dotted versions numerically: -1, 0 or 1. Pre-release
// suffixes are ignored.
func Compare(a, b string) int {
	pa, pb := numbers(a), numbers(b)
	for i := 0; i < len(pa) || i < len(pb); i++ {
		var x, y int
		if i < len(pa) {
			x = pa[i]
		}
		if i < len(pb) {
			y = pb[i]
		}
		switch {
		case x < y:
			return -1
		case x > y:
			return 1
		}
	}
	return 0
}

func numbers(v string) []int {
	v = strings.TrimPrefix(v, "v")
	if i := strings.IndexAny(v, "-+"); i >= 0 {
		v = v[:i]
	}
	var out []int
	for p := range strings.SplitSeq(v, ".") {
		n, err := strconv.Atoi(p)
		if err != nil {
			break
		}
		out = append(out, n)
	}
	return out
}

func part(v string, i int) int {
	n := numbers(v)
	if i < len(n) {
		return n[i]
	}
	return 0
}
