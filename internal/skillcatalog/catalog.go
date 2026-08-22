package skillcatalog

import (
	"crypto/sha256"
	"embed"
	"encoding/hex"
	"errors"
	"fmt"
	"io/fs"
	"sort"
	"strconv"
	"strings"
	"unicode"
)

//go:embed skills/*.md
var embeddedFiles embed.FS

type SkillVersion struct {
	Identity    string `json:"identity"`
	Version     int    `json:"version"`
	Name        string `json:"name"`
	Description string `json:"description"`
	Body        string `json:"body"`
	SHA256      string `json:"-"`
	SourcePath  string `json:"-"`
}

type Catalog struct {
	versions map[string]SkillVersion
}

func LoadEmbedded() (Catalog, error) {
	paths, err := fs.Glob(embeddedFiles, "skills/*.md")
	if err != nil {
		return Catalog{}, err
	}
	definitions := make([]SkillVersion, 0, len(paths))
	for _, filePath := range paths {
		payload, err := embeddedFiles.ReadFile(filePath)
		if err != nil {
			return Catalog{}, err
		}
		definition, err := parseMarkdown(filePath, string(payload))
		if err != nil {
			return Catalog{}, err
		}
		definitions = append(definitions, definition)
	}
	return New(definitions)
}

func MustLoadEmbedded() Catalog {
	catalog, err := LoadEmbedded()
	if err != nil {
		panic(err)
	}
	return catalog
}

func New(definitions []SkillVersion) (Catalog, error) {
	catalog := Catalog{versions: make(map[string]SkillVersion, len(definitions))}
	for _, definition := range definitions {
		definition = canonicalize(definition)
		if err := validate(definition); err != nil {
			return Catalog{}, err
		}
		hash, err := CanonicalSHA256(definition)
		if err != nil {
			return Catalog{}, err
		}
		if definition.SHA256 != "" && !strings.EqualFold(definition.SHA256, hash) {
			return Catalog{}, fmt.Errorf("skill %s@%d hash conflict", definition.Identity, definition.Version)
		}
		definition.SHA256 = hash
		key := catalogKey(definition.Identity, definition.Version)
		if existing, ok := catalog.versions[key]; ok {
			if existing.SHA256 != definition.SHA256 {
				return Catalog{}, fmt.Errorf("skill %s@%d definition conflict", definition.Identity, definition.Version)
			}
			continue
		}
		catalog.versions[key] = definition
	}
	if len(catalog.versions) == 0 {
		return Catalog{}, errors.New("skill catalog is empty")
	}
	return catalog, nil
}

func (c Catalog) Resolve(identity string, version int) (SkillVersion, bool) {
	definition, ok := c.versions[catalogKey(strings.TrimSpace(identity), version)]
	return definition, ok
}

func (c Catalog) Versions() []SkillVersion {
	versions := make([]SkillVersion, 0, len(c.versions))
	for _, definition := range c.versions {
		versions = append(versions, definition)
	}
	sort.Slice(versions, func(left, right int) bool {
		if versions[left].Identity == versions[right].Identity {
			return versions[left].Version < versions[right].Version
		}
		return versions[left].Identity < versions[right].Identity
	})
	return versions
}

func CanonicalSHA256(definition SkillVersion) (string, error) {
	definition = canonicalize(definition)
	if err := validate(definition); err != nil {
		return "", err
	}
	canonical := fmt.Sprintf("identity:%s\nversion:%d\nname:%s\ndescription:%s\n\n%s",
		definition.Identity, definition.Version, definition.Name, definition.Description, definition.Body)
	digest := sha256.Sum256([]byte(canonical))
	return hex.EncodeToString(digest[:]), nil
}

func parseMarkdown(filePath, payload string) (SkillVersion, error) {
	payload = normalizeNewlines(payload)
	if !strings.HasPrefix(payload, "---\n") {
		return SkillVersion{}, fmt.Errorf("skill %s has invalid front matter", filePath)
	}
	parts := strings.SplitN(strings.TrimPrefix(payload, "---\n"), "\n---\n", 2)
	if len(parts) != 2 {
		return SkillVersion{}, fmt.Errorf("skill %s has invalid front matter", filePath)
	}
	metadata := make(map[string]string, 4)
	for _, line := range strings.Split(parts[0], "\n") {
		key, value, ok := strings.Cut(line, ":")
		if !ok {
			return SkillVersion{}, fmt.Errorf("skill %s has invalid metadata", filePath)
		}
		key = strings.TrimSpace(key)
		if _, duplicate := metadata[key]; duplicate {
			return SkillVersion{}, fmt.Errorf("skill %s has duplicate metadata %q", filePath, key)
		}
		metadata[key] = strings.TrimSpace(value)
	}
	if len(metadata) != 4 {
		return SkillVersion{}, fmt.Errorf("skill %s has unknown or missing metadata", filePath)
	}
	version, err := strconv.Atoi(metadata["version"])
	if err != nil {
		return SkillVersion{}, fmt.Errorf("skill %s has invalid version", filePath)
	}
	return SkillVersion{
		Identity: metadata["identity"], Version: version, Name: metadata["name"],
		Description: metadata["description"], Body: parts[1], SourcePath: filePath,
	}, nil
}

func canonicalize(definition SkillVersion) SkillVersion {
	definition.Identity = strings.TrimSpace(definition.Identity)
	definition.Name = strings.TrimSpace(definition.Name)
	definition.Description = strings.TrimSpace(definition.Description)
	definition.Body = strings.TrimSpace(normalizeNewlines(definition.Body)) + "\n"
	definition.SourcePath = strings.TrimSpace(definition.SourcePath)
	return definition
}

func validate(definition SkillVersion) error {
	if !validIdentity(definition.Identity) || definition.Identity == "latest" || definition.Version < 1 ||
		len([]rune(definition.Name)) < 1 || len([]rune(definition.Name)) > 100 ||
		len([]rune(definition.Description)) < 1 || len([]rune(definition.Description)) > 500 ||
		strings.TrimSpace(definition.Body) == "" {
		return errors.New("invalid skill version")
	}
	return nil
}

func validIdentity(value string) bool {
	if value == "" || !strings.Contains(value, ".") {
		return false
	}
	for _, character := range value {
		if unicode.IsLower(character) || unicode.IsDigit(character) || character == '.' || character == '-' || character == '_' {
			continue
		}
		return false
	}
	return true
}

func normalizeNewlines(value string) string {
	return strings.ReplaceAll(strings.ReplaceAll(value, "\r\n", "\n"), "\r", "\n")
}

func catalogKey(identity string, version int) string {
	return identity + "@" + strconv.Itoa(version)
}
