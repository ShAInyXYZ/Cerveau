package sh.cerveau.app;

import android.app.KeyguardManager;
import android.content.Context;
import android.content.SharedPreferences;
import android.security.keystore.KeyGenParameterSpec;
import android.security.keystore.KeyProperties;
import android.security.keystore.UserNotAuthenticatedException;

import javax.crypto.Cipher;
import javax.crypto.KeyGenerator;
import javax.crypto.SecretKey;
import javax.crypto.spec.GCMParameterSpec;

import java.security.KeyStore;
import java.util.Base64;

/**
 * The bearer token at rest: AES-GCM encrypted with a Keystore key that is
 * bound to the device lock (fingerprint / PIN / pattern). Reading the token
 * back REQUIRES a recent user authentication — so a stolen phone with the
 * app installed still cannot reach the machine.
 *
 * Degrades honestly: if the phone has no lock screen configured at all, the
 * key is created unbound and {@link #isProtected} reports false, so the UI
 * can tell the user their token is not guarded.
 */
public final class Vault {
    private static final String ALIAS = "cerveau.vault";
    private static final String KS = "AndroidKeyStore";
    private static final String PREF_BLOB = "token.enc";
    private static final String PREF_IV = "token.iv";
    /** how long an unlock stays valid before we ask again */
    private static final int AUTH_WINDOW_SECONDS = 300;

    private Vault() {}

    /** True when the device has a lock screen, so the vault can be guarded. */
    public static boolean deviceSecure(Context ctx) {
        KeyguardManager km = ctx.getSystemService(KeyguardManager.class);
        return km != null && km.isDeviceSecure();
    }

    private static SecretKey key(Context ctx, boolean create) throws Exception {
        KeyStore ks = KeyStore.getInstance(KS);
        ks.load(null);
        if (ks.containsAlias(ALIAS)) return (SecretKey) ks.getKey(ALIAS, null);
        if (!create) return null;

        KeyGenerator kg = KeyGenerator.getInstance(KeyProperties.KEY_ALGORITHM_AES, KS);
        KeyGenParameterSpec.Builder b = new KeyGenParameterSpec.Builder(
                ALIAS, KeyProperties.PURPOSE_ENCRYPT | KeyProperties.PURPOSE_DECRYPT)
                .setBlockModes(KeyProperties.BLOCK_MODE_GCM)
                .setEncryptionPaddings(KeyProperties.ENCRYPTION_PADDING_NONE);
        if (deviceSecure(ctx)) {
            // the lock screen IS the decrypt trigger
            b.setUserAuthenticationRequired(true)
             .setUserAuthenticationParameters(AUTH_WINDOW_SECONDS,
                     KeyProperties.AUTH_BIOMETRIC_STRONG | KeyProperties.AUTH_DEVICE_CREDENTIAL);
        }
        kg.init(b.build());
        return kg.generateKey();
    }

    /** Is the stored token actually guarded by the device lock? */
    public static boolean isProtected(Context ctx) {
        return deviceSecure(ctx);
    }

    public static void store(Context ctx, SharedPreferences prefs, String token) throws Exception {
        Cipher c = Cipher.getInstance("AES/GCM/NoPadding");
        c.init(Cipher.ENCRYPT_MODE, key(ctx, true));
        byte[] blob = c.doFinal(token.getBytes("UTF-8"));
        prefs.edit()
                .putString(PREF_BLOB, Base64.getEncoder().encodeToString(blob))
                .putString(PREF_IV, Base64.getEncoder().encodeToString(c.getIV()))
                .apply();
    }

    public static boolean has(SharedPreferences prefs) {
        return prefs.getString(PREF_BLOB, null) != null;
    }

    /**
     * Decrypt the token. Throws {@link UserNotAuthenticatedException} when the
     * caller must show a lock prompt first — that exception IS the signal to
     * unlock, not an error to swallow.
     */
    public static String read(Context ctx, SharedPreferences prefs) throws Exception {
        String blobB64 = prefs.getString(PREF_BLOB, null);
        String ivB64 = prefs.getString(PREF_IV, null);
        if (blobB64 == null || ivB64 == null) return null;
        SecretKey k = key(ctx, false);
        if (k == null) return null;
        Cipher c = Cipher.getInstance("AES/GCM/NoPadding");
        c.init(Cipher.DECRYPT_MODE, k,
                new GCMParameterSpec(128, Base64.getDecoder().decode(ivB64)));
        return new String(c.doFinal(Base64.getDecoder().decode(blobB64)), "UTF-8");
    }

    /** Forget everything — used on unpair. */
    public static void clear(SharedPreferences prefs) {
        prefs.edit().remove(PREF_BLOB).remove(PREF_IV).apply();
        try {
            KeyStore ks = KeyStore.getInstance(KS);
            ks.load(null);
            if (ks.containsAlias(ALIAS)) ks.deleteEntry(ALIAS);
        } catch (Exception ignored) { }
    }
}
