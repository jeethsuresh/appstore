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
    @Test fun failuresBackOffWithoutLeavingAlertsWaitingForHalfAnHour() {
        val delays = (1..100).map { NotificationRelayPolicy.retryDelaySeconds(it, 0) }
        assertEquals(5L, delays.first())
        assertEquals(120L, delays.last())
        assertTrue(delays.zipWithNext().all { (a,b) -> b >= a })
        assertTrue(delays.all { it in 5L..120L })
    }
    @Test fun brieflyAcceptedConnectionsDoNotResetBackoff() {
        assertEquals(6, NotificationRelayPolicy.afterDisconnect(6, 500))
        assertEquals(1, NotificationRelayPolicy.afterDisconnect(6, 30_000))
        assertEquals(1, NotificationRelayPolicy.afterDisconnect(6, 5 * 60_000L))
        assertEquals(1, NotificationRelayPolicy.afterDisconnect(0, 0))
    }
    @Test fun jitterCannotOverflowOrTurnAReconnectIntoAnImmediateLoop() {
        assertEquals(5L, NotificationRelayPolicy.retryDelaySeconds(Int.MIN_VALUE, -10))
        assertEquals(124L, NotificationRelayPolicy.retryDelaySeconds(Int.MAX_VALUE, Int.MAX_VALUE))
    }

    @Test fun receiverFailuresAreNeverAcknowledgedAndDoNotPreventTheNextAppHandoff() {
        assertFalse(NotificationRelayDelivery.accepted { throw IllegalArgumentException("provider not ready") })
        assertFalse(NotificationRelayDelivery.accepted { throw IllegalStateException("temporary IPC failure") })
        assertFalse(NotificationRelayDelivery.accepted { false })
        assertTrue(NotificationRelayDelivery.accepted { true })
    }

    @Test fun aMissingServerKeepaliveCannotBlockTheListenerForTwentyMinutes() {
        assertTrue(NotificationRelayPolicy.STREAM_READ_TIMEOUT_MILLIS > 60_000)
        assertTrue(NotificationRelayPolicy.STREAM_READ_TIMEOUT_MILLIS <= 90_000)
    }
}
