package herdr

import (
	"strconv"
	"strings"
)

var discoveryFunction = []string{
	"emit() {",
	"    path=$1",
	"    if [ -n \"$path\" ] && [ -x \"$path\" ]; then",
	"        printf '%s\\n' \"$path\"",
	"    fi",
	"}",
}

func supportedDiscoveryScript(command string) bool {
	lines := strings.Split(command, "\n")
	if len(lines) < 12 || lines[0] != "home=${HOME:-}" || lines[1] != "user=${USER:-}" || !strings.HasPrefix(lines[2], "version=") {
		return false
	}
	if !supportedVersion(strings.TrimPrefix(lines[2], "version=")) {
		return false
	}
	for i, expected := range discoveryFunction {
		if lines[i+3] != expected {
			return false
		}
	}

	seenCandidate, blockHasCandidate := false, false
	block := ""
	seenBlocks := map[string]bool{}
	for _, rawLine := range lines[3+len(discoveryFunction):] {
		line := strings.TrimSpace(rawLine)
		switch line {
		case `if [ -n "$home" ]; then`, `if [ -n "$user" ]; then`:
			variable := "$home"
			if line == `if [ -n "$user" ]; then` {
				variable = "$user"
			}
			if block != "" || seenBlocks[variable] {
				return false
			}
			block, blockHasCandidate = variable, false
			seenBlocks[variable] = true
		case "fi":
			if block == "" || !blockHasCandidate {
				return false
			}
			block = ""
		default:
			if !safeEmitLine(line, block) {
				return false
			}
			seenCandidate = true
			blockHasCandidate = blockHasCandidate || block != ""
		}
	}
	return seenCandidate && block == ""
}

func safeEmitLine(line, block string) bool {
	if !strings.HasPrefix(line, `emit "`) || !strings.HasSuffix(line, `"`) {
		return false
	}
	path := strings.TrimSuffix(strings.TrimPrefix(line, `emit "`), `"`)
	if path == "" || !strings.HasSuffix(path, "/herdr") {
		return false
	}
	for _, r := range path {
		if !(r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || strings.ContainsRune("/._-$", r)) {
			return false
		}
	}
	if block == "" {
		return !strings.Contains(path, "$")
	}
	return strings.Contains(path, block) && !strings.Contains(strings.ReplaceAll(path, block, ""), "$")
}

func supportedVersion(version string) bool {
	coreAndPre, build, hasBuild := strings.Cut(version, "+")
	if hasBuild && (!validIdentifiers(build, false) || strings.Contains(build, "+")) {
		return false
	}
	core, prerelease, hasPrerelease := strings.Cut(coreAndPre, "-")
	if hasPrerelease && !validIdentifiers(prerelease, true) {
		return false
	}
	fields := strings.Split(core, ".")
	if len(fields) != 3 {
		return false
	}
	parts := [3]int{}
	for i := range parts {
		if fields[i] == "" || len(fields[i]) > 1 && fields[i][0] == '0' {
			return false
		}
		part, err := strconv.Atoi(fields[i])
		if err != nil || part < 0 {
			return false
		}
		parts[i] = part
	}
	minimum := [3]int{0, 9, 0}
	for i := range parts {
		if parts[i] != minimum[i] {
			return parts[i] > minimum[i]
		}
	}
	return !hasPrerelease
}

func validIdentifiers(value string, prerelease bool) bool {
	for _, identifier := range strings.Split(value, ".") {
		if identifier == "" {
			return false
		}
		allDigits := true
		for _, r := range identifier {
			if !(r >= '0' && r <= '9') {
				allDigits = false
			}
			if !(r >= '0' && r <= '9' || r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r == '-') {
				return false
			}
		}
		if prerelease && allDigits && len(identifier) > 1 && identifier[0] == '0' {
			return false
		}
	}
	return true
}
