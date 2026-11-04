package config

import (
	"encoding/json"
	"fmt"
	"strings"

	"opentools/tools/govdoc/engine/gbstandard"
)

// Builtin 公文 profile id. This is the one the tool has always produced, and it
// stays selectable forever so an existing user never loses their output.
const ProfileGovDoc = "govdoc"

// Profile is one named formatting rule set.
//
// # Why the spec is stored as raw JSON
//
// A profile IS a gbstandard.Spec, and the obvious thing is to type the field as
// gbstandard.Spec. This package deliberately does not, for one reason: config
// is the lowest layer in the program (it must be readable before anything else
// has initialized), and making it depend on gbstandard would tie the storage
// format's compile-time shape to the rendering rules. Keeping it as
// json.RawMessage lets the file be read, listed and written even if a rendering
// field is renamed, with the parse deferred to where the spec is actually
// needed. The cost is one unmarshal call at the point of use.
type Profile struct {
	// ID is the stable key used by documents to remember their choice. It must
	// not change when the profile is renamed.
	ID string `json:"id"`
	// Name is what the UI shows.
	Name string `json:"name"`
	// Description is one line of guidance, e.g. which standard it follows.
	Description string `json:"description,omitempty"`
	// BuiltIn marks a seeded profile. Built-ins can be copied and tuned but not
	// deleted or edited, so a user always has a known-good reference to return
	// to — deleting the 公文 rules would strand every existing document.
	BuiltIn bool `json:"built_in,omitempty"`
	// Spec is the full formatting rule set: page geometry plus one style per
	// role. It round-trips gbstandard.Spec exactly.
	Spec json.RawMessage `json:"spec"`
}

// ProfileByID returns the profile with the given id.
func (f File) ProfileByID(id string) (Profile, bool) {
	for _, p := range f.Profiles {
		if p.ID == id {
			return p, true
		}
	}
	return Profile{}, false
}

// Decode parses the profile's rule set.
//
// It is a method rather than direct JSON access because the field is raw bytes:
// callers that want rules must ask for them explicitly, and get a named error
// when the stored data is unusable instead of an empty Spec that renders as
// something plausible.
func (p Profile) Decode() (gbstandard.Spec, error) {
	var s gbstandard.Spec
	if len(p.Spec) == 0 {
		return s, fmt.Errorf("配置 %q 没有规则数据", p.Name)
	}
	if err := json.Unmarshal(p.Spec, &s); err != nil {
		return s, fmt.Errorf("配置 %q 的规则无法解析：%w", p.Name, err)
	}
	return s, nil
}

// NewProfile builds a user profile from a spec.
func NewProfile(id, name, description string, spec gbstandard.Spec) (Profile, error) {
	raw, err := json.Marshal(spec)
	if err != nil {
		return Profile{}, err
	}
	return Profile{
		ID:          id,
		Name:        name,
		Description: description,
		Spec:        raw,
	}, nil
}

// ---------------------------------------------------------------------------
// Built-in profiles
// ---------------------------------------------------------------------------

// BuiltinProfiles returns the rule sets the tool ships with.
//
// 公文 (GB/T 9704-2012) is what this program was built for and is the default.
// The others exist because the user asked for the tool to be general: the same
// pipeline (structure a document, apply rules, export .docx) is useful well
// beyond 公文, and a shared page/paragraph model means those profiles need no
// new rendering code — only a different style table.
//
// They are deliberately modest and conventional rather than opinionated, so
// changing one number is enough to tune them.
func BuiltinProfiles() []Profile {
	var out []Profile
	add := func(id, name, desc string, s gbstandard.Spec) {
		p, err := NewProfile(id, name, desc, s)
		if err != nil {
			// Marshalling our own types cannot fail; skipping is still safer
			// than panicking during startup over a built-in list.
			return
		}
		p.BuiltIn = true
		out = append(out, p)
	}
	add(ProfileGovDoc, "公文", "GB/T 9704-2012 党政机关公文格式（默认）", gbstandard.Default())
	add("generic", "通用", "通用中文文档：A4、宋体小四、1.5 倍行距、首行缩进 2 字", gbstandard.Generic())
	add("paper", "论文", "学术论文：A4、宋体小四、固定行距 20 磅、标题分级加粗", gbstandard.Thesis())
	add("report", "报告", "工作报告/汇报材料：仿宋小三、行距 28 磅、标题黑体", gbstandard.Report())
	return out
}

// EnsureProfiles seeds built-ins into the stored list on first run and returns
// the file with a usable profile set.
//
// Two properties matter and are both deliberate:
//
//   - built-ins are re-added if missing, so upgrading the program can introduce
//     a new profile without disturbing the user's copies;
//   - a user copy that happens to reuse a built-in id is never overwritten, so
//     their tuning survives every startup.
func EnsureProfiles(f File) File {
	have := map[string]bool{}
	for _, p := range f.Profiles {
		have[p.ID] = true
	}
	for _, p := range BuiltinProfiles() {
		if !have[p.ID] {
			f.Profiles = append(f.Profiles, p)
		}
	}
	if f.ActiveProfile == "" || !haveProfile(f.Profiles, f.ActiveProfile) {
		f.ActiveProfile = ProfileGovDoc
	}
	return f
}

func haveProfile(list []Profile, id string) bool {
	for _, p := range list {
		if p.ID == id {
			return true
		}
	}
	return false
}

// SanitizeProfileID turns a user-supplied profile name into a stable key.
//
// # Why the key is kept even though ids are never shown
//
// Documents store the profile they were uploaded with. When a user renames a
// profile, the stored id must keep resolving — so the key cannot be derived from
// the name at read time, and it has to be reasonably stable on the way in too.
//
// # Why Chinese names are kept rather than transliterated
//
// A CJK name has no sensible romanisation, so the first version filtered it to
// nothing and every Chinese-named profile became "profile", "profile-2", …
// That is not merely ugly: it makes the settings file unreadable, and a user
// comparing two copies of it cannot tell which entry is which. Keeping the
// original characters costs nothing (JSON is UTF-8, the id never becomes a file
// name) and makes the file self-describing.
//
// A purely non-Latin name therefore keeps its own characters; only characters
// that would break a URL path or a query string are folded to '-', since ids
// travel in a REST path.
func SanitizeProfileID(name string, existing func(string) bool) string {
	if existing == nil {
		existing = func(string) bool { return false }
	}
	base := strings.TrimSpace(name)
	if base == "" {
		base = "profile"
	}

	var b strings.Builder
	for _, r := range base {
		switch {
		case r >= 'a' && r <= 'z':
			b.WriteRune(r)
		case r >= 'A' && r <= 'Z':
			b.WriteRune(r + ('a' - 'A')) // fold case inline
		case r >= '0' && r <= '9', r == '_':
			b.WriteRune(r)
		case r == ' ':
			// A space reads as a word break; '-' is the conventional separator.
			b.WriteByte('-')
		case r < 0x80:
			// Punctuation and control characters would need escaping in a path.
			b.WriteByte('-')
		default:
			// Non-ASCII letters are kept verbatim — including CJK, which is the
			// common case for this tool.
			b.WriteRune(r)
		}
	}

	id := strings.Trim(b.String(), "-")
	if id == "" {
		id = "profile"
	}
	candidate := id
	for i := 2; existing(candidate); i++ {
		candidate = fmt.Sprintf("%s-%d", id, i)
	}
	return candidate
}
