package sh.cerveau.app;

import java.io.ByteArrayOutputStream;
import java.io.InputStream;
import java.io.OutputStream;
import java.net.HttpURLConnection;
import java.net.URL;
import java.util.Base64;

/**
 * Letting an ALREADY-TRUSTED phone admit another device.
 *
 * The phone holds the same credential the machine does, so it can mint a
 * pairing invitation on the machine's behalf: the user is away from the
 * desktop, wants the laptop in, and approves it from the phone instead.
 *
 * What this does NOT do is grant reachability. The invitation names a
 * tailnet address, so the new device still has to be on the tailnet for
 * that address to resolve — pairing hands over a CREDENTIAL, not a route.
 *
 * Every request here is signed with the Keystore key, which is what makes
 * the resulting "approved_by" record trustworthy on the server side.
 */
final class Vouch {
    private Vouch() {}

    /** An invitation minted for another device. */
    static final class Invite {
        final String slug, code;
        Invite(String slug, String code) { this.slug = slug; this.code = code; }
    }

    /**
     * Ask the machine to mint an invitation for a new device.
     * @param base   the gate origin this phone is already paired to
     * @param token  the bearer token from the vault
     */
    static Invite mint(String base, String token, String deviceId) throws Exception {
        HttpURLConnection c = (HttpURLConnection) new URL(base + "/api/pair/invite").openConnection();
        c.setRequestMethod("POST");
        c.setConnectTimeout(8000);
        c.setReadTimeout(8000);
        c.setDoOutput(true);
        c.setRequestProperty("Authorization", "Bearer " + token);
        signRequest(c, base, deviceId);
        c.getOutputStream().close();

        int code = c.getResponseCode();
        if (code != 200) throw new Exception("machine refused to mint an invitation (HTTP " + code + ")");
        String body = read(c.getInputStream());
        String slug = field(body, "slug");
        String pin  = field(body, "code");
        if (slug == null || pin == null) throw new Exception("invitation was malformed");
        return new Invite(slug, pin);
    }

    /**
     * Prove to the machine that THIS phone is approving the enrollment.
     * Returns the nonce/signature pair the new device must forward, so the
     * server can record who vouched. Without this the approval trail would
     * be a caller-supplied claim, which is worth nothing.
     */
    static String[] approvalProof(String base, String deviceId) throws Exception {
        HttpURLConnection n = (HttpURLConnection) new URL(base + "/api/nonce").openConnection();
        n.setConnectTimeout(8000);
        n.setReadTimeout(8000);
        String nonce = field(read(n.getInputStream()), "nonce");
        if (nonce == null) throw new Exception("no nonce");
        String sig = DeviceKey.sign(Base64.getDecoder().decode(nonce));
        return new String[]{ deviceId, nonce, sig };
    }

    /** attach this device's identity + a fresh signature to a request */
    private static void signRequest(HttpURLConnection c, String base, String deviceId) {
        try {
            HttpURLConnection n = (HttpURLConnection) new URL(base + "/api/nonce").openConnection();
            n.setConnectTimeout(8000);
            n.setReadTimeout(8000);
            String nonce = field(read(n.getInputStream()), "nonce");
            if (nonce == null) return;
            String sig = DeviceKey.sign(Base64.getDecoder().decode(nonce));
            c.setRequestProperty("X-Cerveau-Device", deviceId);
            c.setRequestProperty("X-Cerveau-Nonce", nonce);
            c.setRequestProperty("X-Cerveau-Sig", sig);
        } catch (Exception ignored) {
            // unsigned: the server will reject with 403 and the caller reports it
        }
    }

    private static String read(InputStream in) throws Exception {
        ByteArrayOutputStream out = new ByteArrayOutputStream();
        byte[] buf = new byte[4096];
        int n;
        while ((n = in.read(buf)) > 0) out.write(buf, 0, n);
        in.close();
        return out.toString("UTF-8");
    }

    /** minimal JSON string-field reader — the payloads here are tiny and flat */
    private static String field(String json, String key) {
        String needle = "\"" + key + "\"";
        int i = json.indexOf(needle);
        if (i < 0) return null;
        int c = json.indexOf(':', i + needle.length());
        if (c < 0) return null;
        int q1 = json.indexOf('"', c);
        if (q1 < 0) return null;
        int q2 = json.indexOf('"', q1 + 1);
        if (q2 < 0) return null;
        return json.substring(q1 + 1, q2);
    }
}
