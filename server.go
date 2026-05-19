package main

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
)

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

	// Copy headers
	for key, values := range resp.Header {
		for _, value := range values {
			w.Header().Set(key, value)
		}
	}

	// Set CORS headers to allow iframe embedding
	w.Header().Set("Access-Control-Allow-Origin", "*")
	w.Header().Set("X-Frame-Options", "ALLOWALL")

	// For HTML content, rewrite all URLs to go through proxy
	contentType := resp.Header.Get("Content-Type")
	if strings.Contains(contentType, "text/html") {
		bodyStr = string(body)

		// Inject JavaScript to rewrite URLs
		proxyScript := fmt.Sprintf(`
<script>
(function() {
	const proxyBase = '%s';
	const targetBase = '%s';
	
	// Rewrite all links to go through proxy
	document.querySelectorAll('a[href]').forEach(a => {
		try {
			const href = a.getAttribute('href');
			if (href && !href.startsWith('#') && !href.startsWith('javascript:') && !href.startsWith('mailto:')) {
				const absoluteUrl = new URL(href, targetBase).href;
				a.href = proxyBase + encodeURIComponent(absoluteUrl);
			}
		} catch(e) {}
	});
	
	// Rewrite all image sources
	document.querySelectorAll('img[src]').forEach(img => {
		try {
			const src = img.getAttribute('src');
			if (src && !src.startsWith('data:')) {
				const absoluteUrl = new URL(src, targetBase).href;
				img.src = proxyBase + encodeURIComponent(absoluteUrl);
			}
		} catch(e) {}
	});
	
	// Rewrite stylesheet links
	document.querySelectorAll('link[rel="stylesheet"][href]').forEach(link => {
		try {
			const href = link.getAttribute('href');
			if (href) {
				const absoluteUrl = new URL(href, targetBase).href;
				link.href = proxyBase + encodeURIComponent(absoluteUrl);
			}
		} catch(e) {}
	});
	
	// Rewrite script sources
	document.querySelectorAll('script[src]').forEach(script => {
		try {
			const src = script.getAttribute('src');
			if (src && !src.startsWith('http')) {
				const absoluteUrl = new URL(src, targetBase).href;
				script.src = proxyBase + encodeURIComponent(absoluteUrl);
			}
		} catch(e) {}
	});
})();
</script>
`, "/proxy?url=", targetURL)

		// Inject script before </body> or at end of <head>
		if strings.Contains(bodyStr, "</body>") {
			bodyStr = strings.Replace(bodyStr, "</body>", proxyScript+"</body>", 1)
		} else if strings.Contains(bodyStr, "</head>") {
			bodyStr = strings.Replace(bodyStr, "</head>", proxyScript+"</head>", 1)
		} else {
			bodyStr += proxyScript
		}

		body = []byte(bodyStr)
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
