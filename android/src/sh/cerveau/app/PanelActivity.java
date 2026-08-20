package sh.cerveau.app;

import android.annotation.SuppressLint;
import android.app.Activity;
import android.graphics.Color;
import android.graphics.Insets;
import android.os.Build;
import android.os.Bundle;
import android.view.Gravity;
import android.view.View;
import android.view.WindowInsets;
import android.view.Window;
import android.view.WindowInsetsController;
import android.view.WindowManager;
import android.webkit.WebResourceRequest;
import android.webkit.WebSettings;
import android.webkit.WebView;
import android.webkit.WebViewClient;
import android.widget.FrameLayout;
import android.widget.LinearLayout;
import android.widget.TextView;

/**
 *  The panel — a WebView onto the paired machine, served through the app's
 *  own loopback proxy so the page never holds a credential.
 *
 *  NOT fullscreen. The window sits inside the system bars: the status bar
 *  stays visible at the top, the navigation bar / gesture pill at the bottom,
 *  and the keyboard pushes content instead of covering it. The old build
 *  forced Theme.…Fullscreen, so pulling the status bar down or showing the
 *  nav buttons cropped the UI.
 */
public class PanelActivity extends Activity {
    private WebView web;
    private AuthProxy proxy;

    @SuppressLint("SetJavaScriptEnabled")
    @Override protected void onCreate(Bundle b) {
        super.onCreate(b);
        getWindow().setStatusBarColor(Color.parseColor(MainActivity.BG));
        getWindow().setNavigationBarColor(Color.parseColor(MainActivity.BG));

        String url = getIntent().getStringExtra("url");
        String token = getIntent().getStringExtra("token");
        String deviceId = getIntent().getStringExtra("device_id");
        if (url == null || token == null) { finish(); return; }

        web = new WebView(this);
        WebSettings s = web.getSettings();
        s.setJavaScriptEnabled(true);
        s.setDomStorageEnabled(true);
        s.setMediaPlaybackRequiresUserGesture(false);
        web.setBackgroundColor(Color.parseColor(MainActivity.BG));
        web.setWebViewClient(new WebViewClient() {
            @Override public boolean shouldOverrideUrlLoading(WebView v, WebResourceRequest r) {
                // navigation is jailed to our own loopback origin
                return !r.getUrl().toString().startsWith("http://127.0.0.1");
            }
            @Override public void onReceivedError(WebView v, WebResourceRequest req,
                                                  android.webkit.WebResourceError e) {
                android.util.Log.e("cerveau", "webview error " + e.getErrorCode()
                        + " " + e.getDescription() + " for " + req.getUrl());
                if (req.isForMainFrame()) {
                    offline(String.valueOf(e.getDescription()));
                }
            }
        });

        // Immersive: the panel gets the whole screen, and the status/nav bars
        // slide away. A swipe from either edge brings them back temporarily,
        // then they hide again on their own (BEHAVIOR_SHOW_TRANSIENT_BARS).
        //
        // Immersive is NOT the same as ignoring insets — that was the original
        // cropping bug. The bars are hidden but their insets still arrive when
        // they are transiently shown, and the keyboard's inset always does, so
        // content is padded by whatever is ACTUALLY on screen at the time.
        FrameLayout host = new FrameLayout(this);
        host.setBackgroundColor(Color.parseColor(MainActivity.BG));
        host.addView(web, new FrameLayout.LayoutParams(-1, -1));
        host.setOnApplyWindowInsetsListener((v, insets) -> {
            // Pad ONLY for what is really occupying the screen.
            //
            // systemBars is 0 while immersive (and non-zero only while a bar
            // is transiently swiped in), and the IME must never be drawn under
            // or the composer would sit behind the keyboard.
            //
            // displayCutout is deliberately EXCLUDED here: it reports the
            // notch region every time, hidden bars or not. Including it left a
            // permanent 111px black band at the top of a phone whose cutout is
            // already covered by the (now hidden) status bar. The cutout is
            // handled by LAYOUT_IN_DISPLAY_CUTOUT_MODE instead, so content
            // flows into it and only real obstructions cost space.
            Insets bars = insets.getInsets(
                    WindowInsets.Type.systemBars() | WindowInsets.Type.ime());
            v.setPadding(bars.left, bars.top, bars.right, bars.bottom);
            return insets;
        });
        setContentView(host);
        goImmersive();

        try {
            proxy = new AuthProxy(url, token, deviceId);
            String local = proxy.start();
            android.util.Log.i("cerveau", "proxy on " + local + " → gate " + url);
            web.loadUrl(local + "/");
        } catch (Exception e) {
            android.util.Log.e("cerveau", "proxy failed to start", e);
            offline("could not start the local bridge\n" + e.getMessage());
        }
    }

    /**
     * Hide the system bars, letting a swipe reveal them transiently.
     *
     * setDecorFitsSystemWindows(false) is what lets the app own the full
     * window; without it the bars leave a permanent gap even when hidden.
     */
    @SuppressWarnings("deprecation")
    private void goImmersive() {
        Window w = getWindow();
        // WindowInsetsController is API 30. minSdk is 29, and android.jar is a
        // COMPILE-TIME stub — calling it unguarded compiles cleanly and then
        // throws NoSuchMethodError on a real API-29 phone. Same trap as the
        // JDK-only httpserver import. Guard, with the legacy flags as fallback.
        if (Build.VERSION.SDK_INT >= 30) {
            w.setDecorFitsSystemWindows(false);
            // Draw into the notch region too; without this the window is laid
            // out below the cutout and the immersive gain is given straight
            // back as a black bar.
            w.getAttributes().layoutInDisplayCutoutMode =
                    WindowManager.LayoutParams.LAYOUT_IN_DISPLAY_CUTOUT_MODE_SHORT_EDGES;
            WindowInsetsController c = w.getInsetsController();
            if (c != null) {
                c.hide(WindowInsets.Type.systemBars());
                c.setSystemBarsBehavior(
                        WindowInsetsController.BEHAVIOR_SHOW_TRANSIENT_BARS_BY_SWIPE);
            }
        } else {
            w.getDecorView().setSystemUiVisibility(
                    View.SYSTEM_UI_FLAG_LAYOUT_STABLE
                            | View.SYSTEM_UI_FLAG_LAYOUT_HIDE_NAVIGATION
                            | View.SYSTEM_UI_FLAG_LAYOUT_FULLSCREEN
                            | View.SYSTEM_UI_FLAG_HIDE_NAVIGATION
                            | View.SYSTEM_UI_FLAG_FULLSCREEN
                            | View.SYSTEM_UI_FLAG_IMMERSIVE_STICKY);
        }
    }

    /**
     * Android restores the bars whenever the window loses and regains focus
     * (app switcher, notification shade, permission dialog, unlocking). Without
     * re-asserting here they would come back permanently after the first
     * interruption — which is exactly how they end up "always showing".
     */
    @Override public void onWindowFocusChanged(boolean hasFocus) {
        super.onWindowFocusChanged(hasFocus);
        if (hasFocus) goImmersive();
    }

    /**
     * The disconnected screen.
     *
     * It used to print the raw WebView failure — "net::ERR_EMPTY_RESPONSE" —
     * which names a Chromium constant, not anything a person can act on. The
     * common cause by far is that the phone is off the tailnet, and the screen
     * never said so.
     *
     * What it shows now: the cracked mark, one plain sentence, and the two or
     * three things actually worth checking, in the order they fail. The
     * technical detail stays, small and last, for when it matters.
     */
    private void offline(String detail) {
        int dp = (int) getResources().getDisplayMetrics().density;

        LinearLayout box = new LinearLayout(this);
        box.setOrientation(LinearLayout.VERTICAL);
        box.setGravity(Gravity.CENTER);
        box.setBackgroundColor(Color.parseColor(MainActivity.BG));
        box.setPadding(32 * dp, 0, 32 * dp, 0);

        android.widget.ImageView mark = new android.widget.ImageView(this);
        mark.setImageResource(R.drawable.brain_broken);
        mark.setAlpha(0.9f);
        LinearLayout.LayoutParams mp =
                new LinearLayout.LayoutParams(148 * dp, LinearLayout.LayoutParams.WRAP_CONTENT);
        mp.bottomMargin = 26 * dp;
        box.addView(mark, mp);

        TextView head = new TextView(this);
        head.setText("Can't reach Cerveau");
        head.setTextColor(Color.WHITE);
        head.setTextSize(21);
        head.setGravity(Gravity.CENTER);
        box.addView(head);

        TextView sub = new TextView(this);
        sub.setText("The app is running, but your machine did not answer.");
        sub.setTextColor(Color.parseColor("#9A9AA2"));
        sub.setTextSize(14);
        sub.setGravity(Gravity.CENTER);
        LinearLayout.LayoutParams sp =
                new LinearLayout.LayoutParams(-2, LinearLayout.LayoutParams.WRAP_CONTENT);
        sp.topMargin = 8 * dp;
        sp.bottomMargin = 22 * dp;
        box.addView(sub, sp);

        // Ordered by how often each one is the actual cause.
        TextView checks = new TextView(this);
        checks.setText("1.  Is this phone connected to your VPN?\n"
                     + "2.  Is Cerveau running on your machine?\n"
                     + "3.  Is the address in Settings still correct?");
        checks.setTextColor(Color.parseColor("#C9C9D1"));
        checks.setTextSize(14);
        checks.setLineSpacing(7 * dp, 1f);
        box.addView(checks);

        TextView retry = new TextView(this);
        retry.setText("Try again");
        retry.setTextColor(Color.parseColor(MainActivity.ACCENT));
        retry.setTextSize(15);
        retry.setGravity(Gravity.CENTER);
        retry.setPadding(30 * dp, 12 * dp, 30 * dp, 12 * dp);
        android.graphics.drawable.GradientDrawable btn = new android.graphics.drawable.GradientDrawable();
        btn.setCornerRadius(9 * dp);
        btn.setStroke(1 * dp, Color.parseColor(MainActivity.ACCENT));
        retry.setBackground(btn);
        retry.setOnClickListener(v -> recreate());
        LinearLayout.LayoutParams rp =
                new LinearLayout.LayoutParams(-2, LinearLayout.LayoutParams.WRAP_CONTENT);
        rp.topMargin = 28 * dp;
        box.addView(retry, rp);

        // Kept, but demoted: useful when the cause is NOT one of the three
        // above, and noise every other time.
        if (detail != null && !detail.isEmpty()) {
            TextView tech = new TextView(this);
            tech.setText(detail);
            tech.setTextColor(Color.parseColor("#55555E"));
            tech.setTextSize(11);
            tech.setGravity(Gravity.CENTER);
            LinearLayout.LayoutParams tp =
                    new LinearLayout.LayoutParams(-2, LinearLayout.LayoutParams.WRAP_CONTENT);
            tp.topMargin = 22 * dp;
            box.addView(tech, tp);
        }

        box.setOnApplyWindowInsetsListener((v, insets) -> {
            Insets bars = insets.getInsets(WindowInsets.Type.systemBars());
            v.setPadding(32 * dp + bars.left, bars.top, 32 * dp + bars.right, bars.bottom);
            return insets;
        });
        setContentView(box);
        if (web != null) { web.destroy(); web = null; }
    }

    @Override protected void onDestroy() {
        if (web != null) web.destroy();
        if (proxy != null) proxy.stop();
        super.onDestroy();
    }

    @Override public void onBackPressed() {
        if (web != null && web.canGoBack()) web.goBack(); else super.onBackPressed();
    }
}
