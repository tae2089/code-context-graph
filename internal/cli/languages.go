package cli

import (
	"fmt"
	"sort"
	"strings"

	"github.com/spf13/cobra"

	"github.com/imtaebin/code-context-graph/internal/parse/treesitter"
)

// newLanguagesCmd creates the supported-languages listing command.
// @intent 등록된 Tree-sitter 워커를 언어별 확장자 표로 보여준다.
// @sideEffect 지원 언어 목록을 표준 출력으로 기록한다.
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

// langInfo groups one language with all registered file extensions.
// @intent 출력 단계에서 언어 이름과 확장자 목록을 함께 다루기 쉽게 만든다.
type langInfo struct {
	Name string
	Exts []string
}

// collectLanguages collapses walkers into language rows sorted by name.
// @intent 확장자별 워커 맵을 사람이 읽기 쉬운 언어 단위 목록으로 정규화한다.
// @return 동일 언어의 확장자가 합쳐진 정렬된 목록을 반환한다.
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
