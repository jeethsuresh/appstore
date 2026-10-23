package dev.jeeth.privatestore
import org.junit.Assert.*
import org.junit.Test
class BrokerProtocolTest {
    private val challenge="c".repeat(43)
    private val state="s".repeat(43)
    private val type=BrokerProtocol.PREFIX+challenge+":"+state
    @Test fun actualTokenTypeBindsChallengeAndState() {
        assertEquals(BrokerRequest(challenge,state),BrokerProtocol.request(type,1,challenge,state))
        for(args in listOf(Triple(type,challenge,"other".repeat(9)),Triple(type,"x".repeat(43),state),Triple(BrokerProtocol.PREFIX+state+":"+challenge,challenge,state))) assertTrue(runCatching { BrokerProtocol.request(args.first,1,args.second,args.third) }.isFailure)
        assertTrue(runCatching { BrokerProtocol.request(type,2,challenge,state) }.isFailure)
        assertNotEquals(type,BrokerProtocol.PREFIX+challenge+":"+"n".repeat(43))
    }
    @Test fun packageNameAloneNeverAuthorizesTheCaller() {
        val pkg=BrokerProtocol.CLIENT_PACKAGE;val pin=BrokerProtocol.CLIENT_CERT
        assertTrue(BrokerProtocol.approved(listOf(pkg),pkg,listOf(pin)))
        assertFalse(BrokerProtocol.approved(listOf(pkg),pkg,listOf("0".repeat(64))))
        assertFalse(BrokerProtocol.approved(listOf(pkg,"shared.uid.peer"),pkg,listOf(pin)))
        assertFalse(BrokerProtocol.approved(listOf("other.app"),pkg,listOf(pin)))
        assertFalse(BrokerProtocol.approved(listOf(pkg),"other.app",listOf(pin)))
        assertFalse(BrokerProtocol.approved(listOf(pkg),pkg,listOf(pin,pin)))
    }
    @Test fun brokerCapabilityMustBeExplicitAndCannotExpandPermissions() {
        assertTrue(BrokerProtocol.scopes("profile.name native.broker"));assertTrue(BrokerProtocol.scopes("native.broker profile.name"))
        for(scope in listOf("profile.name","native.broker","profile.name native.broker chats.read","profile.name native.broker native.broker")) assertFalse(BrokerProtocol.scopes(scope))
    }
}
