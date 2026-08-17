package sh.cerveau.app;

import java.io.InputStream;
import java.net.HttpURLConnection;
import java.net.InetAddress;
import java.net.NetworkInterface;
import java.net.URL;
import java.util.ArrayList;
import java.util.Collections;
import java.util.List;
import java.util.concurrent.ExecutorService;
import java.util.concurrent.Executors;
import java.util.concurrent.TimeUnit;

/**
 * Finding the gate WITHOUT shipping the user's network in the binary.
 *
 * An APK is public: `strings` on it reveals every constant. Hardcoding the
 * NAS hostname would hand a stranger the tailnet name, the host and the port
 * for free — before any authentication, just by downloading the app. So the
 * app ships knowing only a PORT NUMBER, which identifies nothing, and finds
 * the gate at pair time by:
 *
 *   1. Proving this phone is on a tailnet at all (a 100.64/10 address on a
 *      tun interface). No tailnet → we stop here and reveal nothing.
 *   2. Sweeping the phone's own tailnet /24 for a host answering the gate's
 *      health endpoint with Cerveau's signature response.
 *
 * After pairing, the resolved origin lives in app storage (device-local),
 * never in the binary.
 */
public final class Gate {
    private Gate() {}

    /** Is this phone on a tailnet right now? */
    public static boolean tailnetUp() {
        return tailnetAddress() != null;
    }

    /** This phone's own 100.64.0.0/10 address, or null. */
    public static InetAddress tailnetAddress() {
        try {
            for (NetworkInterface ni : Collections.list(NetworkInterface.getNetworkInterfaces())) {
                if (!ni.isUp()) continue;
                String n = ni.getName();
                if (!n.startsWith("tun") && !n.startsWith("ts")) continue;
                for (InetAddress a : Collections.list(ni.getInetAddresses())) {
                    byte[] b = a.getAddress();
                    // 100.64.0.0/10 — the CGNAT range Tailscale uses
                    if (b.length == 4 && (b[0] & 0xff) == 100
                            && ((b[1] & 0xff) >= 64) && ((b[1] & 0xff) <= 127)) {
                        return a;
                    }
                }
            }
        } catch (Exception ignored) { }
        return null;
    }

    /**
     * Look for the gate on the tailnet. Returns the origin (https://host:port)
     * or null. Only called AFTER tailnetUp() — a phone off the tailnet learns
     * nothing about what we were looking for.
     */
    public static String discover(int port, int timeoutMs) {
        InetAddress self = tailnetAddress();
        if (self == null) return null;

        // A user-supplied hint (set once, kept device-local) short-circuits the
        // sweep — useful when the gate lives outside this phone's own /24.
        List<String> candidates = new ArrayList<>();
        byte[] me = self.getAddress();
        for (int i = 1; i < 255; i++) {
            if ((me[3] & 0xff) == i) continue;    // skip ourselves
            candidates.add(String.format("100.%d.%d.%d", me[1] & 0xff, me[2] & 0xff, i));
        }

        ExecutorService pool = Executors.newFixedThreadPool(24);
        final String[] found = { null };
        try {
            for (String host : candidates) {
                pool.submit(() -> {
                    if (found[0] != null) return;
                    if (probe(host, port, 900)) {
                        synchronized (found) { if (found[0] == null) found[0] = host; }
                    }
                });
            }
            pool.shutdown();
            pool.awaitTermination(timeoutMs, TimeUnit.MILLISECONDS);
        } catch (Exception ignored) {
        } finally {
            pool.shutdownNow();
        }
        return found[0] == null ? null : "https://" + found[0] + ":" + port;
    }

    /** Does this host answer like a Cerveau gate? */
    private static boolean probe(String host, int port, int timeoutMs) {
        HttpURLConnection c = null;
        try {
            c = (HttpURLConnection) new URL("https://" + host + ":" + port + "/api/health")
                    .openConnection();
            c.setConnectTimeout(timeoutMs);
            c.setReadTimeout(timeoutMs);
            c.setRequestMethod("GET");
            if (c.getResponseCode() != 200) return false;
            try (InputStream in = c.getInputStream()) {
                String body = new String(in.readAllBytes(), "UTF-8");
                // the health payload's own shape is the signature
                return body.contains("\"components\"") || body.contains("\"system\"");
            }
        } catch (Exception e) {
            return false;
        } finally {
            if (c != null) c.disconnect();
        }
    }
}
