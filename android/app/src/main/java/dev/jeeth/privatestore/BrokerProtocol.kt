package dev.jeeth.privatestore

data class BrokerRequest(val challenge: String, val state: String)
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
    private val proof = Regex("[A-Za-z0-9_-]{43}")
    fun request(type: String, version: Int, challenge: String, state: String): BrokerRequest {
        if (version != 1 || !proof.matches(challenge) || !proof.matches(state) || type != "$PREFIX$challenge:$state") throw BrokerFailure(BrokerError.INVALID_REQUEST)
        return BrokerRequest(challenge,state)
    }
    fun approved(packages: List<String>, declared: String?, certificates: List<String>): Boolean =
        packages == listOf(CLIENT_PACKAGE) && declared == CLIENT_PACKAGE && certificates == listOf(CLIENT_CERT)
    fun scopes(value: String): Boolean = value.split(' ').filter { it.isNotEmpty() }.let { it.size == 2 && it.toSet() == setOf("profile.name","native.broker") }
    fun credential(value: String) = proof.matches(value)
}
