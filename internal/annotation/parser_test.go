package annotation

import (
	"testing"

	"github.com/tae2089/code-context-graph/internal/model"
)

func TestParse_EmptyString(t *testing.T) {
	p := NewParser()
	ann, _ := p.Parse("")
	if ann.Summary != "" {
		t.Errorf("expected empty Summary, got %q", ann.Summary)
	}
	if ann.Context != "" {
		t.Errorf("expected empty Context, got %q", ann.Context)
	}
	if len(ann.Tags) != 0 {
		t.Errorf("expected 0 tags, got %d", len(ann.Tags))
	}
}

func TestParse_WhitespaceOnly(t *testing.T) {
	p := NewParser()
	ann, _ := p.Parse("   \n\t\n  ")
	if ann.Summary != "" {
		t.Errorf("expected empty Summary, got %q", ann.Summary)
	}
	if ann.Context != "" {
		t.Errorf("expected empty Context, got %q", ann.Context)
	}
	if len(ann.Tags) != 0 {
		t.Errorf("expected 0 tags, got %d", len(ann.Tags))
	}
}

func TestParse_SummaryOnly(t *testing.T) {
	p := NewParser()
	ann, _ := p.Parse("사용자 인증을 수행한다")
	if ann.Summary != "사용자 인증을 수행한다" {
		t.Errorf("Summary = %q, want %q", ann.Summary, "사용자 인증을 수행한다")
	}
	if ann.Context != "" {
		t.Errorf("Context = %q, want empty", ann.Context)
	}
}

func TestParse_SummaryAndContext(t *testing.T) {
	p := NewParser()
	// 빈 줄 없이 이어진 두 줄은 같은 단락 → Summary 하나로 합쳐짐
	ann, _ := p.Parse("사용자 인증을 수행한다\n로그인 핸들러에서 호출됨")
	if ann.Summary != "사용자 인증을 수행한다\n로그인 핸들러에서 호출됨" {
		t.Errorf("Summary = %q, want multiline summary", ann.Summary)
	}
	if ann.Context != "" {
		t.Errorf("Context = %q, want empty", ann.Context)
	}
}

func TestParse_SummaryAndContext_WithBlankLine(t *testing.T) {
	p := NewParser()
	ann, _ := p.Parse("사용자 인증을 수행한다\n\n로그인 핸들러에서 호출됨")
	if ann.Summary != "사용자 인증을 수행한다" {
		t.Errorf("Summary = %q, want %q", ann.Summary, "사용자 인증을 수행한다")
	}
	if ann.Context != "로그인 핸들러에서 호출됨" {
		t.Errorf("Context = %q, want %q", ann.Context, "로그인 핸들러에서 호출됨")
	}
}

func TestParse_MultilineSummary(t *testing.T) {
	p := NewParser()
	// 빈 줄 없이 이어진 여러 줄은 하나의 Summary 단락
	ann, _ := p.Parse("첫째 줄\n둘째 줄\n셋째 줄")
	if ann.Summary != "첫째 줄\n둘째 줄\n셋째 줄" {
		t.Errorf("Summary = %q, want multiline", ann.Summary)
	}
	if ann.Context != "" {
		t.Errorf("Context = %q, want empty", ann.Context)
	}
}

func TestParse_MultilineContext(t *testing.T) {
	p := NewParser()
	// 빈 줄로 구분된 두 단락: 첫 단락 → Summary, 둘째 단락 → Context
	input := "요약 첫째 줄\n요약 둘째 줄\n\n컨텍스트 첫째 줄\n컨텍스트 둘째 줄"
	ann, _ := p.Parse(input)
	if ann.Summary != "요약 첫째 줄\n요약 둘째 줄" {
		t.Errorf("Summary = %q, want multiline summary", ann.Summary)
	}
	if ann.Context != "컨텍스트 첫째 줄\n컨텍스트 둘째 줄" {
		t.Errorf("Context = %q, want multiline context", ann.Context)
	}
}

func TestParse_SingleParam(t *testing.T) {
	p := NewParser()
	ann, _ := p.Parse("@param username 사용자 로그인 ID")
	if len(ann.Tags) != 1 {
		t.Fatalf("expected 1 tag, got %d", len(ann.Tags))
	}
	tag := ann.Tags[0]
	if tag.Kind != model.TagParam {
		t.Errorf("Kind = %q, want %q", tag.Kind, model.TagParam)
	}
	if tag.Name != "username" {
		t.Errorf("Name = %q, want %q", tag.Name, "username")
	}
	if tag.Value != "사용자 로그인 ID" {
		t.Errorf("Value = %q, want %q", tag.Value, "사용자 로그인 ID")
	}
	if tag.Ordinal != 0 {
		t.Errorf("Ordinal = %d, want 0", tag.Ordinal)
	}
}

func TestParse_MultipleParams(t *testing.T) {
	p := NewParser()
	ann, _ := p.Parse("@param username 사용자 ID\n@param password 비밀번호")
	if len(ann.Tags) != 2 {
		t.Fatalf("expected 2 tags, got %d", len(ann.Tags))
	}
	if ann.Tags[0].Name != "username" || ann.Tags[0].Ordinal != 0 {
		t.Errorf("first param: Name=%q Ordinal=%d", ann.Tags[0].Name, ann.Tags[0].Ordinal)
	}
	if ann.Tags[1].Name != "password" || ann.Tags[1].Ordinal != 1 {
		t.Errorf("second param: Name=%q Ordinal=%d", ann.Tags[1].Name, ann.Tags[1].Ordinal)
	}
}

func TestParse_Return(t *testing.T) {
	p := NewParser()
	ann, _ := p.Parse("@return 인증된 사용자 토큰")
	if len(ann.Tags) != 1 {
		t.Fatalf("expected 1 tag, got %d", len(ann.Tags))
	}
	tag := ann.Tags[0]
	if tag.Kind != model.TagReturn {
		t.Errorf("Kind = %q, want %q", tag.Kind, model.TagReturn)
	}
	if tag.Name != "" {
		t.Errorf("Name = %q, want empty", tag.Name)
	}
	if tag.Value != "인증된 사용자 토큰" {
		t.Errorf("Value = %q, want %q", tag.Value, "인증된 사용자 토큰")
	}
}

func TestParse_See(t *testing.T) {
	p := NewParser()
	ann, _ := p.Parse("@see LoginHandler.Handle")
	if len(ann.Tags) != 1 {
		t.Fatalf("expected 1 tag, got %d", len(ann.Tags))
	}
	if ann.Tags[0].Kind != model.TagSee {
		t.Errorf("Kind = %q, want %q", ann.Tags[0].Kind, model.TagSee)
	}
	if ann.Tags[0].Value != "LoginHandler.Handle" {
		t.Errorf("Value = %q, want %q", ann.Tags[0].Value, "LoginHandler.Handle")
	}
}

func TestParse_MultipleSee(t *testing.T) {
	p := NewParser()
	ann, _ := p.Parse("@see LoginHandler.Handle\n@see SessionManager.Create")
	if len(ann.Tags) != 2 {
		t.Fatalf("expected 2 tags, got %d", len(ann.Tags))
	}
	if ann.Tags[0].Value != "LoginHandler.Handle" {
		t.Errorf("first see Value = %q", ann.Tags[0].Value)
	}
	if ann.Tags[1].Value != "SessionManager.Create" {
		t.Errorf("second see Value = %q", ann.Tags[1].Value)
	}
}

func TestParse_Intent(t *testing.T) {
	p := NewParser()
	ann, _ := p.Parse("@intent 사용자 세션 생성 전 자격 검증")
	if len(ann.Tags) != 1 {
		t.Fatalf("expected 1 tag, got %d", len(ann.Tags))
	}
	if ann.Tags[0].Kind != model.TagIntent {
		t.Errorf("Kind = %q, want %q", ann.Tags[0].Kind, model.TagIntent)
	}
	if ann.Tags[0].Value != "사용자 세션 생성 전 자격 검증" {
		t.Errorf("Value = %q", ann.Tags[0].Value)
	}
}

func TestParse_DomainRule(t *testing.T) {
	p := NewParser()
	ann, _ := p.Parse("@domainRule 5회 실패 시 계정 잠금")
	if len(ann.Tags) != 1 {
		t.Fatalf("expected 1 tag, got %d", len(ann.Tags))
	}
	if ann.Tags[0].Kind != model.TagDomainRule {
		t.Errorf("Kind = %q, want %q", ann.Tags[0].Kind, model.TagDomainRule)
	}
	if ann.Tags[0].Value != "5회 실패 시 계정 잠금" {
		t.Errorf("Value = %q", ann.Tags[0].Value)
	}
}

func TestParse_SideEffect(t *testing.T) {
	p := NewParser()
	ann, _ := p.Parse("@sideEffect 감사 로그 기록")
	if len(ann.Tags) != 1 {
		t.Fatalf("expected 1 tag, got %d", len(ann.Tags))
	}
	if ann.Tags[0].Kind != model.TagSideEffect {
		t.Errorf("Kind = %q, want %q", ann.Tags[0].Kind, model.TagSideEffect)
	}
	if ann.Tags[0].Value != "감사 로그 기록" {
		t.Errorf("Value = %q", ann.Tags[0].Value)
	}
}

func TestParse_Mutates(t *testing.T) {
	p := NewParser()
	ann, _ := p.Parse("@mutates user.LastLoginAt, session.Token")
	if len(ann.Tags) != 1 {
		t.Fatalf("expected 1 tag, got %d", len(ann.Tags))
	}
	if ann.Tags[0].Kind != model.TagMutates {
		t.Errorf("Kind = %q, want %q", ann.Tags[0].Kind, model.TagMutates)
	}
	if ann.Tags[0].Value != "user.LastLoginAt, session.Token" {
		t.Errorf("Value = %q", ann.Tags[0].Value)
	}
}

func TestParse_Requires(t *testing.T) {
	p := NewParser()
	ann, _ := p.Parse("@requires user.IsActive == true")
	if len(ann.Tags) != 1 {
		t.Fatalf("expected 1 tag, got %d", len(ann.Tags))
	}
	if ann.Tags[0].Kind != model.TagRequires {
		t.Errorf("Kind = %q, want %q", ann.Tags[0].Kind, model.TagRequires)
	}
	if ann.Tags[0].Value != "user.IsActive == true" {
		t.Errorf("Value = %q", ann.Tags[0].Value)
	}
}

func TestParse_Ensures(t *testing.T) {
	p := NewParser()
	ann, _ := p.Parse("@ensures session != nil")
	if len(ann.Tags) != 1 {
		t.Fatalf("expected 1 tag, got %d", len(ann.Tags))
	}
	if ann.Tags[0].Kind != model.TagEnsures {
		t.Errorf("Kind = %q, want %q", ann.Tags[0].Kind, model.TagEnsures)
	}
	if ann.Tags[0].Value != "session != nil" {
		t.Errorf("Value = %q", ann.Tags[0].Value)
	}
}

func TestParse_MultiLineParam(t *testing.T) {
	p := NewParser()
	ann, _ := p.Parse("@param username 사용자의\n  로그인 ID")
	if len(ann.Tags) != 1 {
		t.Fatalf("expected 1 tag, got %d", len(ann.Tags))
	}
	if ann.Tags[0].Kind != model.TagParam {
		t.Errorf("Kind = %q, want %q", ann.Tags[0].Kind, model.TagParam)
	}
	if ann.Tags[0].Name != "username" {
		t.Errorf("Name = %q, want %q", ann.Tags[0].Name, "username")
	}
	// 태그 continuation은 \n으로 이어붙임
	if ann.Tags[0].Value != "사용자의\n로그인 ID" {
		t.Errorf("Value = %q, want %q", ann.Tags[0].Value, "사용자의\n로그인 ID")
	}
}

func TestParse_MultiLineIntent(t *testing.T) {
	p := NewParser()
	ann, _ := p.Parse("@intent 사용자 세션 생성 전\n  자격을 검증한다")
	if len(ann.Tags) != 1 {
		t.Fatalf("expected 1 tag, got %d", len(ann.Tags))
	}
	// 태그 continuation은 \n으로 이어붙임
	if ann.Tags[0].Value != "사용자 세션 생성 전\n자격을 검증한다" {
		t.Errorf("Value = %q, want %q", ann.Tags[0].Value, "사용자 세션 생성 전\n자격을 검증한다")
	}
}

func TestParse_FullAnnotation(t *testing.T) {
	input := `사용자 인증을 수행한다

로그인 핸들러에서 호출됨

@param username 사용자 로그인 ID
@param password 비밀번호
@return 인증 성공 시 JWT 토큰
@intent 세션 생성 전 자격 검증
@domainRule 5회 실패 시 계정 잠금
@sideEffect 감사 로그 기록
@mutates user.FailedAttempts
@requires user.IsActive == true
@ensures err == nil이면 token 유효
@see LoginHandler.Handle
@see SessionManager.Create`

	p := NewParser()
	ann, _ := p.Parse(input)
	// 빈 줄로 구분된 두 단락: Summary와 Context
	if ann.Summary != "사용자 인증을 수행한다" {
		t.Errorf("Summary = %q", ann.Summary)
	}
	if ann.Context != "로그인 핸들러에서 호출됨" {
		t.Errorf("Context = %q", ann.Context)
	}
	if len(ann.Tags) != 11 {
		t.Fatalf("expected 11 tags, got %d", len(ann.Tags))
	}

	expects := []struct {
		kind  model.TagKind
		name  string
		value string
	}{
		{model.TagParam, "username", "사용자 로그인 ID"},
		{model.TagParam, "password", "비밀번호"},
		{model.TagReturn, "", "인증 성공 시 JWT 토큰"},
		{model.TagIntent, "", "세션 생성 전 자격 검증"},
		{model.TagDomainRule, "", "5회 실패 시 계정 잠금"},
		{model.TagSideEffect, "", "감사 로그 기록"},
		{model.TagMutates, "", "user.FailedAttempts"},
		{model.TagRequires, "", "user.IsActive == true"},
		{model.TagEnsures, "", "err == nil이면 token 유효"},
		{model.TagSee, "", "LoginHandler.Handle"},
		{model.TagSee, "", "SessionManager.Create"},
	}

	for i, exp := range expects {
		tag := ann.Tags[i]
		if tag.Kind != exp.kind {
			t.Errorf("tag[%d] Kind = %q, want %q", i, tag.Kind, exp.kind)
		}
		if tag.Name != exp.name {
			t.Errorf("tag[%d] Name = %q, want %q", i, tag.Name, exp.name)
		}
		if tag.Value != exp.value {
			t.Errorf("tag[%d] Value = %q, want %q", i, tag.Value, exp.value)
		}
	}
}

// P2-a. JSDoc `@returns`는 `@return`의 공식 alias로 JSDoc 표준에서 양쪽 모두 허용된다.
// ccg 파서는 `@return`만 인식하고 있어 `@returns`는 unknown warning을 내고 드롭됨.
// alias로 매핑해 Kind=TagReturn, Ordinal도 @return과 동일 카운터를 공유해야 한다.
func TestParse_ReturnsAlias(t *testing.T) {
	p := NewParser()
	ann, warnings := p.Parse("@returns 인증된 사용자 토큰")
	if len(warnings) != 0 {
		t.Errorf("@returns should not produce warnings, got %v", warnings)
	}
	if len(ann.Tags) != 1 {
		t.Fatalf("expected 1 tag, got %d", len(ann.Tags))
	}
	if ann.Tags[0].Kind != model.TagReturn {
		t.Errorf("Kind = %q, want %q", ann.Tags[0].Kind, model.TagReturn)
	}
	if ann.Tags[0].Value != "인증된 사용자 토큰" {
		t.Errorf("Value = %q", ann.Tags[0].Value)
	}
}

// `@return`과 `@returns`가 섞여 쓰여도 Kind 수준에서는 동일하게 Ordinal이 순차 증가해야 한다.
// (JSDoc 코드베이스에서 혼용 문서가 흔함.)
func TestParse_ReturnAndReturnsAlias_SharedOrdinal(t *testing.T) {
	p := NewParser()
	ann, warnings := p.Parse("@return first\n@returns second")
	if len(warnings) != 0 {
		t.Errorf("no warnings expected, got %v", warnings)
	}
	if len(ann.Tags) != 2 {
		t.Fatalf("expected 2 tags, got %d", len(ann.Tags))
	}
	if ann.Tags[0].Kind != model.TagReturn || ann.Tags[0].Ordinal != 0 {
		t.Errorf("tag[0]: Kind=%q Ordinal=%d", ann.Tags[0].Kind, ann.Tags[0].Ordinal)
	}
	if ann.Tags[1].Kind != model.TagReturn || ann.Tags[1].Ordinal != 1 {
		t.Errorf("tag[1]: Kind=%q Ordinal=%d (want shared counter)", ann.Tags[1].Kind, ann.Tags[1].Ordinal)
	}
}

func TestParse_RawTextPreserved(t *testing.T) {
	input := "요약\n@param x 값"
	p := NewParser()
	ann, _ := p.Parse(input)
	if ann.RawText != input {
		t.Errorf("RawText = %q, want %q", ann.RawText, input)
	}
}

func TestParse_UnknownTag(t *testing.T) {
	p := NewParser()
	ann, warnings := p.Parse("@foo 알 수 없는 태그\n@param x 값")
	if len(warnings) != 1 {
		t.Fatalf("expected 1 warning for unknown tag, got %d: %v", len(warnings), warnings)
	}
	if warnings[0] != "foo" {
		t.Errorf("warning = %q, want %q", warnings[0], "foo")
	}
	if len(ann.Tags) != 1 {
		t.Fatalf("expected 1 tag (unknown skipped), got %d", len(ann.Tags))
	}
	if ann.Tags[0].Kind != model.TagParam {
		t.Errorf("Kind = %q, want %q", ann.Tags[0].Kind, model.TagParam)
	}
}

func TestParse_TagWithoutValue(t *testing.T) {
	p := NewParser()
	ann, _ := p.Parse("@return")
	if len(ann.Tags) != 1 {
		t.Fatalf("expected 1 tag, got %d", len(ann.Tags))
	}
	if ann.Tags[0].Kind != model.TagReturn {
		t.Errorf("Kind = %q, want %q", ann.Tags[0].Kind, model.TagReturn)
	}
	if ann.Tags[0].Value != "" {
		t.Errorf("Value = %q, want empty", ann.Tags[0].Value)
	}
}

func TestParse_ParamWithoutName(t *testing.T) {
	p := NewParser()
	// @param 이름 없이 단독 사용 → 무시
	ann, _ := p.Parse("@param")
	if len(ann.Tags) != 0 {
		t.Errorf("expected 0 tags for @param without name, got %d: %+v", len(ann.Tags), ann.Tags)
	}
}

func TestParse_ParamNameOnlyNoDescription(t *testing.T) {
	p := NewParser()
	// @param name (설명 없음) → Name은 있지만 Value는 빈 문자열로 허용
	ann, _ := p.Parse("@param x")
	if len(ann.Tags) != 1 {
		t.Fatalf("expected 1 tag, got %d", len(ann.Tags))
	}
	if ann.Tags[0].Name != "x" {
		t.Errorf("Name = %q, want %q", ann.Tags[0].Name, "x")
	}
	if ann.Tags[0].Value != "" {
		t.Errorf("Value = %q, want empty", ann.Tags[0].Value)
	}
}

func TestParse_MixedIndentation(t *testing.T) {
	p := NewParser()
	ann, _ := p.Parse("\t  요약 내용  \n\t\t@param x 값")
	if ann.Summary != "요약 내용" {
		t.Errorf("Summary = %q, want %q", ann.Summary, "요약 내용")
	}
	if len(ann.Tags) != 1 {
		t.Fatalf("expected 1 tag, got %d", len(ann.Tags))
	}
	if ann.Tags[0].Name != "x" {
		t.Errorf("Name = %q, want %q", ann.Tags[0].Name, "x")
	}
}
