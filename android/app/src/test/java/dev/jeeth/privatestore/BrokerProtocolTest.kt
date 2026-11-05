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
        assertTrue(BrokerProtocol.scopes("profile.name profile.username native.broker"));assertTrue(BrokerProtocol.scopes("native.broker profile.name"))
        for(scope in listOf("profile.name","native.broker","profile.name profile.username native.broker chats.read","profile.name profile.username native.broker native.broker")) assertFalse(BrokerProtocol.scopes(scope))
    }
    @Test fun riverUsesItsOwnPinnedCallerAndFixedTarget() {
        val river=BrokerProtocol.client("cool.gravitas.river")!!
        val request=BrokerProtocol.request(river.prefix+challenge+":"+state,1,challenge,state,river.packageName)
        assertEquals(river,request.client)
        assertEquals("https://chat.gravitas.cool/auth/gravitas/callback",river.redirect)
        assertTrue(river.scope.split(' ').containsAll(listOf("matrix.full","contacts.read","contacts.manage")))
        assertFalse(river.scope.split(' ').contains("native.broker"))
        assertTrue(BrokerProtocol.approved(listOf(river.packageName),river.packageName,listOf(river.certificate)))
        assertFalse(BrokerProtocol.approved(listOf(river.packageName),river.packageName,listOf(BrokerProtocol.CLIENT_CERT)))
        assertFalse(BrokerProtocol.approved(listOf(river.packageName,"shared.uid"),river.packageName,listOf(river.certificate)))
        assertTrue(runCatching { BrokerProtocol.request(river.prefix+challenge+":"+state,1,challenge,state,BrokerProtocol.CLIENT_PACKAGE) }.isFailure)
        assertTrue(runCatching { BrokerProtocol.request(type,1,challenge,state,river.packageName) }.isFailure)
    }
    @Test fun havenUsesItsPermanentCertificateAndOnlyItsOwnTarget() {
        val haven=BrokerProtocol.client("cool.gravitas.haven")!!
        assertEquals("3c01375ebd40c543f9d6d1a6c1aeb18c689188eee35341a3b1ba3b89a397c085",haven.certificate)
        assertEquals("https://haven.gravitas.cool/auth/gravitas/callback",haven.redirect)
        assertEquals("profile.name",haven.scope)
        val tokenType="gravitas_code:haven:v1:$challenge:$state"
        assertEquals(haven,BrokerProtocol.request(tokenType,1,challenge,state,haven.packageName).client)
        assertTrue(BrokerProtocol.approved(listOf(haven.packageName),haven.packageName,listOf(haven.certificate)))
        assertFalse(BrokerProtocol.approved(listOf(haven.packageName),haven.packageName,listOf(BrokerProtocol.CLIENT_CERT)))
        assertFalse(BrokerProtocol.approved(listOf(haven.packageName,"shared.uid"),haven.packageName,listOf(haven.certificate)))
        assertTrue(runCatching { BrokerProtocol.request(type,1,challenge,state,haven.packageName) }.isFailure)
        assertTrue(runCatching { BrokerProtocol.request(tokenType,1,challenge,state) }.isFailure)
    }
}
