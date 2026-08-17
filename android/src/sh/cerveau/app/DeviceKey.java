package sh.cerveau.app;

import android.security.keystore.KeyGenParameterSpec;
import android.security.keystore.KeyProperties;

import java.security.KeyPairGenerator;
import java.security.KeyStore;
import java.security.PrivateKey;
import java.security.Signature;
import java.security.cert.Certificate;
import java.util.Base64;

/**
 * The device's identity: a P-256 keypair generated INSIDE the Android
 * Keystore. The private key never exists in app memory and cannot be
 * exported — copying the app's files to another phone yields an identity
 * that cannot sign, which is exactly the "cannot be replicated" property
 * the pairing model needs.
 *
 * The server registers the public key at pairing and afterwards challenges
 * every request with a nonce we sign here (SHA256withECDSA).
 */
public final class DeviceKey {
    private static final String ALIAS = "cerveau.device";
    private static final String KS = "AndroidKeyStore";

    private DeviceKey() {}

    /** Create the keypair if this phone doesn't have one yet. */
    public static void ensure() throws Exception {
        KeyStore ks = KeyStore.getInstance(KS);
        ks.load(null);
        if (ks.containsAlias(ALIAS)) return;

        KeyPairGenerator g = KeyPairGenerator.getInstance(
                KeyProperties.KEY_ALGORITHM_EC, KS);
        g.initialize(new KeyGenParameterSpec.Builder(ALIAS, KeyProperties.PURPOSE_SIGN)
                .setDigests(KeyProperties.DIGEST_SHA256)
                .setAlgorithmParameterSpec(new java.security.spec.ECGenParameterSpec("secp256r1"))
                // deliberately NOT user-auth-bound: the signing key proves WHICH
                // DEVICE this is on every request. The fingerprint guards the
                // token at rest (see Vault) — two separate jobs.
                .build());
        g.generateKeyPair();
    }

    /** Base64 SPKI public key — what the server stores at pairing. */
    public static String publicKeyB64() throws Exception {
        KeyStore ks = KeyStore.getInstance(KS);
        ks.load(null);
        Certificate cert = ks.getCertificate(ALIAS);
        if (cert == null) throw new IllegalStateException("no device key");
        return Base64.getEncoder().encodeToString(cert.getPublicKey().getEncoded());
    }

    /** Sign a server nonce (raw bytes) — returns base64 ASN.1 DER signature. */
    public static String sign(byte[] nonce) throws Exception {
        KeyStore ks = KeyStore.getInstance(KS);
        ks.load(null);
        PrivateKey pk = (PrivateKey) ks.getKey(ALIAS, null);
        if (pk == null) throw new IllegalStateException("no device key");
        Signature s = Signature.getInstance("SHA256withECDSA");
        s.initSign(pk);
        s.update(nonce);
        return Base64.getEncoder().encodeToString(s.sign());
    }

    /** Wipe the identity (used when unpairing). */
    public static void clear() {
        try {
            KeyStore ks = KeyStore.getInstance(KS);
            ks.load(null);
            if (ks.containsAlias(ALIAS)) ks.deleteEntry(ALIAS);
        } catch (Exception ignored) { }
    }
}
