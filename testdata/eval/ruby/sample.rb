require 'json'

class UserService
  def get_user(id)
    "user-#{id}"
  end
end

def handle_request
  svc = UserService.new
  svc.get_user(1)
end
