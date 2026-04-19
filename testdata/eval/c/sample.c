#include <stdio.h>
#include <string.h>

typedef struct {
    int id;
} UserService;

void get_user(UserService *svc, char *buf) {
    sprintf(buf, "user-%d", svc->id);
}

void handle_request() {
    UserService svc = {1};
    char buf[64];
    get_user(&svc, buf);
}
