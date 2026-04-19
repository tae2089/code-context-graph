const fs = require('fs');

class UserService {
    getUser(id) {
        return `user-${id}`;
    }
}

function handleRequest() {
    const svc = new UserService();
    svc.getUser(1);
}
