package sample;

import java.util.List;

public class UserService {
    public String getUser(int id) {
        return "user-" + id;
    }
}

class RequestHandler {
    public void handleRequest() {
        UserService svc = new UserService();
        svc.getUser(1);
    }
}
