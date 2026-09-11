package skills

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// DiscoveredSkill pairs a parsed skill with its on-disk location and bundled
// resource files.
type DiscoveredSkill struct {
	Parsed
	Dir       string
	Resources []string // paths relative to Dir, excluding SKILL.md
}

// Discover walks root looking for `<root>/<slug>/SKILL.md` files.
func Discover(root string) ([]DiscoveredSkill, error) {
	entries, err := os.ReadDir(root)
	if err != nil {
		return nil, fmt.Errorf("skills.Discover: %w", err)
	}
	var out []DiscoveredSkill
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		dir := filepath.Join(root, e.Name())
		mdPath := filepath.Join(dir, "SKILL.md")
		content, err := os.ReadFile(mdPath)
		if err != nil {
			if os.IsNotExist(err) {
				continue
			}
			return nil, fmt.Errorf("skills.Discover: read %s: %w", mdPath, err)
		}
		parsed, err := Parse(e.Name(), content)
		if err != nil {
			return nil, err
		}
		res, err := listResources(dir)
		if err != nil {
			return nil, err
		}
		out = append(out, DiscoveredSkill{Parsed: parsed, Dir: dir, Resources: res})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Slug < out[j].Slug })
	return out, nil
}

func listResources(dir string) ([]string, error) {
	var res []string
	err := filepath.WalkDir(dir, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}
		rel, _ := filepath.Rel(dir, path)
		if rel == "SKILL.md" {
			return nil
		}
		res = append(res, rel)
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("skills.listResources: %w", err)
	}
	sort.Strings(res)
	return res, nil
}

// slugList extracts the slugs from a set of discovered skills.
func slugList(ds []DiscoveredSkill) []string {
	out := make([]string, len(ds))
	for i, d := range ds {
		out[i] = d.Slug
	}
	return out
}

var _ = strings.TrimSpace
