package annotation

import (
	"testing"

	"github.com/imtaebin/code-context-graph/internal/model"
)

func TestParse_EmptyString(t *testing.T) {
	p := NewParser()
	ann, err := p.Parse("")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
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
	ann, err := p.Parse("   \n\t\n  ")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
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
	ann, err := p.Parse("사용자 인증을 수행한다")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if ann.Summary != "사용자 인증을 수행한다" {
		t.Errorf("Summary = %q, want %q", ann.Summary, "사용자 인증을 수행한다")
	}
	if ann.Context != "" {
		t.Errorf("Context = %q, want empty", ann.Context)
	}
}

func TestParse_SummaryAndContext(t *testing.T) {
	p := NewParser()
	ann, err := p.Parse("사용자 인증을 수행한다\n로그인 핸들러에서 호출됨")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if ann.Summary != "사용자 인증을 수행한다" {
		t.Errorf("Summary = %q, want %q", ann.Summary, "사용자 인증을 수행한다")
	}
	if ann.Context != "로그인 핸들러에서 호출됨" {
		t.Errorf("Context = %q, want %q", ann.Context, "로그인 핸들러에서 호출됨")
	}
}

func TestParse_SummaryAndContext_WithBlankLine(t *testing.T) {
	p := NewParser()
	ann, err := p.Parse("사용자 인증을 수행한다\n\n로그인 핸들러에서 호출됨")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if ann.Summary != "사용자 인증을 수행한다" {
		t.Errorf("Summary = %q, want %q", ann.Summary, "사용자 인증을 수행한다")
	}
	if ann.Context != "로그인 핸들러에서 호출됨" {
		t.Errorf("Context = %q, want %q", ann.Context, "로그인 핸들러에서 호출됨")
	}
}

func TestParse_ThreeNonTagLines(t *testing.T) {
	p := NewParser()
	ann, err := p.Parse("첫째 줄\n둘째 줄\n셋째 줄")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if ann.Summary != "첫째 줄" {
		t.Errorf("Summary = %q, want %q", ann.Summary, "첫째 줄")
	}
	if ann.Context != "둘째 줄" {
		t.Errorf("Context = %q, want %q", ann.Context, "둘째 줄")
	}
}

func TestParse_SingleParam(t *testing.T) {
	p := NewParser()
	ann, err := p.Parse("@param username 사용자 로그인 ID")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
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
	ann, err := p.Parse("@param username 사용자 ID\n@param password 비밀번호")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
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
	ann, err := p.Parse("@return 인증된 사용자 토큰")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
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
	ann, err := p.Parse("@see LoginHandler.Handle")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
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
	ann, err := p.Parse("@see LoginHandler.Handle\n@see SessionManager.Create")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
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
	ann, err := p.Parse("@intent 사용자 세션 생성 전 자격 검증")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
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
	ann, err := p.Parse("@domainRule 5회 실패 시 계정 잠금")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
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
	ann, err := p.Parse("@sideEffect 감사 로그 기록")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
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
	ann, err := p.Parse("@mutates user.LastLoginAt, session.Token")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
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
	ann, err := p.Parse("@requires user.IsActive == true")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
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
	ann, err := p.Parse("@ensures session != nil")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
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
	ann, err := p.Parse("@param username 사용자의\n  로그인 ID")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(ann.Tags) != 1 {
		t.Fatalf("expected 1 tag, got %d", len(ann.Tags))
	}
	if ann.Tags[0].Kind != model.TagParam {
		t.Errorf("Kind = %q, want %q", ann.Tags[0].Kind, model.TagParam)
	}
	if ann.Tags[0].Name != "username" {
		t.Errorf("Name = %q, want %q", ann.Tags[0].Name, "username")
	}
	if ann.Tags[0].Value != "사용자의 로그인 ID" {
		t.Errorf("Value = %q, want %q", ann.Tags[0].Value, "사용자의 로그인 ID")
	}
}

func TestParse_MultiLineIntent(t *testing.T) {
	p := NewParser()
	ann, err := p.Parse("@intent 사용자 세션 생성 전\n  자격을 검증한다")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(ann.Tags) != 1 {
		t.Fatalf("expected 1 tag, got %d", len(ann.Tags))
	}
	if ann.Tags[0].Value != "사용자 세션 생성 전 자격을 검증한다" {
		t.Errorf("Value = %q, want %q", ann.Tags[0].Value, "사용자 세션 생성 전 자격을 검증한다")
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
	ann, err := p.Parse(input)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
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

func TestParse_RawTextPreserved(t *testing.T) {
	input := "요약\n@param x 값"
	p := NewParser()
	ann, err := p.Parse(input)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if ann.RawText != input {
		t.Errorf("RawText = %q, want %q", ann.RawText, input)
	}
}

func TestParse_UnknownTag(t *testing.T) {
	p := NewParser()
	ann, err := p.Parse("@foo 알 수 없는 태그\n@param x 값")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
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
	ann, err := p.Parse("@return")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
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

func TestParse_MixedIndentation(t *testing.T) {
	p := NewParser()
	ann, err := p.Parse("\t  요약 내용  \n\t\t@param x 값")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
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
