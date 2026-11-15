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

// The markers of the block pit maintains in an ignore file.
const (
	IgnoreBegin = "# >>> pit externals - managed by pit, do not edit"
	IgnoreEnd   = "# <<< pit externals"
)

// IgnoreBlock is the block in an ignore file that keeps the externals out of the repo containing them: in the root repo's .gitignore, and
// in the .git/info/exclude of an external, for the externals inside it. The root repo's is versioned and works in every clone of it; an
// external's is local to the clone and leaves the external's history alone.
type IgnoreBlock struct {
	Repo    *Repo    // the repo whose ignore file it is
	File    string   // absolute
	Entries []string // the lines the block should have; none means there should be no block
}

// IgnoreBlocks returns the ignore blocks of the cloned externals and of the root repo, if the root is a git repo, in workspace order. A
// cloned external without externals inside gets one too, without entries, so a block left over from earlier is noticed and removed.
func (w *Workspace) IgnoreBlocks() []IgnoreBlock {
	var blocks []IgnoreBlock
	for _, r := range w.Cloned() {
		blocks = append(blocks, w.IgnoreBlock(r))
	}
	return blocks
}

// IgnoreBlock returns the ignore block of repo c. The root repo's lists pit's state directory and the externals directly below the root,
// an external's the externals directly inside it, each anchored to the repo's top directory. Externals further inside are kept out by the
// block of the external holding them.
func (w *Workspace) IgnoreBlock(c *Repo) IgnoreBlock {
	b := IgnoreBlock{Repo: c}
	if c.IsRoot() {
		b.File = filepath.Join(w.Root, ".gitignore")
		b.Entries = []string{ignorePattern(config.StateDir)}
	} else {
		b.File = filepath.Join(c.CommonDir(), "info", "exclude")
	}
	for _, r := range w.Inside(c) {
		b.Entries = append(b.Entries, ignorePattern(r.PathIn(c)))
	}
	return b
}

// Name is how messages name the block's file: ".gitignore", or ".git/info/exclude" (an absolute path if it's not below the repo's
// directory, as for a linked worktree).
func (b IgnoreBlock) Name() string {
	rel, err := filepath.Rel(b.Repo.Dir, b.File)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return b.File
	}
	return filepath.ToSlash(rel)
}

// Read returns the content of the block's file ("" if it doesn't exist).
func (b IgnoreBlock) Read() (string, error) {
	data, err := os.ReadFile(b.File)
	if errors.Is(err, fs.ErrNotExist) {
		return "", nil
	}
	return string(data), err
}

// UpToDate reports whether the file has the block with exactly the entries or, if there are none, has no block.
func (b IgnoreBlock) UpToDate() bool {
	content, err := b.Read()
	if err != nil {
		return false
	}
	current, found := ParseIgnoreBlock(content)
	if len(b.Entries) == 0 {
		return !found
	}
	return found && slices.Equal(current, b.Entries)
}

// ignorePattern matches exactly the directory p (relative to the repo's top directory) and nothing else. The leading and trailing slash
// keep '#', '!' and trailing spaces from having their special meaning; glob characters are escaped.
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

// ParseIgnoreBlock returns the entries of the managed block in an ignore file.
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

// UpdateIgnoreBlock returns content with the managed block replaced by entries, or appended if there is none. Without entries, the block
// is removed instead, together with the empty line that set it apart if it was at the end. The rest of the file and its line endings are
// kept.
func UpdateIgnoreBlock(content string, entries []string) string {
	nl := "\n"
	if strings.Contains(content, "\r\n") {
		nl = "\r\n"
	}
	block := ""
	if len(entries) > 0 {
		block = IgnoreBegin + nl + strings.Join(entries, nl) + nl + IgnoreEnd + nl
	}

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
	s := out.String()
	if block == "" {
		if replaced && strings.HasSuffix(s, nl+nl) {
			s = strings.TrimSuffix(s, nl)
		}
		return s
	}
	if replaced {
		return s
	}
	if s != "" && !strings.HasSuffix(s, "\n") {
		s += nl
	}
	if s != "" {
		s += nl
	}
	return s + block
}
