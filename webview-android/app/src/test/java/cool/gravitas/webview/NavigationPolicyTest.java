package cool.gravitas.webview;
import org.junit.Test;
import static org.junit.Assert.*;

public class NavigationPolicyTest {
    @Test public void originCannotBeSpoofed() {
        String app = "https://work.gravitas.cool";
        assertTrue(NavigationPolicy.sameOrigin(app + "/documents/one", app));
        assertTrue(NavigationPolicy.sameOrigin(app + ":443/auth/gravitas/start", app));
        for (String url : new String[]{"http://work.gravitas.cool", "https://work.gravitas.cool.evil.org", "https://work.gravitas.cool@evil.org", "https://work.gravitas.cool:444", "file:///etc/passwd", "javascript:alert(1)", "intent://evil", "https://["})
            assertFalse(url, NavigationPolicy.sameOrigin(url, app));
    }
    @Test public void onlySafeExternalSchemes() {
        assertTrue(NavigationPolicy.external("https://example.org/article"));
        assertTrue(NavigationPolicy.external("mailto:a@example.org"));
        assertTrue(NavigationPolicy.external("tel:+12345"));
        assertFalse(NavigationPolicy.external("intent://example.org"));
        assertFalse(NavigationPolicy.external("content://private/data"));
        assertFalse(NavigationPolicy.external("javascript:alert(1)"));
    }
    @Test public void forwardRequiresAnInwardHorizontalSwipe() {
        assertTrue(NavigationPolicy.forwardGesture(-80, 10, 64));
        assertFalse(NavigationPolicy.forwardGesture(80, 0, 64));
        assertFalse(NavigationPolicy.forwardGesture(-20, 0, 64));
        assertFalse(NavigationPolicy.forwardGesture(-80, 100, 64));
    }
}
