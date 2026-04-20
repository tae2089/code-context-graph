@dataclass
class User:
    """
    @intent 사용자 엔티티
    @domainRule email 유니크
    """
    name: str
    email: str
