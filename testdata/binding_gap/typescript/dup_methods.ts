// 두 클래스가 같은 이름의 메서드를 가진 경우 nameIndex dedup 오병합 측정용 fixture.
// - Alpha.render: @Memoized 데코레이터 있음
// - Beta.render: @Memoized 데코레이터 있음 (Alpha.render보다 아래)

declare function Memoized(target: any, key: string): void;

export class Alpha {
    @Memoized
    render(): string {
        return "alpha";
    }
}

export class Beta {
    @Memoized
    render(): string {
        return "beta";
    }
}
