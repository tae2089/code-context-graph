use std::fmt;

struct UserService {
    id: i32,
}

impl UserService {
    fn get_user(&self) -> String {
        format!("user-{}", self.id)
    }
}

fn handle_request() {
    let svc = UserService { id: 1 };
    svc.get_user();
}
