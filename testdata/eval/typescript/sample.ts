class UserService {
  getUser(id: number): string {
    return `user-${id}`;
  }
}

function handleRequest(): void {
  const svc = new UserService();
  svc.getUser(1);
}

export { UserService, handleRequest };
