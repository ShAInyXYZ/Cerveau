package sh.cerveau.app;

import java.io.BufferedOutputStream;
import java.io.ByteArrayOutputStream;
import java.io.InputStream;
import java.io.OutputStream;
import java.net.HttpURLConnection;
import java.net.InetAddress;
import java.net.ServerSocket;
import java.net.Socket;
import java.net.URL;
import java.util.ArrayList;
import java.util.Base64;
import java.util.List;
import java.util.Locale;
import java.util.Map;
import java.util.concurrent.ExecutorService;
import java.util.concurrent.Executors;

/**
 * A localhost bridge between the WebView and the machine.
 *
 * The panel's JavaScript must never hold the bearer token, and it cannot reach
 * the Android Keystore to sign anything. So the app listens on
 * 127.0.0.1:&lt;ephemeral&gt;, points the WebView at it, and adds the credentials
 * on the way out:
 *
 *   WebView → 127.0.0.1:port → [ + Bearer token
 *                                + X-Cerveau-Device / -Nonce / -Sig ] → gate
 *
 * The token exists only in this process's memory (decrypted from the Vault
 * after the user unlocks). Nothing on the page can read it.
 *
 * IMPLEMENTATION NOTE: this is a hand-rolled HTTP/1.1 loop on a raw
 * ServerSocket, NOT com.sun.net.httpserver — that package is JDK-only. It
 * compiles fine against the desktop android.jar and then throws
 * NoClassDefFoundError at runtime on the device, which crashed the panel the
 * moment it opened.
 */
public final class AuthProxy {
    private final String base;      // the paired gate origin, from app storage
    private final String token;
    private final String deviceId;
    private ServerSocket server;
    private ExecutorService pool;
    private volatile boolean running;

    public AuthProxy(String base, String token, String deviceId) {
        this.base = base.replaceAll("/+$", "");
        this.token = token;
        this.deviceId = deviceId;
    }

    /** Start on a free loopback port; returns the local origin to load. */
    public String start() throws Exception {
        server = new ServerSocket(0, 16, InetAddress.getByName("127.0.0.1"));
        pool = Executors.newCachedThreadPool();
        running = true;
        Thread accept = new Thread(() -> {
            while (running) {
                try {
                    Socket s = server.accept();
                    pool.submit(() -> handle(s));
                } catch (Exception e) {
                    if (running) continue;
                    break;
                }
            }
        });
        accept.setDaemon(true);
        accept.start();
        return "http://127.0.0.1:" + server.getLocalPort();
    }

    public void stop() {
        running = false;
        try { if (server != null) server.close(); } catch (Exception ignored) { }
        if (pool != null) pool.shutdownNow();
    }

    private void handle(Socket sock) {
        try (Socket s = sock;
             InputStream in = s.getInputStream();
             OutputStream rawOut = s.getOutputStream()) {
            BufferedOutputStream out = new BufferedOutputStream(rawOut);

            // ── request line + headers ──
            String requestLine = readLine(in);
            if (requestLine == null || requestLine.isEmpty()) return;
            String[] parts = requestLine.split(" ");
            if (parts.length < 2) return;
            String method = parts[0], path = parts[1];

            List<String[]> headers = new ArrayList<>();
            int contentLength = 0;
            for (String line; (line = readLine(in)) != null && !line.isEmpty(); ) {
                int c = line.indexOf(':');
                if (c <= 0) continue;
                String k = line.substring(0, c).trim(), v = line.substring(c + 1).trim();
                if (k.equalsIgnoreCase("Content-Length")) {
                    try { contentLength = Integer.parseInt(v); } catch (Exception ignored) { }
                }
                if (k.equalsIgnoreCase("Host") || k.equalsIgnoreCase("Connection")
                        || k.equalsIgnoreCase("Accept-Encoding")) continue;
                headers.add(new String[]{ k, v });
            }

            byte[] body = new byte[0];
            if (contentLength > 0) {
                body = new byte[contentLength];
                int read = 0;
                while (read < contentLength) {
                    int n = in.read(body, read, contentLength - read);
                    if (n < 0) break;
                    read += n;
                }
            }

            // ── upstream call, with the credentials the page never sees ──
            HttpURLConnection c = (HttpURLConnection) new URL(base + path).openConnection();
            c.setRequestMethod(method);
            c.setConnectTimeout(10000);
            c.setReadTimeout(600000);        // long turns stream for minutes
            c.setInstanceFollowRedirects(false);
            for (String[] h : headers) c.addRequestProperty(h[0], h[1]);
            c.setRequestProperty("Authorization", "Bearer " + token);
            attachDeviceSignature(c);
            if (body.length > 0) {
                c.setDoOutput(true);
                try (OutputStream os = c.getOutputStream()) { os.write(body); }
            }

            int status = c.getResponseCode();
            StringBuilder head = new StringBuilder();
            head.append("HTTP/1.1 ").append(status).append(" ")
                .append(status < 400 ? "OK" : "ERR").append("\r\n");
            String ctype = c.getContentType();
            if (ctype != null) head.append("Content-Type: ").append(ctype).append("\r\n");
            // stream everything: SSE (live tool steps) must arrive as it happens
            head.append("Cache-Control: no-store\r\n");
            head.append("Connection: close\r\n\r\n");
            out.write(head.toString().getBytes("UTF-8"));
            out.flush();

            try (InputStream up = status >= 400 ? c.getErrorStream() : c.getInputStream()) {
                if (up != null) {
                    byte[] buf = new byte[1024];
                    for (int n; (n = up.read(buf)) > 0; ) { out.write(buf, 0, n); out.flush(); }
                }
            }
            out.flush();
        } catch (Exception ignored) {
            // a dropped connection is normal when the WebView navigates away
        }
    }

    private static String readLine(InputStream in) throws Exception {
        ByteArrayOutputStream b = new ByteArrayOutputStream();
        int prev = -1;
        for (int ch; (ch = in.read()) >= 0; ) {
            if (ch == '\n') break;
            if (prev == '\r') b.write(prev);
            if (ch != '\r') b.write(ch);
            prev = ch;
        }
        return b.size() == 0 && prev < 0 ? null : new String(b.toByteArray(), "UTF-8");
    }

    /** Fetch a one-shot nonce and sign it with the Keystore key. */
    private void attachDeviceSignature(HttpURLConnection c) {
        try {
            HttpURLConnection n = (HttpURLConnection) new URL(base + "/api/nonce").openConnection();
            n.setRequestProperty("Authorization", "Bearer " + token);
            n.setConnectTimeout(8000);
            n.setReadTimeout(8000);
            if (n.getResponseCode() != 200) return;
            String bodyText = new String(n.getInputStream().readAllBytes(), "UTF-8");
            String nonce = jsonField(bodyText, "nonce");
            if (nonce == null) return;
            String sig = DeviceKey.sign(Base64.getDecoder().decode(nonce));
            c.setRequestProperty("X-Cerveau-Device", deviceId);
            c.setRequestProperty("X-Cerveau-Nonce", nonce);
            c.setRequestProperty("X-Cerveau-Sig", sig);
        } catch (Exception ignored) {
            // no signature → the gate answers 403 and the app shows it
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
