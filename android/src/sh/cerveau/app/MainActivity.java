package sh.cerveau.app;

import android.app.Activity;
import android.app.KeyguardManager;
import android.content.Intent;
import android.content.SharedPreferences;
import android.graphics.Color;
import android.graphics.drawable.GradientDrawable;
import android.os.Bundle;
import android.os.Handler;
import android.os.Looper;
import android.util.TypedValue;
import android.view.Gravity;
import android.view.View;
import android.view.WindowInsets;
import android.view.inputmethod.InputMethodManager;
import android.widget.Button;
import android.widget.EditText;
import android.widget.ImageView;
import android.widget.LinearLayout;
import android.widget.ScrollView;
import android.widget.TextView;

import org.json.JSONObject;

import java.io.BufferedReader;
import java.io.InputStream;
import java.io.InputStreamReader;
import java.io.OutputStream;
import java.net.HttpURLConnection;
import java.net.InetAddress;
import java.net.NetworkInterface;
import java.net.URL;
import java.util.Collections;

/**
 *  Cerveau — the portal.
 *
 *  Two states, decided at launch:
 *    NOT PAIRED → the pairing form (machine URL + 6-char code from the core's
 *                 console). Pairing registers this phone's Keystore public key
 *                 with the machine; the identity cannot be copied to another
 *                 device because the private key never leaves this TEE.
 *    PAIRED     → the lock screen. The token is encrypted at rest behind the
 *                 device lock, so opening the app asks for fingerprint (or PIN
 *                 / pattern) and only then opens the panel.
 *
 *  Five wrong pairing IDs lock the form for 15 minutes.
 */
public class MainActivity extends Activity {
    public static final String PREFS = "cerveau";
    // Mirrors panel/src/tokens.css exactly — the app and the panel are one
    // product, so the shell must not drift from the UI it hosts.
    static final String BG = "#09090B", S1 = "#0F0F11", S2 = "#1B1B1E";
    static final String LINE = "#262629", LINE2 = "#2C2C30";
    static final String TEXT = "#FAFAFA", MUTED = "#A1A1AA", DIM = "#71717A", FAINT = "#52525B";
    static final String ACCENT = "#E54866", ERR = "#e6533f", OK = "#4bb894";

    /** The only address this app ever talks to: the NAS gate, real TLS via
     *  `tailscale serve`. The machine is never addressed directly, and there
     *  is no plaintext-http field for the user to get wrong. */
    static final String GATE = "https://shiny-nas.tail58d4d4.ts.net:7443";

    private static final int MAX_TRIES = 5;
    private static final long LOCKOUT_MS = 15 * 60 * 1000L;
    private static final int REQ_UNLOCK = 4711;

    private SharedPreferences prefs;
    private final Handler ui = new Handler(Looper.getMainLooper());

    @Override protected void onCreate(Bundle b) {
        super.onCreate(b);
        getWindow().setStatusBarColor(Color.parseColor(BG));
        getWindow().setNavigationBarColor(Color.parseColor(BG));
        prefs = getSharedPreferences(PREFS, MODE_PRIVATE);
        route();
    }

    @Override protected void onResume() {
        super.onResume();
        // returning from the panel must land on the lock screen, never straight in
        if (isPaired()) showLock();
    }

    private boolean isPaired() {
        return prefs.getString("url", null) != null && Vault.has(prefs);
    }

    private void route() {
        if (lockRemaining() > 0) { showLockedOut(); return; }
        if (isPaired()) showLock(); else showPortal();
    }

    // ── state 1: paired → unlock to continue ─────────────────────────
    private void showLock() {
        LinearLayout root = column();
        root.addView(brandMark());
        root.addView(label("CERVEAU", TEXT, 20));
        TextView sub = text(Vault.isProtected(this)
                ? "unlock to reach your machine"
                : "no device lock set — your token is unguarded", MUTED);
        root.addView(sub);

        Button unlock = button(Vault.isProtected(this) ? "UNLOCK" : "OPEN");
        TextView status = text("", MUTED);
        unlock.setOnClickListener(v -> openPanel(status));
        root.addView(unlock);
        root.addView(status);

        TextView unpair = text("unpair this device", FAINT);
        unpair.setPadding(0, dp(28), 0, 0);
        unpair.setOnClickListener(v -> {
            Vault.clear(prefs);
            DeviceKey.clear();
            prefs.edit().remove("url").remove("device_id").apply();
            showPortal();
        });
        root.addView(unpair);
        setContentView(scroll(root));

        // straight to the biometric prompt — one tap less on every launch
        ui.postDelayed(() -> openPanel(status), 200);
    }

    /**
     * Decrypt the token (the Keystore demands a fresh unlock) and open the
     * panel. UserNotAuthenticatedException is the SIGNAL to show the device
     * lock prompt, not a failure.
     */
    private void openPanel(TextView status) {
        try {
            String token = Vault.read(this, prefs);
            if (token == null) { showPortal(); return; }
            Intent i = new Intent(this, PanelActivity.class);
            i.putExtra("token", token);
            i.putExtra("url", prefs.getString("url", ""));
            i.putExtra("device_id", prefs.getString("device_id", ""));
            startActivity(i);
        } catch (android.security.keystore.UserNotAuthenticatedException e) {
            promptUnlock();
        } catch (Exception e) {
            status.setTextColor(Color.parseColor(ERR));
            status.setText("could not open the vault — unpair and pair again");
        }
    }

    /** Device-lock prompt: fingerprint, or PIN/pattern as the fallback. */
    private void promptUnlock() {
        KeyguardManager km = getSystemService(KeyguardManager.class);
        if (km == null || !km.isDeviceSecure()) { showPortal(); return; }
        Intent i = km.createConfirmDeviceCredentialIntent(
                "Cerveau", "unlock to reach your machine");
        if (i != null) startActivityForResult(i, REQ_UNLOCK);
    }

    @Override protected void onActivityResult(int req, int res, Intent data) {
        super.onActivityResult(req, res, data);
        if (req == REQ_UNLOCK && res == RESULT_OK) openPanel(text("", MUTED));
    }

    // ── state 2: not paired → the pairing form ───────────────────────
    private void showPortal() {
        LinearLayout root = column();
        root.addView(brandMark());
        root.addView(label("CERVEAU", TEXT, 20));
        root.addView(text("this device is not paired yet", MUTED));

        Button startPair = button("PAIR DEVICE");
        TextView status = text("", MUTED);
        root.addView(startPair);
        root.addView(status);
        startPair.setOnClickListener(v -> showPairStep(status));
        setContentView(scroll(root));
    }

    /**
     * Step 2 of pairing: the machine has minted a short-lived code and is
     * showing it on its own screen (over the tailnet). The user reads it there
     * and types it here — the code authorizes THIS device's public key.
     */
    private void showPairStep(TextView unusedStatus) {
        LinearLayout root = column();
        root.addView(brandMark());
        root.addView(label("PAIR", TEXT, 20));
        root.addView(text("on your machine, open\n" + pairPageHint()
                + "\nand type the 6-character code it shows", MUTED));

        EditText codeField = field("6-character code", "");
        codeField.setAllCaps(true);
        Button confirm = button("CONFIRM");
        TextView status = text("", MUTED);

        confirm.setOnClickListener(v -> {
            hideKeyboard(v);
            String code = codeField.getText().toString().trim().toUpperCase();
            if (code.length() != 6) { fail(status, confirm, "the code is 6 characters"); return; }
            status.setTextColor(Color.parseColor(MUTED));
            status.setText("handshaking…");
            confirm.setEnabled(false);
            new Thread(() -> {
                if (!tailscaleUp()) {
                    ui.post(() -> fail(status, confirm,
                            "tailscale is not up on this phone\nstart it, then retry"));
                    return;
                }
                try {
                    DeviceKey.ensure();
                    String[] got = pair(GATE, code, DeviceKey.publicKeyB64());
                    if (got == null) {
                        int tries = prefs.getInt("tries", 0) + 1;
                        prefs.edit().putInt("tries", tries).apply();
                        if (tries >= MAX_TRIES) {
                            prefs.edit().putLong("lock_until",
                                    System.currentTimeMillis() + LOCKOUT_MS).apply();
                            ui.post(this::showLockedOut);
                            return;
                        }
                        ui.post(() -> fail(status, confirm,
                                "wrong or expired code — " + (MAX_TRIES - tries) + " tries left"));
                        return;
                    }
                    prefs.edit().putString("url", GATE)
                            .putString("device_id", got[1])
                            .putInt("tries", 0).apply();
                    Vault.store(this, prefs, got[0]);
                    ui.post(() -> {
                        status.setTextColor(Color.parseColor(OK));
                        status.setText("paired — this device is now known to your machine");
                        ui.postDelayed(this::showLock, 800);
                    });
                } catch (Exception e) {
                    ui.post(() -> fail(status, confirm, "pairing error: " + e.getMessage()));
                }
            }).start();
        });

        root.addView(codeField);
        root.addView(confirm);
        root.addView(status);
        TextView back = text("back", FAINT);
        back.setPadding(0, dp(24), 0, 0);
        back.setOnClickListener(v -> showPortal());
        root.addView(back);
        setContentView(scroll(root));
    }

    private String pairPageHint() { return GATE + "/pair"; }

    private void showLockedOut() {
        LinearLayout root = column();
        root.addView(brandMark());
        root.addView(label("LOCKED", TEXT, 20));
        root.addView(text("too many wrong pairing ids\ntry again in "
                + (lockRemaining() / 60000 + 1) + " min", MUTED));
        setContentView(scroll(root));
        ui.postDelayed(this::route, 30000);
    }

    private long lockRemaining() {
        return Math.max(0, prefs.getLong("lock_until", 0) - System.currentTimeMillis());
    }

    private void fail(TextView status, Button pair, String msg) {
        status.setTextColor(Color.parseColor(ERR));
        status.setText(msg);
        pair.setEnabled(true);
    }

    // ── tailscale detection: a 100.64.0.0/10 address on a tun interface ──
    private boolean tailscaleUp() {
        try {
            for (NetworkInterface ni : Collections.list(NetworkInterface.getNetworkInterfaces())) {
                if (!ni.isUp() || !ni.getName().startsWith("tun")) continue;
                for (InetAddress a : Collections.list(ni.getInetAddresses())) {
                    byte[] b = a.getAddress();
                    if (b.length == 4 && (b[0] & 0xff) == 100 && ((b[1] & 0xff) >> 6) == 1) return true;
                }
            }
        } catch (Exception ignored) { }
        return false;
    }

    /** POST /api/pair with this device's public key → {token, device_id}. */
    private String[] pair(String base, String id, String pubkey) {
        HttpURLConnection c = null;
        try {
            c = (HttpURLConnection) new URL(base + "/api/pair").openConnection();
            c.setRequestMethod("POST");
            c.setRequestProperty("content-type", "application/json");
            c.setConnectTimeout(8000);
            c.setReadTimeout(8000);
            c.setDoOutput(true);
            String body = new JSONObject()
                    .put("pair_id", id)
                    .put("pubkey", pubkey)
                    .toString();
            try (OutputStream os = c.getOutputStream()) { os.write(body.getBytes("UTF-8")); }
            if (c.getResponseCode() != 200) return null;
            JSONObject j = new JSONObject(readAll(c.getInputStream()));
            return new String[]{ j.getString("token"), j.optString("device_id", "") };
        } catch (Exception e) {
            return null;
        } finally {
            if (c != null) c.disconnect();
        }
    }

    static String readAll(InputStream in) throws Exception {
        BufferedReader r = new BufferedReader(new InputStreamReader(in, "UTF-8"));
        StringBuilder sb = new StringBuilder();
        for (String l; (l = r.readLine()) != null; ) sb.append(l);
        return sb.toString();
    }

    // ── views: no xml, one identity ──────────────────────────────────
    /** Scrollable + inset-padded, so the status bar and the nav bar/keyboard
     *  never crop the form. */
    private ScrollView scroll(View content) {
        ScrollView sv = new ScrollView(this);
        sv.setBackgroundColor(Color.parseColor(BG));
        sv.setFillViewport(true);
        sv.addView(content);
        sv.setOnApplyWindowInsetsListener((v, insets) -> {
            android.graphics.Insets bars = insets.getInsets(
                    WindowInsets.Type.systemBars() | WindowInsets.Type.ime());
            v.setPadding(bars.left, bars.top, bars.right, bars.bottom);
            return insets;
        });
        return sv;
    }

    private LinearLayout column() {
        LinearLayout root = new LinearLayout(this);
        root.setOrientation(LinearLayout.VERTICAL);
        root.setGravity(Gravity.CENTER);
        root.setPadding(dp(36), dp(24), dp(36), dp(24));
        root.setBackgroundColor(Color.parseColor(BG));
        root.setLayoutParams(new LinearLayout.LayoutParams(-1, -1));
        return root;
    }

    private Button button(String text) {
        Button b = new Button(this);
        b.setText(text);
        b.setTextColor(Color.BLACK);
        b.setLetterSpacing(0.2f);
        GradientDrawable d = new GradientDrawable();
        d.setColor(Color.parseColor(ACCENT));
        d.setCornerRadius(dp(8));
        b.setBackground(d);
        LinearLayout.LayoutParams bp = new LinearLayout.LayoutParams(-1, dp(52));
        bp.setMargins(0, dp(14), 0, dp(6));
        b.setLayoutParams(bp);
        return b;
    }

    private TextView label(String s, String color, int sp) {
        TextView t = new TextView(this);
        t.setText(s);
        t.setTextColor(Color.parseColor(color));
        t.setTextSize(TypedValue.COMPLEX_UNIT_SP, sp);
        t.setTypeface(android.graphics.Typeface.MONOSPACE);
        t.setGravity(Gravity.CENTER);
        t.setPadding(0, dp(6), 0, dp(6));
        return t;
    }

    private TextView text(String s, String color) { return label(s, color, 12); }

    /** The real Cerveau brain, not a glyph — same mark as the panel. */
    private ImageView brandMark() {
        ImageView v = new ImageView(this);
        v.setImageResource(R.drawable.brand_mark);
        LinearLayout.LayoutParams p = new LinearLayout.LayoutParams(dp(72), dp(72));
        p.gravity = Gravity.CENTER_HORIZONTAL;
        p.setMargins(0, 0, 0, dp(10));
        v.setLayoutParams(p);
        return v;
    }

    private EditText field(String hint, String value) {
        EditText e = new EditText(this);
        e.setHint(hint);
        e.setText(value);
        e.setHintTextColor(Color.parseColor(FAINT));
        e.setTextColor(Color.parseColor(TEXT));
        e.setTextSize(TypedValue.COMPLEX_UNIT_SP, 13);
        e.setTypeface(android.graphics.Typeface.MONOSPACE);
        e.setSingleLine();
        GradientDrawable d = new GradientDrawable();
        d.setColor(Color.parseColor(S2));
        d.setStroke(1, Color.parseColor(LINE));
        d.setCornerRadius(dp(6));
        e.setBackground(d);
        e.setPadding(dp(14), 0, dp(14), 0);
        LinearLayout.LayoutParams p = new LinearLayout.LayoutParams(-1, dp(50));
        p.setMargins(0, dp(8), 0, dp(8));
        e.setLayoutParams(p);
        return e;
    }

    private void hideKeyboard(View v) {
        InputMethodManager im = (InputMethodManager) getSystemService(INPUT_METHOD_SERVICE);
        if (im != null) im.hideSoftInputFromWindow(v.getWindowToken(), 0);
    }

    private int dp(int n) {
        return (int) TypedValue.applyDimension(
                TypedValue.COMPLEX_UNIT_DIP, n, getResources().getDisplayMetrics());
    }
}
