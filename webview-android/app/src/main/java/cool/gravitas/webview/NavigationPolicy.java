package cool.gravitas.webview;

import java.net.URI;

/** No JavaScript bridge or arbitrary intent URLs are exposed to hosted pages. */
final class NavigationPolicy {
    static boolean https(String raw) {
        try {
            URI uri = URI.create(raw);
            return "https".equalsIgnoreCase(uri.getScheme()) && uri.getHost() != null
                && uri.getRawUserInfo() == null;
        } catch (IllegalArgumentException error) { return false; }
    }

    static boolean sameOrigin(String raw, String origin) {
        if (!https(raw)) return false;
        URI uri = URI.create(raw), app = URI.create(origin);
        return app.getHost().equalsIgnoreCase(uri.getHost())
            && (uri.getPort() == -1 || uri.getPort() == 443);
    }

    static boolean authentication(String raw, String origin) {
        return sameOrigin(raw, origin) && URI.create(raw).getPath().startsWith("/auth/");
    }

    static boolean external(String raw) {
        if (https(raw)) return true;
        try {
            String scheme = URI.create(raw).getScheme();
            return "mailto".equalsIgnoreCase(scheme) || "tel".equalsIgnoreCase(scheme);
        } catch (IllegalArgumentException error) { return false; }
    }

    static boolean forwardGesture(float dx, float dy, float threshold) {
        return dx < -threshold && Math.abs(dx) > Math.abs(dy) * 1.5f;
    }
}
