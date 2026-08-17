package sh.cerveau.app;

import android.app.Activity;
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
import android.view.inputmethod.InputMethodManager;
import android.widget.Button;
import android.widget.EditText;
import android.widget.LinearLayout;
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

/** Cerveau portal — the door into your machine.
 *
 *  Flow: splash → portal (URL + pairing ID, Tailscale verified first) →
 *  pair with the core → PanelActivity. Five wrong IDs locks the app for
 *  15 minutes. Secrets live in app-private storage, never on the wire
 *  outside the tailnet address the user confirmed. */
public class MainActivity extends Activity {
    static final String PREFS = "cerveau";
    static final String BG = "#000000", S1 = "#0F0F11", S2 = "#1B1B1E",
        LINE = "#262629", TEXT = "#FAFAFA", MUTED = "#A1A1AA",
        FAINT = "#52525B", ACCENT = "#E54866", ERR = "#e6533f", OK = "#4bb894";

    private static final int MAX_ATTEMPTS = 5;
    private static final long LOCK_MS = 15 * 60 * 1000;

    private SharedPreferences prefs;
    private Handler ui = new Handler(Looper.getMainLooper());

    @Override protected void onCreate(Bundle b) {
        super.onCreate(b);
        getWindow().setStatusBarColor(Color.BLACK);
        getWindow().setNavigationBarColor(Color.BLACK);
        prefs = getSharedPreferences(PREFS, MODE_PRIVATE);

        if (prefs.getString("token", null) != null && prefs.getString("url", null) != null) {
            startActivity(new Intent(this, PanelActivity.class));
            finish();
            return;
        }
        if (lockRemaining() > 0) { showLocked(); return; }
        showPortal();
    }

    // ── lockout ──────────────────────────────────────────────────────
    private long lockRemaining() {
        long until = prefs.getLong("lockUntil", 0);
        return Math.max(0, until - System.currentTimeMillis());
    }

    private void showLocked() {
        LinearLayout root = column();
        TextView mark = label("◈", ACCENT, 34);
        TextView t = label("LOCKED", ERR, 13);
        t.setLetterSpacing(0.25f);
        TextView sub = text("too many wrong pairing ids\ntry again in " + (lockRemaining() / 60000 + 1) + " min", MUTED);
        sub.setGravity(Gravity.CENTER);
        root.addView(mark); root.addView(t); root.addView(sub);
        setContentView(root);
        ui.postDelayed(this::recheckLock, 10000);
    }

    private void recheckLock() {
        if (lockRemaining() <= 0) showPortal(); else showLocked();
    }

    // ── portal ───────────────────────────────────────────────────────
    private void showPortal() {
        LinearLayout root = column();

        TextView mark = label("◈", ACCENT, 30);
        TextView title = label("CERVEAU", TEXT, 18);
        title.setLetterSpacing(0.35f);
        TextView sub = text("portal — pair this device with your machine", MUTED);
        sub.setGravity(Gravity.CENTER);

        EditText urlField = field("http://100.90.163.54:7700", prefs.getString("url", ""));
        EditText idField = field("pairing id (6 chars, from the core's console)", "");
        idField.setInputType(android.text.InputType.TYPE_CLASS_TEXT
            | android.text.InputType.TYPE_TEXT_FLAG_CAP_CHARACTERS
            | android.text.InputType.TYPE_TEXT_FLAG_NO_SUGGESTIONS);

        TextView status = text("", FAINT);
        status.setGravity(Gravity.CENTER);

        Button pair = new Button(this);
        pair.setText("PAIR");
        pair.setTextColor(Color.BLACK);
        pair.setLetterSpacing(0.2f);
        GradientDrawable d = new GradientDrawable();
        d.setColor(Color.parseColor(ACCENT));
        d.setCornerRadius(6);
        pair.setBackground(d);
        LinearLayout.LayoutParams bp = new LinearLayout.LayoutParams(-1, dp(48));
        bp.setMargins(0, dp(18), 0, 0);
        pair.setLayoutParams(bp);

        pair.setOnClickListener(v -> {
            hideKeyboard(urlField);
            String url = urlField.getText().toString().trim();
            String id = idField.getText().toString().trim().toLowerCase();
            if (url.isEmpty() || id.length() != 6) {
                status.setTextColor(Color.parseColor(ERR));
                status.setText("need a url and a 6-char pairing id");
                return;
            }
            if (!url.startsWith("http")) url = "http://" + url;
            final String furl = url;
            pair.setEnabled(false);
            status.setTextColor(Color.parseColor(FAINT));
            status.setText("checking tailscale…");
            new Thread(() -> {
                if (!tailscaleUp()) {
                    ui.post(() -> fail(status, pair, "tailscale is not up on this phone\nstart it, then retry"));
                    return;
                }
                ui.post(() -> status.setText("reaching " + furl + " …"));
                String token = pair(furl, id);
                if (token == null) {
                    int fails = prefs.getInt("fails", 0) + 1;
                    prefs.edit().putInt("fails", fails).apply();
                    if (fails >= MAX_ATTEMPTS) {
                        prefs.edit().putLong("lockUntil", System.currentTimeMillis() + LOCK_MS)
                            .putInt("fails", 0).apply();
                        ui.post(this::showLocked);
                        return;
                    }
                    ui.post(() -> fail(status, pair,
                        "pairing refused (" + fails + "/" + MAX_ATTEMPTS + ")\nwrong id, unreachable, or already paired"));
                    return;
                }
                prefs.edit().putString("url", furl).putString("token", token)
                    .putInt("fails", 0).apply();
                ui.post(() -> {
                    status.setTextColor(Color.parseColor(OK));
                    status.setText("paired — entering");
                    startActivity(new Intent(this, PanelActivity.class));
                    finish();
                });
            }).start();
        });

        root.addView(mark); root.addView(title); root.addView(sub);
        root.addView(urlField); root.addView(idField); root.addView(pair); root.addView(status);
        setContentView(root);
    }

    private void fail(TextView status, Button pair, String msg) {
        status.setTextColor(Color.parseColor(ERR));
        status.setText(msg);
        pair.setEnabled(true);
    }

    // ── tailscale detection: a 100.64.0.0/10 address on the interface ──
    private boolean tailscaleUp() {
        try {
            for (NetworkInterface ni : Collections.list(NetworkInterface.getNetworkInterfaces())) {
                if (!ni.isUp() || !ni.getName().startsWith("tun")) continue;
                for (InetAddress a : Collections.list(ni.getInetAddresses())) {
                    byte[] b = a.getAddress();
                    if (b.length == 4 && (b[0] & 0xff) == 100 && ((b[1] & 0xff) >> 6) == 1) return true;
                }
            }
        } catch (Exception ignored) {}
        return false;
    }

    private String pair(String base, String id) {
        HttpURLConnection c = null;
        try {
            URL u = new URL(base + "/api/pair");
            c = (HttpURLConnection) u.openConnection();
            c.setRequestMethod("POST");
            c.setRequestProperty("content-type", "application/json");
            c.setConnectTimeout(5000);
            c.setReadTimeout(5000);
            c.setDoOutput(true);
            byte[] body = ("{\"pair_id\":\"" + id + "\"}").getBytes("UTF-8");
            OutputStream os = c.getOutputStream();
            os.write(body); os.close();
            if (c.getResponseCode() != 200) return null;
            String s = readAll(c.getInputStream());
            return new JSONObject(s).getString("token");
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

    // ── tiny view helpers (no xml, one identity) ─────────────────────
    private LinearLayout column() {
        LinearLayout root = new LinearLayout(this);
        root.setOrientation(LinearLayout.VERTICAL);
        root.setGravity(Gravity.CENTER);
        root.setPadding(dp(36), 0, dp(36), 0);
        root.setBackgroundColor(Color.parseColor(BG));
        return root;
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

    private TextView text(String s, String color) {
        return label(s, color, 12);
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
        d.setCornerRadius(6);
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
        return (int) TypedValue.applyDimension(TypedValue.COMPLEX_UNIT_DIP, n, getResources().getDisplayMetrics());
    }
}
