package mateassets

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/nguyenngocanh94/mate/internal/memory"
)

// ManualName is the operating manual's file name in a Mate's directory. A
// harness that reads its instructions under another name is pointed at this
// file by its own launch (harness.Launcher.Prepare).
const ManualName = "AGENTS.md"

// Write renders the manual and the skills into dir, replacing whatever is
// there, and creates memory.md and backlog.md with their headers and empty
// sections (internal/memory) only if they do not already exist. dir must
// already exist; Write does not create it. It never touches an existing
// memory.md or backlog.md.
func Write(dir string, p Params) error {
	if !filepath.IsLocal(filepath.FromSlash(p.SkillsDir)) {
		return fmt.Errorf("mateassets: skills directory %q is not a relative path inside the Mate's directory", p.SkillsDir)
	}
	agentsMD, err := Render(p)
	if err != nil {
		return err
	}
	if err := writeAtomic(filepath.Join(dir, ManualName), agentsMD); err != nil {
		return err
	}
	if err := ensureFile(filepath.Join(dir, "memory.md"), memory.Header); err != nil {
		return err
	}
	if err := ensureFile(filepath.Join(dir, "backlog.md"), memory.BacklogHeader()); err != nil {
		return err
	}
	return writeSkills(dir, p)
}

// writeSkills installs every skill of p.Skills() under dir, replacing
// whatever is there. Skills are generated content like the manual, not the
// Mate's own memory, so they are rewritten on every start rather than
// preserved. The directories of RetiredSkillNames go first; a user's own
// skills and plugin-* skills are never touched here.
func writeSkills(dir string, p Params) error {
	root := filepath.Join(dir, filepath.FromSlash(p.SkillsDir))
	for _, name := range RetiredSkillNames {
		if err := removeRetiredSkill(filepath.Join(root, name)); err != nil {
			return err
		}
	}
	for _, name := range p.Skills() {
		data, err := RenderSkill(name, p)
		if err != nil {
			return err
		}
		skillDir := filepath.Join(dir, filepath.FromSlash(p.SkillsDir), name)
		if err := os.MkdirAll(skillDir, 0o755); err != nil {
			return err
		}
		if err := writeAtomic(filepath.Join(skillDir, "SKILL.md"), data); err != nil {
			return err
		}
	}
	return nil
}

// removeRetiredSkill removes the directory of a built-in skill an older mate
// installed. Only a real directory is removed; a symlink a user put in its
// place is theirs and is left, and an absent one is nothing to do.
func removeRetiredSkill(path string) error {
	info, err := os.Lstat(path)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return err
	}
	if !info.IsDir() {
		return nil
	}
	return os.RemoveAll(path)
}

// ensureFile writes header to path only if path does not already exist. An
// existing file, of any content, is left untouched.
func ensureFile(path, header string) error {
	if _, err := os.Stat(path); err == nil {
		return nil
	} else if !os.IsNotExist(err) {
		return err
	}
	return writeAtomic(path, []byte(header))
}

// writeAtomic writes data to path via a temp file in the same directory plus
// rename, so a reader never observes a partial write.
func writeAtomic(path string, data []byte) error {
	dir := filepath.Dir(path)
	tmp, err := os.CreateTemp(dir, "."+filepath.Base(path)+".tmp*")
	if err != nil {
		return err
	}
	name := tmp.Name()
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		os.Remove(name)
		return err
	}
	if err := tmp.Chmod(0o644); err != nil {
		tmp.Close()
		os.Remove(name)
		return err
	}
	if err := tmp.Close(); err != nil {
		os.Remove(name)
		return err
	}
	if err := os.Rename(name, path); err != nil {
		os.Remove(name)
		return err
	}
	return nil
}

// ProjectDocSeeded reports whether the PROJECT.md at path says nothing
// yet: it is missing, empty, or every non-blank line is a heading or an
// HTML comment, the seed nobody has written into. The manual then asks
// for the onboarding scout (CON-144).
func ProjectDocSeeded(path string) bool {
	data, err := os.ReadFile(path)
	if err != nil {
		return true
	}
	for _, l := range strings.Split(string(data), "\n") {
		t := strings.TrimSpace(l)
		if t == "" || strings.HasPrefix(t, "#") || (strings.HasPrefix(t, "<!--") && strings.HasSuffix(t, "-->")) {
			continue
		}
		return false
	}
	return true
}
