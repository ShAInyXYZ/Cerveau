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
                    offline("the bridge could not reach your machine\n" + e.getDescription());
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

    private void offline(String detail) {
        LinearLayout box = new LinearLayout(this);
        box.setOrientation(LinearLayout.VERTICAL);
        box.setGravity(Gravity.CENTER);
        box.setBackgroundColor(Color.parseColor(MainActivity.BG));
        TextView t = new TextView(this);
        // Say what actually failed. "is tailscale up?" was a guess that sent
        // the user chasing a healthy network.
        t.setText("◈ " + (detail == null || detail.isEmpty()
                ? "could not load the panel" : detail) + "\n\ntap to retry");
        t.setTextColor(Color.parseColor(MainActivity.ACCENT));
        t.setGravity(Gravity.CENTER);
        t.setPadding(48, 0, 48, 0);
        box.addView(t);
        box.setOnClickListener(v -> recreate());
        box.setOnApplyWindowInsetsListener((v, insets) -> {
            Insets bars = insets.getInsets(WindowInsets.Type.systemBars());
            v.setPadding(bars.left, bars.top, bars.right, bars.bottom);
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
