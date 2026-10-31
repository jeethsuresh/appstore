package dev.jeeth.privatestore

import org.junit.Assert.*
import org.junit.Test

class NotificationRelayPolicyTest {
    @Test fun idleDeliveryHonorsTheUsersBatteryExemption() {
        assertFalse(NotificationRelayPolicy.canConnect(deviceIdle = true, batteryExempt = false, networkValidated = true))
        assertTrue(NotificationRelayPolicy.canConnect(deviceIdle = true, batteryExempt = true, networkValidated = true))
    }
    @Test fun powerExemptionDoesNotRetryWithoutAValidatedNetwork() {
        for (idle in listOf(false, true)) for (exempt in listOf(false, true)) {
            assertFalse(NotificationRelayPolicy.canConnect(idle, exempt, networkValidated = false))
        }
    }
    @Test fun anAwakePhoneCanConnectWithoutAPowerExemption() {
        assertTrue(NotificationRelayPolicy.canConnect(deviceIdle = false, batteryExempt = false, networkValidated = true))
        assertTrue(NotificationRelayPolicy.canConnect(deviceIdle = false, batteryExempt = true, networkValidated = true))
    }
    @Test fun failuresBackOffToHalfAnHourWithoutFastRetryStorms() {
        val delays = (1..100).map { NotificationRelayPolicy.retryDelaySeconds(it, 0) }
        assertEquals(60L, delays.first())
        assertEquals(1800L, delays.last())
        assertTrue(delays.zipWithNext().all { (a,b) -> b >= a })
        assertTrue(delays.all { it in 60L..1800L })
    }
    @Test fun brieflyAcceptedConnectionsDoNotResetBackoff() {
        assertEquals(7, NotificationRelayPolicy.afterDisconnect(6, 500))
        assertEquals(1, NotificationRelayPolicy.afterDisconnect(6, 5 * 60_000L))
        assertEquals(1, NotificationRelayPolicy.afterDisconnect(0, 0))
    }
    @Test fun jitterCannotOverflowOrTurnAReconnectIntoAnImmediateLoop() {
        assertEquals(60L, NotificationRelayPolicy.retryDelaySeconds(Int.MIN_VALUE, -10))
        assertEquals(1829L, NotificationRelayPolicy.retryDelaySeconds(Int.MAX_VALUE, Int.MAX_VALUE))
    }
}
