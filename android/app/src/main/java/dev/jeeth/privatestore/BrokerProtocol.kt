package dev.jeeth.privatestore

data class BrokerClient(val packageName: String, val certificate: String, val prefix: String, val origin: String, val scope: String) {
    val redirect get() = "$origin/auth/gravitas/callback"
}
data class BrokerRequest(val challenge: String, val state: String, val client: BrokerClient = BrokerProtocol.clients.first())
enum class BrokerError(val wire: String) { INVALID_REQUEST("invalid_request"), UNAUTHORIZED_CLIENT("unauthorized_client"), LOGIN_REQUIRED("login_required"), CONSENT_REQUIRED("consent_required"), UNAVAILABLE("unavailable"), CANCELLED("cancelled") }
class BrokerFailure(val reason: BrokerError) : Exception(reason.wire)
object BrokerProtocol {
    const val ACCOUNT_TYPE = "dev.jeeth.privatestore.gravitas"
    const val ISSUER = "https://gravitas.cool"
    const val ORIGIN = "https://spacetime.gravitas.cool"
    const val REDIRECT = "$ORIGIN/auth/gravitas/callback"
    const val CLIENT_PACKAGE = "com.jeethsuresh.spacetime"
    const val CLIENT_CERT = "f5714ad3d2b3d82b283efd61b2d99f5eaad5ec9efe67b644a62efc79fec94582"
    const val PREFIX = "gravitas_code:spacetime:v1:"
    val clients = listOf(
        BrokerClient(CLIENT_PACKAGE, CLIENT_CERT, PREFIX, ORIGIN, "profile.name"),
        BrokerClient("cool.gravitas.river", "6559956b5cf6b2f796824995b31da7fa2c176ba652e8fcb40741686d850bb6b0", "gravitas_code:river:v1:", "https://chat.gravitas.cool", "profile.name matrix.full chats.list chats.read chats.write groups.create groups.manage groups.join spaces.channels contacts.read contacts.manage calls.read calls.join posts.publish")
    )
    fun client(packageName: String?) = clients.singleOrNull { it.packageName == packageName }
    private val proof = Regex("[A-Za-z0-9_-]{43}")
    fun request(type: String, version: Int, challenge: String, state: String, packageName: String = CLIENT_PACKAGE): BrokerRequest {
        val target = client(packageName) ?: throw BrokerFailure(BrokerError.UNAUTHORIZED_CLIENT)
        if (version != 1 || !proof.matches(challenge) || !proof.matches(state) || type != "${target.prefix}$challenge:$state") throw BrokerFailure(BrokerError.INVALID_REQUEST)
        return BrokerRequest(challenge,state,target)
    }
    fun approved(packages: List<String>, declared: String?, certificates: List<String>): Boolean =
        client(declared)?.let { packages == listOf(it.packageName) && certificates == listOf(it.certificate) } == true
    fun scopes(value: String): Boolean = value.split(' ').filter { it.isNotEmpty() }.let { it.size == 2 && it.toSet() == setOf("profile.name","native.broker") }
    fun credential(value: String) = proof.matches(value)
}
