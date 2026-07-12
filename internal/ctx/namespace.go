// @index 요청 범위 값을 context.Context에 안전하게 전파하는 공용 패키지. 현재는 namespace 격리 계약을 제공한다.
package ctx

import "context"

// ctxKey is the unexported type used as the namespace context key to avoid collisions.
// @intent isolate the namespace value in the context map from any other package's keys.
type ctxKey struct{}

const DefaultNamespace = "default"

// Normalize replaces an empty namespace with DefaultNamespace, leaving other values unchanged.
// @intent normalize namespace query parameter values so store and DB-backed search layers always observe a non-empty namespace string.
func Normalize(ns string) string {
	if ns == "" {
		return DefaultNamespace
	}
	return ns
}

// WithNamespace는 context에 namespace 값을 설정한다.
// @intent 호출자 시그니처 변경 없이 store 레이어까지 namespace를 전달한다.
func WithNamespace(ctx context.Context, ns string) context.Context {
	return context.WithValue(ctx, ctxKey{}, Normalize(ns))
}

// FromContext는 context에서 namespace를 추출한다.
// @intent store 내부에서 context로부터 namespace를 꺼내 쿼리 필터에 적용한다.
// @return namespace가 설정되지 않았으면 DefaultNamespace를 반환한다.
func FromContext(ctx context.Context) string {
	if v, ok := ctx.Value(ctxKey{}).(string); ok {
		return Normalize(v)
	}
	return DefaultNamespace
}
