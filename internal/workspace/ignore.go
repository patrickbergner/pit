package workspace

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/patrickbergner/pit/internal/config"
)

// The block pit maintains in the root repo's .gitignore.
const (
	IgnoreBegin = "# >>> pit externals - managed by pit, do not edit"
	IgnoreEnd   = "# <<< pit externals"
)

// IgnoreFile is the root repo's .gitignore.
func (w *Workspace) IgnoreFile() string { return filepath.Join(w.Root, ".gitignore") }

// IgnoreEntries are the lines of the managed block: pit's state directory and every external's path, anchored to the root.
func (w *Workspace) IgnoreEntries() []string {
	entries := []string{ignorePattern(config.StateDir)}
	for _, r := range w.Externals {
		entries = append(entries, ignorePattern(r.Path))
	}
	return entries
}

// ignorePattern matches exactly the directory p (relative to the root) and nothing else. The leading and trailing slash keep '#', '!' and
// trailing spaces from having their special meaning; glob characters are escaped.
func ignorePattern(p string) string {
	var b strings.Builder
	b.WriteByte('/')
	for _, c := range p {
		if strings.ContainsRune(`\*?[`, c) {
			b.WriteByte('\\')
		}
		b.WriteRune(c)
	}
	b.WriteByte('/')
	return b.String()
}

// ReadIgnore returns the root's .gitignore ("" if it doesn't exist).
func (w *Workspace) ReadIgnore() (string, error) {
	data, err := os.ReadFile(w.IgnoreFile())
	if errors.Is(err, fs.ErrNotExist) {
		return "", nil
	}
	return string(data), err
}

// IgnoreUpToDate reports whether the managed block in .gitignore lists exactly the current entries.
func (w *Workspace) IgnoreUpToDate() bool {
	content, err := w.ReadIgnore()
	if err != nil {
		return false
	}
	current, found := ParseIgnoreBlock(content)
	return found && slices.Equal(current, w.IgnoreEntries())
}

// ParseIgnoreBlock returns the entries of the managed block in a .gitignore.
func ParseIgnoreBlock(content string) (entries []string, found bool) {
	lines := strings.Split(strings.ReplaceAll(content, "\r\n", "\n"), "\n")
	in := false
	for _, l := range lines {
		switch {
		case l == IgnoreBegin:
			in, found = true, true
		case l == IgnoreEnd:
			in = false
		case in && strings.TrimSpace(l) != "":
			entries = append(entries, l)
		}
	}
	return entries, found
}

// UpdateIgnoreBlock returns content with the managed block replaced by entries, or appended if there is none. The rest of the file and its
// line endings are kept.
func UpdateIgnoreBlock(content string, entries []string) string {
	nl := "\n"
	if strings.Contains(content, "\r\n") {
		nl = "\r\n"
	}
	block := IgnoreBegin + nl + strings.Join(entries, nl) + nl + IgnoreEnd + nl

	lines := strings.SplitAfter(content, "\n")
	var out strings.Builder
	replaced, in := false, false
	for _, l := range lines {
		bare := strings.TrimRight(l, "\r\n")
		switch {
		case !in && bare == IgnoreBegin:
			in = true
			if !replaced {
				out.WriteString(block)
				replaced = true
			}
		case in:
			if bare == IgnoreEnd {
				in = false
			}
		default:
			out.WriteString(l)
		}
	}
	if replaced {
		return out.String()
	}
	s := out.String()
	if s != "" && !strings.HasSuffix(s, "\n") {
		s += nl
	}
	if s != "" {
		s += nl
	}
	return s + block
}
