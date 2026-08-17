package sh.cerveau.app;

import android.annotation.SuppressLint;
import android.app.Activity;
import android.graphics.Color;
import android.graphics.Insets;
import android.os.Bundle;
import android.view.Gravity;
import android.view.View;
import android.view.WindowInsets;
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
                if (req.isForMainFrame()) offline();
            }
        });

        // The window content must respect the system bars — this is the whole
        // fix for the cropping: pad by the insets instead of drawing under them.
        FrameLayout host = new FrameLayout(this);
        host.setBackgroundColor(Color.parseColor(MainActivity.BG));
        host.addView(web, new FrameLayout.LayoutParams(-1, -1));
        host.setOnApplyWindowInsetsListener((v, insets) -> {
            Insets bars = insets.getInsets(
                    WindowInsets.Type.systemBars() | WindowInsets.Type.displayCutout()
                            | WindowInsets.Type.ime());
            v.setPadding(bars.left, bars.top, bars.right, bars.bottom);
            return insets;
        });
        setContentView(host);

        try {
            proxy = new AuthProxy(url, token, deviceId);
            web.loadUrl(proxy.start() + "/");
        } catch (Exception e) {
            offline();
        }
    }

    private void offline() {
        LinearLayout box = new LinearLayout(this);
        box.setOrientation(LinearLayout.VERTICAL);
        box.setGravity(Gravity.CENTER);
        box.setBackgroundColor(Color.parseColor(MainActivity.BG));
        TextView t = new TextView(this);
        t.setText("◈ machine unreachable\nis tailscale up on both ends?");
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
