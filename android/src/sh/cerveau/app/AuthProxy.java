package sh.cerveau.app;

import com.sun.net.httpserver.HttpExchange;
import com.sun.net.httpserver.HttpServer;

import java.io.InputStream;
import java.io.OutputStream;
import java.net.HttpURLConnection;
import java.net.InetAddress;
import java.net.InetSocketAddress;
import java.net.URL;
import java.util.Base64;
import java.util.List;
import java.util.Map;

/**
 * A localhost bridge between the WebView and the machine.
 *
 * The panel's JavaScript must never hold the bearer token, and it cannot
 * reach the Android Keystore to sign anything. So the app runs this tiny
 * HTTP server on 127.0.0.1:<ephemeral>, points the WebView at it, and
 * signs + authorizes every request on the way out:
 *
 *   WebView → 127.0.0.1:port → [ + Bearer token
 *                                + X-Cerveau-Device / -Nonce / -Sig ] → core
 *
 * The token exists only in this process's memory (decrypted from the Vault
 * after the user unlocks). Nothing on the page can read it.
 */
public final class AuthProxy {
    private final String base;      // e.g. http://100.90.163.54:7701
    private final String token;
    private final String deviceId;
    private HttpServer server;

    public AuthProxy(String base, String token, String deviceId) {
        this.base = base.replaceAll("/+$", "");
        this.token = token;
        this.deviceId = deviceId;
    }

    /** Start on a free loopback port; returns the local origin to load. */
    public String start() throws Exception {
        server = HttpServer.create(
                new InetSocketAddress(InetAddress.getByName("127.0.0.1"), 0), 0);
        server.createContext("/", this::handle);
        server.setExecutor(java.util.concurrent.Executors.newFixedThreadPool(4));
        server.start();
        return "http://127.0.0.1:" + server.getAddress().getPort();
    }

    public void stop() {
        if (server != null) server.stop(0);
    }

    private void handle(HttpExchange ex) {
        try {
            String path = ex.getRequestURI().toString();
            HttpURLConnection c = (HttpURLConnection) new URL(base + path).openConnection();
            c.setRequestMethod(ex.getRequestMethod());
            c.setConnectTimeout(10000);
            c.setReadTimeout(600000);   // long turns stream for minutes
            c.setInstanceFollowRedirects(false);

            // forward the client's headers (content-type, accept, …)
            for (Map.Entry<String, List<String>> h : ex.getRequestHeaders().entrySet()) {
                String k = h.getKey();
                if (k.equalsIgnoreCase("Host") || k.equalsIgnoreCase("Connection")) continue;
                for (String v : h.getValue()) c.addRequestProperty(k, v);
            }

            // the credentials the page never sees
            c.setRequestProperty("Authorization", "Bearer " + token);
            attachDeviceSignature(c);

            if (!"GET".equals(ex.getRequestMethod()) && !"HEAD".equals(ex.getRequestMethod())) {
                c.setDoOutput(true);
                try (OutputStream os = c.getOutputStream()) {
                    ex.getRequestBody().transferTo(os);
                }
            }

            int code = c.getResponseCode();
            for (Map.Entry<String, List<String>> h : c.getHeaderFields().entrySet()) {
                if (h.getKey() == null) continue;
                String k = h.getKey();
                if (k.equalsIgnoreCase("Transfer-Encoding") || k.equalsIgnoreCase("Content-Length")
                        || k.equalsIgnoreCase("Connection")) continue;
                for (String v : h.getValue()) ex.getResponseHeaders().add(k, v);
            }
            ex.sendResponseHeaders(code, 0);
            try (InputStream in = code >= 400 ? c.getErrorStream() : c.getInputStream();
                 OutputStream out = ex.getResponseBody()) {
                if (in != null) {
                    // small buffer + flush per chunk so SSE (live tool steps)
                    // reaches the WebView as it happens instead of at EOF
                    byte[] buf = new byte[1024];
                    int n;
                    while ((n = in.read(buf)) > 0) { out.write(buf, 0, n); out.flush(); }
                }
            }
        } catch (Exception e) {
            try { ex.sendResponseHeaders(502, -1); } catch (Exception ignored) { }
        } finally {
            ex.close();
        }
    }

    /** Fetch a one-shot nonce and sign it with the Keystore key. */
    private void attachDeviceSignature(HttpURLConnection c) {
        try {
            HttpURLConnection n = (HttpURLConnection) new URL(base + "/api/nonce").openConnection();
            n.setRequestProperty("Authorization", "Bearer " + token);
            n.setConnectTimeout(8000);
            n.setReadTimeout(8000);
            if (n.getResponseCode() != 200) return;
            String body = new String(n.getInputStream().readAllBytes(), "UTF-8");
            String nonce = jsonField(body, "nonce");
            if (nonce == null) return;
            String sig = DeviceKey.sign(Base64.getDecoder().decode(nonce));
            c.setRequestProperty("X-Cerveau-Device", deviceId);
            c.setRequestProperty("X-Cerveau-Nonce", nonce);
            c.setRequestProperty("X-Cerveau-Sig", sig);
        } catch (Exception ignored) {
            // no signature → the core answers 403 and the app shows the gate
        }
    }

    static String jsonField(String json, String field) {
        String needle = "\"" + field + "\"";
        int i = json.indexOf(needle);
        if (i < 0) return null;
        int q1 = json.indexOf('"', i + needle.length() + 1);
        int q2 = json.indexOf('"', q1 + 1);
        if (q1 < 0 || q2 < 0) return null;
        return json.substring(q1 + 1, q2);
    }
}
