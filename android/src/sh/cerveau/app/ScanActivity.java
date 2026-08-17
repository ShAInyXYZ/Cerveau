package sh.cerveau.app;

import android.Manifest;
import android.app.Activity;
import android.content.Intent;
import android.content.pm.PackageManager;
import android.graphics.Color;
import android.graphics.Insets;
import android.graphics.SurfaceTexture;
import android.hardware.camera2.CameraAccessException;
import android.hardware.camera2.CameraCaptureSession;
import android.hardware.camera2.CameraCharacteristics;
import android.hardware.camera2.CameraDevice;
import android.hardware.camera2.CameraManager;
import android.hardware.camera2.CaptureRequest;
import android.media.Image;
import android.media.ImageReader;
import android.os.Bundle;
import android.os.Handler;
import android.os.HandlerThread;
import android.os.Looper;
import android.util.Size;
import android.view.Gravity;
import android.view.TextureView;
import android.view.View;
import android.view.WindowInsets;
import android.widget.FrameLayout;
import android.widget.TextView;

import com.google.zxing.BinaryBitmap;
import com.google.zxing.PlanarYUVLuminanceSource;
import com.google.zxing.common.HybridBinarizer;
import com.google.zxing.qrcode.QRCodeReader;

import java.nio.ByteBuffer;
import java.util.Arrays;

/**
 * The in-app QR scanner.
 *
 * The pairing QR carries the gate address AND a live code. Handing that to a
 * third-party scanner app would defeat the Keystore identity, the device
 * signature and the "never ship the network" rule in one move — the secret
 * must never leave the app that is allowed to hold it. So the camera frames
 * are read here, decoded here (vendored ZXing, Apache-2.0, decode-only), and
 * the payload goes straight back to the pairing screen. Nothing is written to
 * disk and no other process ever sees a frame.
 */
public class ScanActivity extends Activity {
    private static final int REQ_CAMERA = 5150;

    private TextureView preview;
    private TextView hint;
    private CameraDevice camera;
    private CameraCaptureSession session;
    private ImageReader reader;
    private HandlerThread bgThread;
    private Handler bg;
    private final Handler ui = new Handler(Looper.getMainLooper());
    private final QRCodeReader qr = new QRCodeReader();
    private volatile boolean done = false;

    @Override protected void onCreate(Bundle b) {
        super.onCreate(b);
        getWindow().setStatusBarColor(Color.parseColor(MainActivity.BG));
        getWindow().setNavigationBarColor(Color.parseColor(MainActivity.BG));

        FrameLayout root = new FrameLayout(this);
        root.setBackgroundColor(Color.parseColor(MainActivity.BG));
        preview = new TextureView(this);
        root.addView(preview, new FrameLayout.LayoutParams(-1, -1));

        hint = new TextView(this);
        hint.setText("point at the QR on your machine");
        hint.setTextColor(Color.parseColor(MainActivity.TEXT));
        hint.setTypeface(android.graphics.Typeface.MONOSPACE);
        hint.setGravity(Gravity.CENTER);
        hint.setPadding(0, 0, 0, 64);
        FrameLayout.LayoutParams hp = new FrameLayout.LayoutParams(-1, -2);
        hp.gravity = Gravity.BOTTOM;
        root.addView(hint, hp);

        // respect the system bars like the rest of the app
        root.setOnApplyWindowInsetsListener((v, insets) -> {
            Insets bars = insets.getInsets(WindowInsets.Type.systemBars());
            v.setPadding(bars.left, bars.top, bars.right, bars.bottom);
            return insets;
        });
        setContentView(root);

        if (checkSelfPermission(Manifest.permission.CAMERA) != PackageManager.PERMISSION_GRANTED) {
            requestPermissions(new String[]{ Manifest.permission.CAMERA }, REQ_CAMERA);
        } else {
            start();
        }
    }

    @Override public void onRequestPermissionsResult(int req, String[] perms, int[] granted) {
        super.onRequestPermissionsResult(req, perms, granted);
        if (req == REQ_CAMERA) {
            if (granted.length > 0 && granted[0] == PackageManager.PERMISSION_GRANTED) start();
            else { hint.setText("camera permission is required to scan"); }
        }
    }

    private void start() {
        bgThread = new HandlerThread("scan");
        bgThread.start();
        bg = new Handler(bgThread.getLooper());
        if (preview.isAvailable()) openCamera();
        else preview.setSurfaceTextureListener(new TextureView.SurfaceTextureListener() {
            @Override public void onSurfaceTextureAvailable(SurfaceTexture s, int w, int h) { openCamera(); }
            @Override public void onSurfaceTextureSizeChanged(SurfaceTexture s, int w, int h) { }
            @Override public boolean onSurfaceTextureDestroyed(SurfaceTexture s) { return true; }
            @Override public void onSurfaceTextureUpdated(SurfaceTexture s) { }
        });
    }

    private void openCamera() {
        CameraManager cm = getSystemService(CameraManager.class);
        try {
            String pick = null;
            for (String id : cm.getCameraIdList()) {
                Integer facing = cm.getCameraCharacteristics(id).get(CameraCharacteristics.LENS_FACING);
                if (facing != null && facing == CameraCharacteristics.LENS_FACING_BACK) { pick = id; break; }
            }
            if (pick == null && cm.getCameraIdList().length > 0) pick = cm.getCameraIdList()[0];
            if (pick == null) { hint.setText("no camera available"); return; }

            reader = ImageReader.newInstance(1280, 720, android.graphics.ImageFormat.YUV_420_888, 2);
            reader.setOnImageAvailableListener(this::onFrame, bg);

            cm.openCamera(pick, new CameraDevice.StateCallback() {
                @Override public void onOpened(CameraDevice d) { camera = d; startSession(); }
                @Override public void onDisconnected(CameraDevice d) { d.close(); }
                @Override public void onError(CameraDevice d, int e) { d.close(); }
            }, bg);
        } catch (CameraAccessException | SecurityException e) {
            hint.setText("could not open the camera");
        }
    }

    private void startSession() {
        try {
            SurfaceTexture st = preview.getSurfaceTexture();
            st.setDefaultBufferSize(1280, 720);
            android.view.Surface previewSurface = new android.view.Surface(st);
            CaptureRequest.Builder req = camera.createCaptureRequest(CameraDevice.TEMPLATE_PREVIEW);
            req.addTarget(previewSurface);
            req.addTarget(reader.getSurface());
            req.set(CaptureRequest.CONTROL_AF_MODE, CaptureRequest.CONTROL_AF_MODE_CONTINUOUS_PICTURE);

            camera.createCaptureSession(Arrays.asList(previewSurface, reader.getSurface()),
                    new CameraCaptureSession.StateCallback() {
                        @Override public void onConfigured(CameraCaptureSession s) {
                            session = s;
                            try { s.setRepeatingRequest(req.build(), null, bg); }
                            catch (CameraAccessException ignored) { }
                        }
                        @Override public void onConfigureFailed(CameraCaptureSession s) { }
                    }, bg);
        } catch (CameraAccessException e) {
            hint.setText("could not start the camera");
        }
    }

    /** Decode each frame in-process; the payload never leaves this app. */
    private void onFrame(ImageReader r) {
        Image img = r.acquireLatestImage();
        if (img == null || done) { if (img != null) img.close(); return; }
        try {
            ByteBuffer buf = img.getPlanes()[0].getBuffer();   // Y plane = luminance
            byte[] y = new byte[buf.remaining()];
            buf.get(y);
            int w = img.getWidth(), h = img.getHeight();
            PlanarYUVLuminanceSource src =
                    new PlanarYUVLuminanceSource(y, w, h, 0, 0, w, h, false);
            String text = qr.decode(new BinaryBitmap(new HybridBinarizer(src))).getText();
            if (text != null && !done) {
                done = true;
                ui.post(() -> finishWith(text));
            }
        } catch (Exception ignored) {
            // no code in this frame — normal, just keep looking
        } finally {
            img.close();
            qr.reset();
        }
    }

    private void finishWith(String payload) {
        Intent out = new Intent();
        out.putExtra("payload", payload);
        setResult(RESULT_OK, out);
        finish();
    }

    @Override protected void onDestroy() {
        try { if (session != null) session.close(); } catch (Exception ignored) { }
        try { if (camera != null) camera.close(); } catch (Exception ignored) { }
        try { if (reader != null) reader.close(); } catch (Exception ignored) { }
        if (bgThread != null) bgThread.quitSafely();
        super.onDestroy();
    }
}
