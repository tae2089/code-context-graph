<?php

class UserService {
    public function getUser(int $id): string {
        return "user-" . $id;
    }
}

function handleRequest(): void {
    $svc = new UserService();
    $svc->getUser(1);
}
