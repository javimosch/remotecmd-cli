package main

import (
	_ "embed"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
)

// `guide` (cli-guide-spec): the mental model, embedded in the binary so it
// works offline and always matches the build. JSON agent-skill flavor by
// default; --human renders the same data as markdown so the two can't drift.

//go:embed guide.json
var guideJSON []byte

type guideExample struct {
	Goal string   `json:"goal"`
	Do   []string `json:"do"`
}

type guideBody struct {
	Tool     string              `json:"remotecmd-cli"`
	OneLiner string              `json:"one_liner"`
	Model    map[string]string   `json:"model"`
	Loop     []string            `json:"loop"`
	Concepts map[string]string   `json:"concepts"`
	Commands map[string][]string `json:"commands"`
	Examples []guideExample      `json:"examples"`
	Gotchas  []string            `json:"gotchas"`
	Version  string              `json:"version"`
	SeeAlso  []string            `json:"see_also"`
}

func loadGuide() (guideBody, error) {
	var g guideBody
	if err := json.Unmarshal(guideJSON, &g); err != nil {
		return g, err
	}
	g.Version = Version
	return g, nil
}

func handleGuide(args []string) {
	human := false
	for _, a := range args {
		switch a {
		case "--human":
			human = true
		case "--json":
		default:
			fail(ExitConfigError, "invalid_arguments", "unknown guide flag: "+a, "remotecmd-cli guide [--human]")
		}
	}
	g, err := loadGuide()
	if err != nil {
		fail(ExitInternal, "internal", "embedded guide is invalid: "+err.Error())
	}
	if human {
		fmt.Print(renderGuideMarkdown(g))
		return
	}
	b, _ := json.MarshalIndent(struct {
		OK      bool      `json:"ok"`
		Version string    `json:"version"`
		Guide   guideBody `json:"guide"`
	}{true, Version, g}, "", "  ")
	fmt.Println(string(b))
}

func renderGuideMarkdown(g guideBody) string {
	var b strings.Builder
	fmt.Fprintf(&b, "# remotecmd-cli guide (v%s)\n\n%s\n\n%s\n", g.Version, g.Tool, g.OneLiner)

	b.WriteString("\n## Model\n\n")
	for _, k := range sortedKeys(g.Model) {
		fmt.Fprintf(&b, "- **%s**: %s\n", k, g.Model[k])
	}
	b.WriteString("\n## Loop\n\n")
	for i, s := range g.Loop {
		fmt.Fprintf(&b, "%d. %s\n", i+1, s)
	}
	b.WriteString("\n## Concepts\n\n")
	for _, k := range sortedKeys(g.Concepts) {
		fmt.Fprintf(&b, "- **%s**: %s\n", k, g.Concepts[k])
	}
	b.WriteString("\n## Commands\n")
	groups := make([]string, 0, len(g.Commands))
	for k := range g.Commands {
		groups = append(groups, k)
	}
	sort.Strings(groups)
	for _, grp := range groups {
		fmt.Fprintf(&b, "\n### %s\n\n", grp)
		for _, c := range g.Commands[grp] {
			fmt.Fprintf(&b, "    %s\n", c)
		}
	}
	b.WriteString("\n## Examples\n")
	for _, e := range g.Examples {
		fmt.Fprintf(&b, "\n**%s**\n\n", e.Goal)
		for _, c := range e.Do {
			fmt.Fprintf(&b, "    %s\n", c)
		}
	}
	b.WriteString("\n## Gotchas\n\n")
	for _, s := range g.Gotchas {
		fmt.Fprintf(&b, "- %s\n", s)
	}
	if len(g.SeeAlso) > 0 {
		fmt.Fprintf(&b, "\nSee also: %s\n", strings.Join(g.SeeAlso, ", "))
	}
	return b.String()
}

func sortedKeys(m map[string]string) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}
