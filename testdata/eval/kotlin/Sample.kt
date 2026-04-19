import kotlin.math.abs

class UserService {
    fun getUser(id: Int): String {
        return "user-$id"
    }
}

fun handleRequest() {
    val svc = UserService()
    svc.getUser(1)
}
