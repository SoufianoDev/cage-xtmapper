package releasebuild

import (
	"fmt"
	"path/filepath"
	"strings"
)

// NativeContract reuses Ninja's link inputs without running its shell command.
func NativeContract(build, source, command string) (string, error) {
	tokens, err := shellWords(command)
	if err != nil {
		return "", err
	}
	start := -1
	for i, token := range tokens {
		if token == "-o" && i+1 < len(tokens) {
			start = i + 2
			break
		}
	}
	if start < 0 {
		return "", fmt.Errorf("Ninja link command has no output argument")
	}
	libs := []string{"libcage_xtmapper.a"}
	for _, token := range tokens[start:] {
		if !strings.HasSuffix(token, ".o") {
			libs = append(libs, token)
		}
	}
	if len(libs) == 1 {
		return "", fmt.Errorf("empty native link contract")
	}
	for i, lib := range libs {
		if !strings.HasPrefix(lib, "-") && !filepath.IsAbs(lib) {
			lib = filepath.Join(build, lib)
		}
		libs[i] = shellQuote(lib)
	}
	return "# Generated from ninja -t commands cage_xtmapper; do not edit.\n" +
		"Name: cage-xtmapper-native\nDescription: Native Cage/wlroots link inputs for the cgo bridge\n" +
		"Version: 0.3.0\nCflags: " + shellQuote("-I"+source) + "\nLibs: " + strings.Join(libs, " ") + "\n", nil
}

func shellQuote(value string) string {
	return "'" + strings.ReplaceAll(value, "'", "'\\''") + "'"
}

// Only literal shell words are needed for Ninja and pkg-config link arguments.
func shellWords(command string) ([]string, error) {
	var words []string
	var word strings.Builder
	var quote byte
	started := false
	for i := 0; i < len(command); i++ {
		c := command[i]
		if c == 0 || c == '\n' || c == '\r' {
			return nil, fmt.Errorf("invalid character in link command")
		}
		if quote == '\'' {
			if c == quote {
				quote = 0
			} else {
				word.WriteByte(c)
			}
			continue
		}
		switch {
		case c == '\\':
			i++
			if i == len(command) {
				return nil, fmt.Errorf("unfinished escape in link command")
			}
			if quote == '"' && !strings.ContainsRune("$`\"\\", rune(command[i])) {
				word.WriteByte('\\')
			}
			word.WriteByte(command[i])
			started = true
		case c == quote:
			quote = 0
		case quote == 0 && (c == '\'' || c == '"'):
			quote, started = c, true
		case quote == 0 && (c == ' ' || c == '\t'):
			if started {
				words = append(words, word.String())
				word.Reset()
				started = false
			}
		case c == '$' || c == '`' || (quote == 0 && strings.ContainsRune(";&|<>()", rune(c))):
			return nil, fmt.Errorf("shell expansion/operator in link command")
		default:
			word.WriteByte(c)
			started = true
		}
	}
	if quote != 0 {
		return nil, fmt.Errorf("unclosed quote in link command")
	}
	if started {
		words = append(words, word.String())
	}
	return words, nil
}
