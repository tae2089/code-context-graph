// @index context.Context에 namespace를 전파하는 유틸리티 패키지.
package ctxns

import "context"

type ctxKey struct{}

// WithNamespace는 context에 namespace 값을 설정한다.
// @intent 호출자 시그니처 변경 없이 store 레이어까지 namespace를 전달한다.
func WithNamespace(ctx context.Context, ns string) context.Context {
	return context.WithValue(ctx, ctxKey{}, ns)
}

// FromContext는 context에서 namespace를 추출한다.
// @intent store 내부에서 context로부터 namespace를 꺼내 쿼리 필터에 적용한다.
// @return namespace가 설정되지 않았으면 빈 문자열을 반환한다.
func FromContext(ctx context.Context) string {
	if v, ok := ctx.Value(ctxKey{}).(string); ok {
		return v
	}
	return ""
}
