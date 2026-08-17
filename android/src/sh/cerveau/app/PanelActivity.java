package sh.cerveau.app;

import android.annotation.SuppressLint;
import android.app.Activity;
import android.content.SharedPreferences;
import android.graphics.Color;
import android.os.Bundle;
import android.view.View;
import android.webkit.WebResourceRequest;
import android.webkit.WebSettings;
import android.webkit.WebView;
import android.webkit.WebViewClient;
import android.widget.LinearLayout;
import android.widget.TextView;

/** The panel — a fullscreen WebView onto the paired instance. The token
 *  is injected before any app code runs, so the panel's fetch shim picks
 *  it up on its very first request. Offline = one line, black screen. */
public class PanelActivity extends Activity {
    private WebView web;

    @SuppressLint("SetJavaScriptEnabled")
    @Override protected void onCreate(Bundle b) {
        super.onCreate(b);
        getWindow().setStatusBarColor(Color.BLACK);
        getWindow().setNavigationBarColor(Color.BLACK);

        SharedPreferences prefs = getSharedPreferences(MainActivity.PREFS, MODE_PRIVATE);
        String url = prefs.getString("url", null);
        String token = prefs.getString("token", null);
        if (url == null || token == null) { finish(); return; }

        web = new WebView(this);
        WebSettings s = web.getSettings();
        s.setJavaScriptEnabled(true);
        s.setDomStorageEnabled(true);
        s.setMediaPlaybackRequiresUserGesture(false);
        web.setBackgroundColor(Color.BLACK);
        web.setWebViewClient(new WebViewClient() {
            @Override public void onPageStarted(WebView v, String u, android.graphics.Bitmap fav) {
                // token must exist before the panel's first fetch
                v.evaluateJavascript(
                    "try{localStorage.setItem('crv.auth','" + token + "')}catch(e){}", null);
            }
            @Override public boolean shouldOverrideUrlLoading(WebView v, WebResourceRequest r) {
                String u = r.getUrl().toString();
                if (u.startsWith(url)) return false; // in-app
                return true; // never let anything navigate the shell away
            }
            @Override public void onReceivedError(WebView v, WebResourceRequest req, android.webkit.WebResourceError e) {
                if (req.isForMainFrame()) offline();
            }
        });
        setContentView(web);
        web.loadUrl(url + "/");
    }

    private void offline() {
        LinearLayout box = new LinearLayout(this);
        box.setOrientation(LinearLayout.VERTICAL);
        box.setGravity(android.view.Gravity.CENTER);
        box.setBackgroundColor(Color.BLACK);
        TextView t = new TextView(this);
        t.setText("◈ machine unreachable\nis tailscale up on both ends?");
        t.setTextColor(Color.parseColor(MainActivity.ACCENT));
        t.setGravity(android.view.Gravity.CENTER);
        t.setPadding(48, 0, 48, 0);
        box.addView(t);
        box.setOnClickListener(v -> recreate());
        setContentView(box);
        if (web != null) web.destroy();
    }

    @Override protected void onDestroy() {
        if (web != null) web.destroy();
        super.onDestroy();
    }

    @Override public void onBackPressed() {
        if (web != null && web.canGoBack()) web.goBack(); else super.onBackPressed();
    }
}
