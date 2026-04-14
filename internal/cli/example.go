package cli

import (
	"fmt"
	"strings"

	"github.com/spf13/cobra"
	"github.com/tae2089/trace"
)

// exampleEntry holds annotation example snippets for a language.
type exampleEntry struct {
	commentPrefix string // e.g. "//" or "#"
	funcSnippet   string // a realistic function snippet
}

var languageExamples = map[string]exampleEntry{
	"go": {
		commentPrefix: "//",
		funcSnippet: `// @index 사용자 인증 패키지 — 로그인, 토큰 발급, 세션 관리를 담당한다.
package auth

// @intent 사용자의 로그인 요청을 처리하고 JWT 토큰을 반환한다.
//
// 레거시 인증 시스템과의 호환성을 위해 존재하며,
// 새 서비스에서는 AuthService.Login을 사용할 것.
//
// @domainRule 비밀번호는 bcrypt로 해시되어야 한다
// @param ctx  요청 컨텍스트
// @param req  로그인 요청 (이메일, 비밀번호 포함)
// @return     JWT 토큰과 만료 시각
// @requires   req.Email이 비어 있지 않아야 한다
// @ensures    반환된 토큰은 24시간 유효하다
// @sideEffect 로그인 이력을 DB에 기록한다
// @mutates    sessions 테이블
// @see        internal/auth/jwt.go::Sign
func Login(ctx context.Context, req *LoginRequest) (*TokenResponse, error) {
    // ...
}`,
	},
	"python": {
		commentPrefix: "#",
		funcSnippet: `# @index 사용자 인증 모듈 — 로그인, 토큰 발급, 세션 관리를 담당한다.

# @intent 사용자의 로그인 요청을 처리하고 JWT 토큰을 반환한다.
#
# 레거시 인증 시스템과의 호환성을 위해 존재하며,
# 새 서비스에서는 auth_service.login을 사용할 것.
#
# @domainRule 비밀번호는 bcrypt로 해시되어야 한다
# @param ctx  요청 컨텍스트
# @param req  로그인 요청 (이메일, 비밀번호 포함)
# @return     JWT 토큰과 만료 시각
# @requires   req["email"]이 비어 있지 않아야 한다
# @ensures    반환된 토큰은 24시간 유효하다
# @sideEffect 로그인 이력을 DB에 기록한다
# @mutates    sessions 테이블
# @see        auth/jwt.py::sign
def login(ctx, req):
    pass`,
	},
	"typescript": {
		commentPrefix: "//",
		funcSnippet: `// @index 사용자 인증 모듈 — 로그인, 토큰 발급, 세션 관리를 담당한다.

// @intent 사용자의 로그인 요청을 처리하고 JWT 토큰을 반환한다.
//
// 레거시 인증 시스템과의 호환성을 위해 존재하며,
// 새 서비스에서는 AuthService.login을 사용할 것.
//
// @domainRule 비밀번호는 bcrypt로 해시되어야 한다
// @param ctx  요청 컨텍스트
// @param req  로그인 요청 (이메일, 비밀번호 포함)
// @return     JWT 토큰과 만료 시각
// @requires   req.email이 비어 있지 않아야 한다
// @ensures    반환된 토큰은 24시간 유효하다
// @sideEffect 로그인 이력을 DB에 기록한다
// @mutates    sessions 테이블
// @see        auth/jwt.ts::sign
async function login(ctx: Context, req: LoginRequest): Promise<TokenResponse> {
    // ...
}`,
	},
	"javascript": {
		commentPrefix: "//",
		funcSnippet: `// @index 사용자 인증 모듈 — 로그인, 토큰 발급, 세션 관리를 담당한다.

// @intent 사용자의 로그인 요청을 처리하고 JWT 토큰을 반환한다.
//
// @domainRule 비밀번호는 bcrypt로 해시되어야 한다
// @param ctx  요청 컨텍스트
// @param req  로그인 요청 (이메일, 비밀번호 포함)
// @return     JWT 토큰과 만료 시각
// @requires   req.email이 비어 있지 않아야 한다
// @ensures    반환된 토큰은 24시간 유효하다
// @sideEffect 로그인 이력을 DB에 기록한다
// @mutates    sessions 테이블
// @see        auth/jwt.js::sign
async function login(ctx, req) {
    // ...
}`,
	},
	"java": {
		commentPrefix: "//",
		funcSnippet: `// @index 사용자 인증 패키지 — 로그인, 토큰 발급, 세션 관리를 담당한다.

// @intent 사용자의 로그인 요청을 처리하고 JWT 토큰을 반환한다.
//
// 레거시 인증 시스템과의 호환성을 위해 존재하며,
// 새 서비스에서는 AuthService.login을 사용할 것.
//
// @domainRule 비밀번호는 bcrypt로 해시되어야 한다
// @param ctx  요청 컨텍스트
// @param req  로그인 요청 (이메일, 비밀번호 포함)
// @return     JWT 토큰과 만료 시각
// @requires   req.getEmail()이 비어 있지 않아야 한다
// @ensures    반환된 토큰은 24시간 유효하다
// @sideEffect 로그인 이력을 DB에 기록한다
// @mutates    sessions 테이블
// @see        auth/JwtUtils.java::sign
public TokenResponse login(Context ctx, LoginRequest req) {
    // ...
}`,
	},
	"kotlin": {
		commentPrefix: "//",
		funcSnippet: `// @index 사용자 인증 패키지 — 로그인, 토큰 발급, 세션 관리를 담당한다.

// @intent 사용자의 로그인 요청을 처리하고 JWT 토큰을 반환한다.
//
// @domainRule 비밀번호는 bcrypt로 해시되어야 한다
// @param ctx  요청 컨텍스트
// @param req  로그인 요청 (이메일, 비밀번호 포함)
// @return     JWT 토큰과 만료 시각
// @requires   req.email이 비어 있지 않아야 한다
// @ensures    반환된 토큰은 24시간 유효하다
// @sideEffect 로그인 이력을 DB에 기록한다
// @mutates    sessions 테이블
// @see        auth/JwtUtils.kt::sign
fun login(ctx: Context, req: LoginRequest): TokenResponse {
    // ...
}`,
	},
	"rust": {
		commentPrefix: "//",
		funcSnippet: `// @index 사용자 인증 모듈 — 로그인, 토큰 발급, 세션 관리를 담당한다.

// @intent 사용자의 로그인 요청을 처리하고 JWT 토큰을 반환한다.
//
// @domainRule 비밀번호는 bcrypt로 해시되어야 한다
// @param ctx  요청 컨텍스트
// @param req  로그인 요청 (이메일, 비밀번호 포함)
// @return     JWT 토큰과 만료 시각
// @requires   req.email이 비어 있지 않아야 한다
// @ensures    반환된 토큰은 24시간 유효하다
// @sideEffect 로그인 이력을 DB에 기록한다
// @mutates    sessions 테이블
// @see        auth/jwt.rs::sign
pub fn login(ctx: &Context, req: &LoginRequest) -> Result<TokenResponse, AuthError> {
    // ...
}`,
	},
	"ruby": {
		commentPrefix: "#",
		funcSnippet: `# @index 사용자 인증 모듈 — 로그인, 토큰 발급, 세션 관리를 담당한다.

# @intent 사용자의 로그인 요청을 처리하고 JWT 토큰을 반환한다.
#
# @domainRule 비밀번호는 bcrypt로 해시되어야 한다
# @param ctx  요청 컨텍스트
# @param req  로그인 요청 (이메일, 비밀번호 포함)
# @return     JWT 토큰과 만료 시각
# @requires   req[:email]이 비어 있지 않아야 한다
# @ensures    반환된 토큰은 24시간 유효하다
# @sideEffect 로그인 이력을 DB에 기록한다
# @mutates    sessions 테이블
# @see        auth/jwt.rb::sign
def login(ctx, req)
  # ...
end`,
	},
	"c": {
		commentPrefix: "//",
		funcSnippet: `// @index 사용자 인증 모듈 — 로그인, 토큰 발급, 세션 관리를 담당한다.

// @intent 사용자의 로그인 요청을 처리하고 JWT 토큰을 반환한다.
//
// @domainRule 비밀번호는 bcrypt로 해시되어야 한다
// @param ctx  요청 컨텍스트
// @param req  로그인 요청 (이메일, 비밀번호 포함)
// @return     0: 성공, -1: 실패
// @requires   req->email이 NULL이 아니어야 한다
// @ensures    반환 시 token에 유효한 JWT가 채워진다
// @sideEffect 로그인 이력을 DB에 기록한다
// @see        auth/jwt.h::jwt_sign
int login(Context *ctx, LoginRequest *req, char *token) {
    // ...
}`,
	},
	"cpp": {
		commentPrefix: "//",
		funcSnippet: `// @index 사용자 인증 모듈 — 로그인, 토큰 발급, 세션 관리를 담당한다.

// @intent 사용자의 로그인 요청을 처리하고 JWT 토큰을 반환한다.
//
// @domainRule 비밀번호는 bcrypt로 해시되어야 한다
// @param ctx  요청 컨텍스트
// @param req  로그인 요청 (이메일, 비밀번호 포함)
// @return     JWT 토큰
// @requires   req.email이 비어 있지 않아야 한다
// @ensures    반환된 토큰은 24시간 유효하다
// @sideEffect 로그인 이력을 DB에 기록한다
// @see        auth/jwt.hpp::sign
std::string login(Context& ctx, const LoginRequest& req) {
    // ...
}`,
	},
	"csharp": {
		commentPrefix: "//",
		funcSnippet: `// @index 사용자 인증 패키지 — 로그인, 토큰 발급, 세션 관리를 담당한다.

// @intent 사용자의 로그인 요청을 처리하고 JWT 토큰을 반환한다.
//
// @domainRule 비밀번호는 bcrypt로 해시되어야 한다
// @param ctx  요청 컨텍스트
// @param req  로그인 요청 (이메일, 비밀번호 포함)
// @return     JWT 토큰과 만료 시각
// @requires   req.Email이 비어 있지 않아야 한다
// @ensures    반환된 토큰은 24시간 유효하다
// @sideEffect 로그인 이력을 DB에 기록한다
// @see        Auth/JwtUtils.cs::Sign
public TokenResponse Login(Context ctx, LoginRequest req) {
    // ...
}`,
	},
	"php": {
		commentPrefix: "//",
		funcSnippet: `// @index 사용자 인증 모듈 — 로그인, 토큰 발급, 세션 관리를 담당한다.

// @intent 사용자의 로그인 요청을 처리하고 JWT 토큰을 반환한다.
//
// @domainRule 비밀번호는 bcrypt로 해시되어야 한다
// @param ctx  요청 컨텍스트
// @param req  로그인 요청 (이메일, 비밀번호 포함)
// @return     JWT 토큰과 만료 시각
// @requires   $req["email"]이 비어 있지 않아야 한다
// @ensures    반환된 토큰은 24시간 유효하다
// @sideEffect 로그인 이력을 DB에 기록한다
// @see        auth/jwt.php::sign
function login($ctx, $req): array {
    // ...
}`,
	},
	"swift": {
		commentPrefix: "//",
		funcSnippet: `// @index 사용자 인증 모듈 — 로그인, 토큰 발급, 세션 관리를 담당한다.

// @intent 사용자의 로그인 요청을 처리하고 JWT 토큰을 반환한다.
//
// @domainRule 비밀번호는 bcrypt로 해시되어야 한다
// @param ctx  요청 컨텍스트
// @param req  로그인 요청 (이메일, 비밀번호 포함)
// @return     JWT 토큰과 만료 시각
// @requires   req.email이 비어 있지 않아야 한다
// @ensures    반환된 토큰은 24시간 유효하다
// @sideEffect 로그인 이력을 DB에 기록한다
// @see        Auth/JWT.swift::sign
func login(ctx: Context, req: LoginRequest) throws -> TokenResponse {
    // ...
}`,
	},
	"scala": {
		commentPrefix: "//",
		funcSnippet: `// @index 사용자 인증 패키지 — 로그인, 토큰 발급, 세션 관리를 담당한다.

// @intent 사용자의 로그인 요청을 처리하고 JWT 토큰을 반환한다.
//
// @domainRule 비밀번호는 bcrypt로 해시되어야 한다
// @param ctx  요청 컨텍스트
// @param req  로그인 요청 (이메일, 비밀번호 포함)
// @return     JWT 토큰과 만료 시각
// @requires   req.email이 비어 있지 않아야 한다
// @ensures    반환된 토큰은 24시간 유효하다
// @sideEffect 로그인 이력을 DB에 기록한다
// @see        auth/JwtUtils.scala::sign
def login(ctx: Context, req: LoginRequest): TokenResponse = {
    ???
}`,
	},
	"lua": {
		commentPrefix: "--",
		funcSnippet: `-- @index 사용자 인증 모듈 — 로그인, 토큰 발급, 세션 관리를 담당한다.

-- @intent 사용자의 로그인 요청을 처리하고 JWT 토큰을 반환한다.
--
-- @domainRule 비밀번호는 bcrypt로 해시되어야 한다
-- @param ctx  요청 컨텍스트
-- @param req  로그인 요청 (이메일, 비밀번호 포함)
-- @return     JWT 토큰과 만료 시각
-- @requires   req.email이 비어 있지 않아야 한다
-- @ensures    반환된 토큰은 24시간 유효하다
-- @sideEffect 로그인 이력을 DB에 기록한다
-- @see        auth/jwt.lua::sign
local function login(ctx, req)
    -- ...
end`,
	},
	"bash": {
		commentPrefix: "#",
		funcSnippet: `# @index 배포 유틸리티 — 빌드, 테스트, 배포 스크립트 모음.

# @intent 애플리케이션을 빌드하고 지정된 환경에 배포한다.
#
# @domainRule 프로덕션 배포는 main 브랜치에서만 허용된다
# @param ENV   배포 환경 (dev, staging, prod)
# @param TAG   배포할 Docker 이미지 태그
# @return      0: 성공, 1: 실패
# @requires    ENV가 비어 있지 않아야 한다
# @ensures     배포 후 헬스체크가 통과해야 한다
# @sideEffect  배포 로그를 /var/log/deploy.log에 기록한다
# @see         scripts/health_check.sh::check
deploy() {
    local ENV="$1"
    local TAG="$2"
    # ...
}`,
	},
}

func newExampleCmd(_ *Deps) *cobra.Command {
	return &cobra.Command{
		Use:   "example [language]",
		Short: "Show annotation writing examples for a given language",
		Long: `Show annotation writing examples for a given language.

Defaults to "go" when no language is specified.

Supported languages: bash, c, cpp, csharp, go, java, javascript, kotlin,
lua, php, python, ruby, rust, scala, swift, typescript`,
		Args: cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			lang := "go"
			if len(args) > 0 {
				lang = strings.ToLower(args[0])
			}

			entry, ok := languageExamples[lang]
			if !ok {
				return trace.New(fmt.Sprintf("unsupported language: %q (run 'ccg languages' for a list)", lang))
			}

			out := stdout(cmd)
			fmt.Fprintf(out, "Annotation example for %s (comment prefix: %s):\n\n", lang, entry.commentPrefix)
			fmt.Fprintln(out, entry.funcSnippet)
			return nil
		},
	}
}
