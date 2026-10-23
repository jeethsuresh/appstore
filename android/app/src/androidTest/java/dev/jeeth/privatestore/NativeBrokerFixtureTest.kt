package dev.jeeth.privatestore

import android.accounts.AccountManager
import android.content.Intent
import android.os.Bundle
import androidx.test.platform.app.InstrumentationRegistry
import org.junit.Assert.*
import org.junit.Assume.assumeTrue
import org.junit.Test

/** Explicitly invoked on a disposable emulator; never accepts or emits private credentials. */
class NativeBrokerFixtureTest {
    @Test fun preparePublicLogin() {
        val instrumentation=InstrumentationRegistry.getInstrumentation();val context=instrumentation.targetContext
        assumeTrue(InstrumentationRegistry.getArguments().getString("nativeBrokerFixture")=="true")
        assertNull(SessionVault(context).load());assertNull(BrokerVault(context).load())
        val pending=StoreApi(Session(GravitasLogin.STORE,"")).startLogin(GravitasLogin.create(GravitasLogin.STORE,"Contained broker emulator"))
        SessionVault(context).savePending(pending)
        instrumentation.startActivitySync(Intent(context,MainActivity::class.java).addFlags(Intent.FLAG_ACTIVITY_NEW_TASK))
        instrumentation.sendStatus(0,Bundle().apply { putString("publicAuthorizationUrl",pending.authorizationUrl) })
    }
    @Test fun verifySignedIn() {
        assumeTrue(InstrumentationRegistry.getArguments().getString("nativeBrokerFixture")=="true")
        val context=InstrumentationRegistry.getInstrumentation().targetContext
        val device=SessionVault(context).load();val broker=BrokerVault(context).load()
        assertNotNull(device);assertNotNull(broker);assertEquals(device!!.account,broker!!.account)
        assertEquals(1,AccountManager.get(context).getAccountsByType(BrokerAccounts.type(context)).size)
        assertTrue(StoreApi(device).apps().any { it.id=="spacetime" })
    }
    @Test fun verifyCentralRevocation() {
        assumeTrue(InstrumentationRegistry.getArguments().getString("nativeBrokerFixture")=="true")
        val context=InstrumentationRegistry.getInstrumentation().targetContext
        try { BrokerManager.engine(context).refreshForUpdates();fail("Central logout was not enforced") } catch(e:BrokerFailure) { assertEquals(BrokerError.LOGIN_REQUIRED,e.reason) }
        assertNull(BrokerVault(context).load());assertNull(SessionVault(context).load());assertTrue(AccountManager.get(context).getAccountsByType(BrokerAccounts.type(context)).isEmpty())
    }
}
