const PROXY_CONFIG = {
    "/api/**": {
        "target": "http://127.0.0.1:3000",
        "changeOrigin": true,
        "ws": true,
        "cookieDomainRewrite": ""
    },
    "/auth/**": {
        "target": "http://127.0.1:3000",
        "changeOrigin": true,
        "ws": true,
        "cookieDomainRewrite": ""
    },
    "/import/**": {
        "target": "http://127.0.1:3000",
        "changeOrigin": true,
        "ws": true,
        "cookieDomainRewrite": ""
    },
    "/assets/favicon.ico": {
        "target": "http://127.0.1:3000",
        "changeOrigin": true,
        "ws": true,
        "cookieDomainRewrite": ""
    },
    "/firebase-messaging-sw.js": {
        "target": "http://127.0.1:3000",
        "changeOrigin": true,
        "ws": true,
        "cookieDomainRewrite": ""
    },
};

module.exports = PROXY_CONFIG;