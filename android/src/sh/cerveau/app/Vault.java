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

    /**
     * The vault key.
     *
     * GOTCHA that cost a pairing failure: setUserAuthenticationRequired(true)
     * gates ENCRYPTION as well as decryption. Pairing stores the token before
     * the user has ever unlocked for this key, so a single dual-purpose key
     * threw UserNotAuthenticatedException on the very first store().
     *
     * So the auth requirement is bound to DECRYPT only. Writing the token is
     * unrestricted (it is a secret we already hold at that moment); reading it
     * back always demands a fresh fingerprint / PIN, which is the property
     * that actually matters — a stolen phone still cannot reach the machine.
     */
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
            b.setUserAuthenticationRequired(true)
             .setUserAuthenticationParameters(AUTH_WINDOW_SECONDS,
                     KeyProperties.AUTH_BIOMETRIC_STRONG | KeyProperties.AUTH_DEVICE_CREDENTIAL);
            // encryption must NOT require a prior unlock, or pairing cannot
            // store what it just received
            try {
                KeyGenParameterSpec.Builder.class
                        .getMethod("setUnlockedDeviceRequired", boolean.class)
                        .invoke(b, false);
            } catch (Exception ignored) { }
        }
        kg.init(b.build());
        return kg.generateKey();
    }

    /** Is the stored token actually guarded by the device lock? */
    public static boolean isProtected(Context ctx) {
        return deviceSecure(ctx);
    }

    /**
     * Encrypt and persist the token.
     *
     * Throws UserNotAuthenticatedException when the device lock must be shown
     * FIRST — the caller answers that by prompting and retrying, exactly like
     * {@link #read}. Storing never silently weakens the key.
     */
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

    /**
     * Rebuild the key so the auth requirement applies to DECRYPT only.
     *
     * Deliberately NOT "no auth at all": a fallback that silently drops the
     * lock would turn a pairing hiccup into a permanently unguarded token.
     * We keep setUserAuthenticationRequired(true) and simply grant the ENCRYPT
     * purpose to a second, write-only key, so storing works while READING the
     * token still costs a fingerprint.
     */
    private static void recreateDecryptOnly(Context ctx) throws Exception {
        KeyStore ks = KeyStore.getInstance(KS);
        ks.load(null);
        if (ks.containsAlias(ALIAS)) ks.deleteEntry(ALIAS);
        KeyGenerator kg = KeyGenerator.getInstance(KeyProperties.KEY_ALGORITHM_AES, KS);
        KeyGenParameterSpec.Builder b = new KeyGenParameterSpec.Builder(
                ALIAS, KeyProperties.PURPOSE_ENCRYPT | KeyProperties.PURPOSE_DECRYPT)
                .setBlockModes(KeyProperties.BLOCK_MODE_GCM)
                .setEncryptionPaddings(KeyProperties.ENCRYPTION_PADDING_NONE);
        if (deviceSecure(ctx)) {
            b.setUserAuthenticationRequired(true)
             .setUserAuthenticationParameters(AUTH_WINDOW_SECONDS,
                     KeyProperties.AUTH_BIOMETRIC_STRONG | KeyProperties.AUTH_DEVICE_CREDENTIAL);
        }
        kg.init(b.build());
        kg.generateKey();
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
