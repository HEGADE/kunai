package worktree

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
)

// ProjectFile is the name of the optional, checked-in file a repository can use
// to describe how to prepare a worktree of itself. Keeping it in the repository
// rather than in kunai's data directory is deliberate: the setup then travels
// with the project, so every machine and every person gets the same one without
// configuring anything. t3code's t3.json works this way and it is the right call.
const ProjectFile = "kunai.json"

// Project is the checked-in per-repository configuration. It is intentionally
// tiny; anything that is really a personal preference belongs in kunai's own
// settings, not in a file the whole team shares.
type Project struct {
	// Setup is the shell command run in a new worktree of this repository.
	Setup string `json:"setup,omitempty"`
}

// LoadProject reads the repository's kunai.json. A missing or malformed file is
// not an error: the feature has to work in a repository that has never heard of
// kunai, which is every repository the first time.
func LoadProject(repo string) (Project, bool) {
	b, err := os.ReadFile(filepath.Join(repo, ProjectFile))
	if err != nil {
		return Project{}, false
	}
	var p Project
	if err := json.Unmarshal(b, &p); err != nil {
		return Project{}, false
	}
	p.Setup = strings.TrimSpace(p.Setup)
	if p.Setup == "" {
		return Project{}, false
	}
	return p, true
}

// SetupSource says where a setup command came from, so the UI can be honest
// about whether it is running something the project declared or something kunai
// guessed and the user accepted.
type SetupSource string

const (
	// SourceProject is the repository's own kunai.json.
	SourceProject SetupSource = "project"
	// SourceSuggested is inferred from the lockfiles present. It is only ever a
	// proposal: kunai shows it and runs it once the user has accepted it, and
	// never infers-and-executes silently, because this is arbitrary shell run
	// with the server's privileges.
	SourceSuggested SetupSource = "suggested"
	// SourceNone means nothing was found to suggest.
	SourceNone SetupSource = "none"
)

// SetupProposal is what to show a user before a worktree is created.
type SetupProposal struct {
	Command string      `json:"command"`
	Source  SetupSource `json:"source"`
	// Why names the evidence, e.g. "pnpm-lock.yaml", so a suggestion can be
	// judged rather than merely trusted.
	Why string `json:"why,omitempty"`
}

// installRule maps a marker file to the install command for its ecosystem.
// Ordered: the first match wins, and lockfiles come before manifests because a
// lockfile says which tool is actually in use.
var installRules = []struct {
	marker  string
	command string
}{
	{"pnpm-lock.yaml", "pnpm install --frozen-lockfile"},
	{"bun.lockb", "bun install --frozen-lockfile"},
	{"yarn.lock", "yarn install --immutable"},
	{"package-lock.json", "npm ci"},
	{"uv.lock", "uv sync"},
	{"poetry.lock", "poetry install"},
	{"Gemfile.lock", "bundle install"},
	{"go.sum", "go mod download"},
}

// carryFiles are untracked, repository-local files a worktree almost always
// needs and can safely share with the main checkout: small, read-mostly, and
// meaningless to install. A dependency tree is deliberately not in this list.
var carryFiles = []string{".env", ".env.local", ".npmrc", ".tool-versions"}

// ProposeSetup returns the setup command for a repository: the one it declares,
// or one inferred from what is on disk for the user to look at and accept.
func ProposeSetup(repo string) SetupProposal {
	if p, ok := LoadProject(repo); ok {
		return SetupProposal{Command: p.Setup, Source: SourceProject, Why: ProjectFile}
	}
	return suggestSetup(repo)
}

func suggestSetup(repo string) SetupProposal {
	var (
		parts []string
		why   []string
	)
	for _, rule := range installRules {
		if exists(filepath.Join(repo, rule.marker)) {
			parts = append(parts, rule.command)
			why = append(why, rule.marker)
			break
		}
	}
	for _, name := range carryFiles {
		if exists(filepath.Join(repo, name)) && ignored(repo, name) {
			parts = append(parts, "ln -sf \"$"+EnvProjectRoot+"/"+name+"\" "+name)
			why = append(why, name)
		}
	}
	if len(parts) == 0 {
		return SetupProposal{Source: SourceNone}
	}
	return SetupProposal{
		Command: strings.Join(parts, " && "),
		Source:  SourceSuggested,
		Why:     strings.Join(why, ", "),
	}
}

// ignored reports whether git ignores a path. Only an ignored file is worth
// carrying: a tracked one is already in the worktree, and linking over it would
// replace real content with a link to the main checkout.
func ignored(repo, name string) bool {
	_, code, _ := gitCode(repo, "check-ignore", "-q", name)
	return code == 0
}

func exists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}
