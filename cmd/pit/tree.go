package main

import (
	"cmp"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"

	"github.com/patrickbergner/pit/internal/ui"
	"github.com/patrickbergner/pit/internal/workspace"
)

// treeNode is a directory in the tree that tree shows: an external, a directory on the way to one, or a sibling of those.
type treeNode struct {
	name    string
	repo    *workspace.Repo // the external in this directory
	sibling bool            // neither an external nor on the way to one: shown, not gone into
	kids    []*treeNode
}

// child returns n's child called name, added if it isn't there yet.
func (n *treeNode) child(name string) *treeNode {
	for _, k := range n.kids {
		if k.name == name || foldCase && strings.EqualFold(k.name, name) {
			return k
		}
	}
	k := &treeNode{name: name}
	n.kids = append(n.kids, k)
	return k
}

// foldCase: the file system doesn't tell names apart by case, so a path in the config may differ in case from the directory on disk
var foldCase = runtime.GOOS == "windows" || runtime.GOOS == "darwin"

// addSiblings adds the subdirectories of dir, n's directory, that n doesn't have yet as siblings, and does the same in the children on the
// way to an external. .git and pit's .pit are left out; so are directories that don't exist yet, as those of externals not cloned.
func (n *treeNode) addSiblings(dir string) {
	entries, _ := os.ReadDir(dir)
	for _, e := range entries {
		if !e.IsDir() || e.Name() == ".git" || e.Name() == ".pit" {
			continue
		}
		if k := n.child(e.Name()); k.repo == nil && len(k.kids) == 0 {
			k.sibling = true
		}
	}
	for _, k := range n.kids {
		if k.repo == nil && !k.sibling {
			k.addSiblings(filepath.Join(dir, k.name))
		}
	}
}

// cmdTree shows the directories from the root down to the selected externals, sorted by name, together with their sibling directories
// (dim, and not gone into). Each external has its branch, groups and whether it is cloned next to it, aligned across the tree; its name
// too, if that isn't its directory's. Then a summary as in list.
func cmdTree(w *workspace.Workspace, args []string) error {
	repos, err := selection(w, "tree", args)
	if err != nil {
		return err
	}
	if len(w.Externals) == 0 {
		fmt.Println("no externals configured in", w.Root)
		return nil
	}
	root := &treeNode{}
	var exts []*workspace.Repo
	branchW, groupsW := 0, 0
	for _, r := range repos {
		if r.IsRoot() {
			continue
		}
		n := root
		for _, dir := range strings.Split(r.Path, "/") {
			n = n.child(dir)
		}
		n.repo = r // externals don't nest, so this is a leaf
		exts = append(exts, r)
		branchW = max(branchW, ui.Width(r.Ext.Branch))
		groupsW = max(groupsW, ui.Width(strings.Join(r.Ext.Groups, ", ")))
	}
	root.addSiblings(w.Root)

	// the tree part of each line first, to align what follows the externals
	type line struct {
		tree string
		repo *workspace.Repo
	}
	var lines []line
	var walk func(n *treeNode, indent string)
	walk = func(n *treeNode, indent string) {
		slices.SortFunc(n.kids, func(a, b *treeNode) int {
			return cmp.Or(strings.Compare(strings.ToLower(a.name), strings.ToLower(b.name)), strings.Compare(a.name, b.name))
		})
		for i, k := range n.kids {
			branch, more := "├── ", "│   "
			if i == len(n.kids)-1 {
				branch, more = "└── ", "    "
			}
			label := k.name
			if k.sibling {
				label = ui.Dim(k.name)
			}
			if k.repo != nil {
				label = ui.BoldCyan(k.name)
				if k.repo.Name != k.name {
					label += ui.Dim(" (" + k.repo.Name + ")")
				}
			}
			lines = append(lines, line{ui.Dim(indent+branch) + label, k.repo})
			walk(k, indent+more)
		}
	}
	walk(root, "")
	treeW := 0
	for _, l := range lines {
		if l.repo != nil {
			treeW = max(treeW, ui.Width(l.tree))
		}
	}

	fmt.Println(ui.Bold(w.Root) + ui.Dim(" "+workspace.RootLabel))
	for _, l := range lines {
		s := l.tree
		if r := l.repo; r != nil {
			s = ui.Pad(s, treeW) + "  " + ui.Pad(ui.Green(r.Ext.Branch), branchW)
			if groupsW > 0 {
				s += "  " + ui.Pad(ui.Cyan(strings.Join(r.Ext.Groups, ", ")), groupsW)
			}
			s += "  " + cloneState(r)
		}
		fmt.Println(s)
	}
	if len(exts) > 0 {
		fmt.Println(externalsSummary(exts))
	}
	return nil
}
