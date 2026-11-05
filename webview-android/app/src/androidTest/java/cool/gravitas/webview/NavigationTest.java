package cool.gravitas.webview;

import android.net.Uri;
import android.os.Build;
import android.view.MotionEvent;
import android.view.InputDevice;
import android.webkit.WebResourceRequest;
import android.webkit.WebResourceResponse;
import android.webkit.WebView;
import android.webkit.WebViewClient;
import androidx.test.core.app.ActivityScenario;
import androidx.test.ext.junit.runners.AndroidJUnit4;
import androidx.test.platform.app.InstrumentationRegistry;
import androidx.test.uiautomator.UiDevice;
import java.io.ByteArrayInputStream;
import java.nio.charset.StandardCharsets;
import java.util.concurrent.atomic.AtomicBoolean;
import org.junit.Test;
import org.junit.runner.RunWith;
import static org.junit.Assert.*;

@RunWith(AndroidJUnit4.class)
public class NavigationTest {
    private ActivityScenario<MainActivity> launch() {
        android.content.Intent intent = new android.content.Intent(InstrumentationRegistry.getInstrumentation().getTargetContext(), MainActivity.class);
        intent.putExtra("skipInitialLoad", true);
        return ActivityScenario.launch(intent);
    }
    private void fixture(ActivityScenario<MainActivity> scenario) {
        scenario.onActivity(activity -> {
            activity.web.stopLoading();
            WebViewClient original = activity.web.getWebViewClient();
            activity.web.setWebViewClient(new WebViewClient() {
                @Override public WebResourceResponse shouldInterceptRequest(WebView view, WebResourceRequest request) {
                    String html = "<!doctype html><meta name='viewport' content='width=device-width'><title>Fixture</title>"
                        + "<button id='next' style='width:100%;height:80px' onclick=\"history.pushState({},'',this.dataset.path)\">Next destination</button>"
                        + "<h1>" + request.getUrl().getPath() + "</h1>";
                    return new WebResourceResponse("text/html", "UTF-8", new ByteArrayInputStream(html.getBytes(StandardCharsets.UTF_8)));
                }
                @Override public void onPageFinished(WebView view, String url) { original.onPageFinished(view, url); }
                @Override public void doUpdateVisitedHistory(WebView view, String url, boolean reload) { original.doUpdateVisitedHistory(view, url, reload); }
            });
            activity.web.loadUrl(BuildConfig.APP_ORIGIN + "/fixture/a");
        });
        awaitPath(scenario, "/fixture/a");
    }

    private void awaitPath(ActivityScenario<MainActivity> scenario, String path) {
        long deadline = System.currentTimeMillis() + 10000;
        AtomicBoolean matches = new AtomicBoolean();
        while (System.currentTimeMillis() < deadline) {
            scenario.onActivity(activity -> matches.set(activity.web.getProgress() == 100 && path.equals(Uri.parse(activity.web.getUrl()).getPath())));
            if (matches.get()) return;
            android.os.SystemClock.sleep(50);
        }
        fail("WebView did not reach " + path);
    }

    private void push(ActivityScenario<MainActivity> scenario, String path) {
        AtomicBoolean committed = new AtomicBoolean();
        // Chromium deliberately skips history entries made without user activation.
        // Set up the destination, then use a real touch just like the hosted SPA.
        scenario.onActivity(activity -> activity.web.evaluateJavascript("document.getElementById('next').dataset.path='" + path + "'", value -> committed.set(true)));
        for (int i = 0; i < 100 && !committed.get(); i++) android.os.SystemClock.sleep(50);
        assertTrue("Destination setup completed", committed.get());
        int[] position = new int[2], point = new int[2];
        scenario.onActivity(activity -> {
            activity.web.getLocationOnScreen(position);
            point[0] = position[0] + activity.web.getWidth() / 2;
            point[1] = position[1] + Math.round(40 * activity.getResources().getDisplayMetrics().density);
        });
        UiDevice.getInstance(InstrumentationRegistry.getInstrumentation()).click(point[0], point[1]);
        awaitPath(scenario, path);
        AtomicBoolean back = new AtomicBoolean();
        for (int i = 0; i < 100; i++) {
            scenario.onActivity(activity -> back.set(activity.web.canGoBack()));
            if (back.get()) return;
            android.os.SystemClock.sleep(50);
        }
        fail("New destination did not enter WebView history: " + path);
    }

    @Test public void historySupportsForwardAndDiscardsBranches() {
        try (ActivityScenario<MainActivity> scenario = launch()) {
            fixture(scenario);
            scenario.onActivity(activity -> { activity.navigate(true); assertFalse(activity.isFinishing()); });
            awaitPath(scenario, "/fixture/a");
            push(scenario, "/fixture/b");
            push(scenario, "/fixture/c");
            scenario.onActivity(activity -> activity.navigate(false));
            awaitPath(scenario, "/fixture/b");
            scenario.onActivity(activity -> activity.navigate(true));
            awaitPath(scenario, "/fixture/c");
            scenario.onActivity(activity -> activity.navigate(false));
            awaitPath(scenario, "/fixture/b");
            push(scenario, "/fixture/new");
            scenario.onActivity(activity -> assertFalse(activity.web.canGoForward()));
            scenario.recreate();
            scenario.onActivity(activity -> {
                assertEquals("/fixture/new", Uri.parse(activity.web.getUrl()).getPath());
                assertTrue(activity.web.canGoBack());
            });
        }
    }

    @Test public void cancelledPredictiveGestureDoesNotNavigate() {
        if (Build.VERSION.SDK_INT < 34) return;
        try (ActivityScenario<MainActivity> scenario = launch()) {
            fixture(scenario);
            push(scenario, "/fixture/b");
            UiDevice device = UiDevice.getInstance(InstrumentationRegistry.getInstrumentation());
            long time = android.os.SystemClock.uptimeMillis();
            int x = device.getDisplayWidth() - 1, y = device.getDisplayHeight() / 2;
            for (int action : new int[]{MotionEvent.ACTION_DOWN, MotionEvent.ACTION_MOVE, MotionEvent.ACTION_CANCEL}) {
                MotionEvent event = MotionEvent.obtain(time, android.os.SystemClock.uptimeMillis(), action,
                    action == MotionEvent.ACTION_DOWN ? x : x - 30, y, 0);
                event.setSource(InputDevice.SOURCE_TOUCHSCREEN);
                InstrumentationRegistry.getInstrumentation().getUiAutomation().injectInputEvent(event, true);
                event.recycle();
            }
            awaitPath(scenario, "/fixture/b");
        }
    }

    @Test public void leftAndRightSystemGesturesNavigateRealWebHistory() {
        if (Build.VERSION.SDK_INT < 34) return;
        UiDevice device = UiDevice.getInstance(InstrumentationRegistry.getInstrumentation());
        try (ActivityScenario<MainActivity> scenario = launch()) {
            fixture(scenario);
            push(scenario, "/fixture/b");
            int width = device.getDisplayWidth(), y = device.getDisplayHeight() / 2;
            device.swipe(1, y, width / 2, y, 30);
            awaitPath(scenario, "/fixture/a");
            device.swipe(width - 1, y, width / 2, y, 30);
            awaitPath(scenario, "/fixture/b");
            device.swipe(width - 1, y, width / 2, y, 30);
            awaitPath(scenario, "/fixture/b");
            scenario.onActivity(activity -> assertFalse(activity.isFinishing()));
        }
    }

    @Test public void packagesKeepIndependentStorageAndHardenedSettings() {
        try (ActivityScenario<MainActivity> scenario = launch()) {
            scenario.onActivity(activity -> {
                assertEquals("cool.gravitas." + BuildConfig.FLAVOR, activity.getPackageName());
                assertTrue(activity.getDataDir().getAbsolutePath().contains(activity.getPackageName()));
                assertFalse(activity.web.getSettings().getAllowFileAccess());
                assertEquals(android.webkit.WebSettings.MIXED_CONTENT_NEVER_ALLOW, activity.web.getSettings().getMixedContentMode());
                assertFalse(android.webkit.CookieManager.getInstance().acceptThirdPartyCookies(activity.web));
            });
        }
    }

    @Test public void hardwareBackRetainsBackBehavior() {
        UiDevice device = UiDevice.getInstance(InstrumentationRegistry.getInstrumentation());
        try (ActivityScenario<MainActivity> scenario = launch()) {
            fixture(scenario);
            push(scenario, "/fixture/b");
            device.pressBack();
            awaitPath(scenario, "/fixture/a");
        }
    }

    @Test public void logoutDropsPrivateForwardHistory() {
        try (ActivityScenario<MainActivity> scenario = launch()) {
            fixture(scenario);
            push(scenario, "/private/one");
            push(scenario, "/private/two");
            scenario.onActivity(activity -> activity.navigate(false));
            awaitPath(scenario, "/private/one");
            scenario.onActivity(activity -> activity.web.evaluateJavascript("history.replaceState({}, '', '/login?auto=off')", null));
            awaitPath(scenario, "/login");
            scenario.onActivity(activity -> {
                assertFalse(activity.web.canGoBack());
                assertFalse(activity.web.canGoForward());
                activity.navigate(true);
                assertFalse(activity.isFinishing());
            });
        }
    }

    @Test public void modalBlocksForwardAndBackCancelsBeforeNavigating() {
        try (ActivityScenario<MainActivity> scenario = launch()) {
            fixture(scenario);
            push(scenario, "/fixture/b");
            scenario.onActivity(activity -> activity.navigate(false));
            awaitPath(scenario, "/fixture/a");
            scenario.onActivity(activity -> activity.web.evaluateJavascript("const d=document.createElement('dialog');d.textContent='Modal';document.body.append(d);d.showModal();", null));
            InstrumentationRegistry.getInstrumentation().waitForIdleSync();
            scenario.onActivity(activity -> activity.navigate(true));
            android.os.SystemClock.sleep(300);
            awaitPath(scenario, "/fixture/a");
            scenario.onActivity(activity -> activity.navigate(false));
            android.os.SystemClock.sleep(300);
            awaitPath(scenario, "/fixture/a");
            scenario.onActivity(activity -> activity.navigate(true));
            awaitPath(scenario, "/fixture/b");
        }
    }

    @Test public void keyboardDismissalPrecedesHardwareBack() {
        if (Build.VERSION.SDK_INT < 30) return;
        UiDevice device = UiDevice.getInstance(InstrumentationRegistry.getInstrumentation());
        try (ActivityScenario<MainActivity> scenario = launch()) {
            fixture(scenario);
            push(scenario, "/fixture/b");
            scenario.onActivity(activity -> {
                android.widget.EditText input = new android.widget.EditText(activity);
                ((android.widget.LinearLayout) activity.web.getParent()).addView(input, 0);
                input.requestFocus();
                activity.getSystemService(android.view.inputmethod.InputMethodManager.class).showSoftInput(input, 0);
            });
            AtomicBoolean visible = new AtomicBoolean();
            for (int i = 0; i < 100; i++) {
                scenario.onActivity(activity -> visible.set(activity.getWindow().getDecorView().getRootWindowInsets().isVisible(android.view.WindowInsets.Type.ime())));
                if (visible.get()) break;
                android.os.SystemClock.sleep(50);
            }
            assertTrue("Test keyboard must be visible", visible.get());
            device.pressBack();
            android.os.SystemClock.sleep(300);
            awaitPath(scenario, "/fixture/b");
        }
    }
}
