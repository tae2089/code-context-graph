local UserService = {}
UserService.__index = UserService

function UserService.new()
    local self = setmetatable({}, UserService)
    return self
end

function UserService:getUser(id)
    return "user-" .. tostring(id)
end

local function handleRequest()
    local svc = UserService.new()
    svc:getUser(1)
end

return UserService
