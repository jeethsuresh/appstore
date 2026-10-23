package dev.jeeth.privatestore

import android.accounts.AccountManager
import android.content.Intent
import android.content.pm.PackageManager
import android.os.Build
import androidx.test.platform.app.InstrumentationRegistry
import androidx.work.WorkManager
import org.junit.Assert.*
import org.junit.Assume.assumeTrue
import org.junit.Test
import java.io.File
import java.security.MessageDigest
import java.util.concurrent.TimeUnit

/** Host runs begin, approves Android's installer, then runs verify in the replacement process. */
class SelfUpdateTest {
    private val context=InstrumentationRegistry.getInstrumentation().targetContext
    private fun isolated() { assumeTrue(context.packageName.endsWith(".testbed") && InstrumentationRegistry.getArguments().getString("selfUpdateTest")=="true") }
    private fun broker():BrokerSession {
        val now=System.currentTimeMillis()/1000
        return BrokerSession(BrokerProtocol.ISSUER,GravitasLogin.STORE,"self-update-fixture","@selfupdate:gravitas.cool","a".repeat(43),"b".repeat(43),"profile.name native.broker",now+3000,"self-update-family","c".repeat(43),now+7200)
    }
    @Suppress("DEPRECATION")
    @Test fun beginVerifiedSelfUpdate() {
        isolated()
        val file=File(context.filesDir,"self-update.apk");assertTrue(file.isFile)
        val pm=context.packageManager
        val archive=pm.getPackageArchiveInfo(file.path,0)!!
        val flags=if(Build.VERSION.SDK_INT>=28) PackageManager.GET_SIGNING_CERTIFICATES else PackageManager.GET_SIGNATURES
        val own=pm.getPackageInfo(context.packageName,flags)
        val signatures=if(Build.VERSION.SDK_INT>=28) own.signingInfo!!.apkContentsSigners else own.signatures!!
        val cert=MessageDigest.getInstance("SHA-256").digest(signatures.single().toByteArray()).toHex()
        val code=if(Build.VERSION.SDK_INT>=28) archive.longVersionCode else archive.versionCode.toLong()
        val sha=file.inputStream().use { input->val digest=MessageDigest.getInstance("SHA-256");val buffer=ByteArray(65536);while(true) { val n=input.read(buffer);if(n<0) break;digest.update(buffer,0,n) };digest.digest().toHex() }
        val app=StoreApp("appstore","Private Store",context.packageName,cert,"Self-update fixture",StoreRelease(1,code,archive.versionName!!,sha,file.length(),26,"Self-update preservation"))
        assertTrue(Versions.newer(Installer.installed(context,context.packageName),code))
        BrokerManager.engine(context).accept(broker(),Session("https://self-update.invalid","self-update-device","@selfupdate:gravitas.cool"))
        context.getSharedPreferences("self-update-fixture",0).edit().putLong("expectedVersion",code).putString("broker",BrokerVault(context).load()!!.json().toString()).commit()
        context.getSharedPreferences("updates",0).edit().putBoolean("auto",false).putLong("lastCheck",123456L).commit()
        UpdateWorker.schedule(context)
        assertEquals(1,WorkManager.getInstance(context).getWorkInfosForUniqueWork("store-updates").get(10,TimeUnit.SECONDS).count { !it.state.isFinished })
        val instrumentation=InstrumentationRegistry.getInstrumentation()
        instrumentation.startActivitySync(Intent(context,MainActivity::class.java).addFlags(Intent.FLAG_ACTIVITY_NEW_TASK))
        Installer.install(context,file,app,background=false)
        // This test process is intentionally killed by the successful package replacement.
    }
    @Test fun verifyReplacementPreservesPrivateStateAndSchedule() {
        isolated()
        // Instrumentation marks its target stopped when begin finishes. Reopen the
        // replacement normally, exercising recovery even if that suppressed the broadcast.
        val instrumentation=InstrumentationRegistry.getInstrumentation()
        instrumentation.startActivitySync(Intent(context,MainActivity::class.java).addFlags(Intent.FLAG_ACTIVITY_NEW_TASK))
        val fixture=context.getSharedPreferences("self-update-fixture",0)
        assertEquals(fixture.getLong("expectedVersion",-1),Installer.installed(context,context.packageName))
        assertEquals(Session("https://self-update.invalid","self-update-device","@selfupdate:gravitas.cool"),SessionVault(context).load())
        assertEquals(fixture.getString("broker",null),BrokerVault(context).load()!!.json().toString())
        assertEquals(listOf("@selfupdate:gravitas.cool"),AccountManager.get(context).getAccountsByType(BrokerAccounts.type(context)).map { it.name })
        val prefs=context.getSharedPreferences("updates",0)
        assertFalse(prefs.getBoolean("auto",true));assertEquals(123456L,prefs.getLong("lastCheck",0))
        assertFalse(prefs.contains("pending.${context.packageName}"))
        assertEquals(1,WorkManager.getInstance(context).getWorkInfosForUniqueWork("store-updates").get(10,TimeUnit.SECONDS).count { !it.state.isFinished })
    }
}
