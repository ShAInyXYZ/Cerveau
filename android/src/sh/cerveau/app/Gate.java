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
     * Fallback discovery when the user typed a code instead of scanning.
     *
     * NOTE: Tailscale does NOT allocate addresses by subnet — two nodes on the
     * same tailnet routinely differ in the second and third octet — so a /24
     * sweep of the phone's own range is not a reliable way to find the gate.
     * It is kept only as a last resort; the INVITATION (QR or the gate the
     * code was minted by) is the real answer.
     */
    public static String discover(int timeoutMs) {
        List<String> hosts = peerCandidates();
        if (hosts.isEmpty()) return null;

        // Try the NAS gate (https) and the machine doorbell (http) — whichever
        // this phone can actually reach. The invitation is still the reliable
        // path; this only helps when the user typed a code with no scan.
        List<String> origins = new ArrayList<>();
        for (String h : hosts) {
            origins.add("https://" + h + ":7443");
            origins.add("http://" + h + ":7701");
        }

        ExecutorService pool = Executors.newFixedThreadPool(16);
        final String[] found = { null };
        try {
            for (String origin : origins) {
                pool.submit(() -> {
                    if (found[0] != null) return;
                    if (probe(origin, 1500)) {
                        synchronized (found) { if (found[0] == null) found[0] = origin; }
                    }
                });
            }
            pool.shutdown();
            pool.awaitTermination(timeoutMs, TimeUnit.MILLISECONDS);
        } catch (Exception ignored) {
        } finally {
            pool.shutdownNow();
        }
        return found[0];
    }

    /**
     * Tailnet peers this phone can plausibly reach.
     *
     * PROVEN LIMITATION (measured on-device): on Android, Tailscale is a
     * USERSPACE VPN — its peers never appear in /proc/net/arp, and the phone's
     * own /24 says nothing about where other nodes live (the NAS is 100.116.x
     * while the phone is 100.106.x). So there is no reliable way for the app
     * to DISCOVER the gate by itself. This returns whatever is visible and is
     * expected to come back empty; the INVITATION carrying the address is the
     * real mechanism, which is why the QR/full link exists.
     */
    private static List<String> peerCandidates() {
        List<String> out = new ArrayList<>();
        try (java.io.BufferedReader r = new java.io.BufferedReader(
                new java.io.FileReader("/proc/net/arp"))) {
            String line;
            while ((line = r.readLine()) != null) {
                String[] f = line.trim().split("\\s+");
                if (f.length > 0 && f[0].startsWith("100.")) out.add(f[0]);
            }
        } catch (Exception ignored) { }
        return out;
    }

    /** Does this origin answer like a Cerveau gate? */
    private static boolean probe(String origin, int timeoutMs) {
        HttpURLConnection c = null;
        try {
            c = (HttpURLConnection) new URL(origin + "/api/health").openConnection();
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
