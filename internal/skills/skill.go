// Package skills manages file-based skill documents: parsing SKILL.md,
// syncing them into Postgres with embeddings, and retrieving the ones relevant
// to a conversation.
package skills

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"regexp"
	"strings"

	"gopkg.in/yaml.v3"
)

// Frontmatter is the YAML header of a SKILL.md file.
type Frontmatter struct {
	Name         string   `yaml:"name"`
	Description  string   `yaml:"description"`
	AllowedTools []string `yaml:"allowed_tools"`
}

// Parsed is a fully parsed skill file.
type Parsed struct {
	Slug         string
	Name         string
	Description  string
	Body         string
	AllowedTools []string
	Checksum     string
}

var frontmatterRe = regexp.MustCompile(`(?s)\A---\s*\n(.*?)\n---\s*\n?(.*)\z`)

// Parse reads a SKILL.md file body. slug is normally the containing directory
// name.
func Parse(slug string, content []byte) (Parsed, error) {
	m := frontmatterRe.FindSubmatch(content)
	if m == nil {
		return Parsed{}, fmt.Errorf("skill %q: missing YAML frontmatter delimited by ---", slug)
	}
	var fm Frontmatter
	if err := yaml.Unmarshal(m[1], &fm); err != nil {
		return Parsed{}, fmt.Errorf("skill %q: bad frontmatter: %w", slug, err)
	}
	if strings.TrimSpace(fm.Description) == "" {
		return Parsed{}, fmt.Errorf("skill %q: frontmatter 'description' is required", slug)
	}
	name := strings.TrimSpace(fm.Name)
	if name == "" {
		name = slug
	}
	body := strings.TrimSpace(string(m[2]))
	sum := sha256.Sum256(content)

	return Parsed{
		Slug:         slug,
		Name:         name,
		Description:  strings.TrimSpace(fm.Description),
		Body:         body,
		AllowedTools: normalizeTools(fm.AllowedTools),
		Checksum:     hex.EncodeToString(sum[:]),
	}, nil
}

func normalizeTools(in []string) []string {
	out := make([]string, 0, len(in))
	seen := map[string]bool{}
	for _, t := range in {
		t = strings.TrimSpace(t)
		if t == "" || seen[t] {
			continue
		}
		seen[t] = true
		out = append(out, t)
	}
	return out
}

// embedText is the text embedded for retrieval: name plus description. The body
// is deliberately excluded so retrieval keys on the skill's stated purpose.
func (p Parsed) embedText() string {
	return p.Name + "\n" + p.Description
}
