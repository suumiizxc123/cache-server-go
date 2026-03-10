vcl 4.1;

# ═══════════════════════════════════════════════════════
#  Varnish — RAM Cache for Binary Assets
#
#  Flow:
#    Client → HAProxy → Varnish(:6081) → Nginx → Go/MinIO
#
#  Cache policy:
#    /assets/*  → 7 days in RAM (video, images, fonts)
#    /cache/*   → pass to Go server (dynamic)
#    /upload/*  → pass (no cache)
# ═══════════════════════════════════════════════════════

backend nginx {
    .host = "nginx";
    .port = "80";
    .connect_timeout = 5s;
    .first_byte_timeout = 30s;
    .between_bytes_timeout = 10s;
    .max_connections = 1000;

    .probe = {
        .url = "/health";
        .timeout = 3s;
        .interval = 5s;
        .window = 5;
        .threshold = 3;
    }
}

sub vcl_recv {
    set req.backend_hint = nginx;

    # ── PURGE support ──
    if (req.method == "PURGE") {
        return (purge);
    }

    # ── Skip cache for uploads and dynamic API ──
    if (req.url ~ "^/upload/" || req.method != "GET") {
        return (pass);
    }

    # ── Cache binary assets aggressively ──
    if (req.url ~ "^/assets/") {
        unset req.http.Cookie;
        return (hash);
    }

    # ── Pass everything else (API, stats, meta) ──
    return (pass);
}

sub vcl_backend_response {
    # ── Binary assets: cache 7 days in RAM ──
    if (bereq.url ~ "^/assets/") {
        set beresp.ttl = 7d;
        set beresp.grace = 1h;
        unset beresp.http.Set-Cookie;
        unset beresp.http.Vary;
        set beresp.http.Cache-Control = "public, max-age=604800";

        # Always stream large binary content
        set beresp.do_stream = true;
    }

    # Don't cache error responses
    if (beresp.status >= 400) {
        set beresp.ttl = 0s;
        set beresp.uncacheable = true;
        return (deliver);
    }
}

sub vcl_deliver {
    # ── Add cache status header ──
    if (obj.hits > 0) {
        set resp.http.X-Cache = "HIT";
        set resp.http.X-Cache-Hits = obj.hits;
    } else {
        set resp.http.X-Cache = "MISS";
    }
}

sub vcl_hash {
    hash_data(req.url);
    return (lookup);
}
