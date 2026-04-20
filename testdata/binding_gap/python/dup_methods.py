# 두 클래스가 같은 이름의 메서드를 가진 경우 nameIndex dedup 오병합 측정용 fixture.
# - Alpha.save: @classmethod 데코레이터 1개 (StartLine은 데코레이터 줄)
# - Beta.save: @classmethod 데코레이터 1개 (StartLine은 데코레이터 줄, Alpha.save보다 아래)
# 동명 메서드가 서로 다른 클래스 본문에 존재하지만 nameIndex["save"]는 하나만 보관한다.


class Alpha:
    @classmethod
    def save(cls) -> int:
        """@intent Alpha save"""
        return 1


class Beta:
    @classmethod
    def save(cls) -> int:
        """@intent Beta save"""
        return 2
