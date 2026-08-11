package main

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strings"
)

// Headers that must not be forwarded from the upstream response.
// CSP/X-Frame-Options would block resources and framing; Content-Length/
// Content-Encoding are stale after we rewrite the body.
var skipHeaders = map[string]bool{
	"Connection":                          true,
	"Transfer-Encoding":                   true,
	"Content-Length":                      true,
	"Content-Encoding":                    true,
	"Content-Security-Policy":             true,
	"Content-Security-Policy-Report-Only": true,
	"X-Frame-Options":                     true,
	"Strict-Transport-Security":           true,
}

var (
	htmlAttrRe  = regexp.MustCompile(`(?i)\b(href|src|action|poster)\s*=\s*(["'])([^"']*)["']`)
	srcsetRe    = regexp.MustCompile(`(?i)\bsrcset\s*=\s*(["'])([^"']*)["']`)
	styleAttrRe = regexp.MustCompile(`(?i)\bstyle\s*=\s*(["'])([^"']*)["']`)
	cssURLRe    = regexp.MustCompile(`url\(\s*(['"]?)([^'")\s]+)['"]?\s*\)`)
	cssImportRe = regexp.MustCompile(`@import\s+(["'])([^"']+)["']`)
)

// proxify converts a URL found in proxied content into a /proxy?url= URL,
// resolving relative URLs against the page's base URL.
func proxify(raw string, base *url.URL) string {
	low := strings.ToLower(strings.TrimSpace(raw))
	if raw == "" || strings.HasPrefix(low, "#") || strings.HasPrefix(low, "javascript:") ||
		strings.HasPrefix(low, "mailto:") || strings.HasPrefix(low, "data:") ||
		strings.HasPrefix(low, "tel:") || strings.HasPrefix(low, "/proxy") {
		return raw
	}
	abs, err := base.Parse(raw)
	if err != nil {
		return raw
	}
	return "/proxy?url=" + url.QueryEscape(abs.String())
}

// rewriteHTML rewrites link/resource URLs in HTML so they load through the proxy.
func rewriteHTML(body string, base *url.URL) string {
	body = htmlAttrRe.ReplaceAllStringFunc(body, func(m string) string {
		p := htmlAttrRe.FindStringSubmatch(m)
		return p[1] + "=" + p[2] + proxify(p[3], base) + p[2]
	})
	body = srcsetRe.ReplaceAllStringFunc(body, func(m string) string {
		p := srcsetRe.FindStringSubmatch(m)
		entries := strings.Split(p[2], ",")
		for i, e := range entries {
			fields := strings.Fields(strings.TrimSpace(e))
			if len(fields) > 0 {
				fields[0] = proxify(fields[0], base)
				entries[i] = strings.Join(fields, " ")
			}
		}
		return "srcset=" + p[1] + strings.Join(entries, ", ") + p[1]
	})
	// Rewrite url() references inside inline style attributes
	body = styleAttrRe.ReplaceAllStringFunc(body, func(m string) string {
		p := styleAttrRe.FindStringSubmatch(m)
		return "style=" + p[1] + rewriteCSSURLs(p[2], base) + p[1]
	})
	return body
}

// rewriteCSSURLs rewrites only url() references (used for inline styles).
func rewriteCSSURLs(css string, base *url.URL) string {
	return cssURLRe.ReplaceAllStringFunc(css, func(m string) string {
		p := cssURLRe.FindStringSubmatch(m)
		return "url(" + p[1] + proxify(p[2], base) + p[1] + ")"
	})
}

// rewriteCSS rewrites url() and @import references in CSS so images, fonts
// and nested stylesheets load through the proxy.
func rewriteCSS(css string, base *url.URL) string {
	css = cssURLRe.ReplaceAllStringFunc(css, func(m string) string {
		p := cssURLRe.FindStringSubmatch(m)
		return "url(" + p[1] + proxify(p[2], base) + p[1] + ")"
	})
	css = cssImportRe.ReplaceAllStringFunc(css, func(m string) string {
		p := cssImportRe.FindStringSubmatch(m)
		return "@import " + p[1] + proxify(p[2], base) + p[1]
	})
	return css
}

// startWebServer starts the web server for the browser UI
func startWebServer() {
	http.HandleFunc("/", handleHomePage)
	http.HandleFunc("/api/status", handleStatusAPI)
	http.HandleFunc("/api/fetch", handleFetchAPI)
	// Proxy endpoint for direct content serving
	http.HandleFunc("/proxy/", handleProxy)

	addr := ":" + BROWSER_PORT
	fmt.Printf("🚀 Web server starting on http://127.0.0.1%s\n", addr)

	if err := http.ListenAndServe(addr, nil); err != nil {
		fmt.Printf("❌ Failed to start web server: %v\n", err)
	}
}

// handleHomePage serves the main HTML page
func handleHomePage(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/" {
		http.NotFound(w, r)
		return
	}

	htmlContent, err := browserHTML.ReadFile("browser.html")
	if err != nil {
		http.Error(w, "Failed to load page", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Write(htmlContent)
}

// handleStatusAPI returns I2P router status
func handleStatusAPI(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")

	status := map[string]interface{}{
		"running": isI2PRunning(),
	}

	json.NewEncoder(w).Encode(status)
}

// handleFetchAPI fetches content from I2P or regular websites
func handleFetchAPI(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")

	urlStr := r.URL.Query().Get("url")
	if urlStr == "" {
		w.WriteHeader(http.StatusBadRequest)
		json.NewEncoder(w).Encode(map[string]interface{}{
			"error": "URL parameter is required",
		})
		return
	}

	// Process URL
	if !strings.HasPrefix(urlStr, "http://") && !strings.HasPrefix(urlStr, "https://") {
		if strings.Contains(urlStr, ".i2p") || strings.Contains(urlStr, ".") {
			urlStr = "http://" + urlStr
		} else {
			urlStr = "https://duckduckgo.com/?q=" + url.QueryEscape(urlStr)
		}
	}

	// Fetch the page
	client := createHTTPClient(urlStr)
	resp, err := client.Get(urlStr)

	if err != nil {
		errorMsg := err.Error()
		tips := []string{}

		if strings.Contains(urlStr, ".i2p") {
			tips = append(tips, "I2P sites may take 1-3 minutes to load")
			tips = append(tips, "The site might be offline or not exist")
			tips = append(tips, "Check if the address is correct")
			tips = append(tips, "Try visiting http://stats.i2p/ to see available sites")
			tips = append(tips, "Your I2P router may need more peers")
		}

		w.WriteHeader(http.StatusOK)
		json.NewEncoder(w).Encode(map[string]interface{}{
			"error": errorMsg,
			"tips":  tips,
		})
		return
	}
	defer resp.Body.Close()

	// Read content
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		w.WriteHeader(http.StatusOK)
		json.NewEncoder(w).Encode(map[string]interface{}{
			"error": "Failed to read response: " + err.Error(),
		})
		return
	}

	content := string(body)
	// Increase limit to 500KB for full pages
	maxLen := 500 * 1024
	truncated := false
	if len(content) > maxLen {
		content = content[:maxLen]
		truncated = true
	}

	// Get content type from response
	contentType := resp.Header.Get("Content-Type")

	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(map[string]interface{}{
		"content":     content,
		"status":      resp.Status,
		"url":         urlStr,
		"contentType": contentType,
		"truncated":   truncated,
	})
}

// handleProxy serves as a reverse proxy for I2P websites
func handleProxy(w http.ResponseWriter, r *http.Request) {
	// Get URL from query parameter
	rawURL := r.URL.Query().Get("url")
	if rawURL == "" {
		// Fallback: try to get from path
		rawURL = strings.TrimPrefix(r.URL.Path, "/proxy/")
	}

	if rawURL == "" {
		http.Error(w, "Missing URL parameter", http.StatusBadRequest)
		return
	}

	fmt.Printf("🔍 Raw URL from query: %s\n", rawURL)

	// URL decode (query parameters are automatically decoded by Go)
	targetURL := rawURL

	fmt.Printf("🔓 Decoded URL: %s\n", targetURL)

	// Add protocol if missing
	if !strings.HasPrefix(targetURL, "http://") && !strings.HasPrefix(targetURL, "https://") {
		targetURL = "http://" + targetURL
		fmt.Printf("➕ Added protocol: %s\n", targetURL)
	}

	fmt.Printf("📡 Proxying request to: %s\n", targetURL)

	// Fetch the content using I2P proxy
	client := createHTTPClient(targetURL)
	resp, err := client.Get(targetURL)
	if err != nil {
		fmt.Printf("❌ Proxy error: %v\n", err)
		// Return a friendly error page
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.WriteHeader(http.StatusBadGateway)
		fmt.Fprintf(w, `<!DOCTYPE html><html><head><style>
			body { font-family: Arial, sans-serif; background: #1a1d2e; color: #e0e0e0; padding: 50px; text-align: center; }
			.error-box { background: rgba(255,68,68,0.1); border: 1px solid #ff4444; border-radius: 10px; padding: 30px; max-width: 600px; margin: 0 auto; }
			h1 { color: #ff4444; }
			.tips { background: rgba(0,204,255,0.1); border: 1px solid #00ccff; border-radius: 5px; padding: 15px; margin-top: 20px; text-align: left; }
			.tips li { padding: 5px 0; color: #888; list-style: none; }
		</style></head><body>
		<div class="error-box">
			<h1>❌ 无法访问网站</h1>
			<p>%s</p>
			<div class="tips">
				<h3>💡 可能的原因:</h3>
				<ul>
					<li>• 该网站可能不存在或已离线</li>
					<li>• I2P地址拼写错误</li>
					<li>• I2P路由器需要更多时间建立连接</li>
					<li>• 您的I2P网络peer数量不足</li>
				</ul>
				<h3>🔧 建议:</h3>
				<ul>
					<li>• 先访问 <a href="/proxy/http://stats.i2p" style="color: #00ff88;">stats.i2p</a> 测试I2P连接</li>
					<li>• 等待几分钟后重试</li>
					<li>• 检查I2P路由器控制台确认网络状态</li>
				</ul>
			</div>
		</div>
		</body></html>`, escapeHTML(err.Error()))
		return
	}
	defer resp.Body.Close()

	fmt.Printf("✅ Response received: %s\n", resp.Status)

	// Read response body to check for I2P error pages
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		http.Error(w, "Failed to read response", http.StatusInternalServerError)
		return
	}

	// Check if this is an I2P error page
	bodyStr := string(body)
	isI2PError := strings.Contains(bodyStr, "非 I2P 站点") ||
		strings.Contains(bodyStr, "not an I2P site") ||
		strings.Contains(bodyStr, "Website not found") ||
		(resp.StatusCode >= 400 && resp.StatusCode < 600)

	if isI2PError && strings.Contains(targetURL, ".i2p") {
		// Return custom error page for I2P sites
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.WriteHeader(http.StatusOK)
		fmt.Fprintf(w, `<!DOCTYPE html><html><head><style>
			body { font-family: Arial, sans-serif; background: #1a1d2e; color: #e0e0e0; padding: 50px; text-align: center; }
			.error-box { background: rgba(255,68,68,0.1); border: 1px solid #ff4444; border-radius: 10px; padding: 30px; max-width: 600px; margin: 0 auto; }
			h1 { color: #ff4444; }
			.tips { background: rgba(0,204,255,0.1); border: 1px solid #00ccff; border-radius: 5px; padding: 15px; margin-top: 20px; text-align: left; }
			.tips li { padding: 5px 0; color: #888; list-style: none; }
		</style></head><body>
		<div class="error-box">
			<h1>❌ 无法访问 I2P 网站</h1>
			<p>您尝试连接的网站 <strong>%s</strong> 可能不存在或当前位置不可用。</p>
			<div class="tips">
				<h3>💡 可能的原因:</h3>
				<ul>
					<li>• 该I2P网站已下线或暂时不可用</li>
					<li>• I2P地址拼写错误</li>
					<li>• I2P网络需要更多时间建立隧道</li>
					<li>• 您的I2P路由器peer数量不足</li>
				</ul>
				<h3>🔧 建议:</h3>
				<ul>
					<li>• 先访问 <a href="/proxy/http://stats.i2p" style="color: #00ff88;">stats.i2p</a> 测试I2P连接</li>
					<li>• 等待5-10分钟后重试</li>
					<li>• 在I2P路由器控制台(http://127.0.0.1:7657)检查网络状态</li>
					<li>• 尝试其他知名的I2P网站验证连接</li>
				</ul>
			</div>
		</div>
		</body></html>`, targetURL)
		return
	}

	// Copy headers, skipping ones that would break framing or stale after rewriting
	for key, values := range resp.Header {
		if skipHeaders[http.CanonicalHeaderKey(key)] {
			continue
		}
		for _, value := range values {
			w.Header().Set(key, value)
		}
	}

	// Set CORS headers to allow iframe embedding
	w.Header().Set("Access-Control-Allow-Origin", "*")

	// Rewrite URLs in HTML/CSS content so all resources load through the proxy
	contentType := resp.Header.Get("Content-Type")
	base, _ := url.Parse(targetURL)

	if strings.Contains(contentType, "text/css") {
		body = []byte(rewriteCSS(string(body), base))
	} else if strings.Contains(contentType, "text/html") {
		// URLs are already rewritten server-side; no JS injection needed.
		body = []byte(rewriteHTML(string(body), base))
	}

	// Set status code
	w.WriteHeader(resp.StatusCode)

	// Write body
	w.Write(body)
}

// escapeHTML escapes HTML special characters
func escapeHTML(s string) string {
	s = strings.ReplaceAll(s, "&", "&amp;")
	s = strings.ReplaceAll(s, "<", "&lt;")
	s = strings.ReplaceAll(s, ">", "&gt;")
	s = strings.ReplaceAll(s, "\"", "&quot;")
	return s
}
