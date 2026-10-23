package dev.jeeth.privatestore

import android.accounts.AbstractAccountAuthenticator
import android.accounts.Account
import android.accounts.AccountAuthenticatorResponse
import android.accounts.AccountManager
import android.app.Service
import android.content.Context
import android.content.Intent
import android.os.Bundle
import android.os.IBinder

class GravitasAuthenticator(private val context: Context,private val engine: BrokerEngine=BrokerManager.engine(context),private val caller: (Int,String?)->Boolean={ uid,pkg->BrokerAccounts.verifiedCaller(context,uid,pkg) }) : AbstractAccountAuthenticator(context) {
    private fun error(reason: BrokerError) = Bundle().apply {
        putString("brokerError",reason.wire)
        putString(AccountManager.KEY_ERROR_MESSAGE,reason.wire)
        putInt(AccountManager.KEY_ERROR_CODE,when(reason) {
            BrokerError.INVALID_REQUEST->AccountManager.ERROR_CODE_BAD_ARGUMENTS
            BrokerError.LOGIN_REQUIRED->AccountManager.ERROR_CODE_BAD_AUTHENTICATION
            BrokerError.CANCELLED->AccountManager.ERROR_CODE_CANCELED
            BrokerError.UNAVAILABLE->AccountManager.ERROR_CODE_NETWORK_ERROR
            else->AccountManager.ERROR_CODE_UNSUPPORTED_OPERATION
        })
    }
    override fun getAuthToken(response: AccountAuthenticatorResponse,account: Account,authTokenType: String,options: Bundle): Bundle {
        if(!caller(options.getInt(AccountManager.KEY_CALLER_UID,-1),options.getString(AccountManager.KEY_ANDROID_PACKAGE_NAME))) return error(BrokerError.UNAUTHORIZED_CLIENT)
        if(account.type!=BrokerAccounts.type(context)) return error(BrokerError.INVALID_REQUEST)
        return try {
            val request=BrokerProtocol.request(authTokenType,options.getInt("protocolVersion",0),options.getString("codeChallenge").orEmpty(),options.getString("state").orEmpty())
            val value=engine.code(account.name,request)
            Bundle().apply {
                putString(AccountManager.KEY_ACCOUNT_NAME,account.name);putString(AccountManager.KEY_ACCOUNT_TYPE,account.type)
                putString(AccountManager.KEY_AUTHTOKEN,value.getString("code"));putLong(KEY_CUSTOM_TOKEN_EXPIRY,0L)
                putInt("protocolVersion",1);putString("state",request.state);putString("codeChallenge",request.challenge)
                putString("issuer",value.getString("issuer"));putString("appOrigin",value.getString("app_origin"));putString("redirectUri",value.getString("redirect_uri"));putString("sessionId",value.getString("session_id"));putString("subject",value.getString("sub"))
            }
        } catch(e:BrokerFailure) { error(e.reason) } catch (_:Exception) { error(BrokerError.UNAVAILABLE) }
    }
    override fun addAccount(response: AccountAuthenticatorResponse,accountType: String,authTokenType: String?,requiredFeatures: Array<out String>?,options: Bundle?) = error(BrokerError.LOGIN_REQUIRED)
    override fun editProperties(response: AccountAuthenticatorResponse,accountType: String) = error(BrokerError.UNAUTHORIZED_CLIENT)
    override fun confirmCredentials(response: AccountAuthenticatorResponse,account: Account,options: Bundle?) = error(BrokerError.UNAUTHORIZED_CLIENT)
    override fun updateCredentials(response: AccountAuthenticatorResponse,account: Account,authTokenType: String?,options: Bundle?) = error(BrokerError.UNAUTHORIZED_CLIENT)
    override fun hasFeatures(response: AccountAuthenticatorResponse,account: Account,features: Array<out String>) = Bundle().apply { putBoolean(AccountManager.KEY_BOOLEAN_RESULT,false) }
    override fun getAuthTokenLabel(authTokenType: String) = "Sign in to Spacetime"
    override fun getAccountRemovalAllowed(response: AccountAuthenticatorResponse,account: Account): Bundle {
        BrokerManager.clear(context,true)
        return Bundle().apply { putBoolean(AccountManager.KEY_BOOLEAN_RESULT,true) }
    }
}
class GravitasAuthenticatorService : Service() {
    private val authenticator by lazy { GravitasAuthenticator(this) }
    override fun onBind(intent: Intent): IBinder? = if(intent.action==AccountManager.ACTION_AUTHENTICATOR_INTENT) authenticator.iBinder else null
}
