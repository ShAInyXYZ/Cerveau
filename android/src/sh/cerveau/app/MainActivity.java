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
import android.widget.FrameLayout;
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

    /** The gate port. A bare port number identifies nothing on its own —
     *  the HOST is discovered on the tailnet at pair time and is never
     *  compiled into the APK: `strings` on the binary must not reveal the
     *  user's network. Once paired, the resolved gate lives in app storage. */
    static final int GATE_PORT = 7443;

    private static final int MAX_TRIES = 5;
    private static final long LOCKOUT_MS = 15 * 60 * 1000L;
    private static final int REQ_UNLOCK = 4711;
    /** unlock requested specifically to SEAL a freshly paired token */
    private static final int REQ_UNLOCK_STORE = 4713;
    private String pendingToken, pendingDeviceId, pendingGate;

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
    /**
     * The welcome screen for an already-paired device.
     *
     * It used to fire the biometric prompt 200ms after appearing — before the
     * user had touched anything. That saves one tap and costs the user any say
     * in it: the system dialog covers the app, and dismissing it leaves a
     * screen you never chose to be on. Unlocking is now what the Unlock button
     * does, and nothing else does it.
     *
     * Laid out like the disconnected screen: one centred column, mark,
     * headline, one line of cause, the action.
     */
    private void showLock() {
        int dp = (int) getResources().getDisplayMetrics().density;
        boolean guarded = Vault.isProtected(this);

        LinearLayout col = new LinearLayout(this);
        col.setOrientation(LinearLayout.VERTICAL);
        col.setGravity(Gravity.CENTER_HORIZONTAL);

        ImageView mark = new ImageView(this);
        mark.setImageResource(R.drawable.brand_mark);
        LinearLayout.LayoutParams mp = new LinearLayout.LayoutParams(112 * dp, 112 * dp);
        mp.bottomMargin = 30 * dp;
        col.addView(mark, mp);

        TextView head = new TextView(this);
        head.setText("Cerveau");
        head.setTextColor(Color.WHITE);
        head.setTextSize(25);
        head.setGravity(Gravity.CENTER);
        col.addView(head);

        TextView sub = new TextView(this);
        sub.setText(guarded
                ? "This device is paired. Unlock to reach your machine."
                : "This device is paired, but has no screen lock — your token is unguarded.");
        sub.setTextColor(Color.parseColor(guarded ? "#8E8E98" : ERR));
        sub.setTextSize(14);
        sub.setGravity(Gravity.CENTER);
        LinearLayout.LayoutParams sp = new LinearLayout.LayoutParams(-2, -2);
        sp.topMargin = 10 * dp;
        col.addView(sub, sp);

        final TextView status = new TextView(this);
        status.setTextColor(Color.parseColor(MUTED));
        status.setTextSize(12.5f);
        status.setGravity(Gravity.CENTER);

        TextView unlock = new TextView(this);
        unlock.setText(guarded ? "Unlock" : "Open");
        unlock.setTextColor(Color.parseColor("#0B0B0D"));
        unlock.setTextSize(15);
        unlock.setGravity(Gravity.CENTER);
        unlock.setPadding(44 * dp, 13 * dp, 44 * dp, 13 * dp);
        GradientDrawable btn = new GradientDrawable();
        btn.setCornerRadius(10 * dp);
        btn.setColor(Color.parseColor(ACCENT));
        unlock.setBackground(btn);
        unlock.setOnClickListener(v -> openPanel(status));
        LinearLayout.LayoutParams bp = new LinearLayout.LayoutParams(-2, -2);
        bp.topMargin = 32 * dp;
        col.addView(unlock, bp);

        LinearLayout.LayoutParams stp = new LinearLayout.LayoutParams(-2, -2);
        stp.topMargin = 14 * dp;
        col.addView(status, stp);

        TextView unpair = new TextView(this);
        unpair.setText("Unpair this device");
        unpair.setTextColor(Color.parseColor("#55555E"));
        unpair.setTextSize(12.5f);
        unpair.setGravity(Gravity.CENTER);
        unpair.setPadding(16 * dp, 10 * dp, 16 * dp, 10 * dp);
        unpair.setOnClickListener(v -> {
            Vault.clear(prefs);
            DeviceKey.clear();
            prefs.edit().remove("url").remove("device_id").apply();
            showPortal();
        });
        LinearLayout.LayoutParams up = new LinearLayout.LayoutParams(-2, -2);
        up.topMargin = 26 * dp;
        col.addView(unpair, up);

        FrameLayout root = new FrameLayout(this);
        root.setBackgroundColor(Color.parseColor(BG));
        FrameLayout.LayoutParams clp = new FrameLayout.LayoutParams(-2, -2, Gravity.CENTER);
        clp.leftMargin = 32 * dp; clp.rightMargin = 32 * dp;
        root.addView(col, clp);
        root.setOnApplyWindowInsetsListener((v, insets) -> {
            android.graphics.Insets b = insets.getInsets(WindowInsets.Type.systemBars());
            v.setPadding(b.left, b.top, b.right, b.bottom);
            return insets;
        });
        setContentView(root);
        // NO automatic unlock. See the javadoc above.
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
            final String url = prefs.getString("url", "");
            final String devId = prefs.getString("device_id", "");

            // Check reachability BEFORE launching the panel. Opening it first
            // means the user lands inside the app and only then meets a
            // WebView error — the failure looks like the app broke rather than
            // like the machine being unreachable, which is the usual cause
            // (phone off the tailnet).
            status.setTextColor(Color.parseColor(MUTED));
            status.setText("reaching your machine…");
            new Thread(() -> {
                boolean up = Gate.tailnetUp();
                ui.post(() -> {
                    if (!up) { showUnreachable(); return; }
                    Intent i = new Intent(this, PanelActivity.class);
                    i.putExtra("token", token);
                    i.putExtra("url", url);
                    i.putExtra("device_id", devId);
                    startActivity(i);
                });
            }).start();
        } catch (android.security.keystore.UserNotAuthenticatedException e) {
            promptUnlock();
        } catch (Exception e) {
            status.setTextColor(Color.parseColor(ERR));
            status.setText("could not open the vault — unpair and pair again");
        }
    }

    /** Ask for the device lock so the freshly paired token can be sealed. */
    private void promptUnlockForStore() {
        KeyguardManager km = getSystemService(KeyguardManager.class);
        if (km == null || !km.isDeviceSecure()) {
            // No lock configured: the vault cannot be guarded. Say so plainly
            // rather than pretending the token is protected.
            try { Vault.store(this, prefs, pendingToken); showLock(); }
            catch (Exception ignored) { showPortal(); }
            return;
        }
        Intent i = km.createConfirmDeviceCredentialIntent(
                "Cerveau", "confirm to secure this device");
        if (i != null) startActivityForResult(i, REQ_UNLOCK_STORE);
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
        if (req == REQ_UNLOCK && res == RESULT_OK) { openPanel(text("", MUTED)); return; }
        if (req == REQ_UNLOCK_STORE) {
            if (res == RESULT_OK && pendingToken != null) {
                try {
                    Vault.store(this, prefs, pendingToken);
                    pendingToken = null;
                    showLock();
                } catch (Exception e) {
                    showPortal();
                }
            } else {
                showPortal();   // declined: nothing is stored, pairing is not completed
            }
            return;
        }
        if (req == REQ_SCAN && res == RESULT_OK && data != null) {
            String payload = data.getStringExtra("payload");
            if (payload == null) return;
            String g = gateFromPayload(payload), c = codeFromPayload(payload);
            if (c != null && codeFieldRef != null) codeFieldRef.setText(c);
            if (g != null) {
                prefs.edit().putString("scanned_gate", g).apply();
                if (linkFieldRef != null) linkFieldRef.setText(g);
            }
        }
    }

    // ── state 2: not paired → the pairing form ───────────────────────
    /**
     * Shown when the machine cannot be reached, instead of launching the panel
     * into a WebView error.
     *
     * The old path opened the panel first, so the failure read as "the app is
     * broken" rather than "your machine did not answer" — and the message was a
     * raw Chromium constant. Same screen as PanelActivity.offline() so the two
     * entry points look identical.
     */
    private void showUnreachable() {
        setContentView(brokenScreen(this, "Your phone is not on the private network.",
                new String[]{
                    "Turn on your VPN, then try again.",
                    "Check Cerveau is running on your machine."
                }, v -> recreate()));
    }

    /**
     * The disconnected screen, shared by both entry points.
     *
     * One centred column: mark, headline, cause, what to do, the action. The
     * first version stacked everything at the bottom of the screen with the top
     * half empty — a layout that reads as a crash dump rather than a state the
     * app understands.
     */
    static android.view.View brokenScreen(android.app.Activity a, String cause,
                                          String[] steps,
                                          android.view.View.OnClickListener onRetry) {
        int dp = (int) a.getResources().getDisplayMetrics().density;

        LinearLayout col = new LinearLayout(a);
        col.setOrientation(LinearLayout.VERTICAL);
        col.setGravity(Gravity.CENTER_HORIZONTAL);

        ImageView mark = new ImageView(a);
        mark.setImageResource(R.drawable.brain_broken);
        LinearLayout.LayoutParams mp = new LinearLayout.LayoutParams(172 * dp, -2);
        mp.bottomMargin = 34 * dp;
        col.addView(mark, mp);

        TextView head = new TextView(a);
        head.setText("Can't reach Cerveau");
        head.setTextColor(Color.WHITE);
        head.setTextSize(25);
        head.setLetterSpacing(-0.01f);
        head.setGravity(Gravity.CENTER);
        col.addView(head);

        TextView sub = new TextView(a);
        sub.setText(cause);
        sub.setTextColor(Color.parseColor("#8E8E98"));
        sub.setTextSize(15);
        sub.setGravity(Gravity.CENTER);
        LinearLayout.LayoutParams sp = new LinearLayout.LayoutParams(-2, -2);
        sp.topMargin = 10 * dp;
        col.addView(sub, sp);

        // A hairline instead of a heading: the steps are visibly a separate
        // thing from the diagnosis, without a word spent saying so.
        android.view.View rule = new android.view.View(a);
        rule.setBackgroundColor(Color.parseColor("#26262B"));
        LinearLayout.LayoutParams rlp = new LinearLayout.LayoutParams(56 * dp, Math.max(1, dp));
        rlp.topMargin = 26 * dp; rlp.bottomMargin = 24 * dp;
        col.addView(rule, rlp);

        // The steps go in their own left-aligned block. Centring each row
        // independently staggers the numbers, which reads as a mistake rather
        // than a list.
        LinearLayout list = new LinearLayout(a);
        list.setOrientation(LinearLayout.VERTICAL);
        for (int i = 0; i < steps.length; i++) {
            LinearLayout row = new LinearLayout(a);
            row.setOrientation(LinearLayout.HORIZONTAL);
            row.setGravity(Gravity.CENTER_VERTICAL);

            TextView n = new TextView(a);
            n.setText(String.valueOf(i + 1));
            n.setTextColor(Color.parseColor(ACCENT));
            n.setTextSize(12);
            n.setGravity(Gravity.CENTER);
            GradientDrawable ring = new GradientDrawable();
            ring.setShape(GradientDrawable.OVAL);
            ring.setStroke(Math.max(1, dp), Color.parseColor("#4A2430"));
            n.setBackground(ring);
            row.addView(n, new LinearLayout.LayoutParams(24 * dp, 24 * dp));

            TextView t = new TextView(a);
            t.setText(steps[i]);
            t.setTextColor(Color.parseColor("#C4C4CC"));
            t.setTextSize(14.5f);
            LinearLayout.LayoutParams tp = new LinearLayout.LayoutParams(-2, -2);
            tp.leftMargin = 13 * dp;
            row.addView(t, tp);

            LinearLayout.LayoutParams rp2 = new LinearLayout.LayoutParams(-1, -2);
            if (i > 0) rp2.topMargin = 14 * dp;
            list.addView(row, rp2);
        }
        col.addView(list, new LinearLayout.LayoutParams(-2, -2));

        TextView retry = new TextView(a);
        retry.setText("Try again");
        retry.setTextColor(Color.parseColor("#0B0B0D"));
        retry.setTextSize(15);
        retry.setGravity(Gravity.CENTER);
        retry.setPadding(38 * dp, 13 * dp, 38 * dp, 13 * dp);
        GradientDrawable btn = new GradientDrawable();
        btn.setCornerRadius(10 * dp);
        btn.setColor(Color.parseColor(ACCENT));
        retry.setBackground(btn);
        retry.setOnClickListener(onRetry);
        LinearLayout.LayoutParams bp = new LinearLayout.LayoutParams(-2, -2);
        bp.topMargin = 34 * dp;
        col.addView(retry, bp);

        // The column is centred in the window, not pinned to the bottom.
        FrameLayout root = new FrameLayout(a);
        root.setBackgroundColor(Color.parseColor(BG));
        FrameLayout.LayoutParams clp =
                new FrameLayout.LayoutParams(-2, -2, Gravity.CENTER);
        clp.leftMargin = 32 * dp; clp.rightMargin = 32 * dp;
        root.addView(col, clp);
        root.setOnApplyWindowInsetsListener((v, insets) -> {
            android.graphics.Insets b = insets.getInsets(WindowInsets.Type.systemBars());
            v.setPadding(b.left, b.top, b.right, b.bottom);
            return insets;
        });
        return root;
    }

    private void showPortal() {
        LinearLayout root = column();
        root.addView(brandMark());
        root.addView(label("CERVEAU", TEXT, 20));
        root.addView(text("this device is not paired yet", MUTED));

        // "PAIR DEVICE" read as "pair some other device" — this button only
        // ever enrolls the phone it is running on. Say which device it means.
        Button startPair = button("PAIR THIS PHONE");
        TextView status = text("", MUTED);
        root.addView(startPair);
        root.addView(status);
        startPair.setOnClickListener(v -> {
            // Check the network BEFORE revealing anything about it. Off the
            // tailnet, the app says only that — never where the gate lives.
            status.setTextColor(Color.parseColor(MUTED));
            status.setText("checking your network…");
            startPair.setEnabled(false);
            new Thread(() -> {
                if (!Gate.tailnetUp()) {
                    ui.post(() -> {
                        startPair.setEnabled(true);
                        status.setTextColor(Color.parseColor(ERR));
                        status.setText("not on your private network\nconnect tailscale, then pair");
                    });
                    return;
                }
                ui.post(() -> ui.post(() -> { startPair.setEnabled(true); showPairStep(); }));
            }).start();
        });
        setContentView(scroll(root));
    }

    /**
     * Step 2 of pairing: the machine has minted a short-lived code and is
     * showing it on its own screen (over the tailnet). The user reads it there
     * and types it here — the code authorizes THIS device's public key.
     */
    private static final int REQ_SCAN = 4712;
    /** set while the pair step is on screen so the scan result can fill it */
    private EditText linkFieldRef, codeFieldRef;

    private void showPairStep() {
        LinearLayout root = column();
        root.addView(brandMark());
        root.addView(label("PAIR", TEXT, 20));
        root.addView(text("on your machine: open Cerveau → tap the phone icon\nscan the QR, or paste the link it shows", MUTED));

        // Two ways in, both from the same invitation:
        //   • the short link code (EQEF) — resolves the gate for us
        //   • the 6-char code shown under it
        // The phone cannot discover the gate on its own (Tailscale is a
        // userspace VPN on Android — peers are invisible to the app), so the
        // pairing LINK is what carries the address. Paste it or scan it.
        EditText linkField = field("paste the pairing link", "");
        EditText codeField = field("pairing code (6 chars)", "");
        codeField.setAllCaps(true);
        linkFieldRef = linkField; codeFieldRef = codeField;

        Button confirm = button("PAIR");
        TextView status = text("", MUTED);

        // Scanning is delegated to whatever scanner the phone already has —
        // decoding QR by hand in raw Java would be a lot of subtle code for
        // something the OS ecosystem already does well. Typing always works.
        TextView scan = text("or scan the QR instead", ACCENT);
        scan.setPadding(0, dp(14), 0, dp(4));
        scan.setOnClickListener(v -> launchScanner(status));

        confirm.setOnClickListener(v -> {
            hideKeyboard(v);
            final String slug = linkField.getText().toString().trim().toUpperCase();
            final String typed = codeField.getText().toString().trim().toUpperCase();
            if (slug.isEmpty() && typed.isEmpty()) {
                fail(status, confirm, "enter the link code, the pairing code, or scan");
                return;
            }
            status.setTextColor(Color.parseColor(MUTED));
            status.setText("pairing…");
            confirm.setEnabled(false);
            new Thread(() -> {
                if (!Gate.tailnetUp()) {
                    ui.post(() -> fail(status, confirm,
                            "not on your private network\nconnect tailscale, then pair"));
                    return;
                }
                try {
                    String gate = null, code = typed;

                    // A pasted link carries the gate; that is the reliable path.
                    if (!slug.isEmpty()) {
                        String direct = gateFromPayload(linkField.getText().toString().trim());
                        if (direct != null) {
                            gate = direct;
                            String pc = codeFromPayload(linkField.getText().toString().trim());
                            if (pc != null && code.isEmpty()) code = pc;
                            // a /p/SLUG link resolves its own code
                            if (code.isEmpty()) {
                                String[] r = resolveVia(gate, lastPathSegment(
                                        linkField.getText().toString().trim()));
                                if (r != null) code = r[1];
                            }
                        } else {
                            String[] r = resolveSlug(slug);
                            if (r != null) { gate = r[0]; if (code.isEmpty()) code = r[1]; }
                        }
                    }
                    if (gate == null) gate = prefs.getString("scanned_gate", null);
                    if (gate == null) gate = Gate.discover(8000);
                    if (gate == null) {
                        ui.post(() -> fail(status, confirm,
                                "paste the whole link from your machine\n(or scan the QR) — a short code alone\ncannot tell the app where to connect"));
                        return;
                    }
                    if (code.length() != 6) {
                        ui.post(() -> fail(status, confirm, "the pairing code is 6 characters"));
                        return;
                    }
                    DeviceKey.ensure();
                    final String resolvedGate = gate;
                    String[] got = pair(resolvedGate, code, DeviceKey.publicKeyB64());
                    if (got == null) {
                        final String why = lastPairError;
                        // A 401 means the CODE was rejected — that is the only
                        // case worth counting against the retry budget.
                        if (why.startsWith("401")) {
                            int tries = prefs.getInt("tries", 0) + 1;
                            prefs.edit().putInt("tries", tries).apply();
                            if (tries >= MAX_TRIES) {
                                prefs.edit().putLong("lock_until",
                                        System.currentTimeMillis() + LOCKOUT_MS).apply();
                                ui.post(this::showLockedOut);
                                return;
                            }
                            ui.post(() -> fail(status, confirm,
                                    "that code was rejected — get a fresh one on your machine\n"
                                            + (MAX_TRIES - tries) + " tries left"));
                        } else {
                            ui.post(() -> fail(status, confirm,
                                    why.isEmpty() ? "the machine did not answer" : "the machine said: " + why));
                        }
                        return;
                    }
                    final String tok = got[0], devId = got[1];
                    prefs.edit().putString("url", resolvedGate)
                            .putString("device_id", devId)
                            .putInt("tries", 0).apply();
                    try {
                        Vault.store(this, prefs, tok);
                        ui.post(() -> {
                            status.setTextColor(Color.parseColor(OK));
                            status.setText("paired — this device is now known to your machine");
                            ui.postDelayed(this::showLock, 800);
                        });
                    } catch (android.security.keystore.UserNotAuthenticatedException ue) {
                        // The vault key is bound to the device lock, and the
                        // lock has not been satisfied yet in this session.
                        // Ask for it, then seal the token — never store it
                        // unprotected as a workaround.
                        pendingToken = tok; pendingDeviceId = devId; pendingGate = resolvedGate;
                        ui.post(() -> {
                            status.setTextColor(Color.parseColor(MUTED));
                            status.setText("confirm your fingerprint to secure this device");
                            promptUnlockForStore();
                        });
                    }
                } catch (Exception e) {
                    ui.post(() -> fail(status, confirm, "pairing error: " + e.getMessage()));
                }
            }).start();
        });

        root.addView(linkField);
        root.addView(codeField);
        root.addView(confirm);
        root.addView(scan);
        root.addView(status);
        TextView back = text("back", FAINT);
        back.setPadding(0, dp(22), 0, 0);
        back.setOnClickListener(v -> showPortal());
        root.addView(back);
        setContentView(scroll(root));
    }

    /** Open OUR OWN scanner — the pairing payload never leaves this app. */
    private void launchScanner(TextView status) {
        startActivityForResult(new Intent(this, ScanActivity.class), REQ_SCAN);
    }

    static String lastPathSegment(String url) {
        String u = url.replaceAll("/+$", "");
        int i = u.lastIndexOf('/');
        return i < 0 ? u : u.substring(i + 1);
    }

    /** Ask a KNOWN gate to resolve a slug into {gate, code}. */
    private String[] resolveVia(String gate, String slug) {
        HttpURLConnection c = null;
        try {
            c = (HttpURLConnection) new URL(gate + "/p/" + slug).openConnection();
            c.setRequestProperty("Accept", "application/json");
            c.setConnectTimeout(8000);
            c.setReadTimeout(8000);
            if (c.getResponseCode() != 200) return null;
            JSONObject j = new JSONObject(readAll(c.getInputStream()));
            return new String[]{ j.getString("gate"), j.getString("code") };
        } catch (Exception e) {
            return null;
        } finally {
            if (c != null) c.disconnect();
        }
    }

    /** Resolve a 4-char link code into {gate, code} via the invitation. */
    private String[] resolveSlug(String slug) {
        // A pasted full link resolves itself; otherwise ask each reachable
        // gate candidate. NOTE: this must NOT depend on Gate.discover() alone
        // — that was the bug that made every link code fail.
        if (slug.startsWith("HTTP")) return null; // handled by gateFromPayload
        String gate = Gate.discover(8000);
        if (gate == null) gate = prefs.getString("scanned_gate", null);
        if (gate == null) return null;
        HttpURLConnection c = null;
        try {
            c = (HttpURLConnection) new URL(gate + "/p/" + slug).openConnection();
            c.setRequestProperty("Accept", "application/json");
            c.setConnectTimeout(8000);
            c.setReadTimeout(8000);
            if (c.getResponseCode() != 200) return null;
            JSONObject j = new JSONObject(readAll(c.getInputStream()));
            return new String[]{ j.getString("gate"), j.getString("code") };
        } catch (Exception e) {
            return null;
        } finally {
            if (c != null) c.disconnect();
        }
    }

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
            int rc = c.getResponseCode();
            if (rc != 200) {
                // Carry the real reason back: reporting every failure as
                // "wrong or expired code" blamed the user for server-side
                // refusals and burned a retry each time.
                String why = readAllQuiet(c.getErrorStream());
                lastPairError = rc + (why.isEmpty() ? "" : ": " + why.trim());
                return null;
            }
            JSONObject j = new JSONObject(readAll(c.getInputStream()));
            return new String[]{ j.getString("token"), j.optString("device_id", "") };
        } catch (Exception e) {
            return null;
        } finally {
            if (c != null) c.disconnect();
        }
    }

    /** gate origin out of a scanned/pasted invitation, or null. */
    static String gateFromPayload(String s) {
        int i = s.indexOf("gate=");
        if (i >= 0) {
            String rest = s.substring(i + 5);
            int amp = rest.indexOf('&');
            return amp < 0 ? rest : rest.substring(0, amp);
        }
        // https://host:port/p/SLUG → https://host:port
        int p = s.indexOf("/p/");
        if (p > 0 && s.startsWith("http")) return s.substring(0, p);
        return null;
    }

    /** 6-char code out of a scanned/pasted invitation, or null. */
    static String codeFromPayload(String s) {
        int i = s.indexOf("code=");
        if (i < 0) return null;
        String rest = s.substring(i + 5);
        int amp = rest.indexOf('&');
        String c = amp < 0 ? rest : rest.substring(0, amp);
        return c.trim().toUpperCase();
    }

    /** the last /api/pair failure, for an honest message */
    private String lastPairError = "";

    static String readAllQuiet(InputStream in) {
        if (in == null) return "";
        try { return readAll(in); } catch (Exception e) { return ""; }
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
