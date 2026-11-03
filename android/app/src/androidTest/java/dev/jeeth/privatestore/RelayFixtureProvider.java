package dev.jeeth.privatestore;

import android.content.ContentProvider;
import android.content.ContentValues;
import android.content.SharedPreferences;
import android.database.Cursor;
import android.net.Uri;
import android.os.Bundle;

/** The standalone test-APK process has only framework classes on its path. */
public final class RelayFixtureProvider extends ContentProvider {
    @Override public boolean onCreate() { return true; }

    @Override public synchronized Bundle call(String method, String arg, Bundle extras) {
        SharedPreferences p = getContext().getSharedPreferences("relay-fixture", 0);
        Bundle result = new Bundle();
        if ("reset".equals(method)) {
            p.edit().clear().putBoolean("failFirst", extras != null && extras.getBoolean("failFirst")).commit();
        } else if ("notify".equals(method)) {
            if (extras == null || extras.getString("id") == null) throw new IllegalArgumentException("Missing fixture id");
            String id = extras.getString("id");
            int attempts = p.getInt("attempts:" + id, 0) + 1;
            p.edit().putInt("attempts:" + id, attempts).commit();
            if ("1".equals(id) && attempts == 1 && p.getBoolean("failFirst", false)) {
                throw new IllegalArgumentException("Temporary fixture provider failure");
            }
            p.edit().putBoolean("accepted:" + id, true).commit();
            result.putBoolean("accepted", true);
        } else if ("status".equals(method)) {
            result.putBoolean("accepted", p.getBoolean("accepted:" + arg, false));
            result.putInt("attempts", p.getInt("attempts:" + arg, 0));
        } else {
            throw new IllegalArgumentException("Unknown fixture operation");
        }
        return result;
    }

    @Override public Cursor query(Uri u, String[] p, String s, String[] a, String o) { throw new UnsupportedOperationException(); }
    @Override public String getType(Uri u) { return null; }
    @Override public Uri insert(Uri u, ContentValues v) { throw new UnsupportedOperationException(); }
    @Override public int delete(Uri u, String s, String[] a) { throw new UnsupportedOperationException(); }
    @Override public int update(Uri u, ContentValues v, String s, String[] a) { throw new UnsupportedOperationException(); }
}
