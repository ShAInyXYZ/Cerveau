package sh.cerveau.app;

import android.app.Activity;
import android.content.Context;
import android.graphics.Color;
import android.graphics.Typeface;
import android.graphics.drawable.GradientDrawable;
import android.graphics.drawable.RippleDrawable;
import android.content.res.ColorStateList;
import android.view.Gravity;
import android.view.View;
import android.view.WindowInsets;
import android.widget.FrameLayout;
import android.widget.ImageView;
import android.widget.LinearLayout;
import android.widget.TextView;

/**
 * The app's design tokens and the handful of components built from them.
 *
 * The Android side has no UI kit — no AndroidX, no Material, no XML layouts,
 * just android.jar — which keeps the build to a few seconds of javac. The cost
 * was that every screen invented its own spacing and colours: five near-
 * duplicate greys (#8E8E98, #C4C4CC, #4A4A52, #2A2A30, #26262B) had appeared
 * alongside a palette that already named those roles, and the disconnected
 * screen and the welcome screen carried duplicate copies of the same button.
 *
 * These values mirror panel/src/tokens.css deliberately. The phone renders the
 * same product as the desktop, so a colour that means "tertiary text" should be
 * the same colour in both, and drift between them is a bug rather than a
 * platform difference.
 */
final class Ui {
    private Ui() {}

    // ── surfaces ────────────────────────────────────────────────────────────
    static final String BG    = "#09090B";  // app base
    static final String S1    = "#0F0F11";  // panel
    static final String S2    = "#1B1B1E";  // raised / input
    static final String S3    = "#222225";  // control / hover

    // ── lines ───────────────────────────────────────────────────────────────
    static final String LINE  = "#262629";  // default divider
    static final String LINE2 = "#2C2C30";  // emphasised edge

    // ── ink ─────────────────────────────────────────────────────────────────
    static final String TEXT  = "#FAFAFA";  // primary
    static final String MUTED = "#A1A1AA";  // secondary — body copy on a screen
    static final String DIM   = "#71717A";  // tertiary — labels
    static final String FAINT = "#52525B";  // rest / disabled

    // ── meaning ─────────────────────────────────────────────────────────────
    static final String ACCENT     = "#E54866";
    static final String ACCENT_INK = "#0B0B0D"; // text ON accent
    static final String OK         = "#4bb894";
    static final String ERR        = "#e6533f";
    static final String WARN       = "#cf9f2e";

    // ── type scale ──────────────────────────────────────────────────────────
    // Four sizes, not "whatever looked right on that screen". Display is for
    // the one thing a screen is about; MICRO is for text the user is not
    // reading unless something went wrong.
    static final float DISPLAY = 23f;
    static final float TITLE   = 17f;
    static final float BODY    = 14.5f;
    static final float SMALL   = 12.5f;
    static final float MICRO   = 11.5f;

    /** Tracking for the wordmark — matches the panel header and the banner. */
    static final float WORDMARK_TRACKING = 0.34f;

    static int dp(Context c, float v) {
        return Math.round(v * c.getResources().getDisplayMetrics().density);
    }

    static int color(String hex) { return Color.parseColor(hex); }

    private static Typeface mono(boolean bold) {
        return Typeface.create("monospace", bold ? Typeface.BOLD : Typeface.NORMAL);
    }

    // ── components ──────────────────────────────────────────────────────────

    /**
     * A full-bleed screen with its content as one centred column.
     *
     * Every screen in this app is this shape, and each one used to rebuild it —
     * which is how one ended up pinned to the bottom of the display while the
     * others were centred.
     */
    static FrameLayout screen(Activity a, LinearLayout content) {
        FrameLayout root = new FrameLayout(a);
        root.setBackgroundColor(color(BG));
        FrameLayout.LayoutParams lp =
                new FrameLayout.LayoutParams(-1, -2, Gravity.CENTER);
        lp.leftMargin = dp(a, 28); lp.rightMargin = dp(a, 28);
        root.addView(content, lp);
        root.setOnApplyWindowInsetsListener((v, insets) -> {
            android.graphics.Insets b = insets.getInsets(WindowInsets.Type.systemBars());
            v.setPadding(b.left, b.top, b.right, b.bottom);
            return insets;
        });
        return root;
    }

    /** The centred column screens put their content in. */
    static LinearLayout column(Context c) {
        LinearLayout col = new LinearLayout(c);
        col.setOrientation(LinearLayout.VERTICAL);
        col.setGravity(Gravity.CENTER_HORIZONTAL);
        return col;
    }

    /** A brand mark, sized to carry the screen it is on. */
    static ImageView mark(Context c, LinearLayout into, int res, float sizeDp, float gapBelowDp) {
        ImageView v = new ImageView(c);
        v.setImageResource(res);
        LinearLayout.LayoutParams lp =
                new LinearLayout.LayoutParams(dp(c, sizeDp), LinearLayout.LayoutParams.WRAP_CONTENT);
        lp.bottomMargin = dp(c, gapBelowDp);
        into.addView(v, lp);
        return v;
    }

    /**
     * CERVEAU, in the tracked uppercase mono the panel and the banner use.
     *
     * Tracking adds a trailing gap after the last glyph, so a centred string
     * sits visually left without the compensating left padding — and that
     * padding pushes the final letter out of a WRAP_CONTENT box, which is how
     * this once rendered as "CERVEA". MATCH_PARENT is not optional here.
     */
    static TextView wordmark(Context c, LinearLayout into, String s) {
        TextView t = new TextView(c);
        t.setText(s);
        t.setTextColor(color(TEXT));
        t.setTextSize(DISPLAY);
        t.setLetterSpacing(WORDMARK_TRACKING);
        t.setTypeface(mono(true));
        t.setGravity(Gravity.CENTER);
        t.setPadding(Math.round(WORDMARK_TRACKING * DISPLAY * c.getResources().getDisplayMetrics().density), 0, 0, 0);
        into.addView(t, new LinearLayout.LayoutParams(-1, -2));
        return t;
    }

    /** Body copy under a headline. Full width so centred lines never clip. */
    static TextView body(Context c, LinearLayout into, String s, String hex, float gapTopDp) {
        TextView t = new TextView(c);
        t.setText(s);
        t.setTextColor(color(hex));
        t.setTextSize(BODY);
        t.setLineSpacing(dp(c, 5), 1f);
        t.setGravity(Gravity.CENTER);
        LinearLayout.LayoutParams lp = new LinearLayout.LayoutParams(-1, -2);
        lp.topMargin = dp(c, gapTopDp);
        into.addView(t, lp);
        return t;
    }

    /** A short hairline: identity above, instruction below. */
    static View hairline(Context c, LinearLayout into, float gapDp) {
        View v = new View(c);
        v.setBackgroundColor(color(LINE2));
        LinearLayout.LayoutParams lp =
                new LinearLayout.LayoutParams(dp(c, 52), Math.max(1, dp(c, 1)));
        lp.topMargin = dp(c, gapDp); lp.bottomMargin = dp(c, gapDp * 0.9f);
        into.addView(v, lp);
        return v;
    }

    /**
     * The primary action. Filled accent, tracked mono label, and a ripple —
     * the one piece of native feedback this app was missing entirely, so every
     * button felt like a picture of a button.
     */
    static TextView primary(Context c, LinearLayout into, String label,
                            View.OnClickListener onClick, float gapTopDp) {
        TextView t = new TextView(c);
        t.setText(label);
        t.setTextColor(color(ACCENT_INK));
        t.setTextSize(BODY);
        t.setLetterSpacing(0.16f);
        t.setTypeface(mono(true));
        t.setGravity(Gravity.CENTER);
        int padX = dp(c, 52), padY = dp(c, 15);
        t.setPadding(padX, padY, padX + Math.round(0.16f * BODY * c.getResources().getDisplayMetrics().density), padY);

        GradientDrawable fill = new GradientDrawable();
        fill.setCornerRadius(dp(c, 11));
        fill.setColor(color(ACCENT));
        t.setBackground(new RippleDrawable(
                ColorStateList.valueOf(Color.parseColor("#33000000")), fill, null));
        t.setClickable(true);
        t.setOnClickListener(onClick);

        LinearLayout.LayoutParams lp = new LinearLayout.LayoutParams(-2, -2);
        lp.topMargin = dp(c, gapTopDp);
        into.addView(t, lp);
        return t;
    }

    /** A quiet, secondary action — present, never competing. */
    static TextView quiet(Context c, LinearLayout into, String label,
                          View.OnClickListener onClick, float gapTopDp) {
        TextView t = new TextView(c);
        t.setText(label);
        t.setTextColor(color(FAINT));
        t.setTextSize(SMALL);
        t.setGravity(Gravity.CENTER);
        t.setPadding(dp(c, 16), dp(c, 12), dp(c, 16), dp(c, 12));
        GradientDrawable none = new GradientDrawable();
        none.setCornerRadius(dp(c, 8));
        none.setColor(Color.TRANSPARENT);
        t.setBackground(new RippleDrawable(
                ColorStateList.valueOf(color(S3)), none, null));
        t.setClickable(true);
        t.setOnClickListener(onClick);
        LinearLayout.LayoutParams lp = new LinearLayout.LayoutParams(-2, -2);
        lp.topMargin = dp(c, gapTopDp);
        into.addView(t, lp);
        return t;
    }

    /** A numbered step in a checklist. */
    static void step(Context c, LinearLayout into, int n, String text, float gapTopDp) {
        LinearLayout row = new LinearLayout(c);
        row.setOrientation(LinearLayout.HORIZONTAL);
        row.setGravity(Gravity.CENTER_VERTICAL);

        TextView num = new TextView(c);
        num.setText(String.valueOf(n));
        num.setTextColor(color(ACCENT));
        num.setTextSize(MICRO);
        num.setGravity(Gravity.CENTER);
        GradientDrawable ring = new GradientDrawable();
        ring.setShape(GradientDrawable.OVAL);
        ring.setStroke(Math.max(1, dp(c, 1)), color("#4A2430"));
        num.setBackground(ring);
        row.addView(num, new LinearLayout.LayoutParams(dp(c, 24), dp(c, 24)));

        TextView t = new TextView(c);
        t.setText(text);
        t.setTextColor(color("#C4C4CC"));
        t.setTextSize(BODY);
        LinearLayout.LayoutParams tp = new LinearLayout.LayoutParams(-2, -2);
        tp.leftMargin = dp(c, 13);
        row.addView(t, tp);

        LinearLayout.LayoutParams lp = new LinearLayout.LayoutParams(-1, -2);
        lp.topMargin = dp(c, gapTopDp);
        into.addView(row, lp);
    }
}
