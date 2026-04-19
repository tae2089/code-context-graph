import os

class UserService:
    def get_user(self, user_id: int) -> str:
        return f"user-{user_id}"

def handle_request():
    svc = UserService()
    svc.get_user(1)
