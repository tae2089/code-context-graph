package cli

import (
	"fmt"
	"sort"
	"strings"

	"github.com/spf13/cobra"

	"github.com/imtaebin/code-context-graph/internal/parse/treesitter"
)

func newLanguagesCmd(deps *Deps) *cobra.Command {
	return &cobra.Command{
		Use:   "languages",
		Short: "List all supported languages and their file extensions",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			out := stdout(cmd)
			langs := collectLanguages(deps.Walkers)

			fmt.Fprintln(out, "Supported languages:")
			fmt.Fprintln(out)
			fmt.Fprintf(out, "  %-16s %s\n", "Language", "Extensions")
			fmt.Fprintf(out, "  %-16s %s\n", strings.Repeat("-", 16), strings.Repeat("-", 30))
			for _, l := range langs {
				sort.Strings(l.Exts)
				fmt.Fprintf(out, "  %-16s %s\n", l.Name, strings.Join(l.Exts, "  "))
			}
			fmt.Fprintf(out, "\nTotal: %d languages\n", len(langs))
			return nil
		},
	}
}

type langInfo struct {
	Name string
	Exts []string
}

func collectLanguages(walkers map[string]*treesitter.Walker) []langInfo {
	byName := map[string]*langInfo{}
	for ext, w := range walkers {
		name := w.Language()
		if _, ok := byName[name]; !ok {
			byName[name] = &langInfo{Name: name}
		}
		byName[name].Exts = append(byName[name].Exts, ext)
	}
	result := make([]langInfo, 0, len(byName))
	for _, l := range byName {
		result = append(result, *l)
	}
	sort.Slice(result, func(i, j int) bool { return result[i].Name < result[j].Name })
	return result
}
