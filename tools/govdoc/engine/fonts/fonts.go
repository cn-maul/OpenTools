// Package fonts reports which 公文 typefaces are installed on the machine
// running GovDoc.
//
// # Why the user needs to know
//
// GB/T 9704-2012 names specific typefaces (仿宋_GB2312, 黑体, 楷体_GB2312,
// 方正小标宋简体). If one is missing, Word silently substitutes another face
// and the user only discovers it when the printed 公文 looks wrong. Reporting
// availability up front turns a silent substitution into a visible warning.
//
// # Why this is a heuristic, and says so
//
// Reading a font's real name means parsing its name table, and the standard
// library has no font parser or registry access. This package therefore
// matches *file names* in the platform's font directory. That can be wrong in
// both directions: a font installed per-user on Windows lives outside the
// system directory, and a file named simfang.ttf is assumed to be 仿宋. The
// API therefore reports the method used, and callers must present the result
// as an indication rather than a guarantee — the renderers fall back to a
// documented chain either way, so a false "missing" costs a warning, not a
// broken document.
package fonts

import (
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"

	"opentools/tools/govdoc/engine/docmodel"
	"opentools/tools/govdoc/engine/gbstandard"
)

// Status is one typeface's availability.
type Status struct {
	Name      string   `json:"name"`
	Available bool     `json:"available"`
	Fallbacks []string `json:"fallbacks"`
	// Used names the 公文 elements that need this typeface.
	Used []string `json:"used"`
	// MatchedFile is the file that satisfied the check, when one did.
	MatchedFile string `json:"matched_file,omitempty"`
	// Required notes whether a missing face changes the document's
	// appearance materially.
	Required bool `json:"required"`
}

// fontFiles maps each 公文 typeface to the file name stems that indicate it.
// Stems are lower-cased and compared without their extension.
var fontFiles = map[string][]string{
	gbstandard.FontFangSong:     {"simfang", "fangsong", "stfangso", "fzfs", "fzfangzhengfangsong"},
	gbstandard.FontHeiti:        {"simhei", "heiti", "sthei", "msyh"},
	gbstandard.FontKaiti:        {"simkai", "kaiti", "stkaiti", "fzkt"},
	gbstandard.FontXiaoBiaoSong: {"fzxbs", "fzxiaobiaosong", "xiaobiaosong", "stzhongs", "fzxbsjw"},
	gbstandard.FontSongti:       {"simsun", "songti", "stsong", "nsimsun", "song"},
}

// rolesUsing inverts the spec to list the 公文 elements that use a typeface.
// Deriving it from the spec (rather than hard-coding) keeps the report honest
// when a style's font is changed.
func rolesUsing(spec gbstandard.Spec, font string) []string {
	var out []string
	for _, role := range sortedRoles(spec) {
		if spec.Styles[role].Font == font {
			out = append(out, spec.Styles[role].Name)
		}
	}
	if spec.PageNumberFont == font {
		out = append(out, "页码")
	}
	return out
}

func sortedRoles(spec gbstandard.Spec) []docmodel.Role {
	out := make([]docmodel.Role, 0, len(spec.Styles))
	for role := range spec.Styles {
		out = append(out, role)
	}
	sort.Slice(out, func(i, j int) bool { return out[i] < out[j] })
	return out
}

// Check reports the availability of every typeface the spec names.
//
// spec is passed in rather than assuming Default() so that a user who changes
// a style's font gets a report about the fonts they actually use.
func Check(spec gbstandard.Spec) []Status {
	dirs := fontDirs()
	installed := scan(dirs)

	// Every face the spec uses, plus the ones the standard requires even if
	// the current spec does not reach them (so removing a style cannot hide a
	// missing font from the user).
	wanted := map[string]bool{}
	for _, role := range sortedRoles(spec) {
		if f := spec.Styles[role].Font; f != "" {
			wanted[f] = true
		}
	}
	if spec.PageNumberFont != "" {
		wanted[spec.PageNumberFont] = true
	}
	for _, f := range []string{
		gbstandard.FontFangSong, gbstandard.FontHeiti,
		gbstandard.FontKaiti, gbstandard.FontXiaoBiaoSong,
	} {
		wanted[f] = true
	}

	names := make([]string, 0, len(wanted))
	for f := range wanted {
		names = append(names, f)
	}
	sort.Strings(names)

	out := make([]Status, 0, len(names))
	for _, name := range names {
		st := Status{
			Name:      name,
			Fallbacks: gbstandard.FontChain(name),
			Used:      rolesUsing(spec, name),
		}
		// 仿宋 (body) and 小标宋 (title) define a 公文's look; 黑体/楷体
		// affect only headings, which is noticeable but not structural.
		st.Required = name == gbstandard.FontFangSong || name == gbstandard.FontXiaoBiaoSong
		if file, ok := match(name, installed); ok {
			st.Available = true
			st.MatchedFile = file
		}
		out = append(out, st)
	}
	return out
}

// Missing returns the names of typefaces that could not be found.
func Missing(spec gbstandard.Spec) []string {
	var out []string
	for _, st := range Check(spec) {
		if !st.Available {
			out = append(out, st.Name)
		}
	}
	return out
}

// fontDirs returns the directories worth scanning on this platform.
func fontDirs() []string {
	switch runtime.GOOS {
	case "windows":
		dirs := []string{}
		if root := os.Getenv("SystemRoot"); root != "" {
			dirs = append(dirs, filepath.Join(root, "Fonts"))
		}
		if win := os.Getenv("WINDIR"); win != "" {
			d := filepath.Join(win, "Fonts")
			if !contains(dirs, d) {
				dirs = append(dirs, d)
			}
		}
		// Per-user installs (no admin rights) land here.
		if local := os.Getenv("LOCALAPPDATA"); local != "" {
			dirs = append(dirs, filepath.Join(local, "Microsoft", "Windows", "Fonts"))
		}
		return dirs
	case "darwin":
		return []string{"/System/Library/Fonts", "/Library/Fonts",
			filepath.Join(homeDir(), "Library", "Fonts")}
	default:
		return []string{"/usr/share/fonts", "/usr/local/share/fonts",
			filepath.Join(homeDir(), ".fonts"), filepath.Join(homeDir(), ".local", "share", "fonts")}
	}
}

func homeDir() string {
	if h, err := os.UserHomeDir(); err == nil {
		return h
	}
	return ""
}

func contains(list []string, s string) bool {
	for _, v := range list {
		if v == s {
			return true
		}
	}
	return false
}

// scan collects the lower-cased file stems found in the font directories.
// Missing directories are skipped: an absent font directory is a normal
// situation, not an error.
func scan(dirs []string) map[string]string {
	found := map[string]string{}
	for _, dir := range dirs {
		entries, err := os.ReadDir(dir)
		if err != nil {
			continue
		}
		for _, e := range entries {
			if e.IsDir() {
				continue
			}
			name := strings.ToLower(e.Name())
			switch strings.ToLower(filepath.Ext(name)) {
			case ".ttf", ".ttc", ".otf", ".otc", ".fon":
			default:
				continue
			}
			stem := strings.TrimSuffix(name, filepath.Ext(name))
			// First match wins; the system directory is scanned before the
			// per-user one, which matches how the OS resolves them.
			if _, ok := found[stem]; !ok {
				found[stem] = filepath.Join(dir, e.Name())
			}
		}
	}
	return found
}

// match reports whether any file stem indicates the given typeface.
func match(font string, installed map[string]string) (string, bool) {
	stems, ok := fontFiles[font]
	if !ok {
		// Unknown face: try an exact stem match so a user-supplied font name
		// still gets a sensible answer.
		stems = []string{strings.ToLower(font)}
	}
	for _, stem := range stems {
		if file, ok := installed[stem]; ok {
			return file, true
		}
	}
	return "", false
}
