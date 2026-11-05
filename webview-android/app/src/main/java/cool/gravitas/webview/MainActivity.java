package cool.gravitas.webview;

import android.app.Activity;
import android.content.ActivityNotFoundException;
import android.content.Intent;
import android.graphics.Bitmap;
import android.net.Uri;
import android.net.http.SslError;
import android.os.Build;
import android.os.Bundle;
import android.os.Message;
import android.view.View;
import android.view.WindowInsets;
import android.webkit.CookieManager;
import android.webkit.SslErrorHandler;
import android.webkit.ValueCallback;
import android.webkit.WebChromeClient;
import android.webkit.WebResourceError;
import android.webkit.WebResourceRequest;
import android.webkit.WebResourceResponse;
import android.webkit.WebView;
import android.webkit.WebViewClient;
import android.widget.Button;
import android.widget.LinearLayout;
import android.widget.ProgressBar;
import android.widget.TextView;
import android.widget.Toast;
import android.window.OnBackInvokedCallback;
import android.window.OnBackInvokedDispatcher;

import cool.gravitas.navigation.GravitasEdgeNavigation;

public final class MainActivity extends Activity {
    static final int FILE_PICKER = 1;
    WebView web;
    Button back, forward;
    private ProgressBar progress;
    private LinearLayout failure;
    private ValueCallback<Uri[]> fileResult;
    private boolean authenticating;
    private OnBackInvokedCallback backCallback;

    @Override public void onCreate(Bundle state) {
        super.onCreate(state);
        LinearLayout root = new LinearLayout(this);
        root.setOrientation(LinearLayout.VERTICAL);
        root.setBackgroundColor(0xfff4f1e9);
        root.setOnApplyWindowInsetsListener((view, insets) -> {
            if (Build.VERSION.SDK_INT >= 30) {
                android.graphics.Insets bars = insets.getInsets(WindowInsets.Type.systemBars() | WindowInsets.Type.displayCutout());
                view.setPadding(bars.left, bars.top, bars.right,
                    Math.max(bars.bottom, insets.getInsets(WindowInsets.Type.ime()).bottom));
            } else {
                view.setPadding(insets.getSystemWindowInsetLeft(), insets.getSystemWindowInsetTop(),
                    insets.getSystemWindowInsetRight(), insets.getSystemWindowInsetBottom());
            }
            return insets;
        });
        setContentView(root);
        progress = new ProgressBar(this, null, android.R.attr.progressBarStyleHorizontal);
        progress.setMax(100);
        root.addView(progress, new LinearLayout.LayoutParams(-1, dp(3)));
        failure = new LinearLayout(this);
        failure.setOrientation(LinearLayout.VERTICAL);
        failure.setPadding(dp(20), dp(12), dp(20), dp(12));
        TextView error = new TextView(this);
        error.setText("Unable to load " + getString(R.string.app_name) + ". Check your connection and try again.");
        failure.addView(error);
        Button retry = new Button(this);
        retry.setText("Retry");
        retry.setOnClickListener(view -> web.reload());
        failure.addView(retry);
        failure.setVisibility(View.GONE);
        root.addView(failure);
        web = new WebView(this);
        web.setId(View.generateViewId());
        root.addView(web, new LinearLayout.LayoutParams(-1, 0, 1));
        LinearLayout toolbar = new LinearLayout(this);
        back = button(toolbar, "Back", () -> navigate(false));
        forward = button(toolbar, "Forward", () -> navigate(true));
        button(toolbar, "Reload", () -> web.reload());
        root.addView(toolbar);
        configureWebView();
        if (BuildConfig.DEBUG && getIntent().getBooleanExtra("skipInitialLoad", false)) {
            if (state != null) web.restoreState(state);
        } else if (state != null && state.getBoolean("authenticating")) {
            web.loadUrl(BuildConfig.APP_ORIGIN + "/login?auto=off");
        } else if (state == null || web.restoreState(state) == null) web.loadUrl(BuildConfig.APP_ORIGIN + "/");
        updateNavigation();
        installBackHandler();
    }

    private int dp(int value) { return Math.round(value * getResources().getDisplayMetrics().density); }

    private Button button(LinearLayout row, String label, Runnable action) {
        Button button = new Button(this);
        button.setText(label);
        button.setAllCaps(false);
        button.setContentDescription(label);
        button.setMinHeight(dp(48));
        button.setOnClickListener(view -> action.run());
        row.addView(button, new LinearLayout.LayoutParams(0, -2, 1));
        return button;
    }

    @android.annotation.SuppressLint("SetJavaScriptEnabled")
    private void configureWebView() {
        WebView.setWebContentsDebuggingEnabled(BuildConfig.DEBUG);
        web.getSettings().setJavaScriptEnabled(true);
        web.getSettings().setDomStorageEnabled(true);
        web.getSettings().setAllowFileAccess(false);
        web.getSettings().setAllowContentAccess(true);
        web.getSettings().setMixedContentMode(android.webkit.WebSettings.MIXED_CONTENT_NEVER_ALLOW);
        web.getSettings().setSupportMultipleWindows(true);
        CookieManager.getInstance().setAcceptCookie(true);
        CookieManager.getInstance().setAcceptThirdPartyCookies(web, false);
        web.setWebViewClient(new WebViewClient() {
            @Override public boolean shouldOverrideUrlLoading(WebView view, WebResourceRequest request) {
                if (!request.isForMainFrame()) return false;
                String url = request.getUrl().toString();
                if (NavigationPolicy.sameOrigin(url, BuildConfig.APP_ORIGIN)) {
                    if (NavigationPolicy.authentication(url, BuildConfig.APP_ORIGIN)) authenticating = true;
                    return false;
                }
                // Federated sign-in follows the website's HTTPS redirects in this app's cookie jar.
                if (authenticating && NavigationPolicy.https(url)) return false;
                openExternal(url);
                return true;
            }
            @Override public void onPageStarted(WebView view, String url, Bitmap icon) {
                failure.setVisibility(View.GONE);
                if (NavigationPolicy.authentication(url, BuildConfig.APP_ORIGIN)) authenticating = true;
                updateNavigation();
            }
            @Override public void onPageFinished(WebView view, String url) {
                if (authenticating && NavigationPolicy.sameOrigin(url, BuildConfig.APP_ORIGIN)
                    && !NavigationPolicy.authentication(url, BuildConfig.APP_ORIGIN)) {
                    // Account transitions must not leave private screens or login forms in forward history.
                    web.clearHistory();
                    authenticating = false;
                }
                CookieManager.getInstance().flush();
                updateNavigation();
            }
            @Override public void doUpdateVisitedHistory(WebView view, String url, boolean reload) {
                if (NavigationPolicy.privateBoundary(url, BuildConfig.APP_ORIGIN)
                    || web.copyBackForwardList().getSize() > 64) web.clearHistory();
                updateNavigation();
            }
            @Override public void onFormResubmission(WebView view, Message dontResend, Message resend) {
                dontResend.sendToTarget();
            }
            @Override public void onReceivedError(WebView view, WebResourceRequest request, WebResourceError error) {
                if (request.isForMainFrame()) failure.setVisibility(View.VISIBLE);
            }
            @Override public void onReceivedHttpError(WebView view, WebResourceRequest request, WebResourceResponse response) {
                if (NavigationPolicy.sameOrigin(request.getUrl().toString(), BuildConfig.APP_ORIGIN)
                    && (response.getStatusCode() == 401 || response.getStatusCode() == 403)) web.clearHistory();
                if (request.isForMainFrame() && response.getStatusCode() >= 500) failure.setVisibility(View.VISIBLE);
            }
            @Override public void onReceivedSslError(WebView view, SslErrorHandler handler, SslError error) {
                handler.cancel();
                failure.setVisibility(View.VISIBLE);
            }
        });
        web.setWebChromeClient(new WebChromeClient() {
            @Override public void onProgressChanged(WebView view, int value) {
                progress.setProgress(value);
                progress.setVisibility(value < 100 ? View.VISIBLE : View.GONE);
            }
            @Override public boolean onShowFileChooser(WebView view, ValueCallback<Uri[]> result, FileChooserParams params) {
                if (fileResult != null) fileResult.onReceiveValue(null);
                fileResult = result;
                try { startActivityForResult(params.createIntent(), FILE_PICKER); }
                catch (ActivityNotFoundException error) {
                    fileResult.onReceiveValue(null);
                    fileResult = null;
                    Toast.makeText(MainActivity.this, "No file picker is available", Toast.LENGTH_SHORT).show();
                }
                return true;
            }
            @Override public boolean onCreateWindow(WebView view, boolean dialog, boolean gesture, Message message) {
                if (!gesture) return false;
                WebView popup = new WebView(MainActivity.this);
                popup.setWebViewClient(new WebViewClient() {
                    @Override public boolean shouldOverrideUrlLoading(WebView child, WebResourceRequest request) {
                        openExternal(request.getUrl().toString());
                        child.destroy();
                        return true;
                    }
                });
                ((WebView.WebViewTransport) message.obj).setWebView(popup);
                message.sendToTarget();
                return true;
            }
        });
        web.setDownloadListener((url, agent, disposition, mime, length) -> openExternal(url));
    }

    private void openExternal(String raw) {
        if (!NavigationPolicy.external(raw)) {
            Toast.makeText(this, "This link cannot be opened", Toast.LENGTH_SHORT).show();
            return;
        }
        try { startActivity(new Intent(Intent.ACTION_VIEW, Uri.parse(raw)).addCategory(Intent.CATEGORY_BROWSABLE)); }
        catch (ActivityNotFoundException error) { Toast.makeText(this, "No app can open this link", Toast.LENGTH_SHORT).show(); }
    }

    void navigate(boolean goForward) {
        if (authenticating) {
            if (!goForward) {
                authenticating = false;
                web.clearHistory();
                web.loadUrl(BuildConfig.APP_ORIGIN + "/login?auto=off");
            }
            return;
        }
        if (NavigationPolicy.sameOrigin(web.getUrl(), BuildConfig.APP_ORIGIN)) {
            // Give hosted screens a semantic navigation hook; modal cancellation
            // must finish before the destination beneath it can change.
            String direction = goForward ? "forward" : "back";
            web.evaluateJavascript("(() => { const forward = " + goForward + ";"
                + "const event = new CustomEvent('gravitas:navigate', {cancelable:true, detail:{direction:'" + direction + "'}});"
                + "if (!window.dispatchEvent(event)) return true;"
                + "const dialog = document.querySelector('dialog[open]');"
                + "if (dialog) { if (!forward) { const cancel = new Event('cancel', {cancelable:true,bubbles:true});"
                + "if (dialog.dispatchEvent(cancel)) dialog.close(); } return true; }"
                + "const overlay = document.querySelector('.modal-backdrop');"
                + "if (overlay) { if (!forward) overlay.click(); return true; } return false; })()",
                handled -> { if (!"true".equals(handled)) navigateHistory(goForward); });
        } else navigateHistory(goForward);
    }

    private void navigateHistory(boolean goForward) {
        if (goForward) {
            if (web.canGoForward()) web.goForward();
        } else if (web.canGoBack()) web.goBack();
        else finish();
    }

    private void updateNavigation() {
        back.setEnabled(web.canGoBack());
        forward.setEnabled(web.canGoForward());
    }

    private void installBackHandler() {
        if (Build.VERSION.SDK_INT >= 34) {
            GravitasEdgeNavigation.install(this, () -> navigate(false), () -> navigate(true));
            return;
        }
        if (Build.VERSION.SDK_INT >= 33) backCallback = () -> navigate(false);
        if (Build.VERSION.SDK_INT >= 33) getOnBackInvokedDispatcher().registerOnBackInvokedCallback(OnBackInvokedDispatcher.PRIORITY_DEFAULT, backCallback);
    }

    @Override public void onBackPressed() {
        if (Build.VERSION.SDK_INT < 33) navigate(false);
        else super.onBackPressed();
    }

    @Override protected void onActivityResult(int request, int result, Intent data) {
        super.onActivityResult(request, result, data);
        if (request == FILE_PICKER && fileResult != null) {
            fileResult.onReceiveValue(WebChromeClient.FileChooserParams.parseResult(result, data));
            fileResult = null;
        }
    }

    @Override protected void onSaveInstanceState(Bundle state) {
        web.saveState(state);
        state.putBoolean("authenticating", authenticating);
        super.onSaveInstanceState(state);
    }
    @Override protected void onPause() { web.onPause(); CookieManager.getInstance().flush(); super.onPause(); }
    @Override protected void onResume() { super.onResume(); if (web != null) web.onResume(); }
    @Override protected void onDestroy() {
        if (Build.VERSION.SDK_INT >= 33 && backCallback != null) getOnBackInvokedDispatcher().unregisterOnBackInvokedCallback(backCallback);
        if (fileResult != null) fileResult.onReceiveValue(null);
        web.destroy();
        super.onDestroy();
    }

}
