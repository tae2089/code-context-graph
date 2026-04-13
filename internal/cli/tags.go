package cli

import (
	"fmt"

	"github.com/spf13/cobra"
)

type tagDoc struct {
	tag     string
	scope   string
	desc    string
	example string
}

var annotationTags = []tagDoc{
	{
		tag:     "@index",
		scope:   "file",
		desc:    "파일/패키지 수준 설명. 해당 파일이 담당하는 역할과 책임을 한 줄로 요약한다.",
		example: `// @index 사용자 인증 패키지 — 로그인, 토큰 발급, 세션 관리를 담당한다.`,
	},
	{
		tag:     "@intent",
		scope:   "function/method",
		desc:    "함수/메서드의 목적과 의도를 설명한다. '무엇을 하는가'가 아닌 '왜 존재하는가'를 기술한다.",
		example: `// @intent 사용자의 로그인 요청을 처리하고 JWT 토큰을 반환한다.`,
	},
	{
		tag:     "@domainRule",
		scope:   "function/method",
		desc:    "이 함수가 구현하거나 의존하는 비즈니스/도메인 규칙. 여러 개 사용 가능.",
		example: `// @domainRule 비밀번호는 bcrypt로 해시되어야 한다`,
	},
	{
		tag:     "@sideEffect",
		scope:   "function/method",
		desc:    "함수 호출 시 발생하는 눈에 보이지 않는 부작용 (외부 I/O, 이벤트 발행 등).",
		example: `// @sideEffect 로그인 이력을 DB에 기록한다`,
	},
	{
		tag:     "@mutates",
		scope:   "function/method",
		desc:    "함수가 직접 수정하는 데이터 저장소, 테이블, 또는 객체를 명시한다.",
		example: `// @mutates sessions 테이블`,
	},
	{
		tag:     "@requires",
		scope:   "function/method",
		desc:    "함수 호출 전에 반드시 만족해야 하는 사전 조건 (pre-condition).",
		example: `// @requires req.Email이 비어 있지 않아야 한다`,
	},
	{
		tag:     "@ensures",
		scope:   "function/method",
		desc:    "함수가 정상 반환될 때 반드시 보장하는 사후 조건 (post-condition).",
		example: `// @ensures 반환된 토큰은 24시간 유효하다`,
	},
	{
		tag:     "@param",
		scope:   "function/method",
		desc:    "파라미터 설명. 형식: @param <name> <description>",
		example: `// @param req  로그인 요청 (이메일, 비밀번호 포함)`,
	},
	{
		tag:     "@return",
		scope:   "function/method",
		desc:    "반환값 설명.",
		example: `// @return JWT 토큰과 만료 시각`,
	},
	{
		tag:     "@see",
		scope:   "function/method",
		desc:    "관련 심볼 참조. 형식: <file>::<symbol>",
		example: `// @see internal/auth/jwt.go::Sign`,
	},
}

func newTagsCmd(_ *Deps) *cobra.Command {
	return &cobra.Command{
		Use:   "tags",
		Short: "Show all available annotation tags with descriptions",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			out := stdout(cmd)
			fmt.Fprintln(out, "Annotation Tags Reference")
			fmt.Fprintln(out, "=========================")
			fmt.Fprintln(out)

			for _, t := range annotationTags {
				fmt.Fprintf(out, "%-16s [%s]\n", t.tag, t.scope)
				fmt.Fprintf(out, "  %s\n", t.desc)
				fmt.Fprintf(out, "  Example: %s\n", t.example)
				fmt.Fprintln(out)
			}

			fmt.Fprintln(out, "Multiline support:")
			fmt.Fprintln(out, "  Summary and Context support multiple lines. A blank comment line separates")
			fmt.Fprintln(out, "  the Summary (first paragraph) from the Context (second paragraph).")
			fmt.Fprintln(out, "  Tag values also continue on the next line if the line starts without @.")
			return nil
		},
	}
}
