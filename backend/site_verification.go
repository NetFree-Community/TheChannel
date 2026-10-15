package main

import "net/http"

// googleVerificationFiles are the ownership files Google Search Console
// asked to find at the site root ("HTML file" method). Each answers with
// exactly the line Google expects. Listed one by one on purpose: answering
// any google*.html would let anyone verify this site as their own.
var googleVerificationFiles = map[string]string{
	"/google8be09fcc16689402.html": "google-site-verification: google8be09fcc16689402.html",
}

func serveGoogleVerificationFile(w http.ResponseWriter, r *http.Request) {
	body, ok := googleVerificationFiles[r.URL.Path]
	if !ok {
		http.NotFound(w, r)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "public, max-age=3600")
	w.Write([]byte(body))
}
