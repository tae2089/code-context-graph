#include <string>
#include <iostream>

class UserService {
public:
    std::string getUser(int id) {
        return "user-" + std::to_string(id);
    }
};

void handleRequest() {
    UserService svc;
    svc.getUser(1);
}
